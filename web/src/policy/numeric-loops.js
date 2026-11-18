// Numeric shell sequences become bounded judgment-only lists, never subprocesses.
// Mirrors internal/runner/numeric_loops.go; quoted code/data is left untouched.
const HEADER = /^(for[ \t]+[A-Za-z_][A-Za-z0-9_]*[ \t]+in[ \t]+)(\{-?\d+\.\.-?\d+(?:\.\.-?\d+)?\}|\$\(seq[ \t]+-?\d+(?:[ \t]+-?\d+){0,2}[ \t]*\))([ \t]*(?:;|\r?\n))/;
const BRACE = /^\{(-?\d+)\.\.(-?\d+)(?:\.\.(-?\d+))?\}$/;
const SEQ = /^\$\(seq[ \t]+(-?\d+)(?:[ \t]+(-?\d+))?(?:[ \t]+(-?\d+))?[ \t]*\)$/;
const INTEGER = /^-?(?:0|[1-9]\d{0,8})$/;
const boundary = (c) => " \t\r\n;&|()<>".includes(c);

function numericLoopValues(expr) {
  const brace = BRACE.exec(expr), parts = brace || SEQ.exec(expr);
  if (!parts) return null;
  const raw = parts.slice(1).filter((v) => v !== undefined && v !== "");
  if (raw.some((v) => !INTEGER.test(v) || v === "-0")) return null;
  const nums = raw.map(Number);
  let start = 1, step = 1, end = nums[0];
  if (brace) {
    [start, end] = nums;
    if (nums.length === 3) step = nums[2];
    if (step <= 0) return null;
    if (start > end) step = -step;
  } else if (nums.length === 2) [start, end] = nums;
  else if (nums.length === 3) [start, step, end] = nums;
  if (!step || (start < end && step < 0) || (start > end && step > 0)) return null;
  const count = Math.trunc((end - start) / step) + 1;
  return count > 64 ? ["__hoxi_seq__"] : Array.from({ length: count }, (_, i) => String(start + i * step));
}

export function normalizeNumericLoops(s, findLoopDone) {
  if (s.includes("<<")) return s;
  let out = "", quote = "", prev = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote === "'") { if (c === "'") quote = ""; continue; }
    if (c === "\\" && i + 1 < s.length) { i++; continue; }
    if (c === '"') { quote = quote === '"' ? "" : '"'; continue; }
    if (quote) continue;
    if (c === "'") { quote = c; continue; }
    if (c === "#" && (i === 0 || boundary(s[i - 1]))) {
      while (i < s.length && s[i] !== "\n") i++;
      continue;
    }
    if (!s.startsWith("for", i) || (i > 0 && !boundary(s[i - 1]))) continue;
    const m = HEADER.exec(s.slice(i));
    if (!m || !findLoopDone(s, i + m[0].length)) continue;
    if (!/^[ \t\r\n]*do(?:[\s;&|()<>]|$)/.test(s.slice(i + m[0].length))) continue;
    const values = numericLoopValues(m[2]);
    if (!values) continue;
    out += s.slice(prev, i) + m[1] + values.join(" ") + m[3];
    i += m[0].length - 1;
    prev = i + 1;
  }
  return out + s.slice(prev);
}
