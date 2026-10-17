// web/src/components/inspector/pierre-diff-empty.tsx
import { createMemo, Show } from "solid-js";
import {
  CheckCircle2,
  Compass,
  FileDiff,
  FolderOpen,
  GitBranch,
  GitCommitHorizontal,
  Hammer,
  LoaderCircle,
  RefreshCw,
  Terminal,
} from "lucide-solid";
import {
  activeDirectory,
  isSessionBusy,
  setWorkspace,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

export default function DiffEmptyState(props: {
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const busy = createMemo(() => isSessionBusy(workspace.activeSessionID));

  const worktree = createMemo(() => {
    const id = workspace.activeSessionID;
    return (
      (id ? workspace.worktreeBySession[id] : undefined) ||
      workspace.worktreeBySession["__root__"]
    );
  });

  const commitHash = createMemo(() => {
    const wt = worktree();
    const raw = wt?.headCommit || wt?.baseCommit || "";
    return raw ? raw.slice(0, 7) : "";
  });

  const dirPath = createMemo(
    () => worktree()?.directory || activeDirectory() || workspace.rootDir || "—"
  );

  return (
    <div class="flex flex-col items-center justify-center px-3 py-6 text-center space-y-4">
      {/* 1. Hero Visual & Empty State Description */}
      <div class="flex flex-col items-center max-w-[300px]">
        <Show
          when={!busy()}
          fallback={
            <div class="mb-3 flex h-12 w-12 items-center justify-center rounded-2xl border border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)] shadow-2xs">
              <LoaderCircle size={22} class="animate-spin" />
            </div>
          }
        >
          <div class="relative mb-3 flex h-12 w-12 items-center justify-center rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--accent-primary)] shadow-2xs">
            <FileDiff size={22} />
            <span class="absolute -bottom-1 -right-1 flex h-5 w-5 items-center justify-center rounded-full border-2 border-[var(--bg-panel)] bg-[var(--ok-surface)] text-[var(--ok-text)]">
              <CheckCircle2 size={11} />
            </span>
          </div>
        </Show>

        <h3 class="text-[13px] font-semibold text-[var(--text-primary)]">
          {busy() ? t("inspector.diffs.busyEmptyTitle") : t("inspector.diffs.empty")}
        </h3>
        <p class="mt-1 text-[11.5px] leading-relaxed text-[var(--text-muted)]">
          {busy() ? t("inspector.diffs.busyEmptyDesc") : t("inspector.diffs.emptyDesc")}
        </p>
      </div>

      {/* 2. Live Worktree & Git Context Card */}
      <div class="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 p-3 text-left space-y-2">
        <div class="flex items-center justify-between gap-2 pb-1 border-b border-[var(--border-subtle)]/70">
          <span class="text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
            {t("inspector.diffs.envCardTitle")}
          </span>
          <span
            class={`rounded-md px-1.5 py-0.5 text-[10px] font-medium ${
              worktree()?.isolated
                ? "border border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-text)]"
                : "border border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-secondary)]"
            }`}
          >
            {worktree()?.isolated
              ? t("inspector.diffs.envModeIsolated")
              : t("inspector.diffs.envModeMain")}
          </span>
        </div>

        {/* Working Tree Clean Status */}
        <div class="flex items-center justify-between gap-2 text-[11.5px]">
          <span class="text-[var(--text-muted)] shrink-0">
            {t("inspector.diffs.envStatusLabel")}
          </span>
          <div class="inline-flex items-center gap-1.5 font-medium text-[var(--ok-text)]">
            <span class="inline-block h-1.5 w-1.5 rounded-full bg-[var(--ok)]" />
            <span>{t("inspector.diffs.envStatusClean")}</span>
            <span class="font-mono text-[10.5px] text-[var(--text-faint)]">+0 -0</span>
          </div>
        </div>

        {/* Active Git Branch */}
        <Show when={worktree()?.branch}>
          <div class="flex items-center justify-between gap-2 text-[11.5px]">
            <span class="text-[var(--text-muted)] shrink-0">
              {t("inspector.diffs.envBranchLabel")}
            </span>
            <span class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 font-mono text-[11px] font-medium text-[var(--text-primary)] max-w-[200px] truncate">
              <GitBranch size={11} class="text-[var(--accent-primary)] shrink-0" />
              <span class="truncate">{worktree()?.branch}</span>
            </span>
          </div>
        </Show>

        {/* Latest / Base Commit */}
        <Show when={commitHash()}>
          <div class="flex items-center justify-between gap-2 text-[11.5px]">
            <span class="text-[var(--text-muted)] shrink-0">
              {t("inspector.diffs.envCommitLabel")}
            </span>
            <div class="flex items-center gap-1.5 min-w-0 justify-end">
              <span class="inline-flex items-center gap-1 rounded bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--accent-text)] shrink-0">
                <GitCommitHorizontal size={11} />
                {commitHash()}
              </span>
              <Show when={worktree()?.headSubject}>
                <span
                  class="truncate text-[11px] text-[var(--text-secondary)] max-w-[145px]"
                  title={worktree()?.headSubject}
                >
                  {worktree()?.headSubject}
                </span>
              </Show>
            </div>
          </div>
        </Show>

        {/* Directory Path */}
        <div class="pt-1 border-t border-[var(--border-subtle)]/60 flex items-center gap-1.5 text-[10.5px] text-[var(--text-faint)] font-mono">
          <FolderOpen size={11} class="shrink-0 text-[var(--text-muted)]" />
          <span class="truncate" title={dirPath()}>
            {dirPath()}
          </span>
        </div>
      </div>

      {/* 3. Read-Only Plan Mode Hint Banner */}
      <Show when={workspace.activeMode === "plan"}>
        <div class="w-full rounded-xl border border-[var(--accent-border)]/70 bg-[var(--accent-surface)]/35 p-3 text-left space-y-2">
          <div class="flex items-center gap-1.5 text-[11.5px] font-semibold text-[var(--accent-primary)]">
            <Compass size={13} class="shrink-0" />
            <span>{t("inspector.diffs.planModeTipTitle")}</span>
          </div>
          <p class="text-[11px] leading-relaxed text-[var(--text-secondary)]">
            {t("inspector.diffs.planModeTipDesc")}
          </p>
          <button
            type="button"
            onClick={() => setWorkspace("activeMode", "build")}
            class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-white hover:opacity-90 transition-opacity cursor-pointer"
          >
            <Hammer size={11} />
            <span>{t("inspector.diffs.switchToBuildBtn")}</span>
          </button>
        </div>
      </Show>

      {/* 4. Quick Action Bar */}
      <div class="w-full flex flex-wrap items-center justify-center gap-1.5 pt-0.5">
        <button
          type="button"
          disabled={props.refreshing}
          onClick={() => props.onRefresh()}
          class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-2.5 py-1.5 text-[11px] font-medium text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
        >
          <RefreshCw size={12} class={props.refreshing ? "animate-spin text-[var(--accent-primary)]" : "text-[var(--text-muted)]"} />
          <span>
            {props.refreshing
              ? t("inspector.diffs.checkingChangesBtn")
              : t("inspector.diffs.checkChangesBtn")}
          </span>
        </button>

        <button
          type="button"
          onClick={() => setWorkspace("inspectorTab", "files")}
          class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] px-2.5 py-1.5 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
        >
          <FolderOpen size={12} class="text-[var(--text-muted)]" />
          <span>{t("inspector.diffs.browseFilesBtn")}</span>
        </button>

        <button
          type="button"
          onClick={() => setWorkspace("inspectorTab", "terminal")}
          class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] px-2.5 py-1.5 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
        >
          <Terminal size={12} class="text-[var(--text-muted)]" />
          <span>{t("inspector.diffs.openTerminalBtn")}</span>
        </button>
      </div>
    </div>
  );
}
