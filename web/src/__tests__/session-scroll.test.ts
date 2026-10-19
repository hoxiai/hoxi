import { createSessionScrollController } from "../components/timeline/session-scroll";
import { createTimelineScroll } from "../components/timeline/timeline-scroll";
import { assertEqual, describe, it } from "./test-harness";

describe("Session reading restoration", () => {
  it("restores historical anchors, follows latest sessions, and cancels stale switches", () => {
    const originalRAF = globalThis.requestAnimationFrame;
    const originalCancel = globalThis.cancelAnimationFrame;
    let next = 0;
    const frames = new Map<number, FrameRequestCallback>();
    globalThis.requestAnimationFrame = (cb) => { frames.set(++next, cb); return next; };
    globalThis.cancelAnimationFrame = (id) => { frames.delete(id); };
    const flush = () => {
      for (let i = 0; frames.size && i < 10; i++) {
        const pending = [...frames.values()];
        frames.clear();
        pending.forEach((cb) => cb(0));
      }
    };
    try {
      let anchorTop = 300;
      let anchorVisible = true;
      const anchor = {
        dataset: { turnId: "old-turn" },
        getBoundingClientRect: (): { top: number; bottom: number } => ({ top: anchorTop - el.scrollTop, bottom: anchorTop + 400 - el.scrollTop }),
      };
      let scrollTop = 0;
      const el = {
        get scrollTop() { return scrollTop; },
        set scrollTop(value: number) { scrollTop = Math.max(0, Math.min(value, this.scrollHeight - this.clientHeight)); },
        scrollHeight: 1600,
        clientHeight: 400,
        getBoundingClientRect: () => ({ top: 0 }),
        querySelectorAll: () => anchorVisible ? [anchor] : [],
      };
      const scroll = createTimelineScroll(() => el as unknown as HTMLDivElement, () => true);
      let revealed = "";
      const controller = createSessionScrollController({
        scroller: () => el as unknown as HTMLElement,
        scroll,
        ensureTurnVisible: (id) => { revealed = id; anchorVisible = true; },
      });
      controller.switchSession("A");
      controller.ready(["old-turn"], false);
      flush();
      assertEqual(el.scrollTop, 1200);

      el.scrollTop = 350;
      scroll.onScroll();
      controller.capture();
      controller.switchSession("B");
      el.scrollTop = 0;
      controller.ready([], false);
      flush();
      assertEqual(el.scrollTop, 1200);

      controller.switchSession("A");
      anchorVisible = false;
      anchorTop = 500; // Layout above the anchor changed while the session was hidden.
      el.scrollTop = 0;
      controller.ready([], true);
      controller.resized();
      flush();
      assertEqual(el.scrollTop, 0);
      controller.ready(["old-turn"], false);
      assertEqual(revealed, "old-turn");
      flush();
      assertEqual(el.scrollTop, 550);
      assertEqual(scroll.awayFromBottom(), true);
      scroll.schedule(); // Background updates must not restart following.
      flush();
      assertEqual(el.scrollTop, 550);

      controller.switchSession("B");
      controller.ready([], false);
      el.scrollHeight = 2000;
      flush();
      assertEqual(el.scrollTop, 1600);

      controller.switchSession("A");
      controller.ready(["old-turn"], false);
      controller.switchSession("C");
      controller.ready([], false);
      flush();
      assertEqual(el.scrollTop, 1600);

      controller.switchSession("A");
      controller.ready(["old-turn"], false);
      controller.interrupt();
      el.scrollTop = 100;
      flush();
      assertEqual(el.scrollTop, 100);

      // Switching to a bottom-following session and resizing/interrupting
      // during streaming must never pause auto-follow.
      controller.switchSession("C");
      controller.ready([], false);
      flush();
      assertEqual(scroll.isFollowing(), true);
      el.scrollHeight = 2400; // Grows by >64px before scroll ResizeObserver runs
      controller.resized();
      controller.interrupt();
      assertEqual(scroll.isFollowing(), true);

      controller.dispose();
      assertEqual(frames.size, 0);
    } finally {
      globalThis.requestAnimationFrame = originalRAF;
      globalThis.cancelAnimationFrame = originalCancel;
    }
  });
});
