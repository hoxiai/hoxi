// web/src/components/timeline/ComposerSandboxPill.tsx
import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import {
  ArrowUpFromLine,
  Check,
  ChevronDown,
  FileDiff,
  Folder,
  GitBranch,
  GitCommitHorizontal,
  GitFork,
  LoaderCircle,
  ShieldCheck,
} from "lucide-solid";
import {
  canForkSession,
  executeTaskCommitAndPush,
  openDiffInInspector,
  refreshWorktreeStatus,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import { collectCycleChangedFiles } from "./tool-card-state";
import { WorktreeVerifySection } from "./worktree-verify-section";
import CommandSandboxToggle from "./CommandSandboxToggle";
import SandboxLifecycleSection from "./SandboxLifecycleSection";

export function ComposerSandboxPill(props: {
  mode: "local" | "worktree";
  switching: boolean;
  busy: boolean;
  onToggle: (target: "local" | "worktree") => void;
  sessionID?: string | null;
  directory?: string | null;
  forking?: boolean;
  onFork?: () => void;
}) {
  const [open, setOpen] = createSignal(false);
  const [gitAction, setGitAction] = createSignal<"commit-push" | "commit" | "push" | null>(null);
  const [toastMsg, setToastMsg] = createSignal<string | null>(null);
  const [errorMsg, setErrorMsg] = createSignal<string | null>(null);
  const [commandSandboxOn, setCommandSandboxOn] = createSignal(false);

  let buttonRef: HTMLButtonElement | undefined;
  let menuRef: HTMLDivElement | undefined;

  createEffect(() => {
    if (!open()) return;
    void refreshWorktreeStatus(props.sessionID);
    const handleClick = (e: MouseEvent) => {
      const target = e.target as Node;
      if (
        menuRef &&
        !menuRef.contains(target) &&
        buttonRef &&
        !buttonRef.contains(target)
      ) {
        setOpen(false);
      }
    };
    window.addEventListener("pointerdown", handleClick);
    onCleanup(() => window.removeEventListener("pointerdown", handleClick));
  });

  const activeWorktree = createMemo(() => {
    const id = props.sessionID;
    return workspace.worktreeBySession[id || "__root__"];
  });

  const canFork = createMemo(() => canForkSession(props.sessionID));
  const gitBranch = createMemo(() => activeWorktree()?.branch || "");
  const changedFilesCount = createMemo(() => {
    const id = props.sessionID;
    if (activeWorktree()?.dirty === false && !props.busy) return 0;
    if (id) {
      const sDiffs = workspace.diffsBySession[id] ?? [];
      if (sDiffs.length > 0) return sDiffs.length;
      const turns = workspace.messagesBySession[id] ?? [];
      if (turns.length > 0) return collectCycleChangedFiles(turns, id).length;
      return 0;
    }
    return (workspace.diffsBySession["__root__"] ?? []).length;
  });
  const isDirty = createMemo(() =>
    props.mode === "worktree" || !props.sessionID
      ? Boolean(activeWorktree()?.dirty || changedFilesCount() > 0)
      : changedFilesCount() > 0
  );
  const aheadCount = createMemo(() => activeWorktree()?.aheadCount || 0);
  const canCommitOrPush = createMemo(
    () => isDirty() || aheadCount() > 0 || (Boolean(gitBranch()) && !activeWorktree()?.hasUpstream)
  );

  const handleGitCommitAction = async (push: boolean) => {
    if (gitAction() || props.busy || props.switching) return;
    const mode = !isDirty() && push ? "push" : push ? "commit-push" : "commit";
    setGitAction(mode);
    setErrorMsg(null);
    setToastMsg(null);
    try {
      await executeTaskCommitAndPush(props.sessionID, { push });
      setToastMsg(t("timeline.gitTaskRequested"));
      setTimeout(() => setToastMsg(null), 4000);
    } catch (err: any) {
      setErrorMsg(err?.message || String(err));
    } finally {
      setGitAction(null);
    }
  };

  return (
    <div class="relative inline-block select-none">
      <button
        ref={buttonRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        class={`inline-flex items-center rounded-lg transition-colors cursor-pointer ${
          props.mode === "worktree"
            ? "gap-1.5 px-2 py-1 text-[12px] font-medium"
            : "p-1.5"
        } ${
          open()
            ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
            : props.mode === "worktree"
              ? "text-[var(--accent-primary)] hover:bg-[var(--accent-surface)]"
              : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
        }`}
        title={
          props.mode === "worktree"
              ? t("timeline.sandboxWorktreeTooltip")
              : t("timeline.sandboxLocalTooltip")
        }
      >
        <Show
          when={!props.switching && !gitAction()}
          fallback={<LoaderCircle size={12} class="animate-spin shrink-0" />}
        >
          <GitBranch
            size={12.5}
            class={`shrink-0 ${
              props.mode === "worktree"
                ? "text-[var(--accent-primary)]"
                : "text-[var(--text-muted)]"
            }`}
          />
        </Show>

        <Show when={props.mode === "worktree"}>
          <span>{t("timeline.sandboxWorktreeMode")}</span>
        </Show>

        <Show when={changedFilesCount() > 0}>
          <span class="rounded-full bg-[var(--accent-surface)] px-1.5 py-0.2 font-mono text-[10px] text-[var(--accent-primary)]">
            {changedFilesCount()}
          </span>
        </Show>

        <Show when={activeWorktree()?.setupStatus === "failed"}>
          <span
            class="h-1.5 w-1.5 rounded-full bg-rose-500 shrink-0"
            title={t("timeline.worktreeSetupFailed")}
          />
        </Show>
        <Show when={activeWorktree()?.runStatus === "running"}>
          <span
            class="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse shrink-0"
            title={t("timeline.verifyRunBtn")}
          />
        </Show>

        <Show when={commandSandboxOn()}>
          <span
            class="shrink-0 text-[var(--accent-primary)]"
            title={t("timeline.commandSandboxOn")}
          >
            <ShieldCheck size={11.5} />
          </span>
        </Show>

        <ChevronDown
          size={11}
          class={`text-[var(--text-muted)] transition-transform duration-200 ${
            open() ? "rotate-180" : ""
          }`}
        />
      </button>

      <Show when={open()}>
        <div
          ref={menuRef}
          class="absolute bottom-full left-0 mb-2 w-[304px] max-h-[min(70vh,640px)] overflow-y-auto rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-2xl z-50 animate-in fade-in zoom-in-95 duration-100"
        >
          {/* Mode Options */}
          <div class="space-y-0.5">
            <button
              type="button"
              disabled={props.switching || props.busy}
              onClick={() => {
                props.onToggle("local");
                setOpen(false);
              }}
              class={`w-full flex items-start gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed ${
                props.mode === "local"
                  ? "bg-[var(--accent-surface)] text-[var(--text-primary)]"
                  : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
              }`}
            >
              <div
                class={`mt-0.5 p-1 rounded-lg shrink-0 ${
                  props.mode === "local"
                    ? "bg-[var(--accent-primary)] text-[var(--accent-foreground)]"
                    : "bg-[var(--bg-inset)] text-[var(--text-muted)]"
                }`}
              >
                <Folder size={12} />
              </div>
              <div class="flex-1 min-w-0">
                <div class="text-[12px] font-medium">
                  {t("timeline.sandboxLocalMode")}
                </div>
                <p class="mt-0.5 text-[11px] leading-snug text-[var(--text-muted)]">
                  {t("timeline.sandboxLocalTooltip")}
                </p>
              </div>
              <Show when={props.mode === "local"}>
                <Check size={14} class="shrink-0 text-[var(--accent-primary)] mt-1" />
              </Show>
            </button>

            <button
              type="button"
              disabled={props.switching || props.busy}
              onClick={() => {
                props.onToggle("worktree");
                setOpen(false);
              }}
              class={`w-full flex items-start gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors cursor-pointer disabled:opacity-50 ${
                props.mode === "worktree"
                  ? "bg-[var(--accent-surface)] text-[var(--text-primary)]"
                  : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
              }`}
            >
              <div
                class={`mt-0.5 p-1 rounded-lg shrink-0 ${
                  props.mode === "worktree"
                    ? "bg-[var(--accent-primary)] text-[var(--accent-foreground)]"
                    : "bg-[var(--bg-inset)] text-[var(--text-muted)]"
                }`}
              >
                <GitBranch size={12} />
              </div>
              <div class="flex-1 min-w-0">
                <div class="text-[12px] font-medium">
                  {t("timeline.sandboxWorktreeMode")}
                </div>
                <p class="mt-0.5 text-[11px] leading-snug text-[var(--text-muted)]">
                  {t("timeline.sandboxWorktreeTooltip")}
                </p>
              </div>
              <Show when={props.mode === "worktree"}>
                <Check size={14} class="shrink-0 text-[var(--accent-primary)] mt-1" />
              </Show>
            </button>
          </div>

          {/* OS-level Command Confinement */}
          <CommandSandboxToggle
            sessionID={props.sessionID}
            directory={props.directory}
            busy={props.busy || props.switching}
            onStateChange={(next) => setCommandSandboxOn(next.enabled)}
          />

          {/* One-Click Git Commit & Push Section */}
          <div class="mt-1.5 pt-1.5 border-t border-[var(--border-subtle)] px-1 space-y-1.5">
            <Show when={errorMsg()}>
              <div class="rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2 py-1 text-[11px] text-[var(--danger-text)] break-words">
                {errorMsg()}
              </div>
            </Show>

            <Show when={toastMsg()}>
              <div class="flex items-center gap-1.5 rounded-lg border border-[var(--ok)]/30 bg-[var(--ok-surface)] px-2 py-1 text-[11px] text-[var(--ok-text)]">
                <Check size={12} class="shrink-0" />
                <span class="truncate">{toastMsg()}</span>
              </div>
            </Show>

            <div class="flex items-center gap-1.5">
              <button
                type="button"
                disabled={!canCommitOrPush() || Boolean(gitAction()) || props.busy || props.switching}
                onClick={() => void handleGitCommitAction(true)}
                class="flex-1 min-w-0 flex items-center justify-between gap-1.5 rounded-xl bg-[var(--accent-primary)] px-2.5 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-40 disabled:cursor-not-allowed transition-opacity cursor-pointer"
              >
                <span class="inline-flex items-center gap-1.5 min-w-0 truncate">
                  <Show
                    when={gitAction() !== "commit-push" && gitAction() !== "push"}
                    fallback={<LoaderCircle size={13} class="animate-spin shrink-0" />}
                  >
                    <ArrowUpFromLine size={13} class="shrink-0" />
                  </Show>
                  <span class="truncate">
                    {gitAction() === "commit-push"
                      ? t("timeline.gitCommittingAndPushing")
                      : gitAction() === "push"
                        ? t("timeline.gitPushingOnly")
                        : !isDirty() && aheadCount() > 0
                          ? t("timeline.gitPushOnly")
                          : t("timeline.gitCommitAndPush")}
                  </span>
                </span>

                <Show when={changedFilesCount() > 0}>
                  <span class="shrink-0 rounded-md bg-white/20 px-1.5 py-0.5 font-mono text-[10px]">
                    {t("timeline.filesAttached", { count: changedFilesCount() })}
                  </span>
                </Show>
                <Show when={!isDirty() && aheadCount() > 0}>
                  <span class="shrink-0 rounded-md bg-white/20 px-1.5 py-0.5 font-mono text-[10px]">
                    {t("timeline.gitAheadCountBadge", { count: aheadCount() })}
                  </span>
                </Show>
              </button>

              <Show when={isDirty()}>
                <button
                  type="button"
                  disabled={Boolean(gitAction()) || props.busy || props.switching}
                  onClick={() => void handleGitCommitAction(false)}
                  title={t("timeline.gitCommitOnly")}
                  class="shrink-0 inline-flex items-center gap-1 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-2.5 py-1.5 text-[11.5px] font-medium text-[var(--text-primary)] disabled:opacity-40 transition-colors cursor-pointer"
                >
                  <Show
                    when={gitAction() !== "commit"}
                    fallback={<LoaderCircle size={12} class="animate-spin shrink-0" />}
                  >
                    <GitCommitHorizontal size={13} class="shrink-0 text-[var(--text-secondary)]" />
                  </Show>
                  <span>{t("timeline.gitCommitOnly")}</span>
                </button>
              </Show>
            </div>

            <p class="px-1 py-0.5 text-[11px] leading-snug text-[var(--text-muted)]">
              {t("timeline.gitAgentCommitHint")}
            </p>
          </div>

          {/* Worktree Setup Status/Retry & One-Click Run/Test/Preview */}
          <WorktreeVerifySection
            sessionID={props.sessionID}
            directory={activeWorktree()?.directory || workspace.rootDir || undefined}
            onCloseMenu={() => setOpen(false)}
          />

          <SandboxLifecycleSection
            sessionID={props.sessionID}
            busy={props.busy || props.switching || Boolean(gitAction())}
            onClose={() => setOpen(false)}
          />

          {/* Git Branch, Diff Review & Session Fork */}
          <Show when={gitBranch() || props.sessionID}>
            <div class="mt-1.5 pt-1.5 border-t border-[var(--border-subtle)] space-y-0.5">
              <Show when={gitBranch()}>
                <button
                  type="button"
                  onClick={() => {
                    openDiffInInspector(
                      undefined,
                      props.sessionID ? "session" : "worktree"
                    );
                    setOpen(false);
                  }}
                  class="w-full flex items-center justify-between gap-2 rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                >
                  <div class="flex items-center gap-2 min-w-0">
                    <FileDiff size={13} class="text-[var(--accent-primary)] shrink-0" />
                    <span class="truncate">{t("timeline.reviewDiffs")}</span>
                  </div>
                </button>
              </Show>

              <Show when={props.sessionID && props.onFork}>
                <button
                  type="button"
                  disabled={props.forking || !canFork()}
                  title={!canFork() ? t("timeline.runnerFeatureUnsupported") : undefined}
                  onClick={() => {
                    props.onFork?.();
                    setOpen(false);
                  }}
                  class="w-full flex items-center justify-between gap-2 rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors cursor-pointer"
                >
                  <div class="flex items-center gap-2 min-w-0">
                    <Show
                      when={!props.forking}
                      fallback={<LoaderCircle size={13} class="animate-spin shrink-0" />}
                    >
                      <GitFork size={13} class="text-[var(--text-muted)] shrink-0" />
                    </Show>
                    <span class="truncate">{t("timeline.forkHere")}</span>
                  </div>
                </button>
              </Show>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}
