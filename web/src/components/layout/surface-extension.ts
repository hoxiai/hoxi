// web/src/components/layout/surface-extension.ts
import type { JSX } from "solid-js";
import type { AgentInfo, DesktopIntent } from "../../services/api";
import { surfaceMode, type SurfaceMode } from "../../store/workspace";

export type InspectorSurfaceTabID =
  | "diffs"
  | "memory"
  | "files"
  | "skills"
  | "terminal"
  | "browser"
  | "computer"
  | "tasks"
  | "preview";

export interface CustomInspectorTabSpec {
  id: string;
  label: string;
  render: () => JSX.Element;
}

export interface SurfaceExtension {
  enabled: boolean;
  openAppIntent?: (intent: DesktopIntent) => void;
  defaultMode?: SurfaceMode;
  renderTheme?: () => JSX.Element;
  renderHeaderSwitch?: () => JSX.Element;
  renderHeaderCenter?: () => JSX.Element;
  renderLeftNav?: () => JSX.Element;
  renderLaunchpad?: () => JSX.Element;
  renderComposer?: () => JSX.Element;
  isMainViewActive?: () => boolean;
  renderMainView?: () => JSX.Element;
  /** Full-window first-launch surface chooser; replaces `defaultMode` when the user has never picked a surface. */
  renderOnboarding?: () => JSX.Element;
  inspectorTabs?: {
    primary: InspectorSurfaceTabID[];
    secondary: InspectorSurfaceTabID[];
  };
  customInspectorTabs?: () => CustomInspectorTabSpec[];
  hideGitControls?: boolean;
  hideCodeComposerControls?: boolean;
  preferFilePreview?: boolean;
  extraPrimaryModes?: () => AgentInfo[];
  renderModeIcon?: (agentName: string, size: number) => JSX.Element | null;
  formatModeLabel?: (agentName: string, locale: string) => string | null;
  formatModeDescription?: (agentName: string, locale: string) => string | null;
  isWorkspaceVisible?: (path: string) => boolean;
  onCreateWorkspaceIntent?: () => void;
}

export const surfaceExtension: SurfaceExtension = {
  enabled: false,
};

export function isWorkSurface(): boolean {
  return surfaceExtension.enabled && surfaceMode() === "work";
}

export function resolveSurfacePrimaryModes(agents: readonly AgentInfo[] = []): AgentInfo[] {
  return agents.filter((a) => Boolean(a?.name) && !a.extension);
}
