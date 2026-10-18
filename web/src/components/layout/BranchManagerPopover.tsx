// web/src/components/layout/BranchManagerPopover.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  Check,
  ChevronDown,
  FolderGit2,
  GitBranch,
  GitMerge,
  LoaderCircle,
  Pencil,
  Plus,
  Sparkles,
  Trash2,
} from "lucide-solid";
import {
  activeSession,
  appSettings,
  applyAndExitSessionWorktree,
  applySessionWorktree,
  checkoutLocalBranch,
  exitSessionWorktree,
  refreshWorktreeStatus,
  renameSessionBranch,
  switchActiveSessionSandboxMode,
  workspace,
} from "../../store/workspace";
import { hoxiAPI, type GitBranchInfo } from "../../services/api";
import { t } from "../../i18n";

export default function BranchManagerPopover() {
  const [open, setOpen] = createSignal(false);
  const [loadingBranches, setLoadingBranches] = createSignal(false);
  const [branches, setBranches] = createSignal<GitBranchInfo[]>([]);
  const [rootBranch, setRootBranch] = createSignal("");
  const [sandboxCount, setSandboxCount] = createSignal(0);

  const [newBranchName, setNewBranchName] = createSignal("");
  const [renameDraft, setRenameDraft] = createSignal("");
  const [busyAction, setBusyAction] = createSignal<string | null>(null);
  const [toastNote, setToastNote] = createSignal<string | null>(null);
  const [errorMsg, setErrorMsg] = createSignal<string | null>(null);

  let triggerRef: HTMLDivElement | undefined;
  let popoverRef: HTMLDivElement | undefined;

  const session = createMemo(() => activeSession());
  const sessionID = () => workspace.activeSessionID;

  const activeWorktree = createMemo(() => {
    const id = sessionID();
    return id ? workspace.worktreeBySession[id] : undefined;
  });
  const rootWorktree = createMemo(() => workspace.worktreeBySession["__root__"]);
  const isIsolated = createMemo(() => Boolean(activeWorktree()?.isolated));

  const displayBranch = createMemo(() =>
    isIsolated()
      ? activeWorktree()?.branch || ""
      : activeWorktree()?.branch || rootWorktree()?.branch || rootBranch() || ""
  );

  const regularBranches = createMemo(() => branches().filter((b) => !b.isSandbox));
  const isPromotedBranch = createMemo(() => {
    const br = activeWorktree()?.branch || "";
    return Boolean(br && !br.startsWith("hoxi/"));
  });

  const showToast = (msg: string) => {
    setToastNote(msg);
    setTimeout(() => setToastNote(null), 3500);
  };

  const fetchBranchList = async () => {
    setLoadingBranches(true);
    setErrorMsg(null);
    try {
      const res = await hoxiAPI.listBranches(workspace.rootDir || undefined);
      setBranches(res.branches || []);
      if (res.currentBranch) setRootBranch(res.currentBranch);
      setSandboxCount(res.sandboxCount || 0);
    } catch (err) {
      console.error("listBranches failed:", err);
    } finally {
      setLoadingBranches(false);
    }
  };

  createEffect(() => {
    if (workspace.rootDir) void refreshWorktreeStatus("__root__");
  });

  createEffect(() => {
    if (!open()) return;
    void fetchBranchList();
    const curBr = activeWorktree()?.branch || "";
    setRenameDraft(curBr && !curBr.startsWith("hoxi/") ? curBr : "");

    const handlePointerDown = (ev: MouseEvent) => {
      const target = ev.target as Node;
      if (popoverRef && !popoverRef.contains(target) && triggerRef && !triggerRef.contains(target)) {
        setOpen(false);
      }
    };
    const handleKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") setOpen(false);
    };
    window.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      window.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  const runAction = async (key: string, fn: () => Promise<void>, fallbackErr: string) => {
    if (busyAction()) return;
    setBusyAction(key);
    setErrorMsg(null);
    try {
      await fn();
    } catch (err: any) {
      setErrorMsg(err?.message || fallbackErr);
    } finally {
      setBusyAction(null);
    }
  };

  const onApplyAndExit = () => {
    const s = session();
    if (!s) return;
    return runAction(
      "apply-exit",
      async () => {
        await applyAndExitSessionWorktree(s.id, appSettings.worktreeApplyMode, isPromotedBranch());
        setOpen(false);
        showToast(t("header.appliedSuccess"));
      },
      t("header.applyFailed")
    );
  };

  const onApplyKeepSandbox = () => {
    const s = session();
    if (!s) return;
    return runAction(
      "apply-keep",
      async () => {
        await applySessionWorktree(s.id, appSettings.worktreeApplyMode);
        setOpen(false);
        showToast(t("header.appliedSuccess"));
      },
      t("header.applyFailed")
    );
  };

  const onRenameSandboxBranch = () => {
    const s = session();
    const cleaned = renameDraft().trim();
    if (!s || !cleaned) return;
    return runAction(
      "rename",
      async () => {
        await renameSessionBranch(s.id, cleaned);
        await fetchBranchList();
      },
      "Rename failed"
    );
  };

  const onExitSandbox = (keepBranch: boolean) => {
    const s = session();
    if (!s) return;
    return runAction(
      keepBranch ? "keep-exit" : "discard-exit",
      async () => {
        await exitSessionWorktree(s.id, keepBranch);
        setOpen(false);
      },
      "Exit sandbox failed"
    );
  };

  const onCheckoutBranch = (branchName: string, create = false) => {
    const cleaned = branchName.trim();
    if (!cleaned) return;
    return runAction(
      create ? "create-branch" : `checkout:${cleaned}`,
      async () => {
        await checkoutLocalBranch(cleaned, create);
        if (create) setNewBranchName("");
        await fetchBranchList();
        setOpen(false);
      },
      "Checkout failed"
    );
  };

  const onEnableSandbox = () =>
    runAction(
      "enable-sandbox",
      async () => {
        await switchActiveSessionSandboxMode("worktree");
        setOpen(false);
      },
      "Enable sandbox failed"
    );

  const onPruneSandboxes = () =>
    runAction(
      "prune",
      async () => {
        const exclude = isIsolated() ? sessionID() || undefined : undefined;
        const res = await hoxiAPI.pruneSandboxes(exclude, workspace.rootDir || undefined);
        await fetchBranchList();
        await refreshWorktreeStatus("__root__");
        showToast(t("header.prunedSuccess", { count: res.removedCount }));
      },
      "Clean up failed"
    );

  return (
    <Show when={displayBranch()}>
      <div
        ref={triggerRef}
        class="relative inline-flex shrink-0 items-center gap-1.5"
        style={{ "--wails-draggable": "no-drag" } as any}
      >
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          class={`inline-flex items-center gap-1 rounded-lg px-2 py-0.5 text-[12px] font-mono transition-colors cursor-pointer ${
            isIsolated()
              ? "text-[var(--accent-primary)] hover:bg-[var(--accent-surface)]"
              : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
          }`}
          title={`${displayBranch()} (${t("header.branchMenuTooltip")})`}
          aria-label={`${displayBranch()} (${t("header.branchMenuTooltip")})`}
          aria-expanded={open()}
        >
          <GitBranch size={12} class="shrink-0" />
          <span class="hidden md:inline truncate max-w-[100px] lg:max-w-[160px]">{displayBranch()}</span>
          <ChevronDown size={11} class="hidden md:block shrink-0 opacity-65" />
        </button>

        <Show when={toastNote()}>
          <span class="text-[11px] text-[var(--ok-text)] whitespace-nowrap">
            {toastNote()}
          </span>
        </Show>

        {/* Unified Branch & Sandbox Popover */}
        <Show when={open()}>
          <div
            ref={popoverRef}
            class="absolute right-0 top-full mt-2 z-50 w-[320px] max-w-[calc(100vw-2rem)] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-2.5 shadow-xl text-xs"
          >
            <Show when={errorMsg()}>
              <div class="mb-2 rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2.5 py-1.5 text-[11px] text-[var(--danger-text)] break-words">
                {errorMsg()}
              </div>
            </Show>

            <Show
              when={isIsolated()}
              fallback={
                /* ================= LOCAL MODE VIEW ================= */
                <div class="flex flex-col gap-2">
                  <div class="flex items-center justify-between px-1">
                    <span class="text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-muted)]">
                      {t("header.localBranchesTitle")}
                    </span>
                    <Show when={loadingBranches()}>
                      <LoaderCircle size={11} class="animate-spin text-[var(--text-faint)]" />
                    </Show>
                  </div>

                  {/* Create & Checkout New Local Branch */}
                  <div class="flex items-center gap-1.5">
                    <input
                      type="text"
                      value={newBranchName()}
                      placeholder={t("header.newBranchPlaceholder")}
                      onInput={(ev) => setNewBranchName(ev.currentTarget.value)}
                      onKeyDown={(ev) => {
                        if (ev.key === "Enter" && !ev.isComposing) {
                          ev.preventDefault();
                          void onCheckoutBranch(newBranchName(), true);
                        }
                      }}
                      class="flex-1 min-w-0 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-[11.5px] font-mono text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
                    />
                    <button
                      type="button"
                      disabled={!newBranchName().trim() || !!busyAction()}
                      onClick={() => void onCheckoutBranch(newBranchName(), true)}
                      class="shrink-0 inline-flex items-center gap-1 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-40 transition-opacity cursor-pointer"
                    >
                      <Plus size={11} />
                      <span>{t("header.createBranchBtn")}</span>
                    </button>
                  </div>

                  {/* Local Branch List */}
                  <div class="max-h-[180px] overflow-y-auto flex flex-col gap-0.5 pr-0.5">
                    <For each={regularBranches()}>
                      {(b) => (
                        <button
                          type="button"
                          disabled={!!busyAction()}
                          onClick={() => {
                            if (!b.current) void onCheckoutBranch(b.name, false);
                          }}
                          class={`flex items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-left transition-colors cursor-pointer ${
                            b.current
                              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                          }`}
                        >
                          <div class="flex items-center gap-1.5 min-w-0 flex-1">
                            <GitBranch size={11.5} class="shrink-0" />
                            <span class="font-mono text-[11.5px] truncate">{b.name}</span>
                          </div>
                          <div class="flex items-center gap-1.5 shrink-0">
                            <Show when={b.headCommit}>
                              <span class="font-mono text-[10px] text-[var(--text-faint)]">
                                {b.headCommit}
                              </span>
                            </Show>
                            <Show when={b.current}>
                              <Check size={12} class="text-[var(--accent-primary)]" />
                            </Show>
                          </div>
                        </button>
                      )}
                    </For>
                  </div>

                  {/* Switch current session into Worktree Sandbox */}
                  <Show when={sessionID()}>
                    <div class="pt-1.5 border-t border-[var(--border-subtle)]">
                      <button
                        type="button"
                        disabled={!!busyAction()}
                        onClick={() => void onEnableSandbox()}
                        class="w-full flex items-center gap-2 rounded-lg px-2 py-1.5 text-[11.5px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] transition-colors cursor-pointer"
                      >
                        <Sparkles size={12} class="shrink-0" />
                        <span>{t("header.enableSandboxBtn")}</span>
                      </button>
                    </div>
                  </Show>
                </div>
              }
            >
              {/* ================= WORKTREE SANDBOX MODE VIEW ================= */}
              <div class="flex flex-col gap-2.5">
                <div class="flex items-center justify-between px-1">
                  <span class="text-[10.5px] font-semibold uppercase tracking-wider text-[var(--accent-primary)]">
                    {t("header.sandboxHeaderTitle")}
                  </span>
                  <span class="font-mono text-[10.5px] text-[var(--text-muted)] truncate max-w-[140px]">
                    {displayBranch()}
                  </span>
                </div>

                {/* Primary Action: Apply to Main & Exit Sandbox */}
                <button
                  type="button"
                  disabled={!!busyAction()}
                  onClick={() => void onApplyAndExit()}
                  class="w-full flex flex-col gap-0.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-2 text-left hover:opacity-95 disabled:opacity-50 transition-opacity cursor-pointer"
                >
                  <div class="flex items-center gap-1.5 font-semibold text-[11.5px] text-[var(--accent-primary)]">
                    <Show
                      when={busyAction() !== "apply-exit"}
                      fallback={<LoaderCircle size={12} class="animate-spin" />}
                    >
                      <GitMerge size={12} />
                    </Show>
                    <span>{t("header.applyAndExitBtn")}</span>
                  </div>
                  <span class="text-[10.5px] text-[var(--text-muted)] leading-snug">
                    {t("header.applyAndExitDesc")}
                  </span>
                </button>

                {/* Secondary Action: Sync to Main but Keep Sandbox */}
                <button
                  type="button"
                  disabled={!!busyAction()}
                  onClick={() => void onApplyKeepSandbox()}
                  class="w-full flex items-center gap-2 rounded-lg px-2 py-1.5 text-[11.5px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
                >
                  <FolderGit2 size={12} class="shrink-0 text-[var(--text-muted)]" />
                  <span>{t("header.applyKeepSandboxBtn")}</span>
                </button>

                {/* Rename Sandbox Branch (Promote hoxi/ses_... to formal branch) */}
                <div class="pt-2 border-t border-[var(--border-subtle)] flex flex-col gap-1.5">
                  <span class="px-1 text-[10.5px] font-medium text-[var(--text-muted)]">
                    {t("header.renameBranchLabel")}
                  </span>
                  <div class="flex items-center gap-1.5">
                    <input
                      type="text"
                      value={renameDraft()}
                      placeholder={t("header.renameBranchPlaceholder")}
                      onInput={(ev) => setRenameDraft(ev.currentTarget.value)}
                      onKeyDown={(ev) => {
                        if (ev.key === "Enter" && !ev.isComposing) {
                          ev.preventDefault();
                          void onRenameSandboxBranch();
                        }
                      }}
                      class="flex-1 min-w-0 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-[11.5px] font-mono text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
                    />
                    <button
                      type="button"
                      disabled={!renameDraft().trim() || !!busyAction()}
                      onClick={() => void onRenameSandboxBranch()}
                      class="shrink-0 inline-flex items-center gap-1 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] disabled:opacity-40 transition-colors cursor-pointer"
                    >
                      <Pencil size={10.5} />
                      <span>{t("header.renameBranchBtn")}</span>
                    </button>
                  </div>
                </div>

                {/* Exit Sandbox Actions */}
                <div class="pt-1.5 border-t border-[var(--border-subtle)] flex flex-col gap-1">
                  <Show when={isPromotedBranch()}>
                    <button
                      type="button"
                      disabled={!!busyAction()}
                      onClick={() => void onExitSandbox(true)}
                      class="w-full flex items-center gap-2 rounded-lg px-2 py-1.5 text-[11.5px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                    >
                      <GitBranch size={12} class="shrink-0 text-[var(--accent-primary)]" />
                      <span>{t("header.keepBranchExitBtn")}</span>
                    </button>
                  </Show>
                  <button
                    type="button"
                    disabled={!!busyAction()}
                    onClick={() => void onExitSandbox(false)}
                    class="w-full flex items-center gap-2 rounded-lg px-2 py-1.5 text-[11.5px] text-[var(--danger-text)] hover:bg-[var(--danger-surface)] transition-colors cursor-pointer"
                  >
                    <Trash2 size={12} class="shrink-0" />
                    <span>{t("header.discardSandboxBtn")}</span>
                  </button>
                </div>
              </div>
            </Show>

            {/* Global Stale hoxi/* Sandbox Cleanup Footer */}
            <Show when={sandboxCount() > (isIsolated() ? 1 : 0)}>
              <div class="mt-2 pt-2 border-t border-[var(--border-subtle)]">
                <button
                  type="button"
                  disabled={!!busyAction()}
                  onClick={() => void onPruneSandboxes()}
                  class="w-full flex items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-[11px] text-[var(--text-muted)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)] transition-colors cursor-pointer"
                >
                  <span class="inline-flex items-center gap-1.5 truncate">
                    <Trash2 size={11.5} class="shrink-0" />
                    <span class="truncate">
                      {t("header.pruneSandboxesBtn", {
                        count: sandboxCount() - (isIsolated() ? 1 : 0),
                      })}
                    </span>
                  </span>
                  <Show when={busyAction() === "prune"}>
                    <LoaderCircle size={11} class="animate-spin shrink-0" />
                  </Show>
                </button>
              </div>
            </Show>
          </div>
        </Show>
      </div>
    </Show>
  );
}
