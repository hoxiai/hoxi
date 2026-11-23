// Safe shell idioms that reduce false-positive prompts without weakening hard
// security floors. Mirrors internal/runner/shellpolicy/opaque_idioms.go.
import { isProtectedPath, isProtectedShellReadPath, isSecretPath, isSensitiveExternalPath } from "./external-path-risk.js";
import { scanShellWordSpans } from "./command-data-args.js";

export const MKTEMP_PLACEHOLDER_PREFIX = "__hoxi_mktemp_";

const MKTEMP_PLACEHOLDER_EXACT_RE = /^\/tmp\/__hoxi_mktemp_\d+__$/;
const MKTEMP_PREFIX_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
const MKTEMP_TEMPLATE_RE = /^\/(?:private\/)?tmp\/[A-Za-z0-9][A-Za-z0-9._-]*XXX+$/;
const TMPDIR_ASSIGN_RE = /\bTMPDIR\+?=/;
const TRAP_VAR_REF_RE = /^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)\})$/;
const PATH_BRACE_PREFIX_RE = /^[A-Za-z0-9_./-]+\/$/;
const PATH_BRACE_ITEM_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
const PATH_BRACE_SUFFIX_RE = /^(?:\/[A-Za-z0-9][A-Za-z0-9._-]*)+$/;
const FILTER_FLAG_PREFIX_RE = /^--(?:exclude-dir|exclude|include)=$/;
const BRACE_FILTER_ITEM_RE = /^[A-Za-z0-9_.*-]+$/;
const GOFMT_READ_ONLY_FLAG_RE = /^-[ls]+$/;
const SHASUM_ALGO_RE = /^(?:1|224|256|384|512|512224|512256)$/;
const WC_COUNT_FLAG_RE = /^(?:-[lcmwL]+|--(?:lines|bytes|chars|words|max-line-length))$/;
const RM_FLAG_RE = /^(?:-[rRfdvi]+|--(?:recursive|force|dir|verbose))$/;
const ENV_ASSIGN_WORD_RE = /^[A-Za-z_][A-Za-z0-9_]*=/;

const DOUBLE_BRACKET_ARITH_OPS = new Set(["-eq", "-ne", "-lt", "-le", "-gt", "-ge"]);
const TRAP_CLEANUP_SIGNALS = new Set(["EXIT", "0", "INT", "TERM", "HUP"]);
const REALPATH_SAFE_FLAGS = new Set(["-q", "--quiet", "-m", "--canonicalize-missing", "-e", "--canonicalize-existing", "-s", "--no-symlinks"]);
const CHECKSUM_SAFE_FLAGS = new Set(["-b", "-t", "--tag"]);
const SHELL_KEYWORDS = new Set(["while", "until", "do", "done", "if", "then", "else", "elif", "fi", "!"]);
const BENIGN_PATH_ENV_VARS = new Set(["PATH", "NODE_PATH", "GOCACHE", "GOMODCACHE", "GOPATH"]);
const SAFE_SHELL_C_LITERAL_VARS = new Set(["GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "NODE_ENV", "CI", "TZ", "LC_ALL", "LANG", "NO_COLOR", "FORCE_COLOR", "TERM", "GOWORK", "HOXI_ALLOW_DIRTY_EXPORT"]);
const SAFE_SHELL_C_BINARIES = new Set(["sh", "bash", "zsh", "/bin/sh", "/bin/bash", "/usr/bin/bash", "/bin/zsh", "/usr/bin/zsh"]);
const XCRUN_VALUE_QUERY_FLAGS = new Set(["--show-sdk-path", "--show-sdk-version", "--show-sdk-build-version", "--show-sdk-platform-path", "--show-sdk-platform-version"]);
const XCRUN_SDK_NAME_RE = /^[A-Za-z0-9._-]+$/;
const SAFE_SHELL_C_FLAG_RE = /^-[eul]*c$/;
const SAFE_SHELL_C_OPT_RE = /^-[eul]+$/;
const SAFE_SHELL_C_VAL_RE = /^[A-Za-z0-9_.,:/=+-]*$/;
const DEV_NULL_WORD_RE = /^(?:\d*|&)>>?\/dev\/null$/;
const DUP_STREAM_WORD_RE = /^\d*>&[12]$/;
const REDIRECT_WORD_RE = /^(?:\d*|&)>>?$/;
const MAX_PATH_BRACE_ITEMS = 64;

const isBoundary = (c) => " \t\r\n;&|()<>".includes(c);
const baseName = (w) => (w.includes("/") ? w.slice(w.lastIndexOf("/") + 1) : w);

export function containsMktempPlaceholder(s) {
  return String(s ?? "").includes(MKTEMP_PLACEHOLDER_PREFIX);
}

export function mktempPlaceholderPath(idx) {
  return `/tmp/${MKTEMP_PLACEHOLDER_PREFIX}${idx}__`;
}

export function isExactMktempPlaceholderPath(p) {
  return MKTEMP_PLACEHOLDER_EXACT_RE.test(String(p ?? ""));
}

export function isHarmlessSet(words) {
  if (!words || words.length < 2 || baseName(words[0]) !== "set") return false;
  const args = words.slice(1);
  for (let i = 0; i < args.length; i++) {
    const flag = args[i];
    if (flag.length < 2 || (flag[0] !== "-" && flag[0] !== "+") || flag.startsWith("--")) return false;
    const chars = flag.slice(1);
    for (let j = 0; j < chars.length; j++) {
      const c = chars[j];
      if (c === "e" || c === "u" || c === "x") continue;
      if (c === "o") {
        if (j !== chars.length - 1 || i + 1 >= args.length) return false;
        i++;
        if (!["pipefail", "errexit", "nounset", "xtrace"].includes(args[i])) return false;
        continue;
      }
      return false;
    }
  }
  return true;
}

export function isSafeDoubleBracketArgs(args) {
  if (!args || args.length === 0) return false;
  return args.every((a) => !DOUBLE_BRACKET_ARITH_OPS.has(a));
}

function isDisallowedPathBraceCandidate(p) {
  return isSecretPath(p) || isSensitiveExternalPath(p) || isProtectedShellReadPath(p) || isProtectedPath(p);
}

// A literal brace list that is the whole value of a filter flag:
// `--exclude-dir={node_modules,dist,.git}` repeats the flag per item. Items
// naming a secret file are refused, as the expanded `--include=.env` would be.
export function isFilterFlagBraceList(s, open, close) {
  let start = open;
  while (start > 0 && !isBoundary(s[start - 1])) start--;
  if (!FILTER_FLAG_PREFIX_RE.test(s.slice(start, open))) return false;
  if (close + 1 < s.length && !isBoundary(s[close + 1])) return false;
  const items = s.slice(open + 1, close).split(",");
  if (items.length < 2) return false;
  return items.every((item) => BRACE_FILTER_ITEM_RE.test(item) && !item.toLowerCase().startsWith(".en") && !isSecretPath(item));
}

export function isPathBraceList(s, open, close) {
  let start = open;
  while (start > 0 && !isBoundary(s[start - 1])) start--;
  const prefix = s.slice(start, open);
  if (!PATH_BRACE_PREFIX_RE.test(prefix) || !prefix.replaceAll("/", "") || prefix.startsWith("-")) return false;
  if (prefix.split("/").includes("..")) return false;
  let p = start;
  while (p > 0 && (s[p - 1] === " " || s[p - 1] === "\t")) p--;
  if (p === 0 || ";&|\n\r(`".includes(s[p - 1])) return false;
  let prevStart = p;
  while (prevStart > 0 && !isBoundary(s[prevStart - 1])) prevStart--;
  const prevWord = s.slice(prevStart, p);
  if (!prevWord || SHELL_KEYWORDS.has(prevWord) || ENV_ASSIGN_WORD_RE.test(prevWord)) return false;
  let end = close + 1;
  while (end < s.length && !isBoundary(s[end])) end++;
  const suffix = s.slice(close + 1, end);
  if (suffix && (!PATH_BRACE_SUFFIX_RE.test(suffix) || suffix.includes(".."))) return false;
  const items = s.slice(open + 1, close).split(",");
  if (items.length < 2 || items.length > MAX_PATH_BRACE_ITEMS) return false;
  if (isDisallowedPathBraceCandidate(prefix)) return false;
  for (const item of items) {
    if (!PATH_BRACE_ITEM_RE.test(item) || item.includes("..")) return false;
    if (isDisallowedPathBraceCandidate(prefix + item + suffix)) return false;
  }
  return true;
}

export function isSafeXargsTarget(targetBase, targetArgs, replToken) {
  if (targetBase === "basename" || targetBase === "dirname" || targetBase === "echo") {
    for (let j = 0; j < targetArgs.length; j++) {
      const ta = targetArgs[j];
      if (targetBase === "basename" && (ta === "-a" || ta.startsWith("-s="))) continue;
      if (targetBase === "basename" && ta === "-s" && j + 1 < targetArgs.length) {
        j++;
        continue;
      }
      if (targetBase === "echo" && ta === "-n") continue;
      if (ta.startsWith("-")) return false;
    }
    return true;
  }
  if (targetBase === "wc") {
    for (const ta of targetArgs) {
      if (replToken && ta === replToken) continue;
      if (ta.startsWith("-")) {
        if (!WC_COUNT_FLAG_RE.test(ta)) return false;
        continue;
      }
      if (isSecretPath(ta) || isSensitiveExternalPath(ta) || isProtectedShellReadPath(ta)) return false;
    }
    return true;
  }
  if (targetBase === "gofmt") {
    let hasList = false;
    for (const ta of targetArgs) {
      if (replToken && ta === replToken) continue;
      if (ta.startsWith("-")) {
        if (!GOFMT_READ_ONLY_FLAG_RE.test(ta)) return false;
        if (ta.includes("l")) hasList = true;
        continue;
      }
      if (isSecretPath(ta) || isSensitiveExternalPath(ta) || isProtectedShellReadPath(ta)) return false;
    }
    return hasList;
  }
  return false;
}

function hasUnsafeValuePath(p) {
  const trimmed = String(p ?? "").trim();
  if (!trimmed) return true;
  if (trimmed.replaceAll("\\", "/").split("/").includes("..")) return true;
  return isSecretPath(trimmed) || isSensitiveExternalPath(trimmed) || isProtectedShellReadPath(trimmed);
}

export function isValueClassGofmt(args) {
  let hasList = false;
  for (const a of args) {
    if (a.startsWith("-")) {
      if (!GOFMT_READ_ONLY_FLAG_RE.test(a)) return false;
      if (a.includes("l")) hasList = true;
      continue;
    }
    if (hasUnsafeValuePath(a)) return false;
  }
  return hasList;
}

export function isValueClassRealpath(args) {
  let hasPath = false;
  let endFlags = false;
  for (const a of args) {
    if (!endFlags && a === "--") {
      endFlags = true;
      continue;
    }
    if (!endFlags && a.startsWith("-")) {
      if (!REALPATH_SAFE_FLAGS.has(a)) return false;
      continue;
    }
    if (hasUnsafeValuePath(a)) return false;
    hasPath = true;
  }
  return hasPath;
}

export function isValueClassChecksum(base, args) {
  let hasFile = false;
  let endFlags = false;
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (!endFlags && a === "--") {
      endFlags = true;
      continue;
    }
    if (!endFlags && a.startsWith("-")) {
      if (base === "md5") {
        if (!["-q", "-r", "-qr", "-rq"].includes(a)) return false;
      } else if (base === "shasum") {
        if (a === "-a") {
          if (i + 1 >= args.length || !SHASUM_ALGO_RE.test(args[i + 1])) return false;
          i++;
          continue;
        }
        if (!["-b", "-t", "-U"].includes(a)) return false;
      } else if (base === "sha256sum" || base === "md5sum") {
        if (!CHECKSUM_SAFE_FLAGS.has(a)) return false;
      } else {
        return false;
      }
      continue;
    }
    if (hasUnsafeValuePath(a)) return false;
    hasFile = true;
  }
  return hasFile;
}

export function commandRebindsTMPDIR(command, segs, deps) {
  if (!command.includes("TMPDIR")) return false;
  if (TMPDIR_ASSIGN_RE.test(command)) return true;
  for (const seg of segs) {
    const text = command.slice(seg.start, seg.end).trim();
    if (deps.forLoopVar(text) === "TMPDIR") return true;
    if (deps.rebindNames(text).includes("TMPDIR")) return true;
  }
  return false;
}

export function isSafeMktempSegment(seg, deps) {
  seg = String(seg ?? "").trim();
  if (!seg || /[$`;&|\n\r()]/.test(seg)) return false;
  let words = deps.stripShellKeywords(deps.shellWords(seg));
  const stripped = [];
  for (let i = 0; i < words.length; i++) {
    const w = words[i];
    if (/^(?:\d*|&)>>?\/dev\/null$|^\d*>&[12]$/.test(w)) continue;
    if (/^(?:\d*|&)>>?$/.test(w) && words[i + 1] === "/dev/null") {
      i++;
      continue;
    }
    if (/[<>]/.test(w)) return false;
    stripped.push(w);
  }
  words = stripped;
  if (words.length === 0 || ENV_ASSIGN_WORD_RE.test(words[0])) return false;
  const unwrapped = deps.unwrapCommandPrefixes(words);
  if (!unwrapped || unwrapped.lookupOnly || unwrapped.words.length === 0) return false;
  const cmdWords = unwrapped.words;
  if (cmdWords[0] !== "mktemp" && cmdWords[0] !== "/usr/bin/mktemp" && cmdWords[0] !== "/bin/mktemp") return false;
  const args = cmdWords.slice(1);
  let i = 0;
  while (i < args.length) {
    const a = args[i];
    if (a === "--") {
      i++;
      break;
    }
    if (["-d", "--directory", "-q", "--quiet", "-dq", "-qd"].includes(a)) {
      i++;
      continue;
    }
    if (a === "-t") {
      if (i + 1 >= args.length || !MKTEMP_PREFIX_RE.test(args[i + 1]) || args[i + 1].includes("..")) return false;
      i += 2;
      continue;
    }
    if (a.startsWith("-")) return false;
    break;
  }
  const pos = args.slice(i);
  if (pos.length === 0) return true;
  if (pos.length !== 1) return false;
  return MKTEMP_TEMPLATE_RE.test(pos[0]) && !pos[0].includes("..") && !isSecretPath(pos[0]);
}

export function mktempWordSuffixIsSafe(s, afterIdx, quote) {
  let end = afterIdx;
  while (end < s.length) {
    const c = s[end];
    if (quote) {
      if (c === quote) break;
    } else if (isBoundary(c) || c === "'" || c === '"') {
      break;
    }
    end++;
  }
  const suffix = s.slice(afterIdx, end);
  if (!suffix) return true;
  if (!suffix.startsWith("/") || /[*?[$`\\]/.test(suffix)) return false;
  for (const part of suffix.slice(1).split("/")) {
    if (part === ".." || part.startsWith(".")) return false;
  }
  return !isDisallowedPathBraceCandidate(suffix);
}

export function parseSafeMktempTrapSegment(seg, segStart, bindings) {
  seg = String(seg ?? "").trim();
  if (!seg.startsWith("trap ") && !seg.startsWith("trap\t")) return null;
  let i = "trap".length;
  while (i < seg.length && (seg[i] === " " || seg[i] === "\t")) i++;
  if (i >= seg.length || (seg[i] !== "'" && seg[i] !== '"')) return null;
  const q = seg[i];
  i++;
  let action = "";
  let closed = false;
  while (i < seg.length) {
    const c = seg[i];
    if (q === '"' && c === "\\" && i + 1 < seg.length && (seg[i + 1] === '"' || seg[i + 1] === "\\" || seg[i + 1] === "$")) {
      action += seg[i + 1];
      i += 2;
      continue;
    }
    if (c === q) {
      closed = true;
      i++;
      break;
    }
    action += c;
    i++;
  }
  if (!closed || (i < seg.length && seg[i] !== " " && seg[i] !== "\t")) return null;
  const rest = seg.slice(i).trim();
  if (!rest || /[<>|;&()`'"$]/.test(rest)) return null;
  const signals = rest.split(/\s+/).filter(Boolean);
  if (signals.length === 0 || !signals.every((sig) => TRAP_CLEANUP_SIGNALS.has(sig))) return null;
  action = action.trim();
  if (!action || /[;&|\n\r`<>()]/.test(action) || action.includes("$(")) return null;
  const rawTokens = splitTrapActionTokens(action);
  if (!rawTokens || rawTokens.length < 2) return null;
  if (rawTokens[0] !== "rm" && rawTokens[0] !== "/bin/rm") return null;
  const out = ["rm"];
  let idx = 1;
  while (idx < rawTokens.length && rawTokens[idx] !== "--" && rawTokens[idx].startsWith("-")) {
    if (!RM_FLAG_RE.test(rawTokens[idx])) return null;
    out.push(rawTokens[idx]);
    idx++;
  }
  if (idx < rawTokens.length && rawTokens[idx] === "--") {
    out.push("--");
    idx++;
  }
  if (idx >= rawTokens.length) return null;
  for (; idx < rawTokens.length; idx++) {
    let tok = rawTokens[idx];
    if (tok.length >= 2 && ((tok.startsWith('"') && tok.endsWith('"')) || (tok.startsWith("'") && tok.endsWith("'")))) {
      tok = tok.slice(1, -1);
    }
    const m = TRAP_VAR_REF_RE.exec(tok);
    if (!m) return null;
    const name = m[1] || m[2];
    const v = bindings.get(name);
    if (!v || !v.mktemp || segStart <= v.assignedAt) return null;
    out.push(v.text);
  }
  return out.join(" ");
}

function splitTrapActionTokens(s) {
  const tokens = [];
  let cur = "";
  let inTok = false;
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote) {
      cur += c;
      if (c === quote) {
        quote = "";
        if (i + 1 < s.length && s[i + 1] !== " " && s[i + 1] !== "\t") return null;
      }
      continue;
    }
    if (c === " " || c === "\t") {
      if (inTok) {
        tokens.push(cur);
        cur = "";
        inTok = false;
      }
      continue;
    }
    if (c === '"' || c === "'") {
      if (inTok) return null;
      quote = c;
      cur += c;
      inTok = true;
      continue;
    }
    if (c === "\\") return null;
    cur += c;
    inTok = true;
  }
  if (quote) return null;
  if (inTok) tokens.push(cur);
  return tokens;
}

export function isValueClassXcrun(args) {
  let hasQuery = false;
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "--sdk") {
      if (i + 1 >= args.length || !XCRUN_SDK_NAME_RE.test(args[i + 1])) return false;
      i++;
    } else if (a.startsWith("--sdk=")) {
      if (!XCRUN_SDK_NAME_RE.test(a.slice("--sdk=".length))) return false;
    } else if (XCRUN_VALUE_QUERY_FLAGS.has(a)) {
      if (hasQuery) return false;
      hasQuery = true;
    } else {
      return false;
    }
  }
  return hasQuery;
}

export function isBenignToolchainEnvAssignment(word, wordExpansionNames, harmlessEnvVars) {
  const eq = word.indexOf("=");
  if (eq <= 0) return false;
  const name = word.slice(0, eq);
  const value = word.slice(eq + 1);
  if (!BENIGN_PATH_ENV_VARS.has(name) || /[`;&|<>()]/.test(value)) return false;
  const names = wordExpansionNames(value);
  if (names === null || !names.every((n) => harmlessEnvVars.has(n))) return false;
  if (name === "PATH") return true;
  if (!value) return false;
  for (const entry of value.split(":")) {
    if (!entry || /[*?[]/.test(entry)) return false;
    let check = entry;
    for (const pfx of ["~/", "$HOME/", "${HOME}/"]) {
      if (check.startsWith(pfx) && check.length > pfx.length) {
        check = "/__home__/" + check.slice(pfx.length);
        break;
      }
    }
    if (check.startsWith("$PWD/")) check = "/__pwd__/" + check.slice("$PWD/".length);
    else if (check.startsWith("${PWD}/")) check = "/__pwd__/" + check.slice("${PWD}/".length);
    else if (check === "$PWD" || check === "${PWD}") check = "/__pwd__";
    if (check.includes("$")) return false;
    if (check.replaceAll("\\", "/").split("/").includes("..")) return false;
    if (isSecretPath(check) || isSensitiveExternalPath(check) || isProtectedShellReadPath(check)) return false;
  }
  return true;
}

function isSafeShellCPrefixEnv(word, deps) {
  if (isBenignToolchainEnvAssignment(word, deps.wordExpansionNames, deps.harmlessEnvVars)) return true;
  const eq = word.indexOf("=");
  if (eq <= 0) return false;
  const name = word.slice(0, eq);
  const value = word.slice(eq + 1);
  if (name === "TMPDIR") {
    return value === "/tmp" || value === "/private/tmp";
  }
  return SAFE_SHELL_C_LITERAL_VARS.has(name) && SAFE_SHELL_C_VAL_RE.test(value) && !value.includes("..");
}

function extractSafeSingleQuotedShellC(seg, deps) {
  const s = String(seg || "").trim();
  if (!s || s.startsWith("#")) return null;
  const spans = scanShellWordSpans(s);
  if (!spans || spans.length < 3) return null;
  let idx = 0;
  while (idx < spans.length && ENV_ASSIGN_WORD_RE.test(spans[idx].word)) {
    if (!isSafeShellCPrefixEnv(spans[idx].word, deps)) return null;
    idx++;
  }
  if (idx >= spans.length) return null;
  const rawCmd = s.slice(spans[idx].start, spans[idx].end);
  if (/['"$`\\]/.test(rawCmd) || !SAFE_SHELL_C_BINARIES.has(rawCmd)) return null;
  idx++;
  let foundC = false;
  while (idx < spans.length) {
    const w = spans[idx].word;
    if (w === "-o" && idx + 1 < spans.length && (spans[idx + 1].word === "pipefail" || spans[idx + 1].word === "errexit")) {
      idx += 2;
      continue;
    }
    if (SAFE_SHELL_C_FLAG_RE.test(w)) {
      foundC = true;
      idx++;
      break;
    }
    if (SAFE_SHELL_C_OPT_RE.test(w)) {
      idx++;
      continue;
    }
    return null;
  }
  if (!foundC || idx >= spans.length) return null;
  const rawScript = s.slice(spans[idx].start, spans[idx].end);
  if (rawScript.length < 2 || rawScript[0] !== "'" || rawScript[rawScript.length - 1] !== "'") return null;
  const inner = rawScript.slice(1, -1);
  if (inner.includes("'") || !inner.trim()) return null;
  idx++;
  while (idx < spans.length) {
    const w = spans[idx].word;
    if (DEV_NULL_WORD_RE.test(w) || DUP_STREAM_WORD_RE.test(w)) {
      idx++;
      continue;
    }
    if (REDIRECT_WORD_RE.test(w) && idx + 1 < spans.length && spans[idx + 1].word === "/dev/null") {
      idx += 2;
      continue;
    }
    return null;
  }
  return inner;
}

export function unwrapTopLevelSingleQuotedShellC(s, allow, deps) {
  let out = "";
  let quote = "";
  let parenDepth = 0;
  let segStart = 0;
  let pipedIn = false;
  const flush = (segEnd, nextPiped) => {
    const seg = s.slice(segStart, segEnd);
    if (!pipedIn) {
      const inner = extractSafeSingleQuotedShellC(seg, deps);
      out += inner !== null && allow(inner) ? " : " : seg;
    } else {
      out += seg;
    }
    pipedIn = nextPiped;
  };
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    const next = i + 1 < s.length ? s[i + 1] : "";
    if (quote === "'") {
      if (c === "'") quote = "";
      continue;
    }
    if (c === "\\" && i + 1 < s.length) {
      i++;
      continue;
    }
    if (quote === '"') {
      if (c === '"') quote = "";
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      continue;
    }
    if (c === "(") {
      parenDepth++;
      continue;
    }
    if (c === ")") {
      if (parenDepth > 0) parenDepth--;
      continue;
    }
    if (parenDepth > 0) continue;
    let sepLen = 0;
    let nextPipe = false;
    if ((c === "&" && next === "&") || (c === "|" && next === "|") || (c === "|" && next === "&")) {
      sepLen = 2;
      nextPipe = c === "|" && next === "&";
    } else if (c === ";" || c === "\n") {
      sepLen = 1;
    } else if (c === "|") {
      sepLen = 1;
      nextPipe = true;
    } else if (c === "&" && (i === 0 || s[i - 1] !== ">") && next !== ">") {
      sepLen = 1;
    }
    if (sepLen > 0) {
      flush(i, nextPipe);
      out += s.slice(i, i + sepLen);
      i += sepLen - 1;
      segStart = i + 1;
    }
  }
  if (quote || parenDepth !== 0) return s;
  flush(s.length, false);
  return out;
}
