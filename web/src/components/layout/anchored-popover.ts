import { createEffect, createSignal, onCleanup } from "solid-js";

export type AnchoredAlign = "start" | "end";

export type AnchoredPosition = { top: number; left: number };

export function useAnchoredPopover(options: {
  anchor: () => HTMLElement | undefined;
  panel: () => HTMLElement | undefined;
  align?: AnchoredAlign;
  gap?: number;
  margin?: number;
}) {
  const [position, setPosition] = createSignal<AnchoredPosition>({ top: 0, left: 0 });

  const updatePosition = () => {
    const anchorEl = options.anchor();
    const panelEl = options.panel();
    if (!anchorEl || !panelEl) return;
    const rect = anchorEl.getBoundingClientRect();
    const gap = options.gap ?? 6;
    const margin = options.margin ?? 8;
    const width = panelEl.offsetWidth;
    const height = panelEl.offsetHeight;
    const preferredLeft =
      (options.align ?? "end") === "end" ? rect.right - width : rect.left;
    const preferredTop = rect.bottom + gap;
    setPosition({
      left: Math.max(margin, Math.min(preferredLeft, window.innerWidth - width - margin)),
      top:
        preferredTop + height <= window.innerHeight - margin
          ? preferredTop
          : Math.max(margin, rect.top - gap - height),
    });
  };

  createEffect(() => {
    const panelEl = options.panel();
    if (!panelEl) return;
    updatePosition();
    const observer = new ResizeObserver(updatePosition);
    observer.observe(panelEl);
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    onCleanup(() => {
      observer.disconnect();
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
    });
  });

  return {
    position,
    positionStyle: () => ({
      top: `${position().top}px`,
      left: `${position().left}px`,
    }),
    updatePosition,
  };
}
