// Mirrors internal/runner/command_data_args.go. Literal "data" arguments
// (commit messages, search patterns, echo text) are replaced with a
// placeholder before the destructive patterns run, so `git commit -m
// "restore ..."` or `rg "Shutdown"` is not mistaken for `git restore` or a
// shutdown. Anything the tokenizer cannot read with certainty leaves the
// command untouched. .opencode/lib/permission-policy.cases.json keeps both
// ports aligned.

const MASKED_DATA_ARGUMENT = "Q";

const setOf = (...items) => new Set(items);

// Run their arguments or input as shell commands: masking is skipped anywhere.
const DATA_ARG_INTERPRETERS = setOf("sh", "bash", "zsh", "dash", "ksh", "fish", "eval", "exec", "source", ".",
  "xargs", "env", "sudo", "su", "doas", "nohup", "time", "command", "builtin", "ssh", "parallel",
  "watch", "osascript");
// Can execute text they are fed; only block masking when echo/printf text
// reaches them through a pipe or a written file.
const DATA_SCRIPT_INTERPRETERS = setOf("python", "python3", "node", "perl", "ruby", "php", "awk", "sed");

const RG_VALUE_FLAGS = setOf("-A", "-B", "-C", "-m", "-g", "-t", "-T", "-M", "-j", "-d", "-E", "-r",
  "--glob", "--iglob", "--type", "--type-not", "--type-add", "--type-clear", "--max-count",
  "--context", "--after-context", "--before-context", "--max-columns", "--threads",
  "--max-depth", "--maxdepth", "--sort", "--sortr", "--color", "--colors", "--encoding",
  "--replace", "--max-filesize", "--path-separator", "--context-separator", "--engine",
  "--pre", "--pre-glob", "--dfa-size-limit", "--regex-size-limit", "--field-match-separator");
const GREP_VALUE_FLAGS = setOf("-A", "-B", "-C", "-m", "-d", "-D", "--include", "--exclude",
  "--exclude-dir", "--exclude-from", "--max-count", "--context", "--after-context",
  "--before-context", "--color", "--colour", "--label", "--binary-files", "--devices",
  "--directories", "--group-separator");
const ECHO_OPTION_RE = /^-[neE]+$/;
const GIT_GLOBAL_VALUE_FLAGS = setOf("-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path");
const NON_LITERAL_CHARS = new Set(["\\", "$", "*", "?", "[", "<", ">", "~", "{", "}", "!"]);
const WORD_END = new Set([" ", "\t", "\r", ";", "\n", "|", "&"]);

function baseName(word) {
  const slash = word.lastIndexOf("/");
  return slash === -1 ? word : word.slice(slash + 1);
}

// Returns null for unbalanced quotes and grouping parentheses.
function tokenize(s) {
  const tokens = [];
  let i = 0;
  while (i < s.length) {
    let c = s[i];
    if (c === " " || c === "\t" || c === "\r") {
      i++;
      continue;
    }
    if (c === "(" || c === ")") return null;
    if (c === ";" || c === "\n" || c === "|" || c === "&") {
      let j = i + 1;
      if (j < s.length && (s[j] === "|" || s[j] === "&")) j++;
      tokens.push({ start: i, end: j, separator: true });
      i = j;
      continue;
    }
    const start = i;
    let value = "";
    let literal = true;
    while (i < s.length) {
      c = s[i];
      if (WORD_END.has(c)) break;
      if (c === "(" || c === ")") return null;
      if (c === "'") {
        const end = s.indexOf("'", i + 1);
        if (end < 0) return null;
        value += s.slice(i + 1, end);
        i = end + 1;
        continue;
      }
      if (c === '"') {
        let j = i + 1;
        let closed = false;
        while (j < s.length) {
          if (s[j] === "\\" && j + 1 < s.length) {
            value += s[j + 1];
            j += 2;
            continue;
          }
          if (s[j] === '"') {
            closed = true;
            break;
          }
          if (s[j] === "$") literal = false;
          value += s[j];
          j++;
        }
        if (!closed) return null;
        i = j + 1;
        continue;
      }
      if (NON_LITERAL_CHARS.has(c)) literal = false;
      value += c;
      i++;
    }
    tokens.push({ start, end: i, value, literal });
  }
  return tokens;
}

function gitDataArguments(seg) {
  let i = 1;
  while (i < seg.length && seg[i].value.startsWith("-")) {
    if (GIT_GLOBAL_VALUE_FLAGS.has(seg[i].value)) i++;
    i++;
  }
  if (i >= seg.length) return [];
  const sub = seg[i].value;
  let valueFlags;
  if (["commit", "tag", "merge", "stash"].includes(sub)) valueFlags = setOf("-m", "--message");
  else if (sub === "log" || sub === "shortlog") valueFlags = setOf("--grep", "-S", "-G");
  else return [];
  const out = [];
  for (let j = i + 1; j < seg.length; j++) {
    const v = seg[j].value;
    const eq = v.indexOf("=");
    if (eq > 0 && valueFlags.has(v.slice(0, eq))) continue;
    const short = sub === "commit" && v.length > 2 && v[0] === "-" && v[1] !== "-" && v.endsWith("m");
    if ((valueFlags.has(v) || short) && j + 1 < seg.length) {
      out.push(j + 1);
      j++;
    }
  }
  return out;
}

function searchPatternArguments(seg, valueFlags) {
  const explicit = [];
  let positional = -1;
  let usesPatternFile = false;
  let endOfOptions = false;
  for (let j = 1; j < seg.length; j++) {
    const v = seg[j].value;
    if (!endOfOptions && v.startsWith("-") && v !== "-") {
      if (v === "--") {
        endOfOptions = true;
      } else if (v === "-e" || v === "--regexp") {
        if (j + 1 < seg.length) {
          explicit.push(j + 1);
          j++;
        }
      } else if (v === "-f" || v === "--file" || v.startsWith("--file=")) {
        usesPatternFile = true;
        if (v === "-f" || v === "--file") j++;
      } else if (valueFlags.has(v)) {
        j++;
      }
      continue;
    }
    if (positional < 0) positional = j;
  }
  if (explicit.length > 0 || usesPatternFile) return explicit;
  return positional > 0 ? [positional] : [];
}

function dataArgumentIndexes(seg) {
  switch (baseName(seg[0].value)) {
    case "echo":
    case "printf": {
      const out = [];
      for (let i = 1; i < seg.length && !/[<>]/.test(seg[i].value); i++) {
        if (!ECHO_OPTION_RE.test(seg[i].value)) out.push(i);
      }
      return out;
    }
    case "git":
      return gitDataArguments(seg);
    case "rg":
      return searchPatternArguments(seg, RG_VALUE_FLAGS);
    case "grep":
    case "egrep":
    case "fgrep":
      return searchPatternArguments(seg, GREP_VALUE_FLAGS);
    default:
      return [];
  }
}

export function maskCommandDataArguments(command) {
  const s = String(command ?? "");
  if (s.includes("`") || s.includes("$(") || s.includes("<<")) return s;
  const tokens = tokenize(s);
  if (!tokens) return s;
  const segments = [];
  const pipedIn = [];
  let cur = [];
  let lastSep = "";
  for (const tok of tokens) {
    if (tok.separator) {
      if (cur.length > 0) {
        segments.push(cur);
        pipedIn.push(lastSep === "|");
      }
      cur = [];
      lastSep = s.slice(tok.start, tok.end);
    } else {
      cur.push(tok);
    }
  }
  if (cur.length > 0) {
    segments.push(cur);
    pipedIn.push(lastSep === "|");
  }
  const isEcho = (seg) => ["echo", "printf"].includes(baseName(seg[0].value));
  const echoWritesFile = segments.some((seg) => isEcho(seg) && seg.slice(1).some((w) => /[<>]/.test(w.value)));
  for (let i = 0; i < segments.length; i++) {
    const name = baseName(segments[i][0].value);
    if (DATA_ARG_INTERPRETERS.has(name)) return s;
    if (DATA_SCRIPT_INTERPRETERS.has(name) && (echoWritesFile || (pipedIn[i] && i > 0 && isEcho(segments[i - 1])))) {
      return s;
    }
  }
  const masked = [];
  for (const seg of segments) {
    for (const i of dataArgumentIndexes(seg)) {
      // A redirect target is a path, never data.
      if (seg[i].literal && !/[<>]/.test(seg[i - 1].value)) masked.push(seg[i]);
    }
  }
  if (masked.length === 0) return s;
  let out = "";
  let last = 0;
  for (const tok of masked) {
    out += s.slice(last, tok.start) + MASKED_DATA_ARGUMENT;
    last = tok.end;
  }
  return out + s.slice(last);
}
