// Judgment-only script paths and redirections. Go additionally resolves links.
import { isProtectedPath, isSecretPath, isSensitiveExternalPath } from "./external-path-risk.js";
import { cleanPolicyPath, isAbsPolicyPath, isWindowsDriveRoot, isWindowsTempPath, joinPolicyPath, policyPathWithin } from "./policy-paths.js";

const resolve = (p, cwd) => cleanPolicyPath(joinPolicyPath(cwd, p));
const safe = (p) => !isProtectedPath(p) && !isSecretPath(p) && !isSensitiveExternalPath(p);

export function isScopedProjectScript(script, worktree = "", cwd = "") {
  if (!isAbsPolicyPath(worktree) || worktree === "/" || isWindowsDriveRoot(worktree) || !cwd || !safe(worktree)) return false;
  const root = cleanPolicyPath(worktree), target = resolve(script, cwd);
  return policyPathWithin(root, target) && safe(target);
}

function safeOutput(target, worktree = "", cwd = "", isMacOSTempPath) {
  if (target === "/dev/null") return true;
  if (!target || /[$`*?\[\]{}<>&|!\r\n]/.test(target) || target.replaceAll("\\", "/").split("/").includes("..")) return false;
  if (!isAbsPolicyPath(target) && !isAbsPolicyPath(cwd)) return false;
  const resolved = resolve(target, cwd);
  if (!safe(resolved)) return false;
  if (resolved.startsWith("/tmp/") || resolved.startsWith("/private/tmp/") || isMacOSTempPath(resolved) || isWindowsTempPath(resolved)) return true;
  return isAbsPolicyPath(worktree) && worktree !== "/" && !isWindowsDriveRoot(worktree) && safe(worktree) && policyPathWithin(cleanPolicyPath(worktree), resolved);
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
