import { batch } from "solid-js";
import { produce } from "solid-js/store";
import { t } from "../i18n";
import { hoxiAPI, resolveRunnerRuntimeForProvider, type SkillSelection } from "../services/api";
import type { MessagePart } from "../services/sse-types";
import { extractUserTurnSkillSelection, revalidateSkillForTargetDirectory, setComposerSkillNotice, transferDraftComposerSkill } from "./skills";
import { recordPromptDispatched } from "./provider-telemetry";
import { extractLocalFallbackTitle, isPlaceholderSessionTitle, summarizeAndRenameSession } from "./session-title";
import { detectRequiredAttachmentModalities, evaluatePromptModalityIntent, isAutoModel, isImageGenerationModel, isNonChatUtilityModelID, markAutoCandidateCooldown, modelSupportsImageInput, resolveAutoModelTarget } from "./auto-models";
import { clearSessionOrchestratorOverride, isOrchestratorModel, resolveOrchestratorDispatchTarget } from "./orchestrator-mode";
import { isVideoGenerationModel } from "./auto-pool-defaults";
import { resolveEffectiveModelEffort } from "./provider-catalog";
import { reportAutoCandidateOutcome } from "./auto-breaker";
import {
  clearAutoFailoverPending,
  clearTurnDispatchState,
  isSessionTurnRetrying,
  noteTurnManualEffortSelection,
  noteTurnManualModelSelection,
  rememberTurnDispatch,
} from "./auto-failover";
import { cleanupUnusedNewSession, hasPendingSessionSandbox, newIsolatedSession, waitForSessionSandbox } from "./session-sandbox";
import { appSettings, buildCombinedSystemPrompt } from "./settings";
import { setWorkspace, workspace, type MessageTurn, type ReasoningEffort, type SelectedModel } from "./workspace-state";
import { initializeSessionAI, parseModelString, resolveEffectiveFastMode, sessionAIConfig, setFastMode } from "./session-ai";
import { sessionGoal } from "./session-goal-state";
import { compareMessageIDOrder } from "./message-lookup";
import { cancelPendingSessionMessageLoads, loadSessionMessages, replaceSession } from "./workspace-sessions";
import { renameSessionTitle } from "./session-lifecycle";
import { currentPromptImageOptions } from "./media-options";
import { hasPendingPrompt, recoverPendingPrompt, sendRecoverablePrompt } from "./request-recovery";
import { buildWorkspaceFoldersSystemPrompt, normalizeFolderPath, workspaceFoldersForDir } from "./workspace-folders";
import { isSessionBusy, isSessionDeleted, requestTimelineFollow, resolveSessionTargetRuntime, sessionDirectory, sessionRuntimeForID, timelineFollowRequest } from "./session-query";
import { beginQueueDrain, commitEditingQueuedPromptToQueue, endQueueDrain, enqueueSessionPrompt, getEditingQueuedPrompt, isQueuedPromptDrainBlocked, markQueuedTurnShown, markSessionPromptDispatched, removeQueuedPrompt, restoreEditingQueuedPrompt, restoreQueuedPromptAt, takeEditingQueuedPrompt } from "./prompt-queue";
import {
  abortChildSubagentSessions,
  applyOptimisticFailedTailRetry,
  canResumeReadOnlyFailedCycle,
  clearSessionInterrupted,
  handleSessionErrorEvent,
  hasRecentSessionInterrupt,
  isRerouteAbortInFlight,
  markSessionInterrupted,
  pauseSessionTask,
  rerouteRetryingTurnToModel,
  stopSessionTask,
  stopSessionToolCommand,
} from "./session-task-control";

export {
  abortChildSubagentSessions,
  clearSessionInterrupted,
  handleSessionErrorEvent,
  isRerouteAbortInFlight,
  markSessionInterrupted,
  pauseSessionTask,
  rerouteRetryingTurnToModel,
  stopSessionTask,
  stopSessionToolCommand,
};

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
  await dispatchSessionPrompt(prompt, id, { forceImmediate: true, continueTask: true });
}

export { timelineFollowRequest };

export function uploadPromptAttachment(input: {
  sessionID?: string;
  filename: string;
  mime: string;
  dataUrl: string;
}) {
  return hoxiAPI.uploadAttachment(input);
}

export function stagePromptFile(directory: string, file: File) {
  return hoxiAPI.stageWorkspaceFile(directory, file);
}

export async function dispatchSessionPrompt(
  promptText: string,
  targetSessionID?: string | null,
  options?: {
    agent?: string;
    model?: { providerID: string; modelID: string };
    effort?: ReasoningEffort;
    fastMode?: boolean;
    directory?: string;
    attachments?: Array<{ type: "file"; mime: string; filename?: string; url: string }>;
    skillSelection?: import("../services/api").SkillSelection;
    imageOptions?: import("../services/api").PromptImageOptions;
    /**
     * Extra system instructions for this turn only. They reach the model as a
     * system prefix and never appear in the timeline as a user message.
     */
    system?: string;
    forceImmediate?: boolean;
    continueTask?: boolean;
    /** false for automatic dispatches that must not move the reader's scroll position. */
    followTimeline?: boolean;
    /** The prompt was on screen as a queued bubble; its turn replaces it without a fade-in. */
    fromQueue?: boolean;
    replaceFailedTail?: boolean;
    resumeFailedTail?: boolean;
    beforeSend?: (sessionID: string) => Promise<void>;
    /** A caller tracking this exact submission needs transport admission, not a local queue entry. */
    clientRequestId?: string;
    onTurnStaged?: (sessionID: string) => void;
  }
): Promise<string> {
  let id = targetSessionID ?? workspace.activeSessionID;
  const isBrandNewSession = !id;
  const requestedDir = normalizeFolderPath(options?.directory) || workspace.rootDir;
  if (id && isSessionDeleted(id)) throw new Error(t("timeline.sessionUnavailable"));
  if (id && hasPendingPrompt(id) && await recoverPendingPrompt(id)) {
    await loadSessionMessages(id);
    // Keep the user's new draft rather than silently treating it as sent.
    throw new Error(t("timeline.previousRequestRecovered"));
  }
  if (id && !workspace.aiBySession[id]) {
    await loadSessionMessages(id);
    if (isSessionDeleted(id)) throw new Error(t("timeline.sessionUnavailable"));
  }
  const config = sessionAIConfig(id);
  const editingQueued = !options?.forceImmediate && id ? getEditingQueuedPrompt(id) : undefined;
  if (options?.clientRequestId && editingQueued) throw new Error(t("timeline.requestAwaitingConfirmation"));
  const effectiveAgent = options?.agent || editingQueued?.item.agent || config.agent;
  const agentDedicatedModelKey = workspace.globalConfig?.agent?.[effectiveAgent]?.model;
  const agentDedicatedModel =
    !options?.model && typeof agentDedicatedModelKey === "string" && agentDedicatedModelKey
      ? parseModelString(agentDedicatedModelKey)
      : null;
  const attachments = options?.attachments ?? [];
  const requiredModalities = detectRequiredAttachmentModalities(attachments);
  const hasImageAttachment = requiredModalities.includes("image");
  const turnIntent = evaluatePromptModalityIntent(promptText, {
    hasImageAttachment,
    skillName: options?.skillSelection?.name || editingQueued?.item.skillSelection?.name,
  });
  const requireVisionBrain = hasImageAttachment || turnIntent.requiresVisionBrain;
  const chosenModel = options?.model || config.model || agentDedicatedModel || editingQueued?.item.model;
  const resolvedOrch = isOrchestratorModel(chosenModel)
    ? resolveOrchestratorDispatchTarget(id ?? null, options?.effort ?? config.effort ?? editingQueued?.item.effort)
    : null;
  const isAuto = !resolvedOrch && isAutoModel(chosenModel);
  const resolvedConcrete =
    isAuto && chosenModel
      ? resolveAutoModelTarget(chosenModel, {
          advance: true,
          requireImage: requireVisionBrain,
          requiredModalities,
          sessionID: id ?? undefined,
        })
      : null;
  let effectiveModel = resolvedOrch ? { ...resolvedOrch.model } : resolvedConcrete ? { ...resolvedConcrete } : chosenModel ? { ...chosenModel } : undefined;
  const explicitlySelectedImageModel =
    !isAuto &&
    Boolean(
      effectiveModel &&
        (isImageGenerationModel(effectiveModel.providerID, effectiveModel.modelID) ||
          isVideoGenerationModel(effectiveModel.providerID, effectiveModel.modelID))
    );
  // Guardrail: if the user selected a non-media utility endpoint (e.g. embedding/tts) or
  // auto-pool resolved a text-only model for a visual turn, reroute to a Vision + Tool-Calling
  // model. Never hijack an explicitly selected native image or video generation model in chat.
  if (
    effectiveModel &&
    !explicitlySelectedImageModel &&
    (isNonChatUtilityModelID(effectiveModel.modelID) ||
      (requireVisionBrain &&
        !hasImageAttachment &&
        !modelSupportsImageInput(effectiveModel.providerID, effectiveModel.modelID)))
  ) {
    const fallbackBrain = resolveAutoModelTarget(turnIntent.recommendedPoolID, {
      advance: false,
      requireImage: requireVisionBrain,
      requiredModalities,
      sessionID: id ?? undefined,
    });
    if (fallbackBrain) {
      effectiveModel = { ...fallbackBrain };
    }
  }
  const activeRt = sessionRuntimeForID(id);
  if (!effectiveModel && (activeRt === "opencode" || activeRt === "native")) {
    throw new Error(t("timeline.selectModelToSend"));
  }
  if (effectiveModel && workspace.providers.length && !workspace.providers.some((provider) =>
    provider.id === effectiveModel.providerID && provider.models?.[effectiveModel.modelID])) {
    throw new Error(
      t("timeline.modelUnavailableReselect", { model: `${effectiveModel.providerID}/${effectiveModel.modelID}` })
    );
  }
  if (
    hasImageAttachment &&
    effectiveModel &&
    !modelSupportsImageInput(effectiveModel.providerID, effectiveModel.modelID)
  ) {
    throw new Error(t("timeline.modelDoesNotSupportImages", { model: effectiveModel.modelID }));
  }
  const rawEffort = options?.effort ?? config.effort ?? editingQueued?.item.effort;
  const effort = resolvedOrch ? resolvedOrch.effort : explicitlySelectedImageModel ? undefined : resolveEffectiveModelEffort(effectiveModel, rawEffort);
  const requestedFastMode = Boolean(options?.fastMode ?? editingQueued?.item.fastMode ?? config.fastMode);
  const baseExplicitSystem = options?.system ?? editingQueued?.item.system;
  const explicitSystem = explicitlySelectedImageModel
    ? baseExplicitSystem?.trim() || undefined
    : [baseExplicitSystem?.trim(), turnIntent.mediaRoutingDirective?.trim()]
        .filter(Boolean)
        .join("\n\n") || undefined;

  if (id && hasPendingSessionSandbox(id)) {
    await waitForSessionSandbox(id);
  }
  if (id && isSessionDeleted(id)) throw new Error(t("timeline.sessionUnavailable"));

  let effectiveSkill = options?.skillSelection ? { ...options.skillSelection } : undefined;
  const wasBusyBeforeSubmit = Boolean(id && isSessionBusy(id));
  const deferEditedBehindQueue = Boolean(
    editingQueued && editingQueued.index > 0 && (workspace.queuedPromptsBySession[id!]?.length ?? 0) > 0
  );

  // If the session is currently busy and not forced immediate, enqueue the message!
  if (id && !options?.forceImmediate && (wasBusyBeforeSubmit || deferEditedBehindQueue)) {
    if (options?.clientRequestId) throw new Error(t("timeline.requestAwaitingConfirmation"));
    const queueOpts = {
      agent: effectiveAgent,
      model: resolvedOrch ? chosenModel : effectiveModel,
      effort,
      fastMode: requestedFastMode || undefined,
      attachments,
      skillSelection: effectiveSkill,
      system: baseExplicitSystem,
    };
    if (editingQueued) commitEditingQueuedPromptToQueue(id, { text: promptText, ...queueOpts });
    else enqueueSessionPrompt(id, promptText, queueOpts);
    options?.onTurnStaged?.(id);
    if (options?.followTimeline !== false) requestTimelineFollow(id);
    if (!wasBusyBeforeSubmit) void drainNextQueuedPrompt(id);
    return id;
  }
  const takenQueuedEdit = editingQueued && id ? takeEditingQueuedPrompt(id) : undefined;

  const now = Date.now();
  const optimisticMsgID = `optimistic_${now}`;
  let shouldSummarizeTitle = isBrandNewSession;
  let stagedOptimistic = false;
  let priorTailID: string | undefined;
  let replacedFailedTurns: MessageTurn[] | undefined;

  const stageOptimisticTurn = (sessionID: string) => {
    if (stagedOptimistic) return;
    stagedOptimistic = true;
    if (options?.fromQueue) markQueuedTurnShown(optimisticMsgID);
    const existingTurns = workspace.messagesBySession[sessionID] ?? [];
    const hasPriorUserTurn = existingTurns.some((m) => m.info.role === "user");
    const currentSessionObj = workspace.sessions.find((s) => s.id === sessionID);
    const revertBoundary = currentSessionObj?.revert;
    shouldSummarizeTitle =
      isBrandNewSession ||
      !hasPriorUserTurn ||
      isPlaceholderSessionTitle(currentSessionObj?.title);
    batch(() => {
      cancelPendingSessionMessageLoads(sessionID);
      if (currentSessionObj && revertBoundary) {
        replaceSession({ ...currentSessionObj, revert: undefined });
      }
      clearSessionInterrupted(sessionID);
      setWorkspace(
        produce((state) => {
          const list = (state.messagesBySession[sessionID] ||= []);
          if (revertBoundary?.messageID) {
            const bIdx = list.findIndex((m) => m.info.id === revertBoundary.messageID);
            if (bIdx >= 0) {
              if (revertBoundary.partID) {
                const pIdx = list[bIdx].parts.findIndex((p) => p.id === revertBoundary.partID);
                if (pIdx >= 0) list[bIdx].parts.splice(pIdx);
                list.splice(bIdx + 1);
              } else {
                list.splice(bIdx);
              }
            } else {
              const afterIdx = list.findIndex(
                (m) => !m.info.id.startsWith("optimistic_") && compareMessageIDOrder(m.info, revertBoundary.messageID) >= 0
              );
              if (afterIdx >= 0) list.splice(afterIdx);
            }
          }
          const retryStage = applyOptimisticFailedTailRetry(
            list,
            options?.replaceFailedTail,
            options?.resumeFailedTail
          );
          replacedFailedTurns = retryStage.replaced;
          if (retryStage.resumedTailID) {
            priorTailID = retryStage.resumedTailID;
            return;
          }
          if (!wasBusyBeforeSubmit) {
            for (const prior of list) {
              if (prior.info.role === "assistant" && !prior.info.time?.completed && !prior.info.error) {
                const settledAt = prior.info.time?.created || now;
                prior.info.time = { ...(prior.info.time || { created: settledAt }), completed: settledAt };
              }
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
          priorTailID = list[list.length - 1]?.info.id;
          list.push({
            info: {
              id: optimisticMsgID,
              sessionID,
              role: "user",
              time: { created: now },
              agent: effectiveAgent,
              modelID: effectiveModel?.modelID,
              providerID: effectiveModel?.providerID,
              ...(options?.continueTask || wasBusyBeforeSubmit ? { continueTask: true } : {}),
            },
            parts: optimisticParts,
          });
        })
      );
      options?.onTurnStaged?.(sessionID);
      markSessionPromptDispatched(sessionID);
      recordPromptDispatched(sessionID, effectiveModel?.providerID, effectiveModel?.modelID);
      setWorkspace("statusBySession", sessionID, { type: "busy" });
      if (options?.followTimeline !== false) requestTimelineFollow(sessionID);
    });
  };

  let createdSessionID: string | null = null;
  if (!id) {
    const fallbackTitle = extractLocalFallbackTitle(
      promptText || (effectiveSkill ? `/${effectiveSkill.name}` : t("timeline.imagePromptSessionTitle"))
    );
    id = await newIsolatedSession(isPlaceholderSessionTitle(fallbackTitle) ? undefined : fallbackTitle, {
      directory: requestedDir,
      onSessionCreated: (createdID) => {
        if (isSessionDeleted(createdID)) return;
        transferDraftComposerSkill(createdID, requestedDir);
        stageOptimisticTurn(createdID);
      },
    });
    createdSessionID = id;
  }
  if (isSessionDeleted(id)) throw new Error(t("timeline.sessionUnavailable"));
  stageOptimisticTurn(id);
  initializeSessionAI(id, config);
  if (isBrandNewSession && workspace.defaultAI.fastMode) setFastMode(false, null);
  if (isBrandNewSession && workspace.defaultAI.orchestratorOverride) clearSessionOrchestratorOverride(null);
  const fastMode =
    !explicitlySelectedImageModel &&
    appSettings.approvalMode !== "unattended" &&
    sessionGoal(id)?.status !== "active" &&
    resolveEffectiveFastMode(effectiveModel, requestedFastMode);
  const targetDir = sessionDirectory(id) || requestedDir || undefined;
  // Caller-supplied instructions ride along as a system prefix so the visible
  // user message stays exactly what the user (or the triggering action) said.
  const system = [explicitSystem?.trim(), buildCombinedSystemPrompt(
    buildWorkspaceFoldersSystemPrompt(workspaceFoldersForDir(targetDir || ""))
  )].filter(Boolean).join("\n\n") || undefined;

  try {
    if (effectiveSkill) {
      const finalDir = targetDir || workspace.rootDir || "";
      const finalRuntime = resolveSessionTargetRuntime(id, effectiveModel?.providerID);
      const check = await revalidateSkillForTargetDirectory({
        sessionID: id,
        directory: finalDir,
        runtime: finalRuntime,
        selection: effectiveSkill,
      });
      if (isSessionDeleted(id)) throw new Error(t("timeline.sessionUnavailable"));
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
      await options.beforeSend(id);
      if (isSessionDeleted(id)) throw new Error(t("timeline.sessionUnavailable"));
    }
    const usedAutoPool = Boolean(isAuto && chosenModel && effectiveModel);
    if (effectiveModel) {
      rememberTurnDispatch(id, {
        poolID: usedAutoPool && chosenModel ? chosenModel.modelID : "",
        target: effectiveModel,
        promptText,
        agent: effectiveAgent,
        effort: effort || undefined,
        fastMode: fastMode || undefined,
        attachments,
        skillSelection: effectiveSkill,
        system,
        triedKeys: new Set([`${effectiveModel.providerID}/${effectiveModel.modelID}`]),
        sameModelRetries: 0,
        backendRetried: false,
        priorTailID,
        dispatchedAt: Date.now(),
      });
    } else {
      clearTurnDispatchState(id);
    }
    markSessionPromptDispatched(id);
    try {
      const effectiveImageOptions =
        options?.imageOptions ??
        (explicitlySelectedImageModel ? currentPromptImageOptions() : undefined);
      const delivery = await sendRecoverablePrompt(id, promptText, {
        clientRequestId: options?.clientRequestId,
        agent: effectiveAgent,
        model: effectiveModel,
        ...(resolvedOrch ? { orchestrator: resolvedOrch.orchestrator } : {}),
        ...(effectiveImageOptions ? { imageOptions: effectiveImageOptions } : {}),
        variant: effort || undefined,
        fastMode: fastMode || undefined,
        directory: targetDir,
        system,
        attachments,
        skillSelection: effectiveSkill,
        runtime: resolvedOrch ? "native" : resolveSessionTargetRuntime(id, effectiveModel?.providerID),
        continueTask: options?.continueTask || wasBusyBeforeSubmit,
      }, () => !isSessionDeleted(id!) && !hasRecentSessionInterrupt(id!));
      if (delivery === "recovered") {
        setWorkspace(produce((state) => {
          const list = state.messagesBySession[id!];
          const idx = list?.findIndex((turn) => turn.info.id === optimisticMsgID) ?? -1;
          if (list && idx >= 0) list.splice(idx, 1);
        }));
        await loadSessionMessages(id);
        if (options?.clientRequestId) throw new Error(t("timeline.previousRequestRecovered"));
        return id;
      }
    } catch (sendErr) {
      if (usedAutoPool && effectiveModel) {
        const errMsg = sendErr instanceof Error ? sendErr.message : String(sendErr || "error");
        const statusMatch = errMsg.match(/\bHTTP (\d{3})\b/);
        const statusCode = statusMatch ? Number(statusMatch[1]) : undefined;
        markAutoCandidateCooldown(effectiveModel.providerID, effectiveModel.modelID, errMsg, statusCode);
        reportAutoCandidateOutcome({
          providerID: effectiveModel.providerID,
          modelID: effectiveModel.modelID,
          ok: false,
          statusCode,
          message: errMsg,
        });
      }
      throw sendErr;
    }
    if (!hasRecentSessionInterrupt(id)) {
      markSessionPromptDispatched(id);
    }
    if (shouldSummarizeTitle && !explicitlySelectedImageModel) {
      void summarizeAndRenameSession(
        id,
        promptText || (effectiveSkill ? `/${effectiveSkill.name}` : t("timeline.imagePromptSessionTitle")),
        effectiveModel,
        (sessionID, title) => renameSessionTitle(sessionID, title, "automatic")
      );
    }
    setTimeout(() => {
      const targetID = id!;
      if (isSessionDeleted(targetID)) return;
      const turns = workspace.messagesBySession[targetID] ?? [];
      const lastTurn = turns[turns.length - 1];
      const hasInFlightAssistant = turns.some(
        (t) => t.info.role === "assistant" && !t.info.time?.completed && !t.info.error
      );
      // Only poll via HTTP if SSE is disconnected or the optimistic turn still
      // has not been acknowledged; otherwise an HTTP snapshot mid-stream races
      // with live SSE token deltas and triggers redundant diff/status refreshes.
      if (
        !workspace.connected ||
        !lastTurn ||
        (lastTurn.info.role === "user" &&
          !hasInFlightAssistant &&
          !isSessionBusy(targetID) &&
          lastTurn.info.id === optimisticMsgID)
      ) {
        void loadSessionMessages(targetID).catch(() => {});
      }
    }, 900);
  } catch (err) {
    if (takenQueuedEdit && id && !isSessionDeleted(id)) restoreEditingQueuedPrompt(id, takenQueuedEdit);
    // Restore the transcript and status together. A transient busy state with
    // no new user turn hides/remounts the previous footer and loses its error UI.
    batch(() => {
      setWorkspace(
        produce((state) => {
          const list = state.messagesBySession[id!];
          if (!list) return;
          const optIdx = list.findIndex((m) => m.info.id === optimisticMsgID);
          if (optIdx >= 0) list.splice(optIdx, 1);
          if (replacedFailedTurns) {
            if (options?.resumeFailedTail) {
              state.messagesBySession[id!] = replacedFailedTurns;
            } else {
              list.push(...replacedFailedTurns);
            }
          }
        })
      );
      if (!wasBusyBeforeSubmit && !isSessionDeleted(id)) {
        clearTurnDispatchState(id);
        setWorkspace("statusBySession", id, { type: "idle" });
      }
    });
    const outcomeUnknown = err instanceof Error && err.name === "RequestOutcomeUnknownError";
    if (createdSessionID && !outcomeUnknown && !hasPendingPrompt(createdSessionID)) {
      await cleanupUnusedNewSession(createdSessionID, targetDir, null);
    }
    throw err;
  }

  return id;
}


export function applyManualModelSwitchToSession(
  sessionID: string,
  model: SelectedModel,
  effort?: ReasoningEffort
): void {
  if (!sessionID) return;
  if (isSessionTurnRetrying(sessionID)) {
    void rerouteRetryingTurnToModel(sessionID, model, effort).then((rerouted) => {
      if (!rerouted && isSessionTurnRetrying(sessionID)) {
        void retryLastUserPrompt(sessionID);
      }
    });
    return;
  }
  if (isSessionBusy(sessionID)) {
    noteTurnManualModelSelection(sessionID, model, effort);
  }
}

export function applyManualEffortSwitchToSession(
  sessionID: string,
  effort: ReasoningEffort,
  model?: SelectedModel
): void {
  if (!sessionID) return;
  if (model) {
    applyManualModelSwitchToSession(sessionID, model, effort);
    return;
  }
  if (isSessionTurnRetrying(sessionID)) {
    void rerouteRetryingTurnToModel(sessionID, undefined, effort);
    return;
  }
  if (isSessionBusy(sessionID)) {
    noteTurnManualEffortSelection(sessionID, effort);
  }
}

export async function retryLastUserPrompt(sessionID?: string | null): Promise<void> {
  const id = sessionID ?? workspace.activeSessionID;
  if (!id) return;
  if (await recoverPendingPrompt(id)) {
    await loadSessionMessages(id);
    return;
  }
  const wasRetrying = isSessionTurnRetrying(id);
  if (wasRetrying) {
    if (await rerouteRetryingTurnToModel(id)) {
      return;
    }
    clearAutoFailoverPending(id);
    clearTurnDispatchState(id);
    setWorkspace("statusBySession", id, { type: "idle" });
    await hoxiAPI.abortSession(id, sessionDirectory(id)).catch(() => {});
  }
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
        const tail = turns[turns.length - 1];
        const replaceFailedTail = Boolean(
          i === turns.length - 2 &&
            tail?.info.role === "assistant" &&
            tail.info.error &&
            tail.info.error.data?.kind !== "outcome_unknown" &&
            tail.parts.length === 0
        );
        const resumeFailedTail = Boolean(
          !replaceFailedTail &&
            attachments.length === 0 &&
            !skillSelection &&
            resolveSessionTargetRuntime(id, sessionAIConfig(id).model?.providerID) === "native" &&
            canResumeReadOnlyFailedCycle(turns, i)
        );
        await dispatchSessionPrompt(text, id, {
          attachments,
          skillSelection,
          continueTask: true,
          replaceFailedTail,
          resumeFailedTail,
          ...(wasRetrying ? { forceImmediate: true } : {}),
        });
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
      fastMode: target.fastMode,
      attachments: target.attachments,
      skillSelection: target.skillSelection,
      system: target.system,
      forceImmediate: true,
      fromQueue: true,
    });
  } catch (err) {
    restoreQueuedPromptAt(sessionID, target, targetIdx);
    console.error("sendQueuedPromptNow failed:", err);
  }
}

export async function drainNextQueuedPrompt(sessionID: string): Promise<void> {
  if (!sessionID || isSessionBusy(sessionID)) return;
  if (workspace.interruptedBySession[sessionID] || isQueuedPromptDrainBlocked(sessionID)) return;

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
    if (
      isSessionBusy(sessionID) ||
      workspace.interruptedBySession[sessionID] ||
      isQueuedPromptDrainBlocked(sessionID) ||
      workspace.queuedPromptsBySession[sessionID]?.[0]?.id !== next.id
    ) {
      return;
    }
    removeQueuedPrompt(sessionID, next.id);
    try {
      await dispatchSessionPrompt(next.text, sessionID, {
        agent: next.agent,
        model: next.model,
        effort: next.effort,
        fastMode: next.fastMode,
        attachments: next.attachments,
        skillSelection: validatedSkill,
        system: next.system,
        forceImmediate: true,
        followTimeline: false,
        fromQueue: true,
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
    continueTask: true,
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
    continueTask: true,
  });
}
