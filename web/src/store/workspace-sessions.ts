import { produce } from "solid-js/store";
import type { SessionInfo, SnapshotFileDiff } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import {
  ARCHIVED_SESSIONS_KEY,
  isSessionArchived,
  setSessionSandboxMode,
  setWorkspace,
  workspace,
} from "./workspace";
import {
  getCustomSessionTitle,
  isPlaceholderSessionTitle,
  recordCustomSessionTitle,
} from "./session-title";

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
    (s) => s.id !== excludeID && !isSessionArchived(s)
  );
  if (candidates.length === 0) return undefined;
  return [...candidates].sort((a, b) => b.time.created - a.time.created)[0];
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

function mergeSessionAndWorktreeDiffs(
  sessionDiffs: SnapshotFileDiff[],
  worktreeDiffs: SnapshotFileDiff[]
): SnapshotFileDiff[] {
  const byFile = new Map<string, SnapshotFileDiff>();
  for (const d of sessionDiffs || []) {
    if (!d?.file) continue;
    byFile.set(d.file, { ...d });
  }
  for (const wd of worktreeDiffs || []) {
    if (!wd?.file) continue;
    const existing = byFile.get(wd.file);
    if (existing) {
      byFile.set(wd.file, {
        ...existing,
        patch: existing.patch || wd.patch,
        additions: existing.additions || wd.additions,
        deletions: existing.deletions || wd.deletions,
        status: existing.status || wd.status,
      });
    } else {
      byFile.set(wd.file, { ...wd });
    }
  }
  return Array.from(byFile.values());
}

export async function loadSessionMessages(sessionID: string): Promise<void> {
  const directory = sessionDirectory(sessionID);
  void refreshWorktreeStatus(sessionID);
  const turns = await hoxiAPI.getMessages(sessionID, directory);
  setWorkspace("messagesBySession", (prev) => ({
    ...prev,
    [sessionID]: turns.map((t) => ({ info: t.info, parts: t.parts })),
  }));
  const [sessionDiffs, worktreeDiffs] = await Promise.all([
    hoxiAPI.getSessionDiff(sessionID, directory).catch(() => [] as SnapshotFileDiff[]),
    hoxiAPI.getWorktreeDiff(sessionID).catch(() => [] as SnapshotFileDiff[]),
  ]);
  const merged = mergeSessionAndWorktreeDiffs(sessionDiffs, worktreeDiffs);
  setWorkspace("diffsBySession", (prev) => ({ ...prev, [sessionID]: merged }));
}

export async function selectSession(sessionID: string): Promise<void> {
  setWorkspace("activeSessionID", sessionID);
  try {
    await loadSessionMessages(sessionID);
  } catch (err) {
    console.error("load session messages failed:", err);
  }
}

export async function newIsolatedSession(
  title?: string,
  options?: { isolated?: boolean }
): Promise<string> {
  const session = await hoxiAPI.createSession(
    title,
    workspace.rootDir || undefined
  );
  const shouldIsolate =
    options?.isolated ?? workspace.sessionSandboxMode === "worktree";
  if (shouldIsolate) {
    try {
      const meta = await hoxiAPI.createWorktree(
        session.id,
        workspace.rootDir || undefined
      );
      setWorkspace("worktreeBySession", (prev) => ({
        ...prev,
        [session.id]: meta,
      }));
    } catch (err) {
      console.error("create worktree failed:", err);
    }
  }
  replaceSession(session);
  await selectSession(session.id);
  return session.id;
}

export async function switchActiveSessionSandboxMode(
  mode: "local" | "worktree"
): Promise<void> {
  setSessionSandboxMode(mode);
  const id = workspace.activeSessionID;
  if (!id) return;
  const currentMeta = workspace.worktreeBySession[id];
  const isCurrentlyIsolated = Boolean(currentMeta?.isolated);

  if (mode === "worktree" && !isCurrentlyIsolated) {
    try {
      const meta = await hoxiAPI.createWorktree(
        id,
        workspace.rootDir || undefined
      );
      setWorkspace("worktreeBySession", (prev) => ({ ...prev, [id]: meta }));
      await refreshWorktreeStatus(id);
    } catch (err) {
      console.error("enable session worktree failed:", err);
    }
  } else if (mode === "local" && isCurrentlyIsolated) {
    const turnCount = workspace.messagesBySession[id]?.length ?? 0;
    if (turnCount === 0 || !currentMeta?.dirty) {
      try {
        await hoxiAPI.removeWorktree(id, true);
        await refreshWorktreeStatus(id);
      } catch (err) {
        console.error("remove session worktree failed:", err);
      }
    }
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
  setSessionSandboxMode("local");
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

export async function exitSessionWorktree(
  sessionID: string,
  keepBranch = false
): Promise<void> {
  await hoxiAPI.removeWorktree(sessionID, true, keepBranch);
  setSessionSandboxMode("local");
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

export async function renameSessionBranch(
  sessionID: string,
  newBranch: string
): Promise<string> {
  const res = await hoxiAPI.renameBranch(
    sessionID,
    newBranch,
    workspace.rootDir || undefined
  );
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
  return res.branch;
}

export async function checkoutLocalBranch(
  branch: string,
  create = false
): Promise<void> {
  await hoxiAPI.checkoutBranch(branch, create, workspace.rootDir || undefined);
  await refreshWorktreeStatus("__root__");
  if (workspace.activeSessionID) {
    await refreshWorktreeStatus(workspace.activeSessionID);
  }
}

export async function refreshWorktreeStatus(sessionID?: string | null): Promise<void> {
  const key = sessionID || "__root__";
  const dirArg = key === "__root__" ? workspace.rootDir || undefined : undefined;
  try {
    const st = await hoxiAPI.getWorktreeStatus(key, dirArg);
    setWorkspace("worktreeBySession", (prev) => ({
      ...prev,
      [key]: {
        sessionID: key,
        isolated: st.isolated,
        directory: st.directory || workspace.rootDir,
        branch: st.branch || prev[key]?.branch,
        baseCommit: st.baseCommit || prev[key]?.baseCommit,
        headCommit: st.headCommit || prev[key]?.headCommit,
        headSubject: st.headSubject || prev[key]?.headSubject,
        createdAt: prev[key]?.createdAt || new Date().toISOString(),
        dirty: st.dirty,
        changedFiles: st.changedFiles,
        uncommittedLines: st.uncommittedLines,
        additions: st.additions,
        deletions: st.deletions,
      },
    }));

    if (key === "__root__" || key === workspace.activeSessionID) {
      const wtDiffs = st.dirty
        ? await hoxiAPI.getWorktreeDiff(key, dirArg).catch(() => [] as SnapshotFileDiff[])
        : [];
      setWorkspace("diffsBySession", (prev) => {
        if (key === "__root__") {
          return { ...prev, __root__: wtDiffs };
        }
        const existing = prev[key] ?? [];
        const merged = st.dirty ? mergeSessionAndWorktreeDiffs(existing, wtDiffs) : existing;
        return { ...prev, [key]: merged };
      });
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
      delete state.interruptedBySession[sessionID];
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
  pauseSessionTask,
  resumePausedSession,
  retryLastUserPrompt,
  revertToHere,
  sendPrompt,
  stopSessionTask,
  submitSessionPrompt,
  undoRevert,
  upsertMessageInfo,
  upsertMessagePart,
} from "./workspace-session-messages";
