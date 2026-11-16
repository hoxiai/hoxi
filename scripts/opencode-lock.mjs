#!/usr/bin/env node
import { execFileSync } from "node:child_process";
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

const ANSI_ESCAPE_RE = /\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-_]/g;
const VERSION_LINE_RE = /^(?:opencode\s+)?v?(\d+\.\d+\.\d+)\b/i;

export function parseOpenCodeVersion(output) {
  const cleaned = String(output ?? "").replace(ANSI_ESCAPE_RE, "");
  for (const rawLine of cleaned.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line) continue;
    const match = line.match(VERSION_LINE_RE);
    if (match) return match[1];
  }
  return "";
}

export function verifyBinaryVersion(binaryPath, expectedVersion = loadOpenCodeLock().version) {
  const env = { ...process.env, CI: "1", NO_COLOR: "1", GIT_TERMINAL_PROMPT: "0",
    OPENCODE_DISABLE_CONFIG_DEPENDENCY_INSTALL: "1" };
  delete env.FORCE_COLOR;
  delete env.CLICOLOR_FORCE;
  delete env.CLICOLOR;
  let output;
  try {
    output = execFileSync(binaryPath, ["--version"], {
      encoding: "utf8",
      env,
      stdio: ["ignore", "pipe", "pipe"],
      timeout: 15_000,
    });
  } catch (error) {
    throw new Error(`failed to execute ${binaryPath} --version: ${error.message}`);
  }
  const actual = parseOpenCodeVersion(output);
  if (actual !== expectedVersion) {
    throw new Error(`opencode binary version mismatch for ${binaryPath}: expected ${expectedVersion}, got ${actual || JSON.stringify(String(output).trim())}`);
  }
  return actual;
}

function usage() {
  console.error(`usage:
  node scripts/opencode-lock.mjs verify
  node scripts/opencode-lock.mjs version
  node scripts/opencode-lock.mjs url <goos/goarch>
  node scripts/opencode-lock.mjs sha256 <goos/goarch>
  node scripts/opencode-lock.mjs verify-archive <goos/goarch> <file>
  node scripts/opencode-lock.mjs verify-binary <binary>`);
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
    case "verify-binary": {
      const binary = process.argv[3];
      if (!binary) usage();
      const verified = verifyBinaryVersion(binary, lock.version);
      console.log(`${binary}: opencode ${verified} OK`);
      break;
    }
    default:
      usage();
  }
}
