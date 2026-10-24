// web/src/components/timeline/markdown/blocks.ts
import { looksLikeCandidate, normalizeCandidatePath } from "../../../store/workspace";
import type { AlertKind, BlockToken, ListItemEntry, TableAlign } from "./types";

export function isLikelyFilePath(text: string): boolean {
  const trimmed = text.trim();
  if (!trimmed || trimmed.length > 512) return false;
  if (/^\/[^/]+$/.test(trimmed)) return false;
  return looksLikeCandidate(trimmed);
}

export function stripFileLineSuffix(text: string): string {
  return normalizeCandidatePath(text);
}

export function splitTableRow(raw: string): string[] {
  const trimmed = raw.trim().replace(/^\||\|$/g, "");
  const cells: string[] = [];
  let current = "";
  let inCode = false;
  for (let i = 0; i < trimmed.length; i++) {
    const ch = trimmed[i];
    if (ch === "\\" && i + 1 < trimmed.length && trimmed[i + 1] === "|") {
      current += "|";
      i++;
      continue;
    }
    if (ch === "`") {
      inCode = !inCode;
      current += ch;
      continue;
    }
    if (ch === "|" && !inCode) {
      cells.push(current.trim());
      current = "";
      continue;
    }
    current += ch;
  }
  cells.push(current.trim());
  return cells;
}

function isTableSeparatorLine(line: string): boolean {
  const trimmed = line.trim();
  if (!trimmed.includes("|") || !trimmed.includes("-")) return false;
  const cells = splitTableRow(trimmed);
  return cells.length > 0 && cells.every((c) => /^:?-+:?$/.test(c));
}

function parseTableAligns(sepLine: string, colCount: number): TableAlign[] {
  const raw = splitTableRow(sepLine);
  const aligns: TableAlign[] = [];
  for (let i = 0; i < colCount; i++) {
    const c = (raw[i] || "").trim();
    if (c.startsWith(":") && c.endsWith(":")) aligns.push("center");
    else if (c.endsWith(":")) aligns.push("right");
    else aligns.push("left");
  }
  return aligns;
}

function normalizeTableRow(cells: string[], colCount: number): string[] {
  if (cells.length === colCount) return cells;
  if (cells.length > colCount) return cells.slice(0, colCount);
  const padded = cells.slice();
  while (padded.length < colCount) padded.push("");
  return padded;
}

function isPartialTableSeparatorLine(line: string): boolean {
  const trimmed = line.trim();
  if (!trimmed.startsWith("|")) return false;
  return /^\|[\s:|-]*$/.test(trimmed);
}

function isTableStart(lines: string[], idx: number): boolean {
  if (idx + 1 >= lines.length) return false;
  if (!lines[idx].includes("|")) return false;
  if (isTableSeparatorLine(lines[idx + 1])) return true;
  if (idx + 1 === lines.length - 1 && /^\s*\|.+\|\s*$/.test(lines[idx])) {
    return isPartialTableSeparatorLine(lines[idx + 1]);
  }
  return false;
}

function isHorizontalRule(line: string): boolean {
  const trimmed = line.trim();
  if (trimmed.length < 3) return false;
  return /^(?:-\s*){3,}$|^(?:\*\s*){3,}$|^(?:_\s*){3,}$/.test(trimmed);
}

function isPendingHorizontalRuleLine(line: string): boolean {
  return /^\s*(?:-{1,2}|\*{1,2}|_{1,2})\s*$/.test(line) && !/^\s*[-*+]\s+$/.test(line);
}

function matchCodeFenceOpen(
  line: string
): { indent: number; marker: "`" | "~"; count: number; lang: string } | null {
  const m = line.match(/^(\s*)(`{3,}|~{3,})([^\n]*)$/);
  if (!m) return null;
  const fence = m[2];
  const info = m[3].trim();
  if (fence[0] === "`" && info.includes("`")) return null;
  const langToken = info.split(/[\s:]/)[0] || "text";
  return {
    indent: m[1].length,
    marker: fence[0] as "`" | "~",
    count: fence.length,
    lang: langToken,
  };
}

const SINGLE_LINE_FENCE_LANGS = new Set(
  ("json jsonc json5 js javascript jsx mjs cjs ts typescript tsx go golang py python rs rust rb ruby " +
    "sh bash zsh fish shell shellscript console powershell ps1 sql yaml yml toml xml html css scss " +
    "md markdown diff patch c cpp h hpp java kt kotlin swift php lua dart graphql gql ini text txt plaintext")
    .split(" ")
);

function matchSingleLineCodeFence(
  line: string
): { indent: number; marker: "`" | "~"; count: number; lang: string; code: string } | null {
  const m = line.match(/^(\s*)(`{3,}|~{3,})(.*?)(\2)\s*$/);
  if (!m) return null;
  const infoAndCode = m[3].trim();
  if (m[2][0] === "`" && infoAndCode.includes("`")) return null;
  // Only a known language tag followed by code is split off; anything else,
  // such as ```npm run build``` or ```{"a":1}```, is code in full.
  const tagged = infoAndCode.match(/^([A-Za-z0-9_+#.-]+)\s+(\S[\s\S]*)$/);
  const isLang = tagged && SINGLE_LINE_FENCE_LANGS.has(tagged[1].toLowerCase());
  const lang = isLang ? tagged[1] : "text";
  const code = isLang ? tagged[2].trim() : infoAndCode;
  return {
    indent: m[1].length,
    marker: m[2][0] as "`" | "~",
    count: m[2].length,
    lang,
    code,
  };
}

function isPendingCodeFenceLine(line: string, expectedMarker?: "`" | "~"): boolean {
  const m = line.match(/^\s*(`{1,2}|~{1,2})\s*$/);
  if (!m) return false;
  if (expectedMarker && m[1][0] !== expectedMarker) return false;
  return true;
}

function matchHeadingLine(
  line: string,
  isTailLine: boolean
): { level: 1 | 2 | 3 | 4; text: string } | null {
  const m = line.match(/^(#{1,6})\s+(.+)$/);
  if (m) {
    return {
      level: Math.min(4, m[1].length) as 1 | 2 | 3 | 4,
      text: m[2].replace(/\s+#+\s*$/, "").trim(),
    };
  }
  if (isTailLine) {
    const tailMatch = line.match(/^(#{1,6})\s*$/);
    if (tailMatch) {
      return {
        level: Math.min(4, tailMatch[1].length) as 1 | 2 | 3 | 4,
        text: "",
      };
    }
  }
  return null;
}

function matchListMarker(
  line: string,
  isTailLine = false,
  inOrderedList = false,
  inUnorderedList = false
): { indentSpaces: number; kind: "bullet" | "ordered"; orderNumber?: number; text: string } | null {
  const ul = line.match(/^(\s*)[-*+]\s+(.*)$/);
  if (ul) {
    return {
      indentSpaces: ul[1].replace(/\t/g, "  ").length,
      kind: "bullet",
      text: ul[2],
    };
  }
  const ol = line.match(/^(\s*)(\d+)[.)]\s+(.*)$/);
  if (ol) {
    return {
      indentSpaces: ol[1].replace(/\t/g, "  ").length,
      kind: "ordered",
      orderNumber: parseInt(ol[2], 10) || 1,
      text: ol[3],
    };
  }
  if (isTailLine && !isHorizontalRule(line) && !isPendingHorizontalRuleLine(line)) {
    const pendingUl = inUnorderedList
      ? line.match(/^(\s*)[-*+]\s*$/)
      : line.match(/^(\s*)[-*+]\s+$/);
    if (pendingUl) {
      return {
        indentSpaces: pendingUl[1].replace(/\t/g, "  ").length,
        kind: "bullet",
        text: "",
      };
    }
    const pendingOl = inOrderedList
      ? line.match(/^(\s*)(\d+)[.)]?\s*$/)
      : line.match(/^(\s*)(\d+)[.)]\s*$/);
    if (pendingOl) {
      return {
        indentSpaces: pendingOl[1].replace(/\t/g, "  ").length,
        kind: "ordered",
        orderNumber: parseInt(pendingOl[2], 10) || 1,
        text: "",
      };
    }
  }
  return null;
}

export function parseMarkdownBlocks(src: string): BlockToken[] {
  const blocks: BlockToken[] = [];
  const lines = src.replace(/\r\n/g, "\n").split("\n");
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];
    const isTailLine = i === lines.length - 1;

    // Hold trailing 1-2 backticks/tildes or 1-2 rule chars on a new line so it doesn't flash as <p>
    if (isTailLine && (isPendingCodeFenceLine(line) || isPendingHorizontalRuleLine(line))) {
      i++;
      continue;
    }

    // Fenced code block (supports indentation, 3+ backticks/tildes, and info string metadata)
    const singleLineFence = matchSingleLineCodeFence(line);
    if (singleLineFence) {
      blocks.push({ type: "code", lang: singleLineFence.lang, code: singleLineFence.code });
      i++;
      continue;
    }
    const fenceOpen = matchCodeFenceOpen(line);
    if (fenceOpen) {
      const closeRe = new RegExp(`^\\s*${fenceOpen.marker}{${fenceOpen.count},}\\s*$`);
      const stripIndentRe =
        fenceOpen.indent > 0 ? new RegExp(`^ {1,${fenceOpen.indent}}`) : null;
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !closeRe.test(lines[i])) {
        if (i === lines.length - 1 && isPendingCodeFenceLine(lines[i], fenceOpen.marker)) {
          i++;
          break;
        }
        codeLines.push(stripIndentRe ? lines[i].replace(stripIndentRe, "") : lines[i]);
        i++;
      }
      if (i < lines.length) i++; // skip closing fence
      blocks.push({ type: "code", lang: fenceOpen.lang, code: codeLines.join("\n") });
      continue;
    }

    // Blank line
    if (!line.trim()) {
      i++;
      continue;
    }

    // GFM Table (checked before HR so table separator is never misread)
    if (isTableStart(lines, i)) {
      const headers = splitTableRow(line);
      const colCount = Math.max(1, headers.length);
      const aligns = parseTableAligns(lines[i + 1], colCount);
      i += 2; // skip header and separator
      const rows: string[][] = [];
      while (i < lines.length && lines[i].includes("|") && lines[i].trim() !== "") {
        rows.push(normalizeTableRow(splitTableRow(lines[i]), colCount));
        i++;
      }
      blocks.push({ type: "table", headers, aligns, rows });
      continue;
    }

    // Horizontal rule (`---`, `----`, `***`, `___`, `- - -`)
    if (isHorizontalRule(line)) {
      blocks.push({ type: "hr" });
      i++;
      continue;
    }

    // Heading (H1 - H6, including trailing `#` / `### ` while streaming)
    const headingMatch = matchHeadingLine(line, isTailLine);
    if (headingMatch) {
      blocks.push({
        type: "heading",
        level: headingMatch.level,
        text: headingMatch.text,
      });
      i++;
      continue;
    }

    // Blockquote & GitHub-style Alerts (`> [!NOTE]`, `> [!WARNING]`, etc.)
    if (/^>\s?/.test(line)) {
      const quoteLines: string[] = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) {
        quoteLines.push(lines[i].replace(/^>\s?/, ""));
        i++;
      }
      let alert: AlertKind | undefined;
      if (quoteLines.length > 0) {
        const alertMatch = quoteLines[0]
          .trim()
          .match(/^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*(.*)$/i);
        if (alertMatch) {
          alert = alertMatch[1].toLowerCase() as AlertKind;
          const restFirstLine = alertMatch[2].trim();
          if (restFirstLine) {
            quoteLines[0] = restFirstLine;
          } else {
            quoteLines.shift();
          }
        } else if (
          i === lines.length &&
          quoteLines.length === 1 &&
          /^\[![A-Za-z]*\]?$/.test(quoteLines[0].trim())
        ) {
          quoteLines[0] = "";
        }
      }
      blocks.push({ type: "blockquote", alert, text: quoteLines.join("\n").trim() });
      continue;
    }

    // Lists (Unordered & Ordered, preserving nested sub-items and streaming tail markers)
    const firstList = matchListMarker(line, isTailLine, false);
    if (firstList) {
      const baseIndent = firstList.indentSpaces;
      const isOrderedRoot = firstList.kind === "ordered";
      const start = firstList.orderNumber ?? 1;
      const items: ListItemEntry[] = [];
      let rootOrderCounter = start;

      while (i < lines.length) {
        const curLine = lines[i];
        const curIsTail = i === lines.length - 1;
        if (!curLine.trim()) {
          if (curIsTail) {
            i++;
            continue;
          }
          if (
            i + 1 < lines.length &&
            matchListMarker(lines[i + 1], i + 1 === lines.length - 1, isOrderedRoot, !isOrderedRoot)
          ) {
            i++;
            continue;
          }
          break;
        }
        if (
          matchCodeFenceOpen(curLine) ||
          (curIsTail && isPendingCodeFenceLine(curLine)) ||
          isTableStart(lines, i) ||
          isHorizontalRule(curLine) ||
          Boolean(matchHeadingLine(curLine, curIsTail)) ||
          /^>\s?/.test(curLine)
        ) {
          break;
        }

        const marker = matchListMarker(curLine, curIsTail, isOrderedRoot, !isOrderedRoot);
        if (marker) {
          const relIndent = Math.min(
            3,
            Math.floor(Math.max(0, marker.indentSpaces - baseIndent) / 2)
          );
          if (relIndent === 0 && (marker.kind === "ordered") !== isOrderedRoot && items.length > 0) {
            break;
          }
          let orderNum = marker.orderNumber;
          if (marker.kind === "ordered" && relIndent === 0) {
            orderNum = rootOrderCounter++;
          }
          items.push({
            text: marker.text,
            indent: relIndent,
            kind: marker.kind,
            orderNumber: orderNum,
          });
          i++;
          continue;
        }

        const leadingSpaces = (curLine.match(/^(\s*)/)?.[1] ?? "").replace(/\t/g, "  ").length;
        if (items.length > 0 && leadingSpaces >= baseIndent + 2) {
          items[items.length - 1].text += `\n${curLine.trim()}`;
          i++;
          continue;
        }

        break;
      }

      blocks.push({ type: "list", ordered: isOrderedRoot, start, items });
      continue;
    }

    // Regular paragraph (consume until blank line or any block starter)
    const paraLines: string[] = [line];
    i++;
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !matchCodeFenceOpen(lines[i]) &&
      !(i === lines.length - 1 && (isPendingCodeFenceLine(lines[i]) || isPendingHorizontalRuleLine(lines[i]))) &&
      !matchHeadingLine(lines[i], i === lines.length - 1) &&
      !isHorizontalRule(lines[i]) &&
      !isTableStart(lines, i) &&
      !matchListMarker(lines[i], i === lines.length - 1, false) &&
      !/^>\s?/.test(lines[i])
    ) {
      paraLines.push(lines[i]);
      i++;
    }
    blocks.push({ type: "paragraph", text: paraLines.join("\n") });
  }

  return blocks;
}

/**
 * Parses a blockquote body as nested blocks when it contains more than a plain
 * paragraph. Returns null for simple one-paragraph quotes so callers can keep
 * the cheaper inline renderer.
 */
export function parseNestedBlockquote(text: string): BlockToken[] | null {
  if (!text.trim()) return null;
  const blocks = parseMarkdownBlocks(text);
  if (blocks.length === 1 && blocks[0].type === "paragraph") return null;
  return blocks;
}
