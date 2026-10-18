import { createRoot } from "solid-js";
import { createReadingPositionTracker } from "../components/timeline/reading-position";
import { assertEqual, describe, it } from "./test-harness";

function turn(id: string, top: number, bottom: number) {
  return {
    dataset: { turnId: id },
    getBoundingClientRect: () => ({ top, bottom, height: bottom - top }),
  } as unknown as HTMLElement;
}

function withTracker(run: (context: {
  tracker: ReturnType<typeof createReadingPositionTracker>;
  scroller: { scrollTop: number; scrollHeight: number; clientHeight: number };
  nodes: HTMLElement[];
  frames: Map<number, () => void>;
  flush: () => void;
  resize: () => void;
  dispose: () => void;
  observed: unknown[];
  disconnected: () => boolean;
}) => void) {
  const g = globalThis as Record<string, unknown>;
  const originals = {
    requestAnimationFrame: g.requestAnimationFrame,
    cancelAnimationFrame: g.cancelAnimationFrame,
    ResizeObserver: g.ResizeObserver,
  };
  let nextFrame = 0;
  const frames = new Map<number, () => void>();
  let resize = () => {};
  let disconnected = false;
  const observed: unknown[] = [];
  g.requestAnimationFrame = (cb: () => void) => {
    frames.set(++nextFrame, cb);
    return nextFrame;
  };
  g.cancelAnimationFrame = (id: number) => frames.delete(id);
  g.ResizeObserver = class {
    constructor(cb: () => void) { resize = cb; }
    observe(target: unknown) { observed.push(target); }
    disconnect() { disconnected = true; }
  };
  const nodes: HTMLElement[] = [];
  const content = {} as HTMLElement;
  const scroller = {
    scrollTop: 200,
    scrollHeight: 1000,
    clientHeight: 400,
    getBoundingClientRect: () => ({ top: 100 }),
    querySelectorAll: (selector: string) => {
      assertEqual(selector, "[data-turn-id]");
      return nodes;
    },
  };
  let dispose = () => {};
  try {
    const tracker = createRoot((cleanup) => {
      dispose = cleanup;
      return createReadingPositionTracker({
        scroller: () => scroller as unknown as HTMLElement,
        content: () => content,
        sessionID: () => "session-1",
        assistantTurnIDs: () => ["assistant-1", "assistant-2"],
      });
    });
    tracker.observe();
    assertEqual(observed[0], content);
    run({
      tracker, scroller, nodes, frames, observed, dispose,
      resize: () => resize(),
      disconnected: () => disconnected,
      flush: () => {
        const pending = [...frames.values()];
        frames.clear();
        pending.forEach((cb) => cb());
      },
    });
  } finally {
    dispose();
    Object.assign(g, originals);
  }
}

describe("Timeline reading position", () => {
  it("selects the newest assistant at the bottom, ignoring a trailing user turn", () => {
    withTracker(({ tracker, scroller, nodes, flush }) => {
      nodes.push(turn("assistant-1", 200, 300), turn("assistant-2", 500, 600), turn("user-1", 600, 700));
      scroller.scrollTop = 598; // Within the two-pixel bottom tolerance.
      flush();
      assertEqual(tracker.readingTurnID(), "assistant-2");
    });
  });

  it("selects the assistant containing the reading point, ignoring a containing user turn", () => {
    withTracker(({ tracker, nodes, flush }) => {
      // Reading point: scroller top 100 + 35% of its 400px height = 240.
      nodes.push(turn("user-1", 230, 250), turn("assistant-1", 100, 180), turn("assistant-2", 200, 400));
      flush();
      assertEqual(tracker.readingTurnID(), "assistant-2");
    });
  });

  it("selects the nearest assistant center in a gap and clears when only user turns remain", () => {
    withTracker(({ tracker, nodes, flush, resize }) => {
      nodes.push(turn("assistant-1", 0, 200), turn("user-1", 230, 250), turn("assistant-2", 260, 280));
      flush();
      assertEqual(tracker.readingTurnID(), "assistant-2");
      nodes.splice(0, nodes.length, turn("user-1", 230, 250));
      resize();
      flush();
      assertEqual(tracker.readingTurnID(), null);
    });
  });

  it("coalesces scheduled updates and cancels pending work and observation on cleanup", () => {
    withTracker(({ tracker, nodes, frames, flush, resize, dispose, disconnected }) => {
      nodes.push(turn("assistant-1", 200, 300));
      flush();
      assertEqual(tracker.readingTurnID(), "assistant-1");
      tracker.schedule();
      tracker.schedule();
      resize();
      assertEqual(frames.size, 1);
      nodes.splice(0);
      dispose();
      assertEqual(disconnected(), true);
      assertEqual(frames.size, 0);
      flush();
      assertEqual(tracker.readingTurnID(), "assistant-1");
    });
  });
});
