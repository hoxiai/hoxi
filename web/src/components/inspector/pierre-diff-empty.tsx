// web/src/components/inspector/pierre-diff-empty.tsx
import { createMemo, Show } from "solid-js";
import {
  CheckCircle2,
  Compass,
  FileDiff,
  GitBranch,
  GitCommitHorizontal,
  Hammer,
  LoaderCircle,
  RefreshCw,
} from "lucide-solid";
import {
  activeDirectory,
  isSessionBusy,
  setActiveAgent,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

export default function DiffEmptyState(props: {
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const busy = createMemo(() => isSessionBusy(workspace.activeSessionID));
  const sessionScope = () => workspace.diffScope === "session";

  const worktree = createMemo(
    () => workspace.worktreeBySession[workspace.activeSessionID || "__root__"]
  );
  const dirty = () => worktree()?.dirty;

  const emptyTitle = () =>
    sessionScope()
      ? t("inspector.diffs.sessionEmpty")
      : dirty() === false
        ? t("inspector.diffs.cleanStatus")
        : t("inspector.diffs.empty");

  // Only show a subtitle when it adds actionable context beyond the title.
  const emptyDescription = () =>
    sessionScope()
      ? t("inspector.diffs.sessionEmptyDesc")
      : dirty() === true
        ? t("inspector.diffs.worktreeDirtyEmptyDesc")
        : dirty() === false
          ? ""
          : t("inspector.diffs.worktreeUnknownEmptyDesc");

  const needsManualRefresh = () => !sessionScope() && dirty() !== false;

  const commitHash = createMemo(() => {
    const wt = worktree();
    const raw = wt?.headCommit || wt?.baseCommit || "";
    return raw ? raw.slice(0, 7) : "";
  });

  const dirPath = createMemo(
    () => worktree()?.directory || activeDirectory() || ""
  );

  const hasGitContext = createMemo(() =>
    Boolean(worktree()?.branch || commitHash() || typeof worktree()?.isolated === "boolean")
  );

  return (
    <div class="flex flex-col items-center justify-center px-3 py-6 text-center space-y-3.5">
      {/* Hero Visual & Concise Status */}
      <div class="flex flex-col items-center max-w-[280px]">
        <Show
          when={!busy()}
          fallback={
            <div class="mb-2.5 flex h-10 w-10 items-center justify-center rounded-2xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
              <LoaderCircle size={18} class="animate-spin" />
            </div>
          }
        >
          <div class="relative mb-2.5 flex h-10 w-10 items-center justify-center rounded-2xl bg-[var(--bg-inset)] text-[var(--text-secondary)]">
            <FileDiff size={18} />
            <Show when={!sessionScope() && dirty() === false}>
              <span class="absolute -bottom-1 -right-1 flex h-4 w-4 items-center justify-center rounded-full border-2 border-[var(--bg-panel)] bg-[var(--ok-surface)] text-[var(--ok-text)]">
                <CheckCircle2 size={10} />
              </span>
            </Show>
          </div>
        </Show>

        <h3 class="text-[13px] font-semibold text-[var(--text-primary)]">
          {emptyTitle()}
        </h3>
        <Show when={emptyDescription()}>
          <p class="mt-1 text-[11.5px] leading-relaxed text-[var(--text-muted)]">
            {emptyDescription()}
          </p>
        </Show>
      </div>

      {/* Compact Git Context Strip */}
      <Show when={hasGitContext()}>
        <div
          class="w-full max-w-[320px] rounded-xl bg-[var(--bg-inset)]/50 px-3 py-2 text-left space-y-1.5"
          title={dirPath() || undefined}
        >
          <Show when={worktree()?.branch || typeof worktree()?.isolated === "boolean"}>
            <div class="flex items-center justify-between gap-2">
              <Show
                when={worktree()?.branch}
                fallback={<span class="text-[11px] text-[var(--text-faint)]">—</span>}
              >
                <span class="inline-flex min-w-0 items-center gap-1 font-mono text-[11.5px] font-medium text-[var(--text-primary)]">
                  <GitBranch size={11} class="text-[var(--accent-primary)] shrink-0" />
                  <span class="truncate">{worktree()?.branch}</span>
                </span>
              </Show>
              <Show when={typeof worktree()?.isolated === "boolean"}>
                <span
                  class={`shrink-0 rounded-md px-1.5 py-0.5 text-[10.5px] font-medium ${
                    worktree()?.isolated
                      ? "bg-[var(--accent-surface)] text-[var(--accent-text)]"
                      : "bg-[var(--bg-panel)]/80 text-[var(--text-muted)]"
                  }`}
                >
                  {worktree()?.isolated
                    ? t("inspector.diffs.envModeIsolated")
                    : t("inspector.diffs.envModeMain")}
                </span>
              </Show>
            </div>
          </Show>

          <Show when={commitHash()}>
            <div class="flex items-center gap-1.5 min-w-0 text-[11px] text-[var(--text-muted)]">
              <span class="inline-flex items-center gap-1 font-mono text-[var(--accent-text)] shrink-0">
                <GitCommitHorizontal size={11} />
                {commitHash()}
              </span>
              <Show when={worktree()?.headSubject}>
                <span class="truncate" title={worktree()?.headSubject}>
                  {worktree()?.headSubject}
                </span>
              </Show>
            </div>
          </Show>
        </div>
      </Show>

      {/* Read-Only Plan Mode Hint Banner */}
      <Show when={workspace.activeMode === "plan"}>
        <div class="w-full max-w-[320px] rounded-xl bg-[var(--accent-surface)]/45 p-2.5 text-left space-y-1.5">
          <div class="flex items-center gap-1.5 text-[11.5px] font-semibold text-[var(--accent-primary)]">
            <Compass size={12} class="shrink-0" />
            <span>{t("inspector.diffs.planModeTipTitle")}</span>
          </div>
          <p class="text-[11px] leading-relaxed text-[var(--text-secondary)]">
            {t("inspector.diffs.planModeTipDesc")}
          </p>
          <button
            type="button"
            onClick={() => setActiveAgent("build")}
            class="inline-flex items-center gap-1 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 transition-opacity cursor-pointer"
          >
            <Hammer size={11} />
            <span>{t("inspector.diffs.switchToBuildBtn")}</span>
          </button>
        </div>
      </Show>

      {/* Actionable Refresh Button (only when dirty/unknown; clean state uses toolbar refresh) */}
      <Show when={needsManualRefresh()}>
        <button
          type="button"
          disabled={props.refreshing}
          onClick={() => props.onRefresh()}
          class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] px-3 py-1.5 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
        >
          <RefreshCw
            size={12}
            class={props.refreshing ? "animate-spin text-[var(--accent-primary)]" : "text-[var(--text-muted)]"}
          />
          <span>
            {props.refreshing
              ? t("inspector.diffs.checkingChangesBtn")
              : t("inspector.diffs.checkChangesBtn")}
          </span>
        </button>
      </Show>
    </div>
  );
}
