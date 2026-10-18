import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import {
  normalizeVersion, maximumMacOS, minimumFromMachO, validateAppMetadata, validatePublication,
} from "./release-artifact.mjs";
import { releasePayload, publishRelease } from "./release-publisher.mjs";
import { sparkleConfig, validateSparkle, validateSparkleApp, frameworkFiles, verifyUpdateSignature } from "./sparkle.mjs";

test("release versions accept only complete numeric releases", () => {
  for (const version of ["0.2.1", "1.0.0", "v2.3.4"]) {
    assert.equal(normalizeVersion(version), version.replace(/^v/, ""));
  }
  for (const version of ["", "0.2.0garbage", "0.2", "01.2.3", "0.2.3-beta", "0.2.3+meta",
    "0. 2.3", "0.2.3/../x", "0.2.3\nother"]) {
    assert.throws(() => normalizeVersion(version), /complete/);
  }
});

test("macOS floor follows every Mach-O slice and required sidecar, not the SDK", () => {
  assert.equal(minimumFromMachO("cmd LC_BUILD_VERSION\n cmdsize 32\n platform 1\n minos 11.0\n sdk 26.5\n"), "11.0");
  assert.equal(minimumFromMachO("cmd LC_BUILD_VERSION\n minos 11.0\ncmd LC_BUILD_VERSION\n minos 14.0\n"), "14.0");
  assert.equal(minimumFromMachO("cmd LC_VERSION_MIN_MACOSX\n cmdsize 16\n version 10.15\n sdk 14.0"), "10.15");
  assert.equal(maximumMacOS(["13.0", "11.0", "13.2.1"]), "13.2.1");
  assert.throws(() => minimumFromMachO("sdk 26.5"));
});

test("artifact metadata rejects wrong releases, wrong bundle and insufficient OS requirements", () => {
  const info = { CFBundleIdentifier: "ai.hoxi.desktop", CFBundleVersion: "0.2.1",
    CFBundleShortVersionString: "0.2.1", LSMinimumSystemVersion: "13.0",
    CFBundleURLTypes: [{ CFBundleURLSchemes: ["hoxi"] }] };
  validateAppMetadata(info, "0.2.1", "13.0");
  assert.throws(() => validateAppMetadata({ ...info, CFBundleURLTypes: undefined }, "0.2.1", "13.0"), /URL scheme/);
  assert.throws(() => validateAppMetadata({ ...info, CFBundleURLTypes: [{ CFBundleURLSchemes: ["other"] }] }, "0.2.1", "13.0"), /URL scheme/);
  assert.throws(() => validateAppMetadata(info, "0.2.2", "13.0"), /display version/);
  assert.throws(() => validateAppMetadata({ ...info, CFBundleVersion: "0.2.0" }, "0.2.1", "13.0"), /build version/);
  assert.throws(() => validateAppMetadata({ ...info, CFBundleIdentifier: "ai.hoxi.desktop.dev" }, "0.2.1", "13.0"));
  assert.throws(() => validateAppMetadata({ ...info, LSMinimumSystemVersion: "10.13" }, "0.2.1", "13.0"), /minimum macOS/);
});

test("publication requires explicit success; skipped and dry-run cannot become releases", () => {
  for (const result of [{ publication: "skipped" }, {}, { success: false, mode: "api" },
    { success: true, mode: "build" }, { success: true, mode: "api", dryRun: true },
    { success: true, mode: "db", publication: "skipped" }]) {
    assert.throws(() => validatePublication(result));
  }
  validatePublication({ success: true, mode: "api" });
  validatePublication({ success: true, mode: "db" });
});

const payload = releasePayload({ version: "0.2.1", notes: "Release test", sha256: "a".repeat(64), size: 1234 });

test("tracked publisher sends verified artifact metadata, keeps credentials out of URLs and rejects redirects", async () => {
  const result = await publishRelease(payload, { key: "test-secret", url: "http://127.0.0.1:1234/publish",
    fetchImpl: async (url, init) => {
      assert.equal(url.pathname, "/publish");
      assert.equal(url.search, "");
      assert.equal(init.headers.Authorization, "Bearer test-secret");
      assert.equal(init.redirect, "error");
      const body = JSON.parse(init.body);
      assert.equal(body.version, "0.2.1");
      assert.equal(body.sha256, "a".repeat(64));
      assert.equal(body.size, 1234);
      assert.equal(body.dryRun, false);
      assert.ok(body.mirrors["mac-arm64"].global.endsWith("/v0.2.1/Hoxi-0.2.1-darwin-arm64.dmg"));
      return Response.json({ success: true, data: { version: "0.2.1" } });
    } });
  assert.equal(result.mode, "api");
  assert.equal(result.success, true);
});

test("publisher rejects HTTP errors, non-confirmations and confirmations for a different version", async () => {
  for (const body of [{}, { success: false }, { success: false, code: 0 },
    { success: true, dryRun: true }, { success: true, data: { dryRun: true } },
    { success: true, data: { version: "0.2.0" } }, { success: true, publication: "skipped" }]) {
    await assert.rejects(publishRelease(payload, { key: "test", fetchImpl: async () => Response.json(body) }));
  }
  await assert.rejects(publishRelease(payload, { key: "test",
    fetchImpl: async () => Response.json({ success: true }, { status: 403 }) }), /HTTP 403/);
  await assert.rejects(publishRelease(payload, { key: "" }), /HOXI_RELEASE_KEY/);
  await assert.rejects(publishRelease(payload, { key: "test", url: "http://external.example/publish" }), /HTTPS/);
  await assert.rejects(publishRelease(payload, { key: "test", url: "https://secret@example.com/publish" }), /Credentials/);
  assert.equal((await publishRelease(payload, { key: "test",
    fetchImpl: async () => Response.json({ code: 0, data: { version: "0.2.1" } }) })).success, true);
});

// Test shell routing in a disposable project. Native DMG verification is exercised
// separately against a real packaged app; this fixture isolates orchestration.
const scripts = path.dirname(fileURLToPath(import.meta.url));

test("tag workflow gates publication on tests, versioned artifacts and notarization", () => {
  const workflow = fs.readFileSync(path.join(scripts, "../.github/workflows/release.yml"), "utf8");
  const required = [
    'version "$RELEASE_TAG"',
    "npm --prefix web run lint",
    "npm --prefix web test",
    "node --test scripts/release.test.mjs internal/opencode/browser_plugin_test.mjs",
    "npm --prefix web run build",
    "go vet ./...",
    "go test -race -count=1 ./...",
    "go build -tags desktop ./...",
    "- name: Import Apple Developer Certificate",
    "security list-keychains -d user -s",
    "- name: Setup Apple Notarization Profile",
    '--version "$RELEASE_VERSION" --notarize --notary-profile "hoxi-notary"',
    'verify-dmg "$DMG" "$RELEASE_VERSION"',
    'verify-distribution "$DMG"',
    "shasum -a 256",
    "uses: softprops/action-gh-release@v2",
    "build/bin/Hoxi-${{ env.RELEASE_VERSION }}-darwin-arm64.dmg",
    "fail_on_unmatched_files: true",
    "if: always()",
    'security delete-keychain "$KEYCHAIN_PATH"',
  ];
  let position = -1;
  for (const gate of required) {
    const next = workflow.indexOf(gate, position + 1);
    assert.ok(next > position, `Missing or out-of-order release gate: ${gate}`);
    position = next;
  }
  assert.doesNotMatch(workflow, /Optional|CAN_NOTARIZE|\|\| true|build\/bin\/\*\.dmg/);
  assert.equal((workflow.match(/^\s+if:/gm) || []).length, 1, "Only cleanup may be conditional");
  for (const secret of ["MACOS_CERTIFICATE", "MACOS_CERTIFICATE_PWD", "APPLE_ID",
    "APPLE_APP_SPECIFIC_PASSWORD", "APPLE_TEAM_ID"]) {
    assert.ok(workflow.includes(`\${${secret}:?`), `Missing required credential: ${secret}`);
  }
  for (const block of workflow.matchAll(/^        run: \|\n((?:          .*\n|\n)+)/gm)) {
    const shell = block[1].replace(/^          /gm, "");
    const result = spawnSync("bash", ["-n"], { input: shell, encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
  }
});

function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-release-test-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.mkdirSync(path.join(root, "scripts"));
  fs.mkdirSync(path.join(root, "bin"));
  fs.mkdirSync(path.join(root, "build/bin"), { recursive: true });
  fs.copyFileSync(path.join(scripts, "ai-release.sh"), path.join(root, "scripts/ai-release.sh"));
  fs.copyFileSync(path.join(scripts, "release-artifact.mjs"), path.join(root, "scripts/policy.mjs"));
  fs.copyFileSync(path.join(scripts, "sparkle.mjs"), path.join(root, "scripts/sparkle.mjs"));
  fs.writeFileSync(path.join(root, "scripts/release-artifact.mjs"), `
import fs from 'node:fs';
import crypto from 'node:crypto';
import {normalizeVersion, validatePublication} from './policy.mjs';
export {normalizeVersion};
const [command, file, version] = process.argv.slice(2);
try {
  if (command === 'version') console.log(normalizeVersion(file));
  else if (command === 'verify-dmg') {
    const bytes=fs.readFileSync(file);const data=JSON.parse(bytes);
    if (data.version !== version) throw new Error('version mismatch');
    console.log(JSON.stringify({version,sha256:crypto.createHash('sha256').update(bytes).digest('hex'),size:bytes.length}));
  } else if (command === 'verify-distribution') {
    if (process.env.TEST_ADHOC) throw new Error('Developer ID required');
  } else if (command === 'verify-publication') console.log(JSON.stringify(validatePublication(JSON.parse(fs.readFileSync(0,'utf8')))));
  else throw new Error('Unexpected command');
} catch(error) {console.error(error.message);process.exitCode=1;}
`);
  for (const tool of ["npm", "go", "git"]) {
    fs.writeFileSync(path.join(root, "bin", tool), `#!/bin/sh\nprintf '%s\\n' '${tool}' >> "$TEST_CALLS"\nexit 0\n`, { mode: 0o755 });
  }
  fs.writeFileSync(path.join(root, "build-desktop.sh"), `#!/bin/bash
printf '%s\\n' "$*" >> "$TEST_CALLS"
while [[ $# -gt 0 ]]; do if [[ "$1" == --version ]]; then version="$2"; shift; fi; shift; done
printf '{"version":"%s"}' "$version" > build/bin/Hoxi.dmg
`, { mode: 0o755 });
  const publisher = path.join(root, "scripts/release-publisher.mjs");
  fs.writeFileSync(publisher, `import fs from 'node:fs';fs.appendFileSync(process.env.TEST_CALLS,'publisher\\n');console.log(process.env.TEST_PUBLISH_RESULT || '{"success":true,"mode":"api"}');`);
  const calls = path.join(root, "calls");
  const env = { ...process.env, PATH: path.join(root, "bin") + path.delimiter + process.env.PATH,
    HOXI_RELEASE_KEY: "fixture-key", HOXI_RELEASE_API_URL: "", TEST_CALLS: calls };
  delete env.DATABASE_URL;
  for (const key of Object.keys(env)) if (key.startsWith("HOXI_SPARKLE_")) delete env[key];
  return {
    root, env, publisher,
    calls: () => fs.existsSync(calls) ? fs.readFileSync(calls, "utf8") : "",
    dmg: (version, name = "Hoxi.dmg") => fs.writeFileSync(path.join(root, "build/bin", name), JSON.stringify({ version })),
    run: (args, overrides = {}) => spawnSync("bash", ["scripts/ai-release.sh", ...args],
      { cwd: root, env: { ...env, ...overrides }, encoding: "utf8", timeout: 30_000 }),
  };
}

test("invalid versions and missing publisher/credentials stop before building or tagging", (t) => {
  const f = fixture(t);
  assert.notEqual(f.run(["0.2.0garbage", "--tag"]).status, 0);
  assert.notEqual(f.run(["--version"]).status, 0);
  assert.notEqual(f.run(["0.2.1"], { HOXI_RELEASE_KEY: "" }).status, 0);
  fs.unlinkSync(f.publisher);
  assert.notEqual(f.run(["0.2.1", "--tag"]).status, 0);
  assert.equal(f.calls(), "");
});

const sparkle = {
  version: "0.2.1", url: "https://github.com/hoxiai/hoxi/releases/download/v0.2.1/Hoxi-0.2.1-darwin-arm64.dmg",
  feedURL: "https://hoxi.ai/updates/macos/appcast.xml", publicKey: Buffer.alloc(32, 1).toString("base64"),
  edSignature: Buffer.alloc(64, 2).toString("base64"), length: 1234,
};

test("artifact validation enforces Sparkle bridge policy keys as strict plist booleans", () => {
  const info = { CFBundleIdentifier: "ai.hoxi.desktop", CFBundleVersion: "0.2.1",
    CFBundleShortVersionString: "0.2.1", LSMinimumSystemVersion: "13.0",
    CFBundleURLTypes: [{ CFBundleURLSchemes: ["hoxi"] }],
    SUFeedURL: sparkle.feedURL, SUPublicEDKey: sparkle.publicKey,
    SUEnableAutomaticChecks: true, SUAllowsAutomaticUpdates: false, SUAutomaticallyUpdate: false };
  validateAppMetadata(info, "0.2.1", "13.0");
  for (const [key, expected] of [["SUEnableAutomaticChecks", true],
    ["SUAllowsAutomaticUpdates", false], ["SUAutomaticallyUpdate", false]]) {
    for (const value of [undefined, !expected, String(expected), Number(expected), null]) {
      assert.throws(() => validateAppMetadata({ ...info, [key]: value }, "0.2.1", "13.0"), new RegExp(key));
    }
  }
  assert.throws(() => validateSparkleApp({ SUAllowsAutomaticUpdates: false }), /metadata/);
  assert.throws(() => validateSparkleApp({ ...info, CFBundleIdentifier: "ai.hoxi.desktop.dev" }), /Development/);
  const config = sparkleConfig({ HOXI_SPARKLE_DIR: ".", HOXI_SPARKLE_PUBLIC_KEY: sparkle.publicKey,
    HOXI_SPARKLE_FEED_URL: sparkle.feedURL });
  assert.throws(() => validateSparkleApp({}, config), /metadata/);
  assert.throws(() => validateSparkleApp({ ...info, SUFeedURL: "https://hoxi.ai/other.xml" }, config), /feed differs/);
  assert.equal(validateSparkleApp({}), null);
});

test("configure writes bridge policy booleans and strips updater from development bundles", { skip: process.platform !== "darwin" }, (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-sparkle-config-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const app = path.join(root, "Hoxi.app");
  const sdk = path.join(root, "sdk");
  fs.mkdirSync(path.join(app, "Contents"), { recursive: true });
  fs.mkdirSync(path.join(sdk, "Sparkle.framework/Versions/B"), { recursive: true });
  fs.mkdirSync(path.join(sdk, "bin"));
  for (const file of ["Sparkle", "Autoupdate", "Updater", "Installer", "Downloader"]) {
    fs.writeFileSync(path.join(sdk, "Sparkle.framework/Versions/B", file), "fixture");
  }
  fs.symlinkSync("Versions/B/Sparkle", path.join(sdk, "Sparkle.framework/Sparkle"));
  fs.writeFileSync(path.join(sdk, "bin/sign_update"), "#!/bin/sh\nexit 1\n", { mode: 0o755 });
  const plist = path.join(app, "Contents/Info.plist");
  fs.writeFileSync(plist, '<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>ai.hoxi.desktop</string></dict></plist>');
  const env = { ...process.env, HOXI_SPARKLE_DIR: sdk, HOXI_SPARKLE_PUBLIC_KEY: sparkle.publicKey,
    HOXI_SPARKLE_FEED_URL: sparkle.feedURL };
  const configure = (enabled, overrides = {}) => {
    const result = spawnSync(process.execPath, [path.join(scripts, "sparkle.mjs"), "configure", app, enabled],
      { env: { ...env, ...overrides }, encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    const converted = spawnSync("plutil", ["-convert", "json", "-o", "-", plist], { encoding: "utf8" });
    assert.equal(converted.status, 0, converted.stderr);
    return JSON.parse(converted.stdout);
  };
  validateSparkleApp(configure("1"));
  assert.ok(fs.lstatSync(path.join(app, "Contents/Frameworks/Sparkle.framework/Sparkle")).isSymbolicLink());
  for (const disabled of [configure("0"), configure("1", {
    HOXI_SPARKLE_DIR: "", HOXI_SPARKLE_PUBLIC_KEY: "", HOXI_SPARKLE_FEED_URL: "" })]) {
    assert.equal(Object.keys(disabled).some((key) => key.startsWith("SU")), false);
  }
  assert.equal(fs.existsSync(path.join(app, "Contents/Frameworks/Sparkle.framework")), false);
});

test("Sparkle is opt-in, rejects partial config and only accepts explicit official HTTPS feeds", () => {
  assert.equal(sparkleConfig({}), null);
  for (const env of [{ HOXI_SPARKLE_DIR: "/tmp/sdk" }, { HOXI_SPARKLE_PUBLIC_KEY: sparkle.publicKey }]) {
    assert.throws(() => sparkleConfig(env), /Partial/);
  }
  const env = { HOXI_SPARKLE_DIR: "/tmp/sdk", HOXI_SPARKLE_PUBLIC_KEY: sparkle.publicKey,
    HOXI_SPARKLE_FEED_URL: sparkle.feedURL };
  assert.equal(sparkleConfig(env).feedURL, sparkle.feedURL);
  for (const url of ["http://hoxi.ai/feed", "https://other.test/feed", "https://hoxi.ai/", "https://user@hoxi.ai/feed"]) {
    assert.throws(() => sparkleConfig({ ...env, HOXI_SPARKLE_FEED_URL: url }));
  }
  assert.throws(() => sparkleConfig({ ...env, HOXI_SPARKLE_PUBLIC_KEY: "bad" }), /32 bytes/);
});

test("Sparkle payload validates signature/key lengths, version, URL and byte length; no unuploaded GitCode mirror", () => {
  const data = releasePayload({ version: "0.2.1", notes: "test", size: 1234, sha256: "a".repeat(64), sparkle });
  assert.deepEqual(data.sparkle, sparkle);
  assert.equal(data.desktop["mac-arm64"], sparkle.url);
  assert.equal(data.mirrors["mac-arm64"].cn, undefined);
  for (const patch of [{ edSignature: "bad" }, { publicKey: "bad" }, { version: "0.2.2" },
    { length: 1235 }, { url: "https://example.com/app.dmg" }, { feedURL: "http://hoxi.ai/feed" }]) {
    assert.throws(() => validateSparkle({ ...sparkle, ...patch }, "0.2.1", 1234));
  }
});

test("website must explicitly accept exact Sparkle metadata, not merely generic release success", async () => {
  const data = releasePayload({ version: "0.2.1", size: 1234, sha256: "a".repeat(64), sparkle });
  for (const confirmation of [{ version: "0.2.1" }, { version: "0.2.1", sparkleAccepted: true },
    { version: "0.2.1", sparkleAccepted: true, sparkle: { ...sparkle, length: 5 } }]) {
    await assert.rejects(publishRelease(data, { key: "test", fetchImpl: async () =>
      Response.json({ success: true, data: confirmation }) }));
  }
  assert.equal((await publishRelease(data, { key: "test", fetchImpl: async () =>
    Response.json({ success: true, data: { version: "0.2.1", sparkleAccepted: true, sparkle } }) })).success, true);
});

test("Sparkle framework validation requires executable and helpers, and follows distribution symlinks", (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-sparkle-test-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  assert.throws(() => frameworkFiles(root));
  fs.mkdirSync(path.join(root, "Versions/B"), { recursive: true });
  for (const file of ["Sparkle", "Autoupdate", "Updater", "Installer", "Downloader"]) {
    fs.writeFileSync(path.join(root, "Versions/B", file), "fixture");
  }
  fs.symlinkSync("Versions/B/Sparkle", path.join(root, "Sparkle"));
  assert.equal(frameworkFiles(root).length, 5);
  fs.unlinkSync(path.join(root, "Versions/B/Installer"));
  assert.throws(() => frameworkFiles(root), /Installer/);
});

test("signature is checked against the configured public key and exact final DMG bytes", () => {
  // RFC 8032 test vector 1: public data only; no key generation or private material.
  const bytes = Buffer.alloc(0);
  const metadata = { publicKey: Buffer.from("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a", "hex").toString("base64"),
    edSignature: Buffer.from("e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e065224901555fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b", "hex").toString("base64") };
  verifyUpdateSignature(bytes, metadata);
  assert.throws(() => verifyUpdateSignature(Buffer.from("modified"), metadata), /does not match/);
});

test("Sparkle workflow signs only the final stapled DMG and uploads before website publication", () => {
  const workflow = fs.readFileSync(path.join(scripts, "../.github/workflows/release.yml"), "utf8");
  const gates = ['verify-distribution "$DMG"', 'sign-update "$DMG"', "uses: softprops/action-gh-release@v2",
    "- name: Publish website metadata after asset upload", "node scripts/release-publisher.mjs"];
  let position = -1;
  for (const gate of gates) { const next = workflow.indexOf(gate, position + 1); assert.ok(next > position); position = next; }
  assert.match(workflow, /shasum -a 256 -c -/);
  assert.match(workflow, /HOXI_SPARKLE_PUBLIC_KEY:\s*\$\{\{ secrets\./);
  const build = fs.readFileSync(path.join(scripts, "../build-desktop.sh"), "utf8");
  assert.ok(build.indexOf('sparkle.mjs configure') < build.indexOf('sparkle.mjs sign-framework'));
  assert.doesNotMatch(build, /opencode.ai\/install/);
});

test("dry runs and build-only candidates work without a publisher and never publish or tag", (t) => {
  const f = fixture(t);
  fs.unlinkSync(f.publisher);
  f.dmg("0.2.1");
  for (const mode of ["--dry-run", "--build-only"]) {
    const result = f.run(["0.2.1", "--skip-build", mode, "--json"], { HOXI_RELEASE_KEY: "" });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).publication, mode === "--dry-run" ? "dry-run" : "not-requested");
  }
  assert.equal(f.calls().includes("publisher"), false);
  assert.equal(f.calls().includes("git"), false);
});

test("a mismatched DMG cannot be renamed into a release", (t) => {
  const f = fixture(t);
  f.dmg("0.2.0");
  const result = f.run(["0.2.1", "--skip-build", "--dry-run"]);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /version mismatch/);
  assert.equal(fs.existsSync(path.join(f.root, "build/bin/Hoxi-0.2.1-darwin-arm64.dmg")), false);
});

test("named candidates take precedence over an unrelated generic DMG", (t) => {
  const f = fixture(t);
  f.dmg("0.2.0");
  f.dmg("0.2.1", "Hoxi-0.2.1-darwin-arm64.dmg");
  assert.equal(f.run(["0.2.1", "--skip-build", "--dry-run"]).status, 0);
});

test("fresh production builds receive version and notarization flags, and must publish before tagging", (t) => {
  const f = fixture(t);
  const result = f.run(["0.2.1", "--json"]);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(JSON.parse(result.stdout).success, true);
  assert.match(f.calls(), /--version 0\.2\.1/);
  assert.match(f.calls(), /--notarize/);
  assert.match(f.calls(), /--no-install/);
  assert.match(f.calls(), /publisher/);
});

test("skipped publisher results and local signatures fail without creating a tag", (t) => {
  const f = fixture(t);
  f.dmg("0.2.1");
  assert.notEqual(f.run(["0.2.1", "--skip-build", "--tag"],
    { TEST_PUBLISH_RESULT: '{"publication":"skipped"}' }).status, 0);
  assert.equal(f.calls().includes("git"), false);
  const before = f.calls();
  assert.notEqual(f.run(["0.2.1", "--skip-build"], { TEST_ADHOC: "1" }).status, 0);
  assert.equal(f.calls().slice(before.length).includes("publisher"), false);
});
