// Mirrors internal/runner/shellpolicy/readonly_heredoc.go.
// Strips top-level quoted heredoc bodies (`<<'EOF'`, `<<"EOF"`, `<<-'EOF'`,
// `<<-"EOF"`) attached to safe non-piped Python/Node stdin invocations once
// readOnlyScriptCategory proves the script only reads and prints.
import { scanShellWordSpans } from "./command-data-args.js";
import { timeoutPrefixLength } from "./command-wrappers.js";
import { inlineScriptTool } from "./script-interpreter.js";

const READONLY_HEREDOC_SAFE_ENV = new Set([
  "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "NODE_ENV", "CI", "TZ",
  "LC_ALL", "LANG", "NO_COLOR", "FORCE_COLOR", "TERM", "GOWORK",
  "HOXI_ALLOW_DIRTY_EXPORT", "PYTHONIOENCODING", "PYTHONUTF8", "PYTHONDONTWRITEBYTECODE",
]);
const SAFE_ENV_VAL_RE = /^[A-Za-z0-9_.,:/=+-]*$/;
const ENV_ASSIGN_RE = /^[A-Za-z_][A-Za-z0-9_]*=/;
const HEREDOC_WORD_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const REDIRECT_WORD_RE = /^(?:\d*|&)>>?$/;
const DEV_NULL_WORD_RE = /^(?:\d*|&)>>?\/dev\/null$/;
const DUP_STREAM_WORD_RE = /^\d*>&[12]$/;
const SAFE_PY_FLAGS = new Set(["-u", "-B", "-E", "-s", "-S", "-O", "-OO", "-q", "-I"]);
const SAFE_NODE_FLAGS = new Set(["--no-warnings", "--input-type=module", "--input-type=commonjs", "-c", "--check"]);
const isBoundary = (c) => " \t\r\n;&|()<>".includes(c);
const baseName = (w) => (w.includes("/") ? w.slice(w.lastIndexOf("/") + 1) : w);

export function stripReadOnlyInterpreterHeredocs(s, judge, normalizeAbsoluteTool) {
  if (!judge || !s.includes("<<")) return null;
  const lines = s.replaceAll("\r\n", "\n").split("\n");
  const out = [];
  const state = { quote: "", outer: [] };
  let strippedAny = false;
  for (let i = 0; i < lines.length; i++) {
    const rawLine = lines[i];
    const taken = scanReadOnlyHeredocLine(rawLine, state);
    if (!taken) return null;
    if (taken.markers.length === 0) {
      out.push(rawLine);
      continue;
    }
    if (taken.markers.length !== 1 || state.outer.length !== 0 || state.quote !== "") {
      return null;
    }
    const m = taken.markers[0];
    if (!m.quoted) return null;
    const family = readOnlyHeredocOwnerFamily(taken.line, m.outPos, normalizeAbsoluteTool);
    if (!family) return null;
    const body = [];
    let found = false;
    while (i + 1 < lines.length) {
      i++;
      const cmp = m.dash ? lines[i].replace(/^\t+/, "") : lines[i];
      if (cmp === m.word) {
        found = true;
        break;
      }
      body.push(lines[i]);
    }
    if (!found) return null;
    if (judge(family, body.join("\n")) !== "") return null;
    out.push(taken.line);
    strippedAny = true;
  }
  return strippedAny ? out.join("\n") : null;
}

function scanReadOnlyHeredocLine(rawLine, st) {
  let out = "";
  const markers = [];
  for (let i = 0; i < rawLine.length; i++) {
    let c = rawLine[i];
    if (st.quote === "'") {
      if (c === "'") st.quote = "";
    } else if (c === "$" && rawLine[i + 1] === "(") {
      st.outer.push(st.quote);
      st.quote = "";
      out += c;
      i++;
      c = rawLine[i];
    } else if (st.quote === '"') {
      if (c === "\\" && i + 1 < rawLine.length) {
        out += c;
        i++;
        c = rawLine[i];
      } else if (c === "`") {
        return null;
      } else if (c === '"') {
        st.quote = "";
      }
    } else if (c === "`") {
      return null;
    } else if (c === "\\" && i + 1 < rawLine.length) {
      out += c;
      i++;
      c = rawLine[i];
    } else if (c === "(" && st.outer.length > 0) {
      st.outer.push("");
    } else if (c === ")" && st.outer.length > 0) {
      st.quote = st.outer.pop();
    } else if (c === "'" || c === '"') {
      st.quote = c;
    } else if (c === "#" && st.outer.length === 0 && (i === 0 || isBoundary(rawLine[i - 1]))) {
      out += rawLine.slice(i);
      return { line: out, markers };
    } else if (c === "<" && rawLine[i + 1] === "<") {
      if (rawLine[i + 2] === "<") return null;
      let j = i + 2;
      const m = { word: "", dash: false, quoted: false, outPos: out.length };
      if (rawLine[j] === "-") {
        m.dash = true;
        j++;
      }
      while (j < rawLine.length && (rawLine[j] === " " || rawLine[j] === "\t")) j++;
      if (rawLine[j] === "'" || rawLine[j] === '"') {
        const end = rawLine.indexOf(rawLine[j], j + 1);
        if (end < 0) return null;
        m.word = rawLine.slice(j + 1, end);
        m.quoted = true;
        j = end + 1;
        if (j < rawLine.length && !isBoundary(rawLine[j])) return null;
      } else {
        return null;
      }
      if (!HEREDOC_WORD_RE.test(m.word.replaceAll("-", "_"))) return null;
      markers.push(m);
      out += " ";
      i = j - 1;
      continue;
    }
    out += c;
  }
  return { line: out, markers };
}

function readOnlyHeredocOwnerFamily(line, outPos, normalizeAbsoluteTool) {
  let quote = "";
  let parenDepth = 0;
  let segStart = 0;
  let pipedIn = false;
  for (let i = 0; i <= line.length; i++) {
    if (i === line.length) {
      if (quote !== "" || parenDepth !== 0) return "";
      if (outPos >= segStart && outPos <= line.length) {
        if (pipedIn) return "";
        return validateReadOnlyHeredocSegment(line.slice(segStart), normalizeAbsoluteTool);
      }
      return "";
    }
    const c = line[i];
    const next = line[i + 1] || "";
    if (quote === "'") {
      if (c === "'") quote = "";
      continue;
    }
    if (c === "\\" && i + 1 < line.length) {
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
    if (parenDepth > 0) {
      if (i === outPos) return "";
      continue;
    }
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
    } else if (c === "&" && (i === 0 || line[i - 1] !== ">") && next !== ">") {
      sepLen = 1;
    }
    if (sepLen > 0) {
      if (outPos >= segStart && outPos <= i) {
        if (pipedIn || nextPipe) return "";
        return validateReadOnlyHeredocSegment(line.slice(segStart, i), normalizeAbsoluteTool);
      }
      pipedIn = nextPipe;
      i += sepLen - 1;
      segStart = i + 1;
    }
  }
  return "";
}

function isSafeReadOnlyHeredocEnv(word) {
  const eq = word.indexOf("=");
  if (eq <= 0) return false;
  const name = word.slice(0, eq);
  const value = word.slice(eq + 1);
  return READONLY_HEREDOC_SAFE_ENV.has(name) && SAFE_ENV_VAL_RE.test(value) && !value.includes("..");
}

function validateReadOnlyHeredocSegment(rawSeg, normalizeAbsoluteTool) {
  const seg = String(rawSeg || "").trim();
  if (!seg || /[$`()]/.test(seg)) return "";
  const spans = scanShellWordSpans(seg);
  if (!spans || spans.length === 0) return "";
  let idx = 0;
  while (idx < spans.length && ENV_ASSIGN_RE.test(spans[idx].word)) {
    if (!isSafeReadOnlyHeredocEnv(spans[idx].word)) return "";
    idx++;
  }
  while (idx < spans.length) {
    const head = baseName(normalizeAbsoluteTool ? normalizeAbsoluteTool(spans[idx].word) : spans[idx].word);
    if (head === "time" || head === "nohup" || head === "command") {
      if (head === "command" && idx + 1 < spans.length && (spans[idx + 1].word === "-v" || spans[idx + 1].word === "-V")) {
        return "";
      }
      idx++;
      continue;
    }
    if (head === "timeout" || head === "gtimeout") {
      const rem = spans.slice(idx).map((sp) => sp.word);
      const end = timeoutPrefixLength(rem);
      if (end === null) return "";
      idx += end;
      continue;
    }
    break;
  }
  if (idx >= spans.length) return "";
  const rawCmd = seg.slice(spans[idx].start, spans[idx].end);
  if (/['"\\<>]/.test(rawCmd)) return "";
  const family = inlineScriptTool(baseName(normalizeAbsoluteTool ? normalizeAbsoluteTool(spans[idx].word) : spans[idx].word));
  if (family !== "python" && family !== "python3" && family !== "node") return "";
  idx++;
  let seenDash = false;
  while (idx < spans.length) {
    const w = spans[idx].word;
    const rawTok = seg.slice(spans[idx].start, spans[idx].end);
    if (DEV_NULL_WORD_RE.test(w) || DUP_STREAM_WORD_RE.test(w)) {
      idx++;
      continue;
    }
    if (REDIRECT_WORD_RE.test(w) && idx + 1 < spans.length && spans[idx + 1].word === "/dev/null") {
      idx += 2;
      continue;
    }
    if (/[<>]/.test(rawTok)) return "";
    if (w === "-") {
      if (seenDash) return "";
      seenDash = true;
      idx++;
      continue;
    }
    if ((family === "node" && SAFE_NODE_FLAGS.has(w)) || (family !== "node" && SAFE_PY_FLAGS.has(w))) {
      idx++;
      continue;
    }
    return "";
  }
  return family;
}
