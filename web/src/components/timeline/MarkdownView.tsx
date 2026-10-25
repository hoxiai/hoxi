import { createEffect, createMemo, createSignal, onCleanup, untrack } from "solid-js";
import { createIncrementalMarkdownParser } from "./markdown-parser";
import { createStreamPacer, REVEAL_REPLAY_MAX_CHARS } from "./markdown/reveal";
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
  density?: "chat" | "compact";
  streaming?: boolean;
}) {
  const parseIncremental = createIncrementalMarkdownParser();
  const canAnimate = typeof requestAnimationFrame === "function";
  const initial = (props.content || "").replace(/\r\n/g, "\n");
  const replay =
    Boolean(props.streaming) &&
    canAnimate &&
    !isDocumentHidden() &&
    !prefersReducedMotion() &&
    initial.length <= REVEAL_REPLAY_MAX_CHARS;
  const pacer = createStreamPacer(replay ? "" : initial, clock());
  const [displayed, setDisplayed] = createSignal(replay ? "" : initial);
  const [revealActive, setRevealActive] = createSignal(false);
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
    setDisplayed(full);
    setRevealActive(streaming && canAnimate && !isDocumentHidden() && !prefersReducedMotion());
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
    const next = (props.content || "").replace(/\r\n/g, "\n");
    const streaming = Boolean(props.streaming);
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
      setDisplayed(next);
      setRevealActive(false);
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
  };

  const blocks = createMemo(() => parseIncremental(displayed()));

  return (
    <StreamRevealContext.Provider value={reveal}>
      <div
        class={`flex flex-col text-[var(--text-primary)] selectable-text ${
          props.density === "compact" ? "gap-y-2 leading-[1.62]" : "gap-y-4 leading-[1.7]"
        }`}
        style={{ "font-size": "var(--app-chat-text-size)" }}
      >
        <MarkdownBlocks blocks={blocks()} density={props.density ?? "chat"} />
      </div>
    </StreamRevealContext.Provider>
  );
}
