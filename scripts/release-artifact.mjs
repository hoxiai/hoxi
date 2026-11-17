#!/usr/bin/env node
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { verifyBinaryVersion } from "./opencode-lock.mjs";
import { sparkleConfig, validateSparkleApp, machoFiles, frameworkFiles } from "./sparkle.mjs";

export const minimumSupportedMacOS = "13.0";

// Map the release artifact architecture suffix (Go naming) to the Mach-O
// architecture name reported by `lipo`/`otool`.
const MACHO_ARCH_NAMES = { arm64: "arm64", amd64: "x86_64" };

export function machoArchName(arch = "arm64") {
  const name = MACHO_ARCH_NAMES[arch];
  assert.ok(name, `Unsupported release architecture: ${arch}`);
  return name;
}

// The release architecture of the machine running verification, used to decide
// whether bundled binaries can be executed natively or only inspected.
function hostReleaseArch() {
  if (process.arch === "x64") return "amd64";
  if (process.arch === "arm64") return "arm64";
  return process.arch;
}

export function normalizeVersion(input) {
  const version = String(input ?? "").trim().replace(/^v/i, "");
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version)) {
    throw new Error("Version must be a complete major.minor.patch release (e.g. 0.2.1)");
  }
  return version;
}

// Sparkle compares the feed's sparkle:version against the bundle's
// CFBundleVersion, so the build number must be a monotonic numeric code and the
// display version stays the semantic version.
export function versionCode(input) {
  const [major, minor, patch] = normalizeVersion(input).split(".").map(Number);
  return major * 1_000_000 + minor * 1_000 + patch;
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

export function sidecarPolicy(env = process.env) {
  const raw = String(env?.HOXI_BUNDLE_OPENCODE ?? "").trim().toLowerCase();
  if (raw === "" || raw === "auto") return "optional";
  if (raw === "0" || raw === "false") return "forbidden";
  if (raw === "1" || raw === "true") return "required";
  throw new Error(
    `Invalid HOXI_BUNDLE_OPENCODE value: ${JSON.stringify(env?.HOXI_BUNDLE_OPENCODE)} (expected 0, 1, or auto)`,
  );
}

export function inspectBinaries(app, info, arch = "arm64", env = process.env) {
  assert.equal(path.basename(info.CFBundleExecutable), info.CFBundleExecutable, "Invalid app executable");
  const archName = machoArchName(arch);
  const policy = sidecarPolicy(env);
  const main = path.join(app, "Contents/MacOS", info.CFBundleExecutable);
  const sidecarCandidate = path.join(app, "Contents/MacOS/opencode");
  const sidecarStat = fs.lstatSync(sidecarCandidate, { throwIfNoEntry: false });
  if (policy === "forbidden") {
    assert.equal(
      sidecarStat,
      undefined,
      `Unexpected bundled OpenCode sidecar when HOXI_BUNDLE_OPENCODE=0: ${sidecarCandidate}`,
    );
  } else if (policy === "required") {
    assert.ok(sidecarStat?.isFile(), `Missing executable: ${sidecarCandidate}`);
  } else if (sidecarStat) {
    assert.ok(sidecarStat.isFile(), `Invalid bundled OpenCode sidecar: ${sidecarCandidate}`);
  }
  const sidecar = sidecarStat ? sidecarCandidate : null;
  const framework = path.join(app, "Contents/Frameworks/Sparkle.framework");
  // The Intel bundle intentionally ships without Sparkle: the published appcast
  // carries a single arm64 enclosure, so a bundled updater would push arm64 to
  // an Intel Mac. It relies on the arch-aware JSON/self-updater instead.
  const config = arch === "amd64" ? null : sparkleConfig(env);
  if (validateSparkleApp(info, config)) {
    frameworkFiles(framework);
  } else assert.ok(!fs.existsSync(framework), "Unconfigured Sparkle framework");
  const binaries = [main, ...(sidecar ? [sidecar] : []), ...(fs.existsSync(framework) ? machoFiles(framework) : [])];
  const minimum = maximumMacOS([minimumSupportedMacOS, info.LSMinimumSystemVersion, ...binaries.map((binary) => {
    assert.ok(fs.statSync(binary).isFile(), `Missing executable: ${binary}`);
    const architectures = run("lipo", ["-archs", binary]).trim().split(/\s+/);
    assert.ok(architectures.includes(archName), `${binary} must contain ${archName} code`);
    return minimumFromMachO(run("otool", ["-l", binary]));
  })]);
  return { main, sidecar, minimum };
}

export function validateAppMetadata(info, expectedVersion, requiredMinimum) {
  const version = normalizeVersion(expectedVersion);
  assert.equal(info.CFBundleIdentifier, "ai.hoxi.desktop", "Expected the production Hoxi bundle");
  validateSparkleApp(info);
  assert.ok(info.CFBundleURLTypes?.some((type) => type.CFBundleURLSchemes?.includes("hoxi")),
    "App must register the hoxi URL scheme");
  assert.equal(info.CFBundleShortVersionString, version, "App display version differs from release version");
  assert.equal(String(info.CFBundleVersion), String(versionCode(version)),
    "App build version must be the numeric version code");
  assert.equal(maximumMacOS([info.LSMinimumSystemVersion, requiredMinimum]), info.LSMinimumSystemVersion,
    "App minimum macOS is lower than a bundled executable requires");
}

export function configureApp(app, version, arch = "arm64", env = process.env) {
  version = normalizeVersion(version);
  const info = readInfo(app);
  const { minimum } = inspectBinaries(app, info, arch, env);
  for (const [key, value] of Object.entries({
    CFBundleVersion: String(versionCode(version)), CFBundleShortVersionString: version, LSMinimumSystemVersion: minimum,
  })) {
    run("/usr/libexec/PlistBuddy", ["-c", `Set :${key} ${value}`, path.join(app, "Contents/Info.plist")]);
  }
}

export function verifyApp(app, version, arch = "arm64", env = process.env) {
  const info = readInfo(app);
  const { main, sidecar, minimum } = inspectBinaries(app, info, arch, env);
  validateAppMetadata(info, version, minimum);
  run("codesign", ["--verify", "--deep", "--strict", app]);
  // Cross-arch artifacts cannot run natively here: Rosetta cold-start of the
  // large hardened-runtime sidecar exceeds the exec timeout. Verify structure,
  // the exact Mach-O slice, signature and plist version instead; when bundled,
  // the sidecar itself is pinned by opencode.lock's SHA-256. Native builds keep
  // the full runtime check.
  if (hostReleaseArch() === arch) {
    assert.equal(run(main, ["--version"]).trim(), normalizeVersion(version),
      "Embedded runtime version differs from release version");
    if (sidecar) {
      verifyBinaryVersion(sidecar);
    }
  }
  return {
    version: normalizeVersion(version),
    minimumMacOS: info.LSMinimumSystemVersion,
    architecture: arch,
    bundledOpenCode: Boolean(sidecar),
  };
}

export function verifyDMG(dmg, version, arch = "arm64", env = process.env) {
  const mount = fs.mkdtempSync(path.join(os.tmpdir(), "hoxi-verify-dmg-"));
  let attached = false;
  let metadata;
  try {
    run("hdiutil", ["attach", "-readonly", "-nobrowse", "-mountpoint", mount, path.resolve(dmg)]);
    attached = true;
    metadata = verifyApp(path.join(mount, "Hoxi.app"), version, arch, env);
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
  } else if (command === "configure-app") configureApp(args[0], args[1], args[2] ?? "arm64");
  else if (command === "verify-app") console.log(JSON.stringify(verifyApp(args[0], args[1], args[2] ?? "arm64")));
  else if (command === "verify-dmg") console.log(JSON.stringify(verifyDMG(args[0], args[1], args[2] ?? "arm64")));
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
