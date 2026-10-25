// web/src/components/timeline/markdown/reveal.ts
import { advanceStreamingSlice } from "./stream";

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
