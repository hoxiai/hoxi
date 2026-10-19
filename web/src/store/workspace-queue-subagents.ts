import { produce } from "solid-js/store";
import { t } from "../i18n";
import { hoxiAPI } from "../services/api";
import type { ToolPart } from "../services/sse-types";
import { forgetSubagentLinks, subagentChildIDFromPart } from "./message-lookup";
import { type SelectedModel, setWorkspace, workspace } from "./workspace";
import { sessionAIConfig } from "./session-ai";
import { subagentDisplayTitle, type SubagentTitle } from "./session-title";
import {
  forkFromMessage,
  isSessionBusy,
  isSessionDeleted,
  loadSessionMessagesOnly,
  pendingRevertConfirm,
  type PendingRevertRequest,
  replaceSession,
  revertToHere,
  scheduleRefreshSessionDiff,
  scheduleRefreshWorktreeStatus,
  sessionDirectory,
  setPendingRevertConfirm,
  undoRevert,
} from "./workspace-sessions";
import {
  clearSessionInterrupted,
  markSessionInterrupted,
  submitSessionPrompt,
} from "./workspace-session-messages";

export {
  forkFromMessage,
  pendingRevertConfirm,
  type PendingRevertRequest,
  revertToHere,
  setPendingRevertConfirm,
  undoRevert,
};

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
const settledChildSessionIDs = new Set<string>();

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
      followTimeline: false,
    });
  } catch (err) {
    console.error("drainNextQueuedPrompt failed:", err);
  } finally {
    drainingBySession.delete(sessionID);
  }
}

export function extractChildSubagentSessionId(tp: ToolPart): string | undefined {
  const direct = subagentChildIDFromPart(tp);
  if (direct) return direct;
  const input = tp.state.input as Record<string, any> | undefined;
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

/** 1-based spawn order of `childID` among its parent's subagents, or 0 when unknown. */
export function subagentOrdinal(parentID?: string | null, childID?: string | null): number {
  if (!parentID || !childID) return 0;
  const seen = new Set<string>();
  for (const turn of workspace.messagesBySession[parentID] ?? []) {
    for (const part of turn.parts ?? []) {
      if (part.type !== "tool" || (part.tool || "").toLowerCase() !== "task") continue;
      const id = extractChildSubagentSessionId(part as ToolPart);
      if (!id || seen.has(id)) continue;
      seen.add(id);
      if (id === childID) return seen.size;
    }
  }
  const siblings = workspace.sessions
    .filter((s) => s.parentID === parentID)
    .sort((a, b) => (a.time?.created || 0) - (b.time?.created || 0));
  return siblings.findIndex((s) => s.id === childID) + 1;
}

/** Display title for a subagent, falling back to "Subtask N" when it repeats its parent's title. */
export function resolveSubagentTitle(
  parentID: string | null | undefined,
  childID: string | null | undefined,
  rawTitle?: string | null,
): SubagentTitle {
  const parentTitle = parentID ? workspace.sessions.find((s) => s.id === parentID)?.title : undefined;
  const fallback = rawTitle || workspace.sessions.find((s) => s.id === childID)?.title;
  return subagentDisplayTitle(parentTitle, fallback, () => subagentOrdinal(parentID, childID));
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

  const needsInitialLoad = !loadedChildSessionIDs.has(childSessionID);
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

export function collectChildSubagentSessionIDs(parentSessionID: string): string[] {
  const childIDs = new Set<string>();
  for (const s of workspace.sessions) {
    if (s.parentID === parentSessionID) childIDs.add(s.id);
  }
  const turns = workspace.messagesBySession[parentSessionID] ?? [];
  for (const turn of turns) {
    for (const part of turn.parts) {
      if (part.type === "tool") {
        const lower = (part.tool || "").toLowerCase();
        if (lower === "task" || lower === "agent" || lower.includes("subagent")) {
          const cid = extractChildSubagentSessionId(part);
          if (cid) childIDs.add(cid);
        }
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
  forgetSubagentLinks(allIDs);
  for (const id of allIDs) {
    drainingBySession.delete(id);
    lastPromptDispatchAtBySession.delete(id);
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
