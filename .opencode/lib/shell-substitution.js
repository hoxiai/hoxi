// Mirrors extractProcessSubstitutions / destructiveScanForms in
// internal/runner/opaque_loops.go: `<(cmd)` becomes a placeholder argument and
// cmd is appended as its own line, so it is judged like any other command.
import { maskCommandDataArguments } from "./command-data-args.js";

const PROCESS_SUBSTITUTION_PLACEHOLDER = "/dev/fd/63";
const isBoundary = (c) => " \t\r\n;&|()<>".includes(c);

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
  const forms = [maskCommandDataArguments(cmd)];
  const unpacked = extractProcessSubstitutions(cmd);
  if (unpacked !== cmd) forms.push(maskCommandDataArguments(unpacked));
  return forms;
}
