// web/src/components/inspector/PierreDiffViewer.tsx
import { createEffect, createMemo, createSignal, on, onCleanup, Show } from "solid-js";
import { ArrowUpFromLine, Check, Columns2, ExternalLink, FolderSync, GitCommitHorizontal, GitPullRequestArrow, RefreshCw, Rows2, ShieldCheck, Sparkles } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  activeDirectory,
  appSettings,
  generateSmartCommitMessage,
  loadSessionMessagesOnly,
  refreshWorktreeStatus,
  restoreSessionWorktree,
  runSessionCodeAudit,
  setWorkspace,
  syncSessionWorktree,
  updateAppSetting,
  workspace,
} from "../../store/workspace";
import {
  collectCycleChangedFiles,
  enrichDiffWithWorktree,
} from "../timeline/tool-card-state";
import { t } from "../../i18n";
import ObserverAuditBanner from "./ObserverAuditBanner";
import DiffEmptyState from "./pierre-diff-empty";
import DiffFileTree from "./diff-file-tree";
import {
  FileDiffBlock,
  SelectedDiffDetail,
  synthesizeAICommitMessage,
} from "./pierre-diff-parts";

export { FileDiffBlock };

function openRemoteCompareURL(url: string) {
  if (!url) return;
  const wailsRuntime = (window as any).runtime;
  if (typeof wailsRuntime?.BrowserOpenURL === "function") {
    wailsRuntime.BrowserOpenURL(url);
    return;
  }
  window.open(url, "_blank", "noopener,noreferrer");
}

export default function PierreDiffViewer() {
  const diffStyle = () => appSettings.defaultDiffStyle;
  const setDiffStyle = (mode: "unified" | "split") =>
    updateAppSetting("defaultDiffStyle", mode);
  const [refreshing, setRefreshing] = createSignal(false);
  const [commitMsg, setCommitMsg] = createSignal("");
  const [commitExpanded, setCommitExpanded] = createSignal(false);
  const [committing, setCommitting] = createSignal(false);
  const [pushing, setPushing] = createSignal(false);
  const [committedHash, setCommittedHash] = createSignal<string | null>(null);
  const [lastCompareURL, setLastCompareURL] = createSignal<string | null>(null);
  const [restoring, setRestoring] = createSignal(false);
  const [syncingBase, setSyncingBase] = createSignal(false);

  const sessionKey = () => workspace.activeSessionID || "__root__";
  const activeWt = createMemo(() => workspace.worktreeBySession[sessionKey()]);
  const compareURL = () => lastCompareURL() || activeWt()?.compareURL || "";

  const onRestoreWorktree = async () => {
    const id = workspace.activeSessionID;
    if (!id || restoring()) return;
    setRestoring(true);
    try {
      await restoreSessionWorktree(id);
    } catch (err) {
      console.error("restoreSessionWorktree failed:", err);
    } finally {
      setRestoring(false);
    }
  };

  const onSyncWithBase = async () => {
    const id = workspace.activeSessionID;
    if (!id || syncingBase()) return;
    setSyncingBase(true);
    try {
      await syncSessionWorktree(id);
    } catch (err) {
      console.error("syncSessionWorktree failed:", err);
    } finally {
      setSyncingBase(false);
    }
  };

  const diffs = createMemo(() => {
    const id = workspace.activeSessionID;
    const key = sessionKey();
    const wtDiffs = workspace.worktreeDiffsBySession[key] ?? [];
    if (workspace.diffScope === "worktree") {
      return wtDiffs;
    }
    const isolated = activeWt()?.isolated;
    const sessionDiffs = id
      ? workspace.diffsBySession[id] ?? []
      : workspace.diffsBySession[key] ?? [];
    if (sessionDiffs.length > 0 || !id) {
      return wtDiffs.length > 0
        ? sessionDiffs.map((d) => enrichDiffWithWorktree(d, wtDiffs, isolated))
        : sessionDiffs;
    }
    const turns = workspace.messagesBySession[id] ?? [];
    return turns.length > 0 ? collectCycleChangedFiles(turns, id) : sessionDiffs;
  });

  // The clicked turn may describe a historical edit absent from the current Git diff.
  // Keep that preview separate from the live diff list used for totals and commits.
  const previewDiffs = createMemo(() => {
    const list = diffs();
    const clicked = workspace.inspectedFileDiff;
    if (!clicked || !workspace.inspectedDiffFile) return list;
    const exactIdx = list.findIndex((item) => item.file === clicked.file);
    const index =
      exactIdx >= 0
        ? exactIdx
        : list.findIndex(
            (item) =>
              item.file.endsWith(`/${clicked.file}`) ||
              clicked.file.endsWith(`/${item.file}`)
          );
    if (index < 0) return [...list, clicked];
    const current = list[index];
    if (current.patch || typeof current.before === "string" || typeof current.after === "string") return list;
    return list.map((item, i) => i === index ? clicked : item);
  });

  const selectedIndex = createMemo(() => {
    const target = workspace.inspectedDiffFile;
    if (!target) return -1;
    const list = previewDiffs();
    const exactIdx = list.findIndex((d) => d.file === target);
    if (exactIdx >= 0) return exactIdx;
    return list.findIndex(
      (d) => d.file.endsWith(`/${target}`) || target.endsWith(`/${d.file}`)
    );
  });

  const selectedDiff = createMemo(() => {
    const idx = selectedIndex();
    return idx >= 0 ? previewDiffs()[idx] : null;
  });

  const isAuditing = () =>
    Boolean(workspace.activeSessionID && workspace.auditingBySession[workspace.activeSessionID]);

  const totals = createMemo(() => {
    let add = 0;
    let del = 0;
    for (const d of diffs()) {
      add += d.additions || 0;
      del += d.deletions || 0;
    }
    return { add, del };
  });

  createEffect(on(() => [workspace.activeSessionID, workspace.rootDir] as const, ([id, root]) => {
    setLastCompareURL(null);
    if (!root && !id) return;
    void refreshWorktreeStatus(id);
    let cleanStreak = 0;
    const timer = setInterval(() => {
      if (document.hidden || workspace.inspectorTab !== "diffs") return;
      const busy = Boolean(id && workspace.statusBySession[id]?.type === "busy");
      const dirty = Boolean(workspace.worktreeBySession[id || "__root__"]?.dirty);
      if (!busy && !dirty) {
        cleanStreak++;
        if (cleanStreak > 2 && cleanStreak % 3 !== 0) return;
      } else {
        cleanStreak = 0;
      }
      void refreshWorktreeStatus(id);
    }, 5000);
    onCleanup(() => clearInterval(timer));
  }));

  const onRefresh = async () => {
    if (refreshing()) return;
    setRefreshing(true);
    try {
      const id = workspace.activeSessionID;
      await Promise.all([
        id ? loadSessionMessagesOnly(id) : Promise.resolve(),
        refreshWorktreeStatus(id),
      ]);
    } catch (err) {
      console.error(err);
    } finally {
      setRefreshing(false);
    }
  };

  const onGenerateAICommit = async () => {
    setCommitMsg(synthesizeAICommitMessage(workspace.activeSessionID, diffs()));
    const smart = await generateSmartCommitMessage(workspace.activeSessionID, diffs());
    if (smart) setCommitMsg(smart);
  };

  const onCommitWorktree = async (push = false) => {
    const id = sessionKey();
    const msg = commitMsg().trim();
    if (!msg || committing() || pushing()) return;
    setCommitting(true);
    if (push) setPushing(true);
    try {
      const res = await hoxiAPI.commitWorktree(id, msg, {
        push,
        directory: activeDirectory() || workspace.rootDir || undefined,
      });
      setCommitMsg("");
      if (res.compareURL) setLastCompareURL(res.compareURL);
      setCommittedHash(res.commit || "HEAD");
      setWorkspace("inspectedDiffFile", null);
      setTimeout(() => setCommittedHash(null), 5000);
      await refreshWorktreeStatus(workspace.activeSessionID);
      if (workspace.activeSessionID) {
        await loadSessionMessagesOnly(workspace.activeSessionID);
      }
    } catch (err) {
      console.error("commitWorktree failed:", err);
    } finally {
      setCommitting(false);
      setPushing(false);
    }
  };

  const onPushWorktree = async (): Promise<string> => {
    if (pushing() || committing()) return compareURL();
    setPushing(true);
    try {
      const res = await hoxiAPI.pushWorktree(
        sessionKey(),
        activeDirectory() || workspace.rootDir || undefined
      );
      if (res.compareURL) setLastCompareURL(res.compareURL);
      await refreshWorktreeStatus(workspace.activeSessionID);
      return res.compareURL || compareURL();
    } catch (err) {
      console.error("pushWorktree failed:", err);
      return "";
    } finally {
      setPushing(false);
    }
  };

  const onOpenCreatePR = async () => {
    if (pushing() || committing()) return;
    const wt = activeWt();
    let url = compareURL();
    if ((wt?.aheadCount ?? 0) > 0 || !wt?.hasUpstream) {
      const pushedURL = await onPushWorktree();
      if (pushedURL) url = pushedURL;
    }
    if (url) openRemoteCompareURL(url);
  };

  return (
    <div class="flex flex-col h-full">
      <div class="shrink-0 flex items-center gap-1 px-2 py-1 border-b border-[var(--border-subtle)]">
        <button type="button" class={`rounded-md px-2 py-1 text-[11px] cursor-pointer ${workspace.diffScope === "worktree" ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]" : "text-[var(--text-muted)]"}`}
          aria-pressed={workspace.diffScope === "worktree"}
          onClick={() => setWorkspace("diffScope", "worktree")}>
          {t("inspector.diffs.scopeWorktree")}
        </button>
        <button type="button" class={`rounded-md px-2 py-1 text-[11px] cursor-pointer ${workspace.diffScope === "session" ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]" : "text-[var(--text-muted)]"}`}
          aria-pressed={workspace.diffScope === "session"}
          onClick={() => setWorkspace("diffScope", "session")}>
          {t("inspector.diffs.scopeSession")}
        </button>
        <button type="button" onClick={() => setWorkspace("inspectedDiffFile", null)}
          title={t("inspector.diffs.backToTree")}
          class="ml-auto flex shrink-0 items-center gap-1.5 px-1 text-[10.5px] font-mono tabular-nums cursor-pointer">
          <span class="text-[var(--ok-text)]">+{totals().add}</span>
          <span class="text-[var(--danger-text)]">-{totals().del}</span>
        </button>
        <Show when={selectedDiff()}>
          <button type="button" onClick={() => setDiffStyle(diffStyle() === "unified" ? "split" : "unified")}
            title={diffStyle() === "unified" ? t("inspector.diffs.split") : t("inspector.diffs.unified")}
            aria-label={diffStyle() === "unified" ? t("inspector.diffs.split") : t("inspector.diffs.unified")}
            class="shrink-0 rounded-md p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer">
            <Show when={diffStyle() === "unified"} fallback={<Rows2 size={13} />}><Columns2 size={13} /></Show>
          </button>
        </Show>
        <button type="button" onClick={() => void onRefresh()} disabled={refreshing()}
          title={t("inspector.diffs.refresh")} aria-label={t("inspector.diffs.refresh")}
          class="shrink-0 rounded-md p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer">
          <RefreshCw size={13} class={refreshing() ? "animate-spin" : ""} />
        </button>
        <Show when={(activeWt()?.aheadCount ?? 0) > 0}>
          <button
            type="button"
            disabled={pushing() || committing()}
            onClick={() => void onPushWorktree()}
            title={t("inspector.diffs.pushAheadBtn", { count: activeWt()?.aheadCount ?? 0 })}
            aria-label={t("inspector.diffs.pushAheadBtn", { count: activeWt()?.aheadCount ?? 0 })}
            class="shrink-0 inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-1.5 py-1 text-[10.5px] font-mono text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-40 cursor-pointer"
          >
            <ArrowUpFromLine size={11} class={pushing() ? "animate-bounce" : ""} />
            <span>{pushing() ? t("inspector.diffs.pushingBtn") : `↑${activeWt()?.aheadCount ?? 0}`}</span>
          </button>
        </Show>
        <Show when={compareURL() && ((activeWt()?.aheadCount ?? 0) > 0 || activeWt()?.hasUpstream || Boolean(committedHash()))}>
          <button
            type="button"
            disabled={pushing() || committing()}
            onClick={() => void onOpenCreatePR()}
            title={t("inspector.diffs.createPrTooltip")}
            aria-label={t("inspector.diffs.createPrBtn")}
            class="shrink-0 inline-flex items-center gap-1 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-1 text-[10.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-40 cursor-pointer"
          >
            <GitPullRequestArrow size={11} />
            <span>{t("inspector.diffs.createPrBtn")}</span>
            <ExternalLink size={9} />
          </button>
        </Show>
        <Show when={diffs().length > 0}>
          <button type="button" disabled={isAuditing() || !workspace.activeSessionID}
            onClick={() => workspace.activeSessionID && void runSessionCodeAudit(workspace.activeSessionID, true)}
            title={t("inspector.diffs.auditTooltip")}
            aria-label={isAuditing() ? t("inspector.diffs.auditingBtn") : t("inspector.diffs.auditBtn")}
            class="shrink-0 rounded-md p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--accent-primary)] disabled:opacity-40 cursor-pointer">
            <ShieldCheck size={13} class={isAuditing() ? "animate-pulse" : ""} />
          </button>
          <button type="button" onClick={() => setCommitExpanded(!commitExpanded())}
            aria-expanded={commitExpanded()} aria-controls="diff-commit-editor"
            title={t("inspector.diffs.commitBtn", { count: diffs().length })}
            aria-label={t("inspector.diffs.commitBtn", { count: diffs().length })}
            class={`shrink-0 rounded-md p-1.5 cursor-pointer transition-colors ${commitExpanded() ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]" : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--accent-primary)]"}`}>
            <GitCommitHorizontal size={14} />
          </button>
        </Show>
      </div>
      <Show when={activeWt()?.health === "absent-restorable"}>
        <div class="shrink-0 flex items-center justify-between gap-2 px-3 py-2 bg-[var(--warn-surface)]/70 border-b border-[var(--warn-border)] text-[11px]">
          <div class="min-w-0 flex-1">
            <div class="font-semibold text-[var(--warn-text)] truncate">
              {t("inspector.diffs.restorableTitle")}
            </div>
            <div class="text-[10.5px] text-[var(--text-secondary)] truncate">
              {t("inspector.diffs.restorableDesc", {
                branch: activeWt()?.branch || `hoxi/${workspace.activeSessionID || ""}`,
              })}
            </div>
          </div>
          <button
            type="button"
            disabled={restoring()}
            onClick={() => void onRestoreWorktree()}
            class="shrink-0 inline-flex items-center gap-1 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-40 cursor-pointer"
          >
            <FolderSync size={11} />
            <span>
              {restoring()
                ? t("inspector.diffs.restoringWorktreeBtn")
                : t("inspector.diffs.restoreWorktreeBtn")}
            </span>
          </button>
        </div>
      </Show>
      <Show when={activeWt()?.isolated && (activeWt()?.behindCount ?? 0) > 0}>
        <div class="shrink-0 flex items-center justify-between gap-2 px-3 py-1.5 bg-[var(--accent-surface)]/45 border-b border-[var(--accent-border)]/60 text-[11px]">
          <span class="font-mono text-[var(--text-secondary)] truncate">
            {t("inspector.diffs.behindBaseHint", {
              count: activeWt()?.behindCount ?? 0,
              branch: activeWt()?.baseBranch || "main",
            })}
          </span>
          <button
            type="button"
            disabled={syncingBase()}
            onClick={() => void onSyncWithBase()}
            class="shrink-0 inline-flex items-center gap-1 rounded-md border border-[var(--accent-border)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] font-medium text-[var(--accent-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer"
          >
            <GitPullRequestArrow size={11} />
            <span>
              {syncingBase()
                ? t("inspector.diffs.syncingBaseBtn")
                : t("inspector.diffs.syncBaseBtn", {
                    branch: activeWt()?.baseBranch || "main",
                  })}
            </span>
          </button>
        </div>
      </Show>
      <Show when={diffs().length > 0 && commitExpanded()}>
        <div id="diff-commit-editor" class="shrink-0 grid grid-cols-[1fr_auto] items-center gap-2 px-2.5 py-2 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]/60">
          <input
            type="text"
            value={commitMsg()}
            onInput={(ev) => setCommitMsg(ev.currentTarget.value)}
            onKeyDown={(ev) => {
              if (ev.key === "Enter" && !ev.isComposing) {
                ev.preventDefault();
                void onCommitWorktree(false);
              }
            }}
            placeholder={t("inspector.diffs.commitPlaceholder")}
            aria-label={t("inspector.diffs.commitPlaceholder")}
            class="min-w-0 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1.5 text-[12px] font-mono text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
          />
          <button
            type="button"
            onClick={onGenerateAICommit}
            class="shrink-0 rounded-md p-1.5 text-[var(--violet-text)] hover:bg-[var(--violet-surface)] transition-colors cursor-pointer"
            title={t("inspector.diffs.generateCommitTooltip")}
            aria-label={t("inspector.diffs.generateCommitBtn")}
          >
            <Sparkles size={11} />
          </button>
          <div class="col-span-2 flex items-center justify-end gap-1.5">
            <button
              type="button"
              disabled={!commitMsg().trim() || committing() || pushing()}
              onClick={() => void onCommitWorktree(false)}
              class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 transition-colors cursor-pointer"
            >
              <GitCommitHorizontal size={12} />
              <span>
                {committing() && !pushing()
                  ? t("inspector.diffs.committing")
                  : t("inspector.diffs.commitBtn", { count: diffs().length })}
              </span>
            </button>
            <button
              type="button"
              disabled={!commitMsg().trim() || committing() || pushing()}
              onClick={() => void onCommitWorktree(true)}
              class="inline-flex items-center gap-1 rounded-md bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-semibold text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-40 transition-opacity cursor-pointer"
            >
              <ArrowUpFromLine size={11} />
              <span>
                {pushing()
                  ? t("inspector.diffs.pushingBtn")
                  : t("inspector.diffs.commitAndPushBtn")}
              </span>
            </button>
          </div>
        </div>
      </Show>

      <Show when={committedHash()}>
        {(hash) => (
          <div class="shrink-0 flex items-center justify-between gap-2 px-3 py-1.5 bg-[var(--ok-surface)] text-[11px] font-mono text-[var(--ok-text)] border-b border-[var(--ok-border)]">
            <div class="flex items-center gap-1.5 min-w-0 truncate">
              <Check size={11} class="shrink-0" />
              <span class="truncate">{t("inspector.diffs.commitSuccess", { hash: hash() })}</span>
            </div>
            <Show when={compareURL()}>
              <button
                type="button"
                disabled={pushing()}
                onClick={() => void onOpenCreatePR()}
                class="shrink-0 inline-flex items-center gap-1 rounded-md border border-[var(--ok-border)] bg-[var(--bg-panel)]/80 px-2 py-0.5 text-[10.5px] font-sans font-medium text-[var(--ok-text)] hover:bg-[var(--bg-panel)] cursor-pointer"
              >
                <GitPullRequestArrow size={10} />
                <span>{t("inspector.diffs.createPrBtn")}</span>
                <ExternalLink size={9} />
              </button>
            </Show>
          </div>
        )}
      </Show>

      <div class="inspector-scroll flex-1 min-h-0 overflow-y-auto px-2 py-1.5 space-y-1.5">
        <Show when={workspace.activeSessionID}>
          {(sid) => <ObserverAuditBanner sessionID={sid()} />}
        </Show>
        <Show
          when={previewDiffs().length > 0}
          fallback={
            <DiffEmptyState
              refreshing={refreshing()}
              onRefresh={() => void onRefresh()}
            />
          }
        >
          <Show
            when={selectedDiff()}
            fallback={
              <DiffFileTree
                diffs={previewDiffs()}
                onSelectFile={(file) => setWorkspace("inspectedDiffFile", file)}
              />
            }
          >
            {(activeDiff) => (
              <SelectedDiffDetail
                diff={activeDiff()}
                diffStyle={diffStyle()}
                currentIndex={selectedIndex()}
                totalFiles={previewDiffs().length}
                onBack={() => setWorkspace("inspectedDiffFile", null)}
                onPrev={() => {
                  const prev = previewDiffs()[selectedIndex() - 1];
                  if (prev) setWorkspace("inspectedDiffFile", prev.file);
                }}
                onNext={() => {
                  const next = previewDiffs()[selectedIndex() + 1];
                  if (next) setWorkspace("inspectedDiffFile", next.file);
                }}
              />
            )}
          </Show>
        </Show>
      </div>
    </div>
  );
}
