import { createEffect, createSignal, onCleanup } from "solid-js";

export type AnchoredAlign = "start" | "end";
export type AnchoredPlacement = "bottom" | "top";

export type AnchoredPosition = { top: number; left: number };

export function useAnchoredPopover(options: {
  anchor: () => HTMLElement | undefined;
  panel: () => HTMLElement | undefined;
  align?: AnchoredAlign;
  placement?: AnchoredPlacement;
  gap?: number;
  margin?: number;
}) {
  const [position, setPosition] = createSignal<AnchoredPosition>({ top: 0, left: 0 });
  const [side, setSide] = createSignal<AnchoredPlacement>(options.placement ?? "bottom");

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
    const left = Math.max(margin, Math.min(preferredLeft, window.innerWidth - width - margin));

    if (options.placement === "top") {
      const aboveTop = rect.top - gap - height;
      const belowTop = rect.bottom + gap;
      const spaceAbove = rect.top - gap - margin;
      const spaceBelow = window.innerHeight - margin - belowTop;
      const openAbove = aboveTop >= margin || spaceAbove >= spaceBelow;
      setSide(openAbove ? "top" : "bottom");
      setPosition({
        left,
        top: openAbove
          ? Math.max(margin, aboveTop)
          : Math.max(margin, Math.min(belowTop, window.innerHeight - height - margin)),
      });
      return;
    }

    const preferredTop = rect.bottom + gap;
    const openBelow = preferredTop + height <= window.innerHeight - margin;
    setSide(openBelow ? "bottom" : "top");
    setPosition({
      left,
      top: openBelow ? preferredTop : Math.max(margin, rect.top - gap - height),
    });
  };

  createEffect(() => {
    const anchorEl = options.anchor();
    const panelEl = options.panel();
    if (!anchorEl || !panelEl) return;
    updatePosition();
    const observer = new ResizeObserver(updatePosition);
    observer.observe(panelEl);
    observer.observe(anchorEl);
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
    side,
    positionStyle: () => ({
      top: `${position().top}px`,
      left: `${position().left}px`,
    }),
    updatePosition,
  };
}
