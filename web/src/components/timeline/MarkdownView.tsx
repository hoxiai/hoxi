import { createEffect, createMemo, createSignal, onCleanup, untrack } from "solid-js";
import { createIncrementalMarkdownParser } from "./markdown-parser";
import { createStreamPacer, holdIncompleteTableRows, initialRevealSlice, REVEAL_REPLAY_MAX_CHARS } from "./markdown/reveal";
import {
  prefersReducedMotion,
  StreamRevealContext,
  type StreamReveal,
} from "./stream-reveal";
import MarkdownBlocks from "./MarkdownBlocks";

export {
  advanceStreamingSlice,
  type AlertKind,
  type BlockToken,
  computeHeadingSlugList,
  extractMarkdownHeadings,
  type FrontmatterEntry,
  isLikelyFilePath,
  isSameBlockToken,
  type ListItemEntry,
  type MarkdownHeadingItem,
  parseInline,
  parseMarkdownBlocks,
  resolveMarkdownRelativePath,
  slugifyHeading,
  splitTableRow,
  stabilizeBlockToken,
  stripInlineMarkdownFormatting,
  type TableAlign,
} from "./markdown-parser";

const clock = () => (typeof performance !== "undefined" ? performance.now() : Date.now());
const isDocumentHidden = () =>
  typeof document !== "undefined" && document.visibilityState === "hidden";

/**
 * Renders markdown content incrementally.
 * When `streaming` is true (the live tail of a busy session), incoming chunks are
 * paced smoothly across animation frames and newly revealed text slices / blocks
 * softly fade from faint to full contrast (`StreamRevealContext`).
 * Historical turns, session switches, and non-streaming views render immediately.
 */
export default function MarkdownView(props: {
  content: string;
  density?: "chat" | "compact" | "document";
  streaming?: boolean;
  baseFilePath?: string;
}) {
  const parseIncremental = createIncrementalMarkdownParser();
  const canAnimate = typeof requestAnimationFrame === "function";
  // While streaming, a table row is revealed only once its whole line has arrived.
  const revealable = (content: string | undefined, streaming: boolean) => {
    const text = (content || "").replace(/\r\n/g, "\n");
    return streaming ? holdIncompleteTableRows(text) : text;
  };
  const initial = revealable(props.content, Boolean(props.streaming));
  const replay =
    Boolean(props.streaming) &&
    canAnimate &&
    !isDocumentHidden() &&
    !prefersReducedMotion() &&
    initial.length <= REVEAL_REPLAY_MAX_CHARS;
  // Replay starts from the first visible character, not empty: the view must
  // take up space in the same frame the waiting row above it disappears.
  const mounted = replay ? initialRevealSlice(initial) : initial;
  const pacer = createStreamPacer(mounted, clock());
  const [displayed, setDisplayed] = createSignal(mounted);
  const [revealActive, setRevealActive] = createSignal(replay);
  let rafId: number | undefined;

  const cancelFrame = () => {
    if (rafId !== undefined && typeof cancelAnimationFrame === "function") {
      cancelAnimationFrame(rafId);
    }
    rafId = undefined;
  };

  const snapToTarget = (streaming: boolean) => {
    cancelFrame();
    const full = pacer.target;
    pacer.reset(full, clock());
    setRevealActive(streaming && canAnimate && !isDocumentHidden() && !prefersReducedMotion());
    setDisplayed(full);
  };

  const frame = (ts: number) => {
    rafId = undefined;
    if (isDocumentHidden() || prefersReducedMotion()) {
      snapToTarget(Boolean(untrack(() => props.streaming)));
      return;
    }
    const prev = pacer.shown;
    const next = pacer.step(ts);
    if (next !== prev) {
      setRevealActive(true);
      setDisplayed(pacer.target.slice(0, next));
    }
    if (pacer.pending) {
      rafId = requestAnimationFrame(frame);
    } else if (!untrack(() => props.streaming)) {
      setRevealActive(false);
    }
  };

  const ensureFrame = () => {
    if (rafId === undefined && canAnimate) {
      rafId = requestAnimationFrame(frame);
    }
  };

  createEffect(() => {
    const streaming = Boolean(props.streaming);
    const rawText = (props.content || "").replace(/\r\n/g, "\n");
    let next = revealable(props.content, streaming);
    // Never let a transient table-row hold shrink the pacer target while raw
    // content is still growing; shrinking would reset the pacer and flash.
    if (
      streaming &&
      rawText.startsWith(pacer.target) &&
      pacer.target.startsWith(next) &&
      next.length < pacer.target.length
    ) {
      next = pacer.target;
    }
    const at = clock();
    const shouldPace =
      canAnimate &&
      !isDocumentHidden() &&
      !prefersReducedMotion() &&
      (streaming || pacer.pending || untrack(revealActive));

    if (!shouldPace || !pacer.push(next, at)) {
      cancelFrame();
      setRevealActive(false);
      pacer.reset(next, at);
      setDisplayed(next);
      if (streaming && canAnimate && !isDocumentHidden() && !prefersReducedMotion()) {
        setRevealActive(true);
      }
      return;
    }

    if (streaming) setRevealActive(true);
    if (pacer.pending) {
      ensureFrame();
    } else if (!streaming) {
      setRevealActive(false);
      setDisplayed(next);
    }
  });

  if (typeof document !== "undefined" && typeof document.addEventListener === "function") {
    const onVisibilityChange = () => {
      if (isDocumentHidden() && (pacer.pending || !untrack(() => props.streaming))) {
        snapToTarget(Boolean(untrack(() => props.streaming)));
      }
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    onCleanup(() => document.removeEventListener("visibilitychange", onVisibilityChange));
  }

  onCleanup(cancelFrame);

  const reveal: StreamReveal = {
    active: revealActive,
    baseFilePath: () => props.baseFilePath,
  };

  const blocks = createMemo(() => parseIncremental(displayed()));

  return (
    <StreamRevealContext.Provider value={reveal}>
      <div
        class={`flex flex-col text-[var(--text-primary)] selectable-text ${
          props.density === "compact"
            ? "gap-y-2 leading-[1.62]"
            : props.density === "document"
              ? "gap-y-4 leading-[1.75]"
              : "gap-y-4 leading-[1.7]"
        }`}
        style={{ "font-size": "var(--app-chat-text-size)" }}
      >
        <MarkdownBlocks blocks={blocks()} density={props.density ?? "chat"} />
      </div>
    </StreamRevealContext.Provider>
  );
}
