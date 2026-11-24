// web/src/components/timeline/ansi-output.ts
// Lightweight ANSI SGR color parser + clickable workspace file:line linkifier
// for Shell ToolCard output. Pure TypeScript, zero external dependencies.
import { repairWindowsMojibake } from "./terminal-output-text";

// Standard ANSI 16-color palette tuned for dark terminal surface (#0d1117).
const ANSI_STANDARD_COLORS: readonly string[] = [
  "#484f58", // 0 black
  "#ff7b72", // 1 red
  "#3fb950", // 2 green
  "#d29922", // 3 yellow
  "#58a6ff", // 4 blue
  "#bc8cff", // 5 magenta
  "#39c5cf", // 6 cyan
  "#d1d7e0", // 7 white
];

const ANSI_BRIGHT_COLORS: readonly string[] = [
  "#6e7681", // 8 bright black
  "#ffa198", // 9 bright red
  "#56d364", // 10 bright green
  "#e3b341", // 11 bright yellow
  "#79c0ff", // 12 bright blue
  "#d2a8ff", // 13 bright magenta
  "#56d4dd", // 14 bright cyan
  "#f0f6fc", // 15 bright white
];

export interface AnsiStyle {
  fg?: string;
  bg?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
}

export interface AnsiRun {
  text: string;
  style?: AnsiStyle;
  fileRef?: {
    path: string;
    line: number;
  };
}

// Strip OSC (window titles/shell integration) and non-SGR CSI sequences while keeping SGR (`...m`).
const ANSI_OSC_RE =
  // eslint-disable-next-line no-control-regex
  /(?:\u001b\][\s\S]*?(?:\u0007|\u001b\\)|\u009d[\s\S]*?(?:\u0007|\u009c))/g;
const NON_SGR_CSI_RE =
  // eslint-disable-next-line no-control-regex
  /(?:\u001b\[|\u009b)[0-9:;<=>?]*[ -/]*[@A-Z[\\\]^_`a-ln-z{|}~]/g;
const SGR_TOKEN_RE =
  // eslint-disable-next-line no-control-regex
  /\u001b\[([0-9;]*)m/g;

// Matches common compiler/test/stack-trace file:line references (Go, TS/JS, Rust, Python, C/C++).
const FILE_LINE_REF_RE =
  /(?:^|[\s([{"'`,])((?:[A-Za-z]:[\\/]|\/|\.\.?\/)?(?:[\w.@+-]+[\\/])*[\w.+-]+\.(?:go|ts|tsx|js|jsx|mjs|cjs|py|rs|java|kt|swift|rb|php|c|cc|cpp|h|hpp|cs|vue|svelte|css|scss|html|json|yaml|yml|toml|sql|sh|md)):(\d{1,6})(?::\d{1,6})?(?=$|[\s)\]}"'`,;:])/g;

function ansi256ToHex(n: number): string | undefined {
  if (!Number.isInteger(n) || n < 0 || n > 255) return undefined;
  if (n < 8) return ANSI_STANDARD_COLORS[n];
  if (n < 16) return ANSI_BRIGHT_COLORS[n - 8];
  if (n < 232) {
    const idx = n - 16;
    const r = Math.floor(idx / 36);
    const g = Math.floor((idx % 36) / 6);
    const b = idx % 6;
    const toByte = (v: number) => (v === 0 ? 0 : 55 + v * 40);
    return `rgb(${toByte(r)}, ${toByte(g)}, ${toByte(b)})`;
  }
  const gray = 8 + (n - 232) * 10;
  return `rgb(${gray}, ${gray}, ${gray})`;
}

function applySgrCodes(current: AnsiStyle, rawCodes: string): AnsiStyle {
  const codes = rawCodes === "" ? [0] : rawCodes.split(";").map((c) => Number(c || "0"));
  const next: AnsiStyle = { ...current };
  for (let i = 0; i < codes.length; i++) {
    const code = codes[i];
    if (code === 0) {
      delete next.fg;
      delete next.bg;
      delete next.bold;
      delete next.dim;
      delete next.italic;
      delete next.underline;
    } else if (code === 1) {
      next.bold = true;
    } else if (code === 2) {
      next.dim = true;
    } else if (code === 3) {
      next.italic = true;
    } else if (code === 4) {
      next.underline = true;
    } else if (code === 22) {
      delete next.bold;
      delete next.dim;
    } else if (code === 23) {
      delete next.italic;
    } else if (code === 24) {
      delete next.underline;
    } else if (code >= 30 && code <= 37) {
      next.fg = ANSI_STANDARD_COLORS[code - 30];
    } else if (code === 39) {
      delete next.fg;
    } else if (code >= 40 && code <= 47) {
      next.bg = ANSI_STANDARD_COLORS[code - 40];
    } else if (code === 49) {
      delete next.bg;
    } else if (code >= 90 && code <= 97) {
      next.fg = ANSI_BRIGHT_COLORS[code - 90];
    } else if (code >= 100 && code <= 107) {
      next.bg = ANSI_BRIGHT_COLORS[code - 100];
    } else if ((code === 38 || code === 48) && i + 1 < codes.length) {
      const mode = codes[i + 1];
      const isFg = code === 38;
      if (mode === 5 && i + 2 < codes.length) {
        const color = ansi256ToHex(codes[i + 2]);
        if (color) {
          if (isFg) next.fg = color;
          else next.bg = color;
        }
        i += 2;
      } else if (mode === 2 && i + 4 < codes.length) {
        const r = Math.min(255, Math.max(0, codes[i + 2]));
        const g = Math.min(255, Math.max(0, codes[i + 3]));
        const b = Math.min(255, Math.max(0, codes[i + 4]));
        const color = `rgb(${r}, ${g}, ${b})`;
        if (isFg) next.fg = color;
        else next.bg = color;
        i += 4;
      }
    }
  }
  return next;
}

function hasAnyStyle(style: AnsiStyle): boolean {
  return Boolean(style.fg || style.bg || style.bold || style.dim || style.italic || style.underline);
}

/**
 * Resolves carriage returns (`\r`) while keeping ANSI SGR color escape codes intact.
 */
export function processCarriageReturnsPreservingAnsi(input: string): string {
  const cleaned = (input || "")
    .replace(/^\uFEFF/, "")
    .replace(ANSI_OSC_RE, "")
    .replace(NON_SGR_CSI_RE, "")
    .replace(/\r\n/g, "\n")
    .split("\n")
    .map((line) => {
      const idx = line.lastIndexOf("\r");
      return idx === -1 ? line : line.slice(idx + 1);
    })
    .join("\n");
  return repairWindowsMojibake(cleaned);
}

function splitFileLinksInRun(text: string, style?: AnsiStyle): AnsiRun[] {
  if (!text || !text.includes(":")) {
    return text ? [{ text, ...(style ? { style } : {}) }] : [];
  }
  const out: AnsiRun[] = [];
  FILE_LINE_REF_RE.lastIndex = 0;
  let lastIdx = 0;
  let match: RegExpExecArray | null;
  while ((match = FILE_LINE_REF_RE.exec(text)) !== null) {
    const full = match[0];
    const filePath = match[1];
    const lineNum = Number(match[2]);
    // Skip URLs like `http://foo.js:80`
    const fileOffsetInFull = full.indexOf(filePath);
    const matchStart = match.index + fileOffsetInFull;
    const prefixBefore = text.slice(Math.max(0, matchStart - 8), matchStart);
    if (/(?:https?|file):\/\/$/i.test(prefixBefore)) {
      continue;
    }
    const tokenLen = full.length - fileOffsetInFull;
    if (matchStart > lastIdx) {
      out.push({ text: text.slice(lastIdx, matchStart), ...(style ? { style } : {}) });
    }
    out.push({
      text: text.slice(matchStart, matchStart + tokenLen),
      ...(style ? { style } : {}),
      fileRef: { path: filePath, line: lineNum },
    });
    lastIdx = matchStart + tokenLen;
  }
  if (lastIdx < text.length) {
    out.push({ text: text.slice(lastIdx), ...(style ? { style } : {}) });
  }
  return out;
}

/**
 * Parses terminal output into styled runs with optional clickable `path:line` links.
 * Applies subtle semantic tinting for monochrome PASS/FAIL/ERROR lines when `semanticColors` is enabled.
 */
export function parseShellOutputRuns(
  rawInput: string,
  options?: { semanticColors?: boolean }
): AnsiRun[] {
  const prepared = processCarriageReturnsPreservingAnsi(rawInput);
  if (!prepared) return [];

  const semanticColors = options?.semanticColors !== false;
  const hasAnsi = prepared.includes("\u001b[");
  if (!hasAnsi) {
    const lines = prepared.split("\n");
    const runs: AnsiRun[] = [];
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      let semanticStyle: AnsiStyle | undefined;
      if (semanticColors) {
        if (/^(?:--- FAIL:|FAIL\b|Error:|ERROR\b|panic:|✗\s)/.test(line.trim())) {
          semanticStyle = { fg: "#ff7b72" };
        } else if (/^(?:--- PASS:|PASS\b|ok\s+|✓\s)/.test(line.trim())) {
          semanticStyle = { fg: "#3fb950" };
        } else if (/^(?:WARN(?:ING)?\b|⚠️)/.test(line.trim())) {
          semanticStyle = { fg: "#d29922" };
        }
      }
      runs.push(...splitFileLinksInRun(line, semanticStyle));
      if (i < lines.length - 1) {
        runs.push({ text: "\n" });
      }
    }
    return runs;
  }

  const runs: AnsiRun[] = [];
  let style: AnsiStyle = {};
  let lastIdx = 0;
  SGR_TOKEN_RE.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = SGR_TOKEN_RE.exec(prepared)) !== null) {
    if (match.index > lastIdx) {
      const slice = prepared.slice(lastIdx, match.index);
      const activeStyle = hasAnyStyle(style) ? { ...style } : undefined;
      runs.push(...splitFileLinksInRun(slice, activeStyle));
    }
    style = applySgrCodes(style, match[1]);
    lastIdx = SGR_TOKEN_RE.lastIndex;
  }
  if (lastIdx < prepared.length) {
    const slice = prepared.slice(lastIdx);
    const activeStyle = hasAnyStyle(style) ? { ...style } : undefined;
    runs.push(...splitFileLinksInRun(slice, activeStyle));
  }
  return runs;
}
