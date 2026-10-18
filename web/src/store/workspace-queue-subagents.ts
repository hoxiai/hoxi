import { createSignal } from "solid-js";
import { produce } from "solid-js/store";
import { t } from "../i18n";
import { hoxiAPI } from "../services/api";
import type { MessagePart, ToolPart } from "../services/sse-types";
import { recordUserRevertAction } from "./provider-telemetry";
import { appSettings } from "./settings";
import { type SelectedModel, setWorkspace, workspace } from "./workspace";
import { initializeSessionAI, sessionAIConfig } from "./session-ai";
import {
  isSessionBusy,
  loadSessionMessages,
  loadSessionMessagesOnly,
  replaceSession,
  scheduleRefreshWorktreeStatus,
  selectSession,
  sessionDirectory,
} from "./workspace-sessions";
import {
  clearSessionInterrupted,
  markSessionInterrupted,
  submitSessionPrompt,
} from "./workspace-session-messages";

export interface QueuedPromptAttachment {
  type: "file";
  mime: string;
  filename?: string;
  url: string;
}

export interface QueuedPromptItem {
  id: string;
  sessionID: string;
  text: string;
  createdAt: number;
  agent?: string;
  model?: SelectedModel;
  effort?: "" | "low" | "medium" | "high";
  attachments: QueuedPromptAttachment[];
}

export interface ComposerDraftEdit {
  id: string;
  text: string;
  attachments: QueuedPromptAttachment[];
}

const drainingBySession = new Set<string>();
const lastPromptDispatchAtBySession = new Map<string, number>();
const loadedChildSessionIDs = new Set<string>();

export function markSessionPromptDispatched(sessionID: string) {
  lastPromptDispatchAtBySession.set(sessionID, Date.now());
}

export function lastPromptDispatchedAt(sessionID: string): number {
  return lastPromptDispatchAtBySession.get(sessionID) || 0;
}

export function enqueueSessionPrompt(
  sessionID: string,
  text: string,
  options?: { agent?: string; model?: SelectedModel; effort?: QueuedPromptItem["effort"]; attachments?: QueuedPromptAttachment[] }
): QueuedPromptItem {
  const config = sessionAIConfig(sessionID);
  const model = options?.model || config.model;
  const item: QueuedPromptItem = {
    id: `q_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
    sessionID,
    text,
    createdAt: Date.now(),
    agent: options?.agent || config.agent,
    model: model ? { ...model } : undefined,
    effort: options?.effort ?? config.effort,
    attachments: options?.attachments ?? [],
  };
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  setWorkspace("queuedPromptsBySession", sessionID, [...current, item]);
  return item;
}

export function removeQueuedPrompt(sessionID: string, itemID: string) {
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  setWorkspace("queuedPromptsBySession", sessionID, current.filter((item) => item.id !== itemID));
}

export function editQueuedPrompt(sessionID: string, itemID: string) {
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  const target = current.find((item) => item.id === itemID);
  if (!target) return;
  removeQueuedPrompt(sessionID, itemID);
  setWorkspace("composerDraftEditBySession", sessionID, {
    id: target.id,
    text: target.text,
    attachments: target.attachments,
  });
}

export function consumeComposerDraftEdit(sessionID: string) {
  setWorkspace("composerDraftEditBySession", sessionID, undefined);
}

export async function sendQueuedPromptNow(sessionID: string, itemID: string): Promise<void> {
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  const target = current.find((item) => item.id === itemID);
  if (!target) return;
  removeQueuedPrompt(sessionID, itemID);

  await submitSessionPrompt(target.text, sessionID, {
    agent: target.agent,
    model: target.model,
    effort: target.effort,
    attachments: target.attachments,
    forceImmediate: true,
  });
}

export async function drainNextQueuedPrompt(sessionID: string): Promise<void> {
  if (!sessionID || drainingBySession.has(sessionID)) return;
  if (isSessionBusy(sessionID)) return;
  if (workspace.interruptedBySession[sessionID]) return;

  const queue = workspace.queuedPromptsBySession[sessionID] ?? [];
  if (queue.length === 0) return;

  const next = queue[0];
  drainingBySession.add(sessionID);
  removeQueuedPrompt(sessionID, next.id);
  try {
    await submitSessionPrompt(next.text, sessionID, {
      agent: next.agent,
      model: next.model,
      effort: next.effort,
      attachments: next.attachments,
      forceImmediate: true,
    });
  } catch (err) {
    console.error("drainNextQueuedPrompt failed:", err);
  } finally {
    drainingBySession.delete(sessionID);
  }
}

export function extractChildSubagentSessionId(tp: ToolPart): string | undefined {
  const meta = tp.state.metadata as Record<string, any> | undefined;
  const input = tp.state.input as Record<string, any> | undefined;
  if (meta?.sessionId) return String(meta.sessionId);
  if (meta?.sessionID) return String(meta.sessionID);
  if (input?.task_id) return String(input.task_id);
  const out = (tp.state as { output?: string }).output;
  if (typeof out === "string") {
    const m = out.match(/session_id:\s*([a-zA-Z0-9_-]+)/);
    if (m?.[1]) return m[1];
  }
  let desc = typeof input?.description === "string" ? input.description.trim() : "";
  if (!desc) {
    const raw = (tp.state as { raw?: string }).raw;
    if (typeof raw === "string") {
      const m = raw.match(/"description"\s*:\s*"((?:\\.|[^"\\])*)"/);
      if (m?.[1]) desc = m[1].trim();
    }
  }
  if (tp.sessionID && desc) {
    const matched = workspace.sessions.find(
      (s) =>
        s.parentID === tp.sessionID &&
        typeof s.title === "string" &&
        (s.title === desc || s.title.startsWith(`${desc} (`) || s.title.includes(desc))
    );
    if (matched?.id) return matched.id;
  }
  return undefined;
}

function isChildSubagentTurnCompletedCleanly(childSessionID?: string): boolean {
  if (!childSessionID || workspace.interruptedBySession[childSessionID]) return false;
  const turns = workspace.messagesBySession[childSessionID];
  if (!turns || turns.length === 0) return false;
  const lastTurn = turns[turns.length - 1];
  if (lastTurn.info.role !== "assistant") return false;
  if (!lastTurn.info.time?.completed || Boolean(lastTurn.info.error)) return false;
  return !(lastTurn.parts ?? []).some(
    (p) => p.type === "tool" && (p.state.status === "running" || p.state.status === "pending")
  );
}

export function isSubagentToolAlive(
  tp: ToolPart,
  parentSessionID?: string | null
): boolean {
  const childSessionID = extractChildSubagentSessionId(tp);
  if (childSessionID && isSessionBusy(childSessionID)) return true;

  const rawStatus = tp.state.status;
  if (rawStatus !== "running" && rawStatus !== "pending") return false;

  if (childSessionID) {
    if (workspace.interruptedBySession[childSessionID]) return false;
    if (workspace.statusBySession[childSessionID]?.type === "idle") return false;
    if (isChildSubagentTurnCompletedCleanly(childSessionID)) return false;
  }

  const parentID = parentSessionID ?? tp.sessionID;
  return parentID ? isSessionBusy(parentID) : false;
}

export function isSubagentToolInterrupted(
  tp: ToolPart,
  parentSessionID?: string | null
): boolean {
  if (isSubagentToolAlive(tp, parentSessionID)) return false;
  const childSessionID = extractChildSubagentSessionId(tp);
  if (childSessionID && workspace.interruptedBySession[childSessionID]) return true;

  const rawStatus = tp.state.status;
  if (rawStatus === "running" || rawStatus === "pending") {
    if (childSessionID && isChildSubagentTurnCompletedCleanly(childSessionID)) {
      return false;
    }
    return true;
  }
  return rawStatus === "error" && Boolean(childSessionID);
}

export function ensureChildSubagentSessionLoaded(
  childSessionID?: string,
  parentSessionID?: string
) {
  if (!childSessionID) return;
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
        if (info?.id) replaceSession(info);
      })
      .catch(() => {});
  }

  if (!loadedChildSessionIDs.has(childSessionID)) {
    loadedChildSessionIDs.add(childSessionID);
    void loadSessionMessagesOnly(childSessionID).catch(() => {});
  }
}

export function collectChildSubagentSessionIDs(parentSessionID: string): string[] {
  const childIDs = new Set<string>();
  for (const s of workspace.sessions) {
    if (s.parentID === parentSessionID) childIDs.add(s.id);
  }
  const turns = workspace.messagesBySession[parentSessionID] ?? [];
  for (const turn of turns) {
    for (const part of turn.parts) {
      if (part.type === "tool" && part.tool.toLowerCase() === "task") {
        const cid = extractChildSubagentSessionId(part);
        if (cid) childIDs.add(cid);
      }
    }
  }
  return Array.from(childIDs);
}

export function purgeSessionsFromStoreState(
  state: typeof workspace,
  allIDs: string[]
) {
  const idSet = new Set(allIDs);
  for (const id of allIDs) {
    drainingBySession.delete(id);
    lastPromptDispatchAtBySession.delete(id);
    loadedChildSessionIDs.delete(id);
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

export async function deleteChildSubagentSessionsBackend(
  childIDs: string[],
  targetDir?: string
): Promise<void> {
  await Promise.all(
    childIDs.map(async (cid) => {
      try {
        await hoxiAPI.abortSession(cid, targetDir);
      } catch {
        /* ignore */
      }
      try {
        await hoxiAPI.deleteSession(cid, targetDir);
      } catch {
        /* ignore */
      }
    })
  );
}

export async function abortChildSubagentSessions(
  parentSessionID: string
): Promise<void> {
  const childIDs = collectChildSubagentSessionIDs(parentSessionID);
  await Promise.all(
    childIDs.map(async (cid) => {
      markSessionInterrupted(cid, "stopped");
      try {
        await hoxiAPI.abortSession(cid, sessionDirectory(parentSessionID));
      } catch {
        /* ignore */
      }
    })
  );
}

export async function resumeSubagentTask(params: {
  parentSessionID: string;
  childSessionID: string;
  agentType?: string;
  description?: string;
}): Promise<void> {
  const { parentSessionID, childSessionID, agentType, description } = params;
  if (!parentSessionID || !childSessionID) return;
  clearSessionInterrupted(childSessionID);
  const prompt = t("timeline.resumeSubagentPrompt", {
    taskId: childSessionID,
    agentType: agentType || "general",
    description: description || childSessionID,
  });
  await submitSessionPrompt(prompt, parentSessionID, {
    forceImmediate: true,
  });
}

export async function resumeAllInterruptedSubagents(
  parentSessionID: string,
  items: Array<{
    childSessionID: string;
    agentType?: string;
    description?: string;
  }>
): Promise<void> {
  if (!parentSessionID || items.length === 0) return;
  if (items.length === 1) {
    await resumeSubagentTask({ parentSessionID, ...items[0] });
    return;
  }
  for (const item of items) {
    clearSessionInterrupted(item.childSessionID);
  }
  const lines = items.map((item) =>
    t("timeline.resumeSubagentPrompt", {
      taskId: item.childSessionID,
      agentType: item.agentType || "general",
      description: item.description || item.childSessionID,
    })
  );
  await submitSessionPrompt(lines.join("\n\n"), parentSessionID, {
    forceImmediate: true,
  });
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
