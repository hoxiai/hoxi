import { createSignal } from "solid-js";

export const LAYOUT_PREFS_KEY = "hoxi.layout:v1";

export const LEFT_PANE_MIN = 220;
export const LEFT_PANE_MAX = 360;
export const RIGHT_PANE_MIN = 340;
export const RIGHT_PANE_MAX = 640;

export interface LayoutPrefs {
  leftNavOpen: boolean;
  leftWidth: number;
  rightWidth: number;
}

const DEFAULT_LAYOUT: LayoutPrefs = {
  leftNavOpen: true,
  leftWidth: 268,
  rightWidth: 420,
};

function clampWidth(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function initialLayout(): LayoutPrefs {
  try {
    const raw = localStorage.getItem(LAYOUT_PREFS_KEY);
    if (!raw) return DEFAULT_LAYOUT;
    const parsed = JSON.parse(raw) as Partial<LayoutPrefs> | null;
    if (!parsed || typeof parsed !== "object") return DEFAULT_LAYOUT;
    return {
      leftNavOpen:
        typeof parsed.leftNavOpen === "boolean"
          ? parsed.leftNavOpen
          : DEFAULT_LAYOUT.leftNavOpen,
      leftWidth:
        typeof parsed.leftWidth === "number" && Number.isFinite(parsed.leftWidth)
          ? clampWidth(parsed.leftWidth, LEFT_PANE_MIN, LEFT_PANE_MAX)
          : DEFAULT_LAYOUT.leftWidth,
      rightWidth:
        typeof parsed.rightWidth === "number" && Number.isFinite(parsed.rightWidth)
          ? clampWidth(parsed.rightWidth, RIGHT_PANE_MIN, RIGHT_PANE_MAX)
          : DEFAULT_LAYOUT.rightWidth,
    };
  } catch {
    return DEFAULT_LAYOUT;
  }
}

const initial = initialLayout();

export const [leftNavOpen, setLeftNavOpen] = createSignal(initial.leftNavOpen);
export const [leftWidth, setLeftWidth] = createSignal(initial.leftWidth);
export const [rightWidth, setRightWidth] = createSignal(initial.rightWidth);

export function saveLayoutPrefs(): void {
  try {
    localStorage.setItem(
      LAYOUT_PREFS_KEY,
      JSON.stringify({
        leftNavOpen: leftNavOpen(),
        leftWidth: leftWidth(),
        rightWidth: rightWidth(),
      } satisfies LayoutPrefs)
    );
  } catch {
    /* storage unavailable */
  }
}
