// web/src/components/layout/surface-extension.ts
import type { JSX } from "solid-js";
import { surfaceMode, type SurfaceMode } from "../../store/workspace";

export type InspectorSurfaceTabID =
  | "diffs"
  | "memory"
  | "files"
  | "skills"
  | "terminal"
  | "browser"
  | "computer"
  | "tasks";

export interface SurfaceExtension {
  enabled: boolean;
  defaultMode?: SurfaceMode;
  renderTheme?: () => JSX.Element;
  renderHeaderSwitch?: () => JSX.Element;
  renderHeaderCenter?: () => JSX.Element;
  renderLeftNav?: () => JSX.Element;
  renderLaunchpad?: () => JSX.Element;
  renderComposer?: () => JSX.Element;
  /** Full-window first-launch surface chooser; replaces `defaultMode` when the user has never picked a surface. */
  renderOnboarding?: () => JSX.Element;
  inspectorTabs?: {
    primary: InspectorSurfaceTabID[];
    secondary: InspectorSurfaceTabID[];
  };
  hideGitControls?: boolean;
  hideCodeComposerControls?: boolean;
  preferFilePreview?: boolean;
  skipCodeAudit?: boolean;
  formatModeLabel?: (agentName: string, locale: string) => string | null;
  formatModeDescription?: (agentName: string, locale: string) => string | null;
}

export const surfaceExtension: SurfaceExtension = {
  enabled: false,
};

export function isWorkSurface(): boolean {
  return surfaceExtension.enabled && surfaceMode() === "work";
}
