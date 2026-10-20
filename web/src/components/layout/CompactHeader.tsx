import { createMemo, Show } from "solid-js";
import {
  Command,
  PanelLeft,
  PanelRight,
} from "lucide-solid";
import {
  activeSession,
  openEditWorkspaceModal,
  workspace,
  workspaceDisplayName,
} from "../../store/workspace";
import { t } from "../../i18n";
import { isPlaceholderSessionTitle } from "../../store/session-title";
import { resolveRootSessionID } from "../timeline/SubagentSessionBanner";
import BranchManagerPopover from "./BranchManagerPopover";
import DevPulsePopover from "./DevPulsePopover";
import ThemeStylePopover from "./ThemeStylePopover";
import UpdateBadge from "./UpdateBadge";

interface CompactHeaderProps {
  leftNavOpen: boolean;
  onToggleLeftNav: () => void;
  inspectorOpen: boolean;
  onToggleInspector: () => void;
  onOpenCommandPalette: () => void;
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

  const isMac =
    typeof navigator !== "undefined" &&
    /Mac|iPhone|iPad/i.test(navigator.platform || navigator.userAgent);

  return (
    <header
      class={`shrink-0 bg-transparent flex items-center gap-3 px-4 ${
        isMac ? "h-[52px] pl-[96px]" : "h-[42px] pl-4"
      } select-none`}
      style={{ "--wails-draggable": "drag" } as any}
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

        <button
          type="button"
          style={{ "--wails-draggable": "no-drag" } as any}
          onClick={() => openEditWorkspaceModal(workspace.rootDir)}
          class="min-w-0 text-[var(--text-secondary)] hover:text-[var(--text-primary)] font-mono text-[12px] truncate max-w-[180px] transition-colors cursor-pointer"
          title={`${workspace.rootDir} (${t("leftNav.editWorkspace")})`}
        >
          {projectName()}
        </button>
      </div>

      <div class="flex flex-1 min-w-0 items-center justify-center gap-2">
        <span
          class="min-w-0 truncate text-[12px] font-medium text-[var(--text-primary)] transition-opacity duration-150"
          title={sessionTitle()}
        >
          {sessionTitle()}
        </span>
        <BranchManagerPopover />
      </div>

      <div class="flex shrink-0 items-center gap-1.5 text-[12px]">
        <Show when={!workspace.connected}>
          <div class="flex items-center gap-1.5 rounded-lg px-2 py-1 text-[var(--text-muted)]">
            <span class="inline-block w-1.5 h-1.5 rounded-full bg-[var(--warn)] animate-pulse" />
            <span class="text-[11.5px]">{t("common.connecting")}</span>
          </div>
        </Show>

        <DevPulsePopover />
        <UpdateBadge />

        <button
          class="hoxi-press inline-flex items-center justify-center w-7 h-7 rounded-lg text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
          style={{ "--wails-draggable": "no-drag" } as any}
          onClick={props.onOpenCommandPalette}
          title={`${t("commandPalette.actions")} (${isMac ? "⌘K" : "Ctrl+K"})`}
          aria-label={t("commandPalette.actions")}
        >
          <Command size={15} />
        </button>

        <ThemeStylePopover />

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
      </div>
    </header>
  );
}
