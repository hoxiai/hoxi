// web/scripts/check-lines.ts
import fs from "node:fs";
import path from "node:path";

const MAX_LINES = 500;

// Static SVG icon lookup tables explicitly exempted by AGENTS.md Rule #1
const EXEMPT_SUFFIXES = [
  "web/src/components/icons/file-icons/types.ts",
  "web/src/components/icons/file-icons/icon-maps.ts",
];

const IGNORE_DIRS = new Set([
  ".git",
  ".hoxi",
  ".opencode",
  "node_modules",
  "dist",
  "build",
  "wailsjs",
]);

const SOURCE_EXT = /\.(go|ts|tsx|js|m)$/;

function walkRepo(dir: string): string[] {
  let results: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (IGNORE_DIRS.has(entry.name)) continue;
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      results = results.concat(walkRepo(fullPath));
    } else if (SOURCE_EXT.test(entry.name)) {
      results.push(fullPath);
    }
  }
  return results;
}

const repoRoot = fs.existsSync("./src") ? path.resolve("..") : path.resolve(".");
const files = walkRepo(repoRoot);

let violations = 0;

for (const file of files) {
  const rel = path.relative(repoRoot, file).replace(/\\/g, "/");
  if (EXEMPT_SUFFIXES.some((s) => rel.endsWith(s))) continue;

  const content = fs.readFileSync(file, "utf8");
  const lineCount = content.endsWith("\n")
    ? content.split("\n").length - 1
    : content.split("\n").length;

  if (lineCount > MAX_LINES) {
    console.error(`  ✗ [500-line limit] ${rel}: ${lineCount} lines (max ${MAX_LINES})`);
    violations++;
  }
}

if (violations > 0) {
  console.error(`\n[lint:lines] ✗ ${violations} file(s) exceeded the ${MAX_LINES}-line hard limit.`);
  process.exit(1);
} else {
  console.log(`[lint:lines] ✓ All ${files.length} source files comply with the ${MAX_LINES}-line limit.`);
}
