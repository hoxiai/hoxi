// web/src/components/share/share-image-text.ts
// Theme palettes, Canvas 2D layout types, CJK/Latin rich text wrapping, and
// Shiki-aware code line wrapping for local session share PNG cards.
import type { ThemedToken } from "shiki";
import { parseInline } from "../timeline/markdown/inline";
import type { AlertKind, InlineSpan, TableAlign } from "../timeline/markdown/types";
import type { ShareToolPill } from "./share-image-model";

export interface ShareThemePalette {
  mode: "light" | "dark";
  outerBg: string;
  cardBg: string;
  cardBorder: string;
  cardShadow: string;
  divider: string;
  textPrimary: string;
  textSecondary: string;
  textMuted: string;
  textFaint: string;
  accent: string;
  accentBg: string;
  accentBorder: string;
  userBg: string;
  userBorder: string;
  codeBg: string;
  codeHeaderBg: string;
  codeBorder: string;
  codeText: string;
  inlineCodeBg: string;
  inlineCodeText: string;
  quoteBg: string;
  quoteBorder: string;
  thinkingBg: string;
  thinkingBorder: string;
  pillBg: string;
  pillBorder: string;
  tableHeaderBg: string;
  tableBorder: string;
  okDot: string;
  errDot: string;
  warnDot: string;
  linkText: string;
}

export const LIGHT_PALETTE: ShareThemePalette = {
  mode: "light",
  outerBg: "#eef2f6",
  cardBg: "#ffffff",
  cardBorder: "#e2e8f0",
  cardShadow: "rgba(15, 23, 42, 0.08)",
  divider: "#edf1f6",
  textPrimary: "#0f172a",
  textSecondary: "#334155",
  textMuted: "#64748b",
  textFaint: "#94a3b8",
  accent: "#2563eb",
  accentBg: "#eff6ff",
  accentBorder: "#bfdbfe",
  userBg: "#f5f8fc",
  userBorder: "#e2e9f3",
  codeBg: "#f8fafc",
  codeHeaderBg: "#f1f5f9",
  codeBorder: "#e2e8f0",
  codeText: "#1e293b",
  inlineCodeBg: "#f1f5f9",
  inlineCodeText: "#0f172a",
  quoteBg: "#f8fafc",
  quoteBorder: "#cbd5e1",
  thinkingBg: "#f8fafc",
  thinkingBorder: "#cbd5e1",
  pillBg: "#f8fafc",
  pillBorder: "#e2e8f0",
  tableHeaderBg: "#f8fafc",
  tableBorder: "#e2e8f0",
  okDot: "#10b981",
  errDot: "#ef4444",
  warnDot: "#f59e0b",
  linkText: "#2563eb",
};

export const DARK_PALETTE: ShareThemePalette = {
  mode: "dark",
  outerBg: "#0a0d13",
  cardBg: "#131822",
  cardBorder: "#232b3a",
  cardShadow: "rgba(0, 0, 0, 0.42)",
  divider: "#1f2735",
  textPrimary: "#f1f5f9",
  textSecondary: "#cbd5e1",
  textMuted: "#94a3b8",
  textFaint: "#64748b",
  accent: "#60a5fa",
  accentBg: "#172554",
  accentBorder: "#1e3a8a",
  userBg: "#1a2231",
  userBorder: "#273245",
  codeBg: "#0d1117",
  codeHeaderBg: "#161b22",
  codeBorder: "#242c3a",
  codeText: "#e2e8f0",
  inlineCodeBg: "#1e2636",
  inlineCodeText: "#e2e8f0",
  quoteBg: "#171d29",
  quoteBorder: "#3b475e",
  thinkingBg: "#161c27",
  thinkingBorder: "#334155",
  pillBg: "#1a2130",
  pillBorder: "#283246",
  tableHeaderBg: "#18202e",
  tableBorder: "#252f42",
  okDot: "#34d399",
  errDot: "#f87171",
  warnDot: "#fbbf24",
  linkText: "#60a5fa",
};

export const FONT_SANS =
  'Inter, -apple-system, BlinkMacSystemFont, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Segoe UI", sans-serif';
export const FONT_MONO =
  '"JetBrains Mono", "SFMono-Regular", Menlo, Monaco, Consolas, "Liberation Mono", monospace';

// Unified 7x8 dot-matrix H brand mark (matches HoxiActivityLogo.tsx, 20x20 viewBox).
export const HOXI_MATRIX_BODY_DOTS: readonly [number, number][] = [
  [3.7, 2.65], [5.8, 2.65], [14.2, 2.65], [16.3, 2.65],
  [3.7, 4.75], [5.8, 4.75], [14.2, 4.75], [16.3, 4.75],
  [3.7, 6.85], [5.8, 6.85], [14.2, 6.85], [16.3, 6.85],
  [3.7, 8.95], [5.8, 8.95], [7.9, 8.95], [12.1, 8.95], [14.2, 8.95], [16.3, 8.95],
  [3.7, 11.05], [5.8, 11.05], [7.9, 11.05], [12.1, 11.05], [14.2, 11.05], [16.3, 11.05],
  [3.7, 13.15], [5.8, 13.15], [14.2, 13.15], [16.3, 13.15],
  [3.7, 15.25], [5.8, 15.25], [14.2, 15.25], [16.3, 15.25],
  [3.7, 17.35], [5.8, 17.35], [14.2, 17.35], [16.3, 17.35],
];
export const HOXI_MATRIX_SPARK_DOTS: readonly [number, number][] = [
  [10, 8.95],
  [10, 11.05],
];

export function drawHoxiMatrixMark(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  size: number,
  palette: ShareThemePalette
): void {
  const s = size / 20;
  const drawDots = (dots: readonly [number, number][], radius: number) => {
    ctx.beginPath();
    for (const [cx, cy] of dots) {
      const px = x + cx * s;
      const py = y + cy * s;
      ctx.moveTo(px + radius * s, py);
      ctx.arc(px, py, radius * s, 0, Math.PI * 2);
    }
    ctx.fill();
  };
  ctx.save();
  ctx.fillStyle = palette.textPrimary;
  ctx.globalAlpha = 0.84;
  drawDots(HOXI_MATRIX_BODY_DOTS, 0.78);
  ctx.globalAlpha = 1;
  ctx.fillStyle = palette.accent;
  drawDots(HOXI_MATRIX_SPARK_DOTS, 0.86);
  ctx.restore();
}

const MAX_CODE_BLOCK_LINES = 85;

export interface LoadedImageInfo {
  image: HTMLImageElement;
  naturalWidth: number;
  naturalHeight: number;
}

export interface TextRun {
  text: string;
  font: string;
  color: string;
  width: number;
  inlineCode?: boolean;
  strike?: boolean;
  underline?: boolean;
}

export interface WrappedLine {
  runs: TextRun[];
  height: number;
}

export interface PositionedPill {
  x: number;
  y: number;
  width: number;
  height: number;
  label: string;
  detail: string;
  status: ShareToolPill["status"];
}

export type LayoutBlock =
  | { kind: "spacer"; height: number }
  | { kind: "divider"; height: number; marginTop: number }
  | { kind: "role-header"; height: number; role: "user" | "assistant"; label: string; modelID?: string; keepWithNext: true }
  | { kind: "user-box"; height: number; lines: WrappedLine[]; roleLabel: string }
  | { kind: "heading"; height: number; level: 1 | 2 | 3 | 4; lines: WrappedLine[]; keepWithNext: true }
  | { kind: "paragraph"; height: number; lines: WrappedLine[]; indentX?: number }
  | { kind: "list-item"; height: number; marker: string; markerX: number; textX: number; lines: WrappedLine[] }
  | { kind: "blockquote"; height: number; lines: WrappedLine[]; alert?: AlertKind }
  | { kind: "thinking"; height: number; label: string; lines: WrappedLine[] }
  | { kind: "tool-pills"; height: number; pills: PositionedPill[] }
  | {
      kind: "code";
      height: number;
      lang: string;
      lines: Array<{ tokens: Array<{ text: string; color: string; bold?: boolean; italic?: boolean }> }>;
      truncatedLines: number;
    }
  | {
      kind: "table";
      height: number;
      colWidths: number[];
      aligns: TableAlign[];
      headerRow: { height: number; cells: WrappedLine[][] };
      bodyRows: Array<{ height: number; cells: WrappedLine[][] }>;
    }
  | {
      kind: "image";
      height: number;
      drawWidth: number;
      drawHeight: number;
      caption: string;
      loaded?: LoadedImageInfo;
      missingLabel?: string;
    };

export interface HeaderLayout {
  height: number;
  titleLines: WrappedLine[];
  metaChips: string[];
}

export interface PaginatedShareLayout {
  cardWidth: number;
  outerPadding: number;
  cardPaddingX: number;
  cardPaddingY: number;
  contentWidth: number;
  header: HeaderLayout;
  footerHeight: number;
  pages: Array<{ blocks: LayoutBlock[]; bodyHeight: number; totalHeight: number }>;
  autoPaginated: boolean;
}

export interface LayoutLabels {
  you: string;
  assistant: string;
  thinking: string;
  moreTools: (count: number) => string;
  truncatedCodeLines: (count: number) => string;
  imageUnavailable: (label: string) => string;
  turnsCount: (count: number) => string;
}

export type TextMeasureFn = (text: string, font: string) => number;

export interface StyledSegment {
  text: string;
  font: string;
  color: string;
  inlineCode?: boolean;
  strike?: boolean;
  underline?: boolean;
}

export function makeFont(size: number, weight = 400, mono = false, italic = false): string {
  const style = italic ? "italic " : "";
  return `${style}${weight} ${size}px ${mono ? FONT_MONO : FONT_SANS}`;
}

function isWideCodePoint(code: number): boolean {
  return (
    code > 0xffff ||
    (code >= 0x2e80 && code <= 0x9fff) ||
    (code >= 0xf900 && code <= 0xfaff) ||
    (code >= 0xff00 && code <= 0xffef) ||
    (code >= 0x3000 && code <= 0x303f) ||
    (code >= 0xac00 && code <= 0xd7af)
  );
}

function splitGraphemeTokens(text: string): string[] {
  const tokens: string[] = [];
  let i = 0;
  while (i < text.length) {
    const ch = text[i];
    if (ch === "\n") {
      tokens.push("\n");
      i++;
      continue;
    }
    if (ch === " " || ch === "\t") {
      let j = i + 1;
      while (j < text.length && (text[j] === " " || text[j] === "\t")) j++;
      tokens.push(text.slice(i, j));
      i = j;
      continue;
    }
    const code = text.codePointAt(i) ?? 0;
    if (isWideCodePoint(code)) {
      const step = code > 0xffff ? 2 : 1;
      tokens.push(text.slice(i, i + step));
      i += step;
      continue;
    }
    let j = i + 1;
    while (j < text.length) {
      const nextCode = text.codePointAt(j) ?? 0;
      if (text[j] === " " || text[j] === "\t" || text[j] === "\n" || isWideCodePoint(nextCode)) break;
      j++;
    }
    tokens.push(text.slice(i, j));
    i = j;
  }
  return tokens;
}

export function wrapStyledSegments(
  segments: StyledSegment[],
  maxWidth: number,
  lineHeight: number,
  measure: TextMeasureFn
): WrappedLine[] {
  const lines: WrappedLine[] = [];
  let currentRuns: TextRun[] = [];
  let currentWidth = 0;

  const flush = () => {
    lines.push({ runs: currentRuns, height: lineHeight });
    currentRuns = [];
    currentWidth = 0;
  };

  const pushRun = (text: string, seg: StyledSegment, width: number) => {
    const pad = seg.inlineCode ? 8 : 0;
    const last = currentRuns[currentRuns.length - 1];
    if (
      last &&
      !seg.inlineCode &&
      !last.inlineCode &&
      last.font === seg.font &&
      last.color === seg.color &&
      Boolean(last.strike) === Boolean(seg.strike) &&
      Boolean(last.underline) === Boolean(seg.underline)
    ) {
      last.text += text;
      last.width += width;
    } else {
      currentRuns.push({
        text,
        font: seg.font,
        color: seg.color,
        width: width + pad,
        inlineCode: seg.inlineCode,
        strike: seg.strike,
        underline: seg.underline,
      });
    }
    currentWidth += width + pad;
  };

  for (const seg of segments) {
    if (!seg.text) continue;
    for (const token of splitGraphemeTokens(seg.text)) {
      if (token === "\n") {
        flush();
        continue;
      }
      if (currentWidth === 0 && /^\s+$/.test(token) && !seg.inlineCode) continue;
      const pad = seg.inlineCode ? 8 : 0;
      const w = measure(token, seg.font);
      if (currentWidth > 0 && currentWidth + w + pad > maxWidth) {
        flush();
        if (/^\s+$/.test(token) && !seg.inlineCode) continue;
      }
      if (w + pad <= maxWidth) {
        pushRun(token, seg, w);
        continue;
      }
      let chunk = "";
      let chunkW = 0;
      for (const ch of Array.from(token)) {
        const chW = measure(ch, seg.font);
        if (chunk && currentWidth + chunkW + chW + pad > maxWidth) {
          pushRun(chunk, seg, chunkW);
          flush();
          chunk = ch;
          chunkW = chW;
        } else {
          chunk += ch;
          chunkW += chW;
        }
      }
      if (chunk) pushRun(chunk, seg, chunkW);
    }
  }

  if (currentRuns.length > 0 || lines.length === 0) {
    lines.push({ runs: currentRuns, height: lineHeight });
  }
  return lines;
}

export function inlineSpansToSegments(
  spans: InlineSpan[],
  baseSize: number,
  baseWeight: number,
  baseColor: string,
  palette: ShareThemePalette
): StyledSegment[] {
  const out: StyledSegment[] = [];
  for (const span of spans) {
    switch (span.type) {
      case "text":
        out.push({ text: span.text, font: makeFont(baseSize, baseWeight), color: baseColor });
        break;
      case "br":
        out.push({ text: "\n", font: makeFont(baseSize, baseWeight), color: baseColor });
        break;
      case "bold":
        out.push({ text: span.text, font: makeFont(baseSize, Math.max(600, baseWeight)), color: palette.textPrimary });
        break;
      case "italic":
        out.push({ text: span.text, font: makeFont(baseSize, baseWeight, false, true), color: baseColor });
        break;
      case "strike":
        out.push({ text: span.text, font: makeFont(baseSize, baseWeight), color: palette.textMuted, strike: true });
        break;
      case "code":
        out.push({
          text: span.text,
          font: makeFont(Math.max(11.5, baseSize - 1.5), 500, true),
          color: palette.inlineCodeText,
          inlineCode: true,
        });
        break;
      case "link":
        out.push({
          text: span.text || span.href,
          font: makeFont(baseSize, Math.max(500, baseWeight)),
          color: palette.linkText,
          underline: true,
        });
        break;
      case "image":
        out.push({
          text: `[${span.alt || "image"}]`,
          font: makeFont(baseSize - 1, 500),
          color: palette.textMuted,
        });
        break;
    }
  }
  return out;
}

export function wrapMarkdownInline(
  rawText: string,
  maxWidth: number,
  size: number,
  lineHeight: number,
  weight: number,
  color: string,
  palette: ShareThemePalette,
  measure: TextMeasureFn
): WrappedLine[] {
  const spans = parseInline(rawText);
  return wrapStyledSegments(
    inlineSpansToSegments(spans, size, weight, color, palette),
    maxWidth,
    lineHeight,
    measure
  );
}

export function wrapCodeTokens(
  code: string,
  shikiLines: ThemedToken[][] | undefined,
  maxWidth: number,
  palette: ShareThemePalette,
  measure: TextMeasureFn
): {
  lines: Array<{ tokens: Array<{ text: string; color: string; bold?: boolean; italic?: boolean }> }>;
  truncatedLines: number;
} {
  const rawLines = code.replace(/\r\n/g, "\n").split("\n");
  const truncatedLines = Math.max(0, rawLines.length - MAX_CODE_BLOCK_LINES);
  const visibleRaw = truncatedLines > 0 ? rawLines.slice(0, MAX_CODE_BLOCK_LINES) : rawLines;
  const font = makeFont(12.5, 400, true);
  const out: Array<{ tokens: Array<{ text: string; color: string; bold?: boolean; italic?: boolean }> }> = [];

  for (let i = 0; i < visibleRaw.length; i++) {
    const sourceTokens =
      shikiLines && shikiLines[i] && shikiLines[i].length > 0
        ? shikiLines[i].map((tok) => ({
            text: tok.content.replace(/\t/g, "  "),
            color: tok.color || palette.codeText,
            bold: Boolean((tok.fontStyle ?? 0) & 2),
            italic: Boolean((tok.fontStyle ?? 0) & 1),
          }))
        : [{ text: visibleRaw[i].replace(/\t/g, "  "), color: palette.codeText }];

    let lineTokens: Array<{ text: string; color: string; bold?: boolean; italic?: boolean }> = [];
    let lineW = 0;

    for (const tok of sourceTokens) {
      const w = measure(tok.text, font);
      if (lineW + w <= maxWidth) {
        lineTokens.push(tok);
        lineW += w;
        continue;
      }
      let buf = "";
      let bufW = 0;
      for (const ch of Array.from(tok.text)) {
        const chW = measure(ch, font);
        if (lineW + bufW + chW > maxWidth && (lineTokens.length > 0 || buf.length > 0)) {
          if (buf) lineTokens.push({ ...tok, text: buf });
          out.push({ tokens: lineTokens });
          lineTokens = [];
          lineW = 0;
          buf = ch;
          bufW = chW;
        } else {
          buf += ch;
          bufW += chW;
        }
      }
      if (buf) {
        lineTokens.push({ ...tok, text: buf });
        lineW += bufW;
      }
    }
    out.push({ tokens: lineTokens });
  }

  return { lines: out, truncatedLines };
}
