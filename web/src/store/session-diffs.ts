// web/src/store/session-diffs.ts
// Session / worktree diff and git-status refresh: debounced scheduling, request
// revisions (stale-read guard) and change-detecting store writes.
import { unwrap } from "solid-js/store";
import { hoxiAPI, isRunnerSessionID, type WorktreeMeta, type WorktreeRepoStatus } from "../services/api";
import type { SnapshotFileDiff } from "../services/sse-types";
import { hasOlderSessionHistory } from "./session-history";
import { isSessionDeleted, sessionDirectory } from "./session-query";
import { normalizeFolderPath, secondaryWorkspaceDirs } from "./workspace-folders";
import { type MessageTurn, setWorkspace, workspace } from "./workspace-state";

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

// Runner sessions are already scoped server-side from their full transcript;
// re-filtering by a partially loaded (paged) transcript would drop files that
// were only edited in older, not yet loaded turns.
function scopeLocalSessionDiffs(sessionID: string, diffs: SnapshotFileDiff[]): SnapshotFileDiff[] {
  if (isRunnerSessionID(sessionID) && hasOlderSessionHistory(sessionID)) return diffs;
  return filterLocalSessionDiffsByTurns(sessionID, diffs, unwrap(workspace.messagesBySession), workspace.rootDir);
}

export function applySessionDiffEvent(sessionID: string, diffs: SnapshotFileDiff[]): void {
  if (isSessionDeleted(sessionID)) return;
  diffRevisions.set(sessionID, (diffRevisions.get(sessionID) ?? 0) + 1);
  const next = workspace.worktreeBySession[sessionID]?.isolated
    ? diffs
    : scopeLocalSessionDiffs(sessionID, diffs);
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
      diffs = scopeLocalSessionDiffs(sessionID, diffs);
    }
    if (isCurrent()) setDiffListIfChanged("diffsBySession", sessionID, diffs);
  } catch (err) {
    console.error("refresh session diff failed:", err);
  }
}

const worktreeRefreshTimers = new Map<string, ReturnType<typeof setTimeout>>();

const worktreeRevisions = new Map<string, number>();
const inFlightWorktreeDirByKey = new Map<string, string>();

export function isWorktreeStatusLoadedOrInFlight(
  sessionID: string | null | undefined,
  directory: string
): boolean {
  const key = sessionID || "__root__";
  const normDir = normalizeFolderPath(directory);
  if (!normDir) return false;
  if (inFlightWorktreeDirByKey.get(key) === normDir) return true;
  return normalizeFolderPath(workspace.worktreeBySession[key]?.directory) === normDir;
}

export function invalidateSessionDiffReads(id: string): void {
  diffRevisions.set(id, (diffRevisions.get(id) ?? 0) + 1);
  worktreeRevisions.set(id, (worktreeRevisions.get(id) ?? 0) + 1);
  inFlightWorktreeDirByKey.delete(id);
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
  const normDirArg = normalizeFolderPath(dirArg);
  const revision = (worktreeRevisions.get(key) ?? 0) + 1;
  worktreeRevisions.set(key, revision);
  if (normDirArg) {
    inFlightWorktreeDirByKey.set(key, normDirArg);
  }
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
  } finally {
    if (worktreeRevisions.get(key) === revision) {
      inFlightWorktreeDirByKey.delete(key);
    }
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

const SESSION_EDITING_TOOLS = new Set([
  "edit",
  "write",
  "patch",
  "multiedit",
  "apply_patch",
  "str_replace_based_edit_tool",
  "create_file",
  "write_to_file",
  "replace_file_content",
  "multi_replace_file_content",
  "file_edit",
  "notebookedit",
]);

function normalizeSessionRelPath(raw: string, rootDir: string): string {
  let trimmed = (raw || "").trim().replace(/\\/g, "/");
  if (!trimmed) return "";
  const root = (rootDir || "").trim().replace(/\\/g, "/").replace(/\/+$/, "");
  if (root && (trimmed === root || trimmed.startsWith(`${root}/`))) {
    trimmed = trimmed.slice(root.length).replace(/^\/+/, "");
  } else {
    trimmed = trimmed.replace(/^\.\/+/, "");
  }
  return trimmed.replace(/^\.hoxi\/worktrees\/[^/]+\/+/, "");
}

export function collectSessionTouchedPaths(
  sessionID: string,
  turnsBySession: Record<string, MessageTurn[]>,
  rootDir: string
): string[] {
  const turns = turnsBySession[sessionID];
  if (!turns || turns.length === 0) return [];

  const touched = new Set<string>();
  const addPath = (raw?: unknown) => {
    if (typeof raw !== "string") return;
    const norm = normalizeSessionRelPath(raw, rootDir);
    if (norm) touched.add(norm);
  };

  const visited = new Set<string>();
  const scanSession = (sid: string) => {
    if (!sid || visited.has(sid)) return;
    visited.add(sid);
    for (const turn of turnsBySession[sid] ?? []) {
      for (const p of turn.parts) {
        if (p.type !== "tool") continue;
        const toolName = (p.tool || "").toLowerCase();
        if (toolName === "task" || toolName === "agent" || toolName.includes("subagent")) {
          const meta = p.state.status !== "pending" ? (p.state.metadata as Record<string, any> | undefined) : undefined;
          const inp = (p.state.input || {}) as Record<string, any>;
          const out = (p.state as { output?: string }).output;
          const outMatch = typeof out === "string" ? out.match(/session_id:\s*([a-zA-Z0-9_-]+)/) : null;
          const childID = meta?.sessionId || meta?.sessionID || inp.task_id || outMatch?.[1];
          if (typeof childID === "string") scanSession(childID);
          continue;
        }
        if (p.state.status !== "completed" || !SESSION_EDITING_TOOLS.has(toolName)) continue;
        const inp = (p.state.input || {}) as Record<string, any>;
        addPath(
          inp.filePath ||
            inp.file_path ||
            inp.Path ||
            inp.AbsolutePath ||
            inp.path ||
            inp.file ||
            inp.TargetFile ||
            inp.target_file ||
            inp.targetFile ||
            inp.filename ||
            inp.notebook_path
        );
        if (typeof inp.patchText === "string") {
          for (const line of inp.patchText.split(/\r?\n/)) {
            const m = line.match(/^\*\*\*\s+(?:Update|Add|Delete)\s+File:\s*(.+)$/);
            if (m) addPath(m[1]);
          }
        }
        const meta = (p.state.metadata || {}) as Record<string, any>;
        addPath(meta.filediff?.file || meta.filediff?.path);
        if (Array.isArray(meta.files)) {
          for (const item of meta.files) {
            if (typeof item === "string") addPath(item);
            else if (item && typeof item === "object") {
              addPath(item.relativePath || item.filePath || item.file || item.path);
            }
          }
        }
      }
    }
  };

  scanSession(sessionID);
  return Array.from(touched);
}

export function filterLocalSessionDiffsByTurns(
  sessionID: string,
  diffs: SnapshotFileDiff[],
  turnsBySession: Record<string, MessageTurn[]>,
  rootDir: string
): SnapshotFileDiff[] {
  if (!diffs.length) return diffs;
  const turns = turnsBySession[sessionID];
  if (!turns || turns.length === 0) return diffs;

  const touchedList = collectSessionTouchedPaths(sessionID, turnsBySession, rootDir);
  if (touchedList.length === 0) return [];
  const touched = new Set(touchedList);

  return diffs.filter((d) => {
    const norm = normalizeSessionRelPath(d.file, rootDir);
    if (!norm) return false;
    if (touched.has(norm)) return true;
    for (const f of touched) {
      if (norm.endsWith(`/${f}`) || f.endsWith(`/${norm}`)) return true;
    }
    return false;
  });
}
