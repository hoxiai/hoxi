import { createSignal } from "solid-js";

// Distance at which the jump-to-bottom button appears.
const AWAY_THRESHOLD = 64;
// A reader re-joins the live tail once they reach the bottom threshold.
const REFOLLOW_THRESHOLD = 4;
// Distance when scrolling downward with wheel/trackpad within bottom padding to re-engage follow.
const DOWNWARD_REFOLLOW_THRESHOLD = 28;
// Without pointer events, only an explicit large upward scrollTop jump (>240px) counts as reading away.
const READ_AWAY_THRESHOLD = 240;
// Grace window after an explicit user scroll input (wheel/touch/key/scrollbar drag).
const USER_INPUT_GRACE_MS = 280;

/**
 * Returns true only when a primary-button pointerdown occurred on the scroller's
 * own scrollbar gutter (KiloCode `gutter()` pattern), never when clicking inside
 * transcript content, buttons, or cards.
 */
export function isScrollbarGutterPointerDown(
  scroller: HTMLElement,
  event: { button?: number; clientX?: number; clientY?: number; target?: EventTarget | null },
): boolean {
  if (typeof event.button === "number" && event.button !== 0) return false;
  if (event.target instanceof Node && event.target !== scroller) return false;
  const x = event.clientX;
  const y = event.clientY;
  if (typeof x !== "number" || typeof y !== "number" || !Number.isFinite(x) || !Number.isFinite(y)) {
    return false;
  }
  const rect = scroller.getBoundingClientRect();
  if (x < rect.left || x > rect.right || y < rect.top || y > rect.bottom) return false;
  const gutterWidth = scroller.offsetWidth - scroller.clientWidth;
  if (Number.isFinite(gutterWidth) && gutterWidth > 0) {
    return x >= rect.right - gutterWidth - 2;
  }
  // macOS overlay scrollbars report offsetWidth === clientWidth; clicks on the
  // overlay scrollbar track target `scroller` directly near the right edge.
  return x >= rect.right - 18;
}

/**
 * Returns true when a wheel gesture is either horizontal-dominant (e.g. scrolling
 * a wide code block/table on a trackpad) or consumed by a nested scrollable container
 * (`[data-scrollable]` or overflow-y/x auto/scroll) that can still scroll in that direction.
 */
export function isNestedScrollableWheelTarget(
  scroller: HTMLElement,
  target: EventTarget | null,
  deltaX: number,
  deltaY: number,
): boolean {
  if (Math.abs(deltaX) > Math.abs(deltaY)) return true;
  let node = target instanceof Element ? target : null;
  while (node && node !== scroller) {
    if (node instanceof HTMLElement) {
      const canScrollUp = deltaY < 0 && node.scrollTop > 0;
      const canScrollDown =
        deltaY > 0 && node.scrollTop + node.clientHeight < node.scrollHeight - 1;
      if (node.hasAttribute("data-scrollable") && node.scrollHeight > node.clientHeight + 1) {
        if (canScrollUp || canScrollDown) return true;
      }
      if (node.scrollHeight > node.clientHeight + 1 && (canScrollUp || canScrollDown)) {
        const style = window.getComputedStyle?.(node);
        const overflowY = style?.overflowY;
        if (overflowY === "auto" || overflowY === "scroll" || overflowY === "overlay") {
          return true;
        }
      }
    }
    node = node.parentElement;
  }
  return false;
}

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
  let lastUserInputAt = 0;
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
    lastUserInputAt = 0;
    syncOverflowAnchor(scroller);
    scroller.scrollTop = scroller.scrollHeight;
    previousScrollTop = scroller.scrollTop;
    updatePosition();
  };

  const smoothToBottom = () => {
    // Direct scrollTop assignment (KiloCode pattern) avoids WebKit's 300ms async
    // smooth scroll animation fighting live ResizeObserver/MutationObserver pins.
    jumpToBottom();
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
    isFollowing() {
      return following;
    },
    markUserScrollInput() {
      lastUserInputAt = Date.now();
    },
    resume() {
      following = true;
      lastUserInputAt = 0;
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
      if (active) lastUserInputAt = Date.now();
    },
    onScroll() {
      const scroller = getScroller();
      if (!scroller || scroller.clientHeight === 0) return false;
      // Upward movement alone is not reader intent: when content shrinks the
      // browser clamps scrollTop upward, and if more output lands before the
      // scroll event is dispatched it looks like distanceFromBottom grew.
      // Compare actual upward scrollTop delta (`upwardDelta`) rather than `distance`.
      const currentTop = scroller.scrollTop;
      const upwardDelta = previousScrollTop - currentTop;
      const movedUp = upwardDelta > 1;
      const movedDown = currentTop - previousScrollTop > 1;
      previousScrollTop = currentTop;
      const distance = distanceFromBottom(scroller);
      const hasRecentUserInput =
        pointerScrolling ||
        (lastUserInputAt > 0 && Date.now() - lastUserInputAt < USER_INPUT_GRACE_MS);

      if (
        distance <= REFOLLOW_THRESHOLD ||
        (!pointerScrolling && movedDown && distance <= DOWNWARD_REFOLLOW_THRESHOLD)
      ) {
        following = true;
        lastUserInputAt = 0;
      } else if (movedUp && (hasRecentUserInput || upwardDelta > READ_AWAY_THRESHOLD)) {
        following = false;
      } else if (following && enabled() && !hasRecentUserInput && distance > 0) {
        // Self-healing pin (KiloCode pattern): if a same-frame DOM swap clamped
        // scrollTop upward without user input, re-pin immediately before paint.
        scroller.scrollTop = scroller.scrollHeight;
        previousScrollTop = scroller.scrollTop;
      }
      updatePosition();
      return !following;
    },
    observe(content: HTMLDivElement) {
      const pinOrUpdate = () => {
        if (enabled() && following && !pointerScrolling) {
          // ResizeObserver and MutationObserver run before paint; follow now.
          if (frame !== undefined) cancelAnimationFrame(frame);
          frame = undefined;
          jumpToBottom();
        } else {
          updatePosition();
        }
      };
      const observer = new ResizeObserver(pinOrUpdate);
      observer.observe(content);
      const scroller = getScroller();
      if (scroller) {
        syncOverflowAnchor(scroller);
        observer.observe(scroller);
      }
      let mutationObserver: MutationObserver | undefined;
      if (typeof MutationObserver === "function" && typeof Node !== "undefined" && content instanceof Node) {
        mutationObserver = new MutationObserver(pinOrUpdate);
        mutationObserver.observe(content, {
          childList: true,
          subtree: true,
          characterData: true,
        });
      }
      schedule();
      return () => {
        observer.disconnect();
        mutationObserver?.disconnect();
        if (frame !== undefined) cancelAnimationFrame(frame);
      };
    },
  };
}
