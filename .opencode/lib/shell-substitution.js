// Mirrors extractProcessSubstitutions / destructiveScanForms in
// internal/runner/opaque_loops.go and joinLineContinuations /
// stripLiteralHeredocs in internal/runner/opaque_command.go: `<(cmd)` becomes
// a placeholder argument and `$(cmd)` the $__hoxi_cmdsub__ variable, and cmd
// is appended as its own line, so it is judged like any other command.
import { maskCommandDataArguments } from "./command-data-args.js";
import {
  DOLLAR_EXPAND_RE,
  isProtectedPath,
  isSecretPath,
  isSensitiveExternalPath,
  normalizeAbsoluteTool,
  shellWords,
  splitCommandChainWithPipes,
  stripNullRedirects,
} from "./permission-policy.js";
import { concretizeLiterals, eraseSubshells, findSubshellSpans } from "./shell-constants.js";
import { isSafeCurlOutputTarget, normalizeHomePrefixPath } from "./shell-loops.js";

const PROCESS_SUBSTITUTION_PLACEHOLDER = "/dev/fd/63";
export const CMD_SUBST_VAR = "__hoxi_cmdsub__";
const MASKED_DATA_ARGUMENT = "Q";
const HEREDOC_WORD_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const SSH_HOST_SPEC_RE = /^(?:[A-Za-z0-9_.-]+@)?([A-Za-z0-9_.-]+)$/;
const SSH_SAFE_SHORT_FLAG_RE = /^-[TtnNqv46AaCxXYy]+$/;
const SCP_SAFE_SHORT_FLAG_RE = /^-[rpfqvBC46OT]+$/;
const SSH_UNSAFE_OPT_RE = /(?:proxycommand|localcommand|permitlocalcommand|include|identityagent)/i;
const SSH_DURATION_RE = /^\d+[smhd]?$/;
const ENV_ASSIGN_RE = /^[A-Za-z_][A-Za-z0-9_]*=/;
const REDIRECT_WORD_RE = /^(?:\d*|&)>>?$/;
const DEV_NULL_WORD_RE = /^(?:\d*|&)>>?\/dev\/null$/;
const DUP_STREAM_WORD_RE = /^\d*>&[12]$/;
const SHELL_KEYWORDS = new Set(["while", "until", "do", "done", "if", "then", "else", "elif", "fi", "!"]);
const BENIGN_TEMP_CONSUMERS = new Set(["scp", "ls", "wc", "head", "tail", "stat", "file", "md5", "shasum", "sha256sum", "rm", "echo", "printf", "grep", "rg", "sed"]);
const SSH_BLOCKED_KEY_BASENAMES = new Set(["authorized_keys", "known_hosts", "config", "environment", "rc", ".ssh"]);
const isBoundary = (c) => " \t\r\n;&|()<>".includes(c);
const baseName = (w) => (w.includes("/") ? w.slice(w.lastIndexOf("/") + 1) : w);

function processSubstitutionEnd(s, start) {
  let quote = "";
  for (let i = start; i < s.length; i++) {
    const c = s[i];
    if (quote === "'") {
      if (c === "'") quote = "";
    } else if (quote === '"') {
      if (c === "\\") i++;
      else if (c === '"') quote = "";
    } else if (c === "\\") {
      i++;
    } else if (c === "'" || c === '"') {
      quote = c;
    } else if (c === "(") {
      return -1;
    } else if (c === ")") {
      return i;
    }
  }
  return -1;
}

export function extractProcessSubstitutions(s) {
  let out = "";
  const inner = [];
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    let c = s[i];
    if (quote === "'") {
      if (c === "'") quote = "";
    } else if (c === "$" && s[i + 1] === "(") {
      const end = processSubstitutionEnd(s, i + 2);
      if (end < 0) return s; // `$((…))` arithmetic or nesting stays opaque
      inner.push(s.slice(i + 2, end));
      out += "$" + CMD_SUBST_VAR;
      i = end;
      continue;
    } else if (quote === '"') {
      if (c === "\\" && i + 1 < s.length) {
        out += c;
        i++;
        c = s[i];
      } else if (c === '"') {
        quote = "";
      }
    } else if (c === "\\" && i + 1 < s.length) {
      out += c;
      i++;
      c = s[i];
    } else if (c === "'" || c === '"') {
      quote = c;
    } else if (c === "<" && s[i + 1] === "(" && (i === 0 || isBoundary(s[i - 1]))) {
      const end = processSubstitutionEnd(s, i + 2);
      if (end < 0) return s;
      inner.push(s.slice(i + 2, end));
      out += PROCESS_SUBSTITUTION_PLACEHOLDER;
      i = end;
      continue;
    }
    out += c;
  }
  return inner.length === 0 ? s : `${out}\n${inner.join("\n")}`;
}

// The texts the destructive patterns scan: the masked command, plus its
// unpacked form when it uses `<(…)` (`cat <(cat .env)` → `cat .env`).
export function destructiveScanForms(cmd) {
  let scanBase = String(cmd ?? "");
  let prepared = joinLineContinuations(scanBase.trim());
  let hadBenignHeredoc = false;
  if (prepared.includes("<<")) {
    const benign = stripBenignHeredocs(prepared);
    if (benign !== null) {
      prepared = benign;
      scanBase = benign;
      hadBenignHeredoc = true;
    } else {
      const stripped = stripLiteralHeredocs(prepared);
      if (stripped !== null) prepared = stripped;
    }
  }
  if (!scanBase.includes("<<") || hadBenignHeredoc) {
    scanBase = maskRemoteSSHAndSCP(scanBase);
    prepared = maskRemoteSSHAndSCP(prepared);
  }
  const forms = [maskCommandDataArguments(scanBase)];
  const unpacked = extractProcessSubstitutions(scanBase);
  if (unpacked !== scanBase) forms.push(maskCommandDataArguments(unpacked));
  if (findSubshellSpans(prepared)) prepared = eraseSubshells(prepared);
  const expanded = concretizeLiterals(prepared);
  if (expanded.status === "applied" || expanded.status === "secret") {
    forms.push(maskCommandDataArguments(expanded.text));
    const again = extractProcessSubstitutions(expanded.text);
    if (again !== expanded.text) forms.push(maskCommandDataArguments(again));
  }
  return forms;
}

// Folds `\` + newline into a space, as the shell does.
export function joinLineContinuations(s) {
  return s.replaceAll("\\\r\n", " ").replaceAll("\\\n", " ");
}

// Removes `<<[-]DELIM` markers from one line. Quote state carries over lines;
// a `$(` (even inside double quotes) opens a fresh context, as in
// `git commit -m "$(cat <<'EOF'`.
function takeHeredocMarkers(line, state) {
  let out = "";
  const markers = [];
  for (let i = 0; i < line.length; i++) {
    let c = line[i];
    if (state.quote === "'") {
      if (c === "'") state.quote = "";
    } else if (c === "$" && line[i + 1] === "(") {
      state.outer.push(state.quote);
      state.quote = "";
      out += c;
      i++;
      c = line[i];
    } else if (state.quote === '"') {
      if (c === "\\" && i + 1 < line.length) {
        out += c;
        i++;
        c = line[i];
      } else if (c === '"') {
        state.quote = "";
      }
    } else if (c === "\\" && i + 1 < line.length) {
      out += c;
      i++;
      c = line[i];
    } else if (c === "(" && state.outer.length > 0) {
      state.outer.push("");
    } else if (c === ")" && state.outer.length > 0) {
      state.quote = state.outer.pop();
    } else if (c === "'" || c === '"') {
      state.quote = c;
    } else if (c === "<" && line[i + 1] === "<") {
      if (line[i + 2] === "<") return null; // here-string
      let j = i + 2;
      const m = { word: "", dash: false, quoted: false };
      if (line[j] === "-") {
        m.dash = true;
        j++;
      }
      while (j < line.length && (line[j] === " " || line[j] === "\t")) j++;
      if (line[j] === "'" || line[j] === '"') {
        const end = line.indexOf(line[j], j + 1);
        if (end < 0) return null;
        m.word = line.slice(j + 1, end);
        m.quoted = true;
        j = end + 1;
      } else {
        let k = j;
        while (k < line.length && /[A-Za-z0-9_-]/.test(line[k])) k++;
        m.word = line.slice(j, k);
        j = k;
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

// Removes heredoc bodies so an interpreter script fed through `<<'EOF'` is
// judged by its command line; returns null (stays opaque) for unterminated
// heredocs, here-strings, or an unquoted delimiter whose body still expands.
export function stripLiteralHeredocs(s) {
  const lines = s.split("\n");
  const out = [];
  const state = { quote: "", outer: [] };
  for (let i = 0; i < lines.length; i++) {
    const taken = takeHeredocMarkers(lines[i], state);
    if (!taken) return null;
    out.push(taken.line);
    for (const m of taken.markers) {
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
      if (!m.quoted && /[$`\\]/.test(body.join("\n"))) return null;
    }
  }
  return out.join("\n");
}

function stripBenignHeredocs(s) {
  const lines = s.split("\n");
  const out = [];
  const state = { quote: "", outer: [] };
  const tempTargets = [];
  for (let i = 0; i < lines.length; i++) {
    const taken = takeHeredocMarkers(lines[i], state);
    if (!taken) return null;
    out.push(taken.line);
    if (taken.markers.length > 0) {
      if (state.outer.length === 1) {
        if (!/\bgit\s+commit\b[\s\S]*-m\s*"?\$\(\s*cat\s*$/.test(taken.line)) return null;
      } else if (state.outer.length === 0) {
        const { segments } = splitCommandChainWithPipes(taken.line);
        if (segments.length === 0) return null;
        const target = tempCatRedirectTarget(segments[segments.length - 1]);
        if (!target) return null;
        tempTargets.push(target);
      } else {
        return null;
      }
    }
    for (const m of taken.markers) {
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
      if (!m.quoted && /[$`\\]/.test(body.join("\n"))) return null;
    }
  }
  const stripped = out.join("\n");
  if (tempTargets.length > 0) {
    const { segments: allSegs } = splitCommandChainWithPipes(stripped);
    for (const seg of allSegs) {
      if (tempCatRedirectTarget(seg)) continue;
      for (const t of tempTargets) {
        if (seg.includes(t) || seg.includes(baseName(t))) {
          const words = stripShellKw(shellWords(seg));
          let j = 0;
          while (j < words.length && ENV_ASSIGN_RE.test(words[j])) j++;
          if (j >= words.length || !BENIGN_TEMP_CONSUMERS.has(baseName(normalizeAbsoluteTool(words[j])))) {
            return null;
          }
        }
      }
    }
  }
  return stripped;
}

function hasBasicDollarExpansion(s) {
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    const next = s[i + 1] || "";
    if (quote === "'") {
      if (c === "'") quote = "";
    } else if (quote === '"') {
      if (c === "\\" && i + 1 < s.length) i++;
      else if (c === '"') quote = "";
      else if (c === "$" && next && next !== '"' && DOLLAR_EXPAND_RE.test(next)) return true;
    } else if (c === "'" || c === '"') {
      quote = c;
    } else if (c === "$" && next && DOLLAR_EXPAND_RE.test(next)) {
      return true;
    }
  }
  return false;
}

function tempCatRedirectTarget(seg) {
  if (hasBasicDollarExpansion(seg) || seg.includes("`")) return "";
  const rawWords = shellWords(seg);
  const expanded = [];
  for (const w of rawWords) {
    if ((w.startsWith(">>") || w.startsWith(">")) && w.length > 1 && !w.startsWith(">&")) {
      const op = w.startsWith(">>") ? ">>" : ">";
      expanded.push(op, w.slice(op.length));
    } else {
      expanded.push(w);
    }
  }
  const words = [];
  for (let i = 0; i < expanded.length; i++) {
    const w = expanded[i];
    if (DEV_NULL_WORD_RE.test(w) || DUP_STREAM_WORD_RE.test(w)) continue;
    if (REDIRECT_WORD_RE.test(w) && expanded[i + 1] === "/dev/null") {
      i++;
      continue;
    }
    words.push(w);
  }
  if (words.length !== 3 || baseName(normalizeAbsoluteTool(words[0])) !== "cat" || (words[1] !== ">" && words[1] !== ">>")) {
    return "";
  }
  const target = words[2];
  return target !== "/dev/null" && isSafeCurlOutputTarget(target, isSecretPath) ? target : "";
}

export function maskRemoteSSHSegments(s) {
  return mapTopLevelSegments(s, (seg) => (isSafeRemoteSSHSegment(seg) ? "ssh " + MASKED_DATA_ARGUMENT : seg));
}

function maskRemoteSSHAndSCP(s) {
  return mapTopLevelSegments(s, (seg) => {
    if (isSafeRemoteSSHSegment(seg)) return "ssh " + MASKED_DATA_ARGUMENT;
    const rewritten = rewriteSafeRemoteSCPSegment(seg);
    return rewritten || seg;
  });
}

function mapTopLevelSegments(s, fn) {
  let out = "";
  let quote = "";
  let parenDepth = 0;
  let segStart = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    const next = s[i + 1] || "";
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
    if ((c === "&" && next === "&") || (c === "|" && next === "|") || (c === "|" && next === "&")) {
      sepLen = 2;
    } else if (c === ";" || c === "\n" || c === "|") {
      sepLen = 1;
    } else if (c === "&" && (i === 0 || s[i - 1] !== ">") && next !== ">") {
      sepLen = 1;
    }
    if (sepLen > 0) {
      out += fn(s.slice(segStart, i)) + s.slice(i, i + sepLen);
      i += sepLen - 1;
      segStart = i + 1;
    }
  }
  if (quote !== "" || parenDepth !== 0) return s;
  return out + fn(s.slice(segStart));
}

function stripShellKw(words) {
  let i = 0;
  while (i < words.length && SHELL_KEYWORDS.has(words[i])) i++;
  return words.slice(i);
}

function unwrapCmdPrefixes(words) {
  while (words.length > 0) {
    const base = baseName(normalizeAbsoluteTool(words[0]));
    if (base === "nohup" || base === "time" || base === "exec") {
      if (words.length === 1) return null;
      words = words.slice(1);
    } else if (base === "command") {
      if (words.length <= 1 || words[1] === "-v" || words[1] === "-V") return null;
      words = words.slice(1);
    } else if (base === "env") {
      let k = 1;
      while (k < words.length && ENV_ASSIGN_RE.test(words[k])) k++;
      if (k === 1 || k >= words.length) return null;
      words = words.slice(k);
    } else {
      return words;
    }
  }
  return null;
}

function hasUnsafeLocalSSHExpansion(seg) {
  let quote = "";
  for (let i = 0; i < seg.length; i++) {
    const c = seg[i];
    if (quote === "'") {
      if (c === "'") quote = "";
      continue;
    }
    if (quote === '"') {
      if (c === "\\" && i + 1 < seg.length) {
        i++;
        continue;
      }
      if (c === '"') {
        quote = "";
        continue;
      }
      if (c === "`") return true;
      if (c === "$" && !seg.slice(i).startsWith("$HOME/") && !seg.slice(i).startsWith("${HOME}/")) return true;
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      continue;
    }
    if (c === "\\" || c === "`") return true;
    if (c === "$" && !seg.slice(i).startsWith("$HOME/") && !seg.slice(i).startsWith("${HOME}/")) return true;
  }
  return quote !== "";
}

function isSSHIdentityKeyArg(raw) {
  let p = String(raw || "").trim();
  if (!p || p.includes("..") || /[*?[`;&|<>()]/.test(p)) return false;
  for (const pfx of ["~/", "$HOME/", "${HOME}/"]) {
    if (p.startsWith(pfx) && p.length > pfx.length) {
      p = "/Users/u/" + p.slice(pfx.length);
      break;
    }
  }
  if (p.includes("$")) return false;
  const slash = p.replaceAll("\\", "/").toLowerCase();
  if (!slash.includes("/.ssh/")) return false;
  const base = slash.slice(slash.lastIndexOf("/") + 1);
  return Boolean(base) && !SSH_BLOCKED_KEY_BASENAMES.has(base);
}

function isNonLoopbackSSHHost(target) {
  if (!target || target.startsWith("-")) return false;
  const m = SSH_HOST_SPEC_RE.exec(target);
  if (!m) return false;
  const host = m[1].toLowerCase();
  return host !== "localhost" && host !== "0.0.0.0" && host !== "::1" && !host.startsWith("127.");
}

function unwrapSSHCommandWords(seg) {
  if (hasUnsafeLocalSSHExpansion(seg)) return null;
  const words = stripNullRedirects(stripShellKw(shellWords(seg)));
  if (!words || words.length === 0) return null;
  let j = 0;
  while (j < words.length && ENV_ASSIGN_RE.test(words[j])) {
    if (words[j].includes("$")) return null;
    j++;
  }
  if (j >= words.length) return null;
  let unwrapped = unwrapCmdPrefixes(words.slice(j));
  if (!unwrapped || unwrapped.length === 0) return null;
  if (baseName(normalizeAbsoluteTool(unwrapped[0])) === "timeout") {
    if (unwrapped.length < 3 || !SSH_DURATION_RE.test(unwrapped[1])) return null;
    unwrapped = unwrapped.slice(2);
  }
  return unwrapped;
}

function isSafeRemoteSSHSegment(seg) {
  const words = unwrapSSHCommandWords(seg);
  if (!words || baseName(normalizeAbsoluteTool(words[0])) !== "ssh") return false;
  const args = words.slice(1);
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "-i") {
      if (i + 1 >= args.length || !isSSHIdentityKeyArg(args[i + 1])) return false;
      i++;
    } else if (a.startsWith("-i") && a.length > 2) {
      if (!isSSHIdentityKeyArg(a.slice(2))) return false;
    } else if (a === "-o") {
      if (i + 1 >= args.length || SSH_UNSAFE_OPT_RE.test(args[i + 1])) return false;
      i++;
    } else if (a.startsWith("-o") && a.length > 2) {
      if (SSH_UNSAFE_OPT_RE.test(a.slice(2))) return false;
    } else if (a === "-p" || a === "-l" || a === "-J") {
      if (i + 1 >= args.length) return false;
      i++;
    } else if (a.startsWith("-p") && a.length > 2) {
      continue;
    } else if (SSH_SAFE_SHORT_FLAG_RE.test(a)) {
      continue;
    } else if (a.startsWith("-")) {
      return false;
    } else {
      return i + 1 < args.length && isNonLoopbackSSHHost(a);
    }
  }
  return false;
}

function rewriteSafeRemoteSCPSegment(seg) {
  const words = unwrapSSHCommandWords(seg);
  if (!words || baseName(normalizeAbsoluteTool(words[0])) !== "scp") return "";
  const args = words.slice(1);
  const out = ["scp"];
  const pos = [];
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === "-i") {
      if (i + 1 >= args.length || !isSSHIdentityKeyArg(args[i + 1])) return "";
      out.push("-i", MASKED_DATA_ARGUMENT);
      i++;
    } else if (a.startsWith("-i") && a.length > 2) {
      if (!isSSHIdentityKeyArg(a.slice(2))) return "";
      out.push("-i", MASKED_DATA_ARGUMENT);
    } else if (a === "-o") {
      if (i + 1 >= args.length || SSH_UNSAFE_OPT_RE.test(args[i + 1])) return "";
      out.push(a, args[i + 1]);
      i++;
    } else if (a === "-P" || a === "-l") {
      if (i + 1 >= args.length) return "";
      out.push(a, args[i + 1]);
      i++;
    } else if (SCP_SAFE_SHORT_FLAG_RE.test(a)) {
      out.push(a);
    } else if (a.startsWith("-")) {
      return "";
    } else {
      pos.push(a);
      out.push(a);
    }
  }
  if (pos.length < 2) return "";
  let hasRemote = false;
  for (const p of pos) {
    const colon = p.indexOf(":");
    if (colon > 0 && !p.startsWith("/") && !p.startsWith(".") && isNonLoopbackSSHHost(p.slice(0, colon))) {
      hasRemote = true;
      continue;
    }
    const check = normalizeHomePrefixPath(p) || p;
    if (check.includes("$") || isSecretPath(check) || isSensitiveExternalPath(check) || isProtectedPath(check)) {
      return "";
    }
  }
  return hasRemote ? out.join(" ") : "";
}
