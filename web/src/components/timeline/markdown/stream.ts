// web/src/components/timeline/markdown/stream.ts
import { parseMarkdownBlocks } from "./blocks";
import type { BlockToken, ListItemEntry } from "./types";

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
  if (a === b) return true;
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
 * - Stabilizes unchanged block/list/table object references in place for SolidJS `<Index>`.
 */
export function createIncrementalMarkdownParser(): (src: string) => BlockToken[] {
  let prevNorm = "";
  let prevBlocks: BlockToken[] = [];
  let checkpointOffset = 0;
  let checkpointBlocks: BlockToken[] = [];
  let lastAttemptLength = 0;

  const findTailCheckpoint = (
    tail: string,
    tailBlocks: BlockToken[]
  ): { relOffset: number; prefixCount: number } => {
    // Locate a safe double-newline boundary outside any code fence within the active tail.
    if (tailBlocks.length < 2 || tail.length < 1024) {
      return { relOffset: 0, prefixCount: 0 };
    }
    const searchFrom = Math.max(0, tail.length - 600);
    const blankIdx = tail.lastIndexOf("\n\n", searchFrom);
    if (blankIdx <= 0) return { relOffset: 0, prefixCount: 0 };
    const candidateRelOffset = blankIdx + 2;
    const prefix = parseMarkdownBlocks(tail.slice(0, blankIdx));
    if (prefix.length === 0 || prefix.length >= tailBlocks.length) {
      return { relOffset: 0, prefixCount: 0 };
    }
    for (let i = 0; i < prefix.length; i++) {
      if (!isSameBlockToken(prefix[i], tailBlocks[i])) {
        return { relOffset: 0, prefixCount: 0 };
      }
    }
    const suffix = parseMarkdownBlocks(tail.slice(candidateRelOffset));
    if (prefix.length + suffix.length !== tailBlocks.length) {
      return { relOffset: 0, prefixCount: 0 };
    }
    for (let j = 0; j < suffix.length; j++) {
      if (!isSameBlockToken(suffix[j], tailBlocks[prefix.length + j])) {
        return { relOffset: 0, prefixCount: 0 };
      }
    }
    return { relOffset: candidateRelOffset, prefixCount: prefix.length };
  };

  return (rawSrc: string): BlockToken[] => {
    const norm = (rawSrc || "").replace(/\r\n/g, "\n");
    if (norm === prevNorm) return prevBlocks;

    let tail: string;
    let tailBlocks: BlockToken[];
    let next: BlockToken[];
    if (checkpointOffset > 0 && norm.startsWith(prevNorm) && norm.length >= checkpointOffset) {
      tail = norm.slice(checkpointOffset);
      tailBlocks = parseMarkdownBlocks(tail);
      next = [...checkpointBlocks, ...tailBlocks];
    } else {
      checkpointOffset = 0;
      checkpointBlocks = [];
      lastAttemptLength = 0;
      tail = norm;
      tailBlocks = parseMarkdownBlocks(norm);
      next = [...tailBlocks];
    }

    const stabilizeStart = checkpointBlocks.length;
    for (let i = stabilizeStart; i < next.length; i++) {
      next[i] = stabilizeBlockToken(prevBlocks[i], next[i]);
    }

    if (
      norm.startsWith(prevNorm) &&
      tail.length > 1600 &&
      tailBlocks.length >= 2 &&
      next.length >= 4 &&
      norm.length - lastAttemptLength >= 512
    ) {
      lastAttemptLength = norm.length;
      const cp = findTailCheckpoint(tail, tailBlocks);
      if (cp.relOffset > 0 && cp.prefixCount > 0) {
        checkpointOffset += cp.relOffset;
        checkpointBlocks = next.slice(0, checkpointBlocks.length + cp.prefixCount);
      }
    } else if (!norm.startsWith(prevNorm)) {
      checkpointOffset = 0;
      checkpointBlocks = [];
      lastAttemptLength = 0;
    }

    prevNorm = norm;
    prevBlocks = next;
    return next;
  };
}

const STREAM_ATOMIC_INLINE_REGEX =
  /!?\[(?:`[^`\n]+`|[^\]`\n])+\]\((?:[^()\s]|\([^()\s]*\))+(?:\s+(?:"[^"\n]*"|'[^'\n]*'))?\)|!?\[(?:`[^`\n]+`|[^\]`\n])+\]\([^()\s]*$|`[^`\n]+`|\*\*(?:`[^`\n]+`|[^*`\n]|\*(?!\*)){1,48}\*\*|__(?:`[^`\n]+`|[^_`\n]|_(?!_)){1,48}__|~~[^~\n]{1,48}~~|\*[^*\n]{1,48}\*|https?:\/\/[^\s<>)\]]+|(?:~\/|\.\.?\/|\/)?(?:[\w@.+\-\u4e00-\u9fa5]+\/)+[\w.+\-\u4e00-\u9fa5]+(?::\d+(?:[:\-–—]\d+)?|#L\d+(?:[-–—]L?\d+)?)?/g;

const STREAM_LINE_PREFIX_REGEX =
  /^(\s*(?:#{1,6}[ \t]+\S?|(?:`{3,}|~{3,})[^\n]*|(?:[-*+]|\d+[.)])[ \t]+(?:\[[ xX]\][ \t]+)?\S?|>\s*(?:\[![A-Za-z]+\]\s*)?|(?:[-*_]\s*){3,}$))/;

/**
 * Aligns streaming Pacer slice boundaries so complete inline tokens (`[label](url)`,
 * `` `code` ``, `**bold**`, file paths) and line-start markers (`### `, `- `, ```` ``` ````)
 * already present in `target` are never cut into half-closed intermediate states.
 */
export function advanceStreamingSlice(
  target: string,
  currentLen: number,
  rawNextLen: number
): number {
  if (!target || currentLen >= target.length) return target.length;
  let nextLen = Math.min(target.length, Math.max(currentLen + 1, rawNextLen));

  // Never split a UTF-16 surrogate pair
  const code = target.charCodeAt(nextLen - 1);
  if (code >= 0xd800 && code <= 0xdbff && nextLen < target.length) {
    nextLen++;
  }

  const lineStart = target.lastIndexOf("\n", nextLen - 1) + 1;
  const lineEndIdx = target.indexOf("\n", lineStart);
  const lineEnd = lineEndIdx === -1 ? target.length : lineEndIdx;
  const line = target.slice(lineStart, lineEnd);
  let relPos = nextLen - lineStart;

  // Reveal table rows (header, separator, body) a whole line at a time so the block
  // never flips between paragraph and table, or reflows cells, while pipes type in.
  if (/^\s*\|/.test(line)) {
    return lineEndIdx === -1 ? lineEnd : lineEnd + 1;
  }

  // Snap past line-start block markers (`### `, `- `, `1. `, code fence opener)
  const prefixMatch = line.match(STREAM_LINE_PREFIX_REGEX);
  if (prefixMatch && relPos <= prefixMatch[1].length) {
    if (/^\s*(?:`{3,}|~{3,})/.test(prefixMatch[1])) {
      return lineEndIdx === -1 ? lineEnd : lineEnd + 1;
    }
    relPos = Math.max(relPos, prefixMatch[1].length);
  }

  // Snap past complete inline tokens (links, images, code spans, file paths, short emphasis)
  const inlineRe = new RegExp(STREAM_ATOMIC_INLINE_REGEX.source, "g");
  let m: RegExpExecArray | null;
  while ((m = inlineRe.exec(line)) !== null) {
    const start = m.index;
    const end = start + m[0].length;
    if (start < relPos && relPos < end) {
      relPos = end;
      break;
    }
    if (start >= relPos) break;
  }

  nextLen = Math.min(target.length, lineStart + relPos);

  // Do not stop mid-delimiter run (`**`, `__`, `~~`, `![`, `](`) when the next char is already in target
  while (
    nextLen < lineEnd &&
    /[*_~`#\-![\]]/.test(target[nextLen - 1]) &&
    /[*_~`#\-![(]/.test(target[nextLen])
  ) {
    nextLen++;
  }

  // Re-verify surrogate pair after adjustments
  if (nextLen < target.length) {
    const c = target.charCodeAt(nextLen - 1);
    if (c >= 0xd800 && c <= 0xdbff) nextLen++;
  }

  // If the stream chunk ends right on a closing `]` of `[label]`, hold before `]` for at most
  // one frame so if `(` arrives in the next chunk, `[label]` never flashes as literal brackets.
  if (
    nextLen === target.length &&
    nextLen - 1 > currentLen &&
    /\[(?:[\u4e00-\u9fa5][^\[\]\n]*|[A-Za-z][^\[\]\n]+)\]$/.test(line)
  ) {
    return nextLen - 1;
  }

  return nextLen;
}
