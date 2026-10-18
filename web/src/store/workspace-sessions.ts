import { produce } from "solid-js/store";
import type { SessionInfo } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import { mergeServerTurnsInto } from "./message-lookup";
import {
  ARCHIVED_SESSIONS_KEY,
  isSessionArchived,
  setWorkspace,
  workspace,
} from "./workspace";
import {
  getCustomSessionTitle,
  isPlaceholderSessionTitle,
  recordCustomSessionTitle,
  sortSessionsByActivity,
} from "./session-title";
import {
  collectChildSubagentSessionIDs,
  deleteChildSubagentSessionsBackend,
  purgeSessionsFromStoreState,
} from "./workspace-queue-subagents";

const deletedSessionIDs = new Set<string>();

export function isSessionDeleted(id: string): boolean {
  return deletedSessionIDs.has(id);
}

function persistArchivedSessionIDs(ids: string[]) {
  try {
    localStorage.setItem(ARCHIVED_SESSIONS_KEY, JSON.stringify(ids));
  } catch {
    /* ignore */
  }
}

function nextActiveSessionCandidate(excludeID?: string): SessionInfo | undefined {
  const candidates = workspace.sessions.filter(
    (s) => s.id !== excludeID && !s.parentID && !isSessionArchived(s)
  );
  if (candidates.length === 0) return undefined;
  return sortSessionsByActivity(candidates)[0];
}

export function activeSession(): SessionInfo | undefined {
  return workspace.sessions.find((s) => s.id === workspace.activeSessionID);
}

export function sessionDirectory(sessionID: string | null): string | undefined {
  if (!sessionID) return undefined;
  const meta = workspace.worktreeBySession[sessionID];
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (meta?.directory || session?.directory) {
    return meta?.directory || session?.directory;
  }
  if (session?.parentID) {
    const parentMeta = workspace.worktreeBySession[session.parentID];
    const parentSession = workspace.sessions.find((s) => s.id === session.parentID);
    if (parentMeta?.directory || parentSession?.directory) {
      return parentMeta?.directory || parentSession?.directory;
    }
  }
  return workspace.rootDir || undefined;
}

export function activeDirectory(): string | undefined {
  return sessionDirectory(workspace.activeSessionID);
}

export function isSessionBusy(sessionID: string | null): boolean {
  if (!sessionID) return false;
  const st = workspace.statusBySession[sessionID]?.type;
  return st === "busy" || st === "retry";
}

export function sessionsWithPendingPermission(): Set<string> {
  const set = new Set<string>();
  for (const p of workspace.pendingPermissions) set.add(p.sessionID);
  for (const q of workspace.pendingQuestions) set.add(q.sessionID);
  return set;
}

export function replaceSession(info: SessionInfo) {
  if (deletedSessionIDs.has(info.id)) return;
  const customTitle = getCustomSessionTitle(info.id);
  const effectiveInfo =
    customTitle && (isPlaceholderSessionTitle(info.title) || info.title !== customTitle)
      ? { ...info, title: customTitle }
      : info;
  setWorkspace("sessions", (prev) => {
    const idx = prev.findIndex((s) => s.id === effectiveInfo.id);
    if (idx < 0) return [effectiveInfo, ...prev];
    const next = prev.slice();
    next[idx] = effectiveInfo;
    return next;
  });
}

export async function loadSessionMessagesOnly(sessionID: string): Promise<void> {
  const directory = sessionDirectory(sessionID);
  const turns = await hoxiAPI.getMessages(sessionID, directory);
  setWorkspace(
    produce((state) => {
      mergeServerTurnsInto((state.messagesBySession[sessionID] ||= []), turns);
    })
  );
}

export async function loadSessionMessages(sessionID: string): Promise<void> {
  const directory = sessionDirectory(sessionID);
  const isChildSession = Boolean(workspace.sessions.find((s) => s.id === sessionID)?.parentID);
  if (!isChildSession) {
    scheduleRefreshWorktreeStatus(sessionID, 120);
  }
  const shouldMarkLoading = workspace.messagesBySession[sessionID] === undefined;
  if (shouldMarkLoading) {
    setWorkspace("loadingMessagesBySession", sessionID, true);
  }
  try {
    const turns = await hoxiAPI.getMessages(sessionID, directory);
    setWorkspace(
      produce((state) => {
        mergeServerTurnsInto((state.messagesBySession[sessionID] ||= []), turns);
      })
    );
  } catch (err) {
    if (workspace.messagesBySession[sessionID] === undefined) {
      setWorkspace("messagesBySession", sessionID, []);
    }
    throw err;
  } finally {
    if (shouldMarkLoading) {
      setWorkspace("loadingMessagesBySession", sessionID, false);
    }
  }
  if (isChildSession && sessionID !== workspace.activeSessionID) {
    return;
  }
  const sessionDiffs = await hoxiAPI.getSessionDiff(sessionID, directory).catch(() => undefined);
  if (sessionDiffs && sessionDirectory(sessionID) === directory && !isSessionDeleted(sessionID)) {
    setWorkspace("diffsBySession", sessionID, sessionDiffs);
  }
}

export async function selectSession(sessionID: string): Promise<void> {
  if (workspace.activeSessionID !== sessionID) {
    setWorkspace({
      inspectedFilePath: null,
      inspectedFileLine: null,
      inspectedFileDiff: null,
      inspectedDiffFile: null,
      activeInspectorFolder: null,
    });
  }
  if (workspace.messagesBySession[sessionID] === undefined) {
    setWorkspace("loadingMessagesBySession", sessionID, true);
  }
  setWorkspace("activeSessionID", sessionID);
  setWorkspace("unreadCompletedBySession", sessionID, false);
  try {
    await loadSessionMessages(sessionID);
  } catch (err) {
    console.error("load session messages failed:", err);
  } finally {
    setWorkspace("loadingMessagesBySession", sessionID, false);
  }
}

export async function applySessionWorktree(
  sessionID: string,
  mode: "patch" | "merge" = "patch"
): Promise<void> {
  await hoxiAPI.applyWorktree(sessionID, mode);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

export async function applyAndExitSessionWorktree(
  sessionID: string,
  mode: "patch" | "merge" = "patch",
  keepBranch = false
): Promise<void> {
  await hoxiAPI.applyWorktree(sessionID, mode);
  await hoxiAPI.removeWorktree(sessionID, true, keepBranch);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

export async function exitSessionWorktree(sessionID: string, keepBranch = false): Promise<void> {
  await hoxiAPI.removeWorktree(sessionID, true, keepBranch);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

export async function renameSessionBranch(sessionID: string, newBranch: string): Promise<string> {
  const res = await hoxiAPI.renameBranch(sessionID, newBranch, workspace.rootDir || undefined);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
  return res.branch;
}

export async function checkoutLocalBranch(branch: string, create = false): Promise<void> {
  await hoxiAPI.checkoutBranch(branch, create, workspace.rootDir || undefined);
  await refreshWorktreeStatus("__root__");
  if (workspace.activeSessionID) {
    await refreshWorktreeStatus(workspace.activeSessionID);
  }
}

const worktreeRefreshTimers = new Map<string, ReturnType<typeof setTimeout>>();

export function scheduleRefreshWorktreeStatus(
  sessionID?: string | null,
  delayMs = 900
): void {
  const key = sessionID || "__root__";
  const existing = worktreeRefreshTimers.get(key);
  if (existing) clearTimeout(existing);
  const timer = setTimeout(() => {
    worktreeRefreshTimers.delete(key);
    void refreshWorktreeStatus(key);
  }, delayMs);
  worktreeRefreshTimers.set(key, timer);
}

export async function refreshWorktreeStatus(sessionID?: string | null): Promise<void> {
  const key = sessionID || "__root__";
  const dirArg = key === "__root__" ? workspace.rootDir || undefined : sessionDirectory(key);
  const isCurrentScope = () => !isSessionDeleted(key) && dirArg ===
    (key === "__root__" ? workspace.rootDir || undefined : sessionDirectory(key));
  try {
    const st = await hoxiAPI.getWorktreeStatus(key, dirArg);
    if (!isCurrentScope()) return;
    const prevMeta = workspace.worktreeBySession[key];
    setWorkspace("worktreeBySession", key, {
      sessionID: key,
      isolated: st.isolated,
      directory: st.directory || dirArg || "",
      branch: st.branch || prevMeta?.branch,
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
      hasUpstream: st.hasUpstream,
    });
    if (key === "__root__" || key === workspace.activeSessionID) {
      // Git status describes the whole directory, not the files owned by a session.
      const diffs = key === "__root__"
        ? st.dirty ? await hoxiAPI.getWorktreeDiff(key, dirArg) : []
        : await hoxiAPI.getSessionDiff(key, dirArg);
      if (isCurrentScope()) setWorkspace("diffsBySession", key, diffs);
    }
  } catch (err) {
    console.error("refresh worktree status failed:", err);
  }
}

export async function renameSessionTitle(sessionID: string, title: string): Promise<void> {
  const cleaned = title.trim();
  if (!cleaned) return;
  recordCustomSessionTitle(sessionID, cleaned);
  // Optimistically update local session title
  setWorkspace(
    "sessions",
    (s) => s.id === sessionID,
    "title",
    cleaned
  );
  try {
    const updated = await hoxiAPI.renameSession(sessionID, cleaned, sessionDirectory(sessionID));
    if (updated?.id) {
      replaceSession({ ...updated, title: cleaned });
    }
  } catch (err) {
    console.error("rename session failed:", err);
  }
}

export async function archiveSession(sessionID: string, directory?: string): Promise<void> {
  const existing = workspace.sessions.find((s) => s.id === sessionID);
  const targetDir =
    directory || existing?.directory || sessionDirectory(sessionID) || workspace.rootDir || undefined;

  if (!workspace.archivedSessionIDs.includes(sessionID)) {
    const nextArchived = [...workspace.archivedSessionIDs, sessionID];
    setWorkspace("archivedSessionIDs", nextArchived);
    persistArchivedSessionIDs(nextArchived);
  }

  const now = Date.now();
  setWorkspace("sessions", (prev) =>
    prev.map((s) =>
      s.id === sessionID ? { ...s, time: { ...s.time, archived: now } } : s
    )
  );

  if (workspace.activeSessionID === sessionID) {
    const next = nextActiveSessionCandidate(sessionID);
    if (next) {
      await selectSession(next.id);
    } else {
      setWorkspace("activeSessionID", null);
    }
  }

  try {
    await hoxiAPI.archiveSession(sessionID, true, targetDir);
  } catch (err) {
    console.warn("archive session backend sync warning:", err);
  }
}

export async function unarchiveSession(sessionID: string, directory?: string): Promise<void> {
  const existing = workspace.sessions.find((s) => s.id === sessionID);
  const targetDir =
    directory || existing?.directory || sessionDirectory(sessionID) || workspace.rootDir || undefined;

  if (workspace.archivedSessionIDs.includes(sessionID)) {
    const nextArchived = workspace.archivedSessionIDs.filter((id) => id !== sessionID);
    setWorkspace("archivedSessionIDs", nextArchived);
    persistArchivedSessionIDs(nextArchived);
  }

  setWorkspace("sessions", (prev) =>
    prev.map((s) => {
      if (s.id !== sessionID) return s;
      const nextTime = { ...s.time };
      delete nextTime.archived;
      return { ...s, time: nextTime };
    })
  );

  try {
    await hoxiAPI.archiveSession(sessionID, false, targetDir);
  } catch (err) {
    console.warn("unarchive session backend sync warning:", err);
  }
}

export function removeSessionFromStore(sessionID: string): SessionInfo | undefined {
  const childIDs = collectChildSubagentSessionIDs(sessionID);
  const allIDs = [sessionID, ...childIDs];
  for (const id of allIDs) deletedSessionIDs.add(id);
  const nextCandidate = nextActiveSessionCandidate(sessionID);
  if (workspace.archivedSessionIDs.some((id) => allIDs.includes(id))) {
    const nextArchived = workspace.archivedSessionIDs.filter((id) => !allIDs.includes(id));
    setWorkspace("archivedSessionIDs", nextArchived);
    persistArchivedSessionIDs(nextArchived);
  }
  setWorkspace(produce((state) => purgeSessionsFromStoreState(state, allIDs)));
  return nextCandidate;
}

export async function discardSession(
  sessionID: string,
  force = true,
  directory?: string
): Promise<void> {
  const existing = workspace.sessions.find((s) => s.id === sessionID);
  const targetDir =
    directory || existing?.directory || sessionDirectory(sessionID) || workspace.rootDir || undefined;
  const childIDs = collectChildSubagentSessionIDs(sessionID);
  const wasActive =
    workspace.activeSessionID === sessionID ||
    (Boolean(workspace.activeSessionID) && childIDs.includes(workspace.activeSessionID!));
  const wasBusy = isSessionBusy(sessionID);

  // Optimistically remove from UI and switch active session immediately
  const nextCandidate = removeSessionFromStore(sessionID);
  if (wasActive) {
    if (nextCandidate) {
      void selectSession(nextCandidate.id);
    } else {
      setWorkspace("activeSessionID", null);
    }
  }

  if (childIDs.length > 0) {
    void deleteChildSubagentSessionsBackend(childIDs, targetDir);
  }

  if (wasBusy) {
    try {
      await hoxiAPI.abortSession(sessionID, targetDir);
    } catch {
      /* ignore abort error */
    }
  }

  try {
    await hoxiAPI.removeWorktree(sessionID, force);
  } catch (err) {
    console.warn("remove worktree warning:", err);
  }

  try {
    await hoxiAPI.deleteSession(sessionID, targetDir);
  } catch (err) {
    if (workspace.rootDir && targetDir !== workspace.rootDir) {
      try {
        await hoxiAPI.deleteSession(sessionID, workspace.rootDir);
        return;
      } catch {
        /* ignore fallback error */
      }
    }
    console.error("delete session failed:", err);
  }
}

export * from "./workspace-session-messages";
export * from "./git-commit";
export * from "./session-sandbox";
