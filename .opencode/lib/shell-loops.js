// Literal `for` expansion. A list of literals (no `$`, glob, command
// substitution, secret or protected path) is unrolled up to 64 items and each
// copy is judged as its own command. Over the cap the loop is left untouched.
// Mirrors expandLiteralForLoops in internal/runner/opaque_expand.go.
const MAX_LITERAL_LOOP_ITEMS = 64;
const SHELL_VAR_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const EXT_GLOB_RE = /^\*\.[A-Za-z0-9]+$/;
const NUMERIC_FOR_HEADER_RE = /(^|[;&|()\n\r\t ])(for[ \t]+[A-Za-z_][A-Za-z0-9_]*[ \t]+in[ \t]+)(?:\$\(seq(?:[ \t]+\d+){1,3}\)|\{\d+\.\.\d+(?:\.\.\d+)?\})([ \t]*(?:;|\r?\n))/gm;
const URL_QUERY_ITEM_RE = /^(?:https?:\/\/[A-Za-z0-9_./:@%+=~?&,-]+|\/[A-Za-z0-9_./%+-]*\?[A-Za-z0-9_=&.%+-]+)$/;
const SED_READ_ONLY_SCRIPT_RE = /^\s*\d+(?:\s*,\s*(?:\d+|\+\d+))?\s*[pq](?:\s*[;\n]\s*\d+(?:\s*,\s*(?:\d+|\+\d+))?\s*[pq])*\s*$/;
const HEAD_TAIL_COUNT_RE = /^[+-]?\d+$/;
const HEAD_TAIL_FLAG_RE = /^-(?:[nc]?[+-]?\d+|q|v|-quiet|-silent|-lines=[+-]?\d+|-bytes=[+-]?\d+)$/;
const WC_COUNT_FLAG_RE = /^(?:-[lcmwL]+|--(?:lines|bytes|chars|words|max-line-length))$/;
const CURL_SILENT_FLAG_RE = /^-(?:[sSLkIf46]+|-silent|-show-error|-location|-insecure|-fail|-head)$/;
const CURL_WRITE_OUT_RE = /^[A-Za-z0-9_.:;,/ \-%{}()\\\n\r\t]+$/;
const POSITIVE_INT_RE = /^[1-9]\d*$/;
const XARGS_REPL_RE = /^(?:\{\}|%|[A-Za-z_][A-Za-z0-9_]*)$/;
const XARGS_ATTACHED_FLAG_RE = /^-(?:[nLPs][1-9]\d*|I(?:\{\}|%|[A-Za-z_][A-Za-z0-9_]*)|-max-(?:args|lines|procs)=[1-9]\d*)$/;

export const SEQ_LOOP_PLACEHOLDER = "__hoxi_seq__";
const isBoundary = (c) => " \t\r\n;&|()<>".includes(c);

export function normalizeNumericForHeaders(s) {
  return String(s ?? "").replace(NUMERIC_FOR_HEADER_RE, `$1$2${SEQ_LOOP_PLACEHOLDER}$3`);
}

function wordAt(s, i, word) {
  if (s.slice(i, i + word.length) !== word) return false;
  if (i > 0 && !isBoundary(s[i - 1])) return false;
  return i + word.length === s.length || isBoundary(s[i + word.length]);
}

export function forLoopRanges(s) {
  const ranges = [];
  scanForLoops(s, () => {
    return null;
  }, (start, end) => {
    ranges.push([start, end]);
  });
  return ranges;
}

export function containsFor(s) {
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote === "'") {
      if (c === "'") quote = "";
      continue;
    }
    if (c === "\\" && i + 1 < s.length) {
      i++;
      continue;
    }
    if (c === '"') {
      quote = quote === '"' ? "" : '"';
      continue;
    }
    if (c === "'") {
      quote = "'";
      continue;
    }
    if (wordAt(s, i, "for")) return true;
  }
  return false;
}

// deps.substituteVars, deps.forLoopVar and deps.itemBlocked come from
// shell-constants so this file does not import it.
export function expandLiteralForLoops(s, bound, deps) {
  let changed = false;
  const out = scanForLoops(s, (start, end, header, body, items) => {
    if (items.length === 0 || items.length > MAX_LITERAL_LOOP_ITEMS || !literalForItems(items, deps.itemBlocked)) return null;
    const name = deps.forLoopVar(header);
    if (!name || bound.has(name)) return null;
    const copies = [];
    for (const item of items) {
      const one = deps.substituteVars(body, new Map([[name, { valueSub: false, text: item, secret: false, inner: "" }]]));
      if (one === null) return null;
      copies.push(one.trim());
    }
    changed = true;
    return copies.join("\n");
  });
  return { text: out, changed };
}

function literalForItems(items, itemBlocked) {
  return items.every((item) => {
    if (item === SEQ_LOOP_PLACEHOLDER) return false;
    if (URL_QUERY_ITEM_RE.test(item) && !item.includes("..")) return true;
    if (/[$*?[`{]/.test(item) || item.includes("${")) return false;
    if (itemBlocked(item)) return false;
    return true;
  });
}

function scanForLoops(s, visit, onSpan) {
  for (;;) {
    let quote = "";
    let rewrote = false;
    for (let i = 0; i < s.length; i++) {
      const c = s[i];
      if (quote === "'") {
        if (c === "'") quote = "";
        continue;
      }
      if (c === "\\" && i + 1 < s.length) {
        i++;
        continue;
      }
      if (c === '"') {
        quote = quote === '"' ? "" : quote === "" ? '"' : quote;
        continue;
      }
      if (c === "'" && !quote) {
        quote = "'";
        continue;
      }
      if (!wordAt(s, i, "for")) continue;
      const header = parseForHeader(s, i);
      if (!header) continue;
      const done = findLoopDone(s, header.bodyStart);
      if (!done) continue;
      if (onSpan) onSpan(i, done.doneEnd);
      const repl = visit(i, done.doneEnd, s.slice(i, done.doneEnd), s.slice(header.bodyStart, done.bodyEnd), header.items);
      if (repl !== null) {
        s = s.slice(0, i) + repl + s.slice(done.doneEnd);
        rewrote = true;
        break;
      }
      i = done.doneEnd - 1;
    }
    if (!rewrote) return s;
  }
}

function skipSpace(s, i) {
  while (i < s.length && " \t\n\r".includes(s[i])) i++;
  return i;
}

function readShellWord(s, i) {
  i = skipSpace(s, i);
  if (i >= s.length) return null;
  if (s[i] === "'" || s[i] === '"') {
    const q = s[i];
    const j = s.indexOf(q, i + 1);
    if (j < 0) return null;
    return { word: s.slice(i + 1, j), next: j + 1 };
  }
  let j = i;
  while (j < s.length && !isBoundary(s[j])) j++;
  if (j === i) return null;
  return { word: s.slice(i, j), next: j };
}

function parseForHeader(s, i) {
  i += "for".length;
  const name = readShellWord(s, i);
  if (!name || !SHELL_VAR_NAME_RE.test(name.word)) return null;
  const inn = readShellWord(s, name.next);
  if (!inn || inn.word !== "in") return null;
  i = inn.next;
  const items = [];
  for (;;) {
    i = skipSpace(s, i);
    if (i >= s.length) return null;
    if (s[i] === ";") {
      i++;
      continue;
    }
    const w = readShellWord(s, i);
    if (!w) return null;
    if (w.word === "do") return { items, bodyStart: w.next };
    items.push(w.word);
    i = w.next;
  }
}

function findLoopDone(s, i) {
  let depth = 0;
  let quote = "";
  for (let j = i; j < s.length; j++) {
    const c = s[j];
    if (quote === "'") {
      if (c === "'") quote = "";
      continue;
    }
    if (c === "\\" && j + 1 < s.length) {
      j++;
      continue;
    }
    if (c === '"') {
      quote = quote === '"' ? "" : quote === "" ? '"' : quote;
      continue;
    }
    if (c === "'" && !quote) {
      quote = "'";
      continue;
    }
    if (wordAt(s, j, "for")) {
      depth++;
      j += "for".length - 1;
      continue;
    }
    if (wordAt(s, j, "done")) {
      if (depth === 0) return { bodyEnd: j, doneEnd: j + "done".length };
      depth--;
      j += "done".length - 1;
    }
  }
  return null;
}

export function isQuotedDirGlobSuffix(s, openIdx, closeIdx) {
  if (openIdx < 0 || closeIdx <= openIdx + 1 || s[closeIdx - 1] !== "/") return false;
  if (!s.slice(openIdx + 1, closeIdx).replaceAll("/", "")) return false;
  let k = closeIdx + 1;
  while (k < s.length && !isBoundary(s[k])) k++;
  const suf = s.slice(closeIdx + 1, k);
  return suf === "*" || EXT_GLOB_RE.test(suf);
}

export function isStdinOnlyHeadTail(args) {
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "-n" || a === "-c" || a === "--lines" || a === "--bytes") {
      if (i + 1 >= args.length || !HEAD_TAIL_COUNT_RE.test(args[i + 1])) return false;
      i++;
      continue;
    }
    if (!HEAD_TAIL_FLAG_RE.test(a)) return false;
  }
  return true;
}

export function isReadOnlySedArgs(rawArgs, stripNullRedirects) {
  const args = stripNullRedirects(rawArgs);
  if (!args || args.length === 0) return false;
  let quiet = false;
  let hasScript = false;
  let i = 0;
  while (i < args.length) {
    const a = args[i];
    if (a === "--") { i++; break; }
    if (["-n", "--quiet", "--silent", "-nE", "-nr", "-En", "-rn"].includes(a)) {
      quiet = true;
      i++;
      continue;
    }
    if (["-E", "-r", "--regexp-extended"].includes(a)) { i++; continue; }
    if (a === "-e") {
      if (i + 1 >= args.length || /[$`]/.test(args[i + 1]) || !SED_READ_ONLY_SCRIPT_RE.test(args[i + 1])) return false;
      hasScript = true;
      i += 2;
      continue;
    }
    if (a.startsWith("-")) return false;
    break;
  }
  if (!quiet) return false;
  if (!hasScript) {
    if (i >= args.length || /[$`]/.test(args[i]) || !SED_READ_ONLY_SCRIPT_RE.test(args[i])) return false;
    i++;
  }
  if (i >= args.length) return false;
  return args.slice(i).every((a) => !a.startsWith("-"));
}

export function isValueClassWc(args, isSecretOrProtected) {
  let counted = false;
  for (const a of args) {
    if (a === "<") continue;
    if (a.startsWith("--files0-from")) return false;
    if (WC_COUNT_FLAG_RE.test(a)) counted = true;
    else if (a.startsWith("-")) return false;
    else {
      const p = a.startsWith("<") ? a.slice(1) : a;
      if (isSecretOrProtected(p)) return false;
    }
  }
  return counted;
}

function isMacOSTempPath(slash) {
  let parts = String(slash ?? "").split("/").filter((p) => p && p !== ".");
  if (parts[0] === "private") parts = parts.slice(1);
  if (parts.length < 5 || parts[0] !== "var" || parts[1] !== "folders") return false;
  let tAt = -1;
  for (let i = 2; i < parts.length - 1 && i <= 4; i++) {
    if (parts[i] === "T") { tAt = i; break; }
  }
  if (tAt < 0 || tAt + 1 >= parts.length) return false;
  return parts.every((part) => part !== ".." && !/[*?[]/.test(part));
}

function isSafeCurlWriteOut(fmtStr) {
  const lower = String(fmtStr ?? "").toLowerCase();
  return Boolean(fmtStr) && !fmtStr.includes("@") && !lower.includes("header") && !lower.includes("json") && CURL_WRITE_OUT_RE.test(fmtStr);
}

function isSafeCurlOutputTarget(p, isSecret) {
  if (p === "/dev/null") return true;
  if (!p || p.includes("..") || /[$*?[`~]/.test(p) || isSecret(p)) return false;
  return p.startsWith("/tmp/") || p.startsWith("/private/tmp/") || isMacOSTempPath(p);
}

export function isValueClassCurl(args, isSecret) {
  let hasWriteOut = false;
  let hasSafeOutput = false;
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "-w" || a === "--write-out") {
      if (i + 1 >= args.length || !isSafeCurlWriteOut(args[i + 1])) return false;
      hasWriteOut = true;
      i++;
    } else if (a.startsWith("-w") && a.length > 2) {
      if (!isSafeCurlWriteOut(a.slice(2))) return false;
      hasWriteOut = true;
    } else if (a.startsWith("--write-out=")) {
      if (!isSafeCurlWriteOut(a.slice("--write-out=".length))) return false;
      hasWriteOut = true;
    } else if (a === "-o" || a === "--output") {
      if (i + 1 >= args.length || !isSafeCurlOutputTarget(args[i + 1], isSecret)) return false;
      hasSafeOutput = true;
      i++;
    } else if (a.startsWith("-o") && a.length > 2) {
      if (!isSafeCurlOutputTarget(a.slice(2), isSecret)) return false;
      hasSafeOutput = true;
    } else if (a.startsWith("--output=")) {
      if (!isSafeCurlOutputTarget(a.slice("--output=".length), isSecret)) return false;
      hasSafeOutput = true;
    } else if (["--max-time", "-m", "--connect-timeout", "-H", "--header", "-A", "--user-agent", "-X", "--request"].includes(a)) {
      if (i + 1 >= args.length) return false;
      i++;
    } else if (!CURL_SILENT_FLAG_RE.test(a) && a.startsWith("-")) {
      return false;
    }
  }
  return hasWriteOut && hasSafeOutput;
}

export function isSafeXargsSegment(rawArgs, piped, prevSeg, deps) {
  if (!piped) return false;
  const args = deps.stripNullRedirects(rawArgs);
  if (!args) return false;
  let i = 0;
  while (i < args.length) {
    const a = args[i];
    if (a === "--") { i++; break; }
    if (["-0", "--null", "-r", "--no-run-if-empty", "-t", "--verbose"].includes(a)) { i++; continue; }
    if (["-n", "-L", "-P", "-s"].includes(a)) {
      if (i + 1 >= args.length || !POSITIVE_INT_RE.test(args[i + 1])) return false;
      i += 2;
      continue;
    }
    if (a === "-I") {
      if (i + 1 >= args.length || !XARGS_REPL_RE.test(args[i + 1])) return false;
      i += 2;
      continue;
    }
    if (XARGS_ATTACHED_FLAG_RE.test(a)) { i++; continue; }
    if (a.startsWith("-")) return false;
    break;
  }
  const rest = args.slice(i);
  if (rest.length === 0) return true;
  if (rest.some((w) => /[$`]/.test(w))) return false;
  const norm = deps.normalizeAbsoluteTool(rest[0]);
  const targetBase = norm.includes("/") ? norm.slice(norm.lastIndexOf("/") + 1) : norm;
  const targetArgs = rest.slice(1);
  if (targetBase !== "basename" && targetBase !== "dirname" && targetBase !== "echo") return false;
  for (let j = 0; j < targetArgs.length; j++) {
    const ta = targetArgs[j];
    if (targetBase === "basename" && (ta === "-a" || ta.startsWith("-s="))) continue;
    if (targetBase === "basename" && ta === "-s" && j + 1 < targetArgs.length) { j++; continue; }
    if (targetBase === "echo" && ta === "-n") continue;
    if (ta.startsWith("-")) return false;
  }
  return true;
}
