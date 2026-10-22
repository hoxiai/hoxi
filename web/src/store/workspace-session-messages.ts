import { createSignal } from "solid-js";
import { produce } from "solid-js/store";
import { t } from "../i18n";
import { hoxiAPI, resolveRunnerRuntimeForProvider, type SkillSelection } from "../services/api";
import type { MessagePart } from "../services/sse-types";
import {
  extractUserTurnSkillSelection,
  revalidateSkillForTargetDirectory,
  setComposerSkillNotice,
  transferDraftComposerSkill,
} from "./skills";
import { recordPromptDispatched, recordUserAbortAction } from "./provider-telemetry";
import { isPlaceholderSessionTitle, summarizeAndRenameSession } from "./session-title";
import {
  isAutoModel,
  markAutoCandidateCooldown,
  modelSupportsImageInput,
  resolveAutoModelTarget,
} from "./auto-models";
import { newIsolatedSession, waitForSessionSandbox } from "./session-sandbox";
import { buildCombinedSystemPrompt } from "./settings";
import { setWorkspace, workspace, type ReasoningEffort } from "./workspace-state";
import { initializeSessionAI, parseModelString, sessionAIConfig } from "./session-ai";
import {
  loadSessionMessages,
  renameSessionTitle,
  replaceSession,
} from "./workspace-sessions";
import { buildWorkspaceFoldersSystemPrompt, workspaceFoldersForDir } from "./workspace-folders";
import {
  isSessionBusy,
  isSessionDeleted,
  sessionDirectory,
  sessionRuntimeForID,
} from "./session-query";
import { pauseSessionGoal } from "./session-goal-state";
import {
  beginQueueDrain,
  endQueueDrain,
  enqueueSessionPrompt,
  markSessionPromptDispatched,
  removeQueuedPrompt,
  restoreQueuedPromptAt,
} from "./prompt-queue";
import { collectChildSubagentSessionIDs } from "./subagent-sessions";

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
    // A model the engine no longer knows can never succeed by retrying it, so
    // the pool must advance exactly like it does for a rate limit.
    const isModelUnavailable =
      error.name === "ProviderModelNotFoundError" ||
      /model not found|model.*not.*available/i.test(errText);
    const isRateOrTransient =
      isModelUnavailable ||
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
        // A runner stop (watchdog, repeated invalid tool calls) explains the engine's own "aborted" error.
        if (error.name === "RunnerStalledError" || error.name === "RepeatedToolFailureError") last.info.error = error;
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

export async function stopSessionToolCommand(
  sessionID: string,
  input: { callID?: string; partID?: string; command?: string }
): Promise<boolean> {
  const id = (sessionID || workspace.activeSessionID || "").trim();
  if (!id) return false;
  const matchesTarget = (p: { id?: string; callID?: string }) =>
    Boolean(
      (input.partID && p.id === input.partID) || (input.callID && p.callID === input.callID)
    );
  const setStoppedFlag = (flag: unknown): unknown => {
    let previous: unknown;
    setWorkspace(
      produce((state) => {
        const turns = state.messagesBySession[id];
        if (!turns) return;
        for (let i = turns.length - 1; i >= 0; i--) {
          const parts = turns[i].parts;
          for (let j = parts.length - 1; j >= 0; j--) {
            const p = parts[j];
            if (p.type !== "tool" || !matchesTarget(p)) continue;
            const stateObj = p.state as { metadata?: Record<string, unknown> };
            const metadata = { ...(stateObj.metadata || {}) };
            previous = metadata.stoppedByUser;
            if (flag === undefined) delete metadata.stoppedByUser;
            else metadata.stoppedByUser = flag;
            stateObj.metadata = metadata;
            return;
          }
        }
      })
    );
    return previous;
  };
  const previous = setStoppedFlag(true);
  try {
    await hoxiAPI.stopToolCommand(id, input, sessionDirectory(id));
    return true;
  } catch (err) {
    // Nothing was stopped (no matching process, or several candidates so the
    // backend refused to guess): roll back the optimistic flag and let the
    // caller surface why instead of failing silently.
    setStoppedFlag(previous);
    console.warn("stop tool command warning:", err);
    return false;
  }
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
  await dispatchSessionPrompt(prompt, id, { forceImmediate: true });
}

// Bumped when the user sends (or queues) a prompt so the timeline re-follows the
// tail. Automatic queue drains skip it and never pull a reader off older output.
const [timelineFollowRequest, setTimelineFollowRequest] =
  createSignal<{ sessionID: string; seq: number } | null>(null);
export { timelineFollowRequest };

function requestTimelineFollow(sessionID: string) {
  setTimelineFollowRequest((prev) => ({ sessionID, seq: (prev?.seq ?? 0) + 1 }));
}

export function uploadPromptAttachment(input: {
  sessionID?: string;
  filename: string;
  mime: string;
  dataUrl: string;
}) {
  return hoxiAPI.uploadAttachment(input);
}

export async function dispatchSessionPrompt(
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
  const wasBusyBeforeSubmit = Boolean(id && isSessionBusy(id));

  // If the session is currently busy and not forced immediate, enqueue the message!
  if (id && !options?.forceImmediate && wasBusyBeforeSubmit) {
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
      const hasInFlightAssistant = turns.some(
        (t) => t.info.role === "assistant" && !t.info.time?.completed && !t.info.error
      );
      // Only poll via HTTP if SSE is disconnected or no assistant turn has arrived yet;
      // otherwise an HTTP snapshot mid-stream races with live SSE token deltas at t=900ms.
      if (
        !workspace.connected ||
        !lastTurn ||
        (lastTurn.info.role === "user" &&
          (!wasBusyBeforeSubmit || !hasInFlightAssistant || lastTurn.info.id === optimisticMsgID))
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
    if (!wasBusyBeforeSubmit) {
      setWorkspace("statusBySession", id, { type: "idle" });
    }
    throw err;
  }

  return id;
}


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
        await dispatchSessionPrompt(text, id, { attachments, skillSelection });
      }
      return;
    }
  }
}

export async function sendQueuedPromptNow(sessionID: string, itemID: string): Promise<void> {
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  const targetIdx = current.findIndex((item) => item.id === itemID);
  if (targetIdx < 0) return;
  const target = current[targetIdx];
  removeQueuedPrompt(sessionID, itemID);

  try {
    await dispatchSessionPrompt(target.text, sessionID, {
      agent: target.agent,
      model: target.model,
      effort: target.effort,
      attachments: target.attachments,
      skillSelection: target.skillSelection,
      forceImmediate: true,
    });
  } catch (err) {
    restoreQueuedPromptAt(sessionID, target, targetIdx);
    console.error("sendQueuedPromptNow failed:", err);
  }
}

export async function drainNextQueuedPrompt(sessionID: string): Promise<void> {
  if (!sessionID || isSessionBusy(sessionID)) return;
  if (workspace.interruptedBySession[sessionID]) return;

  const queue = workspace.queuedPromptsBySession[sessionID] ?? [];
  if (queue.length === 0) return;

  const next = queue[0];
  if (!beginQueueDrain(sessionID)) return;
  try {
    let validatedSkill = next.skillSelection;
    if (validatedSkill) {
      const dir = sessionDirectory(sessionID) || workspace.rootDir || "";
      const rt = sessionRuntimeForID(sessionID);
      const check = await revalidateSkillForTargetDirectory({
        sessionID,
        directory: dir,
        runtime: rt,
        selection: validatedSkill,
      });
      if (!check.ok) {
        setComposerSkillNotice(
          sessionID,
          t("skills.queueSkillInvalid", { name: validatedSkill.name })
        );
        return;
      }
      validatedSkill = check.selection;
    }
    removeQueuedPrompt(sessionID, next.id);
    try {
      await dispatchSessionPrompt(next.text, sessionID, {
        agent: next.agent,
        model: next.model,
        effort: next.effort,
        attachments: next.attachments,
        skillSelection: validatedSkill,
        forceImmediate: true,
        followTimeline: false,
      });
    } catch (err) {
      restoreQueuedPromptAt(sessionID, next, 0);
      throw err;
    }
  } catch (err) {
    console.error("drainNextQueuedPrompt failed:", err);
  } finally {
    endQueueDrain(sessionID);
  }
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
  await dispatchSessionPrompt(prompt, parentSessionID, {
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
  await dispatchSessionPrompt(lines.join("\n\n"), parentSessionID, {
    forceImmediate: true,
  });
}
