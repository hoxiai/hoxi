import { createSignal } from "solid-js";
import { readStorageItem, readStorageJSON, writeStorageItem, writeStorageJSON } from "./workspace-state";

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
  const parsed = readStorageJSON<Partial<LayoutPrefs>>(LAYOUT_PREFS_KEY);
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
}

function readStoredSurfaceMode(): SurfaceMode | null {
  const saved = readStorageItem(SURFACE_MODE_KEY);
  if (saved === "work" || saved === "code") return saved;
  return null;
}

export function hasStoredSurfaceMode(): boolean {
  return readStoredSurfaceMode() !== null;
}

const initial = initialLayout();

function readStoredDefaultWorkspaceDir(): string {
  return readStorageItem(DEFAULT_WORKSPACE_DIR_KEY) || "";
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
  if (dir) writeStorageItem(DEFAULT_WORKSPACE_DIR_KEY, dir);
}

export function setSurfaceMode(mode: SurfaceMode): void {
  setSurfaceModeSignal(mode);
  writeStorageItem(SURFACE_MODE_KEY, mode);
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
  writeStorageJSON(LAYOUT_PREFS_KEY, {
    leftNavOpen: leftNavOpen(),
    leftWidth: leftWidth(),
    rightWidth: rightWidth(),
  } satisfies LayoutPrefs);
}
