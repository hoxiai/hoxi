// Interpreter recognition for the approval policy. Mirrors
// internal/runner/permission_policy.go so a shell request that hands text to an
// interpreter (or an awk program) is treated as opaque in both ports.

const AWK_TOOLS = new Set(["awk", "gawk", "mawk", "nawk"]);
const INTERPRETER_VERSION_RE = /^[0-9]+(?:\.[0-9]+)*$/;
const AWK_EXEC_RE = /\bsystem\s*\(/i;

// inlineScriptTool resolves an interpreter binary, including version-suffixed
// names such as python3.12, node20 or ruby3.2, to its canonical family.
export function inlineScriptTool(base) {
  for (const name of ["python", "python3", "node", "perl", "ruby", "php"]) {
    if (base === name) return name;
    if (base.startsWith(name) && INTERPRETER_VERSION_RE.test(base.slice(name.length))) return name;
  }
  if (base === "nodejs") return "node";
  return "";
}

// awkProgramExecutes reports whether an awk-family invocation carries an inline
// program whose reach cannot be verified: system() calls, command pipes, or a
// program read from a file all execute outside the shell's static view.
export function awkProgramExecutes(base, args) {
  if (!AWK_TOOLS.has(base)) return false;
  const program = [];
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "--") continue;
    if (a === "-f" || a === "--file" || a.startsWith("-f") || a.startsWith("--file=")) return true;
    if (a === "-F" || a === "--field-separator" || a === "-v" || a === "--assign") { i++; continue; }
    if (a.startsWith("-F") || a.startsWith("-v") || a.startsWith("--")) continue;
    if (a.startsWith("-") && a !== "-") continue;
    program.push(a);
  }
  if (program.length === 0) return true;
  const text = program.join(" ");
  return AWK_EXEC_RE.test(text) || text.includes("|");
}
