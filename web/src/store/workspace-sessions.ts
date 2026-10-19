import { createSignal } from "solid-js";
import { produce } from "solid-js/store";
import type { MessagePart, SnapshotFileDiff, SessionInfo } from "../services/sse-types";
import { hoxiAPI, isRunnerSessionID, runnerAPI } from "../services/api";
import { registerRunnerSessions } from "../services/runner-api";
import { mergeServerTurnsInto } from "./message-lookup";
import { recordUserRevertAction } from "./provider-telemetry";
import { initializeSessionAI, persistSessionAI, restoreSessionAI, sessionAIConfig } from "./session-ai";
import { appSettings } from "./settings";
import {
  ARCHIVED_SESSIONS_KEY,
  isSessionArchived,
  setWorkspace,
  workspace,
} from "./workspace";
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
  registerRunnerSessions([info]);
  if (info.sandboxMode && info.directory) {
    setWorkspace("worktreeBySession", info.id, {
      sessionID: info.id, isolated: info.sandboxMode === "worktree", directory: info.directory,
      branch: info.branch, createdAt: new Date(info.time.created).toISOString(),
    });
  }
  const effectiveInfo = preserveSessionTitle(info);
  setWorkspace("sessions", (prev) => {
    const idx = prev.findIndex((s) => s.id === effectiveInfo.id);
    if (idx < 0) return [effectiveInfo, ...prev];
    const next = prev.slice();
    next[idx] = effectiveInfo;
    return next;
  });
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

export async function loadSessionMessagesOnly(sessionID: string): Promise<void> {
  const directory = sessionDirectory(sessionID);
  const turns = await hoxiAPI.getMessages(sessionID, directory);
  if (!isSessionDeleted(sessionID)) restoreSessionAI(sessionID, turns);
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
    if (!isSessionDeleted(sessionID)) restoreSessionAI(sessionID, turns);
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
  await refreshSessionDiff(sessionID);
}

const diffRevisions = new Map<string, number>();

export function applySessionDiffEvent(sessionID: string, diffs: SnapshotFileDiff[]): void {
  if (isSessionDeleted(sessionID)) return;
  diffRevisions.set(sessionID, (diffRevisions.get(sessionID) ?? 0) + 1);
  setWorkspace("diffsBySession", sessionID, diffs);
}

export async function refreshSessionDiff(sessionID: string): Promise<void> {
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
      diffs = await hoxiAPI.getWorktreeDiff(sessionID, directory);
    }
    if (isCurrent()) setWorkspace("diffsBySession", sessionID, diffs);
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
  if (workspace.messagesBySession[sessionID] === undefined) {
    setWorkspace("loadingMessagesBySession", sessionID, true);
  }
  setWorkspace("activeSessionID", sessionID);
  setWorkspace("unreadCompletedBySession", sessionID, false);
  const cached = workspace.messagesBySession[sessionID];
  if (cached !== undefined && workspace.aiBySession[sessionID]) {
    const isChildSession = Boolean(workspace.sessions.find((s) => s.id === sessionID)?.parentID);
    if (!isChildSession) void refreshSessionDiff(sessionID);
    return;
  }
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
  await leaveSessionWorktree(sessionID, keepBranch);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

export async function exitSessionWorktree(sessionID: string, keepBranch = false): Promise<void> {
  await leaveSessionWorktree(sessionID, keepBranch);
  await refreshWorktreeStatus(sessionID);
  await refreshWorktreeStatus("__root__");
}

async function leaveSessionWorktree(sessionID: string, keepBranch: boolean): Promise<void> {
  if (isRunnerSessionID(sessionID)) {
    replaceSession(await runnerAPI.setSandboxMode(sessionID, "local", { force: true, keepBranch }));
  } else {
    await hoxiAPI.removeWorktree(sessionID, true, keepBranch);
  }
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
      const diffs = st.dirty ? await hoxiAPI.getWorktreeDiff(key, dirArg) : [];
      if (!isCurrentScope()) return;
      setWorkspace("worktreeDiffsBySession", key, diffs);
      if (key === "__root__") {
        setWorkspace("diffsBySession", key, diffs);
      } else {
        await refreshSessionDiff(key);
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
  for (const id of allIDs) {
    cancelScheduledWorktreeStatus(id);
    deletedSessionIDs.add(id);
  }
  forgetSessionTitles(allIDs);
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
      if (text || attachments.length > 0) {
        setWorkspace("composerDraftEditBySession", sessionID, { id: `revert_${messageID}`, text, attachments });
      }
    }
  }

  if (existingSession) {
    replaceSession({ ...existingSession, revert: { messageID, partID } });
  }

  if (isSessionBusy(sessionID)) {
    setWorkspace("statusBySession", sessionID, { type: "idle" });
    try {
      await hoxiAPI.abortSession(sessionID, sessionDirectory(sessionID));
    } catch { /* ignore */ }
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
