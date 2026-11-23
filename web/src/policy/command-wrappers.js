// Parsed-word wrappers shared by opacity, data masking and Git output checks.
// Mirrors unwrapCommandPrefixes / timeoutPrefixLength in opaque_command.go.
const NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;
const ASSIGN = /^[A-Za-z_][A-Za-z0-9_]*=/;
const DURATION = /^(?:\d+(?:\.\d*)?|\.\d+)[smhd]?$/;
const SIGNAL = /^(?:[A-Za-z][A-Za-z0-9]*|\d+)$/;
const baseName = (s) => s.slice(s.lastIndexOf("/") + 1);

export function timeoutPrefixLength(words) {
  let i = 1;
  for (; i < words.length && words[i].startsWith("-"); i++) {
    const flag = words[i];
    if (flag === "--") { i++; break; }
    if (["--foreground", "--preserve-status", "--verbose", "-v"].includes(flag)) continue;
    let value, kind;
    if (["-s", "--signal", "-k", "--kill-after"].includes(flag)) {
      kind = flag === "-s" || flag === "--signal" ? "signal" : "duration";
      value = words[++i];
    } else {
      const m = /^(--signal=|--kill-after=|-s|-k)(.+)$/.exec(flag);
      if (!m) return null;
      kind = m[1] === "-s" || m[1] === "--signal=" ? "signal" : "duration";
      value = m[2];
    }
    if (!value || !(kind === "signal" ? SIGNAL : DURATION).test(value)) return null;
  }
  return i + 1 < words.length && DURATION.test(words[i]) ? i + 1 : null;
}

export function unwrapCommandPrefixes(words, normalizeTool = (s) => s) {
  while (words.length > 0) {
    const base = baseName(normalizeTool(words[0]));
    if (base === "timeout" || base === "gtimeout") {
      if (words.length === 2 && ["--help", "--version"].includes(words[1])) return { words, lookupOnly: true };
      const end = timeoutPrefixLength(words);
      if (end === null) return null;
      words = words.slice(end);
    } else if (base === "nohup" || base === "time" || base === "exec") {
      if (words.length === 1) return { words, lookupOnly: false };
      words = words.slice(1);
    } else if (base === "command") {
      if (words.length >= 2 && ["-v", "-V"].includes(words[1])) return { words, lookupOnly: true };
      if (words.length === 1) return { words, lookupOnly: false };
      words = words.slice(1);
    } else if (base === "env") {
      let k = 1;
      while (k < words.length) {
        const w = words[k];
        if (ASSIGN.test(w) || ["-i", "-", "--ignore-environment"].includes(w) ||
          (w.startsWith("--unset=") && NAME.test(w.slice(8)))) k++;
        else if (w === "-u" && k + 1 < words.length && NAME.test(words[k + 1])) k += 2;
        else break;
      }
      if (k === 1 || k >= words.length) return null;
      words = words.slice(k);
    } else return { words, lookupOnly: false };
  }
  return { words, lookupOnly: false };
}

const POSITIVE_INT_RE = /^[1-9]\d*$/;
const XARGS_REPL_RE = /^(?:\{\}|%|[A-Za-z_][A-Za-z0-9_]*)$/;
const XARGS_ATTACHED_FLAG_RE = /^-(?:[nLPs][1-9]\d*|I(?:\{\}|%|[A-Za-z_][A-Za-z0-9_]*)|-max-(?:args|lines|procs)=[1-9]\d*)$/;
const WC_COUNT_FLAG_RE = /^(?:-[lcmwL]+|--(?:lines|bytes|chars|words|max-line-length))$/;
const GOFMT_RO_FLAG_RE = /^-[ls]+$/;
const NULL_REDIR_WORD_RE = /^(?:\d*|&)>>?\/dev\/null$|^\d*>&[12]$/;
const REDIR_OP_RE = /^(?:\d*|&)>>?$/;

function stripXargsNullRedirects(words) {
  const out = [];
  for (let i = 0; i < words.length; i++) {
    const w = words[i];
    if (NULL_REDIR_WORD_RE.test(w)) continue;
    if (REDIR_OP_RE.test(w) && i + 1 < words.length && words[i + 1] === "/dev/null") {
      i++;
      continue;
    }
    if (/[<>]/.test(w)) return null;
    out.push(w);
  }
  return out;
}

// Mirrors isSafeXargsSegment in internal/runner/shellpolicy/opaque_expand.go.
export function isSafeXargsArgs(rawArgs, piped, isUnsafePath) {
  if (!piped) return false;
  const args = stripXargsNullRedirects(rawArgs);
  if (!args) return false;
  let i = 0;
  let replToken = "";
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
      replToken = args[i + 1];
      i += 2;
      continue;
    }
    if (XARGS_ATTACHED_FLAG_RE.test(a)) {
      if (a.startsWith("-I") && a.length > 2) replToken = a.slice(2);
      i++;
      continue;
    }
    if (a.startsWith("-")) return false;
    break;
  }
  const rest = args.slice(i);
  if (rest.length === 0) return true;
  if (rest.some((w) => /[$`]/.test(w))) return false;
  const targetBase = baseName(rest[0]);
  const targetArgs = rest.slice(1);
  if (targetBase === "basename" || targetBase === "dirname" || targetBase === "echo") {
    for (let j = 0; j < targetArgs.length; j++) {
      const ta = targetArgs[j];
      if (targetBase === "basename" && (ta === "-a" || ta.startsWith("-s="))) continue;
      if (targetBase === "basename" && ta === "-s" && j + 1 < targetArgs.length) { j++; continue; }
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
      if (isUnsafePath(ta)) return false;
    }
    return true;
  }
  if (targetBase === "gofmt") {
    let hasList = false;
    for (const ta of targetArgs) {
      if (replToken && ta === replToken) continue;
      if (ta.startsWith("-")) {
        if (!GOFMT_RO_FLAG_RE.test(ta)) return false;
        if (ta.includes("l")) hasList = true;
        continue;
      }
      if (isUnsafePath(ta)) return false;
    }
    return hasList;
  }
  return false;
}
