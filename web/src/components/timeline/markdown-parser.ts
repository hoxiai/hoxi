// web/src/components/timeline/markdown-parser.ts
import { looksLikeCandidate, normalizeCandidatePath } from "../../store/workspace";

const EXTENSIONS_SOURCE =
  "ts|tsx|js|jsx|mjs|cjs|mts|cts|go|py|pyi|rs|md|mdx|markdown|json|jsonc|json5|yaml|yml|toml|xml|plist|svg|png|jpg|jpeg|gif|webp|ico|bmp|avif|pdf|css|scss|sass|less|html|htm|sh|zsh|bash|fish|sql|vue|svelte|astro|c|cpp|cc|cxx|h|hpp|m|mm|java|kt|kts|swift|rb|php|proto|graphql|gql|ini|cfg|conf|env|lock|mod|sum|work|gradle|properties|cmake|mk|dockerfile|tf|tfvars|hcl|prisma|sol|zig|lua|ex|exs|erl|hrl|hs|ml|mli|fs|fsx|cs|vb|dart|groovy|scala|clj|cljs|edn|nim|cr|v|sv|vhd|vhdl|glsl|wgsl|hlsl|metal|wasm|wat|csv|tsv|txt|log";

const KNOWN_FILENAMES_SOURCE =
  "(?:Dockerfile|Containerfile|Makefile|GNUmakefile|Justfile|Caddyfile|Procfile|Gemfile|Rakefile|Guardfile|Brewfile|Vagrantfile|Tiltfile|Earthfile|BUILD|WORKSPACE|CMakeLists\\.txt|CODEOWNERS|LICENSE|README|CHANGELOG|CONTRIBUTING|AUTHORS|NOTICE)(?:\\.[\\w.-]+)?";

const KNOWN_DOTFILES_SOURCE =
  "\\.(?:env(?:\\.[\\w.-]+)?|gitignore|gitattributes|gitmodules|dockerignore|npmignore|eslintignore|prettierignore|editorconfig|nvmrc|node-version|python-version|ruby-version|tool-versions|babelrc(?:\\.[\\w.-]+)?|eslintrc(?:\\.[\\w.-]+)?|prettierrc(?:\\.[\\w.-]+)?|stylelintrc(?:\\.[\\w.-]+)?|npmrc|yarnrc(?:\\.[\\w.-]+)?|bashrc|zshrc|profile|clang-format|clang-tidy)";

const FILE_LEAF_SOURCE = `(?:[\\w.+\\-\\u4e00-\\u9fa5]+\\.(?:${EXTENSIONS_SOURCE})|${KNOWN_FILENAMES_SOURCE}|${KNOWN_DOTFILES_SOURCE})`;

const LINE_SUFFIX_SOURCE = "(?::\\d+(?:[:\\-–—]\\d+)?|#L\\d+(?:[-–—]L?\\d+)?)?";

export function isLikelyFilePath(text: string): boolean {
  const trimmed = text.trim();
  if (!trimmed || trimmed.length > 512) return false;
  if (/^\/[^/]+$/.test(trimmed)) return false;
  return looksLikeCandidate(trimmed);
}

export function stripFileLineSuffix(text: string): string {
  return normalizeCandidatePath(text);
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
  inOrderedList = false
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
  if (isTailLine && !isHorizontalRule(line)) {
    const pendingUl = line.match(/^(\s*)[-*+]\s*$/);
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

    // Hold trailing 1-2 backticks/tildes on a new line so it doesn't flash as <p> before ```
    if (isTailLine && isPendingCodeFenceLine(line)) {
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
            matchListMarker(lines[i + 1], i + 1 === lines.length - 1, isOrderedRoot)
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

        const marker = matchListMarker(curLine, curIsTail, isOrderedRoot);
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
      !(i === lines.length - 1 && isPendingCodeFenceLine(lines[i])) &&
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

export type InlineSpan =
  | { type: "text"; text: string }
  | { type: "br" }
  | { type: "code"; text: string; unclosed?: boolean }
  | { type: "bold"; text: string }
  | { type: "italic"; text: string }
  | { type: "strike"; text: string }
  | { type: "link"; text: string; href: string };

const BARE_FILE_PATH_SOURCE = `(?:~\\/|\\.\\.?\\/|\\/)?(?:[\\w@.+\\-\\u4e00-\\u9fa5]+\\/)+${FILE_LEAF_SOURCE}${LINE_SUFFIX_SOURCE}`;

const INLINE_TOKEN_REGEX = new RegExp(
  `(\`[^\`\\n]+\`|\`[^\`\\n]*$|<br\\s*\\/?>|\\*\\*(?:\`[^\`]+\`|[^*]+|\\*(?!\\*))+\\*\\*|\\*\\*(?:\`[^\`\\n]+\`|\`[^\`\\n]*$|[^*\\n]+|\\*(?!\\*))*$|__(?:\`[^\`]+\`|[^_]+|_(?!_))+__|~~[^~\\n]+~~|\\[(?:\`[^\`]+\`|[^\\]]+)\\]\\((?:[^()\\s]+|\\([^()\\s]*\\))+\\)|\\[(?:\`[^\`\\n]+\`|[^\\]\\n]+)\\]\\([^()\\s]*$|\\*[^*\\n]+\\*|https?:\\/\\/[^\\s<>\\])"]+|${BARE_FILE_PATH_SOURCE})`,
  "gi"
);

function appendPlainTextSpan(spans: InlineSpan[], text: string) {
  if (!text) return;
  const last = spans[spans.length - 1];
  if (last && last.type === "text") {
    last.text += text;
  } else {
    spans.push({ type: "text", text });
  }
}

function stripPendingTrailingDelimiter(tail: string): string {
  // Hold back a lone trailing `*` after start-of-line, whitespace, or open punctuation
  // so the first `*` of an upcoming `**bold**` doesn't flash for one frame.
  if (tail === "*" || /[\s:：,，(（]\*$/.test(tail)) {
    return tail.slice(0, -1);
  }
  return tail;
}

export function parseInline(text: string): InlineSpan[] {
  const spans: InlineSpan[] = [];
  const regex = new RegExp(INLINE_TOKEN_REGEX.source, "gi");
  let lastIdx = 0;
  let match: RegExpExecArray | null;

  while ((match = regex.exec(text)) !== null) {
    const token = match[0];

    // Guard bare file path matches so they aren't mid-word or part of a URL
    if (
      !token.startsWith("`") &&
      !token.startsWith("<") &&
      !token.startsWith("*") &&
      !token.startsWith("_") &&
      !token.startsWith("~~") &&
      !token.startsWith("[") &&
      !/^https?:\/\//i.test(token)
    ) {
      const prevChar = match.index > 0 ? text[match.index - 1] : "";
      if (prevChar && /[A-Za-z0-9_/:.~@\-`]/.test(prevChar)) {
        continue;
      }
      if (match.index > lastIdx) {
        appendPlainTextSpan(spans, text.slice(lastIdx, match.index));
      }
      spans.push({ type: "code", text: token });
      lastIdx = regex.lastIndex;
      continue;
    }

    if (match.index > lastIdx) {
      appendPlainTextSpan(spans, text.slice(lastIdx, match.index));
    }

    if (/^<br\s*\/?>$/i.test(token)) {
      spans.push({ type: "br" });
    } else if (token.startsWith("`")) {
      if (token.length >= 2 && token.endsWith("`")) {
        spans.push({ type: "code", text: token.slice(1, -1) });
      } else {
        const codeBody = token.slice(1);
        if (codeBody.length > 0) {
          spans.push({ type: "code", text: codeBody, unclosed: true });
        }
      }
    } else if (
      (token.startsWith("**") && token.length >= 4 && token.endsWith("**")) ||
      (token.startsWith("__") && token.length >= 4 && token.endsWith("__"))
    ) {
      spans.push({ type: "bold", text: token.slice(2, -2) });
    } else if (token.startsWith("**")) {
      const rawBody = token.slice(2);
      const boldBody = rawBody.endsWith("*") ? rawBody.slice(0, -1) : rawBody;
      if (boldBody.length > 0) {
        spans.push({ type: "bold", text: boldBody });
      }
    } else if (token.startsWith("~~") && token.endsWith("~~")) {
      spans.push({ type: "strike", text: token.slice(2, -2) });
    } else if (token.startsWith("[")) {
      const closedMatch = token.match(
        /^\[((?:`[^`]+`|[^\]]+))\]\(((?:[^()\s]+|\([^()\s]*\))+)\)$/
      );
      if (closedMatch) {
        spans.push({ type: "link", text: closedMatch[1], href: closedMatch[2] });
      } else {
        const unclosedMatch = token.match(/^\[((?:`[^`\n]+`|[^\]\n]+))\]\(([^()\s]*)$/);
        if (unclosedMatch) {
          spans.push({
            type: "link",
            text: unclosedMatch[1],
            href: unclosedMatch[2] || "#",
          });
        } else {
          appendPlainTextSpan(spans, token);
        }
      }
    } else if (token.startsWith("*") && token.endsWith("*")) {
      spans.push({ type: "italic", text: token.slice(1, -1) });
    } else if (/^https?:\/\//i.test(token)) {
      const cleanUrl = token.replace(/[.,;:!?]+$/, "");
      const trailing = token.slice(cleanUrl.length);
      spans.push({ type: "link", text: cleanUrl, href: cleanUrl });
      if (trailing) {
        appendPlainTextSpan(spans, trailing);
      }
    } else {
      appendPlainTextSpan(spans, token);
    }
    lastIdx = regex.lastIndex;
  }

  if (lastIdx < text.length) {
    appendPlainTextSpan(spans, stripPendingTrailingDelimiter(text.slice(lastIdx)));
  }
  return spans;
}

export function isSameInlineSpan(a: InlineSpan | undefined, b: InlineSpan): boolean {
  if (!a || a.type !== b.type) return false;
  if (b.type === "br") return true;
  if (a.type !== "br" && a.text !== b.text) return false;
  if (a.type === "code" && b.type === "code") {
    return Boolean(a.unclosed) === Boolean(b.unclosed);
  }
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

function isSameListItem(a: ListItemEntry | undefined, b: ListItemEntry): boolean {
  if (!a) return false;
  return (
    a.text === b.text &&
    a.indent === b.indent &&
    a.kind === b.kind &&
    a.orderNumber === b.orderNumber
  );
}

function isSameListItems(a: ListItemEntry[], b: ListItemEntry[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (!isSameListItem(a[i], b[i])) return false;
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

/**
 * Reuses unchanged block references AND unchanged sub-items (`list.items[j]`,
 * `table.rows[r]`) so streaming the last bullet point or table cell never
 * invalidates earlier items in the same block.
 */
export function stabilizeBlockToken(prev: BlockToken | undefined, next: BlockToken): BlockToken {
  if (isSameBlockToken(prev, next)) return prev!;
  if (!prev || prev.type !== next.type) return next;
  if (next.type === "list" && prev.type === "list") {
    for (let i = 0; i < next.items.length; i++) {
      if (isSameListItem(prev.items[i], next.items[i])) {
        next.items[i] = prev.items[i];
      }
    }
    return next;
  }
  if (next.type === "table" && prev.type === "table") {
    if (isSameStringArray(prev.headers, next.headers)) {
      next.headers = prev.headers;
    }
    if (isSameStringArray(prev.aligns, next.aligns)) {
      next.aligns = prev.aligns;
    }
    for (let r = 0; r < next.rows.length; r++) {
      if (prev.rows[r] && isSameStringArray(prev.rows[r], next.rows[r])) {
        next.rows[r] = prev.rows[r];
      }
    }
    return next;
  }
  return next;
}

/**
 * Incremental streaming wrapper around `parseMarkdownBlocks` (inspired by Kilo Code's
 * `markdown-stream.ts` projection + `appendFence` fast-path):
 * - Caches finalized prefix blocks before the last 2 active blocks by character offset
 *   so long streaming responses only parse the active tail slice on each 60Hz frame.
 * -Stabilizes unchanged block/list/table object references in place for SolidJS `<Index>`.
 */
export function createIncrementalMarkdownParser(): (src: string) => BlockToken[] {
  let prevNorm = "";
  let prevBlocks: BlockToken[] = [];
  let checkpointOffset = 0;
  let checkpointBlocks: BlockToken[] = [];

  const findSecondLastBlockOffset = (norm: string): { offset: number; prefixBlocks: BlockToken[] } => {
    // Locate a safe double-newline boundary outside any code fence so we can checkpoint closed blocks.
    const allBlocks = parseMarkdownBlocks(norm);
    if (allBlocks.length < 4 || norm.length < 1024) {
      return { offset: 0, prefixBlocks: [] };
    }
    // Verify by finding the last blank-line boundary before the final ~512 chars where prefix parses cleanly.
    const searchFrom = Math.max(0, norm.length - 600);
    const blankIdx = norm.lastIndexOf("\n\n", searchFrom);
    if (blankIdx <= 0) return { offset: 0, prefixBlocks: [] };
    const candidateOffset = blankIdx + 2;
    const prefix = parseMarkdownBlocks(norm.slice(0, blankIdx));
    const suffix = parseMarkdownBlocks(norm.slice(candidateOffset));
    if (prefix.length + suffix.length !== allBlocks.length) {
      return { offset: 0, prefixBlocks: [] };
    }
    for (let i = 0; i < prefix.length; i++) {
      if (!isSameBlockToken(prefix[i], allBlocks[i])) {
        return { offset: 0, prefixBlocks: [] };
      }
    }
    for (let j = 0; j < suffix.length; j++) {
      if (!isSameBlockToken(suffix[j], allBlocks[prefix.length + j])) {
        return { offset: 0, prefixBlocks: [] };
      }
    }
    return { offset: candidateOffset, prefixBlocks: allBlocks.slice(0, prefix.length) };
  };

  return (rawSrc: string): BlockToken[] => {
    const norm = (rawSrc || "").replace(/\r\n/g, "\n");
    if (norm === prevNorm) return prevBlocks;

    let next: BlockToken[];
    if (checkpointOffset > 0 && norm.startsWith(prevNorm) && norm.length >= checkpointOffset) {
      const tailBlocks = parseMarkdownBlocks(norm.slice(checkpointOffset));
      next = [...checkpointBlocks, ...tailBlocks];
    } else {
      checkpointOffset = 0;
      checkpointBlocks = [];
      next = parseMarkdownBlocks(norm);
    }

    for (let i = 0; i < next.length; i++) {
      next[i] = stabilizeBlockToken(prevBlocks[i], next[i]);
    }

    if (norm.startsWith(prevNorm) && norm.length - checkpointOffset > 1600 && next.length >= 4) {
      const cp = findSecondLastBlockOffset(norm);
      if (cp.offset > checkpointOffset) {
        checkpointOffset = cp.offset;
        checkpointBlocks = next.slice(0, cp.prefixBlocks.length);
      }
    } else if (!norm.startsWith(prevNorm)) {
      checkpointOffset = 0;
      checkpointBlocks = [];
    }

    prevNorm = norm;
    prevBlocks = next;
    return next;
  };
}
