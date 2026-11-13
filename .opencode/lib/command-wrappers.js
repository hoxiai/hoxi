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
