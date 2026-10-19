// web/scripts/check-lines.ts
import fs from "node:fs";
import path from "node:path";

const MAX_LINES = 800;

// Static SVG icon lookup tables explicitly exempted by AGENTS.md Rule #1
const EXEMPT_SUFFIXES = [
  "web/src/components/icons/file-icons/types.ts",
  "web/src/components/icons/file-icons/icon-maps.ts",
];

const IGNORE_DIRS = new Set([
  ".git",
  ".hoxi",
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
    console.error(`  ✗ [${MAX_LINES}-line limit] ${rel}: ${lineCount} lines (max ${MAX_LINES})`);
    violations++;
  }

  // Enforce unidirectional frontend layering: components -> store -> services
  if (rel.startsWith("web/src/services/")) {
    const badImport = content.match(/(?:from\s+|import\s*\(\s*)["'][^"']*\/(?:store|components)(?:\/|["'])/);
    if (badImport) {
      console.error(`  ✗ [layering violation] ${rel} must not import from store/ or components/ (${badImport[0]})`);
      violations++;
    }
  } else if (rel.startsWith("web/src/store/")) {
    const badImport = content.match(/(?:from\s+|import\s*\(\s*)["'][^"']*\/components(?:\/|["'])/);
    if (badImport) {
      console.error(`  ✗ [layering violation] ${rel} must not import from components/ (${badImport[0]})`);
      violations++;
    }
    if (rel !== "web/src/store/workspace.ts" && /^\s*export\s+\*\s+from\s+/m.test(content)) {
      console.error(`  ✗ [barrel violation] ${rel} must not use secondary 'export * from' (re-export in workspace.ts instead)`);
      violations++;
    }
  } else if (rel.startsWith("internal/") && rel.endsWith(".go")) {
    if (/type\s+desktopGateway\s*=/.test(content)) {
      console.error(`  ✗ [boundary violation] ${rel} must not define legacy 'type desktopGateway =' alias`);
      violations++;
    }
  }
}

if (violations > 0) {
  console.error(`\n[lint:lines] ✗ ${violations} architectural or line-limit violation(s) found.`);
  process.exit(1);
} else {
  console.log(`[lint:lines] ✓ All ${files.length} source files comply with the ${MAX_LINES}-line limit and layering boundaries.`);
}
