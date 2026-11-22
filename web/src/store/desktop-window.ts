// web/src/store/desktop-window.ts
// Reactive state and actions for desktop window chrome (Windows frameless top bar
// caption controls, DWM theme/background synchronization, and Wails resize-edge
// coordination). Leaf module with no imports from settings.ts to stay acyclic.

import { createSignal } from "solid-js";
import {
  closeDesktopWindow,
  isMacPlatform,
  isWindowsDesktopHost,
  minimiseDesktopWindow,
  queryDesktopWindowFullscreen,
  queryDesktopWindowMaximised,
  setDesktopWindowTheme,
  setWailsFramelessResizeEnabled,
  toggleDesktopWindowMaximise,
} from "../services/desktop-window";

export interface DesktopWindowThemeState {
  effective: "light" | "dark";
  mode: "light" | "dark" | "system";
}

const DEFAULT_LIGHT_BG: readonly [number, number, number] = [244, 247, 251];
const DEFAULT_DARK_BG: readonly [number, number, number] = [11, 18, 32];

const [desktopWindowMaximised, setDesktopWindowMaximised] = createSignal(false);

export { desktopWindowMaximised };

export function hasWindowsWindowControls(win?: Window, nav?: Navigator): boolean {
  return isWindowsDesktopHost(win, nav);
}

export function isMacDesktopPlatform(): boolean {
  return isMacPlatform();
}

export function parseCSSColorRGB(raw?: string): [number, number, number] | null {
  const value = (raw || "").trim();
  if (!value) return null;

  const hex3 = value.match(/^#([0-9a-f]{3})$/i);
  if (hex3) {
    const h = hex3[1];
    return [
      parseInt(h[0] + h[0], 16),
      parseInt(h[1] + h[1], 16),
      parseInt(h[2] + h[2], 16),
    ];
  }

  const hex6 = value.match(/^#([0-9a-f]{6})$/i);
  if (hex6) {
    const h = hex6[1];
    return [
      parseInt(h.slice(0, 2), 16),
      parseInt(h.slice(2, 4), 16),
      parseInt(h.slice(4, 6), 16),
    ];
  }

  const rgb = value.match(
    /^rgba?\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})(?:\s*,\s*[\d.]+)?\s*\)$/i
  );
  if (rgb) {
    const r = Number(rgb[1]);
    const g = Number(rgb[2]);
    const b = Number(rgb[3]);
    if (r <= 255 && g <= 255 && b <= 255) {
      return [r, g, b];
    }
  }
  return null;
}

/**
 * Resolves the active `--bg-canvas` color from the mounted surface shell (so
 * Work surface theme overrides on `[data-surface="work"]` are picked up) with
 * fallback to `:root` and static light/dark defaults.
 */
export function resolveDesktopCanvasRGB(
  effective: "light" | "dark",
  doc?: Document,
  win?: Window
): [number, number, number] {
  const d = doc ?? (typeof document !== "undefined" ? document : undefined);
  const w = win ?? (typeof window !== "undefined" ? window : undefined);
  if (d && w && typeof w.getComputedStyle === "function") {
    const surfaceEl = typeof d.querySelector === "function" ? d.querySelector("[data-surface]") : null;
    for (const target of [surfaceEl, d.documentElement]) {
      if (!target) continue;
      const raw = w.getComputedStyle(target).getPropertyValue("--bg-canvas").trim();
      const parsed = parseCSSColorRGB(raw);
      if (parsed) return parsed;
    }
  }
  const fallback = effective === "dark" ? DEFAULT_DARK_BG : DEFAULT_LIGHT_BG;
  return [fallback[0], fallback[1], fallback[2]];
}

export function syncDesktopWindowThemeFromDOM(
  effective: "light" | "dark",
  mode: "light" | "dark" | "system",
  win?: Window,
  nav?: Navigator,
  doc?: Document
): void {
  const rgb = resolveDesktopCanvasRGB(effective, doc, win);
  setDesktopWindowTheme(effective, mode, rgb, win, nav);
}

/**
 * Disables Wails's 6px frameless DOM edge-resize hit-testing while maximised or
 * fullscreen so Fitts's Law holds at the top-right close corner, and restores it
 * in normal state.
 */
export function syncFramelessResizeBoundary(
  maximised: boolean,
  fullscreen = false,
  win?: Window,
  nav?: Navigator,
  doc?: Document
): void {
  const shouldEnable = !maximised && !fullscreen;
  const { updated, defaultCursor } = setWailsFramelessResizeEnabled(shouldEnable, win, nav);
  if (!updated || shouldEnable) return;
  const d = doc ?? (typeof document !== "undefined" ? document : undefined);
  if (d?.documentElement) {
    d.documentElement.style.cursor = defaultCursor;
  }
}

let stateSyncGen = 0;
let toggleFollowupTimer: ReturnType<typeof setTimeout> | undefined;

export async function refreshDesktopWindowState(): Promise<boolean> {
  if (!isWindowsDesktopHost()) {
    setDesktopWindowMaximised(false);
    return false;
  }
  const gen = ++stateSyncGen;
  const [maximised, fullscreen] = await Promise.all([
    queryDesktopWindowMaximised(),
    queryDesktopWindowFullscreen(),
  ]);
  if (gen !== stateSyncGen) {
    return desktopWindowMaximised();
  }
  setDesktopWindowMaximised(maximised);
  syncFramelessResizeBoundary(maximised, fullscreen);
  return maximised;
}

export function minimiseDesktopWindowAction(): void {
  if (!isWindowsDesktopHost()) return;
  minimiseDesktopWindow();
}

export function toggleDesktopWindowMaximiseAction(): void {
  if (!isWindowsDesktopHost()) return;
  toggleDesktopWindowMaximise();
  if (toggleFollowupTimer !== undefined) {
    clearTimeout(toggleFollowupTimer);
  }
  void refreshDesktopWindowState();
  toggleFollowupTimer = setTimeout(() => {
    toggleFollowupTimer = undefined;
    void refreshDesktopWindowState();
  }, 140);
}

export function closeDesktopWindowAction(): Promise<void> {
  if (!isWindowsDesktopHost()) return Promise.resolve();
  return closeDesktopWindow();
}

/**
 * Subscribes to window resize/focus/load events on Windows desktop to keep the
 * maximize/restore glyph, resize-edge guard, and DWM window theme synchronized.
 */
export function startDesktopWindowSync(
  getThemeState: () => DesktopWindowThemeState
): () => void {
  if (typeof window === "undefined" || !isWindowsDesktopHost()) {
    return () => {};
  }

  let rafId = 0;
  const scheduleRefresh = () => {
    if (rafId) return;
    if (typeof requestAnimationFrame === "function") {
      rafId = requestAnimationFrame(() => {
        rafId = 0;
        void refreshDesktopWindowState();
      });
    } else {
      void refreshDesktopWindowState();
    }
  };

  const syncTheme = () => {
    const { effective, mode } = getThemeState();
    syncDesktopWindowThemeFromDOM(effective, mode);
  };

  // Re-sync once shortly after mount so our maximized resize-edge guard wins
  // after Wails's navigationCompleted injects `enableResize = true` and after
  // dev-mode stylesheets have populated `--bg-canvas`.
  const settleTimer = setTimeout(() => {
    void refreshDesktopWindowState();
    syncTheme();
  }, 180);

  const onLoad = () => {
    scheduleRefresh();
    syncTheme();
  };

  void refreshDesktopWindowState();
  syncTheme();
  window.addEventListener("resize", scheduleRefresh);
  window.addEventListener("focus", scheduleRefresh);
  window.addEventListener("load", onLoad);

  return () => {
    clearTimeout(settleTimer);
    if (toggleFollowupTimer !== undefined) {
      clearTimeout(toggleFollowupTimer);
      toggleFollowupTimer = undefined;
    }
    if (rafId && typeof cancelAnimationFrame === "function") {
      cancelAnimationFrame(rafId);
      rafId = 0;
    }
    window.removeEventListener("resize", scheduleRefresh);
    window.removeEventListener("focus", scheduleRefresh);
    window.removeEventListener("load", onLoad);
  };
}
