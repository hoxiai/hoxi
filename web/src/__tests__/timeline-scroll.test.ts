import { createTimelineScroll } from "../components/timeline/timeline-scroll";
import { assertEqual, describe, it } from "./test-harness";

describe("Timeline layout following", () => {
  it("follows resize and visibility changes, respects readers, and cancels pending work", () => {
    const g = globalThis as Record<string, unknown>;
    const originals = {
      requestAnimationFrame: g.requestAnimationFrame,
      cancelAnimationFrame: g.cancelAnimationFrame,
      ResizeObserver: g.ResizeObserver,
    };
    let nextFrame = 0;
    const frames = new Map<number, () => void>();
    let resized = () => {};
    let disconnected = false;
    const observed: unknown[] = [];
    g.requestAnimationFrame = (cb: () => void) => {
      frames.set(++nextFrame, cb);
      return nextFrame;
    };
    g.cancelAnimationFrame = (id: number) => frames.delete(id);
    g.ResizeObserver = class {
      constructor(cb: () => void) { resized = cb; }
      observe(target: unknown) { observed.push(target); }
      disconnect() { disconnected = true; }
    };
    const flush = () => {
      const pending = [...frames.values()];
      frames.clear();
      pending.forEach((cb) => cb());
    };
    try {
      const scroller = { scrollTop: 0, scrollHeight: 1000, clientHeight: 400 };
      let enabled = true;
      const scroll = createTimelineScroll(() => scroller as HTMLDivElement, () => enabled);
      const dispose = scroll.observe({} as HTMLDivElement);
      assertEqual(observed.length, 2);
      scroll.resume();
      resized();
      assertEqual(frames.size, 1);
      flush();
      assertEqual(scroller.scrollTop, 1000);
      flush();

      // Tool output or an image grows without changing message/part counts.
      scroller.scrollHeight = 1200;
      resized();
      flush();
      assertEqual(scroller.scrollTop, 1200);
      flush();

      scroller.scrollTop = 200;
      assertEqual(scroll.onScroll(), true);
      resized();
      assertEqual(frames.size, 0);
      assertEqual(scroller.scrollTop, 200);

      // A session switch resumes even when its text lengths equal the previous session.
      scroll.resume();
      flush();
      assertEqual(scroller.scrollTop, 1200);
      flush();

      scroller.clientHeight = 0;
      scroller.scrollTop = 0;
      scroller.scrollHeight = 1500;
      resized();
      flush();
      assertEqual(scroller.scrollTop, 0);
      assertEqual(scroll.onScroll(), false);
      scroller.clientHeight = 400;
      resized();
      flush();
      assertEqual(scroller.scrollTop, 1500);
      flush();

      scroll.schedule();
      scroll.pause();
      scroller.scrollTop = 100;
      flush();
      assertEqual(scroller.scrollTop, 100);
      enabled = false;
      scroll.resume();
      resized();
      assertEqual(frames.size, 0);
      enabled = true;
      scroll.schedule();
      dispose();
      assertEqual(disconnected, true);
      assertEqual(frames.size, 0);
    } finally {
      Object.assign(g, originals);
    }
  });
});
