import { produce } from "solid-js/store";
import { t } from "../i18n";
import { hoxiAPI } from "../services/api";
import type { MessagePart, ToolPart } from "../services/sse-types";
import { recordUserRevertAction } from "./provider-telemetry";
import { type SelectedModel, setWorkspace, workspace } from "./workspace";
import {
  activeDirectory,
  isSessionBusy,
  loadSessionMessages,
  loadSessionMessagesOnly,
  replaceSession,
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

export function enqueueSessionPrompt(
  sessionID: string,
  text: string,
  options?: { agent?: string; model?: SelectedModel; attachments?: QueuedPromptAttachment[] }
): QueuedPromptItem {
  const item: QueuedPromptItem = {
    id: `q_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
    sessionID,
    text,
    createdAt: Date.now(),
    agent: options?.agent || workspace.activeMode,
    model: options?.model || workspace.selectedModel || undefined,
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
  const wasBusy = isSessionBusy(sessionID);
  removeQueuedPrompt(sessionID, itemID);

  await submitSessionPrompt(target.text, sessionID, {
    agent: target.agent,
    model: target.model,
    attachments: target.attachments,
    forceImmediate: true,
    beforeSend: wasBusy
      ? async () => {
          try {
            await hoxiAPI.abortSession(sessionID, sessionDirectory(sessionID));
          } catch (err) {
            console.warn("abort before sendQueuedPromptNow warning:", err);
          }
          await abortChildSubagentSessions(sessionID);
        }
      : undefined,
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
    delete state.loadingMessagesBySession[id];
    delete state.statusBySession[id];
    delete state.sessionFinishedAt[id];
    delete state.unreadCompletedBySession[id];
    delete state.diffsBySession[id];
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

export async function syncSessionStatuses(directory?: string): Promise<void> {
  try {
    const dir = directory || activeDirectory() || workspace.rootDir || undefined;
    const remoteStatuses = await hoxiAPI.getSessionStatuses(dir);
    const now = Date.now();
    const newlyIdleSessions: string[] = [];

    setWorkspace(
      produce((state) => {
        for (const [sid, status] of Object.entries(remoteStatuses || {})) {
          if (
            status?.type === "idle" &&
            now - (lastPromptDispatchAtBySession.get(sid) || 0) <= 3500
          ) {
            continue;
          }
          state.statusBySession[sid] = status;
        }
        for (const [sid, current] of Object.entries(state.statusBySession)) {
          if (
            (current?.type === "busy" || current?.type === "retry") &&
            !remoteStatuses?.[sid]
          ) {
            const dispatchedAt = lastPromptDispatchAtBySession.get(sid) || 0;
            if (now - dispatchedAt > 3500) {
              state.statusBySession[sid] = { type: "idle" };
              newlyIdleSessions.push(sid);
            }
          }
        }
      })
    );

    for (const sid of newlyIdleSessions) {
      void drainNextQueuedPrompt(sid);
    }
  } catch (err) {
    console.warn("syncSessionStatuses warning:", err);
  }
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

export async function revertToHere(
  sessionID: string,
  messageID: string,
  partID?: string
): Promise<void> {
  recordUserRevertAction(sessionID, messageID);

  if (!partID) {
    const turns = workspace.messagesBySession[sessionID] ?? [];
    const targetTurn = turns.find((m) => m.info.id === messageID);
    if (targetTurn && targetTurn.info.role === "user") {
      const text = targetTurn.parts
        .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
        .map((p) => p.text)
        .join("\n")
        .trim();
      const attachments = targetTurn.parts
        .filter((p): p is Extract<MessagePart, { type: "file" }> => p.type === "file")
        .map((p) => ({
          type: "file" as const,
          mime: p.mime,
          filename: p.filename,
          url: p.url,
        }));
      if (text || attachments.length > 0) {
        setWorkspace("composerDraftEditBySession", sessionID, {
          id: `revert_${messageID}`,
          text,
          attachments,
        });
      }
    }
  }

  const existingSession = workspace.sessions.find((s) => s.id === sessionID);
  if (existingSession) {
    replaceSession({
      ...existingSession,
      revert: { messageID, partID },
    });
  }

  if (isSessionBusy(sessionID)) {
    setWorkspace("statusBySession", sessionID, { type: "idle" });
    try {
      await hoxiAPI.abortSession(sessionID, sessionDirectory(sessionID));
    } catch {
      /* ignore */
    }
    void abortChildSubagentSessions(sessionID);
  }

  const info = await hoxiAPI.revertSession(
    sessionID,
    messageID,
    partID,
    sessionDirectory(sessionID)
  );
  replaceSession(info);
  void loadSessionMessages(sessionID);
}

export async function undoRevert(sessionID: string): Promise<void> {
  const existingSession = workspace.sessions.find((s) => s.id === sessionID);
  if (existingSession) {
    replaceSession({
      ...existingSession,
      revert: undefined,
    });
  }
  const info = await hoxiAPI.unrevertSession(sessionID, sessionDirectory(sessionID));
  replaceSession(info);
  void loadSessionMessages(sessionID);
}

export async function forkFromMessage(
  sessionID: string,
  messageID?: string
): Promise<string> {
  const forked = await hoxiAPI.forkSession(sessionID, messageID, sessionDirectory(sessionID));
  replaceSession(forked);
  await selectSession(forked.id);
  return forked.id;
}
