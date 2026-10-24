import { createEffect, createRenderEffect, onCleanup, onMount, untrack } from "solid-js";
import {
  pruneRevealChunks,
  REVEAL_BLOCK_MS,
  REVEAL_LEVELS,
  type RevealChunk,
  revealSegments,
} from "./markdown/reveal";

/**
 * Live-reveal state a streaming `MarkdownView` shares with its blocks. Effects only
 * apply to text revealed while the view streams; history renders exactly as before.
 */
export interface StreamReveal {
  /** True once the view has streamed live (and motion is allowed). */
  enabled: () => boolean;
  /** The displayed, normalized markdown source the blocks were parsed from. */
  source: () => string;
  /** Registers a rendered text node whose text starts at absolute source offset `start()`. */
  track: (node: Text, start: () => number) => () => void;
}

const BLOCK_EASE = "cubic-bezier(0.16, 1, 0.3, 1)";

let reducedMotionQuery: MediaQueryList | undefined;

export function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return false;
  reducedMotionQuery ??= window.matchMedia("(prefers-reduced-motion: reduce)");
  return reducedMotionQuery.matches;
}

function supportsHighlights(): boolean {
  return (
    typeof CSS !== "undefined" &&
    "highlights" in CSS &&
    typeof Highlight === "function" &&
    typeof StaticRange === "function"
  );
}

let trailHighlights: Highlight[] | undefined;

// One shared highlight per trail level (`::highlight(hoxi-reveal-N)` in transitions.css);
// each painter adds and removes only its own ranges.
function revealHighlights(): Highlight[] {
  if (!trailHighlights) {
    trailHighlights = Array.from({ length: REVEAL_LEVELS }, (_, level) => {
      const highlight = new Highlight();
      CSS.highlights.set(`hoxi-reveal-${level}`, highlight);
      return highlight;
    });
  }
  return trailHighlights;
}

/**
 * Paints the light trail over freshly revealed text with the CSS Custom Highlight API:
 * no extra DOM, no compositing layers and no change to text shaping, so the text
 * itself never moves or blinks — only its color is stepped through the trail levels.
 */
export function createRevealPainter() {
  const supported = supportsHighlights();
  const tracked = new Map<Text, () => number>();
  let chunks: readonly RevealChunk[] = [];
  let owned: Array<[Highlight, StaticRange]> = [];

  const clearOwned = () => {
    for (const [highlight, range] of owned) highlight.delete(range);
    owned = [];
  };

  return {
    track(node: Text, start: () => number) {
      tracked.set(node, start);
      return () => tracked.delete(node);
    },
    push(chunk: RevealChunk) {
      if (supported) chunks = [...chunks, chunk];
    },
    /** Repaints the trail; returns true while some text is still glowing. */
    paint(now: number): boolean {
      if (!supported) return false;
      clearOwned();
      chunks = pruneRevealChunks(chunks, now);
      if (chunks.length === 0) return false;
      const levels = revealHighlights();
      for (const [node, start] of tracked) {
        if (!node.isConnected) continue;
        for (const seg of revealSegments(chunks, untrack(start), node.length, now)) {
          const range = new StaticRange({
            startContainer: node,
            startOffset: seg.from,
            endContainer: node,
            endOffset: seg.to,
          });
          levels[seg.level].add(range);
          owned.push([levels[seg.level], range]);
        }
      }
      return true;
    },
    clear() {
      clearOwned();
      chunks = [];
    },
    dispose() {
      clearOwned();
      chunks = [];
      tracked.clear();
    },
  };
}

/**
 * Plain text run of a streaming block. Renders a single stable text node (like `{text}`)
 * and registers it with the painter so its freshly revealed characters get the trail.
 */
export function StreamRevealText(props: { text: string; start: number; reveal?: StreamReveal }) {
  const node = document.createTextNode("");
  createRenderEffect(() => {
    node.data = props.text;
  });
  createEffect(() => {
    const reveal = props.reveal;
    if (!reveal) return;
    onCleanup(reveal.track(node, () => props.start));
  });
  return node;
}

/**
 * Fades in a block (code card, table, list marker) that mounted while streaming. Uses a
 * negative delay so a remounted node resumes mid-fade instead of replaying it.
 */
export function revealBlock(el: Element, at: number) {
  if (!at || typeof el.animate !== "function") return;
  const start = () => {
    const age = performance.now() - at;
    if (age >= REVEAL_BLOCK_MS) return;
    el.animate([{ opacity: 0 }, { opacity: 1 }], {
      duration: REVEAL_BLOCK_MS,
      delay: -Math.max(0, age),
      easing: BLOCK_EASE,
      fill: "backwards",
    });
  };
  // Solid clones nodes from <template> content, so inside a `ref` callback the element
  // still belongs to the template's inert document, whose timeline never advances: an
  // animation started there freezes on its first keyframe (invisible). Start it once
  // the node is adopted into the page, which happens later in the same task.
  if (el.isConnected) start();
  else queueMicrotask(() => el.isConnected && start());
}

/**
 * Per-index mount timestamps for a list of rendered items. Items present at the
 * first render (history) get `inherit()` (0 = no animation); items appended later
 * while the view streams get the current time. A remounted item keeps its stamp.
 */
export function createRevealBirths(
  reveal: () => StreamReveal | undefined,
  inherit: () => number = () => 0
): (index: number) => number {
  const births: number[] = [];
  let primed = false;
  onMount(() => {
    primed = true;
  });
  return (index) => {
    let at = births[index];
    if (at === undefined) {
      const current = untrack(reveal);
      at = !primed ? untrack(inherit) : current && untrack(current.enabled) ? performance.now() : 0;
      births[index] = at;
    }
    return at;
  };
}

function lastTextEnd(root: Element, skip: Element | undefined): DOMRect | undefined {
  const accept = (node: Node) =>
    skip?.contains(node) || !node.nodeValue?.trim()
      ? NodeFilter.FILTER_REJECT
      : NodeFilter.FILTER_ACCEPT;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, { acceptNode: accept });
  // Walk backwards from the deepest last node: O(1) for the usual case instead of
  // visiting every text node of a long reply on each frame.
  let deepest: Node = root;
  while (deepest.lastChild) deepest = deepest.lastChild;
  walker.currentNode = deepest;
  const last =
    deepest.nodeType === Node.TEXT_NODE && accept(deepest) === NodeFilter.FILTER_ACCEPT
      ? deepest
      : walker.previousNode();
  if (!last) return undefined;
  const text = last.nodeValue ?? "";
  const end = text.trimEnd().length;
  const range = document.createRange();
  range.setStart(last, Math.max(0, end - 1));
  range.setEnd(last, end);
  const rects = range.getClientRects();
  return rects[rects.length - 1];
}

/**
 * The glowing dot that leads the text. It is absolutely positioned, so it never takes
 * part in line layout; it glides along a line and jumps when the text wraps.
 */
export function createStreamCaret() {
  let anchor: HTMLSpanElement | undefined;
  let lastY = Number.NaN;
  return {
    ref(el: HTMLSpanElement) {
      anchor = el;
      lastY = Number.NaN;
    },
    place(container: HTMLElement | undefined) {
      if (!anchor || !container) return;
      const end = lastTextEnd(container, anchor);
      if (!end) {
        anchor.style.opacity = "0";
        return;
      }
      const box = container.getBoundingClientRect();
      const x = end.right - box.left;
      const y = end.top - box.top + end.height / 2;
      const wrapped = Number.isNaN(lastY) || Math.abs(y - lastY) > 2;
      anchor.style.transition = wrapped ? "opacity 200ms ease-out" : "";
      anchor.style.transform = `translate3d(${x.toFixed(1)}px, ${y.toFixed(1)}px, 0)`;
      anchor.style.opacity = "1";
      lastY = y;
    },
  };
}
