import { produce } from "solid-js/store";
import type { SessionInfo } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import { registerRunnerSessions } from "../services/runner-api";
import {
  evictCachedSessionTurns,
  forgetSubagentLinks,
  mergeServerTurnsInto,
  persistCachedSessionTurns,
  readCachedSessionTurns,
} from "./message-lookup";
import { persistSessionAI, restoreSessionAI } from "./session-ai";
import { syncPendingPermissions } from "./approval-mode";
import { ARCHIVED_SESSIONS_KEY, setWorkspace, workspace } from "./workspace-state";
import {
  forgetSessionTitles,
  hasManualSessionTitle,
  preserveSessionTitle,
  recordCustomSessionTitle,
  recordManualSessionTitle,
  sortSessionsByActivity,
} from "./session-title";
import {
  beginSessionTranscriptRead,
  hydratedSessionIDs,
  isReadingSessionTranscript,
  touchSessionTranscript,
} from "./session-transcripts";
import {
  isManagedSession,
  isSessionArchived,
  isSessionBusy,
  isSessionDeleted,
  markSessionDeleted,
  sessionDirectory,
} from "./session-query";
import {
  cancelScheduledWorktreeStatus,
  refreshSessionDiff,
  refreshWorktreeStatus,
  scheduleRefreshSessionDiff,
  scheduleRefreshWorktreeStatus,
} from "./session-diffs";
import {
  collectChildSubagentSessionIDs,
  deleteChildSubagentSessionsBackend,
  extractChildSubagentSessionId,
} from "./subagent-sessions";
import { forgetPromptQueueState } from "./prompt-queue";
import { indexSession, indexSessions, sessionBelongsToProject } from "./session-index";
import { normalizeFolderPath } from "./workspace-folders";

const sessionMessageLoads = new Map<string, Promise<void>>();

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

export function syncSessionFieldsInPlace(target: SessionInfo, source: SessionInfo): void {
  if (target.title !== source.title) target.title = source.title;
  if (target.slug !== source.slug) target.slug = source.slug;
  if (target.projectID !== source.projectID) target.projectID = source.projectID;
  if (target.directory !== source.directory) target.directory = source.directory;
  if (target.parentID !== source.parentID) target.parentID = source.parentID;
  if (target.runtime !== source.runtime) target.runtime = source.runtime;
  if (target.managed !== source.managed) target.managed = source.managed;
  if (target.modelID !== source.modelID) target.modelID = source.modelID;
  if (target.providerID !== source.providerID) target.providerID = source.providerID;
  if (target.sandboxMode !== source.sandboxMode) target.sandboxMode = source.sandboxMode;
  if (target.branch !== source.branch) target.branch = source.branch;
  target.capabilities = source.capabilities;
  target.revert = source.revert;
  if (source.time) {
    if (!target.time) {
      target.time = { ...source.time };
    } else {
      if (target.time.created !== source.time.created) target.time.created = source.time.created;
      if (target.time.updated !== source.time.updated) target.time.updated = source.time.updated;
      if (target.time.archived !== source.time.archived) {
        if (source.time.archived === undefined) delete target.time.archived;
        else target.time.archived = source.time.archived;
      }
    }
  }
}

export function reconcileSessionsInPlace(incomingList: SessionInfo[]): void {
  indexSessions(incomingList);
  setWorkspace(
    produce((state) => {
      const existingById = new Map(state.sessions.map((s) => [s.id, s]));
      const incomingIds = new Set(incomingList.map((s) => s.id));
      const root = normalizeFolderPath(state.rootDir);
      const next: SessionInfo[] = [];
      for (const s of state.sessions) {
        if (incomingIds.has(s.id) || isSessionDeleted(s.id)) continue;
        const sDir = normalizeFolderPath(
          state.worktreeBySession[s.id]?.directory || s.directory
        );
        const ownsDir =
          !sDir ||
          sDir === root ||
          sDir.startsWith(`${root}/.hoxi/worktrees/`);
        if (ownsDir && (s.id === state.activeSessionID || isSessionBusy(s.id))) {
          next.push(s);
        }
      }
      let orderOrLengthChanged = state.sessions.length !== incomingList.length + next.length;
      for (let i = 0; i < incomingList.length; i++) {
        const incoming = incomingList[i];
        const existing = existingById.get(incoming.id);
        const targetIdx = next.length;
        if (existing) {
          syncSessionFieldsInPlace(existing, incoming);
          next.push(existing);
          if (state.sessions[targetIdx]?.id !== incoming.id) orderOrLengthChanged = true;
        } else {
          next.push(incoming);
          orderOrLengthChanged = true;
        }
      }
      if (orderOrLengthChanged) {
        state.sessions = next;
      }
    })
  );
}

export function replaceSession(info: SessionInfo) {
  if (isSessionDeleted(info.id)) return;
  registerRunnerSessions([info]);
  indexSession(info);
  if (info.sandboxMode && info.directory) {
    setWorkspace("worktreeBySession", info.id, {
      sessionID: info.id, isolated: info.sandboxMode === "worktree", directory: info.directory,
      branch: info.branch, createdAt: new Date(info.time.created).toISOString(),
    });
  }
  // Responses to background renames, sandbox changes and child loads can arrive
  // after a workspace switch. Keep global metadata without adding a foreign row.
  if (!workspace.sessions.some((s) => s.id === info.id) &&
    !sessionBelongsToProject(info, workspace.rootDir, workspace.activeProjectID)) return;
  const effectiveInfo = preserveSessionTitle(info);
  setWorkspace(
    produce((state) => {
      const idx = state.sessions.findIndex((s) => s.id === effectiveInfo.id);
      if (idx < 0) {
        state.sessions.unshift(effectiveInfo);
      } else {
        syncSessionFieldsInPlace(state.sessions[idx], effectiveInfo);
      }
    })
  );
}

export function replaceSessionFromEvent(info: SessionInfo, eventDirectory?: string): void {
  const normalize = (path?: string) => (path || "").trim().replace(/[\\/]+$/, "");
  const root = normalize(workspace.rootDir);
  if (!root) return;
  const worktrees = `${root}/.hoxi/worktrees/`;
  const ownsDirectory = (directory: string) => directory === root ||
    (directory.startsWith(worktrees) && /^[^/\\]+$/.test(directory.slice(worktrees.length)));
  const visited = new Set<string>();
  const activeProjectID = (workspace.activeProjectID || "").trim();
  const belongs = (session: SessionInfo, fallbackDirectory?: string): boolean => {
    if (visited.has(session.id)) return false;
    visited.add(session.id);
    const eventProjectID = (session.projectID || "").trim();
    if (eventProjectID && activeProjectID) {
      if (eventProjectID === activeProjectID) return true;
      // A foreign project ID is only accepted for a session already loaded
      // under that same project; an unattributed loaded session must not be
      // pulled into another project by an event.
      const loaded = workspace.sessions.find((s) => s.id === session.id);
      return Boolean(loaded?.projectID && loaded.projectID === eventProjectID);
    }
    const directory = normalize(session.directory) || normalize(fallbackDirectory);
    // Only Hoxi worktrees belong to the root, not arbitrary nested project folders.
    if (ownsDirectory(directory)) return true;
    const existing = workspace.sessions.find((s) => s.id === session.id);
    if (existing && ownsDirectory(normalize(existing.directory)) &&
      directory === normalize(workspace.worktreeBySession[session.id]?.directory)) return true;
    const parentID = session.parentID || existing?.parentID;
    const parent = workspace.sessions.find((s) => s.id === parentID);
    if (parent && (!directory || directory === normalize(sessionDirectory(parent.id)))) {
      if (belongs(parent)) return true;
    }
    // Directory-less updates to an already loaded session must still update its title/time.
    if (!directory && existing) {
      return !existing.directory || ownsDirectory(normalize(existing.directory));
    }
    return false;
  };
  // activeProjectID is owned by syncActiveProjectID; SSE events never write it.
  if (belongs(info, eventDirectory)) {
    const directory = info.directory || workspace.sessions.find((s) => s.id === info.id)?.directory ||
      eventDirectory;
    replaceSession(directory ? { ...info, directory } : info);
  }
}

// Sessions from other projects still need to be known by directory so status,
// approvals and prompts never get retargeted at the active project.
export function indexSessionFromEvent(info: SessionInfo, eventDirectory?: string): void {
  if (!info?.id) return;
  const directory = info.directory || eventDirectory;
  indexSession(directory ? { ...info, directory } : info);
}

// Idle turns an empty snapshot proves stale (cache seeds); busy sessions may still be persisting.
function idleTurnIDs(sessionID: string): Set<string> | undefined {
  return isSessionBusy(sessionID) ? undefined : new Set((workspace.messagesBySession[sessionID] ?? []).map((t) => t.info.id));
}

const loadRequestRevisions = new Map<string, number>();

export async function loadSessionMessagesOnly(sessionID: string, explicitDirectory?: string): Promise<void> {
  const finishRead = beginSessionTranscriptRead(sessionID);
  try {
    const directory = explicitDirectory || sessionDirectory(sessionID);
    const revision = (loadRequestRevisions.get(sessionID) ?? 0) + 1;
    loadRequestRevisions.set(sessionID, revision);
    const isCurrent = () => loadRequestRevisions.get(sessionID) === revision &&
      sessionDirectory(sessionID) === directory && !isSessionDeleted(sessionID);
    const preRequestIDs = idleTurnIDs(sessionID);
    const turns = await hoxiAPI.getMessages(sessionID, directory);
    if (!isCurrent()) return;
    restoreSessionAI(sessionID, turns);
    setWorkspace(produce((state) => {
      mergeServerTurnsInto((state.messagesBySession[sessionID] ||= []), turns, preRequestIDs);
    }));
    hydratedSessionIDs.add(sessionID);
    persistCachedSessionTurns(sessionID, workspace.messagesBySession[sessionID] ?? turns);
  } finally { finishRead(); }
}

export async function loadSessionMessages(sessionID: string): Promise<void> {
  const finishRead = beginSessionTranscriptRead(sessionID);
  const directory = sessionDirectory(sessionID);
  const revision = (loadRequestRevisions.get(sessionID) ?? 0) + 1;
  loadRequestRevisions.set(sessionID, revision);
  const isCurrent = () => loadRequestRevisions.get(sessionID) === revision &&
    sessionDirectory(sessionID) === directory && !isSessionDeleted(sessionID);
  const parentID = workspace.sessions.find((s) => s.id === sessionID)?.parentID;
  const isChildSession = Boolean(parentID);
  if (!isChildSession) scheduleRefreshWorktreeStatus(sessionID, 120);
  if (workspace.messagesBySession[sessionID] === undefined) {
    setWorkspace("loadingMessagesBySession", sessionID, true);
  }
  try {
    const preRequestIDs = idleTurnIDs(sessionID);
    const turns = await hoxiAPI.getMessages(sessionID, directory);
    if (!isCurrent()) return;
    restoreSessionAI(sessionID, turns);
    setWorkspace(produce((state) => {
      mergeServerTurnsInto((state.messagesBySession[sessionID] ||= []), turns, preRequestIDs);
    }));
    hydratedSessionIDs.add(sessionID);
    persistCachedSessionTurns(sessionID, workspace.messagesBySession[sessionID] ?? turns);
    if (!isChildSession) {
      for (const t of workspace.messagesBySession[sessionID] ?? []) {
        for (const p of t.parts) {
          if (p.type === "tool") {
            const lower = (p.tool || "").toLowerCase();
            if (lower === "task" || lower === "agent" || lower.includes("subagent")) {
              ensureChildSubagentSessionLoaded(
                extractChildSubagentSessionId(p),
                sessionID,
                p.state.status === "completed" || p.state.status === "error"
              );
            }
          }
        }
      }
    }
  } catch (err) {
    if (workspace.messagesBySession[sessionID] === undefined) {
      setWorkspace("messagesBySession", sessionID, []);
    }
    throw err;
  } finally {
    finishRead();
    setWorkspace("loadingMessagesBySession", sessionID, false);
  }
  if (isChildSession && sessionID !== workspace.activeSessionID) {
    if (parentID && !isSessionDeleted(parentID)) scheduleRefreshSessionDiff(parentID);
    return;
  }
  if (!isCurrent()) return;
  await refreshSessionDiff(sessionID);
}

export async function selectSession(sessionID: string): Promise<void> {
  touchSessionTranscript(sessionID);
  if (workspace.activeSessionID !== sessionID) {
    setWorkspace({
      inspectedFilePath: null,
      inspectedFileLine: null,
      inspectedFileDiff: null,
      inspectedDiffFile: null,
      activeInspectorFolder: null,
    });
  }
  const hadLiveMemory = workspace.messagesBySession[sessionID] !== undefined;
  if (!hadLiveMemory) {
    const snap = readCachedSessionTurns(sessionID);
    if (snap && snap.length > 0) {
      setWorkspace("messagesBySession", sessionID, snap);
    } else {
      setWorkspace("loadingMessagesBySession", sessionID, true);
    }
  }
  setWorkspace("activeSessionID", sessionID);
  setWorkspace("unreadCompletedBySession", sessionID, false);
  void syncPendingPermissions(sessionDirectory(sessionID));
  // Background SSE events create a partial transcript for sessions that were
  // never loaded, so only a fully hydrated one may skip the history fetch.
  if (hadLiveMemory && hydratedSessionIDs.has(sessionID) && (workspace.aiBySession[sessionID] || !isSessionBusy(sessionID))) {
    const isChildSession = Boolean(workspace.sessions.find((s) => s.id === sessionID)?.parentID);
    if (!isChildSession) void refreshSessionDiff(sessionID);
    return;
  }
  try {
    let loading = sessionMessageLoads.get(sessionID);
    if (!loading) {
      loading = loadSessionMessages(sessionID);
      sessionMessageLoads.set(sessionID, loading);
      const clearLoad = () => {
        if (sessionMessageLoads.get(sessionID) === loading) sessionMessageLoads.delete(sessionID);
      };
      void loading.then(clearLoad, clearLoad);
    }
    await loading;
  } catch (err) {
    console.error("load session messages failed:", err);
  } finally {
    setWorkspace("loadingMessagesBySession", sessionID, false);
  }
}

export async function exitSessionWorktree(sessionID: string, keepBranch = false): Promise<void> {
  await leaveSessionWorktree(sessionID, keepBranch);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

async function leaveSessionWorktree(sessionID: string, keepBranch: boolean): Promise<void> {
  if (isManagedSession(sessionID)) {
    replaceSession(
      await hoxiAPI.setSandboxMode(
        sessionID,
        "local",
        { force: true, keepBranch },
        workspace.rootDir || undefined
      )
    );
  } else {
    await hoxiAPI.removeWorktree(sessionID, true, keepBranch);
  }
}

export async function renameSessionTitle(
  sessionID: string, title: string, source: "manual" | "automatic" = "manual"
): Promise<void> {
  const cleaned = title.trim();
  if (!cleaned || (source === "automatic" && hasManualSessionTitle(sessionID))) return;
  if (source === "manual") recordManualSessionTitle(sessionID, cleaned);
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
  setWorkspace(
    produce((state) => {
      const target = state.sessions.find((s) => s.id === sessionID);
      if (target) {
        if (!target.time) target.time = { created: now, updated: now, archived: now };
        else target.time.archived = now;
      }
    })
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

  setWorkspace(
    produce((state) => {
      const target = state.sessions.find((s) => s.id === sessionID);
      if (target?.time) {
        delete target.time.archived;
      }
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
  for (const id of allIDs) {
    cancelScheduledWorktreeStatus(id);
    hydratedSessionIDs.delete(id);
    markSessionDeleted(id);
  }
  forgetSessionTitles(allIDs);
  evictCachedSessionTurns(allIDs);
  const nextCandidate = nextActiveSessionCandidate(sessionID);
  if (workspace.archivedSessionIDs.some((id) => allIDs.includes(id))) {
    const nextArchived = workspace.archivedSessionIDs.filter((id) => !allIDs.includes(id));
    setWorkspace("archivedSessionIDs", nextArchived);
    persistArchivedSessionIDs(nextArchived);
  }
  setWorkspace(produce((state) => purgeSessionsFromStoreState(state, allIDs)));
  persistSessionAI();
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

// Queue-overflow recovery includes busy transcripts: waiting for the next idle
// event would leave missing text/parts on screen for the rest of a long run.
export async function resyncResidentTranscripts(): Promise<void> {
  const ids = new Set(Object.keys(workspace.messagesBySession));
  if (workspace.activeSessionID) ids.add(workspace.activeSessionID);
  const queue = [...ids];
  let next = 0;
  await Promise.all(Array.from({ length: Math.min(4, queue.length) }, async () => {
    while (next < queue.length) {
      const id = queue[next++];
      await loadSessionMessagesOnly(id).catch(() => {});
    }
  }));
}

const loadedChildSessionIDs = new Set<string>();
const settledChildSessionIDs = new Set<string>();

export function ensureChildSubagentSessionLoaded(
  childSessionID?: string,
  parentSessionID?: string,
  settled?: boolean
) {
  if (!childSessionID || typeof window === "undefined") return;
  const existing = workspace.sessions.find((s) => s.id === childSessionID);
  const directory =
    sessionDirectory(parentSessionID ?? null) ||
    sessionDirectory(childSessionID) ||
    workspace.rootDir ||
    undefined;

  if (!existing) {
    void hoxiAPI
      .getSession(childSessionID, directory)
      .then((info) => {
        if (info?.id) {
          replaceSession(
            parentSessionID && !info.parentID ? { ...info, parentID: parentSessionID } : info
          );
        }
      })
      .catch(() => {});
  }

  const needsInitialLoad = !loadedChildSessionIDs.has(childSessionID) ||
    (workspace.messagesBySession[childSessionID] === undefined && !isReadingSessionTranscript(childSessionID));
  const needsSettledLoad = Boolean(settled && !settledChildSessionIDs.has(childSessionID));
  if (needsInitialLoad || needsSettledLoad) {
    loadedChildSessionIDs.add(childSessionID);
    if (settled) settledChildSessionIDs.add(childSessionID);
    void loadSessionMessagesOnly(childSessionID, directory)
      .then(() => {
        if (parentSessionID && !isSessionDeleted(parentSessionID)) {
          scheduleRefreshSessionDiff(parentSessionID);
          scheduleRefreshWorktreeStatus(parentSessionID, 150);
        }
      })
      .catch(() => {});
  }
}

export function purgeSessionsFromStoreState(
  state: typeof workspace,
  allIDs: string[]
) {
  const idSet = new Set(allIDs);
  forgetSubagentLinks(allIDs);
  forgetPromptQueueState(allIDs);
  for (const id of allIDs) {
    loadedChildSessionIDs.delete(id);
    settledChildSessionIDs.delete(id);
    delete state.messagesBySession[id];
    delete state.aiBySession[id];
    delete state.loadingMessagesBySession[id];
    delete state.statusBySession[id];
    delete state.sessionFinishedAt[id];
    delete state.unreadCompletedBySession[id];
    delete state.diffsBySession[id];
    delete state.worktreeDiffsBySession[id];
    delete state.worktreeBySession[id];
    delete state.reviewCommentsBySession[id];
    delete state.auditsBySession[id];
    delete state.auditingBySession[id];
    delete state.interruptedBySession[id];
    delete state.queuedPromptsBySession[id];
    delete state.composerDraftEditBySession[id];
    delete state.sessionIndex[id];
  }
  state.sessions = state.sessions.filter(
    (s) => !idSet.has(s.id) && (!s.parentID || !idSet.has(s.parentID))
  );
  state.pendingPermissions = state.pendingPermissions.filter(
    (p) => !idSet.has(p.sessionID)
  );
  state.pendingQuestions = state.pendingQuestions.filter(
    (q) => !idSet.has(q.sessionID)
  );
}

/** Asks the runtime to compact (summarize) a session's context, then reloads its transcript. */
export async function compactSessionContext(
  sessionID: string,
  model: { providerID: string; modelID: string }
): Promise<boolean> {
  const compacted = await hoxiAPI.summarizeSession(
    sessionID,
    model.providerID,
    model.modelID,
    sessionDirectory(sessionID)
  );
  if (compacted) await loadSessionMessages(sessionID);
  return Boolean(compacted);
}
