// web/src/components/inspector/multi-repo-diff-view.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  ArrowUpFromLine,
  Check,
  ChevronRight,
  ExternalLink,
  FolderGit2,
  GitBranch,
  GitCommitHorizontal,
  GitPullRequestArrow,
  Sparkles,
} from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import type { WorktreeRepoStatus } from "../../services/api-types";
import type { SnapshotFileDiff } from "../../services/sse-types";
import {
  activeDirectory,
  generateSmartCommitMessage,
  loadSessionMessagesOnly,
  refreshWorktreeStatus,
  workspace,
} from "../../store/workspace";
import { DiffChanges } from "../icons";
import { t } from "../../i18n";
import DiffFileTree from "./diff-file-tree";
import { openRemoteCompareURL, synthesizeAICommitMessage } from "./pierre-diff-parts";
import AICommitFallbackToast from "./ai-commit-fallback-toast";

export interface RepoDiffBucket {
  key: string;
  repo: string;
  name: string;
  directory: string;
  branch: string;
  headCommit?: string;
  headSubject?: string;
  dirty: boolean;
  aheadCount: number;
  behindCount: number;
  hasUpstream: boolean;
  compareURL?: string;
  diffs: SnapshotFileDiff[];
  additions: number;
  deletions: number;
  isActive: boolean;
}

function RepoDiffCard(props: {
  bucket: RepoDiffBucket;
  onSelectFile: (file: string) => void;
}) {
  const [collapsed, setCollapsed] = createSignal(false);
  const [commitOpen, setCommitOpen] = createSignal(false);
  const [commitMsg, setCommitMsg] = createSignal("");
  const [committing, setCommitting] = createSignal(false);
  const [pushing, setPushing] = createSignal(false);
  const [committedHash, setCommittedHash] = createSignal<string | null>(null);
  const [lastCompareURL, setLastCompareURL] = createSignal<string | null>(null);

  const sessionKey = () => workspace.activeSessionID || "__root__";
  const repoDir = () =>
    props.bucket.directory || activeDirectory() || workspace.rootDir || undefined;
  const compareURL = () => lastCompareURL() || props.bucket.compareURL || "";

  const onGenerateAICommit = async () => {
    const repoDiffs = props.bucket.diffs;
    setCommitMsg(synthesizeAICommitMessage(workspace.activeSessionID, repoDiffs));
    const smart = await generateSmartCommitMessage(workspace.activeSessionID, repoDiffs);
    if (smart) setCommitMsg(smart);
  };

  const onCommitRepo = async (push = false) => {
    const msg = commitMsg().trim();
    if (!msg || committing() || pushing()) return;
    setCommitting(true);
    if (push) setPushing(true);
    try {
      const res = await hoxiAPI.commitWorktree(sessionKey(), msg, {
        push,
        directory: repoDir(),
      });
      setCommitMsg("");
      setCommitOpen(false);
      if (res.compareURL) setLastCompareURL(res.compareURL);
      setCommittedHash(res.commit || "HEAD");
      setTimeout(() => setCommittedHash(null), 5000);
      await refreshWorktreeStatus(workspace.activeSessionID);
      if (workspace.activeSessionID) {
        await loadSessionMessagesOnly(workspace.activeSessionID);
      }
    } catch (err) {
      console.error("commitWorktree (repo) failed:", err);
    } finally {
      setCommitting(false);
      setPushing(false);
    }
  };

  const onPushRepo = async (): Promise<string> => {
    if (pushing() || committing()) return compareURL();
    setPushing(true);
    try {
      const res = await hoxiAPI.pushWorktree(sessionKey(), repoDir());
      if (res.compareURL) setLastCompareURL(res.compareURL);
      await refreshWorktreeStatus(workspace.activeSessionID);
      return res.compareURL || compareURL();
    } catch (err) {
      console.error("pushWorktree (repo) failed:", err);
      return "";
    } finally {
      setPushing(false);
    }
  };

  const onOpenRepoPR = async () => {
    if (pushing() || committing()) return;
    let url = compareURL();
    if (props.bucket.aheadCount > 0 || !props.bucket.hasUpstream) {
      const pushedURL = await onPushRepo();
      if (pushedURL) url = pushedURL;
    }
    if (url) openRemoteCompareURL(url);
  };

  return (
    <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] overflow-hidden">
      {/* Repo Card Header */}
      <div class="flex items-center justify-between gap-2 px-2.5 py-1.5 border-b border-[var(--border-subtle)] bg-[var(--bg-panel)]/75">
        <button
          type="button"
          onClick={() => setCollapsed((v) => !v)}
          class="flex items-center gap-1.5 min-w-0 flex-1 text-left cursor-pointer group"
        >
          <span
            class="hoxi-chevron text-[var(--text-muted)] group-hover:text-[var(--text-primary)] shrink-0"
            data-open={!collapsed() ? "true" : "false"}
          >
            <ChevronRight size={13} />
          </span>
          <FolderGit2 size={13} class="text-[var(--accent-primary)] shrink-0" />
          <span
            class="font-semibold text-[11.5px] text-[var(--text-primary)] truncate"
            title={props.bucket.directory || props.bucket.repo}
          >
            {props.bucket.name}
          </span>
          <Show
            when={
              props.bucket.repo &&
              props.bucket.repo !== "." &&
              props.bucket.repo !== props.bucket.name
            }
          >
            <span
              class="text-[10px] font-mono text-[var(--text-faint)] truncate max-w-[120px]"
              title={props.bucket.repo}
            >
              {props.bucket.repo}
            </span>
          </Show>
          <Show when={props.bucket.branch}>
            <span class="shrink-0 inline-flex items-center gap-1 rounded bg-[var(--accent-surface)] px-1.5 py-0 text-[10px] font-mono text-[var(--accent-primary)] border border-[var(--accent-border)]">
              <GitBranch size={9} />
              <span>{props.bucket.branch}</span>
            </span>
          </Show>
          <Show when={props.bucket.behindCount > 0}>
            <span class="shrink-0 rounded bg-[var(--warn-surface)] px-1 py-0 text-[10px] font-mono text-[var(--warn-text)]">
              ↓{props.bucket.behindCount}
            </span>
          </Show>
        </button>

        {/* Per-Repo Actions & Diff Totals */}
        <div class="flex items-center gap-1.5 shrink-0">
          <Show when={props.bucket.diffs.length > 0}>
            <DiffChanges
              changes={{
                additions: props.bucket.additions,
                deletions: props.bucket.deletions,
              }}
            />
          </Show>

          <Show when={props.bucket.aheadCount > 0}>
            <button
              type="button"
              disabled={pushing() || committing()}
              onClick={() => void onPushRepo()}
              title={t("inspector.diffs.pushAheadBtn", {
                count: props.bucket.aheadCount,
              })}
              class="hoxi-press inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] font-mono text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-40 cursor-pointer"
            >
              <ArrowUpFromLine size={10} class={pushing() ? "animate-bounce" : ""} />
              <span>
                {pushing()
                  ? t("inspector.diffs.pushingBtn")
                  : `↑${props.bucket.aheadCount}`}
              </span>
            </button>
          </Show>

          <Show
            when={
              compareURL() &&
              (props.bucket.aheadCount > 0 ||
                props.bucket.hasUpstream ||
                Boolean(committedHash()))
            }
          >
            <button
              type="button"
              disabled={pushing() || committing()}
              onClick={() => void onOpenRepoPR()}
              title={t("inspector.diffs.createPrTooltip")}
              class="hoxi-press inline-flex items-center gap-1 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-primary)] hover:opacity-90 disabled:opacity-40 cursor-pointer"
            >
              <GitPullRequestArrow size={10} />
              <span>{t("inspector.diffs.createPrBtn")}</span>
              <ExternalLink size={8} />
            </button>
          </Show>

          <Show when={props.bucket.diffs.length > 0}>
            <button
              type="button"
              onClick={() => setCommitOpen((v) => !v)}
              title={t("inspector.diffs.commitBtn", {
                count: props.bucket.diffs.length,
              })}
              class={`hoxi-press inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10.5px] font-medium cursor-pointer ${
                commitOpen()
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] border border-[var(--accent-border)]"
                  : "border border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
              }`}
            >
              <GitCommitHorizontal size={11} />
              <span>
                {t("inspector.diffs.repoCommitAction", {
                  count: props.bucket.diffs.length,
                })}
              </span>
            </button>
          </Show>
        </div>
      </div>

      {/* Per-Repo Inline Commit Drawer */}
      <div
        class="hoxi-drawer-grid"
        data-open={props.bucket.diffs.length > 0 && commitOpen() ? "true" : "false"}
        aria-hidden={!(props.bucket.diffs.length > 0 && commitOpen())}
      >
        <div class="min-h-0 overflow-hidden">
          <div class="grid grid-cols-[1fr_auto] items-center gap-2 px-2.5 py-2 border-b border-[var(--border-subtle)] bg-[var(--bg-panel)]/50">
            <input
              type="text"
              value={commitMsg()}
              tabIndex={props.bucket.diffs.length > 0 && commitOpen() ? 0 : -1}
              onInput={(ev) => setCommitMsg(ev.currentTarget.value)}
              onKeyDown={(ev) => {
                if (ev.key === "Enter" && !ev.isComposing) {
                  ev.preventDefault();
                  void onCommitRepo(false);
                }
              }}
              placeholder={t("inspector.diffs.commitPlaceholder")}
              class="min-w-0 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11.5px] font-mono text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
            />
            <button
              type="button"
              onClick={() => void onGenerateAICommit()}
              class="hoxi-press shrink-0 rounded-md p-1.5 text-[var(--violet-text)] hover:bg-[var(--violet-surface)] cursor-pointer"
              title={t("inspector.diffs.generateCommitTooltip")}
            >
              <Sparkles size={11} />
            </button>
            <div class="col-span-2 flex items-center justify-end gap-1.5">
              <button
                type="button"
                disabled={!commitMsg().trim() || committing() || pushing()}
                onClick={() => void onCommitRepo(false)}
                class="hoxi-press inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer"
              >
                <GitCommitHorizontal size={11} />
                <span>
                  {committing() && !pushing()
                    ? t("inspector.diffs.committing")
                    : t("inspector.diffs.commitBtn", {
                        count: props.bucket.diffs.length,
                      })}
                </span>
              </button>
              <button
                type="button"
                disabled={!commitMsg().trim() || committing() || pushing()}
                onClick={() => void onCommitRepo(true)}
                class="hoxi-press inline-flex items-center gap-1 rounded-md bg-[var(--accent-primary)] px-2 py-0.5 text-[10.5px] font-semibold text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-40 cursor-pointer"
              >
                <ArrowUpFromLine size={10} />
                <span>
                  {pushing()
                    ? t("inspector.diffs.pushingBtn")
                    : t("inspector.diffs.commitAndPushBtn")}
                </span>
              </button>
            </div>
          </div>
        </div>
      </div>

      <AICommitFallbackToast />

      {/* Per-Repo Commit Toast */}
      <Show when={committedHash()}>
        {(hash) => (
          <div class="hoxi-view-enter flex items-center justify-between gap-2 px-2.5 py-1 bg-[var(--ok-surface)] text-[10.5px] font-mono text-[var(--ok-text)] border-b border-[var(--ok-border)]">
            <div class="flex items-center gap-1.5 min-w-0 truncate">
              <Check size={10} class="shrink-0" />
              <span class="truncate">
                {t("inspector.diffs.commitSuccess", { hash: hash() })}
              </span>
            </div>
          </div>
        )}
      </Show>

      {/* Repo File Tree or Ahead/Behind Hint */}
      <div
        class="hoxi-drawer-grid"
        data-open={!collapsed() ? "true" : "false"}
        aria-hidden={collapsed()}
      >
        <div class="min-h-0 overflow-hidden">
          <Show
            when={props.bucket.diffs.length > 0}
            fallback={
              <div class="flex items-center justify-between gap-2 px-3 py-2 text-[11px] text-[var(--text-muted)]">
                <span class="truncate">
                  {props.bucket.aheadCount > 0
                    ? t("inspector.diffs.repoCleanReadyToPush", {
                        count: props.bucket.aheadCount,
                      })
                    : t("inspector.diffs.repoBehindUpstream", {
                        count: props.bucket.behindCount,
                      })}
                </span>
                <Show when={props.bucket.headCommit}>
                  <span class="shrink-0 font-mono text-[10px] text-[var(--text-faint)] truncate max-w-[180px]">
                    {props.bucket.headCommit}{" "}
                    {props.bucket.headSubject ? `· ${props.bucket.headSubject}` : ""}
                  </span>
                </Show>
              </div>
            }
          >
            <div class="p-1.5">
              <DiffFileTree
                diffs={props.bucket.diffs}
                onSelectFile={props.onSelectFile}
                stripRepoPrefix={props.bucket.repo}
                hideHeader={true}
              />
            </div>
          </Show>
        </div>
      </div>
    </div>
  );
}

export default function MultiRepoDiffView(props: {
  repos: WorktreeRepoStatus[];
  diffs: SnapshotFileDiff[];
  onSelectFile: (file: string) => void;
}) {
  const [showCleanRepos, setShowCleanRepos] = createSignal(false);

  const grouped = createMemo(() => {
    const byDir = new Map<string, SnapshotFileDiff[]>();
    const byRepo = new Map<string, SnapshotFileDiff[]>();

    for (const d of props.diffs) {
      if (d.repoDir) {
        let list = byDir.get(d.repoDir);
        if (!list) {
          list = [];
          byDir.set(d.repoDir, list);
        }
        list.push(d);
      }
      const repoKey = d.repo || ".";
      let rList = byRepo.get(repoKey);
      if (!rList) {
        rList = [];
        byRepo.set(repoKey, rList);
      }
      rList.push(d);
    }

    const matchedDiffs = new Set<SnapshotFileDiff>();
    const buckets: RepoDiffBucket[] = [];

    for (const r of props.repos) {
      const repoDiffs =
        (r.directory ? byDir.get(r.directory) : undefined) ??
        byRepo.get(r.repo || ".") ??
        [];
      let add = 0;
      let del = 0;
      for (const d of repoDiffs) {
        matchedDiffs.add(d);
        add += d.additions || 0;
        del += d.deletions || 0;
      }
      const dirty = repoDiffs.length > 0 || Boolean(r.dirty);
      const ahead = r.aheadCount ?? 0;
      const behind = r.behindCount ?? 0;
      buckets.push({
        key: r.directory || r.repo || r.name,
        repo: r.repo || ".",
        name: r.name || r.repo || "repo",
        directory: r.directory || "",
        branch: r.branch || "",
        headCommit: r.headCommit,
        headSubject: r.headSubject,
        dirty,
        aheadCount: ahead,
        behindCount: behind,
        hasUpstream: Boolean(r.hasUpstream),
        compareURL: r.compareURL,
        diffs: repoDiffs,
        additions: repoDiffs.length > 0 ? add : r.additions || 0,
        deletions: repoDiffs.length > 0 ? del : r.deletions || 0,
        isActive: repoDiffs.length > 0 || dirty || ahead > 0 || behind > 0,
      });
    }

    // Include any diffs from session history whose repo wasn't in props.repos
    const orphanByRepo = new Map<string, SnapshotFileDiff[]>();
    for (const d of props.diffs) {
      if (matchedDiffs.has(d)) continue;
      const key = d.repo || ".";
      let list = orphanByRepo.get(key);
      if (!list) {
        list = [];
        orphanByRepo.set(key, list);
      }
      list.push(d);
    }
    for (const [repoKey, repoDiffs] of orphanByRepo.entries()) {
      let add = 0;
      let del = 0;
      for (const d of repoDiffs) {
        add += d.additions || 0;
        del += d.deletions || 0;
      }
      const first = repoDiffs[0];
      const parts = repoKey.split("/").filter(Boolean);
      buckets.push({
        key: first?.repoDir || repoKey,
        repo: repoKey,
        name: parts[parts.length - 1] || repoKey,
        directory: first?.repoDir || "",
        branch: first?.branch || "",
        dirty: true,
        aheadCount: 0,
        behindCount: 0,
        hasUpstream: false,
        diffs: repoDiffs,
        additions: add,
        deletions: del,
        isActive: true,
      });
    }

    const active = buckets.filter((b) => b.isActive);
    const clean = buckets.filter((b) => !b.isActive);
    const dirtyCount = buckets.filter((b) => b.diffs.length > 0 || b.dirty).length;
    let totalAdd = 0;
    let totalDel = 0;
    for (const b of buckets) {
      totalAdd += b.additions;
      totalDel += b.deletions;
    }

    return {
      all: buckets,
      active,
      clean,
      dirtyCount,
      totalAdd,
      totalDel,
    };
  });

  return (
    <div class="space-y-2">
      {/* Multi-Repo Overview Bar */}
      <div class="flex items-center justify-between gap-2 px-2.5 py-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/60 text-[11px]">
        <div class="flex items-center gap-1.5 min-w-0 truncate text-[var(--text-secondary)] font-medium">
          <FolderGit2 size={13} class="text-[var(--accent-primary)] shrink-0" />
          <span class="truncate">
            {t("inspector.diffs.reposSummary", {
              total: grouped().all.length,
              dirty: grouped().dirtyCount,
              files: props.diffs.length,
            })}
          </span>
        </div>
        <DiffChanges
          changes={{
            additions: grouped().totalAdd,
            deletions: grouped().totalDel,
          }}
        />
      </div>

      {/* Active / Dirty / Unsynced Repositories */}
      <For each={grouped().active}>
        {(bucket) => (
          <RepoDiffCard bucket={bucket} onSelectFile={props.onSelectFile} />
        )}
      </For>

      {/* Clean & Synced Repositories Collapsed Summary Bar */}
      <Show when={grouped().clean.length > 0}>
        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 overflow-hidden">
          <button
            type="button"
            onClick={() => setShowCleanRepos((v) => !v)}
            class="w-full flex items-center justify-between gap-2 px-2.5 py-1.5 text-left text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/50 transition-colors cursor-pointer"
          >
            <div class="flex items-center gap-1.5 shrink-0">
              <span
                class="hoxi-chevron"
                data-open={showCleanRepos() ? "true" : "false"}
              >
                <ChevronRight size={12} />
              </span>
              <span class="font-medium">
                {t("inspector.diffs.cleanReposSummary", {
                  count: grouped().clean.length,
                })}
              </span>
            </div>
            <div class="flex items-center gap-1 min-w-0 overflow-hidden justify-end flex-wrap">
              <For each={grouped().clean.slice(0, 4)}>
                {(repo) => (
                  <span class="inline-flex items-center gap-1 rounded bg-[var(--bg-panel)] px-1.5 py-0 text-[10px] font-mono text-[var(--text-secondary)] border border-[var(--border-subtle)] truncate max-w-[140px]">
                    <span class="truncate">{repo.name}</span>
                    <Show when={repo.branch}>
                      <span class="text-[var(--text-faint)]">·</span>
                      <span class="text-[var(--text-muted)] truncate">{repo.branch}</span>
                    </Show>
                    <Check size={9} class="text-[var(--ok-text)] shrink-0" />
                  </span>
                )}
              </For>
            </div>
          </button>

          <div
            class="hoxi-drawer-grid"
            data-open={showCleanRepos() ? "true" : "false"}
            aria-hidden={!showCleanRepos()}
          >
            <div class="min-h-0 overflow-hidden">
              <div class="border-t border-[var(--border-subtle)] divide-y divide-[var(--border-subtle)]/60">
                <For each={grouped().clean}>
                  {(repo) => (
                    <div class="flex items-center justify-between gap-2 px-3 py-1.5 text-[11px]">
                      <div class="flex items-center gap-1.5 min-w-0 truncate">
                        <FolderGit2 size={12} class="text-[var(--text-muted)] shrink-0" />
                        <span
                          class="font-medium text-[var(--text-secondary)] truncate"
                          title={repo.directory || repo.repo}
                        >
                          {repo.name}
                        </span>
                        <Show when={repo.branch}>
                          <span class="inline-flex items-center gap-0.5 rounded bg-[var(--bg-panel)] px-1.5 py-0 text-[10px] font-mono text-[var(--text-muted)] border border-[var(--border-subtle)]">
                            <GitBranch size={9} />
                            <span>{repo.branch}</span>
                          </span>
                        </Show>
                      </div>
                      <div class="flex items-center gap-1.5 shrink-0 font-mono text-[10px] text-[var(--text-faint)]">
                        <Show when={repo.headCommit}>
                          <span class="truncate max-w-[180px]" title={repo.headSubject}>
                            {repo.headCommit}
                            {repo.headSubject ? ` · ${repo.headSubject}` : ""}
                          </span>
                        </Show>
                        <Check size={10} class="text-[var(--ok-text)]" />
                      </div>
                    </div>
                  )}
                </For>
              </div>
            </div>
          </div>
        </div>
      </Show>
    </div>
  );
}
