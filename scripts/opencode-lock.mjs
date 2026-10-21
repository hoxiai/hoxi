#!/usr/bin/env node
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const lockPath = path.join(root, "internal/opencode/opencode.lock");

const PLATFORM_ASSETS = {
  "darwin/arm64": { name: "opencode-darwin-arm64.zip", zip: true },
  "darwin/amd64": { name: "opencode-darwin-x64.zip", zip: true },
  "linux/arm64": { name: "opencode-linux-arm64.tar.gz", zip: false },
  "linux/amd64": { name: "opencode-linux-x64.tar.gz", zip: false },
  "windows/arm64": { name: "opencode-windows-arm64.zip", zip: true },
  "windows/amd64": { name: "opencode-windows-x64.zip", zip: true },
};

export function validateOpenCodeLock(lock) {
  if (!lock || typeof lock !== "object") throw new Error("opencode.lock must be a JSON object");
  if (!/^\d+\.\d+\.\d+$/.test(lock.version || "")) {
    throw new Error(`opencode.lock version ${JSON.stringify(lock.version)} must be numeric without a leading v`);
  }
  const assets = lock.assets || {};
  for (const [platform, expected] of Object.entries(PLATFORM_ASSETS)) {
    const asset = assets[platform];
    if (!asset) throw new Error(`opencode.lock is missing ${platform}`);
    if (!/^[a-f0-9]{64}$/.test(asset.sha256 || "")) {
      throw new Error(`opencode.lock ${platform} has an invalid SHA-256 digest`);
    }
    let url;
    try {
      url = new URL(asset.url);
    } catch {
      throw new Error(`opencode.lock ${platform} has an invalid URL`);
    }
    if (url.protocol !== "https:" || url.hostname !== "github.com") {
      throw new Error(`opencode.lock ${platform} URL must be an HTTPS github.com release URL`);
    }
    const suffix = `/download/v${lock.version}/${expected.name}`;
    if (!url.pathname.endsWith(suffix)) {
      throw new Error(`opencode.lock ${platform} URL must end with ${suffix}`);
    }
  }
  for (const platform of Object.keys(assets)) {
    if (!PLATFORM_ASSETS[platform]) throw new Error(`opencode.lock has an unknown platform ${platform}`);
  }
  return lock;
}

export function loadOpenCodeLock() {
  return validateOpenCodeLock(JSON.parse(readFileSync(lockPath, "utf8")));
}

export function verifyArchive(file, expectedSHA256) {
  const actual = createHash("sha256").update(readFileSync(file)).digest("hex");
  if (actual !== expectedSHA256) {
    throw new Error(`checksum mismatch for ${file}: expected ${expectedSHA256}, got ${actual}`);
  }
}

function usage() {
  console.error(`usage:
  node scripts/opencode-lock.mjs verify
  node scripts/opencode-lock.mjs version
  node scripts/opencode-lock.mjs url <goos/goarch>
  node scripts/opencode-lock.mjs sha256 <goos/goarch>
  node scripts/opencode-lock.mjs verify-archive <goos/goarch> <file>`);
  process.exit(1);
}

function platformArg(lock) {
  const platform = process.argv[3];
  if (!platform || !lock.assets[platform]) usage();
  return lock.assets[platform];
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const command = process.argv[2];
  const lock = loadOpenCodeLock();
  switch (command) {
    case "verify":
      console.log(`opencode ${lock.version}: ${Object.keys(lock.assets).length} pinned platforms verified`);
      break;
    case "version":
      console.log(lock.version);
      break;
    case "url":
      console.log(platformArg(lock).url);
      break;
    case "sha256":
      console.log(platformArg(lock).sha256);
      break;
    case "verify-archive": {
      const asset = platformArg(lock);
      if (!process.argv[4]) usage();
      verifyArchive(process.argv[4], asset.sha256);
      console.log(`${process.argv[4]}: OK`);
      break;
    }
    default:
      usage();
  }
}
