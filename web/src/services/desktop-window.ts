// web/src/services/desktop-window.ts
// Pure desktop window transport bridge for Wails v2 (primarily Windows frameless
// mode). Keeps raw window.runtime / window.wails IPC and local gateway HTTP
// calls inside services/ without DOM styling mutations or CSS parsing utilities.

import { requestJSON } from "./http";

interface WailsWindowRuntime {
  WindowMinimise?: () => void;
  WindowMaximise?: () => void;
  WindowUnmaximise?: () => void;
  WindowToggleMaximise?: () => void;
  WindowIsMaximised?: () => Promise<boolean>;
  WindowIsFullscreen?: () => Promise<boolean>;
  WindowHide?: () => void;
  WindowSetLightTheme?: () => void;
  WindowSetDarkTheme?: () => void;
  WindowSetSystemDefaultTheme?: () => void;
  WindowSetBackgroundColour?: (r: number, g: number, b: number, a: number) => void;
}

interface WailsInternalFlags {
  enableResize?: boolean;
  resizeEdge?: string;
  defaultCursor?: string | null;
}

interface WailsHostWindow extends Window {
  runtime?: WailsWindowRuntime;
  wails?: {
    flags?: WailsInternalFlags;
  };
}

interface NavigatorWithUAData extends Navigator {
  userAgentData?: {
    platform?: string;
  };
}

function resolveWindow(win?: Window): WailsHostWindow | undefined {
  if (win) return win as WailsHostWindow;
  if (typeof window === "undefined") return undefined;
  return window as WailsHostWindow;
}

function resolveNavigator(nav?: Navigator): NavigatorWithUAData | undefined {
  if (nav) return nav as NavigatorWithUAData;
  if (typeof navigator === "undefined") return undefined;
  return navigator as NavigatorWithUAData;
}

export function isWailsDesktopHost(win?: Window): boolean {
  const w = resolveWindow(win);
  if (!w) return false;
  const proto = w.location?.protocol || "";
  const host = w.location?.hostname || "";
  if (proto === "wails:" || host === "wails.localhost" || host.endsWith(".wails.localhost")) {
    return true;
  }
  return Boolean(w.wails || w.runtime);
}

export function isWindowsPlatform(nav?: Navigator): boolean {
  const n = resolveNavigator(nav);
  if (!n) return false;
  const platform = String(n.userAgentData?.platform || n.platform || "").trim();
  if (/^win/i.test(platform)) return true;
  if (platform && /^(mac|darwin|linux|iphone|ipad|ipod|android)/i.test(platform)) {
    return false;
  }
  return /\bwindows\b/i.test(n.userAgent || "");
}

export function isMacPlatform(nav?: Navigator): boolean {
  const n = resolveNavigator(nav);
  if (!n) return false;
  const platform = String(n.userAgentData?.platform || n.platform || "").trim();
  if (/^(mac|darwin|iphone|ipad|ipod)/i.test(platform)) return true;
  if (platform && /^(win|linux|android)/i.test(platform)) {
    return false;
  }
  return /\b(?:macintosh|mac os x|iphone|ipad|ipod)\b/i.test(n.userAgent || "");
}

export function isWindowsDesktopHost(win?: Window, nav?: Navigator): boolean {
  return isWailsDesktopHost(win) && isWindowsPlatform(nav);
}

export function minimiseDesktopWindow(win?: Window): void {
  resolveWindow(win)?.runtime?.WindowMinimise?.();
}

export function toggleDesktopWindowMaximise(win?: Window): void {
  resolveWindow(win)?.runtime?.WindowToggleMaximise?.();
}

export async function queryDesktopWindowMaximised(win?: Window): Promise<boolean> {
  const fn = resolveWindow(win)?.runtime?.WindowIsMaximised;
  if (typeof fn !== "function") return false;
  try {
    return Boolean(await fn());
  } catch {
    return false;
  }
}

export async function queryDesktopWindowFullscreen(win?: Window): Promise<boolean> {
  const fn = resolveWindow(win)?.runtime?.WindowIsFullscreen;
  if (typeof fn !== "function") return false;
  try {
    return Boolean(await fn());
  } catch {
    return false;
  }
}

/**
 * Updates Wails v2's Windows frameless DOM edge-resize flag (`enableResize`).
 * Returns whether the host flag was touched plus any saved default cursor so
 * the caller can reset document cursor styling outside `services/`.
 */
export function setWailsFramelessResizeEnabled(
  enabled: boolean,
  win?: Window,
  nav?: Navigator
): { updated: boolean; defaultCursor: string } {
  const w = resolveWindow(win);
  if (!w || !isWindowsDesktopHost(w, nav)) {
    return { updated: false, defaultCursor: "" };
  }
  const flags = w.wails?.flags;
  if (!flags) {
    return { updated: false, defaultCursor: "" };
  }
  flags.enableResize = enabled;
  if (!enabled) {
    flags.resizeEdge = undefined;
  }
  return { updated: true, defaultCursor: flags.defaultCursor || "" };
}

/**
 * Routes custom close-button clicks through the local desktop gateway so the
 * host persists window geometry before honoring HideWindowOnClose (minimize to
 * tray). Falls back to WindowHide (never Quit) if the bridge is unreachable or
 * times out so the close button can never feel frozen.
 */
export async function closeDesktopWindow(win?: Window, timeoutMs = 1500): Promise<void> {
  const controller = typeof AbortController !== "undefined" ? new AbortController() : undefined;
  const timer =
    controller && timeoutMs > 0
      ? setTimeout(() => controller.abort(), timeoutMs)
      : undefined;
  try {
    await requestJSON<{ ok?: boolean }>("/api/hoxi/desktop/window/close", {
      method: "POST",
      ...(controller ? { signal: controller.signal } : {}),
    });
    return;
  } catch {
    resolveWindow(win)?.runtime?.WindowHide?.();
  } finally {
    if (timer !== undefined) {
      clearTimeout(timer);
    }
  }
}

export function setDesktopWindowTheme(
  effective: "light" | "dark",
  mode: "light" | "dark" | "system",
  rgb: readonly [number, number, number],
  win?: Window,
  nav?: Navigator
): void {
  const w = resolveWindow(win);
  if (!w || !isWindowsDesktopHost(w, nav)) return;
  const runtime = w.runtime;
  if (!runtime) return;

  if (mode === "system") {
    runtime.WindowSetSystemDefaultTheme?.();
  } else if (effective === "dark") {
    runtime.WindowSetDarkTheme?.();
  } else {
    runtime.WindowSetLightTheme?.();
  }

  runtime.WindowSetBackgroundColour?.(rgb[0], rgb[1], rgb[2], 255);
}
