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
      let scrollTop = 0;
      const scroller = {
        get scrollTop() { return scrollTop; },
        set scrollTop(value: number) {
          scrollTop = Math.max(0, Math.min(value, this.scrollHeight - this.clientHeight));
        },
        scrollHeight: 1000,
        clientHeight: 400,
      };
      let enabled = true;
      const scroll = createTimelineScroll(() => scroller as HTMLDivElement, () => enabled);
      const dispose = scroll.observe({} as HTMLDivElement);
      assertEqual(observed.length, 2);
      scroll.resume();
      resized();
      assertEqual(scroller.scrollTop, 600);
      assertEqual(scroll.awayFromBottom(), false);
      assertEqual(frames.size, 0);
      flush();
      assertEqual(scroller.scrollTop, 600);
      assertEqual(scroll.awayFromBottom(), false);
      flush();

      // Tool output or an image grows without changing message/part counts.
      scroller.scrollHeight = 1200;
      resized();
      // The resize must be corrected before another animation frame is painted.
      assertEqual(scroller.scrollTop, 800);
      assertEqual(scroll.awayFromBottom(), false);
      flush();
      assertEqual(scroller.scrollTop, 800);
      flush();

      // Reasoning or tool previews can shrink as well as grow.
      scroller.scrollHeight = 1000;
      resized();
      assertEqual(scroller.scrollTop, 600);
      assertEqual(scroll.awayFromBottom(), false);
      flush();
      scroller.scrollHeight = 1200;
      resized();
      assertEqual(scroller.scrollTop, 800);
      flush();

      // Streaming growth can emit a scroll event before the resize callback.
      // Staying at the same offset must not be mistaken for reading history.
      scroller.scrollHeight = 1400;
      assertEqual(scroll.onScroll(), false);
      resized();
      assertEqual(scroller.scrollTop, 1000);
      flush();
      scroller.scrollHeight = 1200;
      resized();
      flush();

      scroller.scrollTop = 200;
      assertEqual(scroll.onScroll(), true);
      assertEqual(scroll.awayFromBottom(), true);
      resized();
      assertEqual(frames.size, 0);
      assertEqual(scroller.scrollTop, 200);

      // Explicitly returning to latest resumes following after reading history.
      scroll.resume();
      flush();
      assertEqual(scroller.scrollTop, 800);
      assertEqual(scroll.awayFromBottom(), false);
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
      assertEqual(scroller.scrollTop, 1100);
      flush();
      assertEqual(scroller.scrollTop, 1100);
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

      // Manual navigation works even with automatic following disabled.
      assertEqual(scroll.awayFromBottom(), true);
      scroll.jumpToBottom();
      assertEqual(scroller.scrollTop, 1100);
      assertEqual(scroll.awayFromBottom(), false);
      assertEqual(scroll.onScroll(), false);
      flush();
      // A scrollbar drag (pointer held) is reader intent even for small moves.
      scroll.setPointerScrolling(true);
      scroller.scrollTop = 1036; // Exactly 64px from the bottom.
      assertEqual(scroll.onScroll(), true);
      assertEqual(scroll.awayFromBottom(), true);
      // Hiding the button is not enough to re-follow: small upward reads must not
      // snap back on the next streamed frame.
      scroller.scrollTop = 1037;
      assertEqual(scroll.onScroll(), true);
      assertEqual(scroll.awayFromBottom(), false);
      scroller.scrollTop = 1096; // 4px from the bottom.
      assertEqual(scroll.onScroll(), false);

      // The user's upward scroll is honoured even while streaming re-anchors every frame.
      scroller.scrollTop = 1000;
      assertEqual(scroll.onScroll(), true);
      resized();
      assertEqual(scroller.scrollTop, 1000);
      scroll.setPointerScrolling(false);
      scroll.jumpToBottom();

      // A collapsing group clamps scrollTop upward; more output lands before the
      // scroll event is dispatched. That is not the reader scrolling away.
      enabled = true;
      scroller.scrollHeight = 1400;
      scroll.jumpToBottom();
      assertEqual(scroller.scrollTop, 1000);
      scroller.scrollHeight = 1250;
      scroller.scrollTop = 1000; // clamped to 850
      scroller.scrollHeight = 1290;
      assertEqual(scroll.onScroll(), false);
      resized();
      assertEqual(scroller.scrollTop, 890);
      // Without pointer events, a clear read-away still pauses following.
      scroller.scrollTop = 400;
      assertEqual(scroll.onScroll(), true);
      scroller.scrollHeight = 1500;
      resized();
      assertEqual(scroller.scrollTop, 400);
      scroller.scrollHeight = 1100;
      scroll.jumpToBottom();

      enabled = true;
      scroll.jumpToBottom();
      flush();
      scroller.scrollHeight = 1700;
      resized();
      assertEqual(scroller.scrollTop, 1300);
      flush();
      assertEqual(scroller.scrollTop, 1300);
      assertEqual(scroll.awayFromBottom(), false);
      flush();
      scroll.schedule();

      // Send Now starts following before removing the queued card. Its layout
      // shrink can emit a scroll event before ResizeObserver runs.
      scroller.scrollTop = 200;
      assertEqual(scroll.onScroll(), true);
      scroll.jumpToBottom();
      scroller.scrollHeight = 1400;
      scroller.scrollTop = 1000;
      assertEqual(scroll.onScroll(), false);
      resized();
      assertEqual(scroller.scrollTop, 1000);
      flush();
      scroller.scrollHeight = 1800;
      resized();
      assertEqual(scroller.scrollTop, 1400);
      flush();

      scroll.schedule();
      dispose();
      assertEqual(disconnected, true);
      assertEqual(frames.size, 0);
    } finally {
      Object.assign(g, originals);
    }
  });
});
