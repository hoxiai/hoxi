// web/scripts/check-cycles.ts
// Fails when web/src has a circular runtime import. Type-only imports and
// lazy `import()` calls are ignored because they do not run at module init.
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import type tsType from "typescript";

const ts = createRequire(path.resolve(process.cwd(), "package.json"))("typescript") as typeof tsType;

const SRC = path.resolve(process.cwd(), "src");
const EXTENSIONS = [".ts", ".tsx"];

function listSources(dir: string): string[] {
  const out: string[] = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name !== "__tests__" && entry.name !== "node_modules") out.push(...listSources(full));
    } else if (EXTENSIONS.some((ext) => entry.name.endsWith(ext)) && !entry.name.endsWith(".d.ts")) {
      out.push(full);
    }
  }
  return out;
}

function resolveImport(from: string, spec: string): string | undefined {
  const base = path.resolve(path.dirname(from), spec);
  const candidates = [base, ...EXTENSIONS.map((ext) => base + ext), ...EXTENSIONS.map((ext) => path.join(base, "index" + ext))];
  return candidates.find((p) => fs.existsSync(p) && fs.statSync(p).isFile());
}

function isRelativeSpecifier(spec: string): boolean {
  return spec.startsWith("./") || spec.startsWith("../");
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
      if (isRelativeSpecifier(spec) && hasRuntimeImportClause(stmt.importClause)) {
        specs.push(spec);
      }
    } else if (ts.isExportDeclaration(stmt) && stmt.moduleSpecifier && ts.isStringLiteral(stmt.moduleSpecifier)) {
      const spec = stmt.moduleSpecifier.text;
      if (isRelativeSpecifier(spec) && hasRuntimeExportClause(stmt)) {
        specs.push(spec);
      }
    }
  }
  return specs.map((spec) => resolveImport(file, spec)).filter((p): p is string => Boolean(p));
}

const graph = new Map<string, string[]>();
for (const file of listSources(SRC)) graph.set(file, runtimeImports(file));

const state = new Map<string, "visiting" | "done">();
const stack: string[] = [];
const cycles = new Set<string>();

function visit(node: string): void {
  state.set(node, "visiting");
  stack.push(node);
  for (const next of graph.get(node) ?? []) {
    if (state.get(next) === "visiting") {
      const cycle = stack.slice(stack.indexOf(next)).map((p) => path.relative(SRC, p));
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
  console.error(`[lint:cycles] ✗ Found ${cycles.size} circular runtime import(s) in web/src:`);
  for (const cycle of cycles) console.error(`  ✗ ${cycle}`);
  console.error("  Move the shared piece into its own module instead of importing a parent back.");
  process.exit(1);
}
console.log(`[lint:cycles] ✓ No circular runtime imports across ${graph.size} source files.`);
