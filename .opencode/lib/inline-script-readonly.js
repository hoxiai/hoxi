// Auto mode's read-only check for inline interpreter code. Mirrors
// internal/runner/inline_script_readonly.go; `autoOpaque` in
// opaque-command.cases.json keeps both aligned.
//
// Auto mode runs `python3 tool.py` and `node x.mjs` without asking, so inline
// Python and Node code that only computes, reads files it names and prints
// runs too. Code that could write, delete or move files, change a database,
// start a process, open the network, read the environment, reach the home
// directory, load code dynamically, decrypt key material or name a secret
// path still asks. Perl, Ruby and PHP keep asking. This guards a cooperative
// agent against side effects it did not mean; it is not a sandbox.
import { isProtectedShellReadPath, isSecretPath, isSensitiveExternalReadPath } from "./external-path-risk.js";
import { SCRIPT_CAPABILITY_RE, scanUnattendedText, unattendedScriptCategoryWith } from "./unattended-policy.js";

const STD_WRITE_RE = /\b(?:sys|process)\s*\.\s*(?:stdout|stderr)\s*\.\s*write\b/g;
const RE_COMPILE_RE = /\bre\s*\.\s*compile\s*\(/g;
const MAIN_GUARD_RE = /__(?:name|main)__/g;
const SQL_EXEC_RE = /\.\s*execute\s*\(\s*/;
const READ_SQL_RE = /^\s*(?:select\b|pragma\s+(?:table_info|table_xinfo|index_list|index_info|index_xinfo|foreign_key_list|table_list|database_list)\b)/i;
const FIELD_RE = /^[A-Za-z_][A-Za-z0-9_.]*$/;

// Names file, database and process-state changes.
const MUTATION_RE = /write|appendfile|unlink|rmdir|rmtree|rename|mkdir|mkdtemp|mkstemp|mkfifo|mknod|chmod|chown|chflags|symlink|hardlink|link_to|truncate|utime|copyfile|copytree|shutil|tempfile|shelve|\bdbm\b|tarfile|zipfile|\bgzip\b|\bbz2\b|\blzma\b|mmap|fcntl|logging|webbrowser|\bsignal\b|\bkill|setuid|setgid|chdir|chroot|umask|\bO_(?:WRONLY|RDWR|CREAT|TRUNC|APPEND|EXCL)\b|\brm(?:sync|dir)?\b|\bcp(?:sync)?\b|\blink(?:sync)?\b|\btouch\b|\bsave\w*\s*\(|\bto_(?:csv|excel|parquet|pickle|json|html|sql|feather|hdf|stata|latex|markdown|xml|clipboard)\b|tofile|\bdump\s*\(|\bcommit\s*\(|\bbackup\b|load_extension|\b(?:delete|drop|insert|update|alter|vacuum|attach|upsert)\b|\breplace\s+into\b|\.\s*run\s*\(|\bvm\b|runin\w*context|\bconstructor\b|createrequire|globalthis|\breflect\b|\bproxy\b|\bmodule\s*\.\s*_|\bfs\w*\s*\[|\brequire\s*\([^)]*\)\s*\[|(?:^|[^.\w])(?:copy|move)\s*\(/i;
// An open mode that writes, appends or creates.
const WRITE_MODE_RE = /(?:^|[^\w])(?:'[rbtUs]*[wax+][rbtwaxUs+]*'|"[rbtUs]*[wax+][rbtwaxUs+]*")/;
// Reaches the home directory, the user database or key material.
const REACH_RE = /expanduser|expandvars|homedir|userinfo|\bhome\s*\(|getpass|keyring|keychain|\bpwd\b|getpw\w*|decrypt|decipher|aesgcm|fernet|private_?key|\bfrom\s+os\s+import\b|\bimport\s+os\s+as\b/i;
const PATHLIB_RE = /\bpathlib\b|\bPath\b/;
const REPLACE_RE = /\.\s*replace\s*\(/;
const OS_ATTR_RE = /\bos\s*\.\s*([A-Za-z_]\w*)/g;
const REQUIRE_RE = /\b(?:require|import)\s*\(\s*['"]([^'"]+)['"]|\bfrom\s+['"]([^'"]+)['"]/g;
const KEY_FILE_RE = /(?:\.(?:key|pem|p12|pfx|jks|keystore|ppk|asc|gpg)|(?:^|\/)id_[a-z0-9]+)$/i;

const OS_READ_ATTRS = new Set(["path", "sep", "linesep", "name", "getcwd", "stat", "lstat", "listdir", "scandir", "walk",
  "fspath", "fsencode", "fsdecode", "devnull", "curdir", "pardir", "extsep", "altsep", "pathsep", "cpu_count",
  "getpid", "getppid", "access", "readlink", "R_OK", "F_OK", "X_OK", "W_OK", "strerror", "get_terminal_size",
  "platform", "arch", "EOL", "cpus", "totalmem", "freemem", "hostname", "type", "release", "uptime", "endianness",
  "tmpdir", "loadavg", "version", "machine", "availableParallelism"]);
const BLOCKED_NODE_MODULES = new Set(["child_process", "http", "https", "http2", "net", "tls", "dgram", "dns", "cluster",
  "worker_threads", "vm", "inspector", "repl", "v8", "wasi", "module", "process", "async_hooks", "trace_events"]);

/** Whether every inline interpreter script in command only reads and prints. */
export function inlineScriptsReadOnly(command) {
  return unattendedScriptCategoryWith(String(command ?? ""), readOnlyScriptCategory) === "";
}

/** Judges one inline script; "" means it only reads. */
export function readOnlyScriptCategory(family, code) {
  if (family !== "python" && family !== "python3" && family !== "node") return "dynamic";
  const scanned = scanUnattendedText(code);
  if (scanned) return scanned;
  const idioms = maskPrintIdioms(code);
  const masked = maskReadOnlySQL(idioms);
  if (SCRIPT_CAPABILITY_RE.test(masked)) return "dynamic";
  // Query text is masked only for the capability list; a write keyword inside it still counts.
  if (MUTATION_RE.test(idioms) || WRITE_MODE_RE.test(idioms)) return "writes";
  if (REACH_RE.test(masked)) return "secret";
  if (PATHLIB_RE.test(masked) && REPLACE_RE.test(masked)) return "writes"; // Path.replace moves a file
  for (const m of masked.matchAll(OS_ATTR_RE)) {
    if (!OS_READ_ATTRS.has(m[1])) return "writes";
  }
  for (const m of masked.matchAll(REQUIRE_RE)) {
    const mod = (m[1] ?? m[2] ?? "").replace(/^node:/, "");
    if (BLOCKED_NODE_MODULES.has(mod.split("/")[0])) return "dynamic";
  }
  for (const raw of inlineStringLiterals(code)) {
    const lit = raw.trim();
    if (isSecretPath(lit) || isSensitiveExternalReadPath(lit) || isProtectedShellReadPath(lit) || KEY_FILE_RE.test(lit)) {
      return "secret";
    }
  }
  return "";
}

function maskPrintIdioms(code) {
  return code
    .replace(STD_WRITE_RE, "print")
    .replace(RE_COMPILE_RE, "re.pattern(")
    .replace(MAIN_GUARD_RE, "MAIN")
    .replaceAll("`", '"');
}

// Rewrites `.execute("SELECT …")` and read-only PRAGMAs so the capability
// list does not read them as exec. An f-string query passes only when its
// fields are plain names; a literal that cannot be parsed keeps `execute`.
function maskReadOnlySQL(code) {
  let out = "";
  let rest = code;
  for (;;) {
    const m = SQL_EXEC_RE.exec(rest);
    if (!m) return out + rest;
    out += rest.slice(0, m.index);
    const after = rest.slice(m.index + m[0].length);
    const lit = leadingPyStringLiteral(after);
    if (!lit || !READ_SQL_RE.test(lit.body) || (lit.isF && !plainFStringFields(lit.body))) {
      out += m[0];
      rest = after;
      continue;
    }
    out += '.query(""';
    rest = after.slice(lit.n);
  }
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

function inlineStringLiterals(code) {
  const out = [];
  for (let i = 0; i < code.length; i++) {
    const c = code[i];
    if (c !== '"' && c !== "'" && c !== "`") continue;
    let body = "";
    let j = i + 1;
    for (; j < code.length && code[j] !== c; j++) {
      if (code[j] === "\\" && j + 1 < code.length) j++;
      body += code[j];
    }
    out.push(body);
    i = j;
  }
  return out;
}
