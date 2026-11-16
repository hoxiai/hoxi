#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// PE COFF machine types for the Windows release artifacts.
const PE_MACHINE = { amd64: 0x8664, arm64: 0xaa64 };

// Reads the machine field from a PE (Windows .exe) header. Cross-built binaries
// cannot run on the build host, so the release workflow asserts the target
// machine instead of executing them.
export function peMachine(file) {
  const bytes = fs.readFileSync(file);
  if (bytes.length < 0x40 || bytes.toString("ascii", 0, 2) !== "MZ") {
    throw new Error(`${file}: not a PE file (missing MZ header)`);
  }
  const peOffset = bytes.readUInt32LE(0x3c);
  if (peOffset + 6 > bytes.length || bytes.toString("ascii", peOffset, peOffset + 4) !== "PE\0\0") {
    throw new Error(`${file}: not a PE file (missing PE signature)`);
  }
  return bytes.readUInt16LE(peOffset + 4);
}

export function verifyPEMachine(file, arch) {
  const want = PE_MACHINE[arch];
  if (!want) throw new Error(`Unsupported Windows architecture: ${arch}`);
  const actual = peMachine(file);
  if (actual !== want) {
    throw new Error(`${file}: expected ${arch} (0x${want.toString(16)}), got machine 0x${actual.toString(16)}`);
  }
  return actual;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [command, arch] = process.argv.slice(2);
  if (command !== "verify" || !arch) {
    throw new Error("usage: pe-arch.mjs verify <amd64|arm64> <file...>");
  }
  const files = process.argv.slice(4);
  if (!files.length) throw new Error("no files to verify");
  for (const file of files) verifyPEMachine(file, arch);
  console.log(`PE architecture ${arch} verified for ${files.length} file(s)`);
}
