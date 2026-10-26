// web/src/store/session-diffs.ts
// Session / worktree diff and git-status refresh: debounced scheduling, request
// revisions (stale-read guard) and change-detecting store writes.
import { unwrap } from "solid-js/store";
import { hoxiAPI, type WorktreeMeta, type WorktreeRepoStatus } from "../services/api";
import type { SnapshotFileDiff } from "../services/sse-types";
import { filterLocalSessionDiffsByTurns } from "./message-lookup";
import { isSessionDeleted, sessionDirectory } from "./session-query";
import { secondaryWorkspaceDirs } from "./workspace-folders";
import { setWorkspace, workspace } from "./workspace-state";

const diffRevisions = new Map<string, number>();

function areDiffListsEqual(prev: SnapshotFileDiff[] | undefined, next: SnapshotFileDiff[]): boolean {
  if (!prev || prev.length !== next.length) return false;
  for (let i = 0; i < prev.length; i++) {
    const a = prev[i], b = next[i];
    if (a.file !== b.file || a.status !== b.status || a.additions !== b.additions ||
      a.deletions !== b.deletions || a.stamp !== b.stamp || a.patch !== b.patch ||
      a.before !== b.before || a.after !== b.after || a.large !== b.large ||
      a.generatedLike !== b.generatedLike || a.repo !== b.repo ||
      a.repoDir !== b.repoDir || a.branch !== b.branch) return false;
  }
  return true;
}

function setDiffListIfChanged(
  field: "diffsBySession" | "worktreeDiffsBySession", key: string, next: SnapshotFileDiff[]
): void {
  if (!areDiffListsEqual(workspace[field][key], next)) {
    setWorkspace(field, key, next);
  }
}

export function applySessionDiffEvent(sessionID: string, diffs: SnapshotFileDiff[]): void {
  if (isSessionDeleted(sessionID)) return;
  diffRevisions.set(sessionID, (diffRevisions.get(sessionID) ?? 0) + 1);
  const next = workspace.worktreeBySession[sessionID]?.isolated
    ? diffs
    : filterLocalSessionDiffsByTurns(sessionID, diffs, unwrap(workspace.messagesBySession), workspace.rootDir);
  setDiffListIfChanged("diffsBySession", sessionID, next);
}

const diffRefreshTimers = new Map<string, ReturnType<typeof setTimeout>>();

/** Coalesces bursts (e.g. many settled subagents) into one parent diff refresh. */
export function scheduleRefreshSessionDiff(sessionID: string, delayMs = 150): void {
  const pending = diffRefreshTimers.get(sessionID);
  if (pending !== undefined) clearTimeout(pending);
  diffRefreshTimers.set(sessionID, setTimeout(() => {
    diffRefreshTimers.delete(sessionID);
    if (!isSessionDeleted(sessionID)) void refreshSessionDiff(sessionID);
  }, delayMs));
}

export async function refreshSessionDiff(
  sessionID: string,
  preloadedWorktreeDiffs?: SnapshotFileDiff[]
): Promise<void> {
  const directory = sessionDirectory(sessionID);
  const revision = (diffRevisions.get(sessionID) ?? 0) + 1;
  diffRevisions.set(sessionID, revision);
  const isCurrent = () => diffRevisions.get(sessionID) === revision &&
    sessionDirectory(sessionID) === directory && !isSessionDeleted(sessionID);
  try {
    let diffs = await hoxiAPI.getSessionDiff(sessionID, directory);
    if (!isCurrent()) return;
    const meta = workspace.worktreeBySession[sessionID];
    // Only an isolated worktree can safely attribute directory-wide changes to this session.
    if (!diffs.length && meta?.isolated && meta.dirty && meta.directory === directory) {
      diffs = preloadedWorktreeDiffs ?? (await hoxiAPI.getWorktreeDiff(sessionID, directory));
    } else if (!meta?.isolated) {
      diffs = filterLocalSessionDiffsByTurns(sessionID, diffs, unwrap(workspace.messagesBySession), workspace.rootDir);
    }
    if (isCurrent()) setDiffListIfChanged("diffsBySession", sessionID, diffs);
  } catch (err) {
    console.error("refresh session diff failed:", err);
  }
}

const worktreeRefreshTimers = new Map<string, ReturnType<typeof setTimeout>>();

const worktreeRevisions = new Map<string, number>();

export function invalidateSessionDiffReads(id: string): void {
  diffRevisions.set(id, (diffRevisions.get(id) ?? 0) + 1);
  worktreeRevisions.set(id, (worktreeRevisions.get(id) ?? 0) + 1);
  cancelScheduledWorktreeStatus(id);
  clearTimeout(diffRefreshTimers.get(id));
  diffRefreshTimers.delete(id);
}

export function cancelScheduledWorktreeStatus(sessionID?: string | null): void {
  const key = sessionID || "__root__";
  const timer = worktreeRefreshTimers.get(key);
  if (timer !== undefined) clearTimeout(timer);
  worktreeRefreshTimers.delete(key);
}

export function scheduleRefreshWorktreeStatus(
  sessionID?: string | null,
  delayMs = 900
): void {
  const key = sessionID || "__root__";
  cancelScheduledWorktreeStatus(key);
  const timer = setTimeout(() => {
    worktreeRefreshTimers.delete(key);
    void refreshWorktreeStatus(key);
  }, delayMs);
  worktreeRefreshTimers.set(key, timer);
}

export async function refreshWorktreeStatus(sessionID?: string | null): Promise<void> {
  const key = sessionID || "__root__";
  const dirArg = key === "__root__" ? workspace.rootDir || undefined : sessionDirectory(key);
  const revision = (worktreeRevisions.get(key) ?? 0) + 1;
  worktreeRevisions.set(key, revision);
  const isCurrentScope = () => worktreeRevisions.get(key) === revision && !isSessionDeleted(key) && dirArg ===
    (key === "__root__" ? workspace.rootDir || undefined : sessionDirectory(key));
  try {
    const extraDirs = workspace.worktreeBySession[key]?.isolated ? [] : secondaryWorkspaceDirs(dirArg);
    const st = extraDirs.length > 0
      ? await hoxiAPI.getWorktreeStatus(key, dirArg, extraDirs)
      : await hoxiAPI.getWorktreeStatus(key, dirArg);
    if (!isCurrentScope()) return;
    const prevMeta = workspace.worktreeBySession[key];
    const nextMeta: WorktreeMeta = {
      sessionID: key,
      isolated: st.isolated,
      directory: st.directory || dirArg || "",
      branch: st.branch || prevMeta?.branch,
      baseBranch: st.baseBranch || prevMeta?.baseBranch,
      baseCommit: st.baseCommit || prevMeta?.baseCommit,
      headCommit: st.headCommit || prevMeta?.headCommit,
      headSubject: st.headSubject || prevMeta?.headSubject,
      createdAt: prevMeta?.createdAt || new Date().toISOString(),
      dirty: st.dirty,
      changedFiles: st.changedFiles,
      uncommittedLines: st.uncommittedLines,
      additions: st.additions,
      deletions: st.deletions,
      aheadCount: st.aheadCount,
      behindCount: st.behindCount,
      hasUpstream: st.hasUpstream,
      compareURL: st.compareURL,
      health: st.health,
      repos: st.repos,
    };
    const statusSignatureUnchanged = Boolean(
      prevMeta &&
      prevMeta.isolated === nextMeta.isolated &&
      prevMeta.directory === nextMeta.directory &&
      prevMeta.branch === nextMeta.branch &&
      prevMeta.baseCommit === nextMeta.baseCommit &&
      prevMeta.headCommit === nextMeta.headCommit &&
      prevMeta.dirty === nextMeta.dirty &&
      prevMeta.changedFiles === nextMeta.changedFiles &&
      prevMeta.uncommittedLines === nextMeta.uncommittedLines &&
      prevMeta.additions === nextMeta.additions &&
      prevMeta.deletions === nextMeta.deletions &&
      areRepoStatusListsEqual(prevMeta.repos, nextMeta.repos)
    );
    setWorktreeMetaIfChanged(key, nextMeta);
    if (key === "__root__" || key === workspace.activeSessionID) {
      const cachedWorktreeDiffs = workspace.worktreeDiffsBySession[key];
      const canReuseWorktreeDiffs =
        statusSignatureUnchanged &&
        Array.isArray(cachedWorktreeDiffs) &&
        cachedWorktreeDiffs.length > 0;
      const diffs = st.dirty
        ? (canReuseWorktreeDiffs
            ? cachedWorktreeDiffs
            : extraDirs.length > 0
              ? await hoxiAPI.getWorktreeDiff(key, dirArg, extraDirs)
              : await hoxiAPI.getWorktreeDiff(key, dirArg))
        : [];
      if (!isCurrentScope()) return;
      setDiffListIfChanged("worktreeDiffsBySession", key, diffs);
      if (key === "__root__") {
        setDiffListIfChanged("diffsBySession", key, diffs);
      } else {
        await refreshSessionDiff(key, diffs);
      }
    }
  } catch (err) {
    console.error("refresh worktree status failed:", err);
  }
}

function areRepoStatusListsEqual(
  prev: WorktreeRepoStatus[] | undefined,
  next: WorktreeRepoStatus[] | undefined
): boolean {
  if (!prev && !next) return true;
  if (!prev || !next || prev.length !== next.length) return false;
  for (let i = 0; i < prev.length; i++) {
    const a = prev[i];
    const b = next[i];
    if (
      a.repo !== b.repo ||
      a.name !== b.name ||
      a.directory !== b.directory ||
      a.branch !== b.branch ||
      a.headCommit !== b.headCommit ||
      a.headSubject !== b.headSubject ||
      a.dirty !== b.dirty ||
      a.changedFiles !== b.changedFiles ||
      a.additions !== b.additions ||
      a.deletions !== b.deletions ||
      a.aheadCount !== b.aheadCount ||
      a.behindCount !== b.behindCount ||
      a.hasUpstream !== b.hasUpstream ||
      a.compareURL !== b.compareURL
    ) {
      return false;
    }
  }
  return true;
}

export function setWorktreeMetaIfChanged(key: string, next: WorktreeMeta): void {
  const prev = workspace.worktreeBySession[key];
  if (
    prev &&
    prev.sessionID === next.sessionID &&
    prev.isolated === next.isolated &&
    prev.directory === next.directory &&
    prev.branch === next.branch &&
    prev.baseBranch === next.baseBranch &&
    prev.baseCommit === next.baseCommit &&
    prev.headCommit === next.headCommit &&
    prev.headSubject === next.headSubject &&
    prev.dirty === next.dirty &&
    prev.changedFiles === next.changedFiles &&
    prev.uncommittedLines === next.uncommittedLines &&
    prev.additions === next.additions &&
    prev.deletions === next.deletions &&
    prev.aheadCount === next.aheadCount &&
    prev.behindCount === next.behindCount &&
    prev.hasUpstream === next.hasUpstream &&
    prev.compareURL === next.compareURL &&
    prev.health === next.health &&
    prev.setupScript === next.setupScript &&
    prev.setupStatus === next.setupStatus &&
    prev.setupError === next.setupError &&
    prev.allocatedPort === next.allocatedPort &&
    prev.runStatus === next.runStatus &&
    prev.previewURL === next.previewURL &&
    areRepoStatusListsEqual(prev.repos, next.repos)
  ) {
    return;
  }
  setWorkspace("worktreeBySession", key, next);
}
