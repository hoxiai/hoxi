import { createSignal } from "solid-js";

// Distance at which the jump-to-bottom button appears.
const AWAY_THRESHOLD = 64;
// A reader re-joins the live tail only once they are actually back at the bottom;
// a looser threshold snapped small upward scrolls straight back down.
const REFOLLOW_THRESHOLD = 4;
// Without pointer events (WebKit scrollbar drags) only a clear upward read counts:
// a shrink-clamp race leaves at most one frame of new output below the viewport.
const READ_AWAY_THRESHOLD = 240;

// Keep following layout changes without pulling readers away from older messages.
export function createTimelineScroll(
  getScroller: () => HTMLDivElement | undefined,
  enabled: () => boolean,
) {
  let following = true;
  let frame: number | undefined;
  let previousScrollTop = 0;
  // Scrollbar drags and touch scrolls emit only scroll events, so upward movement
  // counts as reader intent only while one of them is in progress.
  let pointerScrolling = false;
  const [awayFromBottom, setAwayFromBottom] = createSignal(false);

  const distanceFromBottom = (scroller: HTMLElement) =>
    scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight;

  const syncOverflowAnchor = (scroller?: HTMLElement) => {
    const el = scroller ?? getScroller();
    if (!el?.style) return;
    // Disable native browser scroll anchoring while JS auto-follow is active so
    // WebKit doesn't fight ResizeObserver's jumpToBottom() during streaming;
    // re-enable it when the reader scrolls up so their reading position stays anchored.
    el.style.overflowAnchor = enabled() && following ? "none" : "auto";
  };

  const updatePosition = () => {
    const scroller = getScroller();
    if (scroller && scroller.clientHeight > 0) {
      setAwayFromBottom(distanceFromBottom(scroller) >= AWAY_THRESHOLD);
    }
    syncOverflowAnchor(scroller);
  };

  const jumpToBottom = () => {
    const scroller = getScroller();
    if (!scroller || scroller.clientHeight === 0) return;
    following = true;
    syncOverflowAnchor(scroller);
    scroller.scrollTop = scroller.scrollHeight;
    previousScrollTop = scroller.scrollTop;
    updatePosition();
  };

  const smoothToBottom = () => {
    const scroller = getScroller();
    if (!scroller || scroller.clientHeight === 0) return;
    following = true;
    syncOverflowAnchor(scroller);
    previousScrollTop = scroller.scrollTop;
    scroller.scrollTo({ top: scroller.scrollHeight, behavior: "smooth" });
    updatePosition();
  };

  const schedule = () => {
    syncOverflowAnchor();
    if (!enabled() || !following || frame !== undefined) return;
    frame = requestAnimationFrame(() => {
      frame = undefined;
      const scroller = getScroller();
      if (!scroller || !enabled() || !following || scroller.clientHeight === 0) return;
      jumpToBottom();
    });
  };

  return {
    schedule,
    awayFromBottom,
    updatePosition,
    jumpToBottom,
    smoothToBottom,
    resume() {
      following = true;
      syncOverflowAnchor();
      schedule();
    },
    pause() {
      following = false;
      syncOverflowAnchor();
      if (frame !== undefined) cancelAnimationFrame(frame);
      frame = undefined;
    },
    // Wheel and keyboard intent call pause() directly, before their async scroll lands.
    setPointerScrolling(active: boolean) {
      pointerScrolling = active;
    },
    onScroll() {
      const scroller = getScroller();
      if (!scroller || scroller.clientHeight === 0) return false;
      // Upward movement alone is not reader intent: when content shrinks the
      // browser clamps scrollTop upward, and if more output lands before the
      // scroll event is dispatched it looks exactly like scrolling away.
      const movedUp = scroller.scrollTop < previousScrollTop - 1;
      previousScrollTop = scroller.scrollTop;
      const distance = distanceFromBottom(scroller);
      if (distance <= REFOLLOW_THRESHOLD) following = true;
      else if (movedUp && (pointerScrolling || distance > READ_AWAY_THRESHOLD)) following = false;
      updatePosition();
      return !following;
    },
    observe(content: HTMLDivElement) {
      const observer = new ResizeObserver(() => {
        if (enabled() && following) {
          // ResizeObserver runs before paint; follow now instead of one frame later.
          if (frame !== undefined) cancelAnimationFrame(frame);
          frame = undefined;
          jumpToBottom();
        } else {
          updatePosition();
        }
      });
      observer.observe(content);
      const scroller = getScroller();
      if (scroller) {
        syncOverflowAnchor(scroller);
        observer.observe(scroller);
      }
      schedule();
      return () => {
        observer.disconnect();
        if (frame !== undefined) cancelAnimationFrame(frame);
      };
    },
  };
}
