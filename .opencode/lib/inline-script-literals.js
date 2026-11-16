// Lexical scanner and string-escape decoder for auto mode's read-only inline
// script analyzer. Mirrors internal/runner/inline_script_literals.go.

const MAX_LEX_DEPTH = 4;
export const MAX_PATH_DEPTH = 6;
export const MAX_CANDIDATES = 64;

const JS_EXPR_KEYWORDS = new Set(["return", "typeof", "void", "delete", "throw", "case", "in", "of",
  "instanceof", "new", "yield", "await", "else", "do"]);
const JS_CONTROL_KEYWORDS = new Set(["if", "while", "for", "switch", "catch"]);
export const ASSIGN_OPS = new Set(["=", "+=", "-=", "*=", "/=", "%=", "|=", "&=", "^=", "<<=", ">>=", ">>>=", "**=", "??=", "||=", "&&=", ":="]);
export const READ_MODES = new Set(["r", "rt", "rb", "tr", "br", "U", "rU"]);
export const OPEN_KW_SAFE = new Set(["encoding", "errors", "newline", "buffering"]);
export const FILE_CALLS = new Set(["open", "openSync", "Path", "PurePath", "PosixPath", "WindowsPath", "joinpath",
  "readFileSync", "readFile", "realpathSync", "realpath", "statSync", "lstatSync", "readdirSync",
  "accessSync", "existsSync", "createReadStream", "scandir", "listdir", "lstat", "readlink"]);
export const ZERO_ARG_PATH_CALLS = new Set(["Path", "PurePath", "PosixPath", "WindowsPath", "listdir", "scandir"]);

function isIdentStart(c, allowDollar) {
  return (c >= "a" && c <= "z") || (c >= "A" && c <= "Z") || c === "_" || (allowDollar && c === "$");
}

function isIdentPart(c, allowDollar) {
  return isIdentStart(c, allowDollar) || (c >= "0" && c <= "9");
}

export function lexInlineScript(family, code) {
  const isJS = family === "node";
  const res = lexInlineSpan(isJS, code, 0, false, 0);
  if (!res || res.end !== code.length) return null;
  appendFoldedStringLiterals(res.toks, res.lits);
  return { toks: res.toks, mask: res.mask, lits: res.lits };
}

function lexInlineSpan(isJS, code, start, stopAtBrace, depth) {
  if (depth > MAX_LEX_DEPTH) return null;
  const toks = [];
  let mask = "";
  const lits = [];
  let prevSig = "";
  let prevIdent = "";
  const parenCtrl = [];
  let braceDepth = 0;
  let i = start;

  while (i < code.length) {
    const c = code[i];
    if (c === " " || c === "\t" || c === "\r" || c === "\n") {
      mask += c;
      i++;
      continue;
    }
    if (!isJS && c === "#") {
      while (i < code.length && code[i] !== "\n") {
        mask += " ";
        i++;
      }
      continue;
    }
    if (isJS && c === "/" && code[i + 1] === "/") {
      while (i < code.length && code[i] !== "\n") {
        mask += " ";
        i++;
      }
      continue;
    }
    if (isJS && c === "/" && code[i + 1] === "*") {
      const end = code.indexOf("*/", i + 2);
      if (end < 0) return null;
      for (let k = i; k < end + 2; k++) mask += code[k] === "\n" ? "\n" : " ";
      i = end + 2;
      continue;
    }
    if (stopAtBrace && c === "}" && braceDepth === 0) {
      return { toks, mask, lits, end: i };
    }
    if (!isJS && "rRbBuUfF".includes(c) && (i === start || !isIdentPart(code[i - 1], false))) {
      const pLen = i + 1 < code.length && "rRbBuUfF".includes(code[i + 1]) ? 2 : 1;
      const qChar = code[i + pLen];
      if (qChar === "'" || qChar === '"') {
        const pfx = code.slice(i, i + pLen).toLowerCase();
        const isRaw = pfx.includes("r");
        const isF = pfx.includes("f");
        if (isF && pfx.includes("b")) return null;
        if (isF) {
          const fRes = scanPyFString(code, i, i + pLen, isRaw, depth);
          if (!fRes) return null;
          toks.push(fRes.tok);
          mask += fRes.mask;
          lits.push(...fRes.lits);
          prevSig = "str";
          prevIdent = "";
          i = fRes.nextI;
          continue;
        }
        const sRes = scanQuotedString(false, code, i + pLen, isRaw);
        if (!sRes) return null;
        toks.push({ kind: "str", val: sRes.val, start: i, end: sRes.nextI });
        mask += '"_"';
        lits.push(sRes.val);
        prevSig = "str";
        prevIdent = "";
        i = sRes.nextI;
        continue;
      }
    }
    if (c === "'" || c === '"') {
      const sRes = scanQuotedString(isJS, code, i, false);
      if (!sRes) return null;
      toks.push({ kind: "str", val: sRes.val, start: i, end: sRes.nextI });
      mask += '"_"';
      lits.push(sRes.val);
      prevSig = "str";
      prevIdent = "";
      i = sRes.nextI;
      continue;
    }
    if (isJS && c === "`") {
      if (prevSig === "id" || prevSig === "close_val" || prevSig === "str" || prevSig === "re") return null;
      const tRes = scanJSTemplate(code, i, depth);
      if (!tRes) return null;
      toks.push(tRes.tok);
      mask += tRes.mask;
      lits.push(...tRes.lits);
      prevSig = "str";
      prevIdent = "";
      i = tRes.nextI;
      continue;
    }
    if (isJS && c === "/") {
      const isDiv = prevSig === "id" || prevSig === "num" || prevSig === "str" || prevSig === "re" || prevSig === "close_val" || prevSig === "inc";
      if (!isDiv) {
        const nextI = scanJSRegex(code, i);
        if (nextI < 0) return null;
        toks.push({ kind: "re", val: "/_/", start: i, end: nextI });
        mask += "/_/";
        prevSig = "re";
        prevIdent = "";
        i = nextI;
        continue;
      }
    }
    if (isIdentStart(c, isJS)) {
      let j = i + 1;
      while (j < code.length && isIdentPart(code[j], isJS)) j++;
      const word = code.slice(i, j);
      const isProp = toks.length > 0 && toks[toks.length - 1].kind === "op" && toks[toks.length - 1].val === ".";
      toks.push({ kind: "id", val: word, start: i, end: j });
      mask += word;
      prevSig = isJS && !isProp && JS_EXPR_KEYWORDS.has(word) ? "kw_expr" : "id";
      prevIdent = isProp ? "" : word;
      i = j;
      continue;
    }
    if (c >= "0" && c <= "9") {
      let j = i + 1;
      while (j < code.length && (isIdentPart(code[j], false) || code[j] === ".")) j++;
      const num = code.slice(i, j);
      toks.push({ kind: "num", val: num, start: i, end: j });
      mask += num;
      prevSig = "num";
      prevIdent = "";
      i = j;
      continue;
    }
    const [op, nextI] = scanInlineOperator(code, i);
    toks.push({ kind: "op", val: op, start: i, end: nextI });
    mask += op;
    if (op === "(") {
      parenCtrl.push(isJS && JS_CONTROL_KEYWORDS.has(prevIdent));
      prevSig = "punct";
    } else if (op === ")") {
      const isCtrl = parenCtrl.length > 0 ? parenCtrl.pop() : false;
      prevSig = isCtrl ? "close_ctrl" : "close_val";
    } else if (op === "]") {
      prevSig = "close_val";
    } else if (op === "{") {
      braceDepth++;
      prevSig = "punct";
    } else if (op === "}") {
      if (braceDepth > 0) braceDepth--;
      prevSig = "punct";
    } else if (op === "++" || op === "--") {
      prevSig = "inc";
    } else {
      prevSig = "punct";
    }
    prevIdent = "";
    i = nextI;
  }
  if (stopAtBrace) return null;
  return { toks, mask, lits, end: i };
}

function scanInlineOperator(code, i) {
  if (code.startsWith(">>>=", i)) return [">>>=", i + 4];
  for (const three of ["===", "!==", ">>>", "<<=", ">>=", "**=", "??=", "||=", "&&=", "..."]) {
    if (code.startsWith(three, i)) return [three, i + 3];
  }
  for (const two of ["==", "!=", "<=", ">=", "=>", "++", "--", "+=", "-=", "*=", "/=", "%=", "|=", "&=", "^=", ":=", "&&", "||", "??", "**", "<<", ">>", "?."]) {
    if (code.startsWith(two, i)) return [two, i + 2];
  }
  return [code[i], i + 1];
}

function scanQuotedString(isJS, code, quotePos, isRaw) {
  let q = code[quotePos];
  const triple = !isJS && code.startsWith(q + q + q, quotePos);
  if (triple) q = q + q + q;
  let j = quotePos + q.length;
  while (j < code.length) {
    if (!triple && (code[j] === "\n" || code[j] === "\r")) return null;
    if (code[j] === "\\") {
      if (j + 1 >= code.length) return null;
      j += 2;
      continue;
    }
    if (code.startsWith(q, j)) {
      const decoded = decodeInlineEscapes(isJS, code.slice(quotePos + q.length, j), isRaw);
      if (decoded === null) return null;
      return { val: decoded, nextI: j + q.length };
    }
    j++;
  }
  return null;
}

function scanJSTemplate(code, tickPos, depth) {
  const parts = [];
  const exprs = [];
  const maskExprs = [];
  const lits = [];
  let chunkStart = tickPos + 1;
  let j = chunkStart;
  while (j < code.length) {
    if (code[j] === "\\") {
      if (j + 1 >= code.length) return null;
      j += 2;
      continue;
    }
    if (code[j] === "`") {
      const part = decodeInlineEscapes(true, code.slice(chunkStart, j), false);
      if (part === null) return null;
      parts.push(part);
      if (part !== "" || exprs.length === 0) lits.push(part);
      return {
        tok: { kind: "tpl", start: tickPos, end: j + 1, parts, exprs },
        mask: `tpl(${maskExprs.join(", ")})`,
        lits,
        nextI: j + 1,
      };
    }
    if (code[j] === "$" && code[j + 1] === "{") {
      const part = decodeInlineEscapes(true, code.slice(chunkStart, j), false);
      if (part === null) return null;
      parts.push(part);
      if (part !== "") lits.push(part);
      const sub = lexInlineSpan(true, code, j + 2, true, depth + 1);
      if (!sub) return null;
      exprs.push(sub.toks);
      maskExprs.push(sub.mask);
      lits.push(...sub.lits);
      j = sub.end + 1;
      chunkStart = j;
      continue;
    }
    j++;
  }
  return null;
}

function scanPyFString(code, startPos, quotePos, isRaw, depth) {
  let q = code[quotePos];
  const triple = code.startsWith(q + q + q, quotePos);
  if (triple) q = q + q + q;
  const parts = [];
  const exprs = [];
  const maskExprs = [];
  const lits = [];
  let curRaw = "";
  let j = quotePos + q.length;

  const flushPart = () => {
    const p = decodeInlineEscapes(false, curRaw, isRaw);
    curRaw = "";
    if (p === null) return false;
    parts.push(p);
    if (p !== "") lits.push(p);
    return true;
  };

  while (j < code.length) {
    if (!triple && (code[j] === "\n" || code[j] === "\r")) return null;
    if (code.startsWith(q, j)) {
      if (!flushPart()) return null;
      return {
        tok: { kind: "tpl", start: startPos, end: j + q.length, parts, exprs },
        mask: `tpl(${maskExprs.join(", ")})`,
        lits,
        nextI: j + q.length,
      };
    }
    if (code[j] === "\\") {
      if (j + 1 >= code.length) return null;
      curRaw += code.slice(j, j + 2);
      j += 2;
      continue;
    }
    if (code[j] === "{") {
      if (code[j + 1] === "{") {
        curRaw += "{";
        j += 2;
        continue;
      }
      if (!flushPart()) return null;
      const field = extractPyFStringField(code, j + 1);
      if (!field) return null;
      const sub = lexInlineSpan(false, field.expr, 0, false, depth + 1);
      if (!sub || sub.end !== field.expr.length) return null;
      exprs.push(sub.toks);
      maskExprs.push(sub.mask);
      lits.push(...sub.lits);
      j = field.closeIdx + 1;
      continue;
    }
    if (code[j] === "}") {
      if (code[j + 1] === "}") {
        curRaw += "}";
        j += 2;
        continue;
      }
      return null;
    }
    curRaw += code[j];
    j++;
  }
  return null;
}

function extractPyFStringField(code, start) {
  let depth = 0;
  let exprEnd = -1;
  let inFmt = false;
  for (let j = start; j < code.length; j++) {
    const c = code[j];
    if (c === "'" || c === '"') {
      const sRes = scanQuotedString(false, code, j, false);
      if (!sRes) return null;
      j = sRes.nextI - 1;
      continue;
    }
    if (inFmt) {
      if (c === "{") return null;
      if (c === "}") return { expr: code.slice(start, exprEnd).trim(), closeIdx: j };
      continue;
    }
    if (c === "(" || c === "[" || c === "{") depth++;
    else if (c === ")" || c === "]") {
      if (depth <= 0) return null;
      depth--;
    } else if (c === "}") {
      if (depth === 0) {
        if (exprEnd < 0) exprEnd = j;
        const expr = code.slice(start, exprEnd).trim();
        return expr ? { expr, closeIdx: j } : null;
      }
      depth--;
    } else if (c === "!" && depth === 0 && code[j + 1] !== "=" && exprEnd < 0) {
      exprEnd = j;
    } else if (c === ":" && depth === 0) {
      if (exprEnd < 0) exprEnd = j;
      inFmt = true;
    }
  }
  return null;
}

function scanJSRegex(code, slashPos) {
  let inClass = false;
  for (let j = slashPos + 1; j < code.length; j++) {
    const c = code[j];
    if (c === "\n" || c === "\r") return -1;
    if (c === "\\") {
      if (j + 1 >= code.length || code[j + 1] === "\n" || code[j + 1] === "\r") return -1;
      j++;
      continue;
    }
    if (c === "[") inClass = true;
    else if (c === "]") inClass = false;
    else if (c === "/" && !inClass) {
      let k = j + 1;
      while (k < code.length && "dgimsuvy".includes(code[k])) k++;
      if (k < code.length && isIdentPart(code[k], true)) return -1;
      return k;
    }
  }
  return -1;
}

export function decodeInlineEscapes(isJS, raw, isRaw) {
  if (isRaw || !raw.includes("\\")) return raw;
  let out = "";
  for (let i = 0; i < raw.length; i++) {
    if (raw[i] !== "\\") {
      out += raw[i];
      continue;
    }
    if (i + 1 >= raw.length) return null;
    i++;
    const c = raw[i];
    if (c === "n") out += "\n";
    else if (c === "r") out += "\r";
    else if (c === "t") out += "\t";
    else if (c === "b") out += "\b";
    else if (c === "f") out += "\f";
    else if (c === "v") out += "\v";
    else if (c === "\\" || c === "'" || c === '"' || c === "`" || c === "/") out += c;
    else if (c === "\n") { /* escaped newline */ }
    else if (c === "\r") { if (raw[i + 1] === "\n") i++; }
    else if (c === "x") {
      const hex = raw.slice(i + 1, i + 3);
      if (!/^[0-9a-fA-F]{2}$/.test(hex)) return null;
      out += String.fromCodePoint(parseInt(hex, 16));
      i += 2;
    } else if (c === "u") {
      if (isJS && raw[i + 1] === "{") {
        const end = raw.indexOf("}", i + 2);
        const hex = end > 0 ? raw.slice(i + 2, end) : "";
        if (!/^[0-9a-fA-F]{1,6}$/.test(hex)) return null;
        const cp = parseInt(hex, 16);
        if (cp > 0x10ffff) return null;
        out += String.fromCodePoint(cp);
        i = end;
      } else {
        const hex = raw.slice(i + 1, i + 5);
        if (!/^[0-9a-fA-F]{4}$/.test(hex)) return null;
        out += String.fromCodePoint(parseInt(hex, 16));
        i += 4;
      }
    } else if (c === "U" && !isJS) {
      const hex = raw.slice(i + 1, i + 9);
      if (!/^[0-9a-fA-F]{8}$/.test(hex)) return null;
      const cp = parseInt(hex, 16);
      if (cp > 0x10ffff) return null;
      out += String.fromCodePoint(cp);
      i += 8;
    } else if (c === "N" && !isJS && raw[i + 1] === "{") {
      return null;
    } else if (c >= "0" && c <= "7") {
      let octEnd = i + 1;
      while (octEnd < raw.length && octEnd < i + 3 && raw[octEnd] >= "0" && raw[octEnd] <= "7") octEnd++;
      out += String.fromCodePoint(parseInt(raw.slice(i, octEnd), 8));
      i = octEnd - 1;
    } else {
      out += isJS ? c : "\\" + c;
    }
  }
  return out;
}

function appendFoldedStringLiterals(toks, lits) {
  for (let i = 0; i < toks.length; i++) {
    if (toks[i].kind === "tpl") {
      for (const sub of toks[i].exprs) appendFoldedStringLiterals(sub, lits);
      continue;
    }
    if (toks[i].kind !== "str") continue;
    let combined = toks[i].val;
    let j = i;
    let count = 1;
    while (j + 2 < toks.length && toks[j + 1].kind === "op" && toks[j + 1].val === "+" && toks[j + 2].kind === "str") {
      combined += toks[j + 2].val;
      j += 2;
      count++;
    }
    if (count > 1) {
      lits.push(combined);
      i = j;
    }
  }
}

export function cleanInlinePosixPath(raw) {
  const s = String(raw || "").trim().replaceAll("\\", "/");
  if (!s) return "";
  const abs = s.startsWith("/");
  const parts = [];
  for (const seg of s.split("/")) {
    if (!seg || seg === ".") continue;
    if (seg === "..") {
      if (parts.length > 0 && parts[parts.length - 1] !== "..") parts.pop();
      else if (!abs) parts.push("..");
    } else {
      parts.push(seg);
    }
  }
  const joined = parts.join("/");
  return abs ? "/" + joined : joined || ".";
}

export function joinInlinePosixPath(a, b) {
  if (!a) return b;
  if (!b) return a;
  const bs = b.replaceAll("\\", "/");
  if (bs.startsWith("/")) return bs;
  return a.replaceAll("\\", "/").replace(/\/+$/, "") + "/" + bs;
}
