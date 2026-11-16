import { createEffect, createSignal, onCleanup } from "solid-js";

type ReadingPositionOptions = {
  scroller: () => HTMLElement | undefined;
  content: () => HTMLElement | undefined;
  sessionID: () => string | null;
  assistantTurnIDs: () => string[];
};

export function createReadingPositionTracker(options: ReadingPositionOptions) {
  const [readingTurnID, setReadingTurnID] = createSignal<string | null>(null);
  let frame: number | undefined;
  let observer: ResizeObserver | undefined;
  let observedContent: HTMLElement | undefined;

  const update = () => {
    frame = undefined;
    const scroller = options.scroller();
    if (!scroller) return;

    const assistantIDs = new Set(options.assistantTurnIDs());
    const nodes = Array.from(scroller.querySelectorAll<HTMLElement>("[data-turn-id]")).filter(
      (node) => assistantIDs.has(node.dataset.turnId || "")
    );
    if (nodes.length === 0) {
      setReadingTurnID(null);
      return;
    }

    const atBottom = scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 2;
    if (atBottom) {
      setReadingTurnID(nodes[nodes.length - 1].dataset.turnId || null);
      return;
    }

    const point = scroller.getBoundingClientRect().top + scroller.clientHeight * 0.35;
    let selected: HTMLElement | undefined;
    let nearestNode = nodes[0];
    let nearestDist = Infinity;
    for (const node of nodes) {
      const rect = node.getBoundingClientRect();
      if (rect.top <= point && point <= rect.bottom) {
        selected = node;
        break;
      }
      const dist = Math.abs(rect.top + rect.height / 2 - point);
      if (dist < nearestDist) {
        nearestDist = dist;
        nearestNode = node;
      }
    }
    setReadingTurnID((selected ?? nearestNode).dataset.turnId || null);
  };

  const schedule = () => {
    if (frame !== undefined) return;
    frame = requestAnimationFrame(update);
  };

  const observe = () => {
    const content = options.content();
    if (content && content !== observedContent && typeof ResizeObserver !== "undefined") {
      observer?.disconnect();
      observer = new ResizeObserver(schedule);
      observer.observe(content);
      observedContent = content;
    }
    schedule();
    return () => {
      observer?.disconnect();
      observer = undefined;
      observedContent = undefined;
    };
  };

  let lastSessionID: string | null | undefined;
  createEffect(() => {
    const sid = options.sessionID();
    options.assistantTurnIDs();
    if (sid !== lastSessionID) {
      lastSessionID = sid;
      setReadingTurnID(null);
    }
    schedule();
  });

  onCleanup(() => {
    if (frame !== undefined) cancelAnimationFrame(frame);
    observer?.disconnect();
  });

  return { readingTurnID, observe, schedule };
}
