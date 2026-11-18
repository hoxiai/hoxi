// Protected destinations of Git's read-command --output flag. Mirrors git_output.go.
import { isProtectedPath, isSecretPath, isSensitiveExternalPath } from "./external-path-risk.js";
import { cleanInlinePosixPath } from "./inline-script-literals.js";
import { unwrapCommandPrefixes } from "./command-wrappers.js";

const join = (dir, target) => cleanInlinePosixPath(target.startsWith("/") ? target : dir + "/" + target);
const baseName = (s) => s.slice(s.lastIndexOf("/") + 1);
const valueFlags = new Set(["-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path"]);
const keywords = new Set(["while", "until", "do", "done", "if", "then", "else", "elif", "fi", "!"]);

export function hasProtectedGitOutput(command, splitCommandChain, shellWords) {
  let cwd = ".";
  for (const segment of splitCommandChain(command)) {
    let words = shellWords(segment);
    while (words.length && (keywords.has(words[0]) || /^[A-Za-z_][A-Za-z0-9_]*=/.test(words[0]))) words = words.slice(1);
    const parsed = unwrapCommandPrefixes(words);
    if (!parsed || parsed.lookupOnly || !parsed.words.length) continue;
    words = parsed.words;
    const base = baseName(words[0]);
    if (base === "cd" && words.length === 2) { cwd = join(cwd, words[1]); continue; }
    if (base !== "git") continue;
    let dir = cwd, i = 1;
    while (i < words.length && words[i].startsWith("-")) {
      const flag = words[i];
      if (flag === "-C" && i + 1 < words.length) { dir = join(dir, words[i + 1]); i += 2; continue; }
      if (flag.startsWith("-C") && flag.length > 2) dir = join(dir, flag.slice(2));
      else if (valueFlags.has(flag)) i++;
      i++;
    }
    if (!["log", "show", "diff"].includes(words[i])) continue;
    for (i++; i < words.length && words[i] !== "--"; i++) {
      const eq = words[i].indexOf("="), flag = eq < 0 ? words[i] : words[i].slice(0, eq);
      if (flag.length < 3 || !"--output".startsWith(flag)) continue;
      const target = eq < 0 ? words[++i] : words[i].slice(eq + 1);
      if (!target) continue;
      const p = join(dir, target);
      if (p.includes("\0") || isProtectedPath(p) || isSecretPath(p) || isSensitiveExternalPath(p) ||
        /(?:\.(?:key|pem|p12|pfx|jks|keystore|ppk|asc|gpg)|(?:^|\/)id_[a-z0-9_.-]+)$/i.test(p)) return true;
    }
  }
  return false;
}
