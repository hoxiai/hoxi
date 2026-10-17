// web/src/store/provider-telemetry.ts
import { produce } from "solid-js/store";
import type { MessageInfo, MessagePart } from "../services/sse-types";
import { setWorkspace, workspace } from "./workspace";
import {
  emptyStats,
  initialProviderTelemetry,
  isTimeoutErrorText,
  loadProviderTelemetry,
  PROVIDER_TELEMETRY_KEY,
  pushIncident,
  pushSample,
  resetProviderTelemetry,
  schedulePersistTelemetry,
} from "./provider-telemetry-storage";

export * from "./provider-health";
export {
  initialProviderTelemetry,
  loadProviderTelemetry,
  PROVIDER_TELEMETRY_KEY,
  resetProviderTelemetry,
};

const SLOW_TTFT_THRESHOLD_MS = 22_000;

interface ActiveTurnContext {
  dispatchedAt: number;
  providerID: string;
  modelID: string;
  firstTokenRecorded: boolean;
  consecutiveToolErrors: number;
}

const activeTurnsBySession = new Map<string, ActiveTurnContext>();
const processedMessageKeys = new Set<string>();
const processedToolPartKeys = new Set<string>();

function resolveSessionProvider(sessionID: string): { providerID: string; modelID: string } | null {
  const ctx = activeTurnsBySession.get(sessionID);
  if (ctx?.providerID) {
    return { providerID: ctx.providerID, modelID: ctx.modelID };
  }
  const turns = workspace.messagesBySession[sessionID] ?? [];
  for (let i = turns.length - 1; i >= 0; i--) {
    const info = turns[i].info;
    if (info.providerID) {
      return { providerID: info.providerID, modelID: info.modelID || "" };
    }
  }
  if (workspace.selectedModel?.providerID) {
    return {
      providerID: workspace.selectedModel.providerID,
      modelID: workspace.selectedModel.modelID,
    };
  }
  return null;
}

export function recordPromptDispatched(
  sessionID: string,
  providerID?: string,
  modelID?: string
) {
  const pid = providerID || workspace.selectedModel?.providerID || "";
  const mid = modelID || workspace.selectedModel?.modelID || "";
  if (!pid) return;
  activeTurnsBySession.set(sessionID, {
    dispatchedAt: Date.now(),
    providerID: pid,
    modelID: mid,
    firstTokenRecorded: false,
    consecutiveToolErrors: 0,
  });
}

export function recordFirstToken(sessionID: string) {
  const ctx = activeTurnsBySession.get(sessionID);
  if (!ctx || ctx.firstTokenRecorded || !ctx.providerID) return;
  ctx.firstTokenRecorded = true;
  const ttftMs = Math.max(40, Date.now() - ctx.dispatchedAt);

  setWorkspace(
    produce((state) => {
      const rec = (state.providerTelemetry[ctx.providerID] ||= emptyStats(ctx.providerID));
      rec.ttftSamplesMs = pushSample(rec.ttftSamplesMs, ttftMs);
      if (ttftMs >= SLOW_TTFT_THRESHOLD_MS) {
        rec.timeoutTurns += 1;
        rec.recentIncidents = pushIncident(
          rec.recentIncidents,
          "timeout",
          `Slow TTFT (${(ttftMs / 1000).toFixed(1)}s)`,
          ctx.modelID
        );
      }
      rec.updatedAt = Date.now();
    })
  );
  schedulePersistTelemetry();
}

export function recordSessionRetry(sessionID: string, attempt: number, message?: string) {
  const resolved = resolveSessionProvider(sessionID);
  if (!resolved) return;
  const dedupKey = `retry:${sessionID}:${attempt}:${message || ""}`;
  if (processedMessageKeys.has(dedupKey)) return;
  processedMessageKeys.add(dedupKey);

  const isTimeout = isTimeoutErrorText(message || "");
  setWorkspace(
    produce((state) => {
      const rec = (state.providerTelemetry[resolved.providerID] ||= emptyStats(
        resolved.providerID
      ));
      rec.retryCount += 1;
      if (isTimeout) rec.timeoutTurns += 1;
      rec.recentIncidents = pushIncident(
        rec.recentIncidents,
        isTimeout ? "timeout" : "retry",
        message || `Retry #${attempt}`,
        resolved.modelID
      );
      rec.updatedAt = Date.now();
    })
  );
  schedulePersistTelemetry();
}

export function recordToolPartUpdate(part: MessagePart) {
  if (part.type !== "tool") return;
  const status = part.state.status;
  if (status !== "completed" && status !== "error") return;

  const dedupKey = `${part.id}:${status}`;
  if (processedToolPartKeys.has(dedupKey)) return;
  processedToolPartKeys.add(dedupKey);

  const resolved = resolveSessionProvider(part.sessionID);
  if (!resolved) return;

  const ctx = activeTurnsBySession.get(part.sessionID);
  let isHallucinationLoop = false;
  if (status === "error") {
    if (ctx) {
      ctx.consecutiveToolErrors += 1;
      if (ctx.consecutiveToolErrors >= 2) {
        isHallucinationLoop = true;
      }
    }
  } else if (ctx) {
    ctx.consecutiveToolErrors = 0;
  }

  setWorkspace(
    produce((state) => {
      const rec = (state.providerTelemetry[resolved.providerID] ||= emptyStats(
        resolved.providerID
      ));
      rec.toolCallsTotal += 1;
      if (status === "error") {
        rec.toolCallsFailed += 1;
        const errDetail = `${part.tool}: ${part.state.error || "failed"}`;
        if (isHallucinationLoop) {
          rec.hallucinationFlags += 1;
          rec.recentIncidents = pushIncident(
            rec.recentIncidents,
            "hallucination_loop",
            errDetail,
            resolved.modelID
          );
        } else {
          rec.recentIncidents = pushIncident(
            rec.recentIncidents,
            "tool_error",
            errDetail,
            resolved.modelID
          );
        }
      }
      rec.updatedAt = Date.now();
    })
  );
  schedulePersistTelemetry();
}

export function recordAssistantTurnUpdated(info: MessageInfo) {
  if (info.role !== "assistant") return;
  const pid = info.providerID || resolveSessionProvider(info.sessionID)?.providerID;
  const mid = info.modelID || resolveSessionProvider(info.sessionID)?.modelID || "";
  if (!pid) return;

  const existingCtx = activeTurnsBySession.get(info.sessionID);
  if (existingCtx) {
    existingCtx.providerID = pid;
    if (mid) existingCtx.modelID = mid;
  }

  if (info.error) {
    const errKey = `${info.id}:error`;
    if (processedMessageKeys.has(errKey)) return;
    processedMessageKeys.add(errKey);

    const errMsg = info.error.data?.message || info.error.name || "API Error";
    const statusCode = info.error.data?.statusCode;
    const isTimeout = isTimeoutErrorText(errMsg, statusCode);

    setWorkspace(
      produce((state) => {
        const rec = (state.providerTelemetry[pid] ||= emptyStats(pid));
        rec.totalTurns += 1;
        rec.errorTurns += 1;
        if (isTimeout) rec.timeoutTurns += 1;
        rec.recentIncidents = pushIncident(
          rec.recentIncidents,
          isTimeout ? "timeout" : "api_error",
          statusCode ? `HTTP ${statusCode}: ${errMsg}` : errMsg,
          mid
        );
        rec.updatedAt = Date.now();
      })
    );
    schedulePersistTelemetry();
    return;
  }

  if (info.time?.completed && info.time.completed > 0) {
    const okKey = `${info.id}:completed`;
    if (processedMessageKeys.has(okKey)) return;
    processedMessageKeys.add(okKey);

    const durationMs = Math.max(0, info.time.completed - (info.time.created || info.time.completed));
    const outTokens = info.tokens?.output ?? 0;
    const tps =
      outTokens > 3 && durationMs > 250
        ? Math.round((outTokens / (durationMs / 1000)) * 10) / 10
        : 0;
    const hasCacheHit = (info.tokens?.cache?.read ?? 0) > 0;
    const hasReasoning = (info.tokens?.reasoning ?? 0) > 0;

    setWorkspace(
      produce((state) => {
        const rec = (state.providerTelemetry[pid] ||= emptyStats(pid));
        rec.totalTurns += 1;
        rec.successTurns += 1;
        if (tps > 0 && tps < 800) {
          rec.tpsSamples = pushSample(rec.tpsSamples, tps);
        }
        if (hasCacheHit) rec.cacheHitTurns += 1;
        if (hasReasoning) rec.reasoningTurns += 1;
        rec.updatedAt = Date.now();
      })
    );
    schedulePersistTelemetry();
  }
}

export function recordUserRevertAction(sessionID: string, messageID?: string) {
  const turns = workspace.messagesBySession[sessionID] ?? [];
  let targetProviderID = "";
  let targetModelID = "";

  if (messageID) {
    const idx = turns.findIndex((t) => t.info.id === messageID);
    for (let i = idx >= 0 ? idx : turns.length - 1; i < turns.length; i++) {
      if (turns[i]?.info.role === "assistant" && turns[i].info.providerID) {
        targetProviderID = turns[i].info.providerID!;
        targetModelID = turns[i].info.modelID || "";
        break;
      }
    }
  }
  if (!targetProviderID) {
    const resolved = resolveSessionProvider(sessionID);
    if (!resolved) return;
    targetProviderID = resolved.providerID;
    targetModelID = resolved.modelID;
  }

  setWorkspace(
    produce((state) => {
      const rec = (state.providerTelemetry[targetProviderID] ||= emptyStats(targetProviderID));
      rec.revertCount += 1;
      rec.hallucinationFlags += 1;
      rec.recentIncidents = pushIncident(
        rec.recentIncidents,
        "user_revert",
        "User reverted conversation/code checkpoint",
        targetModelID
      );
      rec.updatedAt = Date.now();
    })
  );
  schedulePersistTelemetry();
}

export function recordUserAbortAction(sessionID: string) {
  const ctx = activeTurnsBySession.get(sessionID);
  if (!ctx || !ctx.providerID) return;
  const elapsedMs = Date.now() - ctx.dispatchedAt;
  if (elapsedMs < 10_000) return;

  setWorkspace(
    produce((state) => {
      const rec = (state.providerTelemetry[ctx.providerID] ||= emptyStats(ctx.providerID));
      rec.timeoutTurns += 1;
      rec.recentIncidents = pushIncident(
        rec.recentIncidents,
        "timeout",
        `Aborted after ${(elapsedMs / 1000).toFixed(0)}s stall`,
        ctx.modelID
      );
      rec.updatedAt = Date.now();
    })
  );
  schedulePersistTelemetry();
}

export function recordProbeTelemetry(
  providerID: string,
  ok: boolean,
  latencyMs?: number,
  error?: string,
  modelID?: string
) {
  const pid = providerID.trim();
  if (!pid) return;
  setWorkspace(
    produce((state) => {
      const rec = (state.providerTelemetry[pid] ||= emptyStats(pid));
      rec.lastProbeOk = ok;
      if (latencyMs && latencyMs > 0) {
        rec.lastProbeLatencyMs = latencyMs;
      }
      if (!ok && error) {
        const isTimeout = isTimeoutErrorText(error);
        if (isTimeout) rec.timeoutTurns += 1;
        rec.recentIncidents = pushIncident(
          rec.recentIncidents,
          isTimeout ? "timeout" : "api_error",
          error,
          modelID
        );
      }
      rec.updatedAt = Date.now();
    })
  );
  schedulePersistTelemetry();
}
