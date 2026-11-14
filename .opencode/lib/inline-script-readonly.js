// Auto mode's read-only check for inline interpreter code. Mirrors
// internal/runner/inline_script_readonly.go; `autoOpaque` in
// opaque-command.cases.json keeps both aligned.
import { isProtectedShellReadPath, isSecretPath, isSensitiveExternalReadPath, claudeSkillPathHasAmbiguousInput } from "./external-path-risk.js";
import {
  FILE_CALLS,
  OPEN_KW_SAFE,
  READ_MODES,
  ZERO_ARG_PATH_CALLS,
  cleanInlinePosixPath,
  decodeInlineEscapes,
  lexInlineScript,
} from "./inline-script-literals.js";
import {
  cartesianConcat,
  collectInlinePathVars,
  matchingCloseParen,
  normalizeInlinePathAliases,
  resolveInlinePathTokens,
  rewriteRegexExecCalls,
  splitCallArgs,
} from "./inline-script-paths.js";
import { setInlineReadOnlyJudge } from "./shell-substitution.js";
import { maskInlineDataFields } from "./inline-script-fields.js";
import { SCRIPT_CAPABILITY_RE, scanUnattendedText, unattendedScriptCategoryWith } from "./unattended-policy.js";

const STD_WRITE_RE = /\b(?:sys|process)\s*\.\s*(?:stdout|stderr)\s*\.\s*write\b/g;
const SAFE_PROCESS_RE = /\bprocess\s*\.\s*(?:argv|cwd|version|versions|platform|arch|pid|ppid|hrtime|memoryUsage|cpuUsage|uptime|stdin)\b/g;
const UNSAFE_PROCESS_RE = /\bprocess\b/;
const RE_COMPILE_RE = /\bre\s*\.\s*compile\s*\(/g;
const MAIN_GUARD_RE = /\b__(?:name|main|dirname|filename)__\b/g;
const SQL_EXEC_RE = /\.\s*execute\s*\(\s*/;
const READ_SQL_RE = /^\s*(?:select\b|pragma\s+(?:table_info|table_xinfo|index_list|index_info|index_xinfo|foreign_key_list|table_list|database_list)\b)/i;
const SQL_WRITE_RE = /\b(?:delete|drop|insert|update|alter|vacuum|attach|upsert|create|replace|reindex|pragma\s+\w+\s*=)\b/i;
const FIELD_RE = /^[A-Za-z_][A-Za-z0-9_.]*$/;

// Names file, database and process-state changes on masked code.
const MUTATION_RE = /\b(?:create)?write\w*\b|\bappendfile(?:sync)?\b|\bunlink(?:sync)?\b|\bremove(?:dirs)?\b|\brmdir(?:sync)?\b|\brmtree\b|\brename(?:s|sync)?\b|\bmkdir(?:sync|p)?\b|\bmkdtemp(?:sync)?\b|\bmkstemp\b|\bmkfifo\b|\bmknod\b|\bf?chmod(?:sync)?\b|\blchmod(?:sync)?\b|\bf?chown(?:sync)?\b|\blchown(?:sync)?\b|\bchflags\b|\bsymlink(?:sync)?\b|\bhardlink\b|\blink_to\b|\bf?truncate(?:sync)?\b|\bf?utimes?(?:sync)?\b|\blutimes(?:sync)?\b|\bcopyfile(?:sync)?\b|\bcopytree\b|\bshutil\b|\btempfile\b|\bfileinput\b|\bshelve\b|\bdbm\b|\btarfile\b|\bzipfile\b|\bgzip\b|\bbz2\b|\blzma\b|\bmmap\b|\bfcntl\b|\blogging\b|\bwebbrowser\b|\bsignal\b|\bkill\w*\b|\bsetuid\b|\bsetgid\b|\bchdir\b|\bchroot\b|\bumask\b|\bposix\b|\bnt\b|\bimportlib\b|\bO_(?:WRONLY|RDWR|CREAT|TRUNC|APPEND|EXCL)\b|\brm(?:sync|dir)?\b|\bcp(?:sync)?\b|\blink(?:sync)?\b|\btouch\b|\bsave\w*\s*\(|\bto_(?:csv|excel|parquet|pickle|json|html|sql|feather|hdf|stata|latex|markdown|xml|clipboard)\b|\btofile\b|\bdump\s*\(|\bcommit\s*\(|\bbackup\b|\bload_extension\b|\b(?:drop|insert|alter|vacuum|attach|upsert)\b|\breplace\s+into\b|\.\s*run\s*\(|\bvm\b|\brunin\w*context\b|\bconstructor\b|\bcreaterequire\b|\bglobalthis\b|\breflect\b|\bproxy\b|\bmodule\s*\.\s*_|\bfs\w*\s*\[|\brequire\s*\([^)]*\)\s*\[|\]\s*\(|[{,]\s*\[[^[\]]*\]\s*:|(?:^|[^.\w])(?:copy|move)\s*\(/i;
const REACH_RE = /\b(?:expanduser|expandvars|homedir|userinfo|getpass|keyring|keychain|pwd|decrypt|decipher|aesgcm|fernet|private_?key)\b|\bgetpw\w*|\bhome\s*\(|\bfrom\s+(?:os|posix|nt)\s+import\b|\bimport\s+(?:os|posix|nt)\s+as\b/i;
const DYNAMIC_STRING_RE = /\b(?:chr|ord|bytes|bytearray|memoryview|fromhex|b64decode|b64encode|unhexlify|hexlify|codecs|atob|btoa|fromCharCode|fromCodePoint|getattr|setattr|delattr|globals|locals|vars|__import__|__builtins__|__dict__|__subclasses__|__class__|__bases__|__mro__|unescape|decodeURI|decodeURIComponent|TextDecoder)\b|\bBuffer\s*\.\s*(?:from|alloc\w*)\b|\bString\s*\.\s*raw\s*\(|\\(?:x[0-9a-fA-F]{2}|u[0-9a-fA-F]{4}|u\{|U[0-9a-fA-F]{8}|N\{|[0-7]{1,3})/;
const SQLITE_MENTION_RE = /\bsqlite3?\b|\.\s*connect\s*\(|\bconnect\s*\(/i;
const SQLITE_CONNECT_RE = /(?:\.\s*|\b)connect\s*\(\s*/g;
const URI_READONLY_RE = /^\s*,\s*uri\s*=\s*True\s*,?\s*$/;
const PATHLIB_RE = /\bpathlib\b|\bPath\b/;
const REPLACE_RE = /\.\s*replace\s*\(/;
const OS_ATTR_RE = /\bos\s*\.\s*([A-Za-z_]\w*)/g;
const KEY_FILE_RE = /(?:\.(?:key|pem|p12|pfx|jks|keystore|ppk|asc|gpg)|(?:^|\/)id_[a-z0-9_.-]+)$/i;
const LIT_KEY_FILE_RE = /(?:\.(?:key|pem|p12|pfx|jks|keystore|ppk|asc|gpg)|(?:^|\/)id_(?:rsa|dsa|ecdsa|ed25519)[a-z0-9_.-]*)$/i;

const OS_READ_ATTRS = new Set(["path", "sep", "linesep", "name", "getcwd", "stat", "lstat", "listdir", "scandir", "walk",
  "fspath", "fsencode", "fsdecode", "devnull", "curdir", "pardir", "extsep", "altsep", "pathsep", "cpu_count",
  "getpid", "getppid", "access", "readlink", "R_OK", "F_OK", "X_OK", "W_OK", "strerror", "get_terminal_size",
  "platform", "arch", "EOL", "cpus", "totalmem", "freemem", "hostname", "type", "release", "uptime", "endianness",
  "tmpdir", "loadavg", "version", "machine", "availableParallelism"]);
const BLOCKED_NODE_MODULES = new Set(["child_process", "http", "https", "http2", "net", "tls", "dgram", "dns", "cluster",
  "worker_threads", "vm", "inspector", "repl", "v8", "wasi", "module", "process", "async_hooks", "trace_events", "sqlite"]);

/** Whether every inline interpreter script in command only reads and prints. */
export function inlineScriptsReadOnly(command) {
  return unattendedScriptCategoryWith(String(command ?? ""), readOnlyScriptCategory) === "";
}

/** Judges one inline script; "" means it only reads. */
export function readOnlyScriptCategory(family, code) {
  if (family !== "python" && family !== "python3" && family !== "node") return "dynamic";
  const isJS = family === "node";
  const rawLex = lexInlineScript(family, code);
  if (!rawLex) return "dynamic";
  if (isJS) rawLex.toks = normalizeInlinePathAliases(code, rawLex.toks);
  const sqlLex = lexInlineScript(family, maskReadOnlySQL(isJS ? maskInlineDataFields(code, rawLex.toks) : code));
  if (!sqlLex) return "dynamic";
  const { pathVars, regexVars } = collectInlinePathVars(family, code, rawLex.toks);
  const masked = maskPrintIdioms(rewriteRegexExecCalls(isJS, regexVars, sqlLex.mask));
  const scanned = scanUnattendedText(masked);
  if (scanned) return scanned;
  if (SCRIPT_CAPABILITY_RE.test(masked) || (isJS && UNSAFE_PROCESS_RE.test(masked))) return "dynamic";
  const { filesOK, openModesOK, requiresOK } = validateInlineCalls(rawLex.toks, pathVars);
  if (!requiresOK) return "dynamic";
  if (!openModesOK || MUTATION_RE.test(masked)) return "writes";
  if (SQLITE_MENTION_RE.test(masked) && !hasOnlyReadOnlySQLiteConnect(code)) return "writes";
  if (REACH_RE.test(masked) || DYNAMIC_STRING_RE.test(masked) || !filesOK) return "secret";
  if (PATHLIB_RE.test(masked) && REPLACE_RE.test(masked)) return "writes";
  for (const m of masked.matchAll(OS_ATTR_RE)) {
    if (!OS_READ_ATTRS.has(m[1])) return "writes";
  }
  for (const raw of rawLex.lits) {
    if (isDisallowedInlineLiteral(raw)) return "secret";
  }
  return "";
}

function maskPrintIdioms(codeMask) {
  return codeMask
    .replace(STD_WRITE_RE, "print")
    .replace(SAFE_PROCESS_RE, "proc_ro")
    .replace(RE_COMPILE_RE, "re.pattern(")
    .replace(MAIN_GUARD_RE, "MAIN");
}

function maskReadOnlySQL(code) {
  let out = "";
  let rest = code;
  for (;;) {
    const m = SQL_EXEC_RE.exec(rest);
    if (!m) return out + rest;
    out += rest.slice(0, m.index);
    const after = rest.slice(m.index + m[0].length);
    const lit = leadingPyStringLiteral(after);
    const decoded = lit ? decodeInlineEscapes(false, lit.body, false) : null;
    if (!lit || decoded === null || decoded.includes(";") || !READ_SQL_RE.test(decoded) ||
      SQL_WRITE_RE.test(decoded) || (lit.isF && !plainFStringFields(lit.body))) {
      out += m[0];
      rest = after;
      continue;
    }
    out += lit.isF ? `.query(f"${extractFStringRefsOnly(lit.body)}"` : '.query("sql"';
    rest = after.slice(lit.n);
  }
}

function extractFStringRefsOnly(body) {
  let out = "";
  for (let i = 0; i < body.length; i++) {
    if (body[i] === "{" && body[i + 1] !== "{") {
      const end = body.indexOf("}", i);
      if (end > i) {
        out += body.slice(i, end + 1);
        i = end;
      }
    } else if (body[i] === "{" || body[i] === "}") {
      i++;
    }
  }
  return out;
}

function leadingPyStringLiteral(s) {
  let i = 0;
  let isF = false;
  while (i < s.length && i < 2 && "rRbBuUfF".includes(s[i])) {
    if (s[i] === "f" || s[i] === "F") isF = true;
    i++;
  }
  if (i >= s.length || (s[i] !== '"' && s[i] !== "'")) return null;
  let q = s[i];
  if (s.startsWith(q + q + q, i)) q = q + q + q;
  const start = i + q.length;
  for (let j = start; j < s.length; j++) {
    if (s[j] === "\\") {
      j++;
      continue;
    }
    if (s.startsWith(q, j)) return { body: s.slice(start, j), n: j + q.length, isF };
  }
  return null;
}

function plainFStringFields(body) {
  for (let i = 0; i < body.length; i++) {
    if (body[i] === "{") {
      if (body[i + 1] === "{") {
        i++;
        continue;
      }
      const end = body.indexOf("}", i);
      if (end < 0 || !FIELD_RE.test(body.slice(i + 1, end))) return false;
      i = end;
    } else if (body[i] === "}") {
      if (body[i + 1] === "}") {
        i++;
        continue;
      }
      return false;
    }
  }
  return true;
}

function isImportBindingPos(toks, idx) {
  for (let k = idx - 1; k >= 0; k--) {
    if (toks[k].kind === "id" && toks[k].val === "import") return true;
    if (toks[k].kind === "id" || (toks[k].kind === "op" && toks[k].val === ",")) continue;
    return false;
  }
  return false;
}

function isPathFirstOpenCall(toks, idx) {
  if (idx === 0 || toks[idx - 1].kind !== "op" || toks[idx - 1].val !== "." || toks[idx].val === "openSync") {
    return true;
  }
  return idx >= 2 && toks[idx - 2].kind === "id" && ["io", "fs", "promises", "fsPromises"].includes(toks[idx - 2].val);
}

function isKeywordArg(arg) {
  if (arg.length >= 3 && arg[0].kind === "id" && arg[1].kind === "op" && arg[1].val === "=") {
    return { kw: arg[0].val, val: arg.slice(2) };
  }
  return null;
}

function checkOpenCallArgs(toks, idx, args) {
  const pathFirst = isPathFirstOpenCall(toks, idx);
  let posIdx = 0;
  for (const arg of args) {
    const kwArg = isKeywordArg(arg);
    if (kwArg) {
      const { kw, val } = kwArg;
      if (kw === "mode" || kw === "flags") {
        if (val.length !== 1 || val[0].kind !== "str" || !READ_MODES.has(val[0].val)) return false;
        continue;
      }
      if (!OPEN_KW_SAFE.has(kw)) return false;
      if (val.length === 2 && kw === "buffering" && val[0].kind === "op" && val[0].val === "-" && val[1].kind === "num") continue;
      if (val.length !== 1 || (val[0].kind !== "str" && val[0].kind !== "num" && !(val[0].kind === "id" && val[0].val === "None"))) {
        return false;
      }
      continue;
    }
    if (pathFirst && posIdx === 0) {
      posIdx++;
      continue;
    }
    const modePos = pathFirst ? 1 : 0;
    if (posIdx === modePos) {
      if (arg.length !== 1 || arg[0].kind !== "str" || !READ_MODES.has(arg[0].val)) return false;
      posIdx++;
      continue;
    }
    return false;
  }
  return !pathFirst || posIdx >= 1;
}

function checkFileCallPaths(fnName, args, vars) {
  const posArgs = args.filter((a) => !isKeywordArg(a));
  if (posArgs.length === 0) return ZERO_ARG_PATH_CALLS.has(fnName);
  if ((fnName === "readFileSync" || fnName === "readFile") && posArgs[0].length === 1 && posArgs[0][0].kind === "num" && posArgs[0][0].val === "0") {
    return true;
  }
  const multiSeg = ["Path", "PurePath", "PosixPath", "WindowsPath", "joinpath"].includes(fnName);
  const checked = multiSeg ? posArgs : posArgs.slice(0, 1);
  let joined = [];
  for (let idx = 0; idx < checked.length; idx++) {
    const vals = resolveInlinePathTokens(checked[idx], vars, 0);
    if (!vals || vals.length === 0) return false;
    if (vals.some(isDisallowedFileCallTarget)) return false;
    joined = idx === 0 ? [...vals] : cartesianConcat(joined, vals, true);
    if (!joined) return false;
  }
  if (multiSeg && checked.length > 1 && joined.some(isDisallowedFileCallTarget)) return false;
  return true;
}

function validateInlineCalls(toks, vars) {
  let filesOK = true;
  let openModesOK = true;
  let requiresOK = true;
  for (let i = 0; i < toks.length; i++) {
    const t = toks[i];
    if (t.kind === "tpl") {
      for (const sub of t.exprs) {
        const subRes = validateInlineCalls(sub, vars);
        if (!subRes.filesOK) filesOK = false;
        if (!subRes.openModesOK) openModesOK = false;
        if (!subRes.requiresOK) requiresOK = false;
      }
      continue;
    }
    if (t.kind !== "id") continue;
    const isProp = i > 0 && toks[i - 1].kind === "op" && toks[i - 1].val === ".";
    if (t.val === "from" && !isProp && i + 1 < toks.length && toks[i + 1].kind === "str") {
      const mod = toks[i + 1].val.replace(/^node:/, "");
      if (BLOCKED_NODE_MODULES.has(mod.split("/", 1)[0])) requiresOK = false;
    }
    if (t.val === "require" && !isProp) {
      if (i + 1 >= toks.length || toks[i + 1].kind !== "op" || toks[i + 1].val !== "(") {
        requiresOK = false;
        continue;
      }
      const closeIdx = matchingCloseParen(toks, i + 1);
      if (closeIdx >= toks.length || closeIdx !== i + 3 || toks[i + 2].kind !== "str") {
        requiresOK = false;
        continue;
      }
      const mod = toks[i + 2].val.replace(/^node:/, "");
      if (BLOCKED_NODE_MODULES.has(mod.split("/", 1)[0])) requiresOK = false;
    }
    if (t.val === "open" || t.val === "openSync") {
      if (i + 1 >= toks.length || toks[i + 1].kind !== "op" || toks[i + 1].val !== "(") {
        openModesOK = false;
        continue;
      }
      const closeIdx = matchingCloseParen(toks, i + 1);
      if (closeIdx >= toks.length) {
        openModesOK = false;
        continue;
      }
      const args = splitCallArgs(toks.slice(i + 2, closeIdx));
      if (!args || !checkOpenCallArgs(toks, i, args)) openModesOK = false;
    }
    if (FILE_CALLS.has(t.val)) {
      if (isImportBindingPos(toks, i)) continue;
      if (i + 1 >= toks.length || toks[i + 1].kind !== "op" || toks[i + 1].val !== "(") {
        filesOK = false;
        continue;
      }
      if (t.val === "open" && !isPathFirstOpenCall(toks, i)) continue;
      const closeIdx = matchingCloseParen(toks, i + 1);
      if (closeIdx >= toks.length) {
        filesOK = false;
        continue;
      }
      const args = splitCallArgs(toks.slice(i + 2, closeIdx));
      if (!args || !checkFileCallPaths(t.val, args, vars)) filesOK = false;
    }
  }
  return { filesOK, openModesOK, requiresOK };
}

function isDisallowedFileCallTarget(raw) {
  if (claudeSkillPathHasAmbiguousInput(raw)) return true;
  if (typeof raw !== "string" || raw.includes("\0")) return true;
  const p = raw.trim();
  if (!p) return true;
  for (const cand of [p, cleanInlinePosixPath(p)]) {
    if (!cand) continue;
    if (isSecretPath(cand) || isSensitiveExternalReadPath(cand) || isProtectedShellReadPath(cand) ||
      KEY_FILE_RE.test(cand) || scanUnattendedText(cand) === "secret") {
      return true;
    }
  }
  return false;
}

function isDisallowedInlineLiteral(raw) {
  if (claudeSkillPathHasAmbiguousInput(raw)) return true;
  const p = String(raw ?? "").trim();
  if (!p) return false;
  for (const cand of [p, cleanInlinePosixPath(p)]) {
    if (!cand) continue;
    if (isSecretPath(cand) || isSensitiveExternalReadPath(cand) || isProtectedShellReadPath(cand) ||
      LIT_KEY_FILE_RE.test(cand) || scanUnattendedText(cand) === "secret") {
      return true;
    }
  }
  return false;
}

function hasOnlyReadOnlySQLiteConnect(code) {
  const matches = [...code.matchAll(SQLITE_CONNECT_RE)];
  if (matches.length === 0) return false;
  for (const m of matches) {
    const after = code.slice(m.index + m[0].length);
    const rawLit = leadingPyStringLiteral(after);
    const lit = rawLit ? decodeInlineEscapes(false, rawLit.body, false) : null;
    if (!rawLit || lit === null || rawLit.isF) return false;
    const rest = after.slice(rawLit.n);
    const closeIdx = rest.indexOf(")");
    if (closeIdx < 0) return false;
    const extraArgs = rest.slice(0, closeIdx);
    if (extraArgs.includes("(") || extraArgs.includes('"') || extraArgs.includes("'")) return false;
    if (lit === ":memory:") {
      if (extraArgs.trim() !== "") return false;
      continue;
    }
    if (!lit.startsWith("file:") || !URI_READONLY_RE.test(extraArgs)) return false;
    const withoutPrefix = lit.slice("file:".length);
    const qIdx = withoutPrefix.indexOf("?");
    if (qIdx < 0) return false;
    const dbPath = withoutPrefix.slice(0, qIdx).trim();
    const query = withoutPrefix.slice(qIdx + 1);
    if (isDisallowedFileCallTarget(dbPath)) return false;
    let hasModeRO = false;
    for (const param of query.split("&")) {
      if (param === "mode=ro") hasModeRO = true;
      else if (param.toLowerCase().startsWith("mode=")) return false;
    }
    if (!hasModeRO) return false;
  }
  return true;
}

setInlineReadOnlyJudge(readOnlyScriptCategory);
