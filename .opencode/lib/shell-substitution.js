// Mirrors extractProcessSubstitutions / destructiveScanForms in
// internal/runner/opaque_loops.go and joinLineContinuations /
// stripLiteralHeredocs in internal/runner/opaque_command.go: `<(cmd)` becomes
// a placeholder argument and `$(cmd)` the $__hoxi_cmdsub__ variable, and cmd
// is appended as its own line, so it is judged like any other command.
import { maskCommandDataArguments } from "./command-data-args.js";
import { concretizeLiterals, eraseSubshells, findSubshellSpans } from "./shell-constants.js";

const PROCESS_SUBSTITUTION_PLACEHOLDER = "/dev/fd/63";
export const CMD_SUBST_VAR = "__hoxi_cmdsub__";
const HEREDOC_WORD_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
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
  const forms = [maskCommandDataArguments(cmd)];
  const unpacked = extractProcessSubstitutions(cmd);
  if (unpacked !== cmd) forms.push(maskCommandDataArguments(unpacked));
  let prepared = joinLineContinuations(String(cmd ?? "").trim());
  if (prepared.includes("<<")) {
    const stripped = stripLiteralHeredocs(prepared);
    if (stripped !== null) prepared = stripped;
  }
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
