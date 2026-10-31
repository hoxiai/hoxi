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
      assertEqual(controller.isRestoring(), true);
      controller.ready(["old-turn"], false);
      // Scroll position must be applied synchronously on ready() before any frame paints.
      assertEqual(el.scrollTop, 1200);
      assertEqual(scroll.awayFromBottom(), false);
      flush();
      assertEqual(el.scrollTop, 1200);
      assertEqual(controller.isRestoring(), false);

      el.scrollTop = 350;
      scroll.onScroll();
      controller.capture();
      controller.switchSession("B");
      el.scrollTop = 0;
      controller.ready([], false);
      assertEqual(el.scrollTop, 1200);
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
      assertEqual(el.scrollTop, 550);
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

  it("jumps directly to the live bottom when switching into or re-selecting an active-task session", () => {
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
      const anchor = {
        dataset: { turnId: "old-turn" },
        getBoundingClientRect: (): { top: number; bottom: number } => ({ top: 300 - el.scrollTop, bottom: 700 - el.scrollTop }),
      };
      let scrollTop = 0;
      const el = {
        get scrollTop() { return scrollTop; },
        set scrollTop(value: number) { scrollTop = Math.max(0, Math.min(value, this.scrollHeight - this.clientHeight)); },
        scrollHeight: 1600,
        clientHeight: 400,
        getBoundingClientRect: () => ({ top: 0 }),
        querySelectorAll: () => [anchor],
      };
      const busySessions = new Set<string>();
      const scroll = createTimelineScroll(() => el as unknown as HTMLDivElement, () => true);
      const controller = createSessionScrollController({
        scroller: () => el as unknown as HTMLElement,
        scroll,
        ensureTurnVisible: () => {},
        shouldFollowBottom: (id) => busySessions.has(id),
      });

      // User scrolls up in session A and switches to session B.
      controller.switchSession("A");
      controller.ready(["old-turn"], false);
      flush();
      el.scrollTop = 250;
      scroll.onScroll();
      controller.capture();
      assertEqual(scroll.isFollowing(), false);

      controller.switchSession("B");
      controller.ready([], false);
      flush();

      // Session A is now executing a task. Switching back into A must ignore the
      // stale historical scroll offset and jump straight to the active tail.
      busySessions.add("A");
      el.scrollHeight = 2000;
      controller.switchSession("A");
      controller.ready(["old-turn", "live-turn"], false);
      assertEqual(el.scrollTop, 1600);
      assertEqual(scroll.isFollowing(), true);
      assertEqual(scroll.awayFromBottom(), false);
      flush();

      // While staying in busy session A, user scrolls up to inspect history,
      // then clicks session A's title again (`followToBottom`).
      el.scrollTop = 300;
      scroll.onScroll();
      controller.capture();
      assertEqual(scroll.isFollowing(), false);
      controller.followToBottom();
      assertEqual(el.scrollTop, 1600);
      assertEqual(scroll.isFollowing(), true);
      flush();

      // Switching into a background-busy session D that has 1 partial SSE turn
      // while full history is still hydrating (`loading === true`) must pin the
      // partial turn immediately and re-pin once full hydration completes.
      busySessions.add("D");
      el.scrollHeight = 600;
      el.scrollTop = 0;
      controller.switchSession("D");
      controller.ready(["partial-sse"], true);
      assertEqual(el.scrollTop, 200);
      assertEqual(controller.isRestoring(), true);
      el.scrollHeight = 2400;
      controller.ready(["h1", "h2", "partial-sse"], false);
      assertEqual(el.scrollTop, 2000);
      flush();
      assertEqual(controller.isRestoring(), false);
      assertEqual(scroll.isFollowing(), true);

      controller.dispose();
    } finally {
      globalThis.requestAnimationFrame = originalRAF;
      globalThis.cancelAnimationFrame = originalCancel;
    }
  });
});
