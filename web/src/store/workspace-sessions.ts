import { produce } from "solid-js/store";
import type { SessionInfo } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import {
  ARCHIVED_SESSIONS_KEY,
  isSessionArchived,
  RECENT_PROJECTS_KEY,
  setWorkspace,
  workspace,
} from "./workspace";
import { loadAgents, loadProviders } from "./workspace-providers";
import {
  getCustomSessionTitle,
  isPlaceholderSessionTitle,
  recordCustomSessionTitle,
} from "./session-title";

const deletedSessionIDs = new Set<string>();

function persistArchivedSessionIDs(ids: string[]) {
  try {
    localStorage.setItem(ARCHIVED_SESSIONS_KEY, JSON.stringify(ids));
  } catch {
    /* ignore */
  }
}

function nextActiveSessionCandidate(excludeID?: string): SessionInfo | undefined {
  const candidates = workspace.sessions.filter(
    (s) => s.id !== excludeID && !isSessionArchived(s)
  );
  if (candidates.length === 0) return undefined;
  return [...candidates].sort((a, b) => b.time.created - a.time.created)[0];
}

export async function loadProjects(): Promise<void> {
  try {
    const remote = await hoxiAPI.listProjects();
    const list = Array.isArray(remote) ? remote : [];
    setWorkspace("projects", list);
  } catch (err) {
    console.error("Failed to load projects:", err);
  }
}

export async function switchWorkspace(directory: string): Promise<void> {
  const cleaned = directory.trim();
  if (!cleaned) return;

  // Persist to recent projects
  const recents = workspace.recentProjects.filter((p) => p !== cleaned);
  const updatedRecents = [cleaned, ...recents].slice(0, 15);
  setWorkspace("recentProjects", updatedRecents);
  try {
    localStorage.setItem(RECENT_PROJECTS_KEY, JSON.stringify(updatedRecents));
  } catch {
    /* ignore */
  }

  setWorkspace({
    rootDir: cleaned,
    activeSessionID: null,
    messagesBySession: {},
    diffsBySession: {},
    worktreeBySession: {},
    reviewCommentsBySession: {},
    pendingPermissions: [],
  });

  try {
    const rawSessions = await hoxiAPI.listSessions(cleaned);
    const sessions = rawSessions.filter((s) => !deletedSessionIDs.has(s.id));
    setWorkspace("sessions", sessions);

    // Restore worktree & git branch/diff status for root and all sessions
    void refreshWorktreeStatus("__root__");
    await Promise.allSettled(sessions.map((s) => refreshWorktreeStatus(s.id)));

    const unarchived = sessions.filter((s) => !isSessionArchived(s));
    if (unarchived.length > 0) {
      const mostRecent = [...unarchived].sort((a, b) => b.time.created - a.time.created)[0];
      await selectSession(mostRecent.id);
    }
  } catch (err) {
    console.error("switch workspace error:", err);
  }

  void loadProviders();
  void loadProjects();
  void loadAgents(cleaned);
}

export async function openWorkspaceFolder(): Promise<void> {
  try {
    const res = await hoxiAPI.openDirectoryDialog();
    if (res?.path && res.path.trim()) {
      await switchWorkspace(res.path.trim());
    }
  } catch (err) {
    console.error("open directory dialog error:", err);
  }
}

export function activeSession(): SessionInfo | undefined {
  return workspace.sessions.find((s) => s.id === workspace.activeSessionID);
}

export function sessionDirectory(sessionID: string | null): string | undefined {
  if (!sessionID) return undefined;
  const meta = workspace.worktreeBySession[sessionID];
  const session = workspace.sessions.find((s) => s.id === sessionID);
  return meta?.directory || session?.directory || workspace.rootDir || undefined;
}

export function activeDirectory(): string | undefined {
  return sessionDirectory(workspace.activeSessionID);
}

export function isSessionBusy(sessionID: string | null): boolean {
  if (!sessionID) return false;
  return workspace.statusBySession[sessionID]?.type === "busy";
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

export async function loadSessionMessages(sessionID: string): Promise<void> {
  const directory = sessionDirectory(sessionID);
  void refreshWorktreeStatus(sessionID);
  const turns = await hoxiAPI.getMessages(sessionID, directory);
  setWorkspace("messagesBySession", (prev) => ({
    ...prev,
    [sessionID]: turns.map((t) => ({ info: t.info, parts: t.parts })),
  }));
  const diffs = await hoxiAPI.getSessionDiff(sessionID, directory);
  setWorkspace("diffsBySession", (prev) => ({ ...prev, [sessionID]: diffs }));
}

export async function selectSession(sessionID: string): Promise<void> {
  setWorkspace("activeSessionID", sessionID);
  try {
    await loadSessionMessages(sessionID);
  } catch (err) {
    console.error("load session messages failed:", err);
  }
}

export async function newIsolatedSession(title?: string): Promise<string> {
  const session = await hoxiAPI.createSession(
    title,
    workspace.rootDir || undefined
  );
  try {
    const meta = await hoxiAPI.createWorktree(session.id);
    setWorkspace("worktreeBySession", (prev) => ({ ...prev, [session.id]: meta }));
  } catch (err) {
    console.error("create worktree failed:", err);
  }
  replaceSession(session);
  await selectSession(session.id);
  return session.id;
}

export async function applySessionWorktree(
  sessionID: string,
  mode: "patch" | "merge" = "patch"
): Promise<void> {
  await hoxiAPI.applyWorktree(sessionID, mode);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

export async function refreshWorktreeStatus(sessionID?: string | null): Promise<void> {
  const key = sessionID || "__root__";
  try {
    const st = await hoxiAPI.getWorktreeStatus(key);
    setWorkspace("worktreeBySession", (prev) => ({
      ...prev,
      [key]: {
        sessionID: key,
        isolated: st.isolated,
        directory: st.directory || workspace.rootDir,
        branch: st.branch || prev[key]?.branch,
        baseCommit: st.baseCommit || prev[key]?.baseCommit,
        createdAt: prev[key]?.createdAt || new Date().toISOString(),
        dirty: st.dirty,
        changedFiles: st.changedFiles,
        uncommittedLines: st.uncommittedLines,
        additions: st.additions,
        deletions: st.deletions,
      },
    }));
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
  deletedSessionIDs.add(sessionID);
  const nextCandidate = nextActiveSessionCandidate(sessionID);
  if (workspace.archivedSessionIDs.includes(sessionID)) {
    const nextArchived = workspace.archivedSessionIDs.filter((id) => id !== sessionID);
    setWorkspace("archivedSessionIDs", nextArchived);
    persistArchivedSessionIDs(nextArchived);
  }
  setWorkspace(
    produce((state) => {
      state.sessions = state.sessions.filter((s) => s.id !== sessionID);
      delete state.messagesBySession[sessionID];
      delete state.statusBySession[sessionID];
      delete state.diffsBySession[sessionID];
      delete state.worktreeBySession[sessionID];
      delete state.reviewCommentsBySession[sessionID];
      delete state.auditsBySession[sessionID];
      delete state.auditingBySession[sessionID];
      state.pendingPermissions = state.pendingPermissions.filter((p) => p.sessionID !== sessionID);
      state.pendingQuestions = state.pendingQuestions.filter((q) => q.sessionID !== sessionID);
    })
  );
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
  const wasActive = workspace.activeSessionID === sessionID;
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

export {
  applyPartDeltaBatch,
  forkFromMessage,
   retryLastUserPrompt,
  revertToHere,
  sendPrompt,
  submitSessionPrompt,
  undoRevert,
  upsertMessageInfo,
  upsertMessagePart,
} from "./workspace-session-messages";
