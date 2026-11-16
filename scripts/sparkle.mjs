#!/usr/bin/env node
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { normalizeVersion } from "./release-artifact.mjs";

export function httpsURL(value) {
  const url = new URL(value);
  assert.ok(url.protocol === "https:" && !url.username && !url.password && !url.hash,
    "Sparkle URLs must be HTTPS without credentials or fragments");
  return url.href;
}

function base64(value, length) {
  assert.ok(typeof value === "string" && Buffer.from(value, "base64").length === length &&
    Buffer.from(value, "base64").toString("base64") === value, `Expected canonical base64 (${length} bytes)`);
  return value;
}

export function sparkleConfig(env = process.env) {
  const values = [env.HOXI_SPARKLE_DIR, env.HOXI_SPARKLE_PUBLIC_KEY, env.HOXI_SPARKLE_FEED_URL];
  if (values.every((value) => !value)) return null;
  assert.ok(values.every(Boolean), "Partial Sparkle config: DIR, PUBLIC_KEY and FEED_URL are all required");
  const feedURL = httpsURL(values[2]);
  assert.equal(new URL(feedURL).origin, "https://hoxi.ai", "Feed must be an explicit official hoxi.ai route");
  assert.notEqual(new URL(feedURL).pathname, "/", "Feed must specify a route");
  return { dir: path.resolve(values[0]), publicKey: base64(values[1], 32), feedURL };
}

export function validateSparkle(value, version, size) {
  assert.ok(value && typeof value === "object", "Sparkle metadata required");
  const config = sparkleConfig({ HOXI_SPARKLE_DIR: ".", HOXI_SPARKLE_PUBLIC_KEY: value.publicKey,
    HOXI_SPARKLE_FEED_URL: value.feedURL });
  assert.equal(value.version, normalizeVersion(version), "Sparkle version mismatch");
  assert.equal(value.length, size, "Sparkle length mismatch");
  assert.ok(Number.isSafeInteger(size) && size > 0, "Invalid Sparkle length");
  const url = httpsURL(value.url);
  assert.equal(url, `https://github.com/hoxiai/hoxi/releases/download/v${version}/Hoxi-${version}-darwin-arm64.dmg`,
    "Sparkle enclosure must be the versioned GitHub asset");
  return { version: value.version, url, feedURL: config.feedURL, publicKey: config.publicKey,
    edSignature: base64(value.edSignature, 64), length: size };
}

export function validateSparkleApp(info, config = null) {
  const keys = ["SUFeedURL", "SUPublicEDKey", "SUEnableAutomaticChecks", "SUAllowsAutomaticUpdates", "SUAutomaticallyUpdate"];
  const configured = keys.some((key) => key in info);
  if (info.CFBundleIdentifier === "ai.hoxi.desktop.dev") {
    assert.ok(!configured, "Development bundles must not configure Sparkle");
    return null;
  }
  if (!configured && !config) return null;
  assert.ok(info.SUPublicEDKey && info.SUFeedURL, "Missing bundled Sparkle metadata");
  const bundled = sparkleConfig({ HOXI_SPARKLE_DIR: ".", HOXI_SPARKLE_PUBLIC_KEY: info.SUPublicEDKey,
    HOXI_SPARKLE_FEED_URL: info.SUFeedURL });
  assert.ok(bundled, "Missing bundled Sparkle metadata");
  for (const [key, expected] of [["SUEnableAutomaticChecks", true],
    ["SUAllowsAutomaticUpdates", false], ["SUAutomaticallyUpdate", false]]) {
    assert.equal(info[key], expected, `Sparkle requires ${key}=${expected} (plist boolean)`);
  }
  if (config) {
    assert.equal(bundled.publicKey, config.publicKey, "Bundled Sparkle public key differs from configuration");
    assert.equal(bundled.feedURL, config.feedURL, "Bundled Sparkle feed differs from configuration");
  }
  return bundled;
}

export function frameworkFiles(framework) {
  assert.ok(fs.statSync(path.join(framework, "Sparkle")).isFile(), "Missing Sparkle framework executable");
  const files = [];
  function walk(dir) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const file = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(file);
      else if (entry.isFile()) files.push(file);
    }
  }
  walk(framework);
  for (const name of ["Autoupdate", "Updater", "Installer", "Downloader"]) {
    assert.ok(files.some((file) => path.basename(file) === name), `Missing Sparkle helper: ${name}`);
  }
  return files;
}

export function machoFiles(framework) {
  return frameworkFiles(framework).filter((file) => {
    const fd = fs.openSync(file, "r");
    const bytes = Buffer.alloc(4);
    try { fs.readSync(fd, bytes, 0, 4, 0); } finally { fs.closeSync(fd); }
    return ["cffaedfe", "cefaedfe", "cafebabe", "bebafeca", "cafebabf", "bfbafeca"].includes(bytes.toString("hex"));
  });
}

const run = (cmd, args) => execFileSync(cmd, args, { encoding: "utf8" });

function configure(app, enabled) {
  const config = sparkleConfig();
  const plist = path.join(app, "Contents/Info.plist");
  const info = JSON.parse(run("plutil", ["-convert", "json", "-o", "-", plist]));
  for (const key of ["SUFeedURL", "SUPublicEDKey", "SUEnableAutomaticChecks", "SUAllowsAutomaticUpdates", "SUAutomaticallyUpdate"]) {
    if (key in info) run("/usr/libexec/PlistBuddy", ["-c", `Delete :${key}`, plist]);
  }
  const framework = path.join(app, "Contents/Frameworks/Sparkle.framework");
  fs.rmSync(framework, { recursive: true, force: true });
  if (!enabled || !config) return;
  frameworkFiles(path.join(config.dir, "Sparkle.framework"));
  fs.accessSync(path.join(config.dir, "bin/sign_update"), fs.constants.X_OK);
  fs.mkdirSync(path.dirname(framework), { recursive: true });
  run("ditto", [path.join(config.dir, "Sparkle.framework"), framework]);
  for (const [key, type, value] of [["SUFeedURL", "string", config.feedURL],
    ["SUPublicEDKey", "string", config.publicKey], ["SUEnableAutomaticChecks", "bool", "true"],
    ["SUAllowsAutomaticUpdates", "bool", "false"], ["SUAutomaticallyUpdate", "bool", "false"]]) {
    run("/usr/libexec/PlistBuddy", ["-c", `Add :${key} ${type} ${value}`, plist]);
  }
}

function signFramework(framework, identity) {
  const files = machoFiles(framework);
  const bundles = new Set([framework]);
  for (const file of files) {
    for (let dir = path.dirname(file); dir.startsWith(framework + path.sep); dir = path.dirname(dir)) {
      if (/\.(app|xpc|framework)$/.test(dir)) bundles.add(dir);
    }
  }
  // Sparkle >= 2.6 requires preserving only Downloader's own entitlements.
  const args = ["--force", "--options", "runtime", "--sign", identity];
  if (identity !== "-") args.push("--timestamp");
  for (const target of [...files, ...[...bundles].sort((a, b) => b.split(path.sep).length - a.split(path.sep).length)]) {
    const downloader = target.endsWith("/Downloader.xpc") || target.includes("/Downloader.xpc/");
    run("codesign", [...args, ...(downloader ? ["--preserve-metadata=entitlements"] : []), target]);
  }
  run("codesign", ["--verify", "--deep", "--strict", framework]);
}

export function verifyUpdateSignature(bytes, metadata) {
  const key = crypto.createPublicKey({ key: Buffer.concat([
    Buffer.from("302a300506032b6570032100", "hex"), Buffer.from(base64(metadata.publicKey, 32), "base64"),
  ]), format: "der", type: "spki" });
  assert.ok(crypto.verify(null, bytes, key, Buffer.from(base64(metadata.edSignature, 64), "base64")),
    "Sparkle signature does not match artifact/public key");
}

function signUpdate(dmg, version, output) {
  const config = sparkleConfig();
  assert.ok(config, "Sparkle config required");
  const key = process.env.HOXI_SPARKLE_PRIVATE_KEY_FILE;
  assert.ok(key && path.isAbsolute(key), "HOXI_SPARKLE_PRIVATE_KEY_FILE must be an absolute secure key path");
  const stat = fs.lstatSync(key);
  assert.ok(stat.isFile() && (stat.mode & 0o077) === 0 && stat.uid === process.getuid(),
    "Private key must be owner-only and owned by current user");
  run(process.execPath, ["scripts/release-artifact.mjs", "verify-distribution", dmg]);
  const result = run(path.join(config.dir, "bin/sign_update"), ["--ed-key-file", key, dmg]);
  const signature = result.match(/sparkle:edSignature="([^"]+)"/);
  const length = result.match(/length="(\d+)"/);
  assert.ok(signature && length, "Unexpected sign_update output");
  version = normalizeVersion(version);
  const metadata = validateSparkle({ version, publicKey: config.publicKey, feedURL: config.feedURL,
    url: `https://github.com/hoxiai/hoxi/releases/download/v${version}/Hoxi-${version}-darwin-arm64.dmg`,
    edSignature: signature[1], length: Number(length[1]) }, version, fs.statSync(dmg).size);
  verifyUpdateSignature(fs.readFileSync(dmg), metadata);
  fs.writeFileSync(output, JSON.stringify(metadata, null, 2) + "\n");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const [command, ...args] = process.argv.slice(2);
    if (command === "check") {
      const config = sparkleConfig();
      if (config) {
        frameworkFiles(path.join(config.dir, "Sparkle.framework"));
        fs.accessSync(path.join(config.dir, "bin/sign_update"), fs.constants.X_OK);
      }
    }
    else if (command === "configure") configure(args[0], args[1] === "1");
    else if (command === "sign-framework") signFramework(args[0], args[1]);
    else if (command === "sign-update") signUpdate(...args);
    else throw new Error("Unknown Sparkle command");
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
