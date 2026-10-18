// Keep following layout changes without pulling readers away from older messages.
export function createTimelineScroll(
  getScroller: () => HTMLDivElement | undefined,
  enabled: () => boolean,
) {
  let following = true;
  let frame: number | undefined;
  let releaseFrame: number | undefined;
  let programmatic = false;

  const schedule = () => {
    if (!enabled() || !following || frame !== undefined) return;
    frame = requestAnimationFrame(() => {
      frame = undefined;
      const scroller = getScroller();
      if (!scroller || !enabled() || !following || scroller.clientHeight === 0) return;
      programmatic = true;
      scroller.scrollTop = scroller.scrollHeight;
      if (releaseFrame !== undefined) cancelAnimationFrame(releaseFrame);
      releaseFrame = requestAnimationFrame(() => {
        releaseFrame = undefined;
        programmatic = false;
      });
    });
  };

  return {
    schedule,
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
      following = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 64;
      return !following;
    },
    observe(content: HTMLDivElement) {
      const observer = new ResizeObserver(schedule);
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
