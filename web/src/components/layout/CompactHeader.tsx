import { createMemo, createSignal, onCleanup, onMount, Show } from "solid-js";
import {
  Activity,
  GitBranch,
  GitMerge,
  KeyRound,
  Languages,
  Moon,
  PanelRightClose,
  PanelRightOpen,
  RotateCcw,
  Sun,
} from "lucide-solid";
import {
  activeSession,
  applySessionWorktree,
  isSessionBusy,
  openEditWorkspaceModal,
  setWorkspace,
  toggleTheme,
  undoRevert,
  workspace,
  workspaceDisplayName,
} from "../../store/workspace";
import { hoxiAPI } from "../../services/api";
import { Spinner, TextShimmer } from "../icons";
import { t, toggleLocale, currentLocaleMetadata } from "../../i18n";

interface CompactHeaderProps {
  inspectorOpen: boolean;
  onToggleInspector: () => void;
}

export default function CompactHeader(props: CompactHeaderProps) {
  const [applying, setApplying] = createSignal(false);
  const [applyNote, setApplyNote] = createSignal<string | null>(null);
  const [lspCount, setLspCount] = createSignal<number | null>(null);

  const session = createMemo(() => activeSession());
  const worktree = createMemo(() => {
    const id = workspace.activeSessionID;
    return id ? workspace.worktreeBySession[id] : undefined;
  });

  const diffCount = createMemo(() => {
    const id = workspace.activeSessionID;
    return id ? (workspace.diffsBySession[id] ?? []).length : 0;
  });

  const projectName = createMemo(() => workspaceDisplayName(workspace.rootDir));

  const onApply = async () => {
    const s = session();
    if (!s || applying()) return;
    setApplying(true);
    setApplyNote(null);
    try {
      await applySessionWorktree(s.id, "patch");
      setApplyNote(t("header.appliedSuccess"));
    } catch (err) {
      console.error(err);
      setApplyNote(t("header.applyFailed"));
    } finally {
      setApplying(false);
      setTimeout(() => setApplyNote(null), 4000);
    }
  };

  const onUndoRevert = async () => {
    const s = session();
    if (!s) return;
    try {
      await undoRevert(s.id);
    } catch (err) {
      console.error(err);
    }
  };

  onMount(() => {
    const poll = async () => {
      try {
        const status = await hoxiAPI.getLSPStatus(workspace.rootDir || undefined);
        setLspCount(Array.isArray(status) ? status.length : 0);
      } catch {
        setLspCount(null);
      }
    };
    void poll();
    const timer = setInterval(poll, 10000);
    onCleanup(() => clearInterval(timer));
  });

  return (
    <header
      class="h-[52px] shrink-0 bg-transparent flex items-center justify-between px-4 pl-[96px] select-none"
      style={{ "--wails-draggable": "drag" } as any}
    >
      <div class="flex items-center gap-2.5 text-xs font-medium min-w-0">
        <div class="flex items-center gap-2 shrink-0">
          <svg viewBox="0 0 64 64" fill="none" class="w-4.5 h-4.5 text-[var(--accent-primary)] shrink-0">
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
            class="text-[var(--text-secondary)] hover:text-[var(--text-primary)] font-mono text-[11px] truncate max-w-[180px] transition-colors cursor-pointer"
            title={`${workspace.rootDir} (${t("leftNav.editWorkspace")})`}
          >
            {projectName()}
          </button>
        </Show>

        <Show when={worktree()?.branch}>
          <span class="ml-1 inline-flex items-center gap-1 rounded-full border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-0.5 text-[11px] font-mono text-[var(--accent-text)]">
            <GitBranch size={11} />
            {worktree()?.branch}
          </span>
          <button
            class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-2.5 py-1 text-[11px] text-[var(--text-primary)] disabled:opacity-50 transition-colors shadow-2xs"
            style={{ "--wails-draggable": "no-drag" } as any}
            disabled={applying()}
            onClick={onApply}
            title={t("header.applyTooltip")}
          >
            <GitMerge size={11} />
            {applying() ? t("header.applying") : t("header.applyToMain")}
          </button>
        </Show>

        <Show when={applyNote()}>
          <span class="text-[11px] text-[var(--ok-text)]">{applyNote()}</span>
        </Show>
      </div>

      <div class="flex items-center gap-2 text-xs">
        <Show when={session()?.revert}>
          <button
            class="inline-flex items-center gap-1 rounded-lg border border-[var(--warn-border)] bg-[var(--warn-surface)] px-2.5 py-1 text-[11px] text-[var(--warn-text)] hover:opacity-90 transition-opacity"
            style={{ "--wails-draggable": "no-drag" } as any}
            onClick={onUndoRevert}
            title={t("header.undoRevertTooltip")}
          >
            <RotateCcw size={11} />
            {t("header.undoRevert")}
          </button>
        </Show>

        <Show when={lspCount() !== null}>
          <span
            class="inline-flex items-center gap-1 rounded-full bg-[var(--bg-inset)] px-2 py-0.5 text-[10px] font-mono text-[var(--text-muted)]"
            title={t("header.lspTooltip")}
          >
            <Activity
              size={11}
              class={lspCount()! > 0 ? "text-[var(--warn-text)]" : "text-[var(--ok-text)]"}
            />
            LSP {lspCount()}
          </span>
        </Show>

        <button
          class="inline-flex items-center gap-1.5 rounded-full bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          style={{ "--wails-draggable": "no-drag" } as any}
          onClick={() => setWorkspace("providerModalOpen", true)}
          title={t("header.modelsTooltip")}
        >
          <KeyRound size={11.5} class="text-[var(--accent-primary)]" />
          <span>{t("header.modelsBtn")}</span>
          <Show when={workspace.connectedProviders.length > 0}>
            <span class="rounded-full bg-[var(--accent-surface)] px-1.5 py-[0.5px] font-mono text-[10px] text-[var(--accent-text)]">
              {workspace.connectedProviders.length}
            </span>
          </Show>
        </button>

        <div class="flex items-center gap-1.5 rounded-full bg-[var(--bg-inset)] px-2.5 py-1">
          <Show
            when={workspace.connected && isSessionBusy(workspace.activeSessionID)}
            fallback={
              <>
                <span
                  class={`inline-block w-1.5 h-1.5 rounded-full ${
                    workspace.connected
                      ? "bg-[var(--ok)]"
                      : "bg-[var(--warn)] animate-pulse"
                  }`}
                />
                <span class="text-[11px] text-[var(--text-secondary)]">
                  {workspace.connected ? t("common.ready") : t("common.connecting")}
                </span>
              </>
            }
          >
            <span class="text-[var(--accent-primary)] inline-flex items-center">
              <Spinner class="w-3.5 h-3.5" />
            </span>
            <TextShimmer
              text={t("common.working")}
              active={true}
              class="text-[11px] font-medium text-[var(--accent-primary)]"
            />
          </Show>
        </div>

        {/* Language Switcher */}
        <button
          type="button"
          class="inline-flex items-center justify-center gap-1 h-7 px-2 rounded-lg text-[11.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
          style={{ "--wails-draggable": "no-drag" } as any}
          onClick={toggleLocale}
          title={t("header.langTooltip")}
        >
          <Languages size={13.5} />
          <span>{currentLocaleMetadata().shortLabel}</span>
        </button>

        <button
          class="inline-flex items-center justify-center w-7 h-7 rounded-lg text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors"
          style={{ "--wails-draggable": "no-drag" } as any}
          onClick={toggleTheme}
          title={workspace.theme === "dark" ? t("header.themeLightTooltip") : t("header.themeDarkTooltip")}
        >
          {workspace.theme === "dark" ? <Sun size={15} /> : <Moon size={15} />}
        </button>

        <button
          class={`inline-flex items-center gap-1.5 h-7 px-2 rounded-lg text-[11px] font-medium transition-colors ${
            props.inspectorOpen
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
              : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
          }`}
          style={{ "--wails-draggable": "no-drag" } as any}
          onClick={props.onToggleInspector}
          title={props.inspectorOpen ? t("header.inspectorHide") : t("header.inspectorShow")}
        >
          {props.inspectorOpen ? <PanelRightClose size={14} /> : <PanelRightOpen size={14} />}
          <Show when={diffCount() > 0}>
            <span class="rounded-full bg-[var(--accent-surface)] px-1.5 py-[1px] font-mono text-[10px] text-[var(--accent-primary)]">
              {diffCount()}
            </span>
          </Show>
        </button>
      </div>
    </header>
  );
}
