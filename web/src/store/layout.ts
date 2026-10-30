import { createSignal } from "solid-js";

export const LAYOUT_PREFS_KEY = "hoxi.layout:v1";
export const SURFACE_MODE_KEY = "hoxi.surface-mode:v1";
export const DEFAULT_WORKSPACE_DIR_KEY = "hoxi.default-workspace-dir:v1";

export type SurfaceMode = "code" | "work";

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

function readStoredSurfaceMode(): SurfaceMode | null {
  try {
    const saved = localStorage.getItem(SURFACE_MODE_KEY);
    if (saved === "work" || saved === "code") return saved;
  } catch {
    /* storage unavailable */
  }
  return null;
}

export function hasStoredSurfaceMode(): boolean {
  return readStoredSurfaceMode() !== null;
}

const initial = initialLayout();

function readStoredDefaultWorkspaceDir(): string {
  try {
    return localStorage.getItem(DEFAULT_WORKSPACE_DIR_KEY) || "";
  } catch {
    return "";
  }
}

export const [leftNavOpen, setLeftNavOpen] = createSignal(initial.leftNavOpen);
export const [leftWidth, setLeftWidth] = createSignal(initial.leftWidth);
export const [rightWidth, setRightWidth] = createSignal(initial.rightWidth);
export const [surfaceMode, setSurfaceModeSignal] = createSignal<SurfaceMode>(
  readStoredSurfaceMode() || "code"
);
export const [defaultWorkspaceDir, setDefaultWorkspaceDirSignal] = createSignal(
  readStoredDefaultWorkspaceDir()
);

export function setDefaultWorkspaceDir(dir: string): void {
  setDefaultWorkspaceDirSignal(dir);
  try {
    if (dir) localStorage.setItem(DEFAULT_WORKSPACE_DIR_KEY, dir);
  } catch {
    /* storage unavailable */
  }
}

export function setSurfaceMode(mode: SurfaceMode): void {
  setSurfaceModeSignal(mode);
  try {
    localStorage.setItem(SURFACE_MODE_KEY, mode);
  } catch {
    /* storage unavailable */
  }
}

// First-launch surface chooser. Startup holds its mode-dependent steps until
// the user picks a surface, so the shell boots straight into the right root.
const [surfaceOnboardingOpen, setSurfaceOnboardingOpen] = createSignal(false);
let surfaceOnboardingWaiters: Array<(mode: SurfaceMode) => void> = [];

export { surfaceOnboardingOpen };

export function waitForSurfaceOnboarding(): Promise<SurfaceMode> {
  setSurfaceOnboardingOpen(true);
  return new Promise((resolve) => {
    surfaceOnboardingWaiters.push(resolve);
  });
}

/** Persist the chosen surface and release startup; the chooser stays mounted for its exit animation. */
export function completeSurfaceOnboarding(mode: SurfaceMode): void {
  setSurfaceMode(mode);
  const waiters = surfaceOnboardingWaiters;
  surfaceOnboardingWaiters = [];
  for (const resolve of waiters) resolve(mode);
}

export function dismissSurfaceOnboarding(): void {
  setSurfaceOnboardingOpen(false);
}

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
