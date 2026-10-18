#!/usr/bin/env node
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { sparkleConfig, machoFiles, frameworkFiles } from "./sparkle.mjs";

export const minimumSupportedMacOS = "13.0";

export function normalizeVersion(input) {
  const version = String(input ?? "").trim().replace(/^v/i, "");
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version)) {
    throw new Error("Version must be a complete major.minor.patch release (e.g. 0.2.1)");
  }
  return version;
}

export function maximumMacOS(versions) {
  for (const version of versions) {
    if (!/^\d+\.\d+(?:\.\d+)?$/.test(version)) throw new Error(`Invalid macOS version: ${version}`);
  }
  return versions.reduce((highest, version) => {
    const left = highest.split(".").map(Number);
    const right = version.split(".").map(Number);
    for (let index = 0; index < 3; index++) {
      if ((left[index] || 0) !== (right[index] || 0)) {
        return (left[index] || 0) > (right[index] || 0) ? highest : version;
      }
    }
    return highest;
  });
}

export function minimumFromMachO(output) {
  const versions = [...output.matchAll(/cmd LC_BUILD_VERSION[\s\S]*?\bminos\s+(\d+\.\d+(?:\.\d+)?)/g)]
    .map((match) => match[1]);
  versions.push(...[...output.matchAll(/cmd LC_VERSION_MIN_MACOSX\s+cmdsize\s+\d+\s+version\s+(\d+\.\d+(?:\.\d+)?)/g)]
    .map((match) => match[1]));
  if (!versions.length) throw new Error("Cannot determine the binary's minimum macOS version");
  return maximumMacOS(versions);
}

function run(command, args, input) {
  return execFileSync(command, args, { encoding: "utf8", input, stdio: ["pipe", "pipe", "pipe"], timeout: 120_000 });
}

function readInfo(app) {
  return JSON.parse(run("plutil", ["-convert", "json", "-o", "-", path.join(app, "Contents/Info.plist")]));
}

function inspectBinaries(app, info) {
  assert.equal(path.basename(info.CFBundleExecutable), info.CFBundleExecutable, "Invalid app executable");
  const main = path.join(app, "Contents/MacOS", info.CFBundleExecutable);
  const sidecar = path.join(app, "Contents/MacOS/opencode");
  const framework = path.join(app, "Contents/Frameworks/Sparkle.framework");
  const config = sparkleConfig();
  if (info.SUFeedURL || info.SUPublicEDKey || (config && info.CFBundleIdentifier === "ai.hoxi.desktop")) {
    const bundled = sparkleConfig({ HOXI_SPARKLE_DIR: ".", HOXI_SPARKLE_PUBLIC_KEY: info.SUPublicEDKey,
      HOXI_SPARKLE_FEED_URL: info.SUFeedURL });
    assert.ok(bundled, "Missing bundled Sparkle metadata");
    if (config) {
      assert.equal(bundled.publicKey, config.publicKey);
      assert.equal(bundled.feedURL, config.feedURL);
    }
    frameworkFiles(framework);
  } else assert.ok(!fs.existsSync(framework), "Unconfigured Sparkle framework");
  const binaries = [main, sidecar, ...(fs.existsSync(framework) ? machoFiles(framework) : [])];
  const minimum = maximumMacOS([minimumSupportedMacOS, info.LSMinimumSystemVersion, ...binaries.map((binary) => {
    assert.ok(fs.statSync(binary).isFile(), `Missing executable: ${binary}`);
    const architectures = run("lipo", ["-archs", binary]).trim().split(/\s+/);
    assert.ok(architectures.includes("arm64"), `${binary} must contain arm64 code`);
    return minimumFromMachO(run("otool", ["-l", binary]));
  })]);
  return { main, minimum };
}

export function validateAppMetadata(info, expectedVersion, requiredMinimum) {
  const version = normalizeVersion(expectedVersion);
  assert.equal(info.CFBundleIdentifier, "ai.hoxi.desktop", "Expected the production Hoxi bundle");
  assert.ok(info.CFBundleURLTypes?.some((type) => type.CFBundleURLSchemes?.includes("hoxi")),
    "App must register the hoxi URL scheme");
  assert.equal(info.CFBundleShortVersionString, version, "App display version differs from release version");
  assert.equal(info.CFBundleVersion, version, "App build version differs from release version");
  assert.equal(maximumMacOS([info.LSMinimumSystemVersion, requiredMinimum]), info.LSMinimumSystemVersion,
    "App minimum macOS is lower than a bundled executable requires");
}

export function configureApp(app, version) {
  version = normalizeVersion(version);
  const info = readInfo(app);
  const { minimum } = inspectBinaries(app, info);
  for (const [key, value] of Object.entries({
    CFBundleVersion: version, CFBundleShortVersionString: version, LSMinimumSystemVersion: minimum,
  })) {
    run("/usr/libexec/PlistBuddy", ["-c", `Set :${key} ${value}`, path.join(app, "Contents/Info.plist")]);
  }
}

export function verifyApp(app, version) {
  const info = readInfo(app);
  const { main, minimum } = inspectBinaries(app, info);
  validateAppMetadata(info, version, minimum);
  run("codesign", ["--verify", "--deep", "--strict", app]);
  assert.equal(run(main, ["--version"]).trim(), normalizeVersion(version),
    "Embedded runtime version differs from release version");
  return { version: normalizeVersion(version), minimumMacOS: info.LSMinimumSystemVersion, architecture: "arm64" };
}

export function verifyDMG(dmg, version) {
  const mount = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-verify-dmg-"));
  let attached = false;
  let metadata;
  try {
    run("hdiutil", ["attach", "-readonly", "-nobrowse", "-mountpoint", mount, path.resolve(dmg)]);
    attached = true;
    metadata = verifyApp(path.join(mount, "Hoxi.app"), version);
  } finally {
    // Never recursively remove a mount point: detach must succeed first.
    if (attached) run("hdiutil", ["detach", mount]);
    fs.rmdirSync(mount);
  }
  return {
    ...metadata,
    sha256: crypto.createHash("sha256").update(fs.readFileSync(dmg)).digest("hex"),
    size: fs.statSync(dmg).size,
  };
}

export function validatePublication(result) {
  assert.ok(result && result.success === true && ["api", "db"].includes(result.mode),
    "Publisher must report success=true and mode=api or db; skipped/dry-run is not a publication");
  assert.notEqual(result.publication, "skipped", "Publisher skipped publication");
  assert.notEqual(result.dryRun, true, "Publisher only performed a dry run");
  return result;
}

function main([command, ...args]) {
  if (command === "version") console.log(normalizeVersion(args[0]));
  else if (command === "default-version") {
    console.log(normalizeVersion(JSON.parse(fs.readFileSync("wails.json", "utf8")).info.productVersion));
  } else if (command === "configure-app") configureApp(args[0], args[1]);
  else if (command === "verify-app") console.log(JSON.stringify(verifyApp(args[0], args[1])));
  else if (command === "verify-dmg") console.log(JSON.stringify(verifyDMG(args[0], args[1])));
  else if (command === "verify-publication") {
    console.log(JSON.stringify(validatePublication(JSON.parse(fs.readFileSync(0, "utf8")))));
  } else if (command === "verify-distribution") {
    const details = spawnSync("codesign", ["-dv", "--verbose=4", args[0]], { encoding: "utf8", timeout: 30_000 });
    assert.equal(details.status, 0, "The DMG must be signed with Developer ID before publishing");
    const signature = details.stdout + details.stderr;
    assert.ok(signature.includes("Authority=Developer ID Application:"), "A Developer ID signature is required to publish");
    run("codesign", ["--verify", "--strict", args[0]]);
    run("xcrun", ["stapler", "validate", args[0]]);
  } else throw new Error("Unknown release artifact command");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { main(process.argv.slice(2)); }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
