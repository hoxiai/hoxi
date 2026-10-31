import { createEffect, onCleanup, onMount, untrack } from "solid-js";
import type { createTimelineScroll } from "./timeline-scroll";

type Position = { bottom: boolean; top: number; turnID?: string; offset: number };

// Positions belong to sessions, not the shared DOM scroller (which changes on every switch).
export function createSessionScrollController(options: {
  scroller: () => HTMLElement | undefined;
  scroll: ReturnType<typeof createTimelineScroll>;
  ensureTurnVisible: (id: string) => void;
  shouldFollowBottom?: (id: string) => boolean;
}) {
  const positions = new Map<string, Position>();
  let sessionID: string | null = null;
  let pending: Position | undefined;
  let ready = false;
  let hasTurns = false;
  let frame: number | undefined;
  let settling: number | undefined;

  const capture = () => {
    const el = options.scroller();
    if (!sessionID || pending || !el || el.clientHeight === 0) return;
    const isAtBottom =
      (options.scroll.isFollowing?.() ?? false) ||
      el.scrollHeight - el.scrollTop - el.clientHeight < 64;
    if (isAtBottom) {
      positions.set(sessionID, {
        bottom: true,
        top: el.scrollTop,
        offset: 0,
      });
      return;
    }
    const top = el.getBoundingClientRect().top;
    const anchor = Array.from(el.querySelectorAll<HTMLElement>("[data-turn-id]"))
      .find((node) => node.getBoundingClientRect().bottom > top);
    positions.set(sessionID, {
      bottom: false,
      top: el.scrollTop,
      turnID: anchor?.dataset.turnId,
      offset: anchor ? anchor.getBoundingClientRect().top - top : 0,
    });
  };

  const applyPending = (): boolean => {
    const el = options.scroller();
    const position = pending;
    if (!position || !el || el.clientHeight === 0) return false;
    if (position.bottom) {
      if (!ready && !hasTurns) return false;
      options.scroll.jumpToBottom();
      return ready;
    }
    if (!ready) return false;
    options.scroll.pause();
    const anchor = Array.from(el.querySelectorAll<HTMLElement>("[data-turn-id]"))
      .find((node) => node.dataset.turnId === position.turnID);
    el.scrollTop = anchor
      ? el.scrollTop + anchor.getBoundingClientRect().top - el.getBoundingClientRect().top - position.offset
      : position.top;
    options.scroll.updatePosition();
    return true;
  };

  const restore = () => {
    frame = undefined;
    const position = pending;
    if (!applyPending() || !position) return;
    if (position.bottom) {
      pending = undefined;
      capture();
      return;
    }
    // Allow the restored layout and the resulting resize to settle before recording.
    if (settling !== undefined) cancelAnimationFrame(settling);
    settling = requestAnimationFrame(() => {
      settling = requestAnimationFrame(() => {
        settling = undefined;
        pending = undefined;
        capture();
      });
    });
  };

  const scheduleRestore = () => {
    if (frame === undefined) frame = requestAnimationFrame(restore);
  };

  return {
    capture,
    isRestoring() {
      return Boolean(pending);
    },
    switchSession(id: string | null) {
      if (id === sessionID) return;
      if (frame !== undefined) cancelAnimationFrame(frame);
      if (settling !== undefined) cancelAnimationFrame(settling);
      frame = settling = undefined;
      sessionID = id;
      ready = false;
      hasTurns = false;
      const followBottom = Boolean(id && options.shouldFollowBottom?.(id));
      pending = followBottom
        ? { bottom: true, top: 0, offset: 0 }
        : positions.get(id || "") || { bottom: true, top: 0, offset: 0 };
      if (followBottom && id) {
        positions.set(id, pending);
      }
      if (pending.bottom) options.scroll.resume();
      else options.scroll.pause();
    },
    followToBottom() {
      if (frame !== undefined) cancelAnimationFrame(frame);
      if (settling !== undefined) cancelAnimationFrame(settling);
      frame = settling = undefined;
      pending = { bottom: true, top: 0, offset: 0 };
      if (sessionID) positions.set(sessionID, pending);
      options.scroll.resume();
      applyPending();
      if (ready) scheduleRestore();
    },
    ready(turnIDs: string[], loading: boolean) {
      ready = !loading;
      hasTurns = turnIDs.length > 0;
      if (!pending) return;
      if (!ready) {
        if (pending.bottom && hasTurns) applyPending();
        return;
      }
      if (pending.turnID && turnIDs.includes(pending.turnID)) {
        options.ensureTurnVisible(pending.turnID);
      }
      applyPending();
      scheduleRestore();
    },
    resized() {
      if (pending) {
        applyPending();
        if (ready) scheduleRestore();
      } else capture();
    },
    interrupt() {
      if (frame !== undefined) cancelAnimationFrame(frame);
      if (settling !== undefined) cancelAnimationFrame(settling);
      frame = settling = undefined;
      if (pending) {
        if (pending.bottom) options.scroll.resume();
        else options.scroll.pause();
      }
      pending = undefined;
    },
    dispose() {
      if (frame !== undefined) cancelAnimationFrame(frame);
      if (settling !== undefined) cancelAnimationFrame(settling);
    },
  };
}

export function createSessionScroll(options: {
  sessionID: () => string | null;
  turnIDs: () => string[];
  loading: () => boolean;
  content: () => HTMLElement | undefined;
  scroller: () => HTMLElement | undefined;
  scroll: ReturnType<typeof createTimelineScroll>;
  ensureTurnVisible: (id: string) => void;
  shouldFollowBottom?: (id: string) => boolean;
}) {
  const controller = createSessionScrollController(options);
  createEffect(() => {
    const id = options.sessionID();
    const ids = options.turnIDs();
    const loading = options.loading();
    untrack(() => {
      controller.switchSession(id);
      controller.ready(ids, loading);
    });
  });
  onMount(() => {
    const observer = new ResizeObserver(() => controller.resized());
    const content = options.content();
    const scroller = options.scroller();
    if (content) observer.observe(content);
    if (scroller) observer.observe(scroller);
    onCleanup(() => observer.disconnect());
  });
  onCleanup(controller.dispose);
  return controller;
}
