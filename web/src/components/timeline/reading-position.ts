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
    let selected = nodes.find((node) => {
      const rect = node.getBoundingClientRect();
      return rect.top <= point && point <= rect.bottom;
    });
    if (!selected) {
      selected = nodes.reduce((nearest, node) => {
        const rect = node.getBoundingClientRect();
        const nearestRect = nearest.getBoundingClientRect();
        return Math.abs(rect.top + rect.height / 2 - point) <
            Math.abs(nearestRect.top + nearestRect.height / 2 - point)
          ? node
          : nearest;
      });
    }
    setReadingTurnID(selected.dataset.turnId || null);
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

  createEffect(() => {
    options.sessionID();
    options.assistantTurnIDs();
    setReadingTurnID(null);
    schedule();
  });

  onCleanup(() => {
    if (frame !== undefined) cancelAnimationFrame(frame);
    observer?.disconnect();
  });

  return { readingTurnID, observe, schedule };
}
