import { produce } from "solid-js/store";
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
  loadSessionMessages,
  newIsolatedSession,
  renameSessionTitle,
  replaceSession,
  scheduleRefreshWorktreeStatus,
  selectSession,
  sessionDirectory,
} from "./workspace-sessions";

const lastInterruptAtBySession = new Map<string, number>();

export async function pauseSessionTask(sessionID?: string | null): Promise<void> {
  const id = sessionID ?? workspace.activeSessionID;
  if (!id) return;
  lastInterruptAtBySession.set(id, Date.now());
  setWorkspace("interruptedBySession", id, "paused");
  setWorkspace("statusBySession", id, { type: "idle" });
  try {
    await hoxiAPI.abortSession(id, sessionDirectory(id));
  } catch (err) {
    console.warn("pause session abort warning:", err);
  }
  void loadSessionMessages(id);
}

export async function stopSessionTask(sessionID?: string | null): Promise<void> {
  const id = sessionID ?? workspace.activeSessionID;
  if (!id) return;
  recordUserAbortAction(id);
  lastInterruptAtBySession.set(id, Date.now());
  setWorkspace("interruptedBySession", id, "stopped");
  setWorkspace("statusBySession", id, { type: "idle" });
  try {
    await hoxiAPI.abortSession(id, sessionDirectory(id));
  } catch (err) {
    console.warn("stop session abort warning:", err);
  }
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
  await submitSessionPrompt(prompt, id);
}

export async function revertToHere(
  sessionID: string,
  messageID: string,
  partID?: string
): Promise<void> {
  recordUserRevertAction(sessionID, messageID);
  const info = await hoxiAPI.revertSession(
    sessionID,
    messageID,
    partID,
    sessionDirectory(sessionID)
  );
  replaceSession(info);
}

export async function undoRevert(sessionID: string): Promise<void> {
  const info = await hoxiAPI.unrevertSession(sessionID, sessionDirectory(sessionID));
  replaceSession(info);
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

export async function submitSessionPrompt(
  promptText: string,
  targetSessionID?: string | null,
  options?: {
    agent?: string;
    model?: { providerID: string; modelID: string };
    attachments?: Array<{ type: "file"; mime: string; filename?: string; url: string }>;
  }
): Promise<string> {
  let id = targetSessionID ?? workspace.activeSessionID;
  const isBrandNewSession = !id;
  if (!id) {
    id = await newIsolatedSession();
  }

  const existingTurns = workspace.messagesBySession[id] ?? [];
  const hasPriorUserTurn = existingTurns.some((m) => m.info.role === "user");
  const currentSessionObj = workspace.sessions.find((s) => s.id === id);
  const shouldSummarizeTitle =
    isBrandNewSession ||
    !hasPriorUserTurn ||
    isPlaceholderSessionTitle(currentSessionObj?.title);

  const now = Date.now();
  const optimisticMsgID = `optimistic_${now}`;
  const effectiveAgent = options?.agent || workspace.activeMode;
  const effectiveModel = options?.model || workspace.selectedModel || undefined;
  const attachments = options?.attachments ?? [];

  recordPromptDispatched(id, effectiveModel?.providerID, effectiveModel?.modelID);
  if (shouldSummarizeTitle) {
    void summarizeAndRenameSession(
      id,
      promptText || t("timeline.imagePromptSessionTitle"),
      effectiveModel,
      renameSessionTitle
    );
  }

  // Optimistically show user prompt & busy status immediately (0ms perceived latency)
  lastInterruptAtBySession.delete(id);
  setWorkspace("interruptedBySession", id, undefined);
  setWorkspace("statusBySession", id, { type: "busy" });
  setWorkspace(
    produce((state) => {
      const list = (state.messagesBySession[id!] ||= []);
      const optimisticParts: MessagePart[] = attachments.map((att, idx) => ({
        id: `prt_file_${now}_${idx}`,
        sessionID: id!,
        messageID: optimisticMsgID,
        type: "file",
        mime: att.mime,
        filename: att.filename,
        url: att.url,
      }));
      if (promptText || optimisticParts.length === 0) {
        optimisticParts.push({
          id: `prt_${now}`,
          sessionID: id!,
          messageID: optimisticMsgID,
          type: "text",
          text: promptText,
        });
      }
      list.push({
        info: {
          id: optimisticMsgID,
          sessionID: id!,
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

  try {
    await hoxiAPI.sendPromptAsync(id, promptText, {
      agent: effectiveAgent,
      model: effectiveModel,
      variant: workspace.reasoningEffort || undefined,
      directory: sessionDirectory(id),
      system: buildCombinedSystemPrompt(buildWorkspaceFoldersSystemPrompt()),
      attachments,
    });
    // Sync server messages immediately and follow up shortly after
    void loadSessionMessages(id);
    setTimeout(() => {
      void loadSessionMessages(id!);
    }, 900);
  } catch (err) {
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
  if (info.role === "user" && !info.id.startsWith("optimistic_")) {
    const optIdx = list.findIndex((m) => m.info.id.startsWith("optimistic_"));
    if (optIdx >= 0) {
      list.splice(optIdx, 1);
    }
  }
  const idx = list.findIndex((m) => m.info.id === info.id);
  if (idx >= 0) {
    Object.assign(list[idx].info, info);
  } else {
    list.push({ info, parts: [] });
  }
}

function mutateUpsertPart(state: typeof workspace, part: MessagePart) {
  const list = (state.messagesBySession[part.sessionID] ||= []);
  let turn = list.find((m) => m.info.id === part.messageID);
  if (!turn) {
    turn = {
      info: {
        id: part.messageID,
        sessionID: part.sessionID,
        role: "assistant",
        time: { created: Date.now() },
      },
      parts: [],
    };
    list.push(turn);
  }
  const pIdx = turn.parts.findIndex((p) => p.id === part.id);
  if (pIdx >= 0) {
    Object.assign(turn.parts[pIdx], part);
  } else {
    turn.parts.push(part);
  }
}

export function upsertMessageInfo(info: MessageInfo) {
  recordAssistantTurnUpdated(info);
  setWorkspace(produce((state) => mutateUpsertInfo(state, info)));
}

export function upsertMessagePart(part: MessagePart) {
  recordFirstToken(part.sessionID);
  recordToolPartUpdate(part);
  if (
    part.type === "tool" &&
    part.state.status === "completed" &&
    part.sessionID === workspace.activeSessionID &&
    isMutatingTool(part.tool)
  ) {
    scheduleRefreshWorktreeStatus(part.sessionID, 900);
  }
  const interruptedAt = lastInterruptAtBySession.get(part.sessionID) || 0;
  const isActivelyStreamingPart =
    (part.type === "tool" &&
      (part.state.status === "pending" || part.state.status === "running")) ||
    (part.type === "reasoning" && !part.time?.end);
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
  if (batches.length > 0) {
    const sid = batches[0].sessionID;
    recordFirstToken(sid);
    const interruptedAt = lastInterruptAtBySession.get(sid) || 0;
    if (
      Date.now() - interruptedAt > 2500 &&
      workspace.statusBySession[sid]?.type !== "busy"
    ) {
      setWorkspace("statusBySession", sid, { type: "busy" });
    }
  }
  setWorkspace(
    produce((state) => {
      for (const b of batches) {
        const list = state.messagesBySession[b.sessionID];
        if (!list) continue;
        const turn = list.find((m) => m.info.id === b.messageID);
        if (!turn) continue;
        const part = turn.parts.find((p) => p.id === b.partID) as any;
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
    if (
      part.type === "tool" &&
      part.state.status === "completed" &&
      part.sessionID === workspace.activeSessionID &&
      isMutatingTool(part.tool)
    ) {
      scheduleRefreshWorktreeStatus(part.sessionID, 900);
    }
    const isActivelyStreamingPart =
      (part.type === "tool" &&
        (part.state.status === "pending" || part.state.status === "running")) ||
      (part.type === "reasoning" && !part.time?.end);
    if (isActivelyStreamingPart) {
      busySessionsToMark.add(part.sessionID);
    }
  }

  for (const d of batch.deltas) {
    recordFirstToken(d.sessionID);
    busySessionsToMark.add(d.sessionID);
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
        const turn = list.find((m) => m.info.id === b.messageID);
        if (!turn) continue;
        const part = turn.parts.find((p) => p.id === b.partID) as any;
        if (part && typeof part[b.field] === "string") {
          part[b.field] += b.delta;
        }
      }
    })
  );
}

