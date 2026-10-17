// web/src/components/inspector/PierreDiffViewer.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { Check, GitCommitHorizontal, RefreshCw, Sparkles } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  activeDirectory,
  loadSessionMessages,
  refreshWorktreeStatus,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import { DiffCard, FileDiffBlock, synthesizeAICommitMessage } from "./pierre-diff-parts";

export { FileDiffBlock };

export default function PierreDiffViewer() {
  const [diffStyle, setDiffStyle] = createSignal<"unified" | "split">("unified");
  const [refreshing, setRefreshing] = createSignal(false);
  const [commitMsg, setCommitMsg] = createSignal("");
  const [committing, setCommitting] = createSignal(false);
  const [committedHash, setCommittedHash] = createSignal<string | null>(null);

  const diffs = createMemo(() => {
    const id = workspace.activeSessionID;
    return id ? workspace.diffsBySession[id] ?? [] : [];
  });

  const totals = createMemo(() => {
    let add = 0;
    let del = 0;
    for (const d of diffs()) {
      add += d.additions || 0;
      del += d.deletions || 0;
    }
    return { add, del };
  });

  const onRefresh = async () => {
    const id = workspace.activeSessionID;
    if (!id || refreshing()) return;
    setRefreshing(true);
    try {
      await loadSessionMessages(id);
    } catch (err) {
      console.error(err);
    } finally {
      setRefreshing(false);
    }
  };

  const onGenerateAICommit = () => {
    const generated = synthesizeAICommitMessage(workspace.activeSessionID, diffs());
    setCommitMsg(generated);
  };

  const onCommitWorktree = async () => {
    const id = workspace.activeSessionID;
    const msg = commitMsg().trim();
    if (!id || !msg || committing()) return;
    setCommitting(true);
    try {
      const res = await hoxiAPI.commitWorktree(id, msg);
      setCommitMsg("");
      setCommittedHash(res.commit || "HEAD");
      setTimeout(() => setCommittedHash(null), 4000);
      await refreshWorktreeStatus(id);
      await loadSessionMessages(id);
    } catch (err) {
      console.error("commitWorktree failed:", err);
    } finally {
      setCommitting(false);
    }
  };

  return (
    <div class="flex flex-col h-full">
      <div class="shrink-0 flex items-center justify-between px-3 py-2 border-b border-[var(--border-subtle)]">
        <div class="flex items-center gap-2 text-[11.5px] text-[var(--text-secondary)]">
          <span class="font-mono">
            <span class="text-[var(--ok-text)]">+{totals().add}</span>{" "}
            <span class="text-[var(--danger-text)]">-{totals().del}</span>
          </span>
          <span class="text-[var(--text-faint)]">·</span>
          <span>
            {diffs().length} {t("inspector.diffs.files")}
          </span>
        </div>
        <div class="flex items-center gap-1.5">
          <div class="flex rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5">
            <button
              type="button"
              class={`rounded-md px-2 py-0.5 text-[10.5px] font-medium transition-colors cursor-pointer ${
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
              class={`rounded-md px-2 py-0.5 text-[10.5px] font-medium transition-colors cursor-pointer ${
                diffStyle() === "split"
                  ? "bg-[var(--bg-panel)] text-[var(--accent-primary)] shadow-2xs"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
              onClick={() => setDiffStyle("split")}
            >
              {t("inspector.diffs.split")}
            </button>
          </div>
          <button
            type="button"
            class="p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            onClick={onRefresh}
            title={t("inspector.diffs.refresh")}
          >
            <RefreshCw size={13} class={refreshing() ? "animate-spin" : ""} />
          </button>
        </div>
      </div>

      {/* Kilo AI Commit Message Generator & Worktree Commit Bar */}
      <Show when={diffs().length > 0 || committedHash()}>
        <div class="shrink-0 flex items-center gap-1.5 px-2.5 py-2 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]/60">
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
            class="flex-1 min-w-0 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] font-mono text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
          />
          <button
            type="button"
            onClick={onGenerateAICommit}
            class="shrink-0 inline-flex items-center gap-1 rounded-lg border border-[var(--violet-border)] bg-[var(--violet-surface)] px-2 py-1 text-[11px] font-medium text-[var(--violet-text)] hover:opacity-90 transition-opacity cursor-pointer"
            title={t("inspector.diffs.generateCommitTooltip")}
          >
            <Sparkles size={11} />
            <span>{t("inspector.diffs.generateCommitBtn")}</span>
          </button>
          <button
            type="button"
            disabled={!commitMsg().trim() || committing()}
            onClick={() => void onCommitWorktree()}
            class="shrink-0 inline-flex items-center gap-1 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-semibold text-white hover:opacity-90 disabled:opacity-40 transition-opacity cursor-pointer"
          >
            <GitCommitHorizontal size={12} />
            <span>
              {committing()
                ? t("inspector.diffs.committing")
                : t("inspector.diffs.commitBtn", { count: diffs().length })}
            </span>
          </button>
        </div>
        <Show when={committedHash()}>
          {(hash) => (
            <div class="shrink-0 flex items-center gap-1.5 px-3 py-1 bg-[var(--ok-surface)] text-[11px] font-mono text-[var(--ok-text)] border-b border-[var(--ok-border)]">
              <Check size={11} />
              <span>{t("inspector.diffs.commitSuccess", { hash: hash() })}</span>
            </div>
          )}
        </Show>
      </Show>

      <div class="flex-1 min-h-0 overflow-y-auto px-2.5 py-2.5 space-y-2.5">
        <Show
          when={diffs().length > 0}
          fallback={
            <div class="px-3 py-10 text-center text-[12px] text-[var(--text-muted)]">
              {t("inspector.diffs.empty")}
              <div class="mt-1 text-[10.5px] text-[var(--text-faint)] font-mono truncate">
                {activeDirectory() || "—"}
              </div>
            </div>
          }
        >
          <For each={diffs()}>
            {(diff) => <DiffCard diff={diff} diffStyle={diffStyle()} />}
          </For>
        </Show>
      </div>
    </div>
  );
}
