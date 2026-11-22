import { createMemo, Show, type Accessor } from "solid-js";
import {
  Command,
  PanelLeft,
  PanelRight,
} from "lucide-solid";
import {
  activeSession,
  defaultWorkspaceDir,
  hasWindowsWindowControls,
  isMacDesktopPlatform,
  normalizeFolderPath,
  openEditWorkspaceModal,
  resolveRootSessionID,
  workspace,
  workspaceDisplayName,
} from "../../store/workspace";
import { t } from "../../i18n";
import { isPlaceholderSessionTitle } from "../../store/session-title";
import BranchManagerPopover from "./BranchManagerPopover";
import HeaderOverflowMenu from "./HeaderOverflowMenu";
import UpdateIndicator from "./UpdateIndicator";
import NetworkStatusBanner from "./NetworkStatusBanner";
import WindowControls, { handleFramelessHeaderDoubleClick } from "./WindowControls";
import { isWorkSurface, surfaceExtension } from "./surface-extension";

interface CompactHeaderProps {
  leftNavOpen: boolean;
  onToggleLeftNav: () => void;
  inspectorOpen: boolean;
  onToggleInspector: () => void;
  onOpenCommandPalette: () => void;
  network?: {
    online: Accessor<boolean>;
    connected: Accessor<boolean>;
    streamStarted: Accessor<boolean>;
    onRetry: () => void;
  };
}

export default function CompactHeader(props: CompactHeaderProps) {
  const session = createMemo(() => activeSession());
  // While a subagent session is open its title lives in the subagent banner;
  // the header keeps naming the main session.
  const mainSession = createMemo(() => {
    const active = session();
    const rootID = resolveRootSessionID(active?.id);
    if (!active || !rootID || rootID === active.id) return active;
    return workspace.sessions.find((s) => s.id === rootID) ?? active;
  });

  const projectName = createMemo(() => workspaceDisplayName(workspace.rootDir));
  const sessionTitle = createMemo(() => {
    const title = mainSession()?.title;
    return isPlaceholderSessionTitle(title) ? t("leftNav.newThread") : title;
  });

  const isMac = isMacDesktopPlatform();
  const showWinControls = hasWindowsWindowControls();

  return (
    <header
      class={`hoxi-header-bar shrink-0 bg-transparent flex items-center gap-3 ${
        isMac
          ? "h-[52px] pl-[96px] pr-4"
          : showWinControls
            ? "h-[42px] pl-3 pr-0"
            : "h-[42px] px-4"
      } select-none`}
      style={{ "--wails-draggable": "drag" } as any}
      onDblClick={handleFramelessHeaderDoubleClick}
    >
      <div class="flex shrink-0 items-center gap-2 text-[12px] font-medium max-w-[35%] min-w-0">
        <button
          type="button"
          class={`hoxi-press inline-flex items-center justify-center w-7 h-7 shrink-0 rounded-lg cursor-pointer ${
            props.leftNavOpen
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
              : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
          }`}
          style={{ "--wails-draggable": "no-drag" } as any}
          onClick={props.onToggleLeftNav}
          title={props.leftNavOpen ? t("header.leftNavHide") : t("header.leftNavShow")}
          aria-label={props.leftNavOpen ? t("header.leftNavHide") : t("header.leftNavShow")}
          aria-expanded={props.leftNavOpen}
          aria-controls="workspace-left-nav"
        >
          <PanelLeft size={15} />
        </button>

        <Show when={surfaceExtension.enabled && surfaceExtension.renderHeaderSwitch}>
          {surfaceExtension.renderHeaderSwitch!()}
        </Show>

        <Show
          when={
            !(
              isWorkSurface() &&
              surfaceExtension.hideGitControls &&
              (!workspace.rootDir ||
                normalizeFolderPath(workspace.rootDir) ===
                  normalizeFolderPath(defaultWorkspaceDir()))
            )
          }
        >
          <button
            type="button"
            style={{ "--wails-draggable": "no-drag" } as any}
            onClick={() => openEditWorkspaceModal(workspace.rootDir)}
            class="min-w-0 text-[var(--text-secondary)] hover:text-[var(--text-primary)] font-mono text-[12px] truncate max-w-[180px] transition-colors cursor-pointer"
            title={`${workspace.rootDir} (${t("leftNav.editWorkspace")})`}
          >
            {projectName()}
          </button>
        </Show>
      </div>

      <div class="flex flex-1 min-w-0 items-center justify-center gap-2">
        <Show when={surfaceExtension.enabled && surfaceExtension.renderHeaderCenter}>
          <div class={isWorkSurface() ? "contents" : "hidden"}>
            {surfaceExtension.renderHeaderCenter!()}
          </div>
        </Show>
        <div
          class={
            isWorkSurface() && surfaceExtension.renderHeaderCenter
              ? "hidden"
              : "contents"
          }
        >
          <span
            class="min-w-0 truncate text-[12px] font-medium text-[var(--text-primary)]"
            title={sessionTitle()}
          >
            {sessionTitle()}
          </span>
          <BranchManagerPopover />
        </div>
      </div>

      <div class={`flex shrink-0 items-center gap-1.5 text-[12px] ${showWinControls ? "h-full" : ""}`}>
        <UpdateIndicator />

        <Show when={props.network}>
          {(network) => (
            <NetworkStatusBanner
              online={network().online}
              connected={network().connected}
              streamStarted={network().streamStarted}
              onRetry={network().onRetry}
            />
          )}
        </Show>

        <button
          class="hoxi-press inline-flex items-center justify-center w-7 h-7 rounded-lg text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
          style={{ "--wails-draggable": "no-drag" } as any}
          onClick={props.onOpenCommandPalette}
          title={`${t("commandPalette.actions")} (${isMac ? "⌘K" : "Ctrl+K"})`}
          aria-label={t("commandPalette.actions")}
        >
          <Command size={15} />
        </button>

        <HeaderOverflowMenu />

        <button
          class={`hoxi-press inline-flex items-center justify-center w-7 h-7 rounded-lg cursor-pointer ${
            props.inspectorOpen
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
              : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
          }`}
          style={{ "--wails-draggable": "no-drag" } as any}
          onClick={props.onToggleInspector}
          title={props.inspectorOpen ? t("header.inspectorHide") : t("header.inspectorShow")}
        >
          <PanelRight size={15} />
        </button>

        <WindowControls />
      </div>
    </header>
  );
}
