import { batch } from "solid-js";
import { produce } from "solid-js/store";
import type { SessionInfo } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import { forgetRunnerSession, registerRunnerSessions } from "../services/runner-api";
import { forgetSessionMarkdownImages } from "./markdown-images";
import {
  forgetSessionToolOutputs,
  forgetSubagentLinks,
  mergeServerTurnsInto,
} from "./message-lookup";
import { forgetPendingSessionAI, restoreSessionAI } from "./session-ai";
import { clearSessionGoal } from "./session-goal-state";
import { forgetSessionTaskHistory } from "./task-history";
import {
  forgetSessionHistory,
  noteSessionHistoryTail,
  SESSION_HISTORY_PAGE_TURNS,
} from "./session-history";
import { syncPendingPermissions } from "./approval-mode";
import { syncPendingQuestions } from "./question-answers";
import { setWorkspace, workspace } from "./workspace-state";
import {
  preserveSessionTitle,
  sortSessionsByActivity,
} from "./session-title";
import {
  beginSessionTranscriptRead,
  hydratedSessionIDs,
  isReadingSessionTranscript,
  persistCachedSessionTurns,
  readCachedSessionTurns,
  touchSessionTranscript,
} from "./session-transcripts";
import {
  isSessionArchived,
  isSessionBusy,
  isSessionDeleted,
  isSessionTaskActive,
  requestTimelineFollow,
  sessionDirectory,
} from "./session-query";
import {
  cancelScheduledSessionDiff,
  cancelScheduledWorktreeStatus,
  forgetSessionDiffState,
  refreshSessionDiff,
  scheduleRefreshSessionDiff,
  scheduleRefreshWorktreeStatus,
} from "./session-diffs";
import {
  clearSubagentDrawerOverride,
  extractChildSubagentSessionId,
  resolveParentSessionID,
} from "./subagent-sessions";
import { clearAutoFailoverPending, clearTurnDispatchState } from "./auto-failover";
import { clearSessionAutoTarget } from "./auto-models";
import { forgetPromptQueueState } from "./prompt-queue";
import { indexSession, indexSessions, sessionBelongsToProject } from "./session-index";
import { bumpSessionSurfaceRevision, normalizeFolderPath } from "./workspace-folders";

const sessionMessageLoads = new Map<string, Promise<void>>();
let localSessionCreationDepth = 0;
const deferredCreatedEvents = new Map<string, { info: SessionInfo; eventDirectory?: string }>();

export function beginLocalSessionCreation(): void {
  localSessionCreationDepth++;
}

export function endLocalSessionCreation(createdSessionID?: string): void {
  if (createdSessionID) {
    deferredCreatedEvents.delete(createdSessionID);
  }
  localSessionCreationDepth = Math.max(0, localSessionCreationDepth - 1);
  if (localSessionCreationDepth === 0 && deferredCreatedEvents.size > 0) {
    const pending = [...deferredCreatedEvents.values()];
    deferredCreatedEvents.clear();
    for (const item of pending) {
      replaceSessionFromEvent(item.info, item.eventDirectory);
    }
  }
}

export function nextActiveSessionCandidate(
  exclude?: string | ReadonlySet<string>
): SessionInfo | undefined {
  const isExcluded =
    typeof exclude === "string"
      ? (id: string) => id === exclude
      : exclude
        ? (id: string) => exclude.has(id)
        : () => false;
  const candidates = workspace.sessions.filter(
    (s) => !isExcluded(s.id) && !resolveParentSessionID(s.id) && !isSessionArchived(s)
  );
  if (candidates.length === 0) return undefined;
  return sortSessionsByActivity(candidates)[0];
}

function shouldSuppressStaleRevert(sessionID: string): boolean {
  if (isSessionBusy(sessionID)) return true;
  const turns = workspace.messagesBySession[sessionID];
  return Boolean(turns && turns.some((t) => t.info.id.startsWith("optimistic_")));
}

export function syncSessionFieldsInPlace(target: SessionInfo, source: SessionInfo): void {
  if (target.title !== source.title) target.title = source.title;
  if (target.slug !== source.slug) target.slug = source.slug;
  if (target.projectID !== source.projectID) target.projectID = source.projectID;
  if (target.surface !== source.surface && source.surface) target.surface = source.surface;
  if (target.directory !== source.directory) target.directory = source.directory;
  if (target.parentID !== source.parentID) target.parentID = source.parentID;
  if (target.runtime !== source.runtime) target.runtime = source.runtime;
  if (target.managed !== source.managed) target.managed = source.managed;
  if (target.modelID !== source.modelID) target.modelID = source.modelID;
  if (target.providerID !== source.providerID) target.providerID = source.providerID;
  if (target.sandboxMode !== source.sandboxMode) target.sandboxMode = source.sandboxMode;
  if (target.branch !== source.branch) target.branch = source.branch;
  target.capabilities = source.capabilities;
  target.revert =
    source.revert && shouldSuppressStaleRevert(target.id) ? undefined : source.revert;
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
          next.push(
            incoming.revert && shouldSuppressStaleRevert(incoming.id)
              ? { ...incoming, revert: undefined }
              : incoming
          );
          orderOrLengthChanged = true;
        }
      }
      if (orderOrLengthChanged) {
        const nextIds = new Set(next.map((s) => s.id));
        for (const prevId of existingById.keys()) {
          if (!nextIds.has(prevId) && prevId !== state.activeSessionID) {
            cancelScheduledWorktreeStatus(prevId);
            cancelScheduledSessionDiff(prevId);
          }
        }
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
  const rawEffective = preserveSessionTitle(info);
  const effectiveInfo =
    rawEffective.revert && shouldSuppressStaleRevert(rawEffective.id)
      ? { ...rawEffective, revert: undefined }
      : rawEffective;
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
  if (
    localSessionCreationDepth > 0 &&
    !info.parentID &&
    !workspace.sessions.some((s) => s.id === info.id)
  ) {
    deferredCreatedEvents.set(info.id, { info, eventDirectory });
    return;
  }
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
const settledSessionLoads = new Map<string, {
  directory: string | undefined;
  tailID: string | undefined;
  completedAt: number | undefined;
  promise: Promise<void>;
}>();

export function cancelPendingSessionMessageLoads(sessionID: string): void {
  if (!sessionID) return;
  settledSessionLoads.delete(sessionID);
  loadRequestRevisions.set(sessionID, (loadRequestRevisions.get(sessionID) ?? 0) + 1);
}

// Subagent transcripts stay whole: parent diff scoping and inline timelines scan them.
function historyLimitFor(sessionID: string, full?: boolean): number | "all" {
  if (full || workspace.sessions.find((s) => s.id === sessionID)?.parentID) return "all";
  return SESSION_HISTORY_PAGE_TURNS;
}

function mergeHistorySnapshot(
  sessionID: string,
  turns: Awaited<ReturnType<typeof hoxiAPI.getMessages>>,
  limit: number | "all",
  preRequestIDs: Set<string> | undefined
): void {
  // Only a server-hydrated list holds real older pages; a cache seed may be stale.
  const keepOlder = limit !== "all" && hydratedSessionIDs.has(sessionID);
  let keptOlder = 0;
  setWorkspace(produce((state) => {
    keptOlder = mergeServerTurnsInto((state.messagesBySession[sessionID] ||= []), turns, preRequestIDs, { keepOlder });
    state.loadingMessagesBySession[sessionID] = false;
  }));
  noteSessionHistoryTail(sessionID, turns.length, limit, keptOlder);
}

export async function loadSessionMessagesOnly(
  sessionID: string,
  explicitDirectory?: string,
  options?: { full?: boolean }
): Promise<void> {
  const finishRead = beginSessionTranscriptRead(sessionID);
  try {
    const directory = explicitDirectory || sessionDirectory(sessionID);
    const revision = (loadRequestRevisions.get(sessionID) ?? 0) + 1;
    loadRequestRevisions.set(sessionID, revision);
    const isCurrent = () => loadRequestRevisions.get(sessionID) === revision &&
      sessionDirectory(sessionID) === directory && !isSessionDeleted(sessionID);
    const preRequestIDs = idleTurnIDs(sessionID);
    const limit = historyLimitFor(sessionID, options?.full);
    const turns = await hoxiAPI.getMessages(sessionID, directory, limit);
    if (!isCurrent()) return;
    restoreSessionAI(sessionID, turns);
    mergeHistorySnapshot(sessionID, turns, limit, preRequestIDs);
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
  const sessionInfo = workspace.sessions.find((s) => s.id === sessionID);
  const parentID = sessionInfo?.parentID;
  const isChildSession = Boolean(parentID);
  const hasOwnWorktree =
    sessionInfo?.sandboxMode === "worktree" ||
    Boolean(workspace.worktreeBySession[sessionID]?.isolated);
  if (!isChildSession || hasOwnWorktree) scheduleRefreshWorktreeStatus(sessionID, 120);
  if (workspace.messagesBySession[sessionID] === undefined) {
    setWorkspace("loadingMessagesBySession", sessionID, true);
  }
  try {
    const preRequestIDs = idleTurnIDs(sessionID);
    const limit = historyLimitFor(sessionID);
    const turns = await hoxiAPI.getMessages(sessionID, directory, limit);
    if (!isCurrent()) return;
    restoreSessionAI(sessionID, turns);
    mergeHistorySnapshot(sessionID, turns, limit, preRequestIDs);
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
    if (!isSessionDeleted(sessionID) && workspace.messagesBySession[sessionID] === undefined) {
      setWorkspace("messagesBySession", sessionID, []);
    }
    throw err;
  } finally {
    finishRead();
    if (!isSessionDeleted(sessionID)) {
      setWorkspace("loadingMessagesBySession", sessionID, false);
    }
  }
  if (isChildSession && !hasOwnWorktree && sessionID !== workspace.activeSessionID) {
    if (parentID && !isSessionDeleted(parentID)) scheduleRefreshSessionDiff(parentID);
    return;
  }
  if (!isCurrent()) return;
  await refreshSessionDiff(sessionID);
}

/** Both terminal SSE events may request the same settled transcript. */
export function refreshSettledSessionMessages(sessionID: string): Promise<void> {
  const directory = sessionDirectory(sessionID);
  const tail = workspace.messagesBySession[sessionID]?.at(-1)?.info;
  const pending = settledSessionLoads.get(sessionID);
  if (pending && pending.directory === directory &&
      pending.tailID === tail?.id && pending.completedAt === tail?.time.completed) {
    return pending.promise;
  }
  const read = {
    directory,
    tailID: tail?.id,
    completedAt: tail?.time.completed,
    promise: loadSessionMessages(sessionID).finally(() => {
      if (settledSessionLoads.get(sessionID) === read) settledSessionLoads.delete(sessionID);
    }),
  };
  settledSessionLoads.set(sessionID, read);
  return read.promise;
}

export async function selectSession(sessionID: string): Promise<void> {
  touchSessionTranscript(sessionID);
  const hadLiveMemory = workspace.messagesBySession[sessionID] !== undefined;
  const snap = !hadLiveMemory ? readCachedSessionTurns(sessionID) : undefined;
  const skipHydration =
    hadLiveMemory &&
    hydratedSessionIDs.has(sessionID) &&
    Boolean(workspace.aiBySession[sessionID] || !isSessionBusy(sessionID));
  batch(() => {
    if (workspace.activeSessionID !== sessionID) {
      setWorkspace({
        inspectedFilePath: null,
        inspectedFileLine: null,
        inspectedFileDiff: null,
        inspectedDiffFile: null,
        activeInspectorFolder: null,
      });
    }
    if (!hadLiveMemory) {
      if (snap && snap.length > 0) {
        setWorkspace("messagesBySession", sessionID, snap);
      } else {
        setWorkspace("loadingMessagesBySession", sessionID, true);
      }
    }
    if (!skipHydration && !hydratedSessionIDs.has(sessionID)) {
      setWorkspace("loadingMessagesBySession", sessionID, true);
    }
    setWorkspace("activeSessionID", sessionID);
    setWorkspace("unreadCompletedBySession", sessionID, false);
  });
  if (isSessionTaskActive(sessionID)) {
    requestTimelineFollow(sessionID);
  }
  const dir = sessionDirectory(sessionID);
  void syncPendingPermissions(dir);
  void syncPendingQuestions(dir);
  // Background SSE events create a partial transcript for sessions that were
  // never loaded, so only a fully hydrated one may skip the history fetch.
  if (skipHydration) {
    const info = workspace.sessions.find((s) => s.id === sessionID);
    const isChildWithoutWorktree =
      Boolean(info?.parentID) &&
      info?.sandboxMode !== "worktree" &&
      !workspace.worktreeBySession[sessionID]?.isolated;
    if (!isChildWithoutWorktree) void refreshSessionDiff(sessionID);
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
    if (!isSessionDeleted(sessionID)) {
      setWorkspace("loadingMessagesBySession", sessionID, false);
    }
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
const inflightChildSessionLookups = new Map<string, string | undefined>();

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
    if (inflightChildSessionLookups.has(childSessionID)) {
      if (parentSessionID) inflightChildSessionLookups.set(childSessionID, parentSessionID);
    } else {
      inflightChildSessionLookups.set(childSessionID, parentSessionID);
      void hoxiAPI
        .getSession(childSessionID, directory)
        .then((info) => {
          const resolvedParentID = inflightChildSessionLookups.get(childSessionID) ?? parentSessionID;
          if (info?.id) {
            replaceSession(
              resolvedParentID ? { ...info, parentID: resolvedParentID } : info
            );
          }
        })
        .catch(() => {})
        .finally(() => {
          inflightChildSessionLookups.delete(childSessionID);
        });
    }
  } else if (parentSessionID && existing.parentID !== parentSessionID) {
    replaceSession({ ...existing, parentID: parentSessionID });
  }

  const needsInitialLoad = !loadedChildSessionIDs.has(childSessionID) ||
    (workspace.messagesBySession[childSessionID] === undefined && !isReadingSessionTranscript(childSessionID));
  const needsSettledLoad = Boolean(settled && !settledChildSessionIDs.has(childSessionID));
  if (needsInitialLoad || needsSettledLoad) {
    loadedChildSessionIDs.add(childSessionID);
    if (settled) settledChildSessionIDs.add(childSessionID);
    void loadSessionMessagesOnly(childSessionID, directory, { full: true })
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
  forgetSessionMarkdownImages(allIDs);
  forgetSubagentLinks(allIDs);
  forgetSessionToolOutputs(allIDs);
  forgetPromptQueueState(allIDs);
  let surfaceRemoved = false;
  for (const id of allIDs) {
    settledSessionLoads.delete(id);
    clearTurnDispatchState(id);
    clearAutoFailoverPending(id);
    clearSessionAutoTarget(id);
    clearSubagentDrawerOverride(id);
    clearSessionGoal(id);
    forgetPendingSessionAI(id);
    forgetRunnerSession(id);
    forgetSessionDiffState(id);
    forgetSessionHistory(id);
    forgetSessionTaskHistory(id);
    hydratedSessionIDs.delete(id);
    loadRequestRevisions.delete(id);
    sessionMessageLoads.delete(id);
    deferredCreatedEvents.delete(id);
    loadedChildSessionIDs.delete(id);
    settledChildSessionIDs.delete(id);
    inflightChildSessionLookups.delete(id);
    delete state.messagesBySession[id];
    delete state.aiBySession[id];
    delete state.loadingMessagesBySession[id];
    delete state.statusBySession[id];
    delete state.stalledSinceBySession[id];
    delete state.sessionFinishedAt[id];
    delete state.unreadCompletedBySession[id];
    delete state.diffsBySession[id];
    delete state.worktreeDiffsBySession[id];
    delete state.worktreeBySession[id];
    delete state.reviewCommentsBySession[id];
    delete state.interruptedBySession[id];
    delete state.queuedPromptsBySession[id];
    delete state.composerDraftEditBySession[id];
    const keyPrefix = `${id}:`;
    for (const k of Object.keys(state.dismissedPlanFollowups)) {
      if (k.startsWith(keyPrefix)) delete state.dismissedPlanFollowups[k];
    }
    for (const k of Object.keys(state.dismissedSandboxMergePrompts)) {
      if (k.startsWith(keyPrefix)) delete state.dismissedSandboxMergePrompts[k];
    }
    if (state.sessionIndex[id]?.surface) surfaceRemoved = true;
    delete state.sessionIndex[id];
  }
  if (surfaceRemoved) bumpSessionSurfaceRevision();
  state.sessions = state.sessions.filter(
    (s) => !idSet.has(s.id) && (!s.parentID || !idSet.has(s.parentID))
  );
  state.pendingPermissions = state.pendingPermissions.filter(
    (p) =>
      !idSet.has(p.sessionID) &&
      !idSet.has(p.subagentSessionID || p.metadata?.subagentSessionID)
  );
  state.pendingQuestions = state.pendingQuestions.filter(
    (q) => !idSet.has(q.sessionID) && (!q.subagentSessionID || !idSet.has(q.subagentSessionID))
  );
}
