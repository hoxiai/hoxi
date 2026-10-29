#!/usr/bin/env node
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// SQLite publishes SHA3-256 checksums on its official download page.
export const sqliteWindowsURL = "https://www.sqlite.org/2026/sqlite-tools-win-x64-3540100.zip";
export const sqliteWindowsSHA3 = "e171f3993a992edf076746926dd52b5e2ebfabaee38818e74987e1d8112ab987";

export function verifySQLiteArchive(file) {
  const actual = createHash("sha3-256").update(readFileSync(file)).digest("hex");
  if (actual !== sqliteWindowsSHA3) throw new Error(`SQLite archive checksum mismatch: ${actual}`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (process.argv[2] === "url") {
    console.log(sqliteWindowsURL);
  } else if (process.argv[2] === "verify" && process.argv[3]) {
    verifySQLiteArchive(process.argv[3]);
    console.log("SQLite archive verified");
  } else {
    throw new Error("usage: sqlite-tools.mjs url | verify <archive>");
  }
}
