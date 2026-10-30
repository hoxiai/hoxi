// web/src/components/timeline/SandboxMergePromptCard.tsx
import { createEffect, createMemo, createSignal, Show } from "solid-js";
import {
  Check,
  FolderGit2,
  GitBranch,
  GitMerge,
  GitPullRequestArrow,
  LoaderCircle,
  Sparkles,
  X,
} from "lucide-solid";
import type { SnapshotFileDiff } from "../../services/sse-types";
import {
  appSettings,
  applyAndExitSessionWorktree,
  applySessionWorktree,
  dismissSandboxMergePrompt,
  generateSmartCommitMessage,
  isSessionBusy,
  refreshWorktreeStatus,
  sessionGoal,
  syncSessionWorktree,
  synthesizeLocalCommitMessage,
  uncommittedSessionFiles,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

export default function SandboxMergePromptCard(props: {
  sessionID: string;
  messageID: string;
  changedFiles: SnapshotFileDiff[];
}) {
  const [commitOpen, setCommitOpen] = createSignal(false);
  const [commitMsg, setCommitMsg] = createSignal("");
  const [generatingMsg, setGeneratingMsg] = createSignal(false);
  const [busyAction, setBusyAction] = createSignal<
    "merge-exit" | "patch-exit" | "apply-keep" | "sync-base" | null
  >(null);
  const [errorMsg, setErrorMsg] = createSignal<string | null>(null);
  const [notice, setNotice] = createSignal<string | null>(null);
  let inputRef: HTMLInputElement | undefined;

  const dismissKey = createMemo(() => `${props.sessionID}:${props.messageID}`);
  const activeWorktree = createMemo(() => workspace.worktreeBySession[props.sessionID]);
  const isolated = createMemo(() => Boolean(activeWorktree()?.isolated));
  const sandboxBranch = createMemo(
    () => activeWorktree()?.branch || `hoxi/${props.sessionID}`
  );
  const baseBranch = createMemo(
    () =>
      activeWorktree()?.baseBranch ||
      workspace.worktreeBySession["__root__"]?.branch ||
      "main"
  );
  const behindCount = createMemo(() => activeWorktree()?.behindCount ?? 0);
  const aheadCount = createMemo(() => activeWorktree()?.aheadCount ?? 0);
  const isPromotedBranch = createMemo(() => {
    const br = activeWorktree()?.branch || "";
    return Boolean(br && !br.startsWith("hoxi/"));
  });

  const effectiveDiffs = createMemo(() => {
    const sessionDiffs = uncommittedSessionFiles(props.sessionID);
    return sessionDiffs.length > 0 ? sessionDiffs : props.changedFiles;
  });

  const dirtyCount = createMemo(() =>
    Math.max(
      activeWorktree()?.changedFiles ?? 0,
      effectiveDiffs().length
    )
  );

  const hasUnmergedWork = createMemo(
    () =>
      Boolean(activeWorktree()?.dirty) ||
      dirtyCount() > 0 ||
      aheadCount() > 0
  );

  const isIdleAndSettled = createMemo(() => {
    const sid = props.sessionID;
    if (!sid || isSessionBusy(sid)) return false;
    if (workspace.interruptedBySession[sid]) return false;
    if (workspace.pendingPermissions.some((p) => p.sessionID === sid)) return false;
    if (workspace.pendingQuestions.some((q) => q.sessionID === sid)) return false;
    if ((workspace.queuedPromptsBySession[sid]?.length ?? 0) > 0) return false;
    if (sessionGoal(sid)?.status === "active") return false;
    return true;
  });

  // Ensure worktree status is fresh when the turn finishes
  let refreshedForTurn = "";
  createEffect(() => {
    const key = dismissKey();
    if (isIdleAndSettled() && isolated() && refreshedForTurn !== key) {
      refreshedForTurn = key;
      void refreshWorktreeStatus(props.sessionID);
    }
  });

  const visible = createMemo(() => {
    if (workspace.dismissedSandboxMergePrompts[dismissKey()]) return false;
    if (notice()) return true;
    return isIdleAndSettled() && isolated() && hasUnmergedWork();
  });

  const metaDescription = createMemo(() => {
    const files = dirtyCount();
    const commits = aheadCount();
    if (files > 0 && commits > 0) {
      return t("timeline.sandboxMergePromptMetaBoth", {
        sandbox: sandboxBranch(),
        files,
        commits,
      });
    }
    if (commits > 0 && files === 0) {
      return t("timeline.sandboxMergePromptMetaAhead", {
        sandbox: sandboxBranch(),
        branch: baseBranch(),
        count: commits,
      });
    }
    return t("timeline.sandboxMergePromptMetaDirty", {
      sandbox: sandboxBranch(),
      count: Math.max(1, files),
    });
  });

  const populateSmartCommitMessage = async (forceOverwrite = false) => {
    const diffs = effectiveDiffs();
    const fallback = synthesizeLocalCommitMessage(props.sessionID, diffs);
    if (!commitMsg().trim() || forceOverwrite) {
      setCommitMsg(fallback);
    }
    setGeneratingMsg(true);
    try {
      const smart = await generateSmartCommitMessage(props.sessionID, diffs);
      if (smart?.trim()) {
        setCommitMsg(smart.trim());
      }
    } catch {
      /* keep local fallback */
    } finally {
      setGeneratingMsg(false);
    }
  };

  const onOpenMergeCommitBar = () => {
    if (busyAction()) return;
    setErrorMsg(null);
    setNotice(null);
    if (!commitOpen()) {
      setCommitOpen(true);
      void populateSmartCommitMessage(false);
      queueMicrotask(() => inputRef?.focus());
      return;
    }
    void onConfirmMergeAndExit();
  };

  const runAction = async (
    action: "merge-exit" | "patch-exit" | "apply-keep" | "sync-base",
    fn: () => Promise<void>
  ) => {
    if (busyAction()) return;
    setBusyAction(action);
    setErrorMsg(null);
    setNotice(null);
    try {
      await fn();
    } catch (err: any) {
      setErrorMsg(err?.message || t("timeline.sandboxApplyFailed"));
    } finally {
      setBusyAction(null);
    }
  };

  const onConfirmMergeAndExit = () =>
    runAction("merge-exit", async () => {
      const fallback = synthesizeLocalCommitMessage(
        props.sessionID,
        effectiveDiffs()
      );
      const finalMessage = commitMsg().trim() || fallback;
      const targetBranch = baseBranch();
      await applyAndExitSessionWorktree(
        props.sessionID,
        "merge",
        isPromotedBranch(),
        finalMessage
      );
      setCommitOpen(false);
      setNotice(
        t("timeline.sandboxMergeCompletedNotice", { branch: targetBranch })
      );
    });

  const onPatchAndExit = () =>
    runAction("patch-exit", async () => {
      const targetBranch = baseBranch();
      await applyAndExitSessionWorktree(
        props.sessionID,
        "patch",
        isPromotedBranch()
      );
      setCommitOpen(false);
      setNotice(
        t("timeline.sandboxPatchCompletedNotice", { branch: targetBranch })
      );
    });

  const onApplyKeepSandbox = () =>
    runAction("apply-keep", async () => {
      await applySessionWorktree(props.sessionID, appSettings.worktreeApplyMode);
      setNotice(t("timeline.sandboxAppliedSuccess"));
    });

  const onSyncWithBase = () =>
    runAction("sync-base", async () => {
      await syncSessionWorktree(props.sessionID);
      setNotice(
        t("timeline.sandboxSyncedBaseSuccess", { branch: baseBranch() })
      );
    });

  return (
    <Show when={visible()}>
      <div
        data-sandbox-merge-prompt
        class="rounded-2xl border border-[var(--accent-border)] bg-[var(--bg-panel)] px-4 py-3 shadow-2xs transition-colors"
      >
        <div class="flex items-start justify-between gap-3 flex-wrap">
          <div class="flex items-center gap-3 min-w-0">
            <div class="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]">
              <GitMerge size={16} />
            </div>
            <div class="min-w-0">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="text-[13px] font-semibold text-[var(--text-primary)]">
                  {t("timeline.sandboxMergePromptTitle", {
                    branch: baseBranch(),
                  })}
                </span>
              </div>
              <div class="mt-0.5 flex items-center gap-1.5 text-[11.5px] font-mono text-[var(--text-muted)] truncate">
                <GitBranch size={11} class="shrink-0 text-[var(--accent-primary)]" />
                <span class="truncate">{metaDescription()}</span>
              </div>
            </div>
          </div>

          <div class="flex items-center gap-1.5 flex-wrap ml-auto">
            <Show when={isolated()}>
              <button
                type="button"
                disabled={!!busyAction()}
                onClick={onOpenMergeCommitBar}
                class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] hover:opacity-95 px-3 py-1.5 text-[12px] font-semibold text-[var(--accent-primary)] disabled:opacity-50 transition-opacity cursor-pointer"
              >
                <Show
                  when={busyAction() !== "merge-exit"}
                  fallback={<LoaderCircle size={12} class="animate-spin shrink-0" />}
                >
                  <GitMerge size={12} class="shrink-0" />
                </Show>
                <span>
                  {t("timeline.sandboxMergeCommitExitBtn", {
                    branch: baseBranch(),
                  })}
                </span>
              </button>

              <button
                type="button"
                disabled={!!busyAction()}
                onClick={() => void onPatchAndExit()}
                class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] hover:border-[var(--border-strong)] px-2.5 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
              >
                <Show
                  when={busyAction() !== "patch-exit"}
                  fallback={<LoaderCircle size={12} class="animate-spin shrink-0" />}
                >
                  <FolderGit2 size={12} class="shrink-0 text-[var(--text-muted)]" />
                </Show>
                <span>
                  {t("timeline.sandboxPatchExitBtn", {
                    branch: baseBranch(),
                  })}
                </span>
              </button>

              <button
                type="button"
                disabled={!!busyAction()}
                onClick={() => void onApplyKeepSandbox()}
                class="inline-flex items-center gap-1.5 rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
              >
                <Show
                  when={busyAction() !== "apply-keep"}
                  fallback={<LoaderCircle size={12} class="animate-spin shrink-0" />}
                >
                  <Check size={12} class="shrink-0" />
                </Show>
                <span>
                  {t("timeline.sandboxMergeKeepBtn", {
                    branch: baseBranch(),
                  })}
                </span>
              </button>

              <Show when={behindCount() > 0}>
                <button
                  type="button"
                  disabled={!!busyAction()}
                  onClick={() => void onSyncWithBase()}
                  class="inline-flex items-center gap-1 rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] disabled:opacity-50 transition-colors cursor-pointer"
                >
                  <Show
                    when={busyAction() !== "sync-base"}
                    fallback={<LoaderCircle size={12} class="animate-spin shrink-0" />}
                  >
                    <GitPullRequestArrow size={12} class="shrink-0" />
                  </Show>
                  <span>
                    {t("timeline.sandboxSyncBaseBtn", {
                      branch: baseBranch(),
                      count: behindCount(),
                    })}
                  </span>
                </button>
              </Show>
            </Show>

            <button
              type="button"
              onClick={() => dismissSandboxMergePrompt(dismissKey())}
              title={t("common.close")}
              aria-label={t("common.close")}
              class="inline-flex h-7 w-7 items-center justify-center rounded-lg text-[var(--text-faint)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-secondary)] transition-colors cursor-pointer"
            >
              <X size={13} />
            </button>
          </div>
        </div>

        <Show when={commitOpen() && isolated()}>
          <div class="mt-2.5 pt-2.5 border-t border-[var(--border-subtle)] flex items-center gap-2 flex-wrap">
            <div class="relative flex-1 min-w-[220px] flex items-center">
              <input
                ref={inputRef}
                type="text"
                value={commitMsg()}
                placeholder={t("timeline.sandboxMergeCommitPlaceholder")}
                disabled={!!busyAction()}
                onInput={(ev) => setCommitMsg(ev.currentTarget.value)}
                onKeyDown={(ev) => {
                  if (ev.key === "Enter" && !ev.isComposing) {
                    ev.preventDefault();
                    void onConfirmMergeAndExit();
                  } else if (ev.key === "Escape") {
                    ev.preventDefault();
                    setCommitOpen(false);
                  }
                }}
                class="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] pl-3 pr-8 py-1.5 text-[12px] font-mono text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
              />
              <button
                type="button"
                disabled={generatingMsg() || !!busyAction()}
                onClick={() => void populateSmartCommitMessage(true)}
                title={t("timeline.gitCommitModelLabel")}
                class="absolute right-2 inline-flex h-5 w-5 items-center justify-center rounded-md text-[var(--text-muted)] hover:text-[var(--accent-primary)] disabled:opacity-40 transition-colors cursor-pointer"
              >
                <Show
                  when={!generatingMsg()}
                  fallback={<LoaderCircle size={12} class="animate-spin" />}
                >
                  <Sparkles size={12} />
                </Show>
              </button>
            </div>

            <button
              type="button"
              disabled={!!busyAction()}
              onClick={() => void onConfirmMergeAndExit()}
              class="shrink-0 inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] hover:opacity-95 px-3 py-1.5 text-[12px] font-semibold text-white disabled:opacity-50 transition-opacity cursor-pointer"
            >
              <Show
                when={busyAction() !== "merge-exit"}
                fallback={<LoaderCircle size={12} class="animate-spin shrink-0" />}
              >
                <GitMerge size={12} class="shrink-0" />
              </Show>
              <span>{t("timeline.sandboxMergeConfirmBtn")}</span>
            </button>
          </div>
        </Show>

        <Show when={errorMsg()}>
          <div class="mt-2 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3 py-1.5 text-[11.5px] text-[var(--danger-text)] break-words">
            {errorMsg()}
          </div>
        </Show>

        <Show when={notice()}>
          <div class="mt-2 flex items-center gap-1.5 rounded-xl border border-[var(--ok)]/30 bg-[var(--ok-surface)] px-3 py-1.5 text-[11.5px] text-[var(--ok-text)] break-words">
            <Check size={12} class="shrink-0" />
            <span>{notice()}</span>
          </div>
        </Show>
      </div>
    </Show>
  );
}
