#!/usr/bin/env node
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// SQLite publishes SHA3-256 checksums on its official download page.
// Windows ships an amd64 and an arm64 tools zip; both are pinned here.
const SQLITE_TOOLS = {
  amd64: {
    url: "https://www.sqlite.org/2026/sqlite-tools-win-x64-3540100.zip",
    sha3: "e171f3993a992edf076746926dd52b5e2ebfabaee38818e74987e1d8112ab987",
  },
  arm64: {
    url: "https://www.sqlite.org/2026/sqlite-tools-win-arm64-3540100.zip",
    sha3: "d58e230f16d9590c1c61bb19cf128f540a2818381d5bf3afcfd7b9d1ea6a0b2e",
  },
};

// Back-compat: the default (amd64) Windows SQLite archive.
export const sqliteWindowsURL = SQLITE_TOOLS.amd64.url;
export const sqliteWindowsSHA3 = SQLITE_TOOLS.amd64.sha3;

export function sqliteAsset(arch = "amd64") {
  const asset = SQLITE_TOOLS[arch];
  if (!asset) throw new Error(`Unsupported Windows SQLite architecture: ${arch}`);
  return asset;
}

export function verifySQLiteArchive(file, arch = "amd64") {
  const { sha3 } = sqliteAsset(arch);
  const actual = createHash("sha3-256").update(readFileSync(file)).digest("hex");
  if (actual !== sha3) throw new Error(`SQLite archive checksum mismatch: ${actual}`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const command = process.argv[2];
  if (command === "url") {
    console.log(sqliteAsset(process.argv[3] || "amd64").url);
  } else if (command === "verify" && process.argv[3]) {
    verifySQLiteArchive(process.argv[3], process.argv[4] || "amd64");
    console.log("SQLite archive verified");
  } else {
    throw new Error("usage: sqlite-tools.mjs url [amd64|arm64] | verify <archive> [amd64|arm64]");
  }
}
