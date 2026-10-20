import { createSignal } from "solid-js";
import { produce } from "solid-js/store";
import {
  findMessageIndex,
  findPartIndex,
  upsertTurnInfo,
  upsertTurnPart,
} from "./message-lookup";
import { t } from "../i18n";
import { ensureAIConsent } from "./privacy";
import { hoxiAPI, resolveRunnerRuntimeForProvider, type SkillSelection } from "../services/api";
import type { StreamFrameBatch } from "../services/sse";
import type { MessageInfo, MessagePart } from "../services/sse-types";
import {
  extractUserTurnSkillSelection,
  revalidateSkillForTargetDirectory,
  setComposerSkillNotice,
  transferDraftComposerSkill,
} from "./skills";
import {
  recordAssistantTurnUpdated,
  recordFirstToken,
  recordPromptDispatched,
  recordToolPartUpdate,
  recordUserAbortAction,
  recordUserRevertAction,
} from "./provider-telemetry";
import { isPlaceholderSessionTitle, summarizeAndRenameSession } from "./session-title";
import {
  isAutoModel,
  markAutoCandidateCooldown,
  modelSupportsImageInput,
  resolveAutoModelTarget,
} from "./auto-models";
import { newIsolatedSession, waitForSessionSandbox } from "./session-sandbox";
import { buildCombinedSystemPrompt, setWorkspace, workspace } from "./workspace";
import { parseGoalCommand, pauseSessionGoal, runGoalCommand } from "./session-goal";
import { buildWorkspaceFoldersSystemPrompt, workspaceFoldersForDir } from "./workspace-projects";
import { initializeSessionAI, parseModelString, sessionAIConfig, type ReasoningEffort } from "./session-ai";
import { sessionRuntimeForID } from "./workspace-providers";
import {
  isSessionBusy,
  isSessionDeleted,
  loadSessionMessages,
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

interface AutoDispatchState {
  poolID: string;
  target: { providerID: string; modelID: string };
  promptText: string;
  agent?: string;
  effort?: ReasoningEffort;
  attachments?: Array<{ type: "file"; mime: string; filename?: string; url: string }>;
  skillSelection?: SkillSelection;
  triedKeys: Set<string>;
  dispatchedAt: number;
}

const lastAutoDispatchBySession = new Map<string, AutoDispatchState>();

export function markSessionInterrupted(
  sessionID: string,
  kind: "paused" | "stopped" = "stopped"
) {
  if (!sessionID) return;
  pauseSessionGoal(sessionID, "stopped");
  lastInterruptAtBySession.set(sessionID, Date.now());
  setWorkspace("interruptedBySession", sessionID, kind);
  setWorkspace("statusBySession", sessionID, { type: "idle" });
}

export function clearSessionInterrupted(sessionID: string) {
  if (!sessionID) return;
  lastInterruptAtBySession.delete(sessionID);
  setWorkspace("interruptedBySession", sessionID, undefined);
}

export async function handleSessionErrorEvent(
  sessionID: string,
  error?: { name: string; data?: { message?: string; statusCode?: number; isRetryable?: boolean } }
): Promise<void> {
  if (!sessionID) return;
  pauseSessionGoal(sessionID, "error");
  const autoState = lastAutoDispatchBySession.get(sessionID);
  if (autoState && error && error.name !== "RunnerStalledError") {
    const errText = error.data?.message || error.name || "error";
    const status = error.data?.statusCode;
    markAutoCandidateCooldown(autoState.target.providerID, autoState.target.modelID, errText, status);
    const isElapsedFast = Date.now() - autoState.dispatchedAt < 45_000;
    const isRateOrTransient =
      status === 429 ||
      status === 502 ||
      status === 503 ||
      Boolean(error.data?.isRetryable) ||
      /429|rate.?limit|quota|too many requests|overloaded|unavailable|timeout/i.test(errText);
    if ((isRateOrTransient || isElapsedFast) && autoState.triedKeys.size < 3) {
      const retryHasImage = (autoState.attachments ?? []).some(
        (a) => (a.mime || "").toLowerCase().startsWith("image/") || /^data:image\//i.test(a.url || "")
      );
      const nextCand = resolveAutoModelTarget(autoState.poolID, {
        advance: true,
        excludeKeys: autoState.triedKeys,
        requireImage: retryHasImage,
      });
      if (
        nextCand &&
        `${nextCand.providerID}/${nextCand.modelID}` !==
          `${autoState.target.providerID}/${autoState.target.modelID}`
      ) {
        autoState.triedKeys.add(`${nextCand.providerID}/${nextCand.modelID}`);
        autoState.target = nextCand;
        autoState.dispatchedAt = Date.now();
        void hoxiAPI
          .sendPromptAsync(sessionID, autoState.promptText, {
            agent: autoState.agent,
            model: nextCand,
            variant: autoState.effort || undefined,
            directory: sessionDirectory(sessionID),
            attachments: autoState.attachments,
            skillSelection: autoState.skillSelection,
            runtime: resolveRunnerRuntimeForProvider(
              nextCand.providerID,
              Boolean(workspace.hoxiProviders?.[nextCand.providerID || ""])
            ),
          })
          .catch(() => {});
        return;
      }
    }
  }
  lastInterruptAtBySession.set(sessionID, Date.now());
  setWorkspace("statusBySession", sessionID, { type: "idle" });
  setWorkspace("unreadCompletedBySession", sessionID, false);
  await loadSessionMessages(sessionID);
  if (!error) return;
  setWorkspace(
    produce((state) => {
      const list = (state.messagesBySession[sessionID] ||= []);
      const last = list[list.length - 1];
      if (last?.info.role === "assistant") {
        // A watchdog stop explains the engine's own "aborted" error.
        if (error.name === "RunnerStalledError") last.info.error = error;
        else last.info.error ||= error;
        return;
      }
      const now = Date.now();
      list.push({
        info: {
          id: `msg_err_${now}`,
          sessionID,
          role: "assistant",
          time: { created: now, completed: now },
          modelID: sessionAIConfig(sessionID).model?.modelID,
          providerID: sessionAIConfig(sessionID).model?.providerID,
          error,
        },
        parts: [],
      });
    })
  );
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

// Bumped when the user sends (or queues) a prompt so the timeline re-follows the
// tail. Automatic queue drains skip it and never pull a reader off older output.
const [timelineFollowRequest, setTimelineFollowRequest] =
  createSignal<{ sessionID: string; seq: number } | null>(null);
export { timelineFollowRequest };

function requestTimelineFollow(sessionID: string) {
  setTimelineFollowRequest((prev) => ({ sessionID, seq: (prev?.seq ?? 0) + 1 }));
}

export async function submitSessionPrompt(
  promptText: string,
  targetSessionID?: string | null,
  options?: {
    agent?: string;
    model?: { providerID: string; modelID: string };
    effort?: ReasoningEffort;
    attachments?: Array<{ type: "file"; mime: string; filename?: string; url: string }>;
    skillSelection?: import("../services/api").SkillSelection;
    forceImmediate?: boolean;
    /** false for automatic dispatches that must not move the reader's scroll position. */
    followTimeline?: boolean;
    beforeSend?: () => Promise<void>;
  }
): Promise<string> {
  const goalCommand = parseGoalCommand(promptText);
  if (goalCommand) {
    await runGoalCommand(goalCommand, targetSessionID ?? workspace.activeSessionID);
    return "";
  }
  if (!ensureAIConsent()) throw new Error(t("privacy.consentRequired"));
  let id = targetSessionID ?? workspace.activeSessionID;
  const isBrandNewSession = !id;
  if (id && isSessionDeleted(id)) throw new Error(t("timeline.sessionUnavailable"));
  if (id && !workspace.aiBySession[id]) {
    await loadSessionMessages(id);
  }
  const config = sessionAIConfig(id);
  const effectiveAgent = options?.agent || config.agent;
  const agentDedicatedModelKey = workspace.globalConfig?.agent?.[effectiveAgent]?.model;
  const agentDedicatedModel =
    !options?.model && typeof agentDedicatedModelKey === "string" && agentDedicatedModelKey
      ? parseModelString(agentDedicatedModelKey)
      : null;
  const attachments = options?.attachments ?? [];
  const hasImageAttachment = attachments.some(
    (a) => (a.mime || "").toLowerCase().startsWith("image/") || /^data:image\//i.test(a.url || "")
  );
  const chosenModel = options?.model || agentDedicatedModel || config.model;
  const isAuto = isAutoModel(chosenModel);
  const resolvedConcrete =
    isAuto && chosenModel
      ? resolveAutoModelTarget(chosenModel, { advance: true, requireImage: hasImageAttachment })
      : null;
  const effectiveModel = resolvedConcrete
    ? { ...resolvedConcrete }
    : chosenModel
    ? { ...chosenModel }
    : undefined;
  if (!effectiveModel && sessionRuntimeForID(id) === "opencode") {
    throw new Error(t("timeline.selectModel"));
  }
  if (effectiveModel && workspace.providers.length && !workspace.providers.some((provider) =>
    provider.id === effectiveModel.providerID && provider.models?.[effectiveModel.modelID])) {
    throw new Error(`${t("timeline.selectModel")}: ${effectiveModel.providerID}/${effectiveModel.modelID}`);
  }
  if (
    hasImageAttachment &&
    effectiveModel &&
    !modelSupportsImageInput(effectiveModel.providerID, effectiveModel.modelID)
  ) {
    throw new Error(t("timeline.modelDoesNotSupportImages", { model: effectiveModel.modelID }));
  }
  const effort = options?.effort ?? config.effort;
  const initialDirectory = id ? sessionDirectory(id) : workspace.rootDir;
  const system = buildCombinedSystemPrompt(buildWorkspaceFoldersSystemPrompt(
    workspaceFoldersForDir(initialDirectory || "")
  ));

  if (id) {
    await waitForSessionSandbox(id);
    if (isSessionDeleted(id)) throw new Error(t("timeline.sessionUnavailable"));
  }

  let effectiveSkill = options?.skillSelection ? { ...options.skillSelection } : undefined;

  // If the session is currently busy and not forced immediate, enqueue the message!
  if (id && !options?.forceImmediate && isSessionBusy(id)) {
    enqueueSessionPrompt(id, promptText, {
      agent: effectiveAgent,
      model: effectiveModel,
      effort,
      attachments,
      skillSelection: effectiveSkill,
    });
    if (options?.followTimeline !== false) requestTimelineFollow(id);
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
        if (promptText || effectiveSkill || optimisticParts.length === 0) {
          optimisticParts.push({
            id: `prt_${now}`,
            sessionID,
            messageID: optimisticMsgID,
            type: "text",
            text: promptText,
            ...(effectiveSkill ? { metadata: { hoxiSkill: { ...effectiveSkill } } } : {}),
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
    if (options?.followTimeline !== false) requestTimelineFollow(sessionID);
  };

  if (!id) {
    id = await newIsolatedSession(undefined, {
      onSessionCreated: (createdID) => {
        transferDraftComposerSkill(createdID);
        stageOptimisticTurn(createdID);
      },
    });
  }
  stageOptimisticTurn(id);
  initializeSessionAI(id, config);

  try {
    if (effectiveSkill) {
      const finalDir = sessionDirectory(id) || workspace.rootDir || "";
      const finalRuntime = resolveRunnerRuntimeForProvider(
        effectiveModel?.providerID,
        Boolean(workspace.hoxiProviders?.[effectiveModel?.providerID || ""])
      );
      const check = await revalidateSkillForTargetDirectory({
        sessionID: id,
        directory: finalDir,
        runtime: finalRuntime,
        selection: effectiveSkill,
      });
      if (!check.ok || !check.selection) {
        const msg = t("skills.sandboxSkillMissing", { name: effectiveSkill.name });
        setComposerSkillNotice(id, msg);
        throw new Error(msg);
      }
      effectiveSkill = check.selection;
      setWorkspace(
        produce((state) => {
          const turn = state.messagesBySession[id!]?.find((m) => m.info.id === optimisticMsgID);
          const textPart = turn?.parts.find((p) => p.type === "text");
          if (textPart && textPart.type === "text") {
            textPart.metadata = { ...(textPart.metadata || {}), hoxiSkill: { ...effectiveSkill! } };
          }
        })
      );
    }
    if (options?.beforeSend) {
      await options.beforeSend();
    }
    if (isAuto && chosenModel && effectiveModel) {
      lastAutoDispatchBySession.set(id, {
        poolID: chosenModel.modelID,
        target: effectiveModel,
        promptText,
        agent: effectiveAgent,
        effort: effort || undefined,
        attachments,
        skillSelection: effectiveSkill,
        triedKeys: new Set([`${effectiveModel.providerID}/${effectiveModel.modelID}`]),
        dispatchedAt: Date.now(),
      });
    } else {
      lastAutoDispatchBySession.delete(id);
    }
    markSessionPromptDispatched(id);
    await hoxiAPI.sendPromptAsync(id, promptText, {
      agent: effectiveAgent,
      model: effectiveModel,
      variant: effort || undefined,
      directory: sessionDirectory(id),
      system,
      attachments,
      skillSelection: effectiveSkill,
      runtime: resolveRunnerRuntimeForProvider(effectiveModel?.providerID, Boolean(workspace.hoxiProviders?.[effectiveModel?.providerID || ""])),
    });
    if (!lastInterruptAtBySession.has(id)) {
      markSessionPromptDispatched(id);
    }
    if (shouldSummarizeTitle) {
      void summarizeAndRenameSession(
        id,
        promptText || (effectiveSkill ? `/${effectiveSkill.name}` : t("timeline.imagePromptSessionTitle")),
        effectiveModel,
        (sessionID, title) => renameSessionTitle(sessionID, title, "automatic")
      );
    }
    setTimeout(() => {
      const targetID = id!;
      const turns = workspace.messagesBySession[targetID] ?? [];
      const lastTurn = turns[turns.length - 1];
      // Only poll via HTTP if SSE is disconnected or no assistant turn has arrived yet;
      // otherwise an HTTP snapshot mid-stream races with live SSE token deltas at t=900ms.
      if (
        !workspace.connected ||
        !lastTurn ||
        lastTurn.info.role === "user"
      ) {
        void loadSessionMessages(targetID);
      }
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
      const attachments = turns[i].parts
        .filter((p): p is Extract<MessagePart, { type: "file" }> => p.type === "file")
        .map((p) => ({
          type: "file" as const,
          mime: p.mime,
          filename: p.filename,
          url: p.url,
        }));
      const skillSelection = extractUserTurnSkillSelection(turns[i].parts) ?? undefined;
      if (text || attachments.length > 0 || skillSelection) {
        await submitSessionPrompt(text, id, { attachments, skillSelection });
      }
      return;
    }
  }
}

function isMutatingTool(toolName?: string): boolean {
  const t = (toolName || "").toLowerCase();
  if (!t || t === "todowrite" || t === "todoread" || t === "read" || t.endsWith("_read")) {
    return false;
  }
  return (
    t === "edit" ||
    t === "write" ||
    t === "patch" ||
    t === "multiedit" ||
    t === "apply_patch" ||
    t === "bash" ||
    t === "shell" ||
    t === "create_file" ||
    t.includes("edit") ||
    t.includes("write") ||
    t.includes("patch") ||
    t.includes("replace") ||
    t.includes("bash") ||
    t.includes("shell")
  );
}

function isTaskLikeTool(toolName?: string): boolean {
  const t = (toolName || "").toLowerCase();
  return t === "task" || t === "agent" || t.includes("subagent");
}

function belongsToActiveSession(sessionID: string): boolean {
  const activeID = workspace.activeSessionID;
  if (!activeID || !sessionID) return false;
  if (sessionID === activeID) return true;
  let cur: string | undefined = sessionID;
  const seen = new Set<string>();
  while (cur && !seen.has(cur)) {
    seen.add(cur);
    const s = workspace.sessions.find((item) => item.id === cur);
    if (!s?.parentID) break;
    if (s.parentID === activeID) return true;
    cur = s.parentID;
  }
  for (const turn of workspace.messagesBySession[activeID] ?? []) {
    for (const p of turn.parts) {
      if (
        p.type === "tool" &&
        isTaskLikeTool(p.tool) &&
        extractChildSubagentSessionId(p) === sessionID
      ) {
        return true;
      }
    }
  }
  return false;
}

function mutateUpsertInfo(state: typeof workspace, info: MessageInfo) {
  const list = (state.messagesBySession[info.sessionID] ||= []);
  upsertTurnInfo(list, info);
}

function mutateUpsertPart(state: typeof workspace, part: MessagePart) {
  const list = (state.messagesBySession[part.sessionID] ||= []);
  upsertTurnPart(list, part);
}

export function upsertMessageInfo(info: MessageInfo) {
  recordAssistantTurnUpdated(info);
  setWorkspace(produce((state) => mutateUpsertInfo(state, info)));
}

export function upsertMessagePart(part: MessagePart) {
  recordFirstToken(part.sessionID);
  recordToolPartUpdate(part);
  if (part.type === "tool" && part.tool === "hoxi_browser" &&
      part.state.status === "running" && part.sessionID === workspace.activeSessionID) {
    setWorkspace({ inspectorOpen: true, inspectorTab: "browser" });
  }
  if (part.type === "tool" && isTaskLikeTool(part.tool)) {
    ensureChildSubagentSessionLoaded(
      extractChildSubagentSessionId(part),
      part.sessionID,
      part.state.status === "completed" || part.state.status === "error"
    );
  }
  if (
    part.type === "tool" &&
    (part.state.status === "pending" ||
      part.state.status === "running" ||
      part.state.status === "completed") &&
    workspace.activeSessionID &&
    isMutatingTool(part.tool) &&
    belongsToActiveSession(part.sessionID)
  ) {
    scheduleRefreshWorktreeStatus(workspace.activeSessionID, 250);
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
  }
  setWorkspace(
    produce((state) => {
      for (const b of batches) {
        const list = state.messagesBySession[b.sessionID];
        if (!list) continue;
        const turn = list[findMessageIndex(list, b.messageID)];
        if (!turn) continue;
        appendPartDelta(turn.parts[findPartIndex(turn.parts, b.partID)], b.field, b.delta);
      }
    })
  );
}

function appendPartDelta(part: MessagePart | undefined, field: string, delta: string) {
  if (!part) return;
  // Older Runner snapshots can omit empty text, including snapshots loaded by REST.
  if (field === "text" && (part.type === "text" || part.type === "reasoning") && part.text == null) {
    part.text = "";
  }
  const fields = part as unknown as Record<string, unknown>;
  if (typeof fields[field] === "string") fields[field] += delta;
}

export function applyStreamFrameBatch(batch: StreamFrameBatch) {
  for (const info of batch.infos) {
    recordAssistantTurnUpdated(info);
  }

  let shouldRefreshActiveWorktree = false;
  for (const part of batch.parts) {
    recordFirstToken(part.sessionID);
    recordToolPartUpdate(part);
    if (
      part.type === "tool" &&
      part.tool === "hoxi_browser" &&
      part.state.status === "running" &&
      part.sessionID === workspace.activeSessionID
    ) {
      setWorkspace({ inspectorOpen: true, inspectorTab: "browser" });
    }
    if (part.type === "tool" && isTaskLikeTool(part.tool)) {
      ensureChildSubagentSessionLoaded(
        extractChildSubagentSessionId(part),
        part.sessionID,
        part.state.status === "completed" || part.state.status === "error"
      );
    }
    if (
      part.type === "tool" &&
      (part.state.status === "pending" ||
        part.state.status === "running" ||
        part.state.status === "completed") &&
      isMutatingTool(part.tool) &&
      belongsToActiveSession(part.sessionID)
    ) {
      shouldRefreshActiveWorktree = true;
    }
  }
  if (shouldRefreshActiveWorktree && workspace.activeSessionID) {
    scheduleRefreshWorktreeStatus(workspace.activeSessionID, 250);
  }

  for (const d of batch.deltas) {
    recordFirstToken(d.sessionID);
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
        appendPartDelta(turn.parts[findPartIndex(turn.parts, b.partID)], b.field, b.delta);
      }
    })
  );
}
