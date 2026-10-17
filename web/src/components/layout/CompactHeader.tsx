import { createMemo, Show } from "solid-js";
import {
  Moon,
  PanelRight,
  RotateCcw,
  Sun,
} from "lucide-solid";
import {
  activeSession,
  openEditWorkspaceModal,
  toggleTheme,
  undoRevert,
  workspace,
  workspaceDisplayName,
} from "../../store/workspace";
import { t } from "../../i18n";
import BranchManagerPopover from "./BranchManagerPopover";
import DevPulsePopover from "./DevPulsePopover";

interface CompactHeaderProps {
  inspectorOpen: boolean;
  onToggleInspector: () => void;
}

export default function CompactHeader(props: CompactHeaderProps) {
  const session = createMemo(() => activeSession());

  const projectName = createMemo(() => workspaceDisplayName(workspace.rootDir));

  const onUndoRevert = async () => {
    const s = session();
    if (!s) return;
    try {
      await undoRevert(s.id);
    } catch (err) {
      console.error(err);
    }
  };

  const isMac =
    typeof navigator !== "undefined" &&
    /Mac|iPhone|iPad/i.test(navigator.platform || navigator.userAgent);

  return (
    <header
      class={`h-[42px] shrink-0 bg-transparent flex items-center justify-between px-4 ${
        isMac ? "pl-[96px]" : "pl-4"
      } select-none`}
      style={{ "--wails-draggable": "drag" } as any}
    >
      <div class="flex items-center gap-2 text-[12px] font-medium min-w-0">
        <div class="flex items-center gap-2 shrink-0">
          <svg viewBox="0 0 64 64" fill="none" class="w-4 h-4 text-[var(--accent-primary)] shrink-0">
            <path d="M32 8L52 21H44L32 13L20 21H12L32 8Z" fill="currentColor" />
            <rect x="20" y="25" width="6.5" height="12" rx="3.25" fill="currentColor" />
            <rect x="37.5" y="25" width="6.5" height="12" rx="3.25" fill="currentColor" />
            <path d="M19 42.5C23 50 41 50 45 42.5" stroke="currentColor" stroke-width="5.5" stroke-linecap="round" />
          </svg>
          <span class="text-[var(--text-primary)] font-semibold tracking-tight text-[13px]" title={workspace.rootDir}>
            Hoxi
          </span>
        </div>

        <Show when={projectName().toLowerCase() !== "hoxi"}>
          <span class="text-[var(--text-faint)]">/</span>
          <button
            type="button"
            style={{ "--wails-draggable": "no-drag" } as any}
            onClick={() => openEditWorkspaceModal(workspace.rootDir)}
            class="text-[var(--text-secondary)] hover:text-[var(--text-primary)] font-mono text-[12px] truncate max-w-[180px] transition-colors cursor-pointer"
            title={`${workspace.rootDir} (${t("leftNav.editWorkspace")})`}
          >
            {projectName()}
          </button>
        </Show>

        <BranchManagerPopover />
      </div>

      <div class="flex items-center gap-1.5 text-[12px]">
        <Show when={session()?.revert}>
          <button
            class="inline-flex items-center gap-1 rounded-lg border border-[var(--warn-border)] bg-[var(--warn-surface)] px-2.5 py-1 text-[12px] text-[var(--warn-text)] hover:opacity-90 transition-opacity cursor-pointer"
            style={{ "--wails-draggable": "no-drag" } as any}
            onClick={onUndoRevert}
            title={t("header.undoRevertTooltip")}
          >
            <RotateCcw size={12} />
            {t("header.undoRevert")}
          </button>
        </Show>

        <Show when={!workspace.connected}>
          <div class="flex items-center gap-1.5 rounded-lg px-2 py-1 text-[var(--text-muted)]">
            <span class="inline-block w-1.5 h-1.5 rounded-full bg-[var(--warn)] animate-pulse" />
            <span class="text-[11.5px]">{t("common.connecting")}</span>
          </div>
        </Show>

        <DevPulsePopover />

        <button
          class="inline-flex items-center justify-center w-7 h-7 rounded-lg text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
          style={{ "--wails-draggable": "no-drag" } as any}
          onClick={toggleTheme}
          title={workspace.theme === "dark" ? t("header.themeLightTooltip") : t("header.themeDarkTooltip")}
        >
          {workspace.theme === "dark" ? <Sun size={15} /> : <Moon size={15} />}
        </button>

        <button
          class={`inline-flex items-center justify-center w-7 h-7 rounded-lg transition-colors cursor-pointer ${
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
