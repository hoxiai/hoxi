// web/src/store/session-task-control.ts
import { produce } from "solid-js/store";
import { hoxiAPI, resolveRunnerRuntimeForProvider } from "../services/api";
import { recordPromptDispatched, recordSessionRetry, recordUserAbortAction } from "./provider-telemetry";
import {
  clearAutoFailoverPending,
  clearTurnDispatchState,
  getTurnDispatchState,
  markAutoFailoverPending,
  recordAutoFailover,
  selectNextDispatchTarget,
  type AutoFailoverReason,
} from "./auto-failover";
import { setWorkspace, workspace } from "./workspace-state";
import { sessionAIConfig } from "./session-ai";
import { loadSessionMessages } from "./workspace-sessions";
import { isSessionBusy, isSessionDeleted, sessionDirectory } from "./session-query";
import { pauseSessionGoal } from "./session-goal-state";
import { markSessionPromptDispatched } from "./prompt-queue";
import { collectChildSubagentSessionIDs } from "./subagent-sessions";

const lastInterruptAtBySession = new Map<string, number>();

export function hasRecentSessionInterrupt(sessionID: string): boolean {
  return lastInterruptAtBySession.has(sessionID);
}

export function markSessionInterrupted(
  sessionID: string,
  kind: "paused" | "stopped" = "stopped"
) {
  if (!sessionID) return;
  clearAutoFailoverPending(sessionID);
  clearTurnDispatchState(sessionID);
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
  const autoState = getTurnDispatchState(sessionID);
  let currentError = error;
  const initialFailed = autoState ? { ...autoState.target } : undefined;
  let initialReason: AutoFailoverReason | undefined;
  let initialCooldownUntil: number | undefined;
  const isGone = () =>
    isSessionDeleted(sessionID) || workspace.archivedSessionIDs.includes(sessionID);
  const isSuperseded = () =>
    autoState !== undefined && getTurnDispatchState(sessionID) !== autoState;
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
        clearAutoFailoverPending(sessionID);
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
  clearAutoFailoverPending(sessionID);
  if (isGone() || workspace.interruptedBySession[sessionID] || (isSuperseded() && isSessionBusy(sessionID))) {
    return;
  }
  pauseSessionGoal(sessionID, "error");
  lastInterruptAtBySession.set(sessionID, Date.now());
  setWorkspace("statusBySession", sessionID, { type: "idle" });
  setWorkspace("unreadCompletedBySession", sessionID, false);
  await loadSessionMessages(sessionID);
  if (
    !currentError ||
    isGone() ||
    workspace.interruptedBySession[sessionID] ||
    (isSuperseded() && isSessionBusy(sessionID))
  ) {
    return;
  }
  setWorkspace(
    produce((state) => {
      const list = (state.messagesBySession[sessionID] ||= []);
      const last = list[list.length - 1];
      if (last?.info.role === "assistant") {
        // A runner stop (watchdog, repeated invalid tool calls) explains the engine's own "aborted" error.
        if (currentError.name === "RunnerStalledError" || currentError.name === "RepeatedToolFailureError") last.info.error = currentError;
        else last.info.error ||= currentError;
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
