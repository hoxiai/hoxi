// web/src/store/provider-telemetry.ts
import { produce } from "solid-js/store";
import {
  hoxiAPI,
  type ModelTelemetryStats,
  type ProviderIncident,
  type ProviderIncidentType,
  type ProviderTelemetryStats,
} from "../services/api";
import type { MessageInfo, MessagePart } from "../services/sse-types";
import { PROVIDER_TELEMETRY_KEY, setWorkspace, workspace } from "./workspace-state";

const MAX_SAMPLES = 30;
const MAX_INCIDENTS = 10;
let syncDebounceTimer: ReturnType<typeof setTimeout> | null = null;

export function emptyStats(providerID: string): ProviderTelemetryStats {
  return {
    providerID,
    totalTurns: 0,
    successTurns: 0,
    errorTurns: 0,
    timeoutTurns: 0,
    retryCount: 0,
    toolCallsTotal: 0,
    toolCallsFailed: 0,
    hallucinationFlags: 0,
    revertCount: 0,
    cacheHitTurns: 0,
    reasoningTurns: 0,
    auditTotalCount: 0,
    auditPassCount: 0,
    auditScores: [],
    ttftSamplesMs: [],
    tpsSamples: [],
    models: {},
    recentIncidents: [],
    updatedAt: Date.now(),
  };
}

export function emptyModelStats(modelID: string): ModelTelemetryStats {
  return {
    modelID,
    totalTurns: 0,
    successTurns: 0,
    errorTurns: 0,
    timeoutTurns: 0,
    retryCount: 0,
    toolCallsTotal: 0,
    toolCallsFailed: 0,
    hallucinationFlags: 0,
    revertCount: 0,
    cacheHitTurns: 0,
    reasoningTurns: 0,
    auditTotalCount: 0,
    auditPassCount: 0,
    auditScores: [],
    ttftSamplesMs: [],
    tpsSamples: [],
    updatedAt: Date.now(),
  };
}

export function ensureModelStats(
  rec: ProviderTelemetryStats,
  modelID?: string
): ModelTelemetryStats | null {
  const mid = (modelID || "").trim();
  if (!mid) return null;
  if (!rec.models) {
    rec.models = {};
  }
  if (!rec.models[mid]) {
    rec.models[mid] = emptyModelStats(mid);
  }
  return rec.models[mid];
}

export function schedulePersistTelemetry() {
  if (syncDebounceTimer) clearTimeout(syncDebounceTimer);
  syncDebounceTimer = setTimeout(() => {
    syncDebounceTimer = null;
    persistLocalTelemetry();
    void hoxiAPI.saveProviderTelemetry(workspace.providerTelemetry).catch(() => {});
  }, 600);
}

function persistLocalTelemetry() {
  try {
    localStorage.setItem(PROVIDER_TELEMETRY_KEY, JSON.stringify(workspace.providerTelemetry));
  } catch {
    /* ignore */
  }
}

if (typeof window !== "undefined") {
  window.addEventListener("pagehide", () => {
    if (syncDebounceTimer !== null) persistLocalTelemetry();
  });
}

export async function loadProviderTelemetry(): Promise<void> {
  try {
    const res = await hoxiAPI.getProviderTelemetry();
    const remote = res?.providers ?? {};
    setWorkspace(
      produce((state) => {
        for (const [pid, remoteRec] of Object.entries(remote)) {
          if (!remoteRec || typeof remoteRec !== "object") continue;
          const localRec = state.providerTelemetry[pid];
          if (!localRec || (remoteRec.updatedAt ?? 0) >= (localRec.updatedAt ?? 0)) {
            state.providerTelemetry[pid] = {
              ...emptyStats(pid),
              ...remoteRec,
              providerID: pid,
            };
          }
        }
      })
    );
  } catch {
    /* ignore when offline */
  }
}

export async function resetProviderTelemetry(providerID?: string): Promise<void> {
  setWorkspace(
    produce((state) => {
      if (providerID) {
        delete state.providerTelemetry[providerID];
      } else {
        state.providerTelemetry = {};
      }
    })
  );
  try {
    localStorage.setItem(
      PROVIDER_TELEMETRY_KEY,
      JSON.stringify(workspace.providerTelemetry)
    );
  } catch {
    /* ignore */
  }
  await hoxiAPI.clearProviderTelemetry(providerID).catch(() => {});
}

export function pushSample(list: number[] | undefined, value: number, allowZero = false): number[] {
  const arr = Array.isArray(list) ? list.slice() : [];
  if ((value > 0 || (allowZero && value === 0)) && Number.isFinite(value)) {
    arr.push(value);
  }
  return arr.length > MAX_SAMPLES ? arr.slice(arr.length - MAX_SAMPLES) : arr;
}

export function pushIncident(
  list: ProviderIncident[] | undefined,
  type: ProviderIncidentType,
  detail: string,
  modelID?: string
): ProviderIncident[] {
  const arr = Array.isArray(list) ? list.slice() : [];
  arr.unshift({
    timestamp: Date.now(),
    type,
    modelID,
    detail: detail.slice(0, 160),
  });
  return arr.slice(0, MAX_INCIDENTS);
}

export function isTimeoutErrorText(text: string, statusCode?: number): boolean {
  if (statusCode && [408, 502, 504, 522, 524].includes(statusCode)) return true;
  const lower = text.toLowerCase();
  return (
    lower.includes("timeout") ||
    lower.includes("timed out") ||
    lower.includes("etimedout") ||
    lower.includes("deadline") ||
    lower.includes("504") ||
    lower.includes("502") ||
    lower.includes("stall") ||
    lower.includes("socket hang up")
  );
}

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
      const m = ensureModelStats(rec, ctx.modelID);
      if (m) {
        m.ttftSamplesMs = pushSample(m.ttftSamplesMs, ttftMs);
      }
      if (ttftMs >= SLOW_TTFT_THRESHOLD_MS) {
        rec.timeoutTurns += 1;
        if (m) m.timeoutTurns += 1;
        rec.recentIncidents = pushIncident(
          rec.recentIncidents,
          "timeout",
          `Slow TTFT (${(ttftMs / 1000).toFixed(1)}s)`,
          ctx.modelID
        );
      }
      rec.updatedAt = Date.now();
      if (m) m.updatedAt = rec.updatedAt;
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
      const m = ensureModelStats(rec, resolved.modelID);
      if (m) {
        m.retryCount += 1;
        if (isTimeout) m.timeoutTurns += 1;
        m.updatedAt = Date.now();
      }
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
      const m = ensureModelStats(rec, resolved.modelID);
      if (m) {
        m.toolCallsTotal += 1;
      }
      if (status === "error") {
        rec.toolCallsFailed += 1;
        if (m) m.toolCallsFailed += 1;
        const errDetail = `${part.tool}: ${part.state.error || "failed"}`;
        if (isHallucinationLoop) {
          rec.hallucinationFlags += 1;
          if (m) m.hallucinationFlags += 1;
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
      if (m) m.updatedAt = rec.updatedAt;
    })
  );
  schedulePersistTelemetry();
}

export function isUserAbortError(info?: MessageInfo | null): boolean {
  if (!info?.error) return false;
  const name = (info.error.name || "").toLowerCase();
  const msg = (info.error.data?.message || "").toLowerCase();
  return name.includes("abort") || msg.includes("aborted") || msg.includes("abort");
}

/** The runner watchdog stopped a silent turn; not a provider or model failure. */
export function isRunnerStallError(info?: MessageInfo | null): boolean {
  return info?.error?.name === "RunnerStalledError";
}

/**
 * The running engine does not know this provider/model (usually a stale model
 * catalog after models.json changed). Switching model or refreshing the catalog
 * is the only way forward; retrying the same request cannot help.
 */
export function isModelUnavailableError(info?: MessageInfo | null): boolean {
  if (!info?.error) return false;
  const name = (info.error.name || "").toLowerCase();
  const msg = (info.error.data?.message || "").toLowerCase();
  return (
    name === "providermodelnotfounderror" ||
    msg.includes("model not found") ||
    msg.includes("is not loaded in the running engine")
  );
}

/**
 * The model rejected the request because the conversation no longer fits its
 * window. Retrying the same prompt cannot help: the session has to be compacted
 * (or branched) first, so the error card offers compaction instead of a retry.
 */
export function isContextOverflowError(info?: MessageInfo | null): boolean {
  const name = (info?.error?.name || "").toLowerCase();
  const msg = (info?.error?.data?.message || "").toLowerCase();
  return (
    name === "contextoverflowerror" ||
    name === "messageoutputlengtherror" ||
    msg.includes("context limit") ||
    msg.includes("context length") ||
    msg.includes("context window") ||
    msg.includes("maximum context") ||
    msg.includes("too many tokens") ||
    msg.includes("prompt is too long") ||
    msg.includes("reduce the length")
  );
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
    if (isUserAbortError(info) || isRunnerStallError(info)) return;
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
        const m = ensureModelStats(rec, mid);
        if (m) {
          m.totalTurns += 1;
          m.errorTurns += 1;
          if (isTimeout) m.timeoutTurns += 1;
          m.updatedAt = Date.now();
        }
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
        if (hasReasoning) rec.reasoningTurns = (rec.reasoningTurns || 0) + 1;
        const m = ensureModelStats(rec, mid);
        if (m) {
          m.totalTurns += 1;
          m.successTurns += 1;
          if (tps > 0 && tps < 800) {
            m.tpsSamples = pushSample(m.tpsSamples, tps);
          }
          if (hasCacheHit) m.cacheHitTurns += 1;
          if (hasReasoning) m.reasoningTurns = (m.reasoningTurns || 0) + 1;
          m.updatedAt = Date.now();
        }
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
      const m = ensureModelStats(rec, targetModelID);
      if (m) {
        m.revertCount += 1;
        m.hallucinationFlags += 1;
        m.updatedAt = Date.now();
      }
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
      const m = ensureModelStats(rec, ctx.modelID);
      if (m) {
        m.timeoutTurns += 1;
        m.updatedAt = Date.now();
      }
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
      const now = Date.now();
      rec.lastProbeOk = ok;
      if (latencyMs && latencyMs > 0) {
        rec.lastProbeLatencyMs = latencyMs;
      }
      const m = ensureModelStats(rec, modelID);
      if (m) {
        m.lastProbeOk = ok;
        m.lastProbeLatencyMs = latencyMs && latencyMs > 0 ? latencyMs : undefined;
        m.updatedAt = now;
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
      rec.updatedAt = now;
    })
  );
  schedulePersistTelemetry();
}

export type ProviderModelProbeInput = Parameters<typeof hoxiAPI.testProviderModel>[0];

/** Sends a one-shot probe request to a provider model and records it in provider telemetry. */
export async function probeProviderModel(
  input: ProviderModelProbeInput
): Promise<{ ok: boolean; latencyMs?: number; error?: string }> {
  const pid = input.providerID;
  try {
    const res = await hoxiAPI.testProviderModel(input);
    if (pid) recordProbeTelemetry(pid, res.ok, res.latencyMs, res.error, input.modelID);
    return res;
  } catch (err: any) {
    if (pid) recordProbeTelemetry(pid, false, undefined, err?.message || String(err), input.modelID);
    throw err;
  }
}
