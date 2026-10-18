import { createSignal } from "solid-js";

// Keep following layout changes without pulling readers away from older messages.
export function createTimelineScroll(
  getScroller: () => HTMLDivElement | undefined,
  enabled: () => boolean,
) {
  let following = true;
  let frame: number | undefined;
  let releaseFrame: number | undefined;
  let programmatic = false;
  const [awayFromBottom, setAwayFromBottom] = createSignal(false);

  const updatePosition = () => {
    const scroller = getScroller();
    if (scroller && scroller.clientHeight > 0) {
      setAwayFromBottom(scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight >= 64);
    }
  };

  const jumpToBottom = () => {
    const scroller = getScroller();
    if (!scroller || scroller.clientHeight === 0) return;
    following = true;
    programmatic = true;
    scroller.scrollTop = scroller.scrollHeight;
    updatePosition();
    if (releaseFrame !== undefined) cancelAnimationFrame(releaseFrame);
    releaseFrame = requestAnimationFrame(() => {
      releaseFrame = undefined;
      programmatic = false;
    });
  };

  const schedule = () => {
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
    jumpToBottom,
    resume() {
      following = true;
      schedule();
    },
    pause() {
      following = false;
    },
    onScroll() {
      const scroller = getScroller();
      if (!scroller || programmatic || scroller.clientHeight === 0) return false;
      updatePosition();
      following = !awayFromBottom();
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
      if (scroller) observer.observe(scroller);
      schedule();
      return () => {
        observer.disconnect();
        if (frame !== undefined) cancelAnimationFrame(frame);
        if (releaseFrame !== undefined) cancelAnimationFrame(releaseFrame);
      };
    },
  };
}
