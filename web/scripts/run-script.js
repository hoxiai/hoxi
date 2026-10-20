// web/scripts/run-script.js
import { build } from "esbuild";
import { pathToFileURL } from "node:url";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomBytes } from "node:crypto";
import { unlinkSync } from "node:fs";

const entry = process.argv[2];
if (!entry) {
  console.error("Usage: node scripts/run-script.js <entry-file>");
  process.exit(1);
}

const outfile = join(tmpdir(), `hoxi-script-${randomBytes(8).toString("hex")}.mjs`);

try {
  await build({
    entryPoints: [entry],
    bundle: true,
    platform: "node",
    format: "esm",
    outfile,
    loader: { ".svg": "text" },
    logLevel: "error",
  });
} catch (err) {
  console.error(`[build-failed] ${entry}:`, err);
  process.exit(1);
}

try {
  await import(pathToFileURL(outfile).href);
} catch (err) {
  console.error(`[exec-failed] ${entry}:`, err);
  process.exit(1);
} finally {
  try {
    unlinkSync(outfile);
  } catch {}
}
