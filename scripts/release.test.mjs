import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import {
  normalizeVersion, maximumMacOS, minimumFromMachO, sidecarPolicy, configureApp, verifyApp,
  validateAppMetadata, validatePublication,
} from "./release-artifact.mjs";
import { releasePayload, publishRelease } from "./release-publisher.mjs";
import { sparkleConfig, validateSparkle, validateSparkleApp, frameworkFiles, verifyUpdateSignature, resolveSparkleEnclosureURL } from "./sparkle.mjs";
import { hasCdnConfig, resolveCdnConfig, resolveCdnBaseUrl, signedRequest, uploadToCdn, syncDesktopRelease, desktopReleaseFiles } from "./upload-cdn.mjs";
import { loadOpenCodeLock, parseOpenCodeVersion, verifyBinaryVersion } from "./opencode-lock.mjs";
import { verifySQLiteArchive, sqliteAsset } from "./sqlite-tools.mjs";

test("SQLite release archive rejects a mismatched digest", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-sqlite-lock-"));
  try {
    const file = path.join(dir, "sqlite.zip");
    fs.writeFileSync(file, "not the pinned SQLite archive");
    assert.throws(() => verifySQLiteArchive(file), /checksum mismatch/);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test("SQLite pins both Windows amd64 and arm64 tools archives", () => {
  const amd64 = sqliteAsset("amd64");
  const arm64 = sqliteAsset("arm64");
  assert.match(amd64.url, /sqlite-tools-win-x64-/);
  assert.match(arm64.url, /sqlite-tools-win-arm64-/);
  assert.match(amd64.sha3, /^[a-f0-9]{64}$/);
  assert.match(arm64.sha3, /^[a-f0-9]{64}$/);
  assert.notEqual(amd64.sha3, arm64.sha3);
  assert.throws(() => sqliteAsset("ia64"), /Unsupported/);
});

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
  const info = { CFBundleIdentifier: "ai.hoxi.desktop", CFBundleVersion: "2001",
    CFBundleShortVersionString: "0.2.1", LSMinimumSystemVersion: "13.0",
    CFBundleURLTypes: [{ CFBundleURLSchemes: ["hoxi"] }] };
  validateAppMetadata(info, "0.2.1", "13.0");
  assert.throws(() => validateAppMetadata({ ...info, CFBundleURLTypes: undefined }, "0.2.1", "13.0"), /URL scheme/);
  assert.throws(() => validateAppMetadata({ ...info, CFBundleURLTypes: [{ CFBundleURLSchemes: ["other"] }] }, "0.2.1", "13.0"), /URL scheme/);
  assert.throws(() => validateAppMetadata(info, "0.2.2", "13.0"), /display version/);
  assert.throws(() => validateAppMetadata({ ...info, CFBundleVersion: "2000" }, "0.2.1", "13.0"), /build version/);
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
      assert.equal(body.appKey, "hoxi-desktop");
      assert.equal(body.channel, "stable");
      assert.equal(body.version, "0.2.1");
      assert.equal(body.dryRun, undefined);
      assert.ok(body.locales.zh.body === "Release test");
      const mac = body.assets.find((asset) => asset.platform === "macos" && asset.arch === "arm64");
      assert.equal(mac.sha256, "a".repeat(64));
      assert.equal(mac.size, 1234);
      assert.ok(mac.browser_download_url.endsWith("/v0.2.1/Hoxi-0.2.1-darwin-arm64.dmg"));
      const macIntel = body.assets.find((asset) => asset.platform === "macos" && asset.arch === "x64");
      assert.ok(macIntel.browser_download_url.endsWith("/v0.2.1/Hoxi-0.2.1-darwin-amd64.dmg"));
      assert.equal(macIntel.name, "Hoxi-0.2.1-darwin-amd64.dmg");
      const win = body.assets.find((asset) => asset.platform === "windows" && asset.arch === "x64");
      assert.ok(win.browser_download_url.endsWith("/v0.2.1/Hoxi-0.2.1-windows-amd64-setup.exe"));
      const winArm = body.assets.find((asset) => asset.platform === "windows" && asset.arch === "arm64");
      assert.ok(winArm.browser_download_url.endsWith("/v0.2.1/Hoxi-0.2.1-windows-arm64-setup.exe"));
      assert.equal(body.assets.length, 5);
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
  await assert.rejects(publishRelease(payload, { key: "test", url: "https://secret@hoxi.ai/publish" }), /Credentials/);
  assert.equal((await publishRelease(payload, { key: "test",
    fetchImpl: async () => Response.json({ code: 0, data: { version: "0.2.1" } }) })).success, true);
});

// Test shell routing in a disposable project. Native DMG verification is exercised
// separately against a real packaged app; this fixture isolates orchestration.
const scripts = path.dirname(fileURLToPath(import.meta.url));

test("opencode.lock pins every platform while release workflows ship lean sidecar-free bundles by default", () => {
  const lock = loadOpenCodeLock();
  assert.match(lock.version, /^\d+\.\d+\.\d+$/);
  assert.equal(Object.keys(lock.assets).length, 6);

  const macWorkflowPath = path.join(scripts, "../.github/workflows/release.yml");
  const hasDesktopWorkflows = fs.existsSync(macWorkflowPath);
  const crossWorkflow = fs.readFileSync(path.join(scripts, "../.github/workflows/release-cross-platform.yml"), "utf8");

  if (hasDesktopWorkflows) {
    const macWorkflow = fs.readFileSync(macWorkflowPath, "utf8");
    assert.match(macWorkflow, /node scripts\/opencode-lock\.mjs verify/);
    assert.match(macWorkflow, /HOXI_BUNDLE_OPENCODE:\s*"0"/);
    assert.doesNotMatch(macWorkflow, /opencode-arm64\.zip|opencode-amd64\.zip|opencode-windows-|opencode-linux-/);
    assert.doesNotMatch(macWorkflow, /vars\.HOXI_OPENCODE_ARCHIVE_(URL|SHA256)/);
    assert.doesNotMatch(macWorkflow, /releases\/latest\/download/);
    assert.match(macWorkflow, /HOXI_RELEASE_REPO:\s*\$\{\{ vars\.HOXI_RELEASE_REPO \}\}/);
    assert.match(macWorkflow, /HOXI_PUBLIC_RELEASE_TOKEN:\?/);
    // Unified desktop workflow builds macOS, Windows (amd64 + arm64), and Linux before one atomic publish job.
    assert.match(macWorkflow, /^  preflight:$/m);
    assert.match(macWorkflow, /^  build-macos:$/m);
    assert.match(macWorkflow, /^  build-windows:$/m);
    assert.match(macWorkflow, /^  build-linux:$/m);
    assert.match(macWorkflow, /^  publish-release:$/m);
    assert.match(macWorkflow, /needs:\s*\[preflight,\s*build-macos,\s*build-windows,\s*build-linux\]/);
    assert.match(macWorkflow, /arch: \[amd64, arm64\]/);
    assert.match(macWorkflow, /wails build -platform windows\/\$\{\{ matrix\.arch \}\} -nsis/);
    assert.match(macWorkflow, /Hoxi-\$\{RELEASE_VERSION\}-windows-\$\{\{ matrix\.arch \}\}-setup\.exe/);
    assert.match(macWorkflow, /Hoxi-\$\{RELEASE_VERSION\}-windows-\$\{\{ matrix\.arch \}\}\.zip/);
    assert.match(macWorkflow, /test ! -f build\/bin\/opencode\.exe/);
    assert.match(macWorkflow, /test ! -f build\/bin\/sqlite3\.exe/);
    assert.match(macWorkflow, /pe-arch\.mjs verify \$\{\{ matrix\.arch \}\} build\/bin\/Hoxi\.exe$/m);
    assert.match(macWorkflow, /7z a "\$ZIP_NAME" Hoxi\.exe$/m);
    assert.match(macWorkflow, /tar -czf "\$TAR_NAME" Hoxi hoxi\.desktop hoxi\.png/);
    assert.match(macWorkflow, /Hoxi-\$\{\{ env\.RELEASE_VERSION \}\}-linux-amd64\.tar\.gz\.sha256/);
    assert.equal((macWorkflow.match(/uses:\s*softprops\/action-gh-release@v2/g) || []).length, 1,
      "Desktop release workflow must upload all platform assets in one atomic publish-release step");
    assert.equal((macWorkflow.match(/repository:\s*\$\{\{ env\.RELEASE_TARGET_REPO \}\}/g) || []).length, 1);
  }
  // Cross-platform workflow stays scoped strictly to Hoxi Remote Android APK so desktop jobs never collide with OSS mirror tags.
  assert.doesNotMatch(crossWorkflow, /^\s+build-(windows|linux):/m);
  assert.doesNotMatch(crossWorkflow, /HOXI_WORK_TOKEN|HOXI_RELEASE_REPO|HOXI_PUBLIC_RELEASE_TOKEN/);
  assert.match(crossWorkflow, /build-mobile-android:/);
  assert.match(crossWorkflow, /applicationId = "ai\.hoxi\.remote"/);
  assert.match(crossWorkflow, /Hoxi-Remote-\$\{\{ env\.RELEASE_VERSION \}\}-android\.apk/);
  assert.match(crossWorkflow, /Hoxi-Remote-\$\{\{ env\.RELEASE_VERSION \}\}-android\.apk\.sha256/);
  assert.match(crossWorkflow, /rm -f mobile\/android\/hoxi-release\.jks/);

  const nsisProject = fs.readFileSync(path.join(scripts, "../build/windows/installer/project.nsi"), "utf8");
  assert.match(nsisProject, /!define WAILS_INSTALL_SCOPE "user"/);
  assert.match(nsisProject, /!define REQUEST_EXECUTION_LEVEL "user"/);
  assert.match(nsisProject, /!include "MUI2\.nsh"/);
  assert.match(nsisProject, /!include "nsDialogs\.nsh"/);
  assert.match(nsisProject, /Page custom HoxiQuickInstallPageCreate HoxiQuickInstallPageLeave/);
  assert.match(nsisProject, /Page custom HoxiFinishPageCreate HoxiFinishPageLeave/);
  assert.match(nsisProject, /ShowInstDetails nevershow/);
  assert.doesNotMatch(nsisProject, /!error "Hoxi installer requires opencode\.exe/);
  assert.doesNotMatch(nsisProject, /!error "Hoxi installer requires the verified sqlite3\.exe sidecar"/);
  assert.match(nsisProject, /!ifdef ARG_HOXI_OPENCODE_BINARY/);
  assert.match(nsisProject, /File "\/oname=opencode\.exe"/);
  assert.match(nsisProject, /Delete "\$INSTDIR\\opencode\.exe"/);
  assert.match(nsisProject, /!ifdef ARG_HOXI_SQLITE_BINARY/);
  assert.match(nsisProject, /File "\/oname=sqlite3\.exe"/);
  assert.match(nsisProject, /Delete "\$INSTDIR\\sqlite3\.exe"/);
  assert.match(nsisProject, /!insertmacro wails\.webview2runtime/);
  assert.match(nsisProject, /WriteRegStr SHCTX "Software\\Classes\\hoxi" "URL Protocol" ""/);
  assert.match(nsisProject, /DeleteRegKey SHCTX "Software\\Classes\\hoxi"/);
  for (const bmp of ["banner.bmp", "header.bmp", "wizard.bmp"]) {
    const bmpPath = path.join(scripts, "../build/windows/installer", bmp);
    assert.equal(fs.existsSync(bmpPath), true, `Missing Windows installer asset: ${bmp}`);
    assert.equal(fs.readFileSync(bmpPath).subarray(0, 2).toString("ascii"), "BM");
  }
  // Upgrading from an older sidecar-bundled install must still stop any legacy
  // $INSTDIR\opencode.exe child before deleting it, matched by full path rather
  // than by process name so user-installed external opencode binaries stay intact.
  assert.equal(nsisProject.match(/^\s*!insertmacro hoxi\.stopInstalledProcesses\s*$/gm)?.length, 2);
  assert.match(nsisProject, /'opencode\.exe', 'sqlite3\.exe'/);
  assert.match(nsisProject, /GetDirectoryName\(\$\$_\.ExecutablePath\) -ieq \$\$root/);
  assert.doesNotMatch(nsisProject, /taskkill[^\n]*opencode/i);

  const macBuild = fs.readFileSync(path.join(scripts, "../build-desktop.sh"), "utf8");
  assert.match(macBuild, /opencode-lock\.mjs verify-binary/);
  assert.match(macBuild, /opencode-lock\.mjs verify-archive/);
  const winBuild = fs.readFileSync(path.join(scripts, "build-windows.ps1"), "utf8");
  assert.match(winBuild, /\[switch\]\$BundleOpenCode/);
  assert.match(winBuild, /\$env:HOXI_BUNDLE_OPENCODE = \$BundleMode/);
  assert.match(winBuild, /opencode-lock\.mjs verify-binary/);
  assert.match(winBuild, /opencode-lock\.mjs verify-archive/);
  assert.match(winBuild, /\[ValidateSet\("amd64", "arm64"\)\]/);
  assert.match(winBuild, /windows\/\$Arch/);
});

test("verifyBinaryVersion accepts only the pinned OpenCode version and rejects drift or failing binaries", { skip: process.platform === "win32" }, (t) => {
  assert.equal(parseOpenCodeVersion("\x1b[33mWarning: test\x1b[0m\nopencode v1.19.3\n"), "1.19.3");
  assert.equal(parseOpenCodeVersion("1.19.3\n"), "1.19.3");
  assert.equal(parseOpenCodeVersion("noise line\n"), "");

  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-opencode-bin-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const lock = loadOpenCodeLock();
  const goodBin = path.join(dir, "opencode-good");
  fs.writeFileSync(goodBin, `#!/bin/sh\necho "opencode ${lock.version}"\n`, { mode: 0o755 });
  assert.equal(verifyBinaryVersion(goodBin), lock.version);

  const driftedBin = path.join(dir, "opencode-drifted");
  fs.writeFileSync(driftedBin, "#!/bin/sh\necho 'opencode 0.0.1'\n", { mode: 0o755 });
  assert.throws(() => verifyBinaryVersion(driftedBin), /version mismatch/);

  const brokenBin = path.join(dir, "opencode-broken");
  fs.writeFileSync(brokenBin, "#!/bin/sh\nexit 1\n", { mode: 0o755 });
  assert.throws(() => verifyBinaryVersion(brokenBin), /failed to execute/);
});

test("tag workflow gates publication on tests, versioned artifacts and notarization", (t) => {
  const workflowPath = path.join(scripts, "../.github/workflows/release.yml");
  if (!fs.existsSync(workflowPath)) {
    t.skip("Desktop macOS release workflow is maintained outside the OSS export");
    return;
  }
  const workflow = fs.readFileSync(workflowPath, "utf8");
  const required = [
    'version "$RELEASE_TAG"',
    'MACOS_CERTIFICATE:?MACOS_CERTIFICATE is required',
    'APPLE_API_ISSUER:?APPLE_API_ISSUER is required',
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
    '--issuer "$APPLE_API_ISSUER"',
    "- name: Install Rosetta for Intel artifact verification",
    '--version "$RELEASE_VERSION" --notarize --notary-profile "hoxi-notary"',
    'verify-dmg "$DMG" "$RELEASE_VERSION"',
    'verify-distribution "$DMG"',
    "shasum -a 256",
    "if: always()",
    'security delete-keychain "$KEYCHAIN_PATH"',
    'rm -f "${APPLE_API_KEY_PATH:-}"',
    "needs: [preflight, build-macos, build-windows, build-linux]",
    "- name: Resolve release notes",
    'docs/release-notes/v${RELEASE_VERSION}.md',
    "awk '{sub(/\\r$/, \"\"); print}'",
    "sha256sum -c",
    "- name: Mirror release artifacts to CDN",
    "uses: softprops/action-gh-release@v2",
    "body: ${{ env.RELEASE_NOTES }}",
    "build/bin/Hoxi-${{ env.RELEASE_VERSION }}-darwin-arm64.dmg",
    "build/bin/Hoxi-${{ env.RELEASE_VERSION }}-darwin-amd64.dmg",
    "build/bin/Hoxi-${{ env.RELEASE_VERSION }}-windows-amd64-setup.exe",
    "build/bin/Hoxi-${{ env.RELEASE_VERSION }}-windows-arm64-setup.exe",
    "build/bin/Hoxi-${{ env.RELEASE_VERSION }}-linux-amd64.tar.gz",
    "fail_on_unmatched_files: true",
  ];
  let position = -1;
  for (const gate of required) {
    const next = workflow.indexOf(gate, position + 1);
    assert.ok(next > position, `Missing or out-of-order release gate: ${gate}`);
    position = next;
  }
  assert.doesNotMatch(workflow, /Optional|CAN_NOTARIZE|\|\| true|build\/bin\/\*\.dmg|generate_release_notes/);
  assert.equal((workflow.match(/^\s+if:/gm) || []).length, 1, "Only cleanup may be conditional");
  for (const secret of ["MACOS_CERTIFICATE", "MACOS_CERTIFICATE_PWD", "APPLE_API_KEY",
    "APPLE_API_KEY_ID", "APPLE_API_ISSUER"]) {
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
  fs.copyFileSync(path.join(scripts, "upload-cdn.mjs"), path.join(root, "scripts/upload-cdn.mjs"));
  fs.copyFileSync(path.join(scripts, "opencode-lock.mjs"), path.join(root, "scripts/opencode-lock.mjs"));
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
  const info = { CFBundleIdentifier: "ai.hoxi.desktop", CFBundleVersion: "2001",
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

test("Sparkle payload validates signature/key lengths, version, URL and byte length", () => {
  const data = releasePayload({ version: "0.2.1", notes: "test", size: 1234, sha256: "a".repeat(64), sparkle });
  assert.deepEqual(data.metaData.sparkle, sparkle);
  const mac = data.assets.find((asset) => asset.platform === "macos" && asset.arch === "arm64");
  assert.equal(mac.browser_download_url, sparkle.url);
  assert.equal(mac.edSignature, sparkle.edSignature);
  for (const patch of [{ edSignature: "bad" }, { publicKey: "bad" }, { version: "0.2.2" },
    { length: 1235 }, { url: "https://hoxi.ai/app.dmg" }, { feedURL: "http://hoxi.ai/feed" }]) {
    assert.throws(() => validateSparkle({ ...sparkle, ...patch }, "0.2.1", 1234));
  }
});

test("website must explicitly accept exact Sparkle metadata, not merely generic release success", async () => {
  const data = releasePayload({ version: "0.2.1", size: 1234, sha256: "a".repeat(64), sparkle });
  const macAsset = { platform: "macos", arch: "arm64", package: "desktop", edSignature: sparkle.edSignature, size: sparkle.length };
  for (const confirmation of [{ version: "0.2.1" },
    { version: "0.2.1", assets: [{ ...macAsset, edSignature: undefined }] },
    { version: "0.2.1", assets: [{ ...macAsset, edSignature: Buffer.alloc(64, 9).toString("base64") }] },
    { version: "0.2.1", assets: [{ ...macAsset, size: 5 }] }]) {
    await assert.rejects(publishRelease(data, { key: "test", fetchImpl: async () =>
      Response.json({ success: true, data: confirmation }) }));
  }
  assert.equal((await publishRelease(data, { key: "test", fetchImpl: async () =>
    Response.json({ success: true, data: { version: "0.2.1", assets: [macAsset] } }) })).success, true);
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
  const workflowPath = path.join(scripts, "../.github/workflows/release.yml");
  if (fs.existsSync(workflowPath)) {
    const workflow = fs.readFileSync(workflowPath, "utf8");
    const gates = ['verify-distribution "$DMG"', 'sign-update "$DMG"', "uses: softprops/action-gh-release@v2",
      "- name: Publish website metadata after asset upload", "node scripts/release-publisher.mjs"];
    let position = -1;
    for (const gate of gates) { const next = workflow.indexOf(gate, position + 1); assert.ok(next > position); position = next; }
    assert.match(workflow, /shasum -a 256 -c -/);
    assert.match(workflow, /HOXI_SPARKLE_PUBLIC_KEY:\s*\$\{\{ secrets\./);
  }
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

test("ai-release.sh resolves docs/release-notes/v<version>.md when notes argument is omitted", (t) => {
  const f = fixture(t);
  f.dmg("0.2.1");
  fs.mkdirSync(path.join(f.root, "docs/release-notes"), { recursive: true });
  fs.writeFileSync(path.join(f.root, "docs/release-notes/v0.2.1.md"), "Hoxi Desktop v0.2.1\n\n- Audited note\n");
  const result = f.run(["0.2.1", "--skip-build", "--dry-run", "--json"], { HOXI_RELEASE_KEY: "" });
  assert.equal(result.status, 0, result.stderr);
  assert.match(JSON.parse(result.stdout).notes, /Audited note/);
});

test("release version metadata, release notes and root CHANGELOG stay synchronized", () => {
  const repoRoot = path.join(scripts, "..");
  const wails = JSON.parse(fs.readFileSync(path.join(repoRoot, "wails.json"), "utf8"));
  const webPkg = JSON.parse(fs.readFileSync(path.join(repoRoot, "web/package.json"), "utf8"));
  const winInfo = JSON.parse(fs.readFileSync(path.join(repoRoot, "build/windows/info.json"), "utf8"));
  const buildInfoGo = fs.readFileSync(path.join(repoRoot, "internal/buildinfo/version.go"), "utf8");
  const changelog = fs.readFileSync(path.join(repoRoot, "CHANGELOG.md"), "utf8");

  const version = normalizeVersion(wails?.info?.productVersion || "");
  assert.equal(webPkg.version, version, "web/package.json version must match wails.json");
  assert.equal(winInfo.productVersion, `${version}.0`, "build/windows/info.json productVersion must match wails.json");
  assert.equal(winInfo.fileVersion, `${version}.0`, "build/windows/info.json fileVersion must match wails.json");
  assert.match(buildInfoGo, new RegExp(`var\\s+Version\\s*=\\s*"${version.replace(/\./g, "\\.")}"`));

  const notesPath = path.join(repoRoot, `docs/release-notes/v${version}.md`);
  assert.equal(fs.existsSync(notesPath), true, `Missing release notes: docs/release-notes/v${version}.md`);
  assert.ok(fs.readFileSync(notesPath, "utf8").trim().length > 0);

  for (const entry of fs.readdirSync(path.join(repoRoot, "docs/release-notes"))) {
    if (!/^v\d+\.\d+\.\d+\.md$/.test(entry)) continue;
    assert.equal(
      changelog.includes(`docs/release-notes/${entry}`),
      true,
      `CHANGELOG.md must index docs/release-notes/${entry}`
    );
  }
});

test("build-desktop.sh skips incomplete work/ directories and preserves valid fallback for Vite", () => {
  const repoRoot = path.join(scripts, "..");
  const buildScript = fs.readFileSync(path.join(repoRoot, "build-desktop.sh"), "utf8");
  const start = buildScript.indexOf("resolve_work_repo_dir() {");
  const end = buildScript.indexOf("\nif [[ ! -d web/node_modules ]]; then", start);
  assert.ok(start > 0 && end > start, "Expected resolve_work_repo_dir block in build-desktop.sh");
  const snippet = buildScript.slice(start, end);

  const sandboxDir = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-ext-resolve-"));
  try {
    const projectRoot = path.join(sandboxDir, "hoxi");
    const incompleteWork = path.join(projectRoot, "work");
    const siblingWork = path.join(sandboxDir, "hoxi-ext");
    fs.mkdirSync(path.join(incompleteWork, "docs"), { recursive: true });
    fs.mkdirSync(path.join(siblingWork, "web"), { recursive: true });
    fs.writeFileSync(path.join(siblingWork, "web/index.tsx"), "export const surfaceExtension = { enabled: true };\n");

    const run = spawnSync(
      "bash",
      [
        "-c",
        `set -euo pipefail\nPROJECT_ROOT="$1"\n${snippet}\nprintf '%s\\n%s\\n' "$WORK_REPO_DIR" "\${HOXI_WORK_DIR:-}"`,
        "bash",
        projectRoot,
      ],
      { encoding: "utf8", env: { ...process.env, HOXI_WORK_DIR: "" } }
    );
    assert.equal(run.status, 0, run.stderr);
    const [resolvedDir, exportedDir] = run.stdout.trim().split("\n");
    assert.equal(fs.realpathSync(resolvedDir), fs.realpathSync(siblingWork));
    assert.equal(fs.realpathSync(exportedDir), fs.realpathSync(siblingWork));

    // When no candidate has web/index.tsx, HOXI_WORK_DIR must not be exported to an empty work/ dir.
    fs.rmSync(siblingWork, { recursive: true, force: true });
    const runMissing = spawnSync(
      "bash",
      [
        "-c",
        `set -euo pipefail\nPROJECT_ROOT="$1"\n${snippet}\nprintf '%s\\n' "\${HOXI_WORK_DIR:-UNSET}"`,
        "bash",
        projectRoot,
      ],
      { encoding: "utf8", env: { ...process.env, HOXI_WORK_DIR: "" } }
    );
    assert.equal(runMissing.status, 0, runMissing.stderr);
    assert.equal(runMissing.stdout.trim(), "UNSET");
  } finally {
    fs.rmSync(sandboxDir, { recursive: true, force: true });
  }
});

test("sidecarPolicy defaults to optional verification, forbids sidecar on 0 and requires it on 1", () => {
  assert.equal(sidecarPolicy({}), "optional");
  assert.equal(sidecarPolicy({ HOXI_BUNDLE_OPENCODE: "" }), "optional");
  assert.equal(sidecarPolicy({ HOXI_BUNDLE_OPENCODE: "auto" }), "optional");
  assert.equal(sidecarPolicy({ HOXI_BUNDLE_OPENCODE: "0" }), "forbidden");
  assert.equal(sidecarPolicy({ HOXI_BUNDLE_OPENCODE: "false" }), "forbidden");
  assert.equal(sidecarPolicy({ HOXI_BUNDLE_OPENCODE: "1" }), "required");
  assert.equal(sidecarPolicy({ HOXI_BUNDLE_OPENCODE: "true" }), "required");
  for (const invalid of ["yes", "2", "invalid"]) {
    assert.throws(() => sidecarPolicy({ HOXI_BUNDLE_OPENCODE: invalid }), /HOXI_BUNDLE_OPENCODE/);
  }
});

test("build-desktop.sh omits opencode sidecar by default and supports opt-in HOXI_BUNDLE_OPENCODE=1", () => {
  const repoRoot = path.join(scripts, "..");
  const buildScript = fs.readFileSync(path.join(repoRoot, "build-desktop.sh"), "utf8");
  const start = buildScript.indexOf('OPENCODE_SRC=""');
  const end = buildScript.indexOf("\nresolve_signing_identity() {", start);
  assert.ok(start > 0 && end > start, "Expected HOXI_BUNDLE_OPENCODE block in build-desktop.sh");
  const snippet = buildScript.slice(start, end);

  const runSnippet = (envOverrides = {}) => spawnSync(
    "bash",
    [
      "-c",
      `set -euo pipefail\nTARGET_ARCH="arm64"\nresolve_opencode_bin() {\n  if [[ "\${MOCK_OPENCODE_FAIL:-0}" == "1" ]]; then return 1; fi\n  echo "/mock/bin/opencode"\n}\n${snippet}\nprintf '%s|%s\\n' "\${HOXI_BUNDLE_OPENCODE}" "\${OPENCODE_SRC}"`,
    ],
    { encoding: "utf8", env: { ...process.env, ...envOverrides } },
  );

  const defaultEnv = { ...process.env };
  delete defaultEnv.HOXI_BUNDLE_OPENCODE;
  const byDefault = spawnSync(
    "bash",
    [
      "-c",
      `set -euo pipefail\nTARGET_ARCH="arm64"\nresolve_opencode_bin() { echo "should-not-run" >&2; exit 99; }\n${snippet}\nprintf '%s|%s\\n' "\${HOXI_BUNDLE_OPENCODE}" "\${OPENCODE_SRC}"`,
    ],
    { encoding: "utf8", env: defaultEnv },
  );
  assert.equal(byDefault.status, 0, byDefault.stderr);
  assert.equal(byDefault.stdout.trim(), "0|");

  const explicitZero = runSnippet({ HOXI_BUNDLE_OPENCODE: "0" });
  assert.equal(explicitZero.status, 0, explicitZero.stderr);
  assert.equal(explicitZero.stdout.trim(), "0|");

  const optIn = runSnippet({ HOXI_BUNDLE_OPENCODE: "1" });
  assert.equal(optIn.status, 0, optIn.stderr);
  assert.equal(optIn.stdout.trim(), "1|/mock/bin/opencode");

  const optInMissing = runSnippet({ HOXI_BUNDLE_OPENCODE: "1", MOCK_OPENCODE_FAIL: "1" });
  assert.notEqual(optInMissing.status, 0);
  assert.match(optInMissing.stderr, /Failed to resolve or download verified OpenCode/);

  const invalidMode = runSnippet({ HOXI_BUNDLE_OPENCODE: "bogus" });
  assert.notEqual(invalidMode.status, 0);
  assert.match(invalidMode.stderr, /Unsupported HOXI_BUNDLE_OPENCODE/);
});

test("configureApp and verifyApp validate lean pure-Go bundles and opt-in OpenCode sidecars", { skip: process.platform !== "darwin" }, (t) => {
  const hostArch = process.arch === "x64" ? "amd64" : process.arch;
  const clangArch = hostArch === "amd64" ? "x86_64" : "arm64";
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-app-sidecar-gate-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));

  const compileStub = (outPath, printedLine, minos = "13.0") => {
    const src = `#include <stdio.h>\nint main(void) { puts(${JSON.stringify(printedLine)}); return 0; }\n`;
    const res = spawnSync(
      "cc",
      ["-x", "c", "-", "-arch", clangArch, `-mmacosx-version-min=${minos}`, "-o", outPath],
      { input: src, encoding: "utf8" },
    );
    assert.equal(res.status, 0, res.stderr);
    fs.chmodSync(outPath, 0o755);
  };

  const signBundle = (appPath) => {
    const sidecarPath = path.join(appPath, "Contents/MacOS/opencode");
    if (fs.existsSync(sidecarPath)) {
      const s1 = spawnSync("codesign", ["--force", "--sign", "-", sidecarPath], { encoding: "utf8" });
      assert.equal(s1.status, 0, s1.stderr);
    }
    const s2 = spawnSync("codesign", ["--force", "--sign", "-", appPath], { encoding: "utf8" });
    assert.equal(s2.status, 0, s2.stderr);
  };

  const app = path.join(root, "Hoxi.app");
  fs.mkdirSync(path.join(app, "Contents/MacOS"), { recursive: true });
  fs.writeFileSync(
    path.join(app, "Contents/Info.plist"),
    `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>Hoxi</string>
  <key>CFBundleIdentifier</key><string>ai.hoxi.desktop</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>CFBundleShortVersionString</key><string>0.0.1</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>CFBundleURLTypes</key><array><dict><key>CFBundleURLSchemes</key><array><string>hoxi</string></array></dict></array>
</dict></plist>`,
  );
  compileStub(path.join(app, "Contents/MacOS/Hoxi"), "0.2.1", "13.0");

  const baseEnv = {};
  configureApp(app, "0.2.1", hostArch, { ...baseEnv, HOXI_BUNDLE_OPENCODE: "0" });
  signBundle(app);

  const leanVerified = verifyApp(app, "0.2.1", hostArch, { ...baseEnv, HOXI_BUNDLE_OPENCODE: "0" });
  assert.equal(leanVerified.bundledOpenCode, false);
  assert.equal(leanVerified.minimumMacOS, "13.0");
  assert.equal(verifyApp(app, "0.2.1", hostArch, baseEnv).bundledOpenCode, false);
  assert.throws(
    () => verifyApp(app, "0.2.1", hostArch, { ...baseEnv, HOXI_BUNDLE_OPENCODE: "1" }),
    /Missing executable: .*opencode/,
  );

  const lock = loadOpenCodeLock();
  const sidecarPath = path.join(app, "Contents/MacOS/opencode");
  compileStub(sidecarPath, `opencode ${lock.version}`, "14.0");
  assert.throws(
    () => verifyApp(app, "0.2.1", hostArch, { ...baseEnv, HOXI_BUNDLE_OPENCODE: "0" }),
    /Unexpected bundled OpenCode sidecar/,
  );

  configureApp(app, "0.2.1", hostArch, { ...baseEnv, HOXI_BUNDLE_OPENCODE: "1" });
  signBundle(app);
  const bundledVerified = verifyApp(app, "0.2.1", hostArch, { ...baseEnv, HOXI_BUNDLE_OPENCODE: "1" });
  assert.equal(bundledVerified.bundledOpenCode, true);
  assert.equal(bundledVerified.minimumMacOS, "14.0");

  compileStub(sidecarPath, "opencode 0.0.1", "14.0");
  signBundle(app);
  assert.throws(
    () => verifyApp(app, "0.2.1", hostArch, { ...baseEnv, HOXI_BUNDLE_OPENCODE: "1" }),
    /version mismatch/,
  );
});

test("export-opensource.sh preserves tracked .hoxi/skills while excluding runtime secrets and .agents", () => {
  const repoRoot = path.join(scripts, "..");
  const exportScript = fs.readFileSync(path.join(repoRoot, "scripts/export-opensource.sh"), "utf8");
  const start = exportScript.indexOf("rsync -a --delete");
  const end = exportScript.indexOf('\npython3 "${PROJECT_ROOT}/client/scripts/export_mobile_oss.py"', start);
  assert.ok(start > 0 && end > start, "Expected rsync block in scripts/export-opensource.sh");
  const rsyncCmd = exportScript.slice(start, end);

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-oss-export-"));
  try {
    const src = path.join(tmp, "src");
    const dst = path.join(tmp, "dst");
    fs.mkdirSync(path.join(src, ".hoxi/skills/tools"), { recursive: true });
    fs.mkdirSync(path.join(src, ".hoxi/memory"), { recursive: true });
    fs.mkdirSync(path.join(src, ".agents/skills/private"), { recursive: true });
    fs.mkdirSync(path.join(src, "internal/app"), { recursive: true });
    fs.mkdirSync(dst, { recursive: true });
    fs.writeFileSync(path.join(src, ".hoxi/skills/tools/SKILL.md"), "# Tools\n");
    fs.writeFileSync(path.join(src, ".hoxi/models.json"), '{"secret":"k"}\n');
    fs.writeFileSync(path.join(src, ".hoxi/memory/notes.md"), "private\n");
    fs.writeFileSync(path.join(src, ".agents/skills/private/SKILL.md"), "private\n");
    fs.writeFileSync(path.join(src, "internal/app/work_bridge.go"), "package app\n");
    fs.writeFileSync(path.join(src, ".env.local"), "KEY=1\n");

    const run = spawnSync("bash", ["-c", `set -euo pipefail\nPROJECT_ROOT="$1"\nTARGET_ABS="$2"\n${rsyncCmd}`, "bash", src, dst], { encoding: "utf8" });
    assert.equal(run.status, 0, run.stderr);
    assert.equal(fs.existsSync(path.join(dst, ".hoxi/skills/tools/SKILL.md")), true);
    assert.equal(fs.existsSync(path.join(dst, ".hoxi/models.json")), false);
    assert.equal(fs.existsSync(path.join(dst, ".hoxi/memory")), false);
    assert.equal(fs.existsSync(path.join(dst, ".agents")), false);
    assert.equal(fs.existsSync(path.join(dst, "internal/app/work_bridge.go")), false);
    assert.equal(fs.existsSync(path.join(dst, ".env.local")), false);
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
});

test("Windows build script and Vite config support Work surface staging and Windows paths", () => {
  const repoRoot = path.join(scripts, "..");
  const winBuild = fs.readFileSync(path.join(repoRoot, "scripts/build-windows.ps1"), "utf8");
  assert.match(winBuild, /function Resolve-WorkRepoDir/);
  assert.match(winBuild, /function ConvertTo-GoWorkPath/);
  assert.match(winBuild, /HOXI_DISABLE_WORK_EXT/);
  assert.match(winBuild, /\$env:HOXI_WORK_DIR = \$WorkRepoDir/);
  assert.match(winBuild, /work_bridge\.go\.tmpl/);
  assert.match(winBuild, /stage-host\.sh/);
  assert.match(winBuild, /WORK_MODULES=\\\(/);
  assert.match(winBuild, /UTF8Encoding\(\$false\)/);
  assert.match(winBuild, /replace github\.com\/hoxiai\/hoxi v0\.0\.0 => \./);
  assert.match(winBuild, /Select-String -Path "web\/dist\/assets\/\*\.js" -SimpleMatch "modeExecuteLabel" -Quiet/);
  assert.match(winBuild, /go version -m "build\/bin\/Hoxi\.exe"/);
  assert.match(winBuild, /foreach \(\$stagedFile in \$StagedHostFiles\)/);

  const viteCfg = fs.readFileSync(path.join(repoRoot, "web/vite.config.ts"), "utf8");
  assert.match(viteCfg, /normalizeComparePath/);
  assert.match(viteCfg, /normalizeEnvPath/);
  assert.match(viteCfg, /process\.platform === "win32"/);
});

test("stage-host.sh writes Windows-compatible portable go.work paths and commercial release workflows gate Work", (t) => {
  const repoRoot = path.join(scripts, "..");
  const stageHostPath = path.join(repoRoot, "work/go/stage-host.sh");
  if (!fs.existsSync(stageHostPath)) {
    t.skip("Work checkout not present in OSS export");
    return;
  }

  const stageHostSrc = fs.readFileSync(stageHostPath, "utf8");
  assert.match(stageHostSrc, /to_go_work_dir\(\)/);
  assert.match(stageHostSrc, /cygpath -m/);
  assert.match(stageHostSrc, /pwd -W/);

  const crossWorkflow = fs.readFileSync(path.join(repoRoot, ".github/workflows/release-cross-platform.yml"), "utf8");
  assert.doesNotMatch(crossWorkflow, /default:\s*"v0\.2\.1"/);
  assert.match(crossWorkflow, /group:.*\bgithub\.ref_name\b/);
  assert.doesNotMatch(crossWorkflow, /group:.*\bgithub\.ref\b(?!_name)/);
  assert.match(crossWorkflow, /ANDROID_KEYSTORE_BASE64:\?/);
  assert.match(crossWorkflow, /tag_name: \$\{\{ env\.RELEASE_TAG_NAME \}\}/);
  for (const block of crossWorkflow.matchAll(/^        run: \|\n((?:          .*\n|\n)+)/gm)) {
    const shell = block[1].replace(/^          /gm, "");
    const check = spawnSync("bash", ["-n"], { input: shell, encoding: "utf8" });
    assert.equal(check.status, 0, check.stderr);
  }

  const desktopWorkflowPath = path.join(repoRoot, ".github/workflows/release.yml");
  if (fs.existsSync(desktopWorkflowPath)) {
    const desktopWorkflow = fs.readFileSync(desktopWorkflowPath, "utf8");
    assert.match(desktopWorkflow, /group:.*\bgithub\.ref_name\b/);
    assert.doesNotMatch(desktopWorkflow, /group:.*\bgithub\.ref\b(?!_name)/);
    assert.match(desktopWorkflow, /HOXI_WORK_TOKEN:\?[^\n]*desktop releases/);
    assert.match(desktopWorkflow, /HOXI_WORK_TOKEN cannot access/);
    assert.match(desktopWorkflow, /HOXI_WORK_TOKEN:\?[^\n]*macOS releases/);
    assert.match(desktopWorkflow, /HOXI_WORK_TOKEN:\?[^\n]*Windows releases/);
    assert.match(desktopWorkflow, /HOXI_WORK_TOKEN:\?[^\n]*Linux releases/);
    assert.match(desktopWorkflow, /persist-credentials: false/);
    assert.match(desktopWorkflow, /git -C "\$\{RUNNER_TEMP:-\/tmp\}" -c "http\.https:\/\/github\.com\/\.extraheader=" -c "http\.https:\/\/github\.com\/\.extraheader=\$\{WORK_AUTH\}" ls-remote "\$WORK_REPO_URL"/);
    assert.match(desktopWorkflow, /ls-remote "\$WORK_REPO_URL"/);
    assert.match(desktopWorkflow, /fetch --depth 1 origin "\$WORK_SHA"/);
    assert.doesNotMatch(desktopWorkflow, /clone --depth 1 https:\/\/github\.com\/hoxiai\/hoxi-ext\.git \.\.\/hoxi-ext/);
    assert.doesNotMatch(desktopWorkflow, /default:\s*"v0\.2\.1"/);
    assert.match(desktopWorkflow, /cygpath -m "\$WORK_DIR"/);
    assert.match(desktopWorkflow, /GOWORK="\$GO_WORK_FILE" go vet \.\/\.\.\./);
    assert.match(desktopWorkflow, /grep -q "modeExecuteLabel" web\/dist\/assets\/\*\.js/);
    assert.match(desktopWorkflow, /go version -m "build\/bin\/Hoxi\.app\/Contents\/MacOS\/Hoxi" \| grep -Fq "\$BRIDGE_PKG"/);
    assert.match(desktopWorkflow, /go version -m build\/bin\/Hoxi\.exe \| grep -Fq "\$BRIDGE_PKG"/);
    assert.match(desktopWorkflow, /go version -m build\/bin\/Hoxi \| grep -Fq "\$BRIDGE_PKG"/);
    assert.match(desktopWorkflow, /git\/ref\/tags\/\$\{encodeURIComponent\(tag\)\}/);
    assert.match(desktopWorkflow, /res\.status === 401 \|\| res\.status === 403/);
    assert.match(desktopWorkflow, /res\.status !== 404 \|\| Date\.now\(\) >= deadline/);
    assert.doesNotMatch(desktopWorkflow, /if \[\[ -f "internal\/app\/work_bridge\.go" \]\]/);
    assert.match(desktopWorkflow, /ref: \$\{\{ needs\.preflight\.outputs\.release_tag \}\}/);
    assert.match(desktopWorkflow, /repository: \$\{\{ env\.RELEASE_TARGET_REPO \}\}/);
    assert.match(desktopWorkflow, /tag_name: \$\{\{ env\.RELEASE_TAG_NAME \}\}/);
  }

  const workCIPath = path.join(repoRoot, ".github/workflows/ci-work-integration.yml");
  if (fs.existsSync(workCIPath)) {
    const workCI = fs.readFileSync(workCIPath, "utf8");
    assert.match(workCI, /clone --depth 1 https:\/\/github\.com\/hoxiai\/hoxi-ext\.git work/);
    assert.match(workCI, /npm --prefix web run lint/);
    assert.match(workCI, /npm --prefix web run build/);
    assert.match(workCI, /grep -q "modeExecuteLabel" web\/dist\/assets\/\*\.js/);
    assert.match(workCI, /go run \.\/tools\/contractcheck/);
    for (const block of workCI.matchAll(/^        run: \|\n((?:          .*\n|\n)+)/gm)) {
      const shell = block[1].replace(/^          /gm, "");
      const check = spawnSync("bash", ["-n"], { input: shell, encoding: "utf8" });
      assert.equal(check.status, 0, check.stderr);
    }
  }

  // Release notes delimiter framing must survive files lacking a trailing newline or using CRLF.
  const notesTmp = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-release-notes-eof-"));
  t.after(() => fs.rmSync(notesTmp, { recursive: true, force: true }));
  const noEofNotes = path.join(notesTmp, "notes.md");
  fs.writeFileSync(noEofNotes, "Line 1\r\nLine 2 without newline");
  const awkCheck = spawnSync("bash", ["-c", 'awk \'{sub(/\\r$/, ""); print}\' "$1" && printf "DELIM\\n"', "bash", noEofNotes], { encoding: "utf8" });
  assert.equal(awkCheck.status, 0, awkCheck.stderr);
  assert.equal(awkCheck.stdout, "Line 1\nLine 2 without newline\nDELIM\n");

  const verifyOssPath = path.join(repoRoot, "client/scripts/verify_oss_root.py");
  if (fs.existsSync(verifyOssPath)) {
    const ossStage = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-oss-workflows-"));
    t.after(() => fs.rmSync(ossStage, { recursive: true, force: true }));
    fs.mkdirSync(path.join(ossStage, ".github/workflows"), { recursive: true });
    fs.writeFileSync(path.join(ossStage, ".github/workflows/release.yml"), "name: Release Desktop\n");
    fs.writeFileSync(path.join(ossStage, ".github/workflows/ci-work-integration.yml"), "name: CI Work Integration\n");
    fs.copyFileSync(path.join(repoRoot, ".github/workflows/release-cross-platform.yml"), path.join(ossStage, ".github/workflows/release-cross-platform.yml"));
    const pyRun = spawnSync("python3", [verifyOssPath, ossStage], { encoding: "utf8" });
    assert.equal(pyRun.status, 0, pyRun.stderr || pyRun.stdout);
    assert.equal(fs.existsSync(path.join(ossStage, ".github/workflows/release.yml")), false, "OSS export must remove release.yml so public mirror cannot overwrite official desktop builds");
    assert.equal(fs.existsSync(path.join(ossStage, ".github/workflows/ci-work-integration.yml")), false, "OSS export must remove ci-work-integration.yml");
    const ossCross = fs.readFileSync(path.join(ossStage, ".github/workflows/release-cross-platform.yml"), "utf8");
    assert.match(ossCross, /^name: Release Hoxi Remote \(Android\)$/m);
    assert.match(ossCross, /^  build-mobile-android:$/m);
    assert.doesNotMatch(ossCross, /^  build-(windows|linux):$/m);
    assert.doesNotMatch(ossCross, /HOXI_WORK_TOKEN|HOXI_RELEASE_REPO|HOXI_PUBLIC_RELEASE_TOKEN/);
    for (const block of ossCross.matchAll(/^        run: \|\n((?:          .*\n|\n)+)/gm)) {
      const shell = block[1].replace(/^          /gm, "");
      const check = spawnSync("bash", ["-n"], { input: shell, encoding: "utf8" });
      assert.equal(check.status, 0, check.stderr);
    }
  }

  const modMatch = stageHostSrc.match(/WORK_MODULES=\(([^)]+)\)/);
  assert.ok(modMatch, "Expected WORK_MODULES declaration in stage-host.sh");
  const workModules = modMatch[1].trim().split(/\s+/).filter(Boolean);
  assert.ok(workModules.length > 0, "Expected at least one Work module in stage-host.sh");

  const sandbox = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-stage-host-"));
  t.after(() => fs.rmSync(sandbox, { recursive: true, force: true }));

  const seedModules = (workGoDir) => {
    fs.mkdirSync(workGoDir, { recursive: true });
    fs.copyFileSync(stageHostPath, path.join(workGoDir, "stage-host.sh"));
    fs.writeFileSync(path.join(workGoDir, "work_bridge.go.tmpl"), "//go:build !oss\npackage app\n");
    for (const mod of workModules) {
      fs.mkdirSync(path.join(workGoDir, mod), { recursive: true });
      fs.writeFileSync(path.join(workGoDir, mod, "go.mod"), `module example.com/${mod}\ngo 1.25.0\n`);
    }
  };

  // Case 1: Nested work/go inside host checkout -> ./work/go/<mod>
  const nestedHost = path.join(sandbox, "nested-host");
  fs.mkdirSync(path.join(nestedHost, "internal/app"), { recursive: true });
  fs.writeFileSync(path.join(nestedHost, "go.mod"), "module example.com/host\ngo 1.25.0\n");
  seedModules(path.join(nestedHost, "work/go"));
  const nestedRun = spawnSync("bash", [path.join(nestedHost, "work/go/stage-host.sh"), nestedHost], { encoding: "utf8" });
  assert.equal(nestedRun.status, 0, nestedRun.stderr);
  const nestedGoWork = fs.readFileSync(path.join(nestedHost, "go.work"), "utf8");
  for (const mod of workModules) {
    assert.ok(nestedGoWork.includes(`"./work/go/${mod}"`), `Expected ./work/go/${mod} in nested go.work`);
  }
  assert.doesNotMatch(nestedGoWork, /replace github\.com\/hoxiai\/hoxi/);
  assert.equal(fs.existsSync(path.join(nestedHost, "internal/app/work_bridge.go")), true);

  // Case 2: Sibling ../hoxi-ext/go beside host checkout -> ../hoxi-ext/go/<mod> + versioned host replace
  const siblingParent = path.join(sandbox, "ci-layout");
  const siblingHost = path.join(siblingParent, "hoxi");
  const siblingWorkGo = path.join(siblingParent, "hoxi-ext/go");
  fs.mkdirSync(path.join(siblingHost, "internal/app"), { recursive: true });
  fs.writeFileSync(path.join(siblingHost, "go.mod"), "module example.com/host\ngo 1.25.0\n");
  seedModules(siblingWorkGo);
  const siblingRun = spawnSync("bash", [path.join(siblingWorkGo, "stage-host.sh"), siblingHost], { encoding: "utf8" });
  assert.equal(siblingRun.status, 0, siblingRun.stderr);
  const siblingGoWork = fs.readFileSync(path.join(siblingHost, "go.work"), "utf8");
  for (const mod of workModules) {
    assert.ok(siblingGoWork.includes(`"../hoxi-ext/go/${mod}"`), `Expected ../hoxi-ext/go/${mod} in sibling go.work`);
  }
  assert.match(siblingGoWork, /replace github\.com\/hoxiai\/hoxi v0\.0\.0 => \./);
  assert.doesNotMatch(siblingGoWork, /"\/[a-z]\//i);
});

test("CDN mirror config fails closed on partial env, signs SigV4, skips identical digests, and publishes CDN + GitHub mirrors", async (t) => {
  assert.equal(hasCdnConfig({}), false);
  assert.throws(() => hasCdnConfig({ HOXI_CDN_PUBLIC_URL: "https://cdn.hoxi.dev" }), /Incomplete CDN/);
  assert.throws(() => resolveCdnConfig({
    HOXI_CDN_ENDPOINT: "http://insecure.r2.local",
    HOXI_CDN_BUCKET: "hoxi",
    HOXI_CDN_ACCESS_KEY_ID: "ak",
    HOXI_CDN_SECRET_ACCESS_KEY: "sk",
    HOXI_CDN_PUBLIC_URL: "https://cdn.hoxi.dev",
  }), /HTTPS/);

  const env = {
    HOXI_CDN_ENDPOINT: "https://acc.r2.cloudflarestorage.com",
    HOXI_CDN_BUCKET: "hoxi",
    HOXI_CDN_ACCESS_KEY_ID: "token-id",
    HOXI_CDN_TOKEN: "cfut_secret_token",
    HOXI_CDN_PUBLIC_URL: "https://cdn.hoxi.dev/",
  };
  assert.equal(hasCdnConfig(env), true);
  assert.equal(resolveCdnBaseUrl(env), "https://cdn.hoxi.dev/hoxi-desktop");
  assert.equal(
    resolveSparkleEnclosureURL("0.7.3", env),
    "https://cdn.hoxi.dev/hoxi-desktop/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg"
  );
  assert.equal(
    resolveSparkleEnclosureURL("0.7.3", {}),
    "https://github.com/hoxiai/hoxi/releases/download/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg"
  );

  const cfg = resolveCdnConfig(env);
  const req = signedRequest({
    method: "PUT",
    ...cfg,
    key: "hoxi-desktop/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg",
    body: Buffer.from("payload"),
    headers: { "x-amz-meta-sha256": "a".repeat(64) },
  });
  assert.match(req.init.headers.authorization, /^AWS4-HMAC-SHA256 Credential=token-id\/\d{8}\/auto\/s3\/aws4_request/);
  assert.match(req.init.headers.authorization, /x-amz-meta-sha256/);
  assert.equal(req.url, "https://acc.r2.cloudflarestorage.com/hoxi/hoxi-desktop/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg");

  // Virtual-hosted-style S3 endpoints (e.g. OSS/COS) auto-detect bucket host prefix without duplicating /bucket/.
  const vhCfg = resolveCdnConfig({
    ...env,
    HOXI_CDN_ENDPOINT: "https://hoxi-dl.oss-cn-hangzhou.aliyuncs.com",
    HOXI_CDN_BUCKET: "hoxi-dl",
    HOXI_CDN_REGION: "oss-cn-hangzhou",
  });
  assert.equal(vhCfg.addressingStyle, "virtual");
  const vhReq = signedRequest({
    method: "PUT",
    ...vhCfg,
    key: "hoxi-desktop/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg",
    body: Buffer.from("payload"),
  });
  assert.equal(vhReq.url, "https://hoxi-dl.oss-cn-hangzhou.aliyuncs.com/hoxi-desktop/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg");

  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-cdn-sync-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const files = desktopReleaseFiles("0.7.3");
  assert.equal(files.length, 16);
  for (const item of files) {
    fs.writeFileSync(path.join(dir, item.name), `content:${item.name}`);
  }

  const stored = new Map();
  const mockFetch = async (url, init) => {
    if (init.method === "HEAD") {
      const hit = stored.get(url);
      if (!hit) return new Response(null, { status: 404 });
      return new Response(null, {
        status: 200,
        headers: { "x-amz-meta-sha256": hit.sha256, "content-length": String(hit.bytes.length) },
      });
    }
    if (init.method === "PUT") {
      const bytes = Buffer.from(init.body);
      stored.set(url, { bytes, sha256: init.headers["x-amz-meta-sha256"] });
      return new Response(null, { status: 200 });
    }
    return new Response(null, { status: 405 });
  };

  const first = await syncDesktopRelease({ version: "0.7.3", dir, config: cfg, fetchImpl: mockFetch });
  assert.equal(first.uploaded, 16);
  assert.equal(first.skipped, 0);
  assert.equal(first.cdnBaseUrl, "https://cdn.hoxi.dev/hoxi-desktop");

  // Re-running with identical bytes skips all 16 uploads without overwriting.
  const second = await syncDesktopRelease({ version: "0.7.3", dir, config: cfg, fetchImpl: mockFetch });
  assert.equal(second.uploaded, 0);
  assert.equal(second.skipped, 16);

  // Tampered overwrite attempt must fail closed.
  fs.writeFileSync(path.join(dir, "checksums.txt"), "mutated-bytes");
  await assert.rejects(
    uploadToCdn({
      config: cfg,
      file: path.join(dir, "checksums.txt"),
      key: "hoxi-desktop/v0.7.3/checksums.txt",
      fetchImpl: async (url, init) => {
        if (init.method === "HEAD") return new Response(null, { status: 200, headers: { "x-amz-meta-sha256": "0".repeat(64) } });
        if (init.method === "GET") return new Response(stored.get(url).bytes, { status: 200 });
        return new Response(null, { status: 500 });
      },
    }),
    /different bytes/
  );

  // Sparkle and releasePayload accept cdn.hoxi.dev while preserving GitHub mirror metadata.
  const cdnSparkle = {
    ...sparkle,
    version: "0.7.3",
    url: "https://cdn.hoxi.dev/hoxi-desktop/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg",
  };
  const cdnPayload = releasePayload({
    version: "0.7.3",
    notes: "CDN release",
    sha256: "a".repeat(64),
    size: 1234,
    sparkle: cdnSparkle,
    cdnBaseUrl: "https://cdn.hoxi.dev/hoxi-desktop",
    assetsMeta: {
      "Hoxi-0.7.3-darwin-amd64.dmg": { sha256: "b".repeat(64), size: 2345 },
      "Hoxi-0.7.3-windows-amd64-setup.exe": { sha256: "c".repeat(64), size: 3456 },
    },
  });
  const macArm = cdnPayload.assets.find((a) => a.platform === "macos" && a.arch === "arm64");
  const macIntel = cdnPayload.assets.find((a) => a.platform === "macos" && a.arch === "x64");
  assert.equal(macArm.browser_download_url, "https://cdn.hoxi.dev/hoxi-desktop/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg");
  assert.equal(macArm.mirrors.cdn, macArm.browser_download_url);
  assert.equal(macArm.mirrors.global, "https://github.com/hoxiai/hoxi/releases/download/v0.7.3/Hoxi-0.7.3-darwin-arm64.dmg");
  assert.equal(macIntel.sha256, "b".repeat(64));
  assert.equal(macIntel.size, 2345);
  assert.equal(cdnPayload.metaData.mirrors["windows-x64-zip"].cdn, "https://cdn.hoxi.dev/hoxi-desktop/v0.7.3/Hoxi-0.7.3-windows-amd64.zip");
  assert.equal(cdnPayload.metaData.mirrors["windows-x64-zip"].global, "https://github.com/hoxiai/hoxi/releases/download/v0.7.3/Hoxi-0.7.3-windows-amd64.zip");
});

test("package-dmg.sh sizes HFS+ DMG explicitly with headroom to avoid hdiutil ENOSPC", () => {
  const pkgScript = path.join(scripts, "package-dmg.sh");
  const src = fs.readFileSync(pkgScript, "utf8");
  const syntax = spawnSync("bash", ["-n", pkgScript], { encoding: "utf8" });
  assert.equal(syntax.status, 0, syntax.stderr);
  assert.match(src, /-fs HFS\+/);
  assert.match(src, /-size "\$\{DMG_SIZE_MB\}m"/);
});

test("Windows NSIS installer uses valid ASCII ${U+XXXX} escapes and compiles with zero warnings", () => {
  const installerDir = path.join(scripts, "../build/windows/installer");
  const nsiPath = path.join(installerDir, "project.nsi");
  const src = fs.readFileSync(nsiPath, "utf8");

  // Keep project.nsi pure ASCII so Windows makensis under CP1252 cannot corrupt strings.
  assert.match(src, /^[\x00-\x7F]*$/, "project.nsi must remain pure ASCII");
  assert.doesNotMatch(src, /\$\\\{U\+/, "project.nsi must not backslash-escape NSIS ${U+XXXX} codepoints");
  assert.ok(src.includes('WriteRegStr SHCTX "Software\\Classes\\hoxi\\shell\\open\\command" "" \'"$INSTDIR\\${PRODUCT_EXECUTABLE}" "%1"\''),
    "hoxi:// must register the executable and URI as separate quoted arguments");
  assert.doesNotMatch(src, /!insertmacro wails\.associateCustomProtocols/);

  const decodeNsisUnicode = (raw) => raw.replace(/\$\{U\+([0-9A-Fa-f]{4,6})\}/g, (_, hex) => String.fromCodePoint(parseInt(hex, 16)));
  const langStrings = new Map();
  for (const match of src.matchAll(/^LangString\s+(\S+)\s+\$\{(LANG_[A-Z]+)\}\s+"([^"]*)"/gm)) {
    const [, key, lang, value] = match;
    if (!langStrings.has(key)) langStrings.set(key, {});
    langStrings.get(key)[lang] = decodeNsisUnicode(value);
  }
  assert.ok(langStrings.size >= 13, `expected at least 13 LangString keys, got ${langStrings.size}`);
  for (const [key, locales] of langStrings) {
    assert.ok(locales.LANG_SIMPCHINESE, `${key} missing LANG_SIMPCHINESE`);
    assert.ok(locales.LANG_ENGLISH, `${key} missing LANG_ENGLISH`);
    assert.doesNotMatch(locales.LANG_SIMPCHINESE, /U\+[0-9A-Fa-f]{4}/, `${key} zh-CN contains unexpanded codepoint`);
    assert.doesNotMatch(locales.LANG_ENGLISH, /U\+[0-9A-Fa-f]{4}/, `${key} en-US contains unexpanded codepoint`);
  }
  assert.equal(langStrings.get("HOXI_SUBTITLE").LANG_SIMPCHINESE, "极速、轻量的原生 AI 工程指挥中心");
  assert.equal(langStrings.get("HOXI_INSTALL_NOW").LANG_SIMPCHINESE, "立即安装");
  assert.equal(langStrings.get("HOXI_CUSTOM_COLLAPSED").LANG_SIMPCHINESE, "自定义选项 ▾");
  assert.equal(langStrings.get("HOXI_CUSTOM_COLLAPSED").LANG_ENGLISH, "Custom Options ▾");
  assert.equal(langStrings.get("HOXI_CUSTOM_EXPANDED").LANG_SIMPCHINESE, "收起选项 ▴");
  assert.equal(langStrings.get("HOXI_CUSTOM_EXPANDED").LANG_ENGLISH, "Hide Options ▴");
  assert.equal(langStrings.get("HOXI_LAUNCH_NOW").LANG_SIMPCHINESE, "立即体验");

  const makensisCandidates = [
    "makensis",
    "/opt/homebrew/bin/makensis",
    "/usr/local/bin/makensis",
    "C:\\Program Files (x86)\\NSIS\\makensis.exe",
  ];
  const makensisBin = makensisCandidates.find((bin) => spawnSync(bin, ["-VERSION"], { encoding: "utf8" }).status === 0);
  if (!makensisBin) return;

  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-nsis-check-"));
  try {
    const tmpInst = path.join(dir, "build", "windows", "installer");
    fs.mkdirSync(tmpInst, { recursive: true });
    fs.mkdirSync(path.join(dir, "build", "bin"), { recursive: true });
    fs.copyFileSync(path.join(scripts, "../build/windows/icon.ico"), path.join(dir, "build/windows/icon.ico"));
    for (const file of ["banner.bmp", "header.bmp", "wizard.bmp", "project.nsi"]) {
      fs.copyFileSync(path.join(installerDir, file), path.join(tmpInst, file));
    }
    for (const notice of ["LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md"]) {
      fs.writeFileSync(path.join(dir, notice), "stub\n");
    }
    fs.writeFileSync(
      path.join(tmpInst, "wails_tools.nsh"),
      [
        '!define INFO_PROJECTNAME "Hoxi"',
        '!define INFO_PRODUCTNAME "Hoxi"',
        '!define INFO_PRODUCTVERSION "0.7.5"',
        '!define INFO_COPYRIGHT "Copyright Hoxi"',
        '!define PRODUCT_EXECUTABLE "Hoxi.exe"',
        '!define ARCH "amd64"',
        "!macro wails.checkArchitecture", "!macroend",
        "!macro wails.setShellContext", "!macroend",
        "!macro wails.webview2runtime", "!macroend",
        "!macro wails.files", "!macroend",
        "!macro wails.associateFiles", "!macroend",
        "!macro wails.writeUninstaller", 'WriteUninstaller "$INSTDIR\\uninstall.exe"', "!macroend",
        "!macro wails.unassociateFiles", "!macroend",
        "!macro wails.deleteUninstaller", "!macroend",
      ].join("\n") + "\n",
    );

    const compile = spawnSync(
      makensisBin,
      ["-WX", "-V3", "-XSetCompress off", path.join(tmpInst, "project.nsi")],
      { encoding: "utf8" },
    );
    assert.equal(compile.status, 0, `makensis failed:\n${compile.stdout}\n${compile.stderr}`);
    assert.doesNotMatch(compile.stdout + compile.stderr, /warning 6000/i);

    const exeBytes = fs.readFileSync(path.join(dir, "build/bin/Hoxi-amd64-installer.exe"));
    assert.ok(exeBytes.includes(Buffer.from('\\Hoxi.exe" "%1"', "utf16le")), "compiled registry command lost its quotes");
    for (const expected of ["极速、轻量的原生 AI 工程指挥中心", "立即安装", "自定义选项 ▾", "Custom Options ▾", "立即体验"]) {
      assert.ok(
        exeBytes.includes(Buffer.from(expected, "utf16le")),
        `compiled installer missing UTF-16LE string: ${expected}`,
      );
    }
    assert.equal(exeBytes.includes(Buffer.from("U+6781", "utf16le")), false, "compiled installer contains raw U+6781");
    assert.equal(exeBytes.includes(Buffer.from("U+6781", "ascii")), false, "compiled installer contains ASCII U+6781");
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test("check-oss-hygiene.py passes on tracked host files and rejects secrets, private paths, and personal home dirs", (t) => {
  const repoRoot = path.join(scripts, "..");
  const hygieneScript = path.join(scripts, "check-oss-hygiene.py");
  const liveRun = spawnSync("python3", [hygieneScript], { cwd: repoRoot, encoding: "utf8" });
  assert.equal(liveRun.status, 0, liveRun.stderr || liveRun.stdout);
  assert.match(liveRun.stdout, /OSS_HYGIENE_OK/);

  const fixtureDir = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-oss-hygiene-"));
  t.after(() => fs.rmSync(fixtureDir, { recursive: true, force: true }));
  fs.mkdirSync(path.join(fixtureDir, ".ai"), { recursive: true });
  fs.mkdirSync(path.join(fixtureDir, "internal/app"), { recursive: true });
  fs.mkdirSync(path.join(fixtureDir, ".github/workflows"), { recursive: true });
  fs.writeFileSync(path.join(fixtureDir, ".env"), "SECRET=1\n");
  fs.writeFileSync(path.join(fixtureDir, "go.work"), "go 1.25.0\n");
  fs.writeFileSync(path.join(fixtureDir, "internal/app/work_bridge.go"), "package app\n");
  fs.writeFileSync(path.join(fixtureDir, ".ai/STATE.md"), "# state\n");
  fs.writeFileSync(path.join(fixtureDir, ".github/workflows/release.yml"), "name: Release\n");
  fs.writeFileSync(path.join(fixtureDir, "notes.md"), "Local path: /Users/privateuser/code/app\n");

  const failRun = spawnSync("python3", [hygieneScript, "--dir", fixtureDir], { encoding: "utf8" });
  assert.notEqual(failRun.status, 0, "Expected check-oss-hygiene.py to reject leaked fixture files");
  assert.match(failRun.stderr, /\.env:/);
  assert.match(failRun.stderr, /go\.work:/);
  assert.match(failRun.stderr, /internal\/app\/work_bridge\.go:/);
  assert.match(failRun.stderr, /\.ai\/STATE\.md:/);
  assert.match(failRun.stderr, /\.github\/workflows\/release\.yml:/);
  assert.match(failRun.stderr, /hardcoded personal home path for "privateuser"/);
});
