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
  ".dart_tool",
  "node_modules",
  "dist",
  "build",
  "wailsjs",
  "Pods",
  "ephemeral",
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

  const baseName = path.basename(rel);

  // Enforce unidirectional frontend layering & naming: components -> store -> services
  if (rel.startsWith("web/src/services/")) {
    if (!/^[a-z0-9-]+\.ts$/.test(baseName)) {
      console.error(`  ✗ [naming violation] ${rel} must use lowercase kebab-case.ts filename.`);
      violations++;
    }
    const badImport = content.match(/(?:from\s+|import\s*\(\s*)["'][^"']*\/(?:store|components)(?:\/|["'])/);
    if (badImport) {
      console.error(`  ✗ [layering violation] ${rel} must not import from store/ or components/ (${badImport[0]}). Move state/UI logic to web/src/store/ or web/src/components/.`);
      violations++;
    }
    if (/["'`]\/(?:session|event|permission)(?:[/"'`?]|$)/.test(content)) {
      console.error(`  ✗ [gateway plane violation] ${rel} must not call raw '/session', '/event', or '/permission'; route through '/api/hoxi/sessions', '/api/hoxi/event', and '/api/hoxi/permission'.`);
      violations++;
    }
  } else if (rel.startsWith("web/src/store/")) {
    if (!/^[a-z0-9-]+\.ts$/.test(baseName)) {
      console.error(`  ✗ [naming violation] ${rel} must use lowercase kebab-case.ts filename.`);
      violations++;
    }
    const badImport = content.match(/(?:from\s+|import\s*\(\s*)["'][^"']*\/components(?:\/|["'])/);
    if (badImport) {
      console.error(`  ✗ [layering violation] ${rel} must not import from components/ (${badImport[0]}). Move pure domain helpers into web/src/store/.`);
      violations++;
    }
    if (rel !== "web/src/store/workspace.ts" && /^\s*export\s+\*\s+from\s+/m.test(content)) {
      console.error(`  ✗ [barrel violation] ${rel} must not use secondary 'export * from' (re-export flatly in web/src/store/workspace.ts instead).`);
      violations++;
    }
    if (rel.endsWith("-storage.ts")) {
      console.error(`  ✗ [fragment violation] ${rel} must not split out a separate *-storage.ts scaffold file; keep persistence inside its domain store module.`);
      violations++;
    }
  } else if (rel.startsWith("web/src/i18n/") && /domain-.*-types\.ts$/.test(rel)) {
    console.error(`  ✗ [i18n violation] ${rel} must not define manual i18n interface files; types are automatically inferred from en-US in web/src/i18n/types.ts.`);
    violations++;
  } else if (rel.startsWith("internal/") && rel.endsWith(".go")) {
    if (!/^[a-z0-9_]+\.go$/.test(baseName)) {
      console.error(`  ✗ [naming violation] ${rel} must use lowercase snake_case.go filename.`);
      violations++;
    }
    if (/type\s+desktopGateway\s*=/.test(content)) {
      console.error(`  ✗ [boundary violation] ${rel} must not define legacy 'type desktopGateway =' alias; use explicit package receiver (*Service / *Gateway).`);
      violations++;
    }
    if (
      !rel.startsWith("internal/app/") &&
      !rel.startsWith("internal/cli/") &&
      !rel.startsWith("internal/gateway/") &&
      !rel.endsWith("_test.go") &&
      /"github\.com\/hoxiai\/hoxi\/internal\/(?:gateway|app|cli)"/.test(content)
    ) {
      console.error(`  ✗ [layering violation] ${rel} must not reverse-import internal/gateway, internal/app, or internal/cli.`);
      violations++;
    }
    if (
      !rel.startsWith("internal/provider/") &&
      !rel.endsWith("_test.go") &&
      rel !== "internal/handoff/grok_bridge.go" &&
      /["']\/(?:v1\/messages|chat\/completions)["']/.test(content)
    ) {
      console.error(`  ✗ [boundary violation] ${rel} must not hand-roll '/v1/messages' or '/chat/completions' requests; reuse callSimpleLLM in internal/provider/http.go.`);
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
