// Judgment-only script paths and redirections. Go additionally resolves links.
import { isProtectedPath, isSecretPath, isSensitiveExternalPath } from "./external-path-risk.js";
import { cleanInlinePosixPath } from "./inline-script-literals.js";

const resolve = (p, cwd) => cleanInlinePosixPath(p.startsWith("/") ? p : `${cwd}/${p}`);
const inside = (root, p) => p === root || p.startsWith(root + "/");
const safe = (p) => !isProtectedPath(p) && !isSecretPath(p) && !isSensitiveExternalPath(p);

export function isScopedProjectScript(script, worktree = "", cwd = "") {
  if (!worktree.startsWith("/") || worktree === "/" || !cwd || !safe(worktree)) return false;
  const root = cleanInlinePosixPath(worktree), target = resolve(script, cwd);
  return inside(root, target) && safe(target);
}

function safeOutput(target, worktree = "", cwd = "", isMacOSTempPath) {
  if (target === "/dev/null") return true;
  if (!target || /[$`*?\[\]{}<>&|!\r\n]/.test(target) || target.split("/").includes("..")) return false;
  if (!target.startsWith("/") && !cwd.startsWith("/")) return false;
  const resolved = resolve(target, cwd);
  if (!safe(resolved)) return false;
  if (resolved.startsWith("/tmp/") || resolved.startsWith("/private/tmp/") || isMacOSTempPath(resolved)) return true;
  return worktree.startsWith("/") && worktree !== "/" && safe(worktree) && inside(cleanInlinePosixPath(worktree), resolved);
}

export function stripScriptRedirects(args, deps, isMacOSTempPath) {
  const out = [];
  for (let i = 0; i < args.length; i++) {
    const word = args[i];
    if (/^\d*>&[12]$/.test(word)) continue;
    const m = /^(?:[012]?)(>>?|<)(.*)$/.exec(word);
    if (!m) {
      if (/[<>]/.test(word)) return null;
      out.push(word);
      continue;
    }
    const target = m[2] || args[++i];
    if ((m[1] === "<" && target !== "/dev/null") || !safeOutput(target, deps.worktree, deps.cwd, isMacOSTempPath)) return null;
  }
  return out;
}
