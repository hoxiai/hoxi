// web/src/components/timeline/SandboxLifecycleSection.tsx
import { createEffect, createMemo, createSignal, Show } from "solid-js";
import {
  GitMerge,
  GitPullRequestArrow,
  FolderGit2,
  GitBranch,
  LoaderCircle,
  Pencil,
  Trash2,
} from "lucide-solid";
import {
  activeSession,
  appSettings,
  applyAndExitSessionWorktree,
  applySessionWorktree,
  exitSessionWorktree,
  renameSessionBranch,
  syncSessionWorktree,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

export default function SandboxLifecycleSection(props: {
  sessionID?: string | null;
  busy: boolean;
  onClose: () => void;
}) {
  const [renameDraft, setRenameDraft] = createSignal("");
  const [busyAction, setBusyAction] = createSignal<string | null>(null);
  const [errorMsg, setErrorMsg] = createSignal<string | null>(null);
  const [notice, setNotice] = createSignal<string | null>(null);

  const session = createMemo(() => activeSession());
  const activeWorktree = createMemo(() => {
    const id = props.sessionID;
    return id ? workspace.worktreeBySession[id] : undefined;
  });
  const isolated = createMemo(() => Boolean(activeWorktree()?.isolated));
  const baseBranch = createMemo(
    () => activeWorktree()?.baseBranch || workspace.worktreeBySession["__root__"]?.branch || "main"
  );
  const behindCount = createMemo(() => activeWorktree()?.behindCount ?? 0);
  const isPromotedBranch = createMemo(() => {
    const br = activeWorktree()?.branch || "";
    return Boolean(br && !br.startsWith("hoxi/"));
  });

  createEffect(() => {
    const br = activeWorktree()?.branch || "";
    setRenameDraft(br && !br.startsWith("hoxi/") ? br : "");
  });

  const runAction = async (key: string, fn: () => Promise<void>, fallbackErr: string) => {
    if (busyAction() || props.busy) return;
    setBusyAction(key);
    setErrorMsg(null);
    setNotice(null);
    try {
      await fn();
    } catch (err: any) {
      setErrorMsg(err?.message || fallbackErr);
    } finally {
      setBusyAction(null);
    }
  };

  const targetSessionID = createMemo(() => props.sessionID || session()?.id || null);

  const onApplyAndExit = () => {
    const sid = targetSessionID();
    if (!sid) return;
    return runAction(
      "apply-exit",
      async () => {
        await applyAndExitSessionWorktree(sid, appSettings.worktreeApplyMode, isPromotedBranch());
        props.onClose();
      },
      t("timeline.sandboxApplyFailed")
    );
  };

  const onApplyKeepSandbox = () => {
    const sid = targetSessionID();
    if (!sid) return;
    return runAction(
      "apply-keep",
      async () => {
        await applySessionWorktree(sid, appSettings.worktreeApplyMode);
        setNotice(t("timeline.sandboxAppliedSuccess"));
      },
      t("timeline.sandboxApplyFailed")
    );
  };

  const onSyncSandboxWithBase = () => {
    const sid = targetSessionID();
    if (!sid) return;
    return runAction(
      "sync-base",
      async () => {
        await syncSessionWorktree(sid);
        setNotice(t("timeline.sandboxSyncedBaseSuccess", { branch: baseBranch() }));
      },
      t("timeline.sandboxApplyFailed")
    );
  };

  const onRenameSandboxBranch = () => {
    const sid = targetSessionID();
    const cleaned = renameDraft().trim();
    if (!sid || !cleaned) return;
    return runAction(
      "rename",
      async () => {
        await renameSessionBranch(sid, cleaned);
      },
      t("timeline.sandboxRenameFailed")
    );
  };

  const onExitSandbox = (keepBranch: boolean) => {
    const sid = targetSessionID();
    if (!sid) return;
    return runAction(
      keepBranch ? "keep-exit" : "discard-exit",
      async () => {
        await exitSessionWorktree(sid, keepBranch);
        props.onClose();
      },
      t("timeline.sandboxExitFailed")
    );
  };

  return (
    <Show when={isolated()}>
      <div class="mt-1.5 pt-1.5 border-t border-[var(--border-subtle)] space-y-1.5">
        <div class="px-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
          {t("timeline.sandboxLifecycleTitle")}
        </div>

        <Show when={errorMsg()}>
          <div class="mx-1 rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2 py-1 text-[11px] text-[var(--danger-text)] break-words">
            {errorMsg()}
          </div>
        </Show>
        <Show when={notice()}>
          <div class="mx-1 rounded-lg border border-[var(--ok)]/30 bg-[var(--ok-surface)] px-2 py-1 text-[11px] text-[var(--ok-text)] break-words">
            {notice()}
          </div>
        </Show>

        <button
          type="button"
          disabled={!!busyAction() || props.busy}
          onClick={() => void onApplyAndExit()}
          class="w-full flex flex-col gap-0.5 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-2 text-left hover:opacity-95 disabled:opacity-50 transition-opacity cursor-pointer"
        >
          <div class="flex items-center gap-1.5 font-semibold text-[12px] text-[var(--accent-primary)]">
            <Show
              when={busyAction() !== "apply-exit"}
              fallback={<LoaderCircle size={12} class="animate-spin" />}
            >
              <GitMerge size={12} />
            </Show>
            <span>{t("timeline.sandboxApplyExitBtn")}</span>
          </div>
          <span class="text-[11px] text-[var(--text-muted)] leading-snug">
            {t("timeline.sandboxApplyExitDesc")}
          </span>
        </button>

        <button
          type="button"
          disabled={!!busyAction() || props.busy}
          onClick={() => void onApplyKeepSandbox()}
          class="w-full flex items-center gap-2 rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
        >
          <FolderGit2 size={12} class="shrink-0 text-[var(--text-muted)]" />
          <span>{t("timeline.sandboxApplyKeepBtn")}</span>
        </button>

        <Show when={behindCount() > 0}>
          <button
            type="button"
            disabled={!!busyAction() || props.busy}
            onClick={() => void onSyncSandboxWithBase()}
            class="w-full flex items-center gap-2 rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] disabled:opacity-50 transition-colors cursor-pointer"
          >
            <GitPullRequestArrow size={12} class="shrink-0" />
            <span>
              {t("timeline.sandboxSyncBaseBtn", {
                branch: baseBranch(),
                count: behindCount(),
              })}
            </span>
          </button>
        </Show>

        <div class="px-1 pt-1 flex flex-col gap-1.5">
          <span class="text-[11px] font-medium text-[var(--text-muted)]">
            {t("timeline.sandboxRenameLabel")}
          </span>
          <div class="flex items-center gap-1.5">
            <input
              type="text"
              value={renameDraft()}
              placeholder={t("timeline.sandboxRenamePlaceholder")}
              onInput={(ev) => setRenameDraft(ev.currentTarget.value)}
              onKeyDown={(ev) => {
                if (ev.key === "Enter" && !ev.isComposing) {
                  ev.preventDefault();
                  void onRenameSandboxBranch();
                }
              }}
              class="flex-1 min-w-0 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-[12px] font-mono text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
            />
            <button
              type="button"
              disabled={!renameDraft().trim() || !!busyAction() || props.busy}
              onClick={() => void onRenameSandboxBranch()}
              class="shrink-0 inline-flex items-center gap-1 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] disabled:opacity-40 transition-colors cursor-pointer"
            >
              <Show
                when={busyAction() !== "rename"}
                fallback={<LoaderCircle size={10.5} class="animate-spin shrink-0" />}
              >
                <Pencil size={10.5} />
              </Show>
              <span>{t("timeline.sandboxRenameBtn")}</span>
            </button>
          </div>
        </div>

        <div class="pt-1 border-t border-[var(--border-subtle)] flex flex-col gap-1">
          <button
            type="button"
            disabled={!!busyAction() || props.busy}
            onClick={() => void onExitSandbox(true)}
            class="w-full flex items-center gap-2 rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
          >
            <Show
              when={busyAction() !== "keep-exit"}
              fallback={<LoaderCircle size={12} class="animate-spin shrink-0" />}
            >
              <GitBranch size={12} class="shrink-0 text-[var(--accent-primary)]" />
            </Show>
            <span>{t("timeline.sandboxKeepExitBtn")}</span>
          </button>
          <button
            type="button"
            disabled={!!busyAction() || props.busy}
            onClick={() => void onExitSandbox(false)}
            class="w-full flex items-center gap-2 rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--danger-text)] hover:bg-[var(--danger-surface)] disabled:opacity-50 transition-colors cursor-pointer"
          >
            <Show
              when={busyAction() !== "discard-exit"}
              fallback={<LoaderCircle size={12} class="animate-spin shrink-0" />}
            >
              <Trash2 size={12} class="shrink-0" />
            </Show>
            <span>{t("timeline.sandboxDiscardBtn")}</span>
          </button>
        </div>
      </div>
    </Show>
  );
}
