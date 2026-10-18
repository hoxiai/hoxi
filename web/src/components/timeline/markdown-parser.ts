// web/src/components/timeline/markdown-parser.ts

const EXTENSIONS_SOURCE =
  "ts|tsx|js|jsx|mjs|cjs|go|py|rs|md|json|yaml|yml|toml|css|scss|less|html|sh|zsh|bash|sql|vue|svelte|c|cpp|cc|h|hpp|m|mm|java|kt|swift|rb|php|proto|graphql";

const LINE_SUFFIX_SOURCE = "(?::\\d+(?:[:\\-–—]\\d+)?|#L\\d+(?:[-–—]L?\\d+)?)?";

const FILE_PATH_PATTERN = new RegExp(
  `^(?:file:\\/\\/)?(?:~\\/|\\.\\.?\\/|\\/)?(?:(?:\\.\\.|[\\w@.-]+)\\/)*[\\w.-]+\\.(?:${EXTENSIONS_SOURCE})${LINE_SUFFIX_SOURCE}$`,
  "i"
);

export function isLikelyFilePath(text: string): boolean {
  const trimmed = text.trim();
  if (!trimmed || trimmed.includes(" ") || trimmed.length > 220) return false;
  if (/^https?:\/\//i.test(trimmed)) return false;
  return FILE_PATH_PATTERN.test(trimmed);
}

export function stripFileLineSuffix(text: string): string {
  return text
    .trim()
    .replace(/^file:\/\//i, "")
    .replace(/(?::\d+(?:[:\-–—]\d+)?|#L\d+(?:[-–—]L?\d+)?)$/i, "");
}

export type TableAlign = "left" | "center" | "right";
export type AlertKind = "note" | "tip" | "important" | "warning" | "caution";

export interface ListItemEntry {
  text: string;
  indent: number;
  kind: "bullet" | "ordered";
  orderNumber?: number;
}

export type BlockToken =
  | { type: "code"; lang: string; code: string }
  | { type: "heading"; level: 1 | 2 | 3 | 4; text: string }
  | { type: "list"; ordered: boolean; start: number; items: ListItemEntry[] }
  | { type: "blockquote"; alert?: AlertKind; text: string }
  | { type: "table"; headers: string[]; aligns: TableAlign[]; rows: string[][] }
  | { type: "hr" }
  | { type: "paragraph"; text: string };

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

function isTableStart(lines: string[], idx: number): boolean {
  if (idx + 1 >= lines.length) return false;
  return lines[idx].includes("|") && isTableSeparatorLine(lines[idx + 1]);
}

function isHorizontalRule(line: string): boolean {
  const trimmed = line.trim();
  if (trimmed.length < 3) return false;
  return /^(?:-\s*){3,}$|^(?:\*\s*){3,}$|^(?:_\s*){3,}$/.test(trimmed);
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

function matchListMarker(
  line: string
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
  return null;
}

export function parseMarkdownBlocks(src: string): BlockToken[] {
  const blocks: BlockToken[] = [];
  const lines = src.replace(/\r\n/g, "\n").split("\n");
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    // Fenced code block (supports indentation, 3+ backticks/tildes, and info string metadata)
    const fenceOpen = matchCodeFenceOpen(line);
    if (fenceOpen) {
      const closeRe = new RegExp(`^\\s*${fenceOpen.marker}{${fenceOpen.count},}\\s*$`);
      const stripIndentRe =
        fenceOpen.indent > 0 ? new RegExp(`^ {1,${fenceOpen.indent}}`) : null;
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !closeRe.test(lines[i])) {
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

    // Heading (H1 - H6)
    const headingMatch = line.match(/^(#{1,6})\s+(.+)$/);
    if (headingMatch) {
      const level = Math.min(4, headingMatch[1].length) as 1 | 2 | 3 | 4;
      blocks.push({
        type: "heading",
        level,
        text: headingMatch[2].replace(/\s+#+\s*$/, "").trim(),
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
        }
      }
      blocks.push({ type: "blockquote", alert, text: quoteLines.join("\n").trim() });
      continue;
    }

    // Lists (Unordered & Ordered, preserving nested sub-items and indented continuation lines)
    const firstList = matchListMarker(line);
    if (firstList) {
      const baseIndent = firstList.indentSpaces;
      const isOrderedRoot = firstList.kind === "ordered";
      const start = firstList.orderNumber ?? 1;
      const items: ListItemEntry[] = [];
      let rootOrderCounter = start;

      while (i < lines.length) {
        const curLine = lines[i];
        if (!curLine.trim()) {
          if (i + 1 < lines.length && matchListMarker(lines[i + 1])) {
            i++;
            continue;
          }
          break;
        }
        if (
          matchCodeFenceOpen(curLine) ||
          isTableStart(lines, i) ||
          isHorizontalRule(curLine) ||
          /^(#{1,6})\s+/.test(curLine) ||
          /^>\s?/.test(curLine)
        ) {
          break;
        }

        const marker = matchListMarker(curLine);
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
      !/^(#{1,6})\s+/.test(lines[i]) &&
      !isHorizontalRule(lines[i]) &&
      !isTableStart(lines, i) &&
      !matchListMarker(lines[i]) &&
      !/^>\s?/.test(lines[i])
    ) {
      paraLines.push(lines[i]);
      i++;
    }
    blocks.push({ type: "paragraph", text: paraLines.join("\n") });
  }

  return blocks;
}

export type InlineSpan =
  | { type: "text"; text: string }
  | { type: "br" }
  | { type: "code"; text: string }
  | { type: "bold"; text: string }
  | { type: "italic"; text: string }
  | { type: "strike"; text: string }
  | { type: "link"; text: string; href: string };

const BARE_FILE_PATH_SOURCE = `(?:\\.\\/)?(?:[\\w@-]+\\/)+[\\w.-]+\\.(?:${EXTENSIONS_SOURCE})${LINE_SUFFIX_SOURCE}`;

const INLINE_TOKEN_REGEX = new RegExp(
  `(\`[^\`]+\`|<br\\s*\\/?>|\\*\\*(?:\`[^\`]+\`|[^*]+|\\*(?!\\*))+\\*\\*|__(?:\`[^\`]+\`|[^_]+|_(?!_))+__|~~[^~\\n]+~~|\\[(?:\`[^\`]+\`|[^\\]]+)\\]\\((?:[^()\\s]+|\\([^()\\s]*\\))+\\)|\\*[^*\\n]+\\*|https?:\\/\\/[^\\s<>\\])"]+|${BARE_FILE_PATH_SOURCE})`,
  "gi"
);

export function parseInline(text: string): InlineSpan[] {
  const spans: InlineSpan[] = [];
  const regex = new RegExp(INLINE_TOKEN_REGEX.source, "gi");
  let lastIdx = 0;
  let match: RegExpExecArray | null;

  while ((match = regex.exec(text)) !== null) {
    const token = match[0];

    // Guard bare file path matches so they aren't mid-word or part of a protocol/URL
    if (
      !token.startsWith("`") &&
      !token.startsWith("<") &&
      !token.startsWith("*") &&
      !token.startsWith("_") &&
      !token.startsWith("~") &&
      !token.startsWith("[") &&
      !/^https?:\/\//i.test(token)
    ) {
      const prevChar = match.index > 0 ? text[match.index - 1] : "";
      if (prevChar && /[A-Za-z0-9_/:.~@-]/.test(prevChar)) {
        continue;
      }
      if (match.index > lastIdx) {
        spans.push({ type: "text", text: text.slice(lastIdx, match.index) });
      }
      spans.push({ type: "code", text: token });
      lastIdx = regex.lastIndex;
      continue;
    }

    if (match.index > lastIdx) {
      spans.push({ type: "text", text: text.slice(lastIdx, match.index) });
    }

    if (/^<br\s*\/?>$/i.test(token)) {
      spans.push({ type: "br" });
    } else if (token.startsWith("`") && token.endsWith("`")) {
      spans.push({ type: "code", text: token.slice(1, -1) });
    } else if (
      (token.startsWith("**") && token.endsWith("**")) ||
      (token.startsWith("__") && token.endsWith("__"))
    ) {
      spans.push({ type: "bold", text: token.slice(2, -2) });
    } else if (token.startsWith("~~") && token.endsWith("~~")) {
      spans.push({ type: "strike", text: token.slice(2, -2) });
    } else if (token.startsWith("[")) {
      const m = token.match(/^\[((?:`[^`]+`|[^\]]+))\]\(((?:[^()\s]+|\([^()\s]*\))+)\)$/);
      if (m) {
        spans.push({ type: "link", text: m[1], href: m[2] });
      } else {
        spans.push({ type: "text", text: token });
      }
    } else if (token.startsWith("*") && token.endsWith("*")) {
      spans.push({ type: "italic", text: token.slice(1, -1) });
    } else if (/^https?:\/\//i.test(token)) {
      const cleanUrl = token.replace(/[.,;:!?]+$/, "");
      const trailing = token.slice(cleanUrl.length);
      spans.push({ type: "link", text: cleanUrl, href: cleanUrl });
      if (trailing) {
        spans.push({ type: "text", text: trailing });
      }
    } else {
      spans.push({ type: "text", text: token });
    }
    lastIdx = regex.lastIndex;
  }

  if (lastIdx < text.length) {
    spans.push({ type: "text", text: text.slice(lastIdx) });
  }
  return spans;
}

export function isSameInlineSpan(a: InlineSpan | undefined, b: InlineSpan): boolean {
  if (!a || a.type !== b.type) return false;
  if (b.type === "br") return true;
  if (a.type !== "br" && a.text !== b.text) return false;
  if (a.type === "link" && b.type === "link") return a.href === b.href;
  return true;
}

function isSameStringArray(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

function isSameListItems(a: ListItemEntry[], b: ListItemEntry[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (
      a[i].text !== b[i].text ||
      a[i].indent !== b[i].indent ||
      a[i].kind !== b[i].kind ||
      a[i].orderNumber !== b[i].orderNumber
    ) {
      return false;
    }
  }
  return true;
}

export function isSameBlockToken(a: BlockToken | undefined, b: BlockToken): boolean {
  if (!a || a.type !== b.type) return false;
  switch (b.type) {
    case "hr":
      return true;
    case "paragraph":
      return (a as typeof b).text === b.text;
    case "blockquote":
      return (a as typeof b).alert === b.alert && (a as typeof b).text === b.text;
    case "heading":
      return (a as typeof b).level === b.level && (a as typeof b).text === b.text;
    case "code":
      return (a as typeof b).lang === b.lang && (a as typeof b).code === b.code;
    case "list":
      return (
        (a as typeof b).ordered === b.ordered &&
        (a as typeof b).start === b.start &&
        isSameListItems((a as typeof b).items, b.items)
      );
    case "table": {
      const prev = a as typeof b;
      if (
        !isSameStringArray(prev.headers, b.headers) ||
        !isSameStringArray(prev.aligns, b.aligns) ||
        prev.rows.length !== b.rows.length
      ) {
        return false;
      }
      for (let r = 0; r < b.rows.length; r++) {
        if (!isSameStringArray(prev.rows[r], b.rows[r])) return false;
      }
      return true;
    }
  }
}
