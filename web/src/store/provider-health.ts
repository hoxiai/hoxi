// web/src/store/provider-health.ts
import type { ModelTelemetryStats, ProviderIncident, ProviderTelemetryStats } from "../services/api";
import { LOCAL_CLI_RUNTIME_PROVIDERS } from "./provider-catalog";
import { workspace, type SelectedModel } from "./workspace-state";

export type ProviderRiskTag =
  | "timeout_prone"
  | "slow_ttft"
  | "high_hallucination"
  | "tool_instability"
  | "frequent_retry";

export interface ProviderHealthSummary {
  providerID: string;
  modelID?: string;
  stabilityScore: number | null;
  status: "untested" | "healthy" | "warning" | "degraded";
  totalTurns: number;
  successRate: number;
  toolSuccessRate: number | null;
  timeoutRate: number;
  avgTtftMs: number | null;
  avgTps: number | null;
  cacheHitRate: number | null;
  hallucinationFlags: number;
  revertCount: number;
  retryCount: number;
  lastProbeOk?: boolean;
  lastProbeLatencyMs?: number;
  riskTags: ProviderRiskTag[];
  recentIncidents: ProviderIncident[];
}

function emptyUntestedSummary(providerID: string, modelID?: string): ProviderHealthSummary {
  return {
    providerID,
    modelID,
    stabilityScore: null,
    status: "untested",
    totalTurns: 0,
    successRate: 100,
    toolSuccessRate: null,
    timeoutRate: 0,
    avgTtftMs: null,
    avgTps: null,
    cacheHitRate: null,
    hallucinationFlags: 0,
    revertCount: 0,
    retryCount: 0,
    riskTags: [],
    recentIncidents: [],
  };
}

/**
 * Execution success measures provider stability.
 * Missing evidence stays unrated instead of receiving a synthetic 100 or zero.
 */
export function computeProviderHealth(
  providerID: string,
  modelID?: string
): ProviderHealthSummary {
  const providerRec = workspace.providerTelemetry[providerID];
  if (!providerRec) {
    return emptyUntestedSummary(providerID, modelID);
  }

  const trimmedModel = (modelID || "").trim();
  const target: ModelTelemetryStats | ProviderTelemetryStats | undefined = trimmedModel
    ? providerRec.models?.[trimmedModel]
    : providerRec;

  const totalTurns = target?.totalTurns ?? 0;

  // Strictly require actual execution turns before showing any score
  if (!target || totalTurns === 0) {
    return emptyUntestedSummary(providerID, trimmedModel || undefined);
  }

  const turns = Math.max(1, totalTurns);
  const successRate =
    totalTurns > 0 ? Math.max(0, Math.min(100, Math.round((target.successTurns / totalTurns) * 100))) : 100;
  const stabilityScore = totalTurns > 0 ? successRate : null;

  const toolSuccessRate =
    target.toolCallsTotal > 0
      ? Math.round(
          ((target.toolCallsTotal - target.toolCallsFailed) / target.toolCallsTotal) * 100
        )
      : null;

  const timeoutRate = Math.min(100, Math.round((target.timeoutTurns / turns) * 100));
  const cacheHitRate =
    target.successTurns > 0
      ? Math.round((target.cacheHitTurns / target.successTurns) * 100)
      : null;

  const ttftList = target.ttftSamplesMs ?? [];
  const avgTtftMs =
    ttftList.length > 0
      ? Math.round(ttftList.reduce((a, b) => a + b, 0) / ttftList.length)
      : null;

  const tpsList = target.tpsSamples ?? [];
  const avgTps =
    tpsList.length > 0
      ? Math.round((tpsList.reduce((a, b) => a + b, 0) / tpsList.length) * 10) / 10
      : null;

  const timeoutRatio = target.timeoutTurns / turns;
  const retryRatio = target.retryCount / turns;
  const hallucinationRatio = target.hallucinationFlags / turns;

  const riskTags: ProviderRiskTag[] = [];
  if (target.timeoutTurns >= 2 && timeoutRatio >= 0.15) {
    riskTags.push("timeout_prone");
  }
  if (avgTtftMs !== null && avgTtftMs > 7_500) {
    riskTags.push("slow_ttft");
  }
  const toolLoopSuppressed =
    target.toolCallsFailed > 0 &&
    target.revertCount === 0 &&
    toolSuccessRate !== null &&
    toolSuccessRate >= 85;
  if (target.hallucinationFlags >= 2 && hallucinationRatio >= 0.15 && !toolLoopSuppressed) {
    riskTags.push("high_hallucination");
  }
  if (toolSuccessRate !== null && toolSuccessRate < 82 && target.toolCallsFailed >= 2) {
    riskTags.push("tool_instability");
  }
  if (target.retryCount >= 2 && retryRatio >= 0.25) {
    riskTags.push("frequent_retry");
  }

  const lowestScore = stabilityScore ?? 100;
  const status: ProviderHealthSummary["status"] =
    stabilityScore === null
      ? "untested"
      : lowestScore >= 82 && riskTags.length === 0
      ? "healthy"
      : lowestScore >= 62 && !riskTags.includes("high_hallucination")
      ? "warning"
      : "degraded";

  const incidents = trimmedModel
    ? (providerRec.recentIncidents ?? []).filter((inc) => inc.modelID === trimmedModel)
    : providerRec.recentIncidents ?? [];

  return {
    providerID,
    modelID: trimmedModel || undefined,
    stabilityScore,
    status,
    totalTurns,
    successRate,
    toolSuccessRate,
    timeoutRate,
    avgTtftMs,
    avgTps,
    cacheHitRate,
    hallucinationFlags: target.hallucinationFlags,
    revertCount: target.revertCount,
    retryCount: target.retryCount,
    lastProbeOk: providerRec.lastProbeOk,
    lastProbeLatencyMs: providerRec.lastProbeLatencyMs,
    riskTags,
    recentIncidents: incidents,
  };
}

export function findBestFallbackModel(
  excludeProviderID?: string,
  options?: { httpOnly?: boolean }
): SelectedModel | null {
  const connected = new Set(workspace.connectedProviders);
  const candidates: Array<{ model: SelectedModel; score: number }> = [];

  for (const prov of workspace.providers) {
    if (excludeProviderID && prov.id === excludeProviderID) continue;
    if (options?.httpOnly && (prov.source === "runner" || LOCAL_CLI_RUNTIME_PROVIDERS.has(prov.id))) continue;
    // Only providers the user connected are eligible; never fall back to an
    // unconnected hosted pool (e.g. `opencode`) the user did not choose.
    if (!connected.has(prov.id)) continue;
    const modelKeys = Object.keys(prov.models || {});
    if (modelKeys.length === 0) continue;
    const defModel = workspace.defaultModels[prov.id] || modelKeys[0];
    if (!defModel || !prov.models?.[defModel]) continue;

    const health = computeProviderHealth(prov.id, defModel);
    if (health.status === "degraded") continue;
    candidates.push({
      model: { providerID: prov.id, modelID: defModel },
      score:
        health.status === "healthy"
          ? (health.stabilityScore ?? 80) + 10
          : health.status === "untested"
          ? 80
           : health.stabilityScore ?? 80,
    });
  }

  candidates.sort((a, b) => b.score - a.score);
  return candidates[0]?.model ?? null;
}
