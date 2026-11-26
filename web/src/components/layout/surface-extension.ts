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

export interface RemoteAccessTabSpec {
  id: string;
  label: () => string;
  tip: () => string;
  render: () => JSX.Element;
}

export interface CollaborationLinkTabSpec {
  id: string;
  label: () => string;
  subtitle: () => string;
  badgeCount?: () => number;
  onOpen?: (directory: string) => void;
  render: (props: { directory: string; onClose: () => void }) => JSX.Element;
}

export interface CodeSidebarExtraViewSpec {
  id: string;
  label: () => string;
  active: () => boolean;
  badgeCount?: () => number;
  online?: () => boolean;
  enter: () => void;
  exit: () => void;
  renderNav: () => JSX.Element;
  renderMainView: () => JSX.Element;
  renderHeaderCenter?: () => JSX.Element;
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
  remoteAccessTabs?: () => RemoteAccessTabSpec[];
  isExternalPortalActive?: () => boolean;
  renderExternalPortal?: () => JSX.Element;
  collaborationLinksButtonLabel?: () => string;
  collaborationLinkTabs?: () => CollaborationLinkTabSpec[];
  renderExtraTasksSection?: (props: { directory: () => string; visible: () => boolean }) => JSX.Element;
  hasExtraTasks?: () => boolean;
  hideGitControls?: boolean;
  hideCodeComposerControls?: boolean;
  preferFilePreview?: boolean;
  extraPrimaryModes?: () => AgentInfo[];
  renderModeIcon?: (agentName: string, size: number) => JSX.Element | null;
  formatModeLabel?: (agentName: string, locale: string) => string | null;
  formatModeDescription?: (agentName: string, locale: string) => string | null;
  isWorkspaceVisible?: (path: string) => boolean;
  onCreateWorkspaceIntent?: () => void;
  codeSidebarExtraView?: CodeSidebarExtraViewSpec;
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
