// Literal `for` expansion. A list of literals (no `$`, glob, command
// substitution, secret or protected path) is unrolled up to 64 items and each
// copy is judged as its own command. Over the cap the loop is left untouched.
// Mirrors expandLiteralForLoops in internal/runner/opaque_expand.go.
const MAX_LITERAL_LOOP_ITEMS = 64;
const SHELL_VAR_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
const isBoundary = (c) => " \t\r\n;&|()<>".includes(c);

function wordAt(s, i, word) {
  if (s.slice(i, i + word.length) !== word) return false;
  if (i > 0 && !isBoundary(s[i - 1])) return false;
  return i + word.length === s.length || isBoundary(s[i + word.length]);
}

export function forLoopRanges(s) {
  const ranges = [];
  scanForLoops(s, () => {
    return null;
  }, (start, end) => {
    ranges.push([start, end]);
  });
  return ranges;
}

export function containsFor(s) {
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
    if (c === '"') {
      quote = quote === '"' ? "" : '"';
      continue;
    }
    if (c === "'") {
      quote = "'";
      continue;
    }
    if (wordAt(s, i, "for")) return true;
  }
  return false;
}

// deps.substituteVars, deps.forLoopVar and deps.itemBlocked come from
// shell-constants so this file does not import it.
export function expandLiteralForLoops(s, bound, deps) {
  let changed = false;
  const out = scanForLoops(s, (start, end, header, body, items) => {
    if (items.length === 0 || items.length > MAX_LITERAL_LOOP_ITEMS || !literalForItems(items, deps.itemBlocked)) return null;
    const name = deps.forLoopVar(header);
    if (!name || bound.has(name)) return null;
    const copies = [];
    for (const item of items) {
      const one = deps.substituteVars(body, new Map([[name, { valueSub: false, text: item, secret: false, inner: "" }]]));
      if (one === null) return null;
      copies.push(one.trim());
    }
    changed = true;
    return copies.join("\n");
  });
  return { text: out, changed };
}

function literalForItems(items, itemBlocked) {
  return items.every((item) => {
    if (/[$*?[`{]/.test(item) || item.includes("${")) return false;
    if (itemBlocked(item)) return false;
    return true;
  });
}

function scanForLoops(s, visit, onSpan) {
  for (;;) {
    let quote = "";
    let rewrote = false;
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
      if (c === '"') {
        quote = quote === '"' ? "" : quote === "" ? '"' : quote;
        continue;
      }
      if (c === "'" && !quote) {
        quote = "'";
        continue;
      }
      if (!wordAt(s, i, "for")) continue;
      const header = parseForHeader(s, i);
      if (!header) continue;
      const done = findLoopDone(s, header.bodyStart);
      if (!done) continue;
      if (onSpan) onSpan(i, done.doneEnd);
      const repl = visit(i, done.doneEnd, s.slice(i, done.doneEnd), s.slice(header.bodyStart, done.bodyEnd), header.items);
      if (repl !== null) {
        s = s.slice(0, i) + repl + s.slice(done.doneEnd);
        rewrote = true;
        break;
      }
      i = done.doneEnd - 1;
    }
    if (!rewrote) return s;
  }
}

function skipSpace(s, i) {
  while (i < s.length && " \t\n\r".includes(s[i])) i++;
  return i;
}

function readShellWord(s, i) {
  i = skipSpace(s, i);
  if (i >= s.length) return null;
  if (s[i] === "'" || s[i] === '"') {
    const q = s[i];
    const j = s.indexOf(q, i + 1);
    if (j < 0) return null;
    return { word: s.slice(i + 1, j), next: j + 1 };
  }
  let j = i;
  while (j < s.length && !isBoundary(s[j])) j++;
  if (j === i) return null;
  return { word: s.slice(i, j), next: j };
}

function parseForHeader(s, i) {
  i += "for".length;
  const name = readShellWord(s, i);
  if (!name || !SHELL_VAR_NAME_RE.test(name.word)) return null;
  const inn = readShellWord(s, name.next);
  if (!inn || inn.word !== "in") return null;
  i = inn.next;
  const items = [];
  for (;;) {
    i = skipSpace(s, i);
    if (i >= s.length) return null;
    if (s[i] === ";") {
      i++;
      continue;
    }
    const w = readShellWord(s, i);
    if (!w) return null;
    if (w.word === "do") return { items, bodyStart: w.next };
    items.push(w.word);
    i = w.next;
  }
}

function findLoopDone(s, i) {
  let depth = 0;
  let quote = "";
  for (let j = i; j < s.length; j++) {
    const c = s[j];
    if (quote === "'") {
      if (c === "'") quote = "";
      continue;
    }
    if (c === "\\" && j + 1 < s.length) {
      j++;
      continue;
    }
    if (c === '"') {
      quote = quote === '"' ? "" : quote === "" ? '"' : quote;
      continue;
    }
    if (c === "'" && !quote) {
      quote = "'";
      continue;
    }
    if (wordAt(s, j, "for")) {
      depth++;
      j += "for".length - 1;
      continue;
    }
    if (wordAt(s, j, "done")) {
      if (depth === 0) return { bodyEnd: j, doneEnd: j + "done".length };
      depth--;
      j += "done".length - 1;
    }
  }
  return null;
}
