import { createSignal } from "solid-js";
import { produce, unwrap } from "solid-js/store";
import type { MessagePart, SnapshotFileDiff, SessionInfo } from "../services/sse-types";
import { hoxiAPI, isRunnerSessionID } from "../services/api";
import { registerRunnerSessions } from "../services/runner-api";
import {
  evictCachedSessionTurns,
  filterLocalSessionDiffsByTurns,
  mergeServerTurnsInto,
  persistCachedSessionTurns,
  readCachedSessionTurns,
} from "./message-lookup";
import { recordUserRevertAction } from "./provider-telemetry";
import { initializeSessionAI, persistSessionAI, restoreSessionAI, sessionAIConfig } from "./session-ai";
import { syncPendingPermissions } from "./approval-mode";
import { extractUserTurnSkillSelection } from "./skills";
import { appSettings } from "./settings";
import { ARCHIVED_SESSIONS_KEY, isSessionArchived, setWorkspace, workspace } from "./workspace";
import {
  forgetSessionTitles,
  hasManualSessionTitle,
  preserveSessionTitle,
  recordCustomSessionTitle,
  recordManualSessionTitle,
  sortSessionsByActivity,
} from "./session-title";
import {
  abortChildSubagentSessions,
  collectChildSubagentSessionIDs,
  deleteChildSubagentSessionsBackend,
  ensureChildSubagentSessionLoaded,
  extractChildSubagentSessionId,
  purgeSessionsFromStoreState,
} from "./workspace-queue-subagents";
import { secondaryWorkspaceDirs, setWorktreeMetaIfChanged } from "./session-sandbox";

const deletedSessionIDs = new Set<string>();
const sessionMessageLoads = new Map<string, Promise<void>>();

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

export function isManagedSession(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (session?.managed !== undefined) return Boolean(session.managed);
  if (session?.runtime && session.runtime !== "opencode") return true;
  return isRunnerSessionID(sessionID);
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

export function syncSessionFieldsInPlace(target: SessionInfo, source: SessionInfo): void {
  if (target.title !== source.title) target.title = source.title;
  if (target.slug !== source.slug) target.slug = source.slug;
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
  setWorkspace(
    produce((state) => {
      const existingById = new Map(state.sessions.map((s) => [s.id, s]));
      const next: SessionInfo[] = [];
      let orderOrLengthChanged = state.sessions.length !== incomingList.length;
      for (let i = 0; i < incomingList.length; i++) {
        const incoming = incomingList[i];
        const existing = existingById.get(incoming.id);
        if (existing) {
          syncSessionFieldsInPlace(existing, incoming);
          next.push(existing);
          if (state.sessions[i]?.id !== incoming.id) orderOrLengthChanged = true;
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
  if (deletedSessionIDs.has(info.id)) return;
  registerRunnerSessions([info]);
  if (info.sandboxMode && info.directory) {
    setWorkspace("worktreeBySession", info.id, {
      sessionID: info.id, isolated: info.sandboxMode === "worktree", directory: info.directory,
      branch: info.branch, createdAt: new Date(info.time.created).toISOString(),
    });
  }
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
  const belongs = (session: SessionInfo, fallbackDirectory?: string): boolean => {
    if (visited.has(session.id)) return false;
    visited.add(session.id);
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
  if (belongs(info, eventDirectory)) {
    const directory = info.directory || workspace.sessions.find((s) => s.id === info.id)?.directory ||
      eventDirectory;
    replaceSession(directory ? { ...info, directory } : info);
  }
}

const hydratedSessionIDs = new Set<string>();

// Idle turns an empty snapshot proves stale (cache seeds); busy sessions may still be persisting.
function idleTurnIDs(sessionID: string): Set<string> | undefined {
  return isSessionBusy(sessionID) ? undefined : new Set((workspace.messagesBySession[sessionID] ?? []).map((t) => t.info.id));
}

const loadRequestRevisions = new Map<string, number>();

export async function loadSessionMessagesOnly(sessionID: string, explicitDirectory?: string): Promise<void> {
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
}

export async function loadSessionMessages(sessionID: string): Promise<void> {
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
    setWorkspace("loadingMessagesBySession", sessionID, false);
  }
  if (isChildSession && sessionID !== workspace.activeSessionID) {
    if (parentID && !isSessionDeleted(parentID)) scheduleRefreshSessionDiff(parentID);
    return;
  }
  if (!isCurrent()) return;
  await refreshSessionDiff(sessionID);
}

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
  if (hadLiveMemory && (workspace.aiBySession[sessionID] || (hydratedSessionIDs.has(sessionID) && !isSessionBusy(sessionID)))) {
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

const worktreeRefreshTimers = new Map<string, ReturnType<typeof setTimeout>>();
const worktreeRevisions = new Map<string, number>();

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
    setWorktreeMetaIfChanged(key, {
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
    });
    if (key === "__root__" || key === workspace.activeSessionID) {
      const diffs = st.dirty
        ? (extraDirs.length > 0
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
    deletedSessionIDs.add(id);
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

export interface PendingRevertRequest {
  sessionID: string;
  messageID: string;
  partID?: string;
  affectedFiles: string[];
}

export const [pendingRevertConfirm, setPendingRevertConfirm] =
  createSignal<PendingRevertRequest | null>(null);

const MUTATING_TOOLS = new Set(["edit", "write", "patch", "multiedit", "apply_patch"]);

function collectAffectedRevertFiles(sessionID: string, messageID: string, partID?: string): string[] {
  const turns = workspace.messagesBySession[sessionID] ?? [];
  const bIdx = turns.findIndex((m) => m.info.id === messageID);
  const slice = bIdx >= 0 ? turns.slice(bIdx) : turns.filter((m) => m.info.id >= messageID);
  const files = new Set<string>();
  slice.forEach((turn, idx) => {
    const start = idx === 0 && partID ? Math.max(0, turn.parts.findIndex((p) => p.id === partID)) : 0;
    for (const p of turn.parts.slice(start)) {
      if (p.type === "patch" && Array.isArray(p.files)) {
        for (const f of p.files) if (f) files.add(f);
      } else if (p.type === "tool" && p.state.status === "completed" && MUTATING_TOOLS.has(p.tool.toLowerCase())) {
        const inp = (p.state.input || {}) as Record<string, any>;
        const fp = inp.filePath || inp.path || inp.file || (p.state.metadata as any)?.filediff?.file;
        if (typeof fp === "string" && fp.trim()) files.add(fp.trim());
      }
    }
  });
  return Array.from(files);
}

export async function revertToHere(
  sessionID: string,
  messageID: string,
  partID?: string,
  options?: { skipConfirm?: boolean }
): Promise<void> {
  const existingSession = workspace.sessions.find((s) => s.id === sessionID);
  const affectedFiles = collectAffectedRevertFiles(sessionID, messageID, partID);
  if (
    !options?.skipConfirm &&
    appSettings.confirmBeforeRevert &&
    !existingSession?.revert &&
    (affectedFiles.length > 0 || Boolean(partID))
  ) {
    setPendingRevertConfirm({ sessionID, messageID, partID, affectedFiles });
    return;
  }
  setPendingRevertConfirm(null);
  recordUserRevertAction(sessionID, messageID);

  if (!partID) {
    const targetTurn = (workspace.messagesBySession[sessionID] ?? []).find((m) => m.info.id === messageID);
    if (targetTurn && targetTurn.info.role === "user") {
      const text = targetTurn.parts
        .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
        .map((p) => p.text)
        .join("\n")
        .trim();
      const attachments = targetTurn.parts
        .filter((p): p is Extract<MessagePart, { type: "file" }> => p.type === "file")
        .map((p) => ({ type: "file" as const, mime: p.mime, filename: p.filename, url: p.url }));
      const skillSelection = extractUserTurnSkillSelection(targetTurn.parts) ?? undefined;
      if (text || attachments.length > 0 || skillSelection) {
        setWorkspace("composerDraftEditBySession", sessionID, { id: `revert_${messageID}`, text, attachments, skillSelection });
      }
    }
  }

  if (existingSession) replaceSession({ ...existingSession, revert: { messageID, partID } });
  if (isSessionBusy(sessionID)) {
    setWorkspace("statusBySession", sessionID, { type: "idle" });
    try { await hoxiAPI.abortSession(sessionID, sessionDirectory(sessionID)); } catch { /* ignore */ }
    void abortChildSubagentSessions(sessionID);
  }
  const info = await hoxiAPI.revertSession(sessionID, messageID, partID, sessionDirectory(sessionID));
  replaceSession(info);
  void loadSessionMessages(sessionID);
  scheduleRefreshWorktreeStatus(sessionID, 120);
}

export async function undoRevert(sessionID: string): Promise<void> {
  setPendingRevertConfirm(null);
  const existingSession = workspace.sessions.find((s) => s.id === sessionID);
  if (existingSession) replaceSession({ ...existingSession, revert: undefined });
  const info = await hoxiAPI.unrevertSession(sessionID, sessionDirectory(sessionID));
  replaceSession(info);
  void loadSessionMessages(sessionID);
  scheduleRefreshWorktreeStatus(sessionID, 120);
}

export async function forkFromMessage(sessionID: string, messageID?: string): Promise<string> {
  setPendingRevertConfirm(null);
  const forked = await hoxiAPI.forkSession(sessionID, messageID, sessionDirectory(sessionID));
  initializeSessionAI(forked.id, sessionAIConfig(sessionID));
  replaceSession(forked);
  await selectSession(forked.id);
  return forked.id;
}
