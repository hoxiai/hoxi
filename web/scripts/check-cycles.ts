// web/scripts/check-cycles.ts
// Fails when web/src or work/web has a circular runtime import. Type-only imports
// and lazy `import()` calls are ignored because they do not run at module init.
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import type tsType from "typescript";

const ts = createRequire(path.resolve(process.cwd(), "package.json"))("typescript") as typeof tsType;

const WEB_ROOT = path.resolve(process.cwd());
const REPO_ROOT = path.resolve(WEB_ROOT, "..");
const SRC = path.resolve(WEB_ROOT, "src");
const EXTENSIONS = [".ts", ".tsx"];

function resolveWorktreeMainRoot(dir: string): string | null {
  try {
    const dotGit = path.join(dir, ".git");
    if (!fs.statSync(dotGit).isFile()) return null;
    const match = fs.readFileSync(dotGit, "utf8").match(/^gitdir:\s*(.+)$/m);
    if (!match) return null;
    const commonGitDir = path.resolve(path.resolve(dir, match[1].trim()), "../..");
    return path.basename(commonGitDir) === ".git" ? path.dirname(commonGitDir) : null;
  } catch {
    return null;
  }
}

function resolveWorkWebDir(): string | null {
  if (process.env.HOXI_DISABLE_WORK_EXT === "1") return null;
  const mainRoot = resolveWorktreeMainRoot(REPO_ROOT);
  const candidates = process.env.HOXI_WORK_DIR
    ? [path.resolve(WEB_ROOT, process.env.HOXI_WORK_DIR)]
    : [
        path.resolve(REPO_ROOT, "work"),
        ...(mainRoot ? [path.resolve(mainRoot, "work")] : []),
        path.resolve(WEB_ROOT, "../../hoxi-work"),
        path.resolve(WEB_ROOT, "../../../../../hoxi-work"),
      ];
  for (const candidate of candidates) {
    const entry = path.join(candidate, "web/index.tsx");
    if (fs.existsSync(entry) && fs.statSync(entry).isFile()) {
      return path.join(candidate, "web");
    }
  }
  return null;
}

const WORK_WEB = resolveWorkWebDir();
const WORK_EXT_ENTRY = WORK_WEB ? path.join(WORK_WEB, "index.tsx") : null;

function listSources(dir: string): string[] {
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name !== "__tests__" && entry.name !== "tests" && entry.name !== "node_modules") {
        out.push(...listSources(full));
      }
    } else if (EXTENSIONS.some((ext) => entry.name.endsWith(ext)) && !entry.name.endsWith(".d.ts")) {
      out.push(full);
    }
  }
  return out;
}

function resolveCandidates(base: string): string | undefined {
  const candidates = [
    base,
    ...EXTENSIONS.map((ext) => base + ext),
    ...EXTENSIONS.map((ext) => path.join(base, "index" + ext)),
  ];
  return candidates.find((p) => fs.existsSync(p) && fs.statSync(p).isFile());
}

// Vite's `surface-extension` alias redirects only specifiers that resolve to the
// core stub. Imports inside the work repo itself (for example `./surface-extension`
// or a path that lands back on the work entry) resolve literally, so mirroring
// that here keeps the cycle graph identical to the bundle.
function isCoreSurfaceExtensionImport(from: string, spec: string): boolean {
  if (!WORK_EXT_ENTRY || !/(?:^|[\/\\])surface-extension(?:\.ts)?$/.test(spec)) return false;
  if (!spec.startsWith("./") && !spec.startsWith("../")) return true;
  const literal = resolveCandidates(path.resolve(path.dirname(from), spec));
  return Boolean(literal && path.resolve(literal) !== path.resolve(WORK_EXT_ENTRY));
}

function resolveImport(from: string, spec: string): string | undefined {
  if (isCoreSurfaceExtensionImport(from, spec)) {
    return WORK_EXT_ENTRY ?? undefined;
  }
  if (spec.startsWith("@hoxi-core/")) {
    return resolveCandidates(path.join(SRC, spec.slice("@hoxi-core/".length)));
  }
  if (spec.startsWith("./") || spec.startsWith("../")) {
    return resolveCandidates(path.resolve(path.dirname(from), spec));
  }
  return undefined;
}

function isTrackedSpecifier(spec: string): boolean {
  return (
    spec.startsWith("./") ||
    spec.startsWith("../") ||
    spec.startsWith("@hoxi-core/") ||
    /(?:^|[\/\\])surface-extension(?:\.ts)?$/.test(spec)
  );
}

function hasRuntimeImportClause(clause: ts.ImportClause | undefined): boolean {
  if (!clause) return true; // Side-effect `import "./x"` executes at module init.
  if (clause.isTypeOnly) return false;
  if (clause.name) return true;
  const bindings = clause.namedBindings;
  if (!bindings) return false;
  if (ts.isNamespaceImport(bindings)) return true;
  return bindings.elements.some((el) => !el.isTypeOnly);
}

function hasRuntimeExportClause(decl: ts.ExportDeclaration): boolean {
  if (decl.isTypeOnly) return false;
  if (!decl.exportClause || ts.isNamespaceExport(decl.exportClause)) return true;
  return decl.exportClause.elements.some((el) => !el.isTypeOnly);
}

function runtimeImports(file: string): string[] {
  const text = fs.readFileSync(file, "utf8");
  const kind = file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
  const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, false, kind);
  const specs: string[] = [];
  for (const stmt of sf.statements) {
    if (ts.isImportDeclaration(stmt) && ts.isStringLiteral(stmt.moduleSpecifier)) {
      const spec = stmt.moduleSpecifier.text;
      if (isTrackedSpecifier(spec) && hasRuntimeImportClause(stmt.importClause)) {
        specs.push(spec);
      }
    } else if (ts.isExportDeclaration(stmt) && stmt.moduleSpecifier && ts.isStringLiteral(stmt.moduleSpecifier)) {
      const spec = stmt.moduleSpecifier.text;
      if (isTrackedSpecifier(spec) && hasRuntimeExportClause(stmt)) {
        specs.push(spec);
      }
    }
  }
  return specs.map((spec) => resolveImport(file, spec)).filter((p): p is string => Boolean(p));
}

const roots = [SRC, ...(WORK_WEB ? [WORK_WEB] : [])];
const graph = new Map<string, string[]>();
for (const root of roots) {
  for (const file of listSources(root)) graph.set(file, runtimeImports(file));
}

const state = new Map<string, "visiting" | "done">();
const stack: string[] = [];
const cycles = new Set<string>();

function visit(node: string): void {
  state.set(node, "visiting");
  stack.push(node);
  for (const next of graph.get(node) ?? []) {
    if (state.get(next) === "visiting") {
      const cycle = stack.slice(stack.indexOf(next)).map((p) => path.relative(REPO_ROOT, p));
      cycles.add(cycle.join(" > "));
    } else if (!state.has(next) && graph.has(next)) {
      visit(next);
    }
  }
  stack.pop();
  state.set(node, "done");
}

for (const node of graph.keys()) if (!state.has(node)) visit(node);

if (cycles.size > 0) {
  console.error(`[lint:cycles] ✗ Found ${cycles.size} circular runtime import(s):`);
  for (const cycle of cycles) console.error(`  ✗ ${cycle}`);
  console.error("  Move the shared piece into its own module instead of importing a parent back.");
  process.exit(1);
}
console.log(`[lint:cycles] ✓ No circular runtime imports across ${graph.size} source files.`);
