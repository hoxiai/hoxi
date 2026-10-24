// web/src/components/timeline/markdown/reveal.ts
import { advanceStreamingSlice } from "./stream";
import type { BlockToken, InlineSpan } from "./types";

/** A run of source characters revealed in one pacer frame, by absolute offset. */
export interface RevealChunk {
  start: number;
  end: number;
  /** `performance.now()`-based timestamp of the frame that revealed it. */
  at: number;
}

/**
 * How long freshly revealed text carries the light trail: it brightens in the accent
 * glow, then cools down to the body color. Older chunks are plain text again.
 */
export const REVEAL_GLOW_MS = 720;
/** Number of stepped highlight levels the trail is painted with (`::highlight(hoxi-reveal-N)`). */
export const REVEAL_LEVELS = 9;
/** How long a newly mounted block (code card, table row, list marker) fades in. */
export const REVEAL_BLOCK_MS = 220;
/** A live view mounting with at most this much text replays it instead of showing it at once. */
export const REVEAL_REPLAY_MAX_CHARS = 200;

const DEFAULT_INTERVAL_MS = 100;
const MIN_INTERVAL_MS = 16;
// Gaps longer than this are a pause in the stream (tool call, slow first token),
// not its delivery rate, so they do not feed the rate estimate.
const MAX_INTERVAL_MS = 600;
const INTERVAL_EWMA_ALPHA = 0.3;
// Drain the buffer slightly slower than the expected arrival interval so the next
// chunk usually lands before the reveal runs dry, which keeps the motion continuous.
const DRAIN_LEAD = 1.15;
const MIN_DRAIN_RATIO = 0.35;
const CATCHUP_CHARS = 240;
const CATCHUP_RATIO_PER_FRAME = 0.12;
const MIN_CHARS_PER_SECOND = 24;
const FRAME_MS = 1000 / 60;
const MAX_SNAP_DEBT = 12;

/**
 * Rate-matched streaming pacer: instead of catching up a fixed share of the backlog
 * every frame (which reveals each network chunk in a burst and then stalls), it
 * estimates the arrival interval and spreads the buffered text evenly until the
 * next chunk is due. A large backlog still catches up quickly. Slice boundaries go
 * through `advanceStreamingSlice` so markdown tokens never render half-closed.
 */
export function createStreamPacer(initial = "", now = 0) {
  let target = initial;
  let shown = initial.length;
  let carry = 0;
  let lastArrival = now;
  let lastStep = 0;
  let interval = DEFAULT_INTERVAL_MS;

  return {
    get target() {
      return target;
    },
    get shown() {
      return shown;
    },
    get pending() {
      return shown < target.length;
    },
    /** Shows `content` in full immediately and forgets the backlog. */
    reset(content: string, at: number) {
      target = content;
      shown = content.length;
      carry = 0;
      lastArrival = at;
      lastStep = 0;
    },
    /**
     * Records newly arrived content. Returns false when it is not an append of the
     * previous content; the caller should then `reset` instead of pacing.
     */
    push(next: string, at: number): boolean {
      if (next === target) return true;
      if (!next.startsWith(target)) return false;
      const gap = at - lastArrival;
      if (gap >= MIN_INTERVAL_MS && gap <= MAX_INTERVAL_MS) {
        interval = interval * (1 - INTERVAL_EWMA_ALPHA) + gap * INTERVAL_EWMA_ALPHA;
      }
      lastArrival = at;
      target = next;
      return true;
    },
    /** Advances one animation frame and returns the new revealed length. */
    step(at: number): number {
      const buffered = target.length - shown;
      if (buffered <= 0) {
        lastStep = 0;
        return shown;
      }
      const dt = lastStep > 0 ? Math.min(50, Math.max(4, at - lastStep)) : FRAME_MS;
      lastStep = at;
      const budget = Math.max(
        interval * MIN_DRAIN_RATIO,
        interval * DRAIN_LEAD - (at - lastArrival)
      );
      let want = buffered * Math.min(1, dt / budget);
      if (buffered > CATCHUP_CHARS) {
        want = Math.max(want, buffered * CATCHUP_RATIO_PER_FRAME * (dt / FRAME_MS));
      }
      carry += Math.max(want, (MIN_CHARS_PER_SECOND * dt) / 1000);
      const whole = Math.min(Math.floor(carry), buffered);
      if (whole <= 0) return shown;
      const next = advanceStreamingSlice(target, shown, shown + whole);
      // Snapping past a whole token reveals extra characters; pay them back over the
      // next frames (bounded) so the overall speed stays even.
      carry = Math.max(carry - (next - shown), -MAX_SNAP_DEBT);
      shown = next;
      return shown;
    },
  };
}

/** Drops chunks whose fade has finished. Returns the same array when nothing changed. */
export function pruneRevealChunks(
  chunks: readonly RevealChunk[],
  now: number,
  fadeMs = REVEAL_GLOW_MS
): readonly RevealChunk[] {
  let drop = 0;
  while (drop < chunks.length && now - chunks[drop].at >= fadeMs) drop++;
  return drop === 0 ? chunks : chunks.slice(drop);
}

/** Trail level for text revealed `age` ms ago: 0 = just appeared, -1 = settled. */
export function revealLevel(age: number, durationMs = REVEAL_GLOW_MS, levels = REVEAL_LEVELS): number {
  if (age >= durationMs) return -1;
  return Math.min(levels - 1, Math.max(0, Math.floor((age / durationMs) * levels)));
}

export interface RevealSegment {
  /** Offsets inside the text node. */
  from: number;
  to: number;
  level: number;
}

/**
 * Maps fresh chunks onto one rendered text run that starts at absolute source offset
 * `start`, returning node-relative segments per trail level (adjacent equal levels
 * merged) so each frame paints only a handful of highlight ranges.
 */
export function revealSegments(
  chunks: readonly RevealChunk[],
  start: number,
  length: number,
  now: number,
  durationMs = REVEAL_GLOW_MS
): RevealSegment[] {
  const segments: RevealSegment[] = [];
  if (start < 0 || length <= 0) return segments;
  const end = start + length;
  for (const chunk of chunks) {
    const from = Math.max(chunk.start, start);
    const to = Math.min(chunk.end, end);
    if (to <= from) continue;
    const level = revealLevel(now - chunk.at, durationMs);
    if (level < 0) continue;
    const last = segments[segments.length - 1];
    if (last && last.level === level && last.to === from - start) last.to = to - start;
    else segments.push({ from: from - start, to: to - start, level });
  }
  return segments;
}

function inlineTextsOf(block: BlockToken, blockIdx: number, maxItems: number): Array<[string, string]> {
  if (block.type === "paragraph" || block.type === "heading") return [[`${blockIdx}`, block.text]];
  if (block.type === "list") {
    const from = Math.max(0, block.items.length - maxItems);
    return block.items.slice(from).map((item, j) => [`${blockIdx}:${from + j}`, item.text]);
  }
  return [];
}

/**
 * Locates the inline source of the trailing blocks (paragraphs, headings and the
 * last list items) inside the displayed markdown, scanning backwards so an earlier
 * block can never match text that belongs to a later one. Keys are `"<block>"` or
 * `"<block>:<item>"`; unlocated entries are absent.
 */
export function locateTailInlines(
  source: string,
  blocks: readonly BlockToken[],
  maxBlocks = 2,
  maxItems = 2
): Map<string, number> {
  const found = new Map<string, number>();
  let limit = source.length;
  for (let i = blocks.length - 1; i >= 0 && i >= blocks.length - maxBlocks; i--) {
    const texts = inlineTextsOf(blocks[i], i, maxItems);
    for (let j = texts.length - 1; j >= 0; j--) {
      const [key, text] = texts[j];
      if (!text || text.length > limit) continue;
      const at = source.lastIndexOf(text, limit - text.length);
      if (at < 0) continue;
      found.set(key, at);
      limit = at;
    }
  }
  return found;
}

function spanVisibleText(span: InlineSpan): string {
  if (span.type === "br") return "";
  if (span.type === "image") return span.alt;
  return span.text;
}

/**
 * Maps each inline span to the offset of its visible text inside the inline source
 * it was parsed from, or -1 when it cannot be located verbatim (escapes, `<br>`).
 */
export function locateInlineSpans(source: string, spans: readonly InlineSpan[]): number[] {
  const offsets: number[] = [];
  let cursor = 0;
  for (const span of spans) {
    const visible = spanVisibleText(span);
    const idx = visible ? source.indexOf(visible, cursor) : -1;
    offsets.push(idx);
    if (idx >= 0) cursor = idx + visible.length;
  }
  return offsets;
}
