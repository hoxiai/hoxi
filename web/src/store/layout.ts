import { createSignal } from "solid-js";
import { readStorageItem, readStorageJSON, writeStorageItem, writeStorageJSON } from "./workspace-state";

export const LAYOUT_PREFS_KEY = "hoxi.layout:v1";
export const SURFACE_MODE_KEY = "hoxi.surface-mode:v1";
export const DEFAULT_WORKSPACE_DIR_KEY = "hoxi.default-workspace-dir:v1";

export type SurfaceMode = "code" | "work";

export const LEFT_PANE_MIN = 220;
export const LEFT_PANE_MAX = 360;
export const RIGHT_PANE_MIN = 260;
export const RIGHT_PANE_MAX = 2400;
export const DEFAULT_LEFT_WIDTH = 268;
export const DEFAULT_RIGHT_WIDTH = 420;
export const PANE_GUTTERS = 24;
export const CENTER_PANE_MIN = 380;

export interface LayoutPrefs {
  leftNavOpen: boolean;
  leftWidth: number;
  rightWidth: number;
}

const DEFAULT_LAYOUT: LayoutPrefs = {
  leftNavOpen: true,
  leftWidth: DEFAULT_LEFT_WIDTH,
  rightWidth: DEFAULT_RIGHT_WIDTH,
};

function clampWidth(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

export function computeRightPaneCeiling(viewportWidth: number, leftPaneWidth: number): number {
  return Math.max(
    RIGHT_PANE_MIN,
    Math.min(RIGHT_PANE_MAX, viewportWidth - PANE_GUTTERS - leftPaneWidth - CENTER_PANE_MIN)
  );
}

export function clampRightPaneWidth(
  desiredWidth: number,
  viewportWidth: number,
  leftPaneWidth: number
): number {
  return clampWidth(desiredWidth, RIGHT_PANE_MIN, computeRightPaneCeiling(viewportWidth, leftPaneWidth));
}

export function canFitRightPane(viewportWidth: number, leftPaneWidth: number): boolean {
  return viewportWidth - PANE_GUTTERS - leftPaneWidth - RIGHT_PANE_MIN >= CENTER_PANE_MIN;
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
