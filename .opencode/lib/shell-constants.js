// Literal constant propagation, literal for-loop expansion and value-class
// $(...) substitutions. The rewritten text is only a judgment form. Mirrors
// internal/runner/opaque_constants.go and opaque_expand.go.
import { DATA_ARG_INTERPRETERS } from "./command-data-args.js";
import { containsFor, expandLiteralForLoops, forLoopRanges } from "./shell-loops.js";

export const CMD_SUBST_VAR = "__hoxi_cmdsub__";
export const MAX_SUBSHELL_DEPTH = 2;
const SHELL_VAR_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const PARAM_SENSITIVE = new Set(["git", "find", "rm", "rmdir", "unlink", "curl", "wget", "cp", "mv"]);
const LOOP_VAR_REBINDERS = new Set(["declare", "local", "typeset", "readonly", "mapfile", "readarray", "let", "unset", "export"]);
const SHELL_KEYWORDS = new Set(["while", "until", "do", "done", "if", "then", "else", "elif", "fi", "!"]);

export const HARMLESS_ENV_VARS = new Set(["HOME", "PATH", "PWD", "OLDPWD", "USER", "LOGNAME", "SHELL", "TMPDIR", "LANG",
  "TERM", "GOPATH", "GOROOT", "GOBIN", "GOCACHE", "GOMODCACHE", "CARGO_HOME", "PNPM_HOME",
  "JAVA_HOME", "ANDROID_HOME", "?", "!", "pipestatus"]);

export const READ_ONLY_DATA_TOOLS = new Set(["rg", "grep", "egrep", "fgrep", "cat", "head", "tail", "wc", "ls", "stat",
  "file", "echo", "printf", "diff", "cmp", "sort", "uniq", "jq", "test", "[", "basename",
  "dirname", "realpath", "true", "false", ":", "exit", "return",
  "lsof", "ps", "du", "df", "cut", "tr", "nl", "column", "shasum", "sha256sum", "md5", "xxd", "od", "sleep"]);

export const VALUE_ONLY_TOOLS = new Set(["echo", "printf", "test", "[", "basename", "dirname", "true", "false", ":", "exit", "return"]);

const isBoundary = (c) => " \t\r\n;&|()<>".includes(c);
const baseName = (w) => (w.includes("/") ? w.slice(w.lastIndexOf("/") + 1) : w);

let judge = {
  opaqueCore: () => true,
  isSecret: () => false,
  isProtected: () => false,
  shellWords: null,
};

export function setLiteralJudge(partial) {
  judge = { ...judge, ...partial };
}

export function acceptsValueClassArg(base) {
  if (PARAM_SENSITIVE.has(base) || DATA_ARG_INTERPRETERS.has(base)) return false;
  return READ_ONLY_DATA_TOOLS.has(base) || base === "mkdir";
}

function processSubstitutionEnd(s, start) {
  let quote = "";
  for (let i = start; i < s.length; i++) {
    const c = s[i];
    if (quote === "'") {
      if (c === "'") quote = "";
    } else if (quote === '"') {
      if (c === "\\") i++;
      else if (c === '"') quote = "";
    } else if (c === "\\") i++;
    else if (c === "'" || c === '"') quote = c;
    else if (c === "(") return -1;
    else if (c === ")") return i;
  }
  return -1;
}

export function findSubshellSpans(s) {
  const spans = [];
  let quote = "";
  let depth = 0;
  let start = -1;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote === "'") {
      if (c === "'") quote = "";
      continue;
    }
    if (c === "\\" && quote !== "'" && i + 1 < s.length) {
      i++;
      continue;
    }
    if (quote === '"') {
      if (c === '"') quote = "";
      else if (c === "$" && s[i + 1] === "(") {
        const end = processSubstitutionEnd(s, i + 2);
        if (end < 0) return null;
        i = end;
      }
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      continue;
    }
    if (c === "$" && s[i + 1] === "(") {
      const end = processSubstitutionEnd(s, i + 2);
      if (end < 0) return null;
      i = end;
      continue;
    }
    if (c === "<" && s[i + 1] === "(" && (i === 0 || isBoundary(s[i - 1]))) {
      const end = processSubstitutionEnd(s, i + 2);
      if (end < 0) return null;
      i = end;
      continue;
    }
    if (c === "(") {
      if (depth === 0) start = i;
      depth++;
    } else if (c === ")") {
      if (depth === 0) return null;
      depth--;
      if (depth === 0 && start >= 0) {
        spans.push([start, i]);
        start = -1;
      }
    }
  }
  if (quote || depth !== 0) return null;
  return spans;
}

export function eraseSubshells(s) {
  const spans = findSubshellSpans(s);
  if (!spans || spans.length === 0) return s;
  let out = "";
  let prev = 0;
  for (const [start, end] of spans) {
    out += s.slice(prev, start) + ":";
    prev = end + 1;
  }
  return out + s.slice(prev);
}

function wordAt(s, i, word) {
  if (s.slice(i, i + word.length) !== word) return false;
  if (i > 0 && !isBoundary(s[i - 1])) return false;
  return i + word.length === s.length || isBoundary(s[i + word.length]);
}

function shellExpansionNames(s) {
  const names = [];
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote === "'") {
      if (c === "'") quote = "";
      continue;
    }
    if (c === "\\") {
      i++;
      continue;
    }
    if (c === '"') {
      quote = quote === '"' ? "" : '"';
      continue;
    }
    if (c === "'" && !quote) {
      quote = "'";
      continue;
    }
    if (c !== "$" || i + 1 >= s.length) continue;
    const next = s[i + 1];
    if (next === "{") {
      const end = s.indexOf("}", i + 2);
      const name = end < 0 ? "" : s.slice(i + 2, end);
      if (!SHELL_VAR_NAME_RE.test(name)) return null;
      names.push(name);
      i = end;
    } else if (/[A-Za-z_]/.test(next)) {
      let k = i + 1;
      while (k < s.length && /[A-Za-z0-9_]/.test(s[k])) k++;
      names.push(s.slice(i + 1, k));
      i = k - 1;
    } else if (next === "?" || next === "!") {
      names.push(next);
      i++;
    } else if (/[0-9_('"@*#$-]/.test(next)) return null;
  }
  return names;
}

function wordExpansionNames(word) {
  const names = [];
  for (let i = 0; i < word.length; i++) {
    if (word[i] !== "$" || i + 1 >= word.length) continue;
    const next = word[i + 1];
    if (next === "{") {
      const end = word.indexOf("}", i + 2);
      const name = end < 0 ? "" : word.slice(i + 2, end);
      if (!SHELL_VAR_NAME_RE.test(name)) return null;
      names.push(name);
      i = end;
    } else if (/[A-Za-z_]/.test(next)) {
      let k = i + 1;
      while (k < word.length && /[A-Za-z0-9_]/.test(word[k])) k++;
      names.push(word.slice(i + 1, k));
      i = k - 1;
    } else if (next === "?" || next === "!") {
      names.push(next);
      i++;
    } else return null;
  }
  return names;
}

function splitConstSegments(command) {
  const segs = [];
  let quote = "";
  let segStart = 0;
  let before = "";
  const flush = (at, next) => {
    if (command.slice(segStart, at).trim()) segs.push({ start: segStart, end: at, before, next });
    segStart = at;
    before = next;
  };
  for (let i = 0; i < command.length; i++) {
    const c = command[i];
    const next = i + 1 < command.length ? command[i + 1] : "";
    if (quote === "'") {
      if (c === "'") quote = "";
      continue;
    }
    if (c === "\\" && i + 1 < command.length) {
      i++;
      continue;
    }
    if (quote === '"') {
      if (c === '"') quote = "";
      continue;
    }
    if (c === "'" || c === '"') quote = c;
    else if (c === "&" && next === "&") {
      flush(i, "&");
      i++;
      segStart = i + 1;
    } else if (c === "|" && next === "|") {
      flush(i, "|");
      i++;
      segStart = i + 1;
    } else if (c === "|") {
      flush(i, "p");
      segStart = i + 1;
    } else if (c === ";" || c === "\n") {
      flush(i, ";");
      segStart = i + 1;
    } else if (c === "&" && (i === 0 || command[i - 1] !== ">") && next !== ">") {
      flush(i, "b");
      segStart = i + 1;
    }
  }
  flush(command.length, ";");
  return segs;
}

function assignmentValueHasBareSpace(s) {
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
    if (quote === '"') {
      if (c === '"') quote = "";
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      continue;
    }
    if (c === "$" && s[i + 1] === "(") {
      const end = processSubstitutionEnd(s, i + 2);
      if (end < 0) return true;
      i = end;
      continue;
    }
    if (c === " " || c === "\t") return true;
  }
  return quote !== "";
}

function splitOneAssignment(s) {
  const eq = s.indexOf("=");
  if (eq <= 0) return null;
  const name = s.slice(0, eq);
  if (!SHELL_VAR_NAME_RE.test(name) || assignmentValueHasBareSpace(s.slice(eq + 1))) return null;
  return { name, raw: s.slice(eq + 1) };
}

function pureAssignment(seg) {
  seg = seg.trim();
  if (seg.startsWith("export ") || seg.startsWith("export\t")) return splitOneAssignment(seg.slice("export".length).trim());
  return splitOneAssignment(seg);
}

function unquoteConst(raw) {
  if (raw.length >= 2 && raw.startsWith("'") && raw.endsWith("'") && raw.split("'").length === 3) return raw.slice(1, -1);
  if (raw.length >= 2 && raw.startsWith('"') && raw.endsWith('"')) {
    const body = raw.slice(1, -1);
    if (/["`]/.test(body) || body.includes("$(")) return null;
    return body;
  }
  if (/[ \t"'`]/.test(raw)) return null;
  return raw;
}

function constLiteralBad(v) {
  const plain = v.replaceAll("$?", "");
  if (plain.includes("\n") || plain.includes("*") || plain.includes("?") || plain.includes("[") || v.includes("`") || v.includes("$(") || v.includes("${")) {
    return true;
  }
  const names = wordExpansionNames(v);
  if (names === null) return true;
  return names.some((name) => !HARMLESS_ENV_VARS.has(name));
}

function pathSecret(p) {
  return Boolean(judge.isSecret(p));
}

function parseConstValue(raw) {
  raw = raw.trim();
  if (!raw) return null;
  if (raw.startsWith("$(") && raw.endsWith(")") && processSubstitutionEnd(raw, 2) === raw.length - 1) {
    const inner = raw.slice(2, -1);
    if (!isValueClassSegment(inner) || judge.opaqueCore(inner)) return null;
    return { valueSub: true, inner, text: "", secret: false };
  }
  const val = unquoteConst(raw);
  if (val === null || constLiteralBad(val)) return null;
  return { valueSub: false, inner: "", text: val, secret: pathSecret(val) };
}

function shellWordsOf(s) {
  return typeof judge.shellWords === "function" ? judge.shellWords(s) : [];
}

function stripShellKeywords(words) {
  let i = 0;
  while (i < words.length && SHELL_KEYWORDS.has(words[i])) i++;
  return words.slice(i);
}

function readBinding(cmd) {
  const names = [];
  for (const a of cmd.slice(1)) {
    if (a === "-r") continue;
    if (a.startsWith("-") || !SHELL_VAR_NAME_RE.test(a)) return "";
    names.push(a);
  }
  return names.length === 1 ? names[0] : "";
}

function rebindNames(seg) {
  const words = stripShellKeywords(shellWordsOf(seg));
  if (words.length === 0) return [];
  const base = baseName(words[0]);
  const names = [];
  if (base === "read") {
    const name = readBinding(words);
    if (name) names.push(name);
  } else if (base === "printf") {
    for (let k = 1; k + 1 < words.length; k++) {
      if (words[k] === "-v" && SHELL_VAR_NAME_RE.test(words[k + 1])) names.push(words[k + 1]);
    }
  } else if (LOOP_VAR_REBINDERS.has(base)) {
    for (const a of words.slice(1)) {
      if (a.startsWith("PATH=") && wordExpansionNames(a.slice(5))?.every((n) => HARMLESS_ENV_VARS.has(n)) && !/[`;&|<>()]/.test(a.slice(5))) {
        continue;
      }
      const eq = a.indexOf("=");
      const name = eq > 0 ? a.slice(0, eq) : a;
      if (SHELL_VAR_NAME_RE.test(name)) names.push(name);
    }
  }
  return names;
}

function forLoopVar(seg) {
  const words = shellWordsOf(seg.trim());
  if (words.length < 2 || words[0] !== "for" || !SHELL_VAR_NAME_RE.test(words[1])) return "";
  return words[1];
}

function indexInside(i, ranges) {
  return ranges.some(([start, end]) => i >= start && i < end);
}

function constBindings(command, segs) {
  const bound = new Map();
  const poisoned = new Set();
  const loops = forLoopRanges(command);
  const subshells = findSubshellSpans(command) || [];
  const poison = (name) => {
    if (!name) return;
    poisoned.add(name);
    bound.delete(name);
  };
  const usedBefore = new Set();
  for (const seg of segs) {
    const text = command.slice(seg.start, seg.end).trim();
    const insideLoop = indexInside(seg.start, loops) || indexInside(seg.start, subshells);
    const names = shellExpansionNames(text) || [];
    for (const name of names) if (!bound.has(name)) usedBefore.add(name);
    const loopName = forLoopVar(text);
    if (loopName && (bound.has(loopName) || poisoned.has(loopName))) poison(loopName);
    for (const name of rebindNames(text)) poison(name);
    const assign = pureAssignment(text);
    if (!assign) continue;
    const uncertain = seg.before === "&" || seg.before === "|" || seg.before === "p" || seg.before === "b" || seg.next === "p";
    if (!assign || insideLoop || uncertain || usedBefore.has(assign.name) || HARMLESS_ENV_VARS.has(assign.name) || poisoned.has(assign.name) || bound.has(assign.name)) {
      poison(assign.name);
      continue;
    }
    const val = parseConstValue(assign.raw);
    if (!val) {
      poison(assign.name);
      continue;
    }
    bound.set(assign.name, val);
  }
  return { bound, poisoned };
}

function expansionsHit(s, names) {
  const found = shellExpansionNames(s);
  if (found === null) return false;
  return found.some((name) => names.has(name));
}

function constResultIsSecret(command, bound) {
  for (const v of bound.values()) {
    if (v.secret || (!v.valueSub && pathSecret(v.text))) return true;
  }
  for (const seg of splitConstSegments(command)) {
    for (const w of shellWordsOf(command.slice(seg.start, seg.end))) {
      if (pathSecret(w)) return true;
    }
  }
  return false;
}

function readDollarName(s, i) {
  if (s[i] !== "$" || i + 1 >= s.length) return null;
  const n = s[i + 1];
  if (n === "{") {
    const end = s.indexOf("}", i + 2);
    const body = end < 0 ? "" : s.slice(i + 2, end);
    if (!SHELL_VAR_NAME_RE.test(body)) return { fail: true };
    return { name: body, next: end + 1 };
  }
  if (/[A-Za-z_]/.test(n)) {
    let k = i + 1;
    while (k < s.length && /[A-Za-z0-9_]/.test(s[k])) k++;
    return { name: s.slice(i + 1, k), next: k };
  }
  if (n === "?" || n === "!") return { name: n, next: i + 2 };
  return { name: "", next: i + 1 };
}

export function substituteVars(s, vars) {
  if (!vars || vars.size === 0) return s;
  let out = "";
  let quote = "";
  let atWordStart = true;
  let commandWord = true;
  let fail = false;
  const writeRaw = (v) => {
    if (atWordStart && commandWord) fail = true;
    out += v;
    atWordStart = false;
  };
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote === "'") {
      out += c;
      if (c === "'") quote = "";
      continue;
    }
    if (c === "\\" && i + 1 < s.length) {
      out += c + s[i + 1];
      i++;
      atWordStart = false;
      continue;
    }
    if (c === "$" && s[i + 1] === "(") {
      const end = processSubstitutionEnd(s, i + 2);
      if (end < 0) return null;
      const inner = substituteVars(s.slice(i + 2, end), vars);
      if (inner === null) return null;
      out += `$(${inner})`;
      i = end;
      atWordStart = false;
      continue;
    }
    if (quote === '"' && c === '"') {
      quote = "";
      out += c;
      continue;
    }
    if (!quote && (c === "'" || c === '"')) {
      quote = c;
      out += c;
      continue;
    }
    if (!quote && (c === ";" || c === "\n" || c === "|" || c === "(")) {
      commandWord = true;
      atWordStart = true;
      out += c;
      continue;
    }
    if (!quote && c === "&" && (i === 0 || s[i - 1] !== ">") && s[i + 1] !== ">") {
      commandWord = true;
      atWordStart = true;
      out += c;
      continue;
    }
    if (!quote && (c === " " || c === "\t" || c === "\r")) {
      if (!atWordStart) commandWord = false;
      atWordStart = true;
      out += c;
      continue;
    }
    if (c === "$" && i + 1 < s.length) {
      const got = readDollarName(s, i);
      if (!got || got.fail) return null;
      if (got.name && vars.has(got.name)) {
        const v = vars.get(got.name);
        writeRaw(v.valueSub ? `$${CMD_SUBST_VAR}` : v.text);
        i = got.next - 1;
        continue;
      }
      if (got.name) {
        out += s.slice(i, got.next);
        i = got.next - 1;
        atWordStart = false;
        continue;
      }
    }
    out += c;
    atWordStart = false;
    if (!quote && c === "=") {
      commandWord = true;
      atWordStart = true;
    }
  }
  return fail ? null : out;
}

function replaceValueSubAssignments(command, segs, bound) {
  if (bound.size === 0) return command;
  let out = "";
  let prev = 0;
  for (const seg of segs) {
    const assign = pureAssignment(command.slice(seg.start, seg.end).trim());
    const v = assign ? bound.get(assign.name) : null;
    if (!assign || !v) continue;
    let repl = "";
    if (v.valueSub) repl = v.inner;
    else if (v.text.includes("$")) repl = ":";
    else continue;
    out += command.slice(prev, seg.start) + repl;
    prev = seg.end;
  }
  return out + command.slice(prev);
}

export function concretizeLiterals(command) {
  command = String(command ?? "").trim();
  if (!command || (!command.includes("=") && !containsFor(command))) return { text: command, status: "noop" };
  const segs = splitConstSegments(command);
  const { bound, poisoned } = constBindings(command, segs);
  if (poisoned.size > 0 && expansionsHit(command, poisoned)) return { text: command, status: "reject" };
  let rewritten = replaceValueSubAssignments(command, segs, bound);
  const expanded = expandLiteralForLoops(rewritten, bound, {
    substituteVars,
    forLoopVar,
    itemBlocked: (item) => pathSecret(item) || judge.isProtected(item),
  });
  rewritten = expanded.text;
  const substituted = substituteVars(rewritten, bound);
  if (substituted === null) return { text: command, status: "reject" };
  rewritten = substituted;
  if (rewritten === command && !expanded.changed) return { text: command, status: "noop" };
  if (constResultIsSecret(rewritten, bound)) return { text: rewritten, status: "secret" };
  return { text: rewritten, status: "applied" };
}

function unwrapCommandPrefixes(words) {
  while (words.length > 0) {
    const base = baseName(words[0]);
    if (base === "nohup" || base === "time" || base === "exec") {
      if (words.length === 1) return words;
      words = words.slice(1);
    } else if (base === "command") {
      if (words.length >= 2 && (words[1] === "-v" || words[1] === "-V")) return words;
      if (words.length === 1) return words;
      words = words.slice(1);
    } else if (base === "env") {
      let k = 1;
      while (k < words.length && /^[A-Za-z_][A-Za-z0-9_]*=/.test(words[k])) k++;
      if (k === 1 || k >= words.length) return null;
      words = words.slice(k);
    } else return words;
  }
  return words;
}

export function isValueClassSegment(seg) {
  seg = String(seg ?? "").trim();
  if (!seg || seg.includes("`") || /\$[A-Za-z_{!(?]/.test(seg)) return false;
  let words = stripShellKeywords(shellWordsOf(seg));
  if (words.length === 0) return false;
  words = unwrapCommandPrefixes(words);
  if (!words || words.length === 0) return false;
  const base = baseName(words[0]);
  const args = words.slice(1);
  if (["pwd", "date", "which", "dirname", "basename"].includes(base)) return true;
  if (base === "command") return args.length >= 1 && (args[0] === "-v" || args[0] === "-V");
  if (base === "go") {
    if (args[0] !== "env") return false;
    return !args.slice(1).some((a) => a === "-w" || a === "-u" || a.startsWith("-w=") || a.startsWith("-u="));
  }
  if (base === "wc") {
    let lines = false;
    for (const a of args) {
      if (a === "-l" || a === "--lines") lines = true;
      else if (["-c", "-m", "-L", "-w", "--bytes", "--chars", "--max-line-length", "--words"].includes(a)) return false;
    }
    return lines;
  }
  if (base === "ps") {
    for (let i = 0; i < args.length; i++) {
      const a = args[i];
      if ((a === "-o" || a === "--format" || a === "-O") && args[i + 1]?.endsWith("=")) return true;
      if (a.startsWith("-o") && a.endsWith("=") && a.length > 2) return true;
      if (a.startsWith("--format=") && a.endsWith("=")) return true;
    }
    return false;
  }
  if (base === "git") {
    if (args.length === 0 || args[0].startsWith("-")) return false;
    return ["rev-parse", "write-tree", "merge-base", "symbolic-ref", "describe", "hash-object"].includes(args[0]);
  }
  return false;
}

export function commandSubsAreValueClass(s) {
  let quote = "";
  let found = false;
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
    if (!quote && c === "'") {
      quote = "'";
      continue;
    }
    if (c === '"') {
      quote = quote === '"' ? "" : '"';
      continue;
    }
    if (c === "$" && s[i + 1] === "(") {
      const end = processSubstitutionEnd(s, i + 2);
      if (end < 0) return false;
      const inner = s.slice(i + 2, end);
      if (!isValueClassSegment(inner) || judge.opaqueCore(inner)) return false;
      found = true;
      i = end;
    }
  }
  return found;
}

export function isHarmlessSet(words) {
  if (!words || words.length < 2 || baseName(words[0]) !== "set") return false;
  const args = words.slice(1);
  if (args.length === 2 && (args[0] === "-o" || args[0] === "+o")) return ["pipefail", "errexit", "xtrace"].includes(args[1]);
  if (args.length !== 1) return false;
  const flag = args[0];
  if (flag.length < 2 || (flag[0] !== "-" && flag[0] !== "+") || flag.startsWith("--")) return false;
  return [...flag.slice(1)].every((c) => c === "e" || c === "x");
}
