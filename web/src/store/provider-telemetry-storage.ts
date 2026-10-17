// web/src/store/provider-telemetry-storage.ts
import { produce } from "solid-js/store";
import {
  hoxiAPI,
  type ModelTelemetryStats,
  type ProviderIncident,
  type ProviderIncidentType,
  type ProviderTelemetryStats,
} from "../services/api";
import { setWorkspace, workspace } from "./workspace";

export const PROVIDER_TELEMETRY_KEY = "hoxi-provider-telemetry:v2";

const MAX_SAMPLES = 30;
const MAX_INCIDENTS = 10;
let syncDebounceTimer: ReturnType<typeof setTimeout> | null = null;

export function initialProviderTelemetry(): Record<string, ProviderTelemetryStats> {
  try {
    const raw = localStorage.getItem(PROVIDER_TELEMETRY_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed;
    }
  } catch {
    /* ignore */
  }
  return {};
}

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
  try {
    localStorage.setItem(
      PROVIDER_TELEMETRY_KEY,
      JSON.stringify(workspace.providerTelemetry)
    );
  } catch {
    /* ignore */
  }
  if (syncDebounceTimer) clearTimeout(syncDebounceTimer);
  syncDebounceTimer = setTimeout(() => {
    syncDebounceTimer = null;
    void hoxiAPI.saveProviderTelemetry(workspace.providerTelemetry).catch(() => {});
  }, 600);
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

export function pushSample(list: number[] | undefined, value: number): number[] {
  const arr = Array.isArray(list) ? list.slice() : [];
  if (value > 0 && Number.isFinite(value)) {
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
