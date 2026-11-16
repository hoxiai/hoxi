// Windows UTF-8 repair for the Hoxi sandbox plugin. Windows shells and some
// CLIs emit GBK, CP1252 or Latin-1 bytes that OpenCode would decode as broken
// UTF-8; these helpers recover the text and set UTF-8 defaults for child
// shells. Split out of sandbox_plugin.mjs, which imports it; it is not a
// plugin itself and is never registered with OpenCode.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { StringDecoder } from "node:string_decoder";
const utf8FatalDecoder = new TextDecoder("utf-8", { fatal: true });
const origBufferToString = Buffer.prototype.toString;
const origTextDecoderDecode = TextDecoder.prototype.decode;
const origStringDecoderWrite = StringDecoder?.prototype?.write;
const rawUTF8 = (buf) => origBufferToString.call(buf, "utf8");
let gb18030Decoder;
let gbReverseTable;
const CP936_PUA_OVERRIDES = [
  [0xe78d, 0xa6d9], [0xe78e, 0xa6da], [0xe78f, 0xa6db], [0xe790, 0xa6dc], [0xe791, 0xa6dd], [0xe792, 0xa6de],
  [0xe793, 0xa6df], [0xe794, 0xa6ec], [0xe795, 0xa6ed], [0xe796, 0xa6f3], [0xe81e, 0xfe59], [0xe826, 0xfe61],
  [0xe82b, 0xfe66], [0xe82c, 0xfe67], [0xe832, 0xfe6d], [0xe843, 0xfe7e], [0xe854, 0xfe90], [0xe864, 0xfea0],
];
const WIN1252_CHAR_TO_BYTE = new Map([
  [0x20ac, 0x80], [0x201a, 0x82], [0x0192, 0x83], [0x201e, 0x84], [0x2026, 0x85], [0x2020, 0x86], [0x2021, 0x87],
  [0x02c6, 0x88], [0x2030, 0x89], [0x0160, 0x8a], [0x2039, 0x8b], [0x0152, 0x8c], [0x017d, 0x8e], [0x2018, 0x91],
  [0x2019, 0x92], [0x201c, 0x93], [0x201d, 0x94], [0x2022, 0x95], [0x2013, 0x96], [0x2014, 0x97], [0x02dc, 0x98],
  [0x2122, 0x99], [0x0161, 0x9a], [0x203a, 0x9b], [0x0153, 0x9c], [0x017e, 0x9e], [0x0178, 0x9f],
]);

function getGB18030Decoder() {
  if (!gb18030Decoder) {
    try { gb18030Decoder = new TextDecoder("gb18030", { fatal: false }); }
    catch { gb18030Decoder = new TextDecoder("utf-8", { fatal: false }); }
  }
  return gb18030Decoder;
}

function getGB18030ReverseTable() {
  if (gbReverseTable) return gbReverseTable;
  const table = new Uint16Array(65536);
  const dec = getGB18030Decoder();
  const buf = new Uint8Array((0xfe - 0x81 + 1) * (0xfe - 0x40 + 1) * 3);
  let offset = 0;
  for (let b1 = 0x81; b1 <= 0xfe; b1++) {
    for (let b2 = 0x40; b2 <= 0xfe; b2++) {
      buf[offset++] = b1;
      buf[offset++] = b2 === 0x7f ? 0x20 : b2;
      buf[offset++] = 0x0a;
    }
  }
  const decoded = origTextDecoderDecode.call(dec, buf);
  let strIdx = 0;
  for (let b1 = 0x81; b1 <= 0xfe; b1++) {
    for (let b2 = 0x40; b2 <= 0xfe; b2++) {
      const nl = decoded.indexOf("\n", strIdx);
      if (nl < 0) break;
      if (b2 !== 0x7f && nl === strIdx + 1) {
        const cp = decoded.charCodeAt(strIdx);
        if (cp >= 0x80 && cp !== 0xfffd && table[cp] === 0) table[cp] = (b1 << 8) | b2;
      }
      strIdx = nl + 1;
    }
  }
  for (const [code, val] of CP936_PUA_OVERRIDES) if (table[code] === 0) table[code] = val;
  return (gbReverseTable = table);
}

function isValidUTF8Slice(buf) {
  try { origTextDecoderDecode.call(utf8FatalDecoder, buf); return true; } catch { return false; }
}

function isAllowedRecoveredChar(cp) {
  return (
    cp === 0x09 || (cp >= 0x20 && cp <= 0x7e) || cp === 0x00b7 || cp === 0x00d7 || cp === 0x00f7 ||
    (cp >= 0x2010 && cp <= 0x26ff) || (cp >= 0x3000 && cp <= 0x303f) ||
    (cp >= 0x4e00 && cp <= 0x9fa5) || (cp >= 0xff01 && cp <= 0xff5e)
  );
}

function advanceUTF8DecodeState(bytes, startIdx) {
  let idx = startIdx, text = "", cjkCount = 0;
  const len = bytes.length;
  while (idx < len) {
    const b0 = bytes[idx];
    let cp = 0;
    if (b0 < 0x80) {
      if (!isAllowedRecoveredChar(b0)) return { nextIdx: idx, text, cjkCount, invalid: true };
      cp = b0; idx += 1;
    } else if (b0 >= 0xc2 && b0 <= 0xdf) {
      if (idx + 1 >= len) break;
      const b1 = bytes[idx + 1];
      if ((b1 & 0xc0) !== 0x80) return { nextIdx: idx, text, cjkCount, invalid: true };
      cp = ((b0 & 0x1f) << 6) | (b1 & 0x3f);
      if (!isAllowedRecoveredChar(cp)) return { nextIdx: idx, text, cjkCount, invalid: true };
      idx += 2;
    } else if (b0 >= 0xe0 && b0 <= 0xef) {
      if (idx + 1 < len) {
        const b1 = bytes[idx + 1];
        if ((b1 & 0xc0) !== 0x80 || (b0 === 0xe0 && b1 < 0xa0) || (b0 === 0xed && b1 >= 0xa0)) {
          return { nextIdx: idx, text, cjkCount, invalid: true };
        }
      }
      if (idx + 2 >= len) break;
      const b1 = bytes[idx + 1], b2 = bytes[idx + 2];
      if ((b2 & 0xc0) !== 0x80) return { nextIdx: idx, text, cjkCount, invalid: true };
      cp = ((b0 & 0x0f) << 12) | ((b1 & 0x3f) << 6) | (b2 & 0x3f);
      if (!isAllowedRecoveredChar(cp)) return { nextIdx: idx, text, cjkCount, invalid: true };
      if ((cp >= 0x4e00 && cp <= 0x9fa5) || (cp >= 0x3000 && cp <= 0x303f) || (cp >= 0xff01 && cp <= 0xff5e)) cjkCount++;
      idx += 3;
    } else {
      return { nextIdx: idx, text, cjkCount, invalid: true };
    }
    text += String.fromCharCode(cp);
  }
  return { nextIdx: idx, text, cjkCount, invalid: false };
}

function tryRecoverLatin1OrCP1252Run(run) {
  if (run.length < 3) return null;
  const bytes = new Uint8Array(run.length);
  let hasLatin1 = false;
  for (let i = 0; i < run.length; i++) {
    const cp = run.charCodeAt(i);
    if (cp >= 0x80 && cp <= 0xff) {
      bytes[i] = cp;
      if (cp >= 0xa0) hasLatin1 = true;
    } else {
      const mapped = WIN1252_CHAR_TO_BYTE.get(cp);
      if (mapped === undefined) return null;
      bytes[i] = mapped;
    }
  }
  if (!hasLatin1) return null;
  const res = advanceUTF8DecodeState(bytes, 0);
  return !res.invalid && res.nextIdx === bytes.length && res.cjkCount > 0 ? res.text : null;
}

function recoverGBKMojibakeSubrun(subrun, map, trailingFault) {
  let out = "", consumedFault = false, p = 0;
  const len = subrun.length;
  while (p < len) {
    const cp0 = subrun.charCodeAt(p);
    if (cp0 === 0x20ac) { out += subrun[p++]; continue; }
    const pair0 = map[cp0], b10 = pair0 >> 8, b20 = pair0 & 0xff;
    if (b10 < 0xe2 || b10 > 0xef || b20 < 0x80 || b20 > 0xbf) { out += subrun[p++]; continue; }
    const byteList = [];
    let decodedIdx = 0, decodedText = "", decodedCJK = 0, hasHint = false;
    let bestQ = -1, bestText = "", bestConsumedFault = false, bytesSinceValid = 0;
    for (let q = p; q < len; q++) {
      const cp = subrun.charCodeAt(q);
      if (cp === 0x20ac) {
        byteList.push(0x80); hasHint = true; bytesSinceValid += 1;
      } else {
        const pair = map[cp];
        if (!pair) break;
        const b1 = pair >> 8, b2 = pair & 0xff;
        byteList.push(b1, b2); bytesSinceValid += 2;
        if ((cp >= 0xe000 && cp <= 0xf8ff) || b1 <= 0xa0 || b2 <= 0xa0 || b1 >= 0xd8) hasHint = true;
      }
      const step = advanceUTF8DecodeState(byteList, decodedIdx);
      if (step.invalid) break;
      decodedIdx = step.nextIdx;
      if (step.text) decodedText += step.text;
      decodedCJK += step.cjkCount;
      if (hasHint && decodedCJK > 0) {
        if (decodedIdx === byteList.length) {
          bestQ = q + 1; bestText = decodedText; bestConsumedFault = false; bytesSinceValid = 0;
        } else if (
          q + 1 === len && trailingFault && byteList.length - decodedIdx === 2 &&
          byteList[decodedIdx] >= 0xe3 && byteList[decodedIdx] <= 0xef &&
          byteList[decodedIdx + 1] >= 0x80 && byteList[decodedIdx + 1] <= 0xbf
        ) {
          bestQ = q + 1; bestText = decodedText; bestConsumedFault = true; bytesSinceValid = 0;
        }
      }
      if (bytesSinceValid >= 6) break;
    }
    if (bestQ > p) {
      out += bestText; p = bestQ;
      if (bestConsumedFault) consumedFault = true;
    } else {
      out += subrun[p++];
    }
  }
  return { text: out, consumedFault };
}

function repairWindowsMojibake(input) {
  if (typeof input !== "string" || input.length < 2) return input || "";
  let hasNonASCII = false;
  for (let i = 0; i < input.length; i++) if (input.charCodeAt(i) >= 0x80) { hasNonASCII = true; break; }
  if (!hasNonASCII) return input;
  const map = getGB18030ReverseTable(), n = input.length;
  let out = "", i = 0;
  while (i < n) {
    const cp = input.charCodeAt(i);
    if (cp < 0x80 || cp === 0xfffd) { out += input[i++]; continue; }
    let j = i + 1;
    while (j < n && input.charCodeAt(j) >= 0x80 && input.charCodeAt(j) !== 0xfffd) j++;
    const run = input.slice(i, j);
    const latin = tryRecoverLatin1OrCP1252Run(run);
    if (latin !== null) { out += latin; i = j; continue; }
    let k = 0;
    while (k < run.length) {
      const rcp = run.charCodeAt(k);
      if (rcp !== 0x20ac && map[rcp] === 0) { out += run[k++]; continue; }
      let m = k + 1;
      while (m < run.length && (run.charCodeAt(m) === 0x20ac || map[run.charCodeAt(m)] !== 0)) m++;
      const trailingFault = m === run.length && j < n && input.charCodeAt(j) === 0xfffd;
      const recovered = recoverGBKMojibakeSubrun(run.slice(k, m), map, trailingFault);
      out += recovered.text;
      if (recovered.consumedFault) j++;
      k = m;
    }
    i = j;
  }
  return out;
}

function countLeadingUTF8Continuation(buf) {
  let i = 0;
  while (i < buf.length && i < 3 && (buf[i] & 0xc0) === 0x80) i++;
  return i;
}

function trimTrailingIncompleteUTF8End(buf) {
  const n = buf.length;
  for (let i = 1; i <= 3 && i <= n; i++) {
    const b = buf[n - i];
    if ((b & 0x80) === 0) return n;
    if ((b & 0xc0) === 0x80) continue;
    const need = (b & 0xe0) === 0xc0 ? 2 : (b & 0xf0) === 0xe0 ? 3 : (b & 0xf8) === 0xf0 ? 4 : 0;
    return need > 0 && i < need ? n - i : n;
  }
  return n;
}

function decodeWindowsOutputBuffer(buf, utf8Str) {
  if (!buf || buf.length === 0) return typeof utf8Str === "string" ? utf8Str : "";
  const raw = Buffer.isBuffer(buf) ? buf : Buffer.from(buf.buffer || buf, buf.byteOffset || 0, buf.byteLength ?? buf.length);
  const str = typeof utf8Str === "string" ? utf8Str : rawUTF8(raw);
  if (raw.includes(0) || !str.includes("\uFFFD")) return str;

  const lead = countLeadingUTF8Continuation(raw);
  const end = trimTrailingIncompleteUTF8End(raw);
  if (lead >= end) return "";
  const interior = raw.subarray(lead, end);
  if (isValidUTF8Slice(interior)) {
    return lead === 0 && end === raw.length ? str : rawUTF8(interior);
  }

  const decoder = getGB18030Decoder();
  const out = [];
  let lineStart = 0;
  for (let i = 0; i <= interior.length; i++) {
    if (i < interior.length && interior[i] !== 0x0a) continue;
    const hasLF = i < interior.length && interior[i] === 0x0a;
    let lineEnd = i;
    const hasCR = lineEnd > lineStart && interior[lineEnd - 1] === 0x0d;
    if (hasCR) lineEnd--;
    const lineBytes = interior.subarray(lineStart, lineEnd);
    let text = "";
    if (lineBytes.length > 0) {
      if (isValidUTF8Slice(lineBytes)) {
        text = rawUTF8(lineBytes);
      } else {
        const decoded = origTextDecoderDecode.call(decoder, lineBytes);
        text = !decoded.includes("\uFFFD") ? decoded : rawUTF8(lineBytes);
      }
    }
    out.push(text + (hasCR ? "\r" : "") + (hasLF ? "\n" : ""));
    lineStart = i + 1;
  }
  return out.join("");
}

const UTF8_SHELL_ENV = { PYTHONUTF8: "1", PYTHONIOENCODING: "utf-8", LANG: "C.UTF-8", LC_CTYPE: "UTF-8", LESSCHARSET: "utf-8" };

function ensureWindowsUTF8Runtime(force = false) {
  if (!force && process.platform !== "win32") return;
  for (const [k, v] of Object.entries(UTF8_SHELL_ENV)) if (!process.env[k]) process.env[k] = v;
  if (!force && !process.env.BASH_ENV) {
    try {
      const scriptPath = path.join(os.tmpdir(), "hoxi-bash-utf8.sh");
      const script =
        '#!/bin/sh\nexport LANG="${LANG:-C.UTF-8}"\nexport LC_CTYPE="${LC_CTYPE:-UTF-8}"\nexport PYTHONUTF8="${PYTHONUTF8:-1}"\nexport PYTHONIOENCODING="${PYTHONIOENCODING:-utf-8}"\nexport LESSCHARSET="${LESSCHARSET:-utf-8}"\n';
      if (!fs.existsSync(scriptPath) || fs.readFileSync(scriptPath, "utf8") !== script) {
        fs.writeFileSync(scriptPath, script, { mode: 0o600 });
      }
      process.env.BASH_ENV = scriptPath.replaceAll("\\", "/");
    } catch { /* Ignore temp script write errors in restricted environments. */ }
  }
  const patchSym = Symbol.for("hoxi.win32.bufferToStringPatched");
  if (!Buffer.prototype[patchSym]) {
    Object.defineProperty(Buffer.prototype, patchSym, { value: true, configurable: true });
    Buffer.prototype.toString = function hoxiToString(encoding, start, end) {
      const str = origBufferToString.apply(this, arguments);
      if (str.includes("\uFFFD") && (encoding === undefined || /^utf-?8$/i.test(encoding))) {
        return decodeWindowsOutputBuffer(start !== undefined || end !== undefined ? this.subarray(start, end) : this, str);
      }
      return str;
    };
  }
  if (!TextDecoder.prototype[patchSym]) {
    Object.defineProperty(TextDecoder.prototype, patchSym, { value: true, configurable: true });
    TextDecoder.prototype.decode = function hoxiTextDecode(input, options) {
      const str = origTextDecoderDecode.apply(this, arguments);
      if (str.includes("\uFFFD") && !this.fatal && /^utf-?8$/i.test(this.encoding) && input && !options?.stream) {
        return decodeWindowsOutputBuffer(input, str);
      }
      return str;
    };
  }
  if (StringDecoder?.prototype && origStringDecoderWrite && !StringDecoder.prototype[patchSym]) {
    Object.defineProperty(StringDecoder.prototype, patchSym, { value: true, configurable: true });
    StringDecoder.prototype.write = function hoxiStringDecoderWrite(buf) {
      const str = origStringDecoderWrite.apply(this, arguments);
      if (str.includes("\uFFFD") && (!this.encoding || /^utf-?8$/i.test(this.encoding)) && buf) {
        return decodeWindowsOutputBuffer(buf, str);
      }
      return str;
    };
  }
  if (force) return;
  try {
    import("bun:ffi")
      .then(({ dlopen, FFIType }) => {
        const k32 = dlopen("kernel32.dll", {
          SetConsoleCP: { args: [FFIType.u32], returns: FFIType.i32 },
          SetConsoleOutputCP: { args: [FFIType.u32], returns: FFIType.i32 },
        });
        k32.symbols.SetConsoleCP(65001);
        k32.symbols.SetConsoleOutputCP(65001);
      })
      .catch(() => {});
  } catch { /* Ignore when not running under Bun. */ }
}

export { UTF8_SHELL_ENV, decodeWindowsOutputBuffer, ensureWindowsUTF8Runtime, repairWindowsMojibake };
