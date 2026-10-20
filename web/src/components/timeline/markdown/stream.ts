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
