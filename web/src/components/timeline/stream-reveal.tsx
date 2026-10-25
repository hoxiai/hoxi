// web/src/components/timeline/stream-reveal.tsx
import {
  createContext,
  createRenderEffect,
  onCleanup,
  untrack,
  useContext,
} from "solid-js";
import { REVEAL_BLOCK_MS, REVEAL_CHUNK_MS } from "./markdown/reveal";

/**
 * Live-reveal state shared by a streaming `MarkdownView` with its descendant
 * blocks and inline text runs via `StreamRevealContext`.
 * Historical or non-streaming views leave `active()` false so they render
 * synchronously in a single text node with zero animation overhead.
 */
export interface StreamReveal {
  /** True while the view is actively pacing out newly arrived streamed content. */
  active: () => boolean;
}

export const StreamRevealContext = createContext<StreamReveal>();

const BLOCK_EASE = "cubic-bezier(0.22, 1, 0.36, 1)";

let reducedMotionQuery: MediaQueryList | undefined;

export function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return false;
  reducedMotionQuery ??= window.matchMedia("(prefers-reduced-motion: reduce)");
  return reducedMotionQuery.matches;
}

interface PendingChunk {
  el: HTMLSpanElement;
  text: string;
  done: boolean;
  timer: ReturnType<typeof setTimeout>;
}

/**
 * Renders an inline text run that appends in place.
 * - Outside a streaming `MarkdownView`, returns a single bare `Text` node.
 * - Inside a streaming `MarkdownView`, keeps settled text in a leading `Text`
 *   node (`prefixNode`) and wraps each newly revealed slice in a transient
 *   `<span class="hoxi-stream-chunk">` that softly fades from faint to solid
 *   (`opacity: 0.06 -> 0.42 -> 1` over `REVEAL_CHUNK_MS`) before merging in FIFO
 *   order into `prefixNode`.
 */
export function StreamRevealText(props: { text: string }) {
  const reveal = useContext(StreamRevealContext);
  const prefixNode = document.createTextNode("");

  if (!reveal) {
    createRenderEffect(() => {
      const text = props.text;
      if (text === prefixNode.data) return;
      if (text.startsWith(prefixNode.data)) {
        prefixNode.appendData(text.slice(prefixNode.length));
      } else {
        prefixNode.data = text;
      }
    });
    return prefixNode;
  }

  const host = document.createElement("span");
  host.appendChild(prefixNode);
  let shownText = "";
  const chunks: PendingChunk[] = [];

  const clearChunks = () => {
    for (const chunk of chunks) {
      clearTimeout(chunk.timer);
      chunk.el.remove();
    }
    chunks.length = 0;
  };

  const flushDoneChunks = () => {
    while (chunks.length > 0 && chunks[0].done) {
      const first = chunks.shift()!;
      clearTimeout(first.timer);
      prefixNode.appendData(first.text);
      first.el.remove();
    }
  };

  const appendFadingChunk = (delta: string) => {
    const el = document.createElement("span");
    el.className = "hoxi-stream-chunk";
    el.textContent = delta;
    const entry: PendingChunk = {
      el,
      text: delta,
      done: false,
      timer: setTimeout(() => {
        entry.done = true;
        flushDoneChunks();
      }, REVEAL_CHUNK_MS + 32),
    };
    el.addEventListener(
      "animationend",
      () => {
        entry.done = true;
        flushDoneChunks();
      },
      { once: true }
    );
    chunks.push(entry);
    host.appendChild(el);
  };

  createRenderEffect(() => {
    const next = props.text;
    if (next === shownText) return;
    const active = untrack(reveal.active);
    if (!active || prefersReducedMotion()) {
      clearChunks();
      if (next.startsWith(prefixNode.data)) {
        prefixNode.appendData(next.slice(prefixNode.length));
      } else {
        prefixNode.data = next;
      }
      shownText = next;
      return;
    }
    if (next.startsWith(shownText)) {
      const delta = next.slice(shownText.length);
      shownText = next;
      if (delta) appendFadingChunk(delta);
      return;
    }
    clearChunks();
    prefixNode.data = next;
    shownText = next;
  });

  onCleanup(clearChunks);
  return host;
}

export interface RevealBlockOptions {
  startOpacity?: number;
  translateY?: number;
  durationMs?: number;
}

/**
 * Softly fades and glides in a block or inline pill that mounted during live streaming.
 * Uses a negative delay so a remounted node resumes mid-fade instead of replaying.
 */
export function revealBlock(
  el: Element,
  at: number,
  options?: RevealBlockOptions
) {
  if (!at || typeof el.animate !== "function" || prefersReducedMotion()) return;
  const startOpacity = options?.startOpacity ?? 0;
  const translateY = options?.translateY ?? 2.5;
  const durationMs = options?.durationMs ?? REVEAL_BLOCK_MS;

  const start = () => {
    const age = performance.now() - at;
    if (age >= durationMs) return;
    const fromFrame: Keyframe =
      translateY !== 0
        ? { opacity: startOpacity, transform: `translate3d(0, ${translateY}px, 0)` }
        : { opacity: startOpacity };
    const toFrame: Keyframe =
      translateY !== 0
        ? { opacity: 1, transform: "translate3d(0, 0, 0)" }
        : { opacity: 1 };
    el.animate([fromFrame, toFrame], {
      duration: durationMs,
      delay: -Math.max(0, age),
      easing: BLOCK_EASE,
      fill: "backwards",
    });
  };
  // Solid clones nodes from an inert <template> document inside `ref` callbacks;
  // start the animation once the element is connected to the live document.
  if (el.isConnected) start();
  else queueMicrotask(() => el.isConnected && start());
}

/**
 * Softly fades in an inline pill (`<code>`, file chip) when it mounts during
 * active streaming.
 */
export function useInlineRevealRef() {
  const reveal = useContext(StreamRevealContext);
  let bornAt: number | undefined;
  return (el: Element) => {
    if (bornAt === undefined) {
      bornAt = reveal && untrack(reveal.active) ? performance.now() : 0;
    }
    if (bornAt > 0) {
      revealBlock(el, bornAt, {
        startOpacity: 0.1,
        translateY: 0,
        durationMs: REVEAL_CHUNK_MS,
      });
    }
  };
}

/**
 * Records per-index birth timestamps for a list of rendered blocks, list items,
 * or table rows. Items present when `reveal.active()` is false get `0` (no
 * animation); items appended while `reveal.active()` is true get `performance.now()`.
 */
export function createRevealBirths(): (index: number) => number {
  const reveal = useContext(StreamRevealContext);
  const births: number[] = [];
  return (index: number) => {
    let at = births[index];
    if (at === undefined) {
      at = reveal && untrack(reveal.active) ? performance.now() : 0;
      births[index] = at;
    }
    return at;
  };
}
