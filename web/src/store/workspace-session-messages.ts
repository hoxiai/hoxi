import { produce } from "solid-js/store";
import {
  findMessageIndex,
  findPartIndex,
  upsertTurnInfo,
  upsertTurnPart,
} from "./message-lookup";
import { t } from "../i18n";
import { hoxiAPI } from "../services/api";
import type { StreamFrameBatch } from "../services/sse";
import type { MessageInfo, MessagePart } from "../services/sse-types";
import {
  recordAssistantTurnUpdated,
  recordFirstToken,
  recordPromptDispatched,
  recordToolPartUpdate,
  recordUserAbortAction,
  recordUserRevertAction,
} from "./provider-telemetry";
import { isPlaceholderSessionTitle, summarizeAndRenameSession } from "./session-title";
import { buildCombinedSystemPrompt, setWorkspace, workspace } from "./workspace";
import { buildWorkspaceFoldersSystemPrompt } from "./workspace-projects";
import {
  isSessionBusy,
  loadSessionMessages,
  newIsolatedSession,
  renameSessionTitle,
  replaceSession,
  scheduleRefreshWorktreeStatus,
  selectSession,
  sessionDirectory,
} from "./workspace-sessions";
import {
  abortChildSubagentSessions,
  enqueueSessionPrompt,
  ensureChildSubagentSessionLoaded,
  extractChildSubagentSessionId,
  forkFromMessage,
  markSessionPromptDispatched,
  revertToHere,
  undoRevert,
} from "./workspace-queue-subagents";

export { forkFromMessage, revertToHere, undoRevert };

const lastInterruptAtBySession = new Map<string, number>();

export function markSessionInterrupted(
  sessionID: string,
  kind: "paused" | "stopped" = "stopped"
) {
  if (!sessionID) return;
  lastInterruptAtBySession.set(sessionID, Date.now());
  setWorkspace("interruptedBySession", sessionID, kind);
  setWorkspace("statusBySession", sessionID, { type: "idle" });
}

export function clearSessionInterrupted(sessionID: string) {
  if (!sessionID) return;
  lastInterruptAtBySession.delete(sessionID);
  setWorkspace("interruptedBySession", sessionID, undefined);
}

export async function pauseSessionTask(sessionID?: string | null): Promise<void> {
  const id = sessionID ?? workspace.activeSessionID;
  if (!id) return;
  markSessionInterrupted(id, "paused");
  try {
    await hoxiAPI.abortSession(id, sessionDirectory(id));
  } catch (err) {
    console.warn("pause session abort warning:", err);
  }
  void abortChildSubagentSessions(id);
  void loadSessionMessages(id);
}

export async function stopSessionTask(sessionID?: string | null): Promise<void> {
  const id = sessionID ?? workspace.activeSessionID;
  if (!id) return;
  recordUserAbortAction(id);
  markSessionInterrupted(id, "stopped");
  try {
    await hoxiAPI.abortSession(id, sessionDirectory(id));
  } catch (err) {
    console.warn("stop session abort warning:", err);
  }
  void abortChildSubagentSessions(id);
  void loadSessionMessages(id);
}

export async function resumePausedSession(
  sessionID?: string | null,
  extraInstruction?: string
): Promise<void> {
  const id = sessionID ?? workspace.activeSessionID;
  if (!id) return;
  const note = (extraInstruction || "").trim();
  const prompt = note
    ? `${t("timeline.resumePromptPrefix")}\n\n${note}`
    : t("timeline.resumeDefaultPrompt");
  await submitSessionPrompt(prompt, id, { forceImmediate: true });
}

export async function submitSessionPrompt(
  promptText: string,
  targetSessionID?: string | null,
  options?: {
    agent?: string;
    model?: { providerID: string; modelID: string };
    attachments?: Array<{ type: "file"; mime: string; filename?: string; url: string }>;
    forceImmediate?: boolean;
    beforeSend?: () => Promise<void>;
  }
): Promise<string> {
  let id = targetSessionID ?? workspace.activeSessionID;
  const isBrandNewSession = !id;
  const effectiveAgent = options?.agent || workspace.activeMode;
  const effectiveModel = options?.model || workspace.selectedModel || undefined;
  const attachments = options?.attachments ?? [];

  // If the session is currently busy and not forced immediate, enqueue the message!
  if (id && !options?.forceImmediate && isSessionBusy(id)) {
    enqueueSessionPrompt(id, promptText, {
      agent: effectiveAgent,
      model: effectiveModel,
      attachments,
    });
    return id;
  }

  const now = Date.now();
  const optimisticMsgID = `optimistic_${now}`;
  let shouldSummarizeTitle = isBrandNewSession;
  let stagedOptimistic = false;

  const stageOptimisticTurn = (sessionID: string) => {
    if (stagedOptimistic) return;
    stagedOptimistic = true;
    const existingTurns = workspace.messagesBySession[sessionID] ?? [];
    const hasPriorUserTurn = existingTurns.some((m) => m.info.role === "user");
    const currentSessionObj = workspace.sessions.find((s) => s.id === sessionID);
    const revertBoundary = currentSessionObj?.revert;
    if (currentSessionObj && revertBoundary) {
      replaceSession({ ...currentSessionObj, revert: undefined });
    }
    shouldSummarizeTitle =
      isBrandNewSession ||
      !hasPriorUserTurn ||
      isPlaceholderSessionTitle(currentSessionObj?.title);

    lastInterruptAtBySession.delete(sessionID);
    setWorkspace("interruptedBySession", sessionID, undefined);
    setWorkspace(
      produce((state) => {
        const list = (state.messagesBySession[sessionID] ||= []);
        if (revertBoundary?.messageID) {
          const bIdx = list.findIndex((m) => m.info.id === revertBoundary.messageID);
          if (bIdx >= 0) {
            list.splice(revertBoundary.partID ? bIdx + 1 : bIdx);
          }
        }
        const optimisticParts: MessagePart[] = attachments.map((att, idx) => ({
          id: `prt_file_${now}_${idx}`,
          sessionID,
          messageID: optimisticMsgID,
          type: "file",
          mime: att.mime,
          filename: att.filename,
          url: att.url,
        }));
        if (promptText || optimisticParts.length === 0) {
          optimisticParts.push({
            id: `prt_${now}`,
            sessionID,
            messageID: optimisticMsgID,
            type: "text",
            text: promptText,
          });
        }
        list.push({
          info: {
            id: optimisticMsgID,
            sessionID,
            role: "user",
            time: { created: now },
            agent: effectiveAgent,
            modelID: effectiveModel?.modelID,
            providerID: effectiveModel?.providerID,
          },
          parts: optimisticParts,
        });
      })
    );
    markSessionPromptDispatched(sessionID);
    recordPromptDispatched(sessionID, effectiveModel?.providerID, effectiveModel?.modelID);
    setWorkspace("statusBySession", sessionID, { type: "busy" });
  };

  if (!id) {
    id = await newIsolatedSession(undefined, {
      onSessionCreated: stageOptimisticTurn,
    });
  }
  stageOptimisticTurn(id);

  try {
    if (options?.beforeSend) {
      await options.beforeSend();
    }
    markSessionPromptDispatched(id);
    await hoxiAPI.sendPromptAsync(id, promptText, {
      agent: effectiveAgent,
      model: effectiveModel,
      variant: workspace.reasoningEffort || undefined,
      directory: sessionDirectory(id),
      system: buildCombinedSystemPrompt(buildWorkspaceFoldersSystemPrompt()),
      attachments,
    });
    if (!lastInterruptAtBySession.has(id)) {
      markSessionPromptDispatched(id);
      if (workspace.statusBySession[id]?.type !== "busy") {
        setWorkspace("statusBySession", id, { type: "busy" });
      }
    }
    if (shouldSummarizeTitle) {
      void summarizeAndRenameSession(
        id,
        promptText || t("timeline.imagePromptSessionTitle"),
        effectiveModel,
        renameSessionTitle
      );
    }
    setTimeout(() => {
      void loadSessionMessages(id!);
    }, 900);
  } catch (err) {
    setWorkspace(
      produce((state) => {
        const list = state.messagesBySession[id!];
        if (!list) return;
        const optIdx = list.findIndex((m) => m.info.id === optimisticMsgID);
        if (optIdx >= 0) list.splice(optIdx, 1);
      })
    );
    setWorkspace("statusBySession", id, { type: "idle" });
    throw err;
  }

  return id;
}

export const sendPrompt = submitSessionPrompt;

export async function retryLastUserPrompt(sessionID?: string | null): Promise<void> {
  const id = sessionID ?? workspace.activeSessionID;
  if (!id) return;
  const turns = workspace.messagesBySession[id] ?? [];
  for (let i = turns.length - 1; i >= 0; i--) {
    if (turns[i].info.role === "user") {
      const text = turns[i].parts
        .filter((p): p is Extract<MessagePart, { type: "text" }> => p.type === "text")
        .map((p) => p.text)
        .join("\n")
        .trim();
      if (text) {
        await submitSessionPrompt(text, id);
      }
      return;
    }
  }
}

function isMutatingTool(toolName?: string): boolean {
  const t = (toolName || "").toLowerCase();
  return (
    t === "edit" ||
    t === "write" ||
    t === "patch" ||
    t === "multiedit" ||
    t === "apply_patch" ||
    t === "bash" ||
    t === "shell"
  );
}

function mutateUpsertInfo(state: typeof workspace, info: MessageInfo) {
  const list = (state.messagesBySession[info.sessionID] ||= []);
  upsertTurnInfo(list, info);
}

function mutateUpsertPart(state: typeof workspace, part: MessagePart) {
  const list = (state.messagesBySession[part.sessionID] ||= []);
  upsertTurnPart(list, part);
}

function isMessageTurnAlreadyFinished(
  sessionID: string,
  messageID: string,
  batchInfos?: MessageInfo[]
): boolean {
  const fromBatch = batchInfos?.find(
    (i) => i.sessionID === sessionID && i.id === messageID
  );
  if (fromBatch && (fromBatch.time?.completed || fromBatch.error)) return true;
  const list = workspace.messagesBySession[sessionID];
  if (!list) return false;
  const idx = findMessageIndex(list, messageID);
  if (idx < 0) return false;
  const info = list[idx].info;
  return Boolean(info.time?.completed || info.error);
}

export function upsertMessageInfo(info: MessageInfo) {
  recordAssistantTurnUpdated(info);
  setWorkspace(produce((state) => mutateUpsertInfo(state, info)));
}

export function upsertMessagePart(part: MessagePart) {
  recordFirstToken(part.sessionID);
  recordToolPartUpdate(part);
  if (part.type === "tool" && part.tool.toLowerCase() === "task") {
    ensureChildSubagentSessionLoaded(
      extractChildSubagentSessionId(part),
      part.sessionID
    );
  }
  if (
    part.type === "tool" &&
      (part.state.status === "pending" ||
        part.state.status === "running" ||
        part.state.status === "completed") &&
      part.sessionID === workspace.activeSessionID &&
      isMutatingTool(part.tool)
    ) {
      scheduleRefreshWorktreeStatus(part.sessionID, 250);
  }
  const interruptedAt = lastInterruptAtBySession.get(part.sessionID) || 0;
  const isActivelyStreamingPart =
    ((part.type === "tool" &&
      (part.state.status === "pending" || part.state.status === "running")) ||
      (part.type === "reasoning" && !part.time?.end)) &&
    !isMessageTurnAlreadyFinished(part.sessionID, part.messageID);
  if (
    isActivelyStreamingPart &&
    Date.now() - interruptedAt > 2500 &&
    workspace.statusBySession[part.sessionID]?.type !== "busy"
  ) {
    setWorkspace("statusBySession", part.sessionID, { type: "busy" });
  }
  setWorkspace(produce((state) => mutateUpsertPart(state, part)));
}

export function applyPartDeltaBatch(
  batches: Array<{
    sessionID: string;
    messageID: string;
    partID: string;
    field: string;
    delta: string;
  }>
) {
  for (const b of batches) {
    recordFirstToken(b.sessionID);
    const interruptedAt = lastInterruptAtBySession.get(b.sessionID) || 0;
    if (
      Date.now() - interruptedAt > 2500 &&
      !isMessageTurnAlreadyFinished(b.sessionID, b.messageID) &&
      workspace.statusBySession[b.sessionID]?.type !== "busy"
    ) {
      setWorkspace("statusBySession", b.sessionID, { type: "busy" });
    }
  }
  setWorkspace(
    produce((state) => {
      for (const b of batches) {
        const list = state.messagesBySession[b.sessionID];
        if (!list) continue;
        const turn = list[findMessageIndex(list, b.messageID)];
        if (!turn) continue;
        const part = turn.parts[findPartIndex(turn.parts, b.partID)] as any;
        if (part && typeof part[b.field] === "string") {
          part[b.field] += b.delta;
        }
      }
    })
  );
}

export function applyStreamFrameBatch(batch: StreamFrameBatch) {
  const now = Date.now();
  const busySessionsToMark = new Set<string>();

  for (const info of batch.infos) {
    recordAssistantTurnUpdated(info);
  }

  for (const part of batch.parts) {
    recordFirstToken(part.sessionID);
    recordToolPartUpdate(part);
    if (part.type === "tool" && part.tool.toLowerCase() === "task") {
      ensureChildSubagentSessionLoaded(
        extractChildSubagentSessionId(part),
        part.sessionID
      );
    }
    if (
      part.type === "tool" &&
      (part.state.status === "pending" ||
        part.state.status === "running" ||
        part.state.status === "completed") &&
      part.sessionID === workspace.activeSessionID &&
      isMutatingTool(part.tool)
    ) {
      scheduleRefreshWorktreeStatus(part.sessionID, 250);
    }
    const isActivelyStreamingPart =
      ((part.type === "tool" &&
        (part.state.status === "pending" || part.state.status === "running")) ||
        (part.type === "reasoning" && !part.time?.end)) &&
      !isMessageTurnAlreadyFinished(part.sessionID, part.messageID, batch.infos);
    if (isActivelyStreamingPart) {
      busySessionsToMark.add(part.sessionID);
    }
  }

  for (const d of batch.deltas) {
    recordFirstToken(d.sessionID);
    if (!isMessageTurnAlreadyFinished(d.sessionID, d.messageID, batch.infos)) {
      busySessionsToMark.add(d.sessionID);
    }
  }

  for (const sid of busySessionsToMark) {
    const interruptedAt = lastInterruptAtBySession.get(sid) || 0;
    if (
      now - interruptedAt > 2500 &&
      workspace.statusBySession[sid]?.type !== "busy"
    ) {
      setWorkspace("statusBySession", sid, { type: "busy" });
    }
  }

  setWorkspace(
    produce((state) => {
      for (const info of batch.infos) {
        mutateUpsertInfo(state, info);
      }
      for (const part of batch.parts) {
        mutateUpsertPart(state, part);
      }
      for (const b of batch.deltas) {
        const list = state.messagesBySession[b.sessionID];
        if (!list) continue;
        const turn = list[findMessageIndex(list, b.messageID)];
        if (!turn) continue;
        const part = turn.parts[findPartIndex(turn.parts, b.partID)] as any;
        if (part && typeof part[b.field] === "string") {
          part[b.field] += b.delta;
        }
      }
    })
  );
}
