// web/src/components/timeline/terminal-output-text.ts

// Strip ANSI CSI, OSC (e.g. Windows ConPTY/PowerShell window title & shell integration),
// and UTF-8 BOM for clean shell rolling output (ported from kilocode).
const ANSI_OSC_REGEX =
  // eslint-disable-next-line no-control-regex
  /(?:\u001b\][\s\S]*?(?:\u0007|\u001b\\)|\u009d[\s\S]*?(?:\u0007|\u009c))/g;
const ANSI_REGEX =
  // eslint-disable-next-line no-control-regex
  /[\u001b\u009b][[()#;?]*(?:[0-9]{1,4}(?:[;:][0-9]{0,4})*)?[0-9A-ORZcf-nqry=><~]/g;

let gb18030Decoder: TextDecoder | null | undefined;
let gbkEncodeMap: Uint16Array | null = null;

// Legacy CP936 PUA codepoints (U+E78D..U+E864) that GB18030-2005 remapped to
// non-PUA BMP characters; Windows MultiByteToWideChar(936) can emit either.
const CP936_PUA_OVERRIDES: ReadonlyArray<readonly [number, number]> = [
  [0xe78d, 0xa6d9], [0xe78e, 0xa6da], [0xe78f, 0xa6db], [0xe790, 0xa6dc],
  [0xe791, 0xa6dd], [0xe792, 0xa6de], [0xe793, 0xa6df], [0xe794, 0xa6ec],
  [0xe795, 0xa6ed], [0xe796, 0xa6f3], [0xe81e, 0xfe59], [0xe826, 0xfe61],
  [0xe82b, 0xfe66], [0xe82c, 0xfe67], [0xe832, 0xfe6d], [0xe843, 0xfe7e],
  [0xe854, 0xfe90], [0xe864, 0xfea0],
];

// Windows-1252 0x80..0x9F characters mapped back to their single-byte values.
const WIN1252_CHAR_TO_BYTE = new Map<number, number>([
  [0x20ac, 0x80], [0x201a, 0x82], [0x0192, 0x83], [0x201e, 0x84],
  [0x2026, 0x85], [0x2020, 0x86], [0x2021, 0x87], [0x02c6, 0x88],
  [0x2030, 0x89], [0x0160, 0x8a], [0x2039, 0x8b], [0x0152, 0x8c],
  [0x017d, 0x8e], [0x2018, 0x91], [0x2019, 0x92], [0x201c, 0x93],
  [0x201d, 0x94], [0x2022, 0x95], [0x2013, 0x96], [0x2014, 0x97],
  [0x02dc, 0x98], [0x2122, 0x99], [0x0161, 0x9a], [0x203a, 0x9b],
  [0x0153, 0x9c], [0x017e, 0x9e], [0x0178, 0x9f],
]);

function getGB18030Decoder(): TextDecoder | null {
  if (gb18030Decoder === undefined) {
    try {
      gb18030Decoder = new TextDecoder("gb18030", { fatal: false });
    } catch {
      gb18030Decoder = null;
    }
  }
  return gb18030Decoder;
}

function getGBKEncodeMap(): Uint16Array | null {
  if (gbkEncodeMap) return gbkEncodeMap;
  const decoder = getGB18030Decoder();
  if (!decoder) return null;
  const map = new Uint16Array(65536);
  const pairCount = (0xfe - 0x81 + 1) * (0xfe - 0x40 + 1);
  const buf = new Uint8Array(pairCount * 3);
  let offset = 0;
  for (let b1 = 0x81; b1 <= 0xfe; b1++) {
    for (let b2 = 0x40; b2 <= 0xfe; b2++) {
      buf[offset++] = b1;
      buf[offset++] = b2 === 0x7f ? 0x20 : b2;
      buf[offset++] = 0x0a;
    }
  }
  const decoded = decoder.decode(buf);
  let strIdx = 0;
  for (let b1 = 0x81; b1 <= 0xfe; b1++) {
    for (let b2 = 0x40; b2 <= 0xfe; b2++) {
      const nl = decoded.indexOf("\n", strIdx);
      if (nl < 0) break;
      if (b2 !== 0x7f && nl === strIdx + 1) {
        const cp = decoded.charCodeAt(strIdx);
        if (cp >= 0x80 && cp !== 0xfffd && map[cp] === 0) {
          map[cp] = (b1 << 8) | b2;
        }
      }
      strIdx = nl + 1;
    }
  }
  for (const [pua, pair] of CP936_PUA_OVERRIDES) {
    if (map[pua] === 0) map[pua] = pair;
  }
  gbkEncodeMap = map;
  return map;
}

function isAllowedRecoveredChar(cp: number): boolean {
  if (cp === 0x09 || (cp >= 0x20 && cp <= 0x7e)) return true;
  if (cp === 0x00b7 || cp === 0x00d7 || cp === 0x00f7) return true;
  if (cp >= 0x2010 && cp <= 0x26ff) return true;
  if (cp >= 0x3000 && cp <= 0x303f) return true;
  if (cp >= 0x4e00 && cp <= 0x9fa5) return true;
  if (cp >= 0xff01 && cp <= 0xff5e) return true;
  return false;
}

function isCJKOrFullwidthChar(cp: number): boolean {
  return (cp >= 0x4e00 && cp <= 0x9fa5) || (cp >= 0x3000 && cp <= 0x303f) || (cp >= 0xff01 && cp <= 0xff5e);
}

function advanceUTF8DecodeState(
  bytes: ReadonlyArray<number> | Uint8Array,
  startIdx: number
): { nextIdx: number; text: string; cjkCount: number; invalid: boolean } {
  let idx = startIdx;
  const len = bytes.length;
  let text = "";
  let cjkCount = 0;

  while (idx < len) {
    const b0 = bytes[idx];
    let cp = 0;
    if (b0 < 0x80) {
      if (!isAllowedRecoveredChar(b0)) {
        return { nextIdx: idx, text, cjkCount, invalid: true };
      }
      cp = b0;
      idx += 1;
    } else if (b0 >= 0xc2 && b0 <= 0xdf) {
      if (idx + 1 >= len) break;
      const b1 = bytes[idx + 1];
      if ((b1 & 0xc0) !== 0x80) {
        return { nextIdx: idx, text, cjkCount, invalid: true };
      }
      cp = ((b0 & 0x1f) << 6) | (b1 & 0x3f);
      if (!isAllowedRecoveredChar(cp)) {
        return { nextIdx: idx, text, cjkCount, invalid: true };
      }
      idx += 2;
    } else if (b0 >= 0xe0 && b0 <= 0xef) {
      if (idx + 1 < len) {
        const b1 = bytes[idx + 1];
        if ((b1 & 0xc0) !== 0x80 || (b0 === 0xe0 && b1 < 0xa0) || (b0 === 0xed && b1 >= 0xa0)) {
          return { nextIdx: idx, text, cjkCount, invalid: true };
        }
      }
      if (idx + 2 >= len) break;
      const b1 = bytes[idx + 1];
      const b2 = bytes[idx + 2];
      if ((b2 & 0xc0) !== 0x80) {
        return { nextIdx: idx, text, cjkCount, invalid: true };
      }
      cp = ((b0 & 0x0f) << 12) | ((b1 & 0x3f) << 6) | (b2 & 0x3f);
      if (!isAllowedRecoveredChar(cp)) {
        return { nextIdx: idx, text, cjkCount, invalid: true };
      }
      if (isCJKOrFullwidthChar(cp)) cjkCount++;
      idx += 3;
    } else {
      return { nextIdx: idx, text, cjkCount, invalid: true };
    }
    text += String.fromCharCode(cp);
  }
  return { nextIdx: idx, text, cjkCount, invalid: false };
}

function tryDecodeValidUTF8(bytes: Uint8Array): string | null {
  if (bytes.length < 3) return null;
  const res = advanceUTF8DecodeState(bytes, 0);
  if (res.invalid || res.nextIdx !== bytes.length || res.cjkCount === 0) return null;
  return res.text;
}

function tryRecoverLatin1OrCP1252Run(run: string): string | null {
  if (run.length < 3) return null;
  const bytes = new Uint8Array(run.length);
  let hasLatin1Supplement = false;
  for (let i = 0; i < run.length; i++) {
    const cp = run.charCodeAt(i);
    if (cp >= 0x80 && cp <= 0xff) {
      bytes[i] = cp;
      if (cp >= 0xa0) hasLatin1Supplement = true;
    } else {
      const mapped = WIN1252_CHAR_TO_BYTE.get(cp);
      if (mapped === undefined) return null;
      bytes[i] = mapped;
    }
  }
  if (!hasLatin1Supplement) return null;
  return tryDecodeValidUTF8(bytes);
}

function recoverGBKMojibakeSubrun(
  subrun: string,
  map: Uint16Array,
  trailingFault: boolean
): { text: string; consumedFault: boolean } {
  let out = "";
  let consumedFault = false;
  let p = 0;
  const len = subrun.length;

  while (p < len) {
    const cp0 = subrun.charCodeAt(p);
    if (cp0 === 0x20ac) {
      out += subrun[p++];
      continue;
    }
    const pair0 = map[cp0];
    const b10 = pair0 >> 8;
    const b20 = pair0 & 0xff;
    // A 3-byte UTF-8 CJK/symbol sequence must start with lead byte 0xE2..0xEF and continuation 0x80..0xBF.
    if (b10 < 0xe2 || b10 > 0xef || b20 < 0x80 || b20 > 0xbf) {
      out += subrun[p++];
      continue;
    }

    const byteList: number[] = [];
    let decodedIdx = 0;
    let decodedText = "";
    let decodedCJK = 0;
    let hasHint = false;
    let bestQ = -1;
    let bestText = "";
    let bestConsumedFault = false;
    let bytesSinceValid = 0;

    for (let q = p; q < len; q++) {
      const cp = subrun.charCodeAt(q);
      if (cp === 0x20ac) {
        byteList.push(0x80);
        hasHint = true;
        bytesSinceValid += 1;
      } else {
        const pair = map[cp];
        if (!pair) break;
        const b1 = pair >> 8;
        const b2 = pair & 0xff;
        byteList.push(b1, b2);
        bytesSinceValid += 2;
        if ((cp >= 0xe000 && cp <= 0xf8ff) || b1 <= 0xa0 || b2 <= 0xa0 || b1 >= 0xd8) {
          hasHint = true;
        }
      }

      const step = advanceUTF8DecodeState(byteList, decodedIdx);
      if (step.invalid) break;
      decodedIdx = step.nextIdx;
      if (step.text) decodedText += step.text;
      decodedCJK += step.cjkCount;

      if (hasHint && decodedCJK > 0) {
        if (decodedIdx === byteList.length) {
          bestQ = q + 1;
          bestText = decodedText;
          bestConsumedFault = false;
          bytesSinceValid = 0;
        } else if (
          q + 1 === len &&
          trailingFault &&
          byteList.length - decodedIdx === 2 &&
          byteList[decodedIdx] >= 0xe3 &&
          byteList[decodedIdx] <= 0xef &&
          byteList[decodedIdx + 1] >= 0x80 &&
          byteList[decodedIdx + 1] <= 0xbf
        ) {
          bestQ = q + 1;
          bestText = decodedText;
          bestConsumedFault = true;
          bytesSinceValid = 0;
        }
      }

      if (bytesSinceValid >= 6) break;
    }

    if (bestQ > p) {
      out += bestText;
      p = bestQ;
      if (bestConsumedFault) consumedFault = true;
    } else {
      out += subrun[p++];
    }
  }

  return { text: out, consumedFault };
}

const NON_ASCII_TEST_RE = /[^\x00-\x7f]/;

/**
 * Repairs UTF-8 text that was mistakenly decoded as Windows CP936/GBK
 * (e.g. "涓枃" -> "中文", "涓€" -> "一", "娴嬭瘯" -> "测试") or Latin-1/CP1252
 * ("Ã¤Â¸Â­Ã¦Â–Â‡" -> "中文"), while leaving valid Chinese, English, symbols and
 * code untouched.
 */
export function repairWindowsMojibake(input: string): string {
  if (!input || !NON_ASCII_TEST_RE.test(input)) return input;
  const map = getGBKEncodeMap();
  let out = "";
  let i = 0;
  const n = input.length;

  while (i < n) {
    const cp = input.charCodeAt(i);
    if (cp < 0x80 || cp === 0xfffd) {
      out += input[i++];
      continue;
    }
    let j = i + 1;
    while (j < n) {
      const c = input.charCodeAt(j);
      if (c < 0x80 || c === 0xfffd) break;
      j++;
    }
    const run = input.slice(i, j);
    const latin = tryRecoverLatin1OrCP1252Run(run);
    if (latin !== null) {
      out += latin;
      i = j;
      continue;
    }
    if (!map) {
      out += run;
      i = j;
      continue;
    }

    let k = 0;
    while (k < run.length) {
      const rcp = run.charCodeAt(k);
      if (rcp !== 0x20ac && map[rcp] === 0) {
        out += run[k++];
        continue;
      }
      let m = k + 1;
      while (m < run.length) {
        const mcp = run.charCodeAt(m);
        if (mcp !== 0x20ac && map[mcp] === 0) break;
        m++;
      }
      const subrun = run.slice(k, m);
      const atRunEnd = m === run.length;
      const nextChar = atRunEnd && j < n ? input[j] : "";
      const trailingFault = atRunEnd && nextChar === "\uFFFD";
      const recovered = recoverGBKMojibakeSubrun(subrun, map, trailingFault);
      out += recovered.text;
      if (recovered.consumedFault) {
        j++;
      }
      k = m;
    }
    i = j;
  }
  return out;
}

/**
 * Ported from @kilocode/kilo-ui/src/components/shell-rolling-results.tsx
 * Resolves carriage returns (\r) like a real terminal: within each line (\n),
 * only text after the last \r is kept so CLI progress bars overwrite in place.
 */
export function processCarriageReturns(input: string): string {
  const cleaned = (input || "")
    .replace(/^\uFEFF/, "")
    .replace(ANSI_OSC_REGEX, "")
    .replace(ANSI_REGEX, "")
    .replace(/\r\n/g, "\n")
    .split("\n")
    .map((line) => {
      const idx = line.lastIndexOf("\r");
      return idx === -1 ? line : line.slice(idx + 1);
    })
    .join("\n");
  return repairWindowsMojibake(cleaned);
}
