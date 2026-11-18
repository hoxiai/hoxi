// Bounded static path-expression resolver and regex-exec rewriter for inline
// Python and Node scripts. Mirrors internal/runner/inline_script_paths.go.
import { ASSIGN_OPS, MAX_CANDIDATES, MAX_PATH_DEPTH, joinInlinePosixPath } from "./inline-script-literals.js";

function inc(map, key, delta = 1) {
  map.set(key, (map.get(key) || 0) + delta);
}

function markAllIdentWrites(toks, writes, delta) {
  for (const t of toks) {
    if (t.kind === "id") inc(writes, t.val, delta);
  }
}

export function matchingCloseDelim(toks, openIdx, open, close) {
  let depth = 0;
  for (let i = openIdx; i < toks.length; i++) {
    if (toks[i].kind === "op") {
      if (toks[i].val === open) depth++;
      else if (toks[i].val === close) {
        depth--;
        if (depth === 0) return i;
      }
    }
  }
  return toks.length;
}

export function matchingCloseParen(toks, openIdx) {
  return matchingCloseDelim(toks, openIdx, "(", ")");
}

function matchingCloseBracket(toks, openIdx) {
  const open = toks[openIdx].val;
  const close = open === "[" ? "]" : open === "{" ? "}" : ")";
  return matchingCloseDelim(toks, openIdx, open, close);
}

function isArrowParamParen(toks, openIdx) {
  if (openIdx >= toks.length || toks[openIdx].kind !== "op" || toks[openIdx].val !== "(") return false;
  const closeIdx = matchingCloseParen(toks, openIdx);
  return closeIdx + 1 < toks.length && toks[closeIdx + 1].kind === "op" && toks[closeIdx + 1].val === "=>";
}

export function countInlineWritesAndParams(isJS, toks, writes, params) {
  let parenDepth = 0;
  for (let i = 0; i < toks.length; i++) {
    const t = toks[i];
    if (t.kind === "tpl") {
      for (const sub of t.exprs) countInlineWritesAndParams(isJS, sub, writes, params);
      continue;
    }
    if (t.kind === "op") {
      if (t.val === "(") {
        if (isJS && isArrowParamParen(toks, i)) {
          const end = matchingCloseParen(toks, i);
          for (let k = i + 1; k < end; k++) {
            if (toks[k].kind === "id") inc(params, toks[k].val, 1);
            else if (toks[k].kind === "op" && toks[k].val !== ",") {
              markAllIdentWrites(toks.slice(i + 1, end), writes, 2);
              break;
            }
          }
          i = end;
          continue;
        }
        parenDepth++;
      } else if (t.val === ")" && parenDepth > 0) {
        parenDepth--;
      }
      continue;
    }
    if (t.kind !== "id") continue;
    if (isJS && i + 1 < toks.length && toks[i + 1].kind === "op" && toks[i + 1].val === "=>") {
      inc(params, t.val, 1);
      continue;
    }
    if (isJS && (t.val === "const" || t.val === "let" || t.val === "var") && i + 1 < toks.length) {
      if (toks[i + 1].kind === "op" && (toks[i + 1].val === "{" || toks[i + 1].val === "[")) {
        const end = matchingCloseBracket(toks, i + 1);
        markAllIdentWrites(toks.slice(i + 1, end), writes, 2);
        continue;
      }
      if ((t.val === "let" || t.val === "var") && toks[i + 1].kind === "id") {
        if (i + 2 >= toks.length || (toks[i + 2].kind === "op" && (toks[i + 2].val === ";" || toks[i + 2].val === ","))) {
          inc(writes, toks[i + 1].val, 1);
        }
      }
    }
    if (isJS && t.val === "for" && i + 1 < toks.length && toks[i + 1].kind === "op" && toks[i + 1].val === "(") {
      let k = i + 2;
      if (k < toks.length && toks[k].kind === "id" && (toks[k].val === "const" || toks[k].val === "let" || toks[k].val === "var")) k++;
      if (k + 1 < toks.length && toks[k].kind === "id" && toks[k + 1].kind === "id" && (toks[k + 1].val === "of" || toks[k + 1].val === "in")) {
        inc(writes, toks[k].val, 1);
      } else if (k < toks.length && toks[k].kind === "op" && (toks[k].val === "[" || toks[k].val === "{")) {
        const end = matchingCloseBracket(toks, k);
        markAllIdentWrites(toks.slice(k, end), writes, 2);
      }
      continue;
    }
    if (!isJS && t.val === "for") {
      let inIdx = -1;
      for (let k = i + 1; k < toks.length && k <= i + 8; k++) {
        if (toks[k].kind === "id" && toks[k].val === "in") {
          inIdx = k;
          break;
        }
      }
      if (inIdx > i + 1) {
        if (inIdx === i + 2 && toks[i + 1].kind === "id") inc(writes, toks[i + 1].val, 1);
        else markAllIdentWrites(toks.slice(i + 1, inIdx), writes, 2);
      }
      continue;
    }
    if ((t.val === "function" || t.val === "def" || t.val === "class" || (!isJS && t.val === "as")) && i + 1 < toks.length && toks[i + 1].kind === "id") {
      inc(writes, toks[i + 1].val, 2);
      continue;
    }
    if (isJS && t.val === "catch" && i + 2 < toks.length && toks[i + 1].kind === "op" && toks[i + 1].val === "(" && toks[i + 2].kind === "id") {
      inc(writes, toks[i + 2].val, 2);
      continue;
    }
    if ((i > 0 && toks[i - 1].kind === "op" && (toks[i - 1].val === "++" || toks[i - 1].val === "--")) ||
      (i + 1 < toks.length && toks[i + 1].kind === "op" && (toks[i + 1].val === "++" || toks[i + 1].val === "--"))) {
      inc(writes, t.val, 2);
      continue;
    }
    if (i + 1 < toks.length && toks[i + 1].kind === "op" && ASSIGN_OPS.has(toks[i + 1].val)) {
      if (i > 0 && toks[i - 1].kind === "op" && toks[i - 1].val === ".") continue;
      if (!isJS && parenDepth > 0 && toks[i + 1].val === "=" && i > 0 && toks[i - 1].kind === "op" && (toks[i - 1].val === "(" || toks[i - 1].val === ",")) {
        continue;
      }
      inc(writes, t.val, 1);
    }
  }
}

function parseStringListTokens(toks, openIdx, allowTuple) {
  if (openIdx >= toks.length || toks[openIdx].kind !== "op") return null;
  const open = toks[openIdx].val;
  if (open !== "[" && (!allowTuple || open !== "(")) return null;
  const close = open === "(" ? ")" : "]";
  const vals = [];
  let expectVal = true;
  for (let i = openIdx + 1; i < toks.length; i++) {
    if (toks[i].kind === "op" && toks[i].val === close) {
      if (vals.length === 0 || vals.length > MAX_CANDIDATES) return null;
      return { vals, endIdx: i };
    }
    if (expectVal) {
      if (toks[i].kind !== "str") return null;
      vals.push(toks[i].val);
      expectVal = false;
    } else {
      if (toks[i].kind !== "op" || toks[i].val !== ",") return null;
      expectVal = true;
    }
  }
  return null;
}

function findMatchingOpenBracket(toks, closeIdx) {
  let depth = 0;
  for (let i = closeIdx; i >= 0; i--) {
    if (toks[i].kind === "op") {
      if (toks[i].val === "]") depth++;
      else if (toks[i].val === "[") {
        depth--;
        if (depth === 0) return i;
      }
    }
  }
  return -1;
}

function extractSingleArrowParam(toks, idx) {
  if (idx + 1 < toks.length && toks[idx].kind === "id" && toks[idx + 1].kind === "op" && toks[idx + 1].val === "=>") {
    return toks[idx].val;
  }
  if (idx + 3 < toks.length && toks[idx].kind === "op" && toks[idx].val === "(" &&
    toks[idx + 1].kind === "id" && toks[idx + 2].kind === "op" && toks[idx + 2].val === ")" &&
    toks[idx + 3].kind === "op" && toks[idx + 3].val === "=>") {
    return toks[idx + 1].val;
  }
  return "";
}

function statementExprEnd(code, toks, start) {
  let depth = 0;
  for (let k = start; k < toks.length; k++) {
    if (k > start && depth === 0 && toks[k - 1].end <= code.length && toks[k].start <= code.length && toks[k - 1].end <= toks[k].start) {
      if (code.slice(toks[k - 1].end, toks[k].start).includes("\n")) {
        const prevOp = toks[k - 1].kind === "op" && (toks[k - 1].val === "+" || toks[k - 1].val === ".");
        const curOp = toks[k].kind === "op" && (toks[k].val === "+" || toks[k].val === ".");
        if (!prevOp && !curOp) return k;
      }
    }
    if (toks[k].kind === "op") {
      const v = toks[k].val;
      if (v === "(" || v === "[" || v === "{") depth++;
      else if (v === ")" || v === "]" || v === "}") {
        if (depth === 0) return k;
        depth--;
      } else if ((v === ";" || v === ",") && depth === 0) {
        return k;
      }
    }
  }
  return toks.length;
}

export function collectInlinePathVars(family, code, toks) {
  const isJS = family === "node";
  const writes = new Map();
  const params = new Map();
  countInlineWritesAndParams(isJS, toks, writes, params);

  const vars = new Map();
  const arrVars = new Map();
  const regexVars = new Set();
  const canBindVar = (name) => writes.get(name) === 1 && !params.has(name);
  const canBindParam = (name) => !writes.has(name) && params.get(name) === 1;

  for (let i = 0; i < toks.length; i++) {
    const t = toks[i];
    if (isJS && t.kind === "id" && t.val === "for" && i + 4 < toks.length && toks[i + 1].kind === "op" && toks[i + 1].val === "(") {
      let k = i + 2;
      if (toks[k].kind === "id" && (toks[k].val === "const" || toks[k].val === "let")) k++;
      if (k + 2 < toks.length && toks[k].kind === "id" && canBindVar(toks[k].val) && toks[k + 1].kind === "id" && toks[k + 1].val === "of") {
        const varName = toks[k].val;
        const listRes = parseStringListTokens(toks, k + 2, false);
        if (listRes && listRes.endIdx + 1 < toks.length && toks[listRes.endIdx + 1].val === ")") {
          vars.set(varName, listRes.vals);
        } else if (toks[k + 2].kind === "id" && k + 3 < toks.length && toks[k + 3].val === ")" && arrVars.has(toks[k + 2].val)) {
          vars.set(varName, arrVars.get(toks[k + 2].val));
        }
      }
      continue;
    }
    if (!isJS && t.kind === "id" && t.val === "for" && i + 3 < toks.length && toks[i + 1].kind === "id" && canBindVar(toks[i + 1].val) && toks[i + 2].kind === "id" && toks[i + 2].val === "in") {
      const varName = toks[i + 1].val;
      const listRes = parseStringListTokens(toks, i + 3, true);
      if (listRes && listRes.endIdx + 1 < toks.length && toks[listRes.endIdx + 1].val === ":") {
        vars.set(varName, listRes.vals);
      } else if (toks[i + 3].kind === "id" && i + 4 < toks.length && toks[i + 4].val === ":" && arrVars.has(toks[i + 3].val)) {
        vars.set(varName, arrVars.get(toks[i + 3].val));
      }
      continue;
    }
    if (isJS && t.kind === "op" && t.val === "." && i + 2 < toks.length && toks[i + 1].kind === "id" &&
      ["forEach", "map", "filter", "some", "every"].includes(toks[i + 1].val) && toks[i + 2].kind === "op" && toks[i + 2].val === "(") {
      let recvList = null;
      if (i > 0 && toks[i - 1].kind === "id") recvList = arrVars.get(toks[i - 1].val) || null;
      else if (i > 0 && toks[i - 1].kind === "op" && toks[i - 1].val === "]") {
        const openBr = findMatchingOpenBracket(toks, i - 1);
        if (openBr >= 0) {
          const listRes = parseStringListTokens(toks, openBr, false);
          if (listRes) recvList = listRes.vals;
        }
      }
      if (recvList && recvList.length > 0) {
        const pName = extractSingleArrowParam(toks, i + 3);
        if (pName && canBindParam(pName)) vars.set(pName, recvList);
      }
      continue;
    }
    let lhsName = "";
    let rhsStart = -1;
    if (isJS && t.kind === "id" && (t.val === "const" || t.val === "let") && i + 2 < toks.length &&
      toks[i + 1].kind === "id" && toks[i + 2].kind === "op" && toks[i + 2].val === "=") {
      lhsName = toks[i + 1].val;
      rhsStart = i + 3;
    } else if (!isJS && t.kind === "id" && i + 1 < toks.length && toks[i + 1].kind === "op" && toks[i + 1].val === "=" &&
      (i === 0 || !(toks[i - 1].kind === "op" && [".", "(", ","].includes(toks[i - 1].val)))) {
      lhsName = t.val;
      rhsStart = i + 2;
    }
    if (lhsName && canBindVar(lhsName) && rhsStart < toks.length) {
      const rhsEnd = statementExprEnd(code, toks, rhsStart);
      const rhs = toks.slice(rhsStart, rhsEnd);
      const listRes = parseStringListTokens(toks, rhsStart, !isJS);
      if (isJS && rhs.length === 1 && rhs[0].kind === "re") {
        regexVars.add(lhsName);
      } else if (listRes && listRes.endIdx + 1 === rhsEnd) {
        arrVars.set(lhsName, listRes.vals);
      } else {
        const resolved = resolveInlinePathTokens(rhs, vars, 0);
        if (resolved) vars.set(lhsName, resolved);
      }
    }
  }
  return { pathVars: vars, regexVars };
}

// Token-only normalization of single-write standard path module aliases.
export function normalizeInlinePathAliases(code, toks) {
  const writes = new Map(), params = new Map(), aliases = new Map();
  countInlineWritesAndParams(true, toks, writes, params);
  for (let i = 0; i < toks.length; i++) {
    if (toks[i].kind !== "id" || !["const", "let"].includes(toks[i].val)) continue;
    for (let j = i + 1; j + 2 < toks.length && toks[j].kind === "id" && toks[j + 1].val === "=";) {
      const end = statementExprEnd(code, toks, j + 2);
      const rhs = toks.slice(j + 2, end);
      if (writes.get(toks[j].val) === 1 && !params.has(toks[j].val) && rhs.length === 4 &&
        rhs[0].kind === "id" && rhs[0].val === "require" && rhs[1].val === "(" &&
        rhs[2].kind === "str" && ["path", "node:path"].includes(rhs[2].val) && rhs[3].val === ")") {
        aliases.set(toks[j].val, { decl: j, end });
      }
      if (end >= toks.length || toks[end].val !== ",") break;
      j = end + 1;
    }
  }
  const templateUses = (t, name) => (t.exprs || []).some((expr) => expr.some((sub) =>
    (sub.kind === "id" && sub.val === name) || templateUses(sub, name)));
  for (const [name, binding] of aliases) {
    if (toks.some((t, i) => templateUses(t, name) || (t.kind === "id" && t.val === name && i !== binding.decl &&
      (i <= binding.end || toks[i - 1]?.val === "." || toks[i + 1]?.val !== "." ||
        toks[i + 2]?.val !== "join" || toks[i + 3]?.val !== "(")))) aliases.delete(name);
  }
  return toks.map((t, i) => aliases.has(t.val) && i > aliases.get(t.val).end && t.kind === "id" &&
    toks[i + 1]?.val === "." && toks[i + 2]?.val === "join" && toks[i + 3]?.val === "(" ? { ...t, val: "path" } : t);
}

function splitTopLevelPlus(expr) {
  const parts = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < expr.length; i++) {
    const t = expr[i];
    if (t.kind === "op") {
      if (t.val === "(" || t.val === "[" || t.val === "{") depth++;
      else if (t.val === ")" || t.val === "]" || t.val === "}") depth--;
      else if (t.val === "+" && depth === 0) {
        if (i === start) return null;
        parts.push(expr.slice(start, i));
        start = i + 1;
      }
    }
  }
  if (parts.length === 0) return [];
  if (start >= expr.length) return null;
  parts.push(expr.slice(start));
  return parts;
}

export function cartesianConcat(left, right, asPathJoin) {
  if (left.length === 0 || right.length === 0 || left.length * right.length > MAX_CANDIDATES) return null;
  const out = [];
  for (const l of left) {
    for (const r of right) out.push(asPathJoin ? joinInlinePosixPath(l, r) : l + r);
  }
  return out;
}

export function splitCallArgs(inner) {
  if (inner.length === 0) return [];
  const args = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < inner.length; i++) {
    const t = inner[i];
    if (t.kind === "op") {
      if (t.val === "(" || t.val === "[" || t.val === "{") depth++;
      else if (t.val === ")" || t.val === "]" || t.val === "}") {
        if (depth <= 0) return null;
        depth--;
      } else if (t.val === "," && depth === 0) {
        if (i === start) return null;
        args.push(inner.slice(start, i));
        start = i + 1;
      }
    }
  }
  if (depth !== 0) return null;
  if (start < inner.length) args.push(inner.slice(start));
  for (const a of args) {
    if (a.length > 0 && a[0].kind === "op" && (a[0].val === "*" || a[0].val === "**" || a[0].val === "...")) return null;
  }
  return args;
}

function matchPathJoinCall(expr) {
  let callOpen = -1;
  if (expr.length >= 5 && expr[0].kind === "id" && (expr[0].val === "path" || expr[0].val === "posixpath") &&
    expr[1].val === "." && expr[2].kind === "id" && expr[2].val === "join" && expr[3].val === "(") {
    callOpen = 3;
  } else if (expr.length >= 7 && expr[0].kind === "id" && expr[0].val === "os" &&
    expr[1].val === "." && expr[2].kind === "id" && expr[2].val === "path" &&
    expr[3].val === "." && expr[4].kind === "id" && expr[4].val === "join" && expr[5].val === "(") {
    callOpen = 5;
  }
  if (callOpen < 0 || matchingCloseParen(expr, callOpen) !== expr.length - 1) return null;
  return splitCallArgs(expr.slice(callOpen + 1, expr.length - 1));
}

export function resolveInlinePathTokens(expr, vars, depth) {
  if (!expr || expr.length === 0 || depth > MAX_PATH_DEPTH) return null;
  if (expr[0].kind === "op" && expr[0].val === "(" && matchingCloseParen(expr, 0) === expr.length - 1) {
    return resolveInlinePathTokens(expr.slice(1, expr.length - 1), vars, depth + 1);
  }
  const plusParts = splitTopLevelPlus(expr);
  if (plusParts === null) return null;
  if (plusParts.length > 1) {
    let acc = [""];
    for (const part of plusParts) {
      const vals = resolveInlinePathTokens(part, vars, depth + 1);
      if (!vals) return null;
      acc = cartesianConcat(acc, vals, false);
      if (!acc) return null;
    }
    return acc;
  }
  if (expr.length === 1) {
    if (expr[0].kind === "str") return [expr[0].val];
    if (expr[0].kind === "id") {
      const vals = vars.get(expr[0].val);
      return vals && vals.length > 0 ? vals : null;
    }
    if (expr[0].kind === "tpl") {
      let acc = [expr[0].parts[0]];
      for (let idx = 0; idx < expr[0].exprs.length; idx++) {
        const vals = resolveInlinePathTokens(expr[0].exprs[idx], vars, depth + 1);
        if (!vals) return null;
        acc = cartesianConcat(acc, vals, false);
        if (!acc) return null;
        const suffix = expr[0].parts[idx + 1];
        if (suffix) acc = acc.map((s) => s + suffix);
      }
      return acc;
    }
    return null;
  }
  const joinArgs = matchPathJoinCall(expr);
  if (joinArgs && joinArgs.length > 0) {
    let acc = null;
    for (let idx = 0; idx < joinArgs.length; idx++) {
      const vals = resolveInlinePathTokens(joinArgs[idx], vars, depth + 1);
      if (!vals) return null;
      acc = idx === 0 ? [...vals] : cartesianConcat(acc, vals, true);
      if (!acc) return null;
    }
    return acc;
  }
  return null;
}

const LITERAL_REGEX_EXEC_RE = /\/_\/\s*\.\s*exec\s*\(/g;

export function rewriteRegexExecCalls(isJS, regexVars, codeMask) {
  if (!isJS) return codeMask;
  let out = codeMask.replace(LITERAL_REGEX_EXEC_RE, "/_/.match(");
  for (const name of regexVars) {
    const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    out = out.replace(new RegExp(`\\b${escaped}\\s*\\.\\s*exec\\s*\\(`, "g"), `${name}.match(`);
  }
  return out;
}
