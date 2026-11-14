// web/src/store/session-task-control.ts
import { produce } from "solid-js/store";
import { hoxiAPI, resolveRunnerRuntimeForProvider } from "../services/api";
import type { MessagePart } from "../services/sse-types";
import { isAutoModel, resolveAutoModelTarget } from "./auto-models";
import { recordPromptDispatched, recordSessionRetry, recordUserAbortAction } from "./provider-telemetry";
import {
  canSwitchPoolCandidate,
  clearAutoFailoverPending,
  clearTurnDispatchState,
  decideAutoFailover,
  getTurnDispatchState,
  hasWarmPrefixForCandidate,
  isAutoFailoverPending,
  isPermanentRetryMessage,
  markAutoFailoverPending,
  markSessionBackendRetried,
  MAX_TURN_RETRY_ATTEMPTS,
  MAX_TURN_RETRY_DELAY_MS,
  parseQuotaResetMs,
  recordAutoFailover,
  rememberTurnDispatch,
  selectNextDispatchTarget,
  type AutoFailoverError,
  type AutoFailoverReason,
  type TurnDispatchState,
} from "./auto-failover";
import {
  setWorkspace,
  workspace,
  type ReasoningEffort,
  type SelectedModel,
} from "./workspace-state";
import { sessionAIConfig } from "./session-ai";
import { loadSessionMessages } from "./workspace-sessions";
import { isSessionBusy, isSessionDeleted, sessionDirectory } from "./session-query";
import { pauseSessionGoal } from "./session-goal-state";
import { markSessionPromptDispatched, promptDispatchSeq } from "./prompt-queue";
import { collectChildSubagentSessionIDs } from "./subagent-sessions";

const lastInterruptAtBySession = new Map<string, number>();
const rerouteGenBySession = new Map<string, number>();
const rerouteAbortInFlightBySession = new Set<string>();
const lastRerouteAbortAtBySession = new Map<string, number>();

export function isRerouteAbortInFlight(sessionID: string): boolean {
  return Boolean(sessionID && rerouteAbortInFlightBySession.has(sessionID));
}

export function hasRecentSessionInterrupt(sessionID: string): boolean {
  return lastInterruptAtBySession.has(sessionID);
}

export function forgetSessionTaskControl(sessionID: string): void {
  if (!sessionID) return;
  lastInterruptAtBySession.delete(sessionID);
  rerouteGenBySession.delete(sessionID);
  rerouteAbortInFlightBySession.delete(sessionID);
  lastRerouteAbortAtBySession.delete(sessionID);
}

export function markSessionInterrupted(
  sessionID: string,
  kind: "paused" | "stopped" = "stopped"
) {
  if (!sessionID) return;
  rerouteGenBySession.delete(sessionID);
  rerouteAbortInFlightBySession.delete(sessionID);
  lastRerouteAbortAtBySession.delete(sessionID);
  clearAutoFailoverPending(sessionID);
  clearTurnDispatchState(sessionID);
  pauseSessionGoal(sessionID, "stopped");
  lastInterruptAtBySession.set(sessionID, Date.now());
  setWorkspace("interruptedBySession", sessionID, kind);
  setWorkspace("statusBySession", sessionID, { type: "idle" });
}

export function clearSessionInterrupted(sessionID: string) {
  if (!sessionID) return;
  rerouteGenBySession.delete(sessionID);
  rerouteAbortInFlightBySession.delete(sessionID);
  lastRerouteAbortAtBySession.delete(sessionID);
  lastInterruptAtBySession.delete(sessionID);
  setWorkspace("interruptedBySession", sessionID, undefined);
}

export async function rerouteRetryingTurnToModel(
  sessionID: string,
  model?: SelectedModel | null,
  effort?: ReasoningEffort
): Promise<boolean> {
  if (!sessionID || isSessionDeleted(sessionID) || workspace.archivedSessionIDs.includes(sessionID)) {
    return false;
  }
  const prev = getTurnDispatchState(sessionID);
  if (!prev) return false;
  const cfg = sessionAIConfig(sessionID);
  const chosen = model ?? prev.manualModel ?? cfg.model;
  if (!chosen?.providerID || !chosen?.modelID) return false;
  const nextEffort =
    effort !== undefined
      ? effort
      : prev.manualEffort !== undefined
        ? prev.manualEffort
        : cfg.effort ?? prev.effort;
  const hasImage = (prev.attachments ?? []).some(
    (a) => (a.mime || "").toLowerCase().startsWith("image/") || /^data:image\//i.test(a.url || "")
  );
  const usedAutoPool = isAutoModel(chosen);
  const resolved = usedAutoPool
    ? resolveAutoModelTarget(chosen, {
        advance: true,
        requireImage: hasImage,
        sessionID,
      })
    : null;
  const effectiveModel = resolved ? { ...resolved } : { ...chosen };
  const gen = (rerouteGenBySession.get(sessionID) ?? 0) + 1;
  rerouteGenBySession.set(sessionID, gen);

  const nextState: TurnDispatchState = {
    poolID: usedAutoPool ? chosen.modelID : "",
    target: effectiveModel,
    promptText: prev.promptText,
    agent: prev.agent,
    effort: nextEffort || undefined,
    attachments: prev.attachments,
    skillSelection: prev.skillSelection,
    system: prev.system,
    triedKeys: new Set([`${effectiveModel.providerID}/${effectiveModel.modelID}`]),
    sameModelRetries: 0,
    backendRetried: false,
    retrying: true,
    priorTailID: prev.priorTailID,
    dispatchedAt: Date.now(),
  };
  rememberTurnDispatch(sessionID, nextState);
  markAutoFailoverPending(sessionID);
  markSessionPromptDispatched(sessionID);
  recordPromptDispatched(sessionID, effectiveModel.providerID, effectiveModel.modelID);
  setWorkspace("statusBySession", sessionID, { type: "busy" });

  const dir = sessionDirectory(sessionID);
  rerouteAbortInFlightBySession.add(sessionID);
  try {
    await hoxiAPI.abortSession(sessionID, dir);
  } catch {
    /* ignore abort errors when no active process is running */
  } finally {
    if (rerouteGenBySession.get(sessionID) === gen) {
      rerouteAbortInFlightBySession.delete(sessionID);
      lastRerouteAbortAtBySession.set(sessionID, Date.now());
    }
  }

  if (
    rerouteGenBySession.get(sessionID) !== gen ||
    getTurnDispatchState(sessionID) !== nextState ||
    isSessionDeleted(sessionID) ||
    workspace.archivedSessionIDs.includes(sessionID) ||
    Boolean(workspace.interruptedBySession[sessionID])
  ) {
    return true;
  }

  markAutoFailoverPending(sessionID);
  markSessionPromptDispatched(sessionID);
  setWorkspace("statusBySession", sessionID, { type: "busy" });
  try {
    await hoxiAPI.sendPromptAsync(sessionID, nextState.promptText, {
      continueTask: true,
      agent: nextState.agent,
      model: effectiveModel,
      variant: nextState.effort || undefined,
      directory: dir,
      attachments: nextState.attachments,
      skillSelection: nextState.skillSelection,
      system: nextState.system,
      runtime: resolveRunnerRuntimeForProvider(
        effectiveModel.providerID,
        Boolean(workspace.hoxiProviders?.[effectiveModel.providerID || ""])
      ),
    });
    if (
      rerouteGenBySession.get(sessionID) === gen &&
      getTurnDispatchState(sessionID) === nextState &&
      !workspace.interruptedBySession[sessionID]
    ) {
      markSessionPromptDispatched(sessionID);
    }
  } catch (sendErr) {
    if (
      rerouteGenBySession.get(sessionID) !== gen ||
      getTurnDispatchState(sessionID) !== nextState ||
      Boolean(workspace.interruptedBySession[sessionID])
    ) {
      return true;
    }
    const errMsg = sendErr instanceof Error ? sendErr.message : String(sendErr || "error");
    const statusMatch = errMsg.match(/\bHTTP (\d{3})\b/);
    await handleSessionErrorEvent(sessionID, {
      name: "APIError",
      data: { message: errMsg, statusCode: statusMatch ? Number(statusMatch[1]) : undefined },
    });
  }
  return true;
}

async function cutoffSessionRetry(
  sessionID: string,
  retryError: AutoFailoverError
): Promise<void> {
  const gen = (rerouteGenBySession.get(sessionID) ?? 0) + 1;
  rerouteGenBySession.set(sessionID, gen);
  const dir = sessionDirectory(sessionID);
  rerouteAbortInFlightBySession.add(sessionID);
  try {
    await hoxiAPI.abortSession(sessionID, dir);
  } catch {
    /* ignore abort errors when no active process is running */
  } finally {
    if (rerouteGenBySession.get(sessionID) === gen) {
      rerouteAbortInFlightBySession.delete(sessionID);
      lastRerouteAbortAtBySession.set(sessionID, Date.now());
    }
  }
  if (
    rerouteGenBySession.get(sessionID) !== gen ||
    isSessionDeleted(sessionID) ||
    workspace.archivedSessionIDs.includes(sessionID) ||
    Boolean(workspace.interruptedBySession[sessionID])
  ) {
    return;
  }
  await handleSessionErrorEvent(sessionID, retryError);
}

/**
 * Inspects an incoming `session.status` `{ type: "retry" }` event and reports
 * whether the retry was intercepted (to cut off an excessive countdown, abort a
 * permanent failure, reroute a manual model selection, or fail over immediately
 * to the next `hoxi-auto` candidate).
 */
export function handleSessionRetryStatus(
  sessionID: string,
  status: { type: "retry"; attempt: number; message: string; next: number }
): boolean {
  if (!sessionID) return false;
  markSessionBackendRetried(sessionID);
  const autoState = getTurnDispatchState(sessionID);
  if (autoState?.manualModel) {
    void rerouteRetryingTurnToModel(sessionID);
    return true;
  }
  const now = Date.now();
  const waitMs = Math.max(0, (status.next || 0) - now);
  const statedResetMs = parseQuotaResetMs(status.message || "", now) ?? 0;
  const effectiveWaitMs = Math.max(waitMs, statedResetMs);
  const attempt = status.attempt || 1;
  const retryError: AutoFailoverError = {
    name: "APIError",
    data: {
      message: status.message || "Model request failed after retry limit",
      isRetryable:
        effectiveWaitMs <= MAX_TURN_RETRY_DELAY_MS && attempt <= MAX_TURN_RETRY_ATTEMPTS,
      retryAfterMs: effectiveWaitMs > 0 ? effectiveWaitMs : undefined,
    },
  };
  const exceedsRetryBounds =
    effectiveWaitMs > MAX_TURN_RETRY_DELAY_MS ||
    attempt > MAX_TURN_RETRY_ATTEMPTS ||
    isPermanentRetryMessage(status.message || "", effectiveWaitMs);
  let shouldFailoverNow = false;
  if (!exceedsRetryBounds && autoState?.poolID && canSwitchPoolCandidate(sessionID, autoState)) {
    const decision = decideAutoFailover(retryError, now - autoState.dispatchedAt);
    if (
      decision.failover &&
      (decision.reason === "rateLimit" ||
        decision.reason === "quota" ||
        decision.reason === "auth" ||
        decision.reason === "model")
    ) {
      shouldFailoverNow = true;
    } else if (
      decision.failover &&
      (decision.reason === "server" || decision.reason === "network") &&
      (attempt >= 2 || !hasWarmPrefixForCandidate(sessionID, autoState.target))
    ) {
      shouldFailoverNow = true;
    }
  }
  if (!exceedsRetryBounds && !shouldFailoverNow) {
    return false;
  }
  if (autoState?.poolID) {
    markAutoFailoverPending(sessionID);
  }
  void cutoffSessionRetry(sessionID, retryError);
  return true;
}

export async function handleSessionErrorEvent(
  sessionID: string,
  error?: {
    name: string;
    data?: {
      message?: string;
      statusCode?: number;
      isRetryable?: boolean;
      kind?: string;
      retryAfterMs?: number;
    };
  }
): Promise<void> {
  if (!sessionID) return;
  if (rerouteAbortInFlightBySession.has(sessionID)) return;
  const lowerErrName = (error?.name || "").toLowerCase();
  const lowerErrMsg = `${error?.data?.message || ""}`.toLowerCase();
  const isAbortErr =
    error?.data?.kind === "aborted" ||
    lowerErrName === "messageabortederror" ||
    /\baborted?\b/.test(lowerErrName) ||
    (/\baborted?\b/.test(lowerErrMsg) && !/connection|network|socket|tcp/.test(lowerErrMsg));
  const lastRerouteAbortAt = lastRerouteAbortAtBySession.get(sessionID) ?? 0;
  if (
    isAbortErr &&
    !workspace.interruptedBySession[sessionID] &&
    (isAutoFailoverPending(sessionID) || Date.now() - lastRerouteAbortAt < 5_000)
  ) {
    return;
  }
  const autoState = getTurnDispatchState(sessionID);
  let currentError = error;
  const initialFailed = autoState ? { ...autoState.target } : undefined;
  let initialReason: AutoFailoverReason | undefined;
  let initialCooldownUntil: number | undefined;
  const isGone = () =>
    isSessionDeleted(sessionID) || workspace.archivedSessionIDs.includes(sessionID);
  const isSuperseded = () => {
    const cur = getTurnDispatchState(sessionID);
    return cur !== undefined && cur !== autoState;
  };
  const isStoppedOrSuperseded = () =>
    isGone() || Boolean(workspace.interruptedBySession[sessionID]) || isSuperseded();

  while (
    autoState &&
    currentError &&
    currentError.name !== "RunnerStalledError" &&
    !isStoppedOrSuperseded()
  ) {
    const failed = { ...autoState.target };
    const nextStep = selectNextDispatchTarget(sessionID, currentError, autoState);
    if (!nextStep) break;
    if (!initialReason) {
      initialReason = nextStep.reason;
      initialCooldownUntil = nextStep.cooldownUntil;
    }
    if (!nextStep.switchedPoolCandidate) {
      recordSessionRetry(sessionID, autoState.sameModelRetries, nextStep.errText);
    }
    markAutoFailoverPending(sessionID);
    markSessionPromptDispatched(sessionID);
    recordPromptDispatched(sessionID, nextStep.target.providerID, nextStep.target.modelID);
    setWorkspace("statusBySession", sessionID, { type: "busy" });
    try {
      await hoxiAPI.sendPromptAsync(sessionID, autoState.promptText, {
        continueTask: true,
        agent: autoState.agent,
        model: nextStep.target,
        variant: autoState.effort || undefined,
        directory: sessionDirectory(sessionID),
        attachments: autoState.attachments,
        skillSelection: autoState.skillSelection,
        system: autoState.system,
        runtime: resolveRunnerRuntimeForProvider(
          nextStep.target.providerID,
          Boolean(workspace.hoxiProviders?.[nextStep.target.providerID || ""])
        ),
      });
      if (isStoppedOrSuperseded()) {
        if (!isSuperseded()) {
          clearAutoFailoverPending(sessionID);
        }
        if (isGone() || workspace.interruptedBySession[sessionID]) {
          setWorkspace("statusBySession", sessionID, { type: "idle" });
          void hoxiAPI.abortSession(sessionID, sessionDirectory(sessionID)).catch(() => {});
        }
        return;
      }
      if (nextStep.switchedPoolCandidate) {
        recordAutoFailover({
          sessionID,
          from: initialFailed ?? failed,
          to: { ...nextStep.target },
          reason: initialReason ?? nextStep.reason,
          at: Date.now(),
          cooldownUntil: initialCooldownUntil,
        });
      }
      markSessionPromptDispatched(sessionID);
      return;
    } catch (sendErr) {
      if (isStoppedOrSuperseded()) break;
      const errMsg = sendErr instanceof Error ? sendErr.message : String(sendErr || "error");
      const statusMatch = errMsg.match(/\bHTTP (\d{3})\b/);
      currentError = {
        name: "APIError",
        data: { message: errMsg, statusCode: statusMatch ? Number(statusMatch[1]) : undefined },
      };
    }
  }
  if (isSuperseded()) {
    return;
  }
  clearAutoFailoverPending(sessionID);
  clearTurnDispatchState(sessionID);
  if (isGone() || workspace.interruptedBySession[sessionID]) {
    return;
  }
  pauseSessionGoal(sessionID, "error");
  lastInterruptAtBySession.set(sessionID, Date.now());
  setWorkspace("statusBySession", sessionID, { type: "idle" });
  setWorkspace("unreadCompletedBySession", sessionID, false);
  const settledDispatchSeq = promptDispatchSeq(sessionID);
  await loadSessionMessages(sessionID);
  if (
    !currentError ||
    isGone() ||
    workspace.interruptedBySession[sessionID] ||
    isSuperseded() ||
    isSessionBusy(sessionID) ||
    promptDispatchSeq(sessionID) !== settledDispatchSeq
  ) {
    return;
  }
  setWorkspace(
    produce((state) => {
      const list = (state.messagesBySession[sessionID] ||= []);
      const priorIdx = autoState?.priorTailID
        ? list.findIndex((m) => m.info.id === autoState.priorTailID)
        : -1;
      const isTurnFromDispatch = (
        m: { info: { id: string; time?: { created?: number } } },
        idx: number
      ): boolean => {
        if (!autoState) return true;
        if (m.info.id.startsWith("optimistic_")) return true;
        if (priorIdx >= 0) return idx > priorIdx;
        return (m.info.time?.created ?? 0) >= autoState.dispatchedAt;
      };
      const hasDispatchTurn = list.some((m, idx) => isTurnFromDispatch(m, idx));
      const atts = autoState?.attachments ?? [];
      if (
        autoState &&
        !hasDispatchTurn &&
        (autoState.promptText || atts.length > 0 || autoState.skillSelection)
      ) {
        const prevCreated = list[list.length - 1]?.info.time?.created ?? 0;
        const userCreated = Math.max(autoState.dispatchedAt, prevCreated + 1);
        const recoveredMsgID = `optimistic_${userCreated}`;
        const recoveredParts: MessagePart[] = atts.map((att, idx) => ({
          id: `prt_file_${userCreated}_${idx}`,
          sessionID,
          messageID: recoveredMsgID,
          type: "file",
          mime: att.mime,
          filename: att.filename,
          url: att.url,
        }));
        if (autoState.promptText || autoState.skillSelection || recoveredParts.length === 0) {
          recoveredParts.push({
            id: `prt_${userCreated}`,
            sessionID,
            messageID: recoveredMsgID,
            type: "text",
            text: autoState.promptText,
            ...(autoState.skillSelection
              ? { metadata: { hoxiSkill: { ...autoState.skillSelection } } }
              : {}),
          });
        }
        list.push({
          info: {
            id: recoveredMsgID,
            sessionID,
            role: "user",
            time: { created: userCreated },
            agent: autoState.agent,
            modelID: autoState.target.modelID,
            providerID: autoState.target.providerID,
          },
          parts: recoveredParts,
        });
      }
      const lastIdx = list.length - 1;
      const last = lastIdx >= 0 ? list[lastIdx] : undefined;
      if (last?.info.role === "assistant" && isTurnFromDispatch(last, lastIdx)) {
        // A runner stop (watchdog, repeated invalid tool calls, or retry cutoff) explains the engine's own "aborted" error.
        const existingErrName = (last.info.error?.name || "").toLowerCase();
        const existingIsAbort =
          existingErrName === "messageabortederror" || /\baborted?\b/.test(existingErrName);
        if (
          currentError.name === "RunnerStalledError" ||
          currentError.name === "RepeatedToolFailureError" ||
          existingIsAbort
        ) {
          last.info.error = currentError;
        } else {
          last.info.error ||= currentError;
        }
        return;
      }
      const now = Math.max(Date.now(), (last?.info.time?.created ?? 0) + 1);
      list.push({
        info: {
          id: `msg_err_${now}`,
          sessionID,
          role: "assistant",
          time: { created: now, completed: now },
          modelID: autoState?.target.modelID ?? sessionAIConfig(sessionID).model?.modelID,
          providerID: autoState?.target.providerID ?? sessionAIConfig(sessionID).model?.providerID,
          error: currentError,
        },
        parts: [],
      });
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
  void loadSessionMessages(id).catch(() => {});
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
  void loadSessionMessages(id).catch(() => {});
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
