// Primitive-only JS records: names are data, never executable capabilities.
// Mirrors inline_script_fields.go. Original tokens still validate paths/literals.
import { ASSIGN_OPS } from "./inline-script-literals.js";
import { countInlineWritesAndParams } from "./inline-script-paths.js";

const primitive = (t) => ["str", "num"].includes(t.kind) || (t.kind === "id" && ["true", "false", "null"].includes(t.val));
const templateUses = (t, name) => (t.exprs || []).some((expr) => expr.some((sub) =>
  (sub.kind === "id" && sub.val === name) || templateUses(sub, name)));
const printUse = (t, i) => i >= 4 && t[i + 1]?.val === ")" && t[i - 1].val === "(" && t[i - 3].val === "." &&
  ((t[i - 4].val === "console" && t[i - 2].val === "log") ||
    (t[i - 4].val === "JSON" && t[i - 2].val === "stringify") ||
    (t[i - 4].val === "Object" && ["keys", "values", "entries"].includes(t[i - 2].val)));

export function maskInlineDataFields(code, toks) {
  const writes = new Map(), params = new Map(), marks = new Set();
  countInlineWritesAndParams(true, toks, writes, params);
  for (let i = 0; i + 4 < toks.length; i++) {
    if (toks[i].kind !== "id" || !["const", "let"].includes(toks[i].val) || toks[i + 1].kind !== "id" ||
      toks[i + 2].val !== "=" || toks[i + 3].val !== "{") continue;
    const name = toks[i + 1].val;
    if (writes.get(name) !== 1 || params.has(name)) continue;
    let depth = 0, end = i + 3;
    for (; end < toks.length; end++) {
      if (toks[end].kind === "op" && toks[end].val === "{") depth++;
      if (toks[end].kind === "op" && toks[end].val === "}" && --depth === 0) break;
    }
    if (end >= toks.length) continue;
    const keys = new Set(), local = new Set();
    let valid = true;
    for (let k = i + 4; k < end;) {
      if (k + 2 >= end || !["id", "str"].includes(toks[k].kind) || toks[k + 1].val !== ":" ||
        !primitive(toks[k + 2]) || toks[k].val.startsWith("__") || ["constructor", "prototype"].includes(toks[k].val)) {
        valid = false; break;
      }
      keys.add(toks[k].val);
      if (toks[k].kind === "id") local.add(k);
      k += 3;
      if (k < end) {
        if (toks[k].val !== ",") { valid = false; break; }
        k++;
      }
    }
    for (let k = 0; k < toks.length; k++) {
      const t = toks[k];
      if (templateUses(t, name)) valid = false;
      if (t.kind !== "id" || t.val !== name || k === i + 1) continue;
      if (k <= end || [".", "delete", "++", "--"].includes(toks[k - 1]?.val)) { valid = false; break; }
      if (toks[k + 1]?.val === "." && keys.has(toks[k + 2]?.val)) {
        if (ASSIGN_OPS.has(toks[k + 3]?.val) || ["(", "[", ".", "++", "--", "?."].includes(toks[k + 3]?.val)) { valid = false; break; }
        local.add(k + 2);
      } else if (!printUse(toks, k)) { valid = false; break; }
    }
    if (valid) for (const k of local) marks.add(k);
  }
  let out = "", prev = 0;
  toks.forEach((t, i) => {
    if (marks.has(i)) { out += code.slice(prev, t.start) + "dataField"; prev = t.end; }
  });
  return out + code.slice(prev);
}
