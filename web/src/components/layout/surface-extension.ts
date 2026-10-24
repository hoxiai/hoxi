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
  | "tasks";

export interface SurfaceExtension {
  enabled: boolean;
  defaultMode?: SurfaceMode;
  renderHeaderSwitch?: () => JSX.Element;
  renderLeftNav?: () => JSX.Element;
  renderLaunchpad?: () => JSX.Element;
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
