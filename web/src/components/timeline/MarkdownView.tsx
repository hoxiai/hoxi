import { createEffect, createMemo, createSignal, onCleanup, Show, untrack } from "solid-js";
import { createIncrementalMarkdownParser } from "./markdown-parser";
import { createStreamPacer, REVEAL_REPLAY_MAX_CHARS } from "./markdown/reveal";
import {
  createRevealPainter,
  createStreamCaret,
  prefersReducedMotion,
  type StreamReveal,
} from "./stream-reveal";
import MarkdownBlocks from "./MarkdownBlocks";

export {
  advanceStreamingSlice,
  type AlertKind,
  type BlockToken,
  isLikelyFilePath,
  isSameBlockToken,
  type ListItemEntry,
  parseInline,
  parseMarkdownBlocks,
  splitTableRow,
  stabilizeBlockToken,
  type TableAlign,
} from "./markdown-parser";

const clock = () => (typeof performance !== "undefined" ? performance.now() : Date.now());

/**
 * `streaming` marks the live tail of a busy session: new text lights up and cools into
 * the body color, new blocks fade in and a glowing dot leads the text. Without it the
 * view only paces growing content evenly, as before.
 */
export default function MarkdownView(props: {
  content: string;
  density?: "chat" | "compact";
  streaming?: boolean;
}) {
  const parseIncremental = createIncrementalMarkdownParser();
  const canAnimate = typeof requestAnimationFrame === "function";
  const initial = (props.content || "").replace(/\r\n/g, "\n");
  // A live part that mounts with little text replays it so its first words light up
  // too; a long one (e.g. reopening a busy session) shows at once.
  const replay = Boolean(props.streaming) && canAnimate && initial.length <= REVEAL_REPLAY_MAX_CHARS;
  const pacer = createStreamPacer(replay ? "" : initial, clock());
  const painter = createRevealPainter();
  const caret = createStreamCaret();
  const [displayed, setDisplayed] = createSignal(replay ? "" : initial);
  const [revealEnabled, setRevealEnabled] = createSignal(false);
  let container: HTMLDivElement | undefined;
  let rafId: number | undefined;

  createEffect(() => {
    if (props.streaming && canAnimate && !prefersReducedMotion()) setRevealEnabled(true);
  });

  const cancelFrame = () => {
    if (rafId !== undefined && typeof cancelAnimationFrame === "function") {
      cancelAnimationFrame(rafId);
    }
    rafId = undefined;
  };

  // One loop drives the pacer, the light trail and the caret in that order, so the
  // trail is painted in the same frame the new characters enter the DOM.
  const frame = (ts: number) => {
    rafId = undefined;
    const prev = pacer.shown;
    const next = pacer.step(ts);
    if (next !== prev) {
      setDisplayed(pacer.target.slice(0, next));
      if (untrack(revealEnabled)) painter.push({ start: prev, end: next, at: ts });
    }
    const glowing = untrack(revealEnabled) && painter.paint(ts);
    if (untrack(() => props.streaming)) caret.place(container);
    if (pacer.pending || glowing) rafId = requestAnimationFrame(frame);
  };

  const ensureFrame = () => {
    if (rafId === undefined && canAnimate) rafId = requestAnimationFrame(frame);
  };

  createEffect(() => {
    const next = (props.content || "").replace(/\r\n/g, "\n");
    const at = clock();
    const streaming = untrack(() => props.streaming);
    const jump =
      !canAnimate ||
      // Content that shows up in an empty, non-live view (a file preview loading)
      // appears at once rather than typing itself out.
      (!streaming && pacer.shown === 0) ||
      !pacer.push(next, at);
    if (jump) {
      cancelFrame();
      painter.clear();
      pacer.reset(next, at);
      setDisplayed(next);
      if (streaming) ensureFrame();
      return;
    }
    if (pacer.pending) ensureFrame();
  });

  // Keep the caret on the text end when the column reflows (resize, sidebar toggle).
  createEffect(() => {
    if (!props.streaming || !container || typeof ResizeObserver !== "function") return;
    const observer = new ResizeObserver(() => caret.place(container));
    observer.observe(container);
    ensureFrame();
    onCleanup(() => observer.disconnect());
  });

  onCleanup(() => {
    cancelFrame();
    painter.dispose();
  });

  const reveal: StreamReveal = {
    enabled: revealEnabled,
    source: displayed,
    track: painter.track,
  };

  const blocks = createMemo(() => parseIncremental(displayed()));

  return (
    <div
      ref={container}
      class={`relative flex flex-col text-[var(--text-primary)] selectable-text ${
        props.density === "compact" ? "gap-y-2 leading-[1.62]" : "gap-y-4 leading-[1.7]"
      }`}
      style={{ "font-size": "var(--app-chat-text-size)" }}
    >
      <MarkdownBlocks blocks={blocks()} density={props.density ?? "chat"} reveal={reveal} />
      <Show when={props.streaming}>
        <span ref={caret.ref} class="hoxi-stream-caret-anchor" aria-hidden="true">
          <span class="hoxi-stream-caret" />
        </span>
      </Show>
    </div>
  );
}
