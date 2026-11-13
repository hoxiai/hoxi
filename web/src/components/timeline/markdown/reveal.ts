// web/src/components/timeline/markdown/reveal.ts
import { advanceStreamingSlice, TABLE_SEPARATOR_RE } from "./stream";

/**
 * Duration (ms) for each newly revealed inline text slice to softly fade from
 * faint (`opacity: 0.06`) to full body contrast (`opacity: 1`).
 */
export const REVEAL_CHUNK_MS = 320;

/**
 * Duration (ms) for a newly mounted block or row (code card, blockquote, table row,
 * list item) to softly glide and fade in during live streaming.
 */
export const REVEAL_BLOCK_MS = 240;

/**
 * A live streaming view mounting with at most this many initial characters paces
 * them from the start so the opening words also fade in softly; longer initial
 * content (e.g. switching to an already-running session) renders immediately.
 */
export const REVEAL_REPLAY_MAX_CHARS = 180;

/** Bound parsing/layout frequency for long live output while short replies stay frame-paced. */
export function streamRenderIntervalMs(length: number): number {
  if (length >= 64_000) return 64;
  if (length >= 8_000) return 32;
  return 0;
}

const DEFAULT_INTERVAL_MS = 96;
const MIN_INTERVAL_MS = 16;
// Gaps longer than this represent a pause (tool call, slow first token) rather
// than steady token delivery rate, so they do not skew the interval EWMA.
const MAX_INTERVAL_MS = 600;
const INTERVAL_EWMA_ALPHA = 0.3;
// Drain slightly slower than the expected arrival interval so the next SSE chunk
// usually lands before the buffer runs dry, keeping typing motion continuous.
const DRAIN_LEAD = 1.15;
const MIN_DRAIN_RATIO = 0.35;
const CATCHUP_CHARS = 180;
const CATCHUP_RATIO_PER_FRAME = 0.14;
const MIN_CHARS_PER_SECOND = 48;
const FRAME_MS = 1000 / 60;

const TABLE_LINE_RE = /^\s*\|/;

/**
 * Text a streaming view may reveal while more is still arriving. A table row
 * is held back until its line is complete, and a table's header and separator
 * until its first body row is complete, so the table first appears with real
 * content. Revealing rows as they type in made the table reflow its columns on
 * every chunk, and a header shown alone was resized again by its first row.
 * Once the stream ends, the caller reveals the full text.
 */
export function holdIncompleteTableRows(text: string): string {
  const partialStart = text.lastIndexOf("\n") + 1;
  const partial = text.slice(partialStart);
  if (!/^\s*$/.test(partial) && !TABLE_LINE_RE.test(partial)) return text;
  // The complete lines just before the one being written, newest first.
  const starts: number[] = [];
  let lineEnd = partialStart - 1;
  while (lineEnd >= 0 && starts.length < 3) {
    const start = text.lastIndexOf("\n", lineEnd - 1) + 1;
    starts.push(start);
    lineEnd = start - 1;
  }
  const line = (i: number) => (i < starts.length ? text.slice(starts[i], (i === 0 ? partialStart : starts[i - 1]) - 1) : undefined);
  const isTable = (value: string | undefined) => value !== undefined && TABLE_LINE_RE.test(value);
  const [last, before, earlier] = [line(0), line(1), line(2)];
  if (isTable(last) && !TABLE_SEPARATOR_RE.test(last!) && !isTable(before)) return text.slice(0, starts[0]);
  if (isTable(last) && TABLE_SEPARATOR_RE.test(last!) && isTable(before) && !isTable(earlier)) {
    return text.slice(0, starts[1]);
  }
  return partial === "" || TABLE_LINE_RE.test(partial) || isTable(last)
    ? text.slice(0, partialStart)
    : text;
}

/**
 * What a replaying view shows on mount: up to its first visible character.
 * Mounting empty and revealing on the next frame left one frame with no
 * content; the waiting row it replaces had already gone, so the timeline
 * shrank and jumped back.
 */
export function initialRevealSlice(content: string): string {
  const firstVisible = content.search(/\S/);
  if (firstVisible < 0) return content;
  return content.slice(0, advanceStreamingSlice(content, 0, firstVisible + 1));
}

/**
 * Rate-matched streaming pacer: estimates the chunk arrival interval and spreads
 * buffered characters evenly across animation frames until the next chunk is due,
 * while accelerating smoothly on large backlogs.
 *
 * Slice boundaries pass through `advanceStreamingSlice` so complete inline tokens
 * (`**bold**`, `` `code` ``, `[label](url)`, table rows) never render half-open,
 * and token snaps never accumulate negative debt that would stall subsequent frames.
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
    /** Shows `content` in full immediately and clears any buffered backlog. */
    reset(content: string, at: number) {
      target = content;
      shown = content.length;
      carry = 0;
      lastArrival = at;
      lastStep = 0;
    },
    /**
     * Records newly arrived content. Returns false when `next` is not an append
     * of the current target (e.g. session switch or revert), signaling the caller
     * to `reset` immediately instead of pacing.
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
    /** Advances one animation frame and returns the new revealed character length. */
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
      // Do not carry negative debt after snapping past an atomic markdown token;
      // negative carry would freeze subsequent frames for ratusan ms after each token.
      carry = Math.max(0, carry - (next - shown));
      shown = next;
      return shown;
    },
  };
}
