// web/src/components/inspector/PierreDiffViewer.tsx
import { createEffect, createMemo, createSignal, on, onCleanup, Show } from "solid-js";
import { Check, GitCommitHorizontal, RefreshCw, ShieldCheck, Sparkles } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  appSettings,
  generateSmartCommitMessage,
  loadSessionMessages,
  refreshWorktreeStatus,
  runSessionCodeAudit,
  setWorkspace,
  updateAppSetting,
  workspace,
} from "../../store/workspace";
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

export default function PierreDiffViewer() {
  const diffStyle = () => appSettings.defaultDiffStyle;
  const setDiffStyle = (mode: "unified" | "split") =>
    updateAppSetting("defaultDiffStyle", mode);
  const [refreshing, setRefreshing] = createSignal(false);
  const [commitMsg, setCommitMsg] = createSignal("");
  const [committing, setCommitting] = createSignal(false);
  const [committedHash, setCommittedHash] = createSignal<string | null>(null);

  const sessionKey = () => workspace.activeSessionID || "__root__";

  const diffs = createMemo(() => {
    const id = workspace.activeSessionID;
    if (workspace.diffScope === "worktree") {
      return workspace.worktreeDiffsBySession[sessionKey()] ?? [];
    }
    return id
      ? workspace.diffsBySession[id] ?? []
      : workspace.diffsBySession[sessionKey()] ?? [];
  });

  // The clicked turn may describe a historical edit absent from the current Git diff.
  // Keep that preview separate from the live diff list used for totals and commits.
  const previewDiffs = createMemo(() => {
    const list = diffs();
    const clicked = workspace.inspectedFileDiff;
    if (!clicked || !workspace.inspectedDiffFile) return list;
    const index = list.findIndex((item) => item.file === clicked.file ||
      item.file.endsWith(`/${clicked.file}`) || clicked.file.endsWith(`/${item.file}`));
    if (index < 0) return [...list, clicked];
    const current = list[index];
    if (current.patch || typeof current.before === "string" || typeof current.after === "string") return list;
    return list.map((item, i) => i === index ? clicked : item);
  });

  const selectedIndex = createMemo(() => {
    const target = workspace.inspectedDiffFile;
    if (!target) return -1;
    const list = previewDiffs();
    return list.findIndex(
      (d) =>
        d.file === target ||
        d.file.endsWith(`/${target}`) ||
        target.endsWith(`/${d.file}`)
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
    if (!root && !id) return;
    void refreshWorktreeStatus(id);
    const timer = setInterval(() => {
      if (!document.hidden) void refreshWorktreeStatus(id);
    }, 5000);
    onCleanup(() => clearInterval(timer));
  }));

  const onRefresh = async () => {
    if (refreshing()) return;
    setRefreshing(true);
    try {
      const id = workspace.activeSessionID;
      if (id) {
        await loadSessionMessages(id);
      }
      await refreshWorktreeStatus(id);
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

  const onCommitWorktree = async () => {
    const id = sessionKey();
    const msg = commitMsg().trim();
    if (!msg || committing()) return;
    setCommitting(true);
    try {
      const res = await hoxiAPI.commitWorktree(id, msg);
      setCommitMsg("");
      setCommittedHash(res.commit || "HEAD");
      setWorkspace("inspectedDiffFile", null);
      setTimeout(() => setCommittedHash(null), 4000);
      await refreshWorktreeStatus(workspace.activeSessionID);
      if (workspace.activeSessionID) {
        await loadSessionMessages(workspace.activeSessionID);
      }
    } catch (err) {
      console.error("commitWorktree failed:", err);
    } finally {
      setCommitting(false);
    }
  };

  return (
    <div class="flex flex-col h-full">
      <div class="shrink-0 flex gap-1 px-3 py-2 border-b border-[var(--border-subtle)]">
        <button type="button" class={`rounded-md px-2 py-1 text-[11px] cursor-pointer ${workspace.diffScope === "worktree" ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]" : "text-[var(--text-muted)]"}`}
          onClick={() => setWorkspace("diffScope", "worktree")}>
          {t("inspector.diffs.scopeWorktree")}
        </button>
        <button type="button" class={`rounded-md px-2 py-1 text-[11px] cursor-pointer ${workspace.diffScope === "session" ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]" : "text-[var(--text-muted)]"}`}
          onClick={() => setWorkspace("diffScope", "session")}>
          {t("inspector.diffs.scopeSession")}
        </button>
      </div>
      <div class="shrink-0 flex items-center justify-between px-3 py-2 border-b border-[var(--border-subtle)]">
        <Show
          when={previewDiffs().length > 0}
          fallback={
            <div class="flex items-center gap-2 text-[11.5px] text-[var(--text-secondary)]">
              <span class="inline-flex items-center gap-1.5 font-medium">
                <span>{workspace.diffScope === "session" ? t("inspector.diffs.sessionEmpty")
                  : workspace.worktreeBySession[sessionKey()]?.dirty === false ? t("inspector.diffs.cleanStatus")
                  : workspace.worktreeBySession[sessionKey()]?.dirty ? t("inspector.diffs.envStatusDirty")
                  : t("inspector.diffs.envStatusUnknown")}</span>
              </span>
              <span class="text-[var(--text-faint)]">·</span>
              <span class="text-[var(--text-muted)]">
                0 {t("inspector.diffs.files")}
              </span>
            </div>
          }
        >
          <button
            type="button"
            onClick={() => setWorkspace("inspectedDiffFile", null)}
            class="flex items-center gap-2 text-[11.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            title={t("inspector.diffs.backToTree")}
          >
            <span class="font-mono">
              <span class="text-[var(--ok-text)]">+{totals().add}</span>{" "}
              <span class="text-[var(--danger-text)]">-{totals().del}</span>
            </span>
            <span class="text-[var(--text-faint)]">·</span>
            <span>
              {diffs().length} {t("inspector.diffs.files")}
            </span>
          </button>
        </Show>

        <div class="flex items-center gap-1.5">
          <Show when={selectedDiff()}>
            <div class="flex rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5">
              <button
                type="button"
                class={`rounded-md px-2 py-0.5 text-[12px] font-medium transition-colors cursor-pointer ${
                  diffStyle() === "unified"
                    ? "bg-[var(--bg-panel)] text-[var(--accent-primary)] shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
                onClick={() => setDiffStyle("unified")}
              >
                {t("inspector.diffs.unified")}
              </button>
              <button
                type="button"
                class={`rounded-md px-2 py-0.5 text-[12px] font-medium transition-colors cursor-pointer ${
                  diffStyle() === "split"
                    ? "bg-[var(--bg-panel)] text-[var(--accent-primary)] shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
                onClick={() => setDiffStyle("split")}
              >
                {t("inspector.diffs.split")}
              </button>
            </div>
          </Show>
          <button
            type="button"
            class="p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            onClick={() => void onRefresh()}
            title={t("inspector.diffs.refresh")}
          >
            <RefreshCw size={13} class={refreshing() ? "animate-spin" : ""} />
          </button>
        </div>
      </div>

      {/* Kilo AI Commit Message Generator & Worktree Commit Bar */}
      <Show when={diffs().length > 0}>
        <div class="shrink-0 flex flex-wrap items-center gap-1.5 px-2.5 py-2 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]/60">
          <input
            type="text"
            value={commitMsg()}
            onInput={(ev) => setCommitMsg(ev.currentTarget.value)}
            onKeyDown={(ev) => {
              if (ev.key === "Enter" && !ev.isComposing) {
                ev.preventDefault();
                void onCommitWorktree();
              }
            }}
            placeholder={t("inspector.diffs.commitPlaceholder")}
            class="flex-1 min-w-[120px] rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] font-mono text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
          />
          <button
            type="button"
            disabled={isAuditing() || !workspace.activeSessionID}
            onClick={() =>
              workspace.activeSessionID &&
              void runSessionCodeAudit(workspace.activeSessionID, true)
            }
            class="shrink-0 inline-flex items-center gap-1 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-1 text-[12px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
            title={t("inspector.diffs.auditTooltip")}
          >
            <ShieldCheck size={11} />
            <span>
              {isAuditing()
                ? t("inspector.diffs.auditingBtn")
                : t("inspector.diffs.auditBtn")}
            </span>
          </button>
          <button
            type="button"
            onClick={onGenerateAICommit}
            class="shrink-0 inline-flex items-center gap-1 rounded-lg border border-[var(--violet-border)] bg-[var(--violet-surface)] px-2 py-1 text-[12px] font-medium text-[var(--violet-text)] hover:opacity-90 transition-opacity cursor-pointer"
            title={t("inspector.diffs.generateCommitTooltip")}
          >
            <Sparkles size={11} />
            <span>{t("inspector.diffs.generateCommitBtn")}</span>
          </button>
          <button
            type="button"
            disabled={!commitMsg().trim() || committing()}
            onClick={() => void onCommitWorktree()}
            class="shrink-0 inline-flex items-center gap-1 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[12px] font-semibold text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-40 transition-opacity cursor-pointer"
          >
            <GitCommitHorizontal size={12} />
            <span>
              {committing()
                ? t("inspector.diffs.committing")
                : t("inspector.diffs.commitBtn", { count: diffs().length })}
            </span>
          </button>
        </div>
      </Show>

      <Show when={committedHash()}>
        {(hash) => (
          <div class="shrink-0 flex items-center gap-1.5 px-3 py-1.5 bg-[var(--ok-surface)] text-[11px] font-mono text-[var(--ok-text)] border-b border-[var(--ok-border)]">
            <Check size={11} />
            <span>{t("inspector.diffs.commitSuccess", { hash: hash() })}</span>
          </div>
        )}
      </Show>

      <Show when={workspace.activeSessionID}>
        {(sid) => <ObserverAuditBanner sessionID={sid()} />}
      </Show>

      <div class="flex-1 min-h-0 overflow-y-auto px-2.5 py-2.5 space-y-2.5">
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
