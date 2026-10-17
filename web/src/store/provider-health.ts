// web/src/store/provider-health.ts
import type { ProviderIncident } from "../services/api";
import { workspace, type SelectedModel } from "./workspace";

export type ProviderRiskTag =
  | "timeout_prone"
  | "slow_ttft"
  | "high_hallucination"
  | "tool_instability"
  | "frequent_retry";

export interface ProviderHealthSummary {
  providerID: string;
  score: number;
  grade: "S" | "A" | "B" | "C" | "—";
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

export function computeProviderHealth(providerID: string): ProviderHealthSummary {
  const rec = workspace.providerTelemetry[providerID];
  if (!rec || (rec.totalTurns === 0 && rec.lastProbeOk === undefined && rec.retryCount === 0)) {
    return {
      providerID,
      score: 100,
      grade: "—",
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

  const turns = Math.max(1, rec.totalTurns);
  const successRate =
    rec.totalTurns > 0
      ? Math.round((rec.successTurns / rec.totalTurns) * 100)
      : rec.lastProbeOk === false
      ? 0
      : 100;

  const toolSuccessRate =
    rec.toolCallsTotal > 0
      ? Math.round(((rec.toolCallsTotal - rec.toolCallsFailed) / rec.toolCallsTotal) * 100)
      : null;

  const timeoutRate = Math.min(100, Math.round((rec.timeoutTurns / turns) * 100));
  const cacheHitRate =
    rec.successTurns > 0 ? Math.round((rec.cacheHitTurns / rec.successTurns) * 100) : null;

  const ttftList = rec.ttftSamplesMs ?? [];
  const avgTtftMs =
    ttftList.length > 0
      ? Math.round(ttftList.reduce((a, b) => a + b, 0) / ttftList.length)
      : rec.lastProbeLatencyMs || null;

  const tpsList = rec.tpsSamples ?? [];
  const avgTps =
    tpsList.length > 0
      ? Math.round((tpsList.reduce((a, b) => a + b, 0) / tpsList.length) * 10) / 10
      : null;

  // Compute composite 0–100 score
  let score = 100;
  const errorRatio = rec.errorTurns / turns;
  const timeoutRatio = rec.timeoutTurns / turns;
  const retryRatio = rec.retryCount / turns;
  const toolFailRatio = rec.toolCallsTotal > 0 ? rec.toolCallsFailed / rec.toolCallsTotal : 0;
  const hallucinationRatio = rec.hallucinationFlags / turns;

  score -= Math.min(40, Math.round(errorRatio * 45));
  score -= Math.min(25, Math.round(timeoutRatio * 35));
  score -= Math.min(15, Math.round(retryRatio * 18));
  score -= Math.min(25, Math.round(toolFailRatio * 35));
  score -= Math.min(25, Math.round(hallucinationRatio * 30));

  if (rec.lastProbeOk === false && rec.totalTurns === 0) {
    score -= 45;
  }
  if (avgTtftMs !== null) {
    if (avgTtftMs > 12_000) score -= 14;
    else if (avgTtftMs > 6_000) score -= 7;
  }
  if (avgTps !== null && avgTps < 12) {
    score -= 6;
  }

  score = Math.max(10, Math.min(100, score));

  const riskTags: ProviderRiskTag[] = [];
  if (rec.timeoutTurns >= 1 && (timeoutRatio >= 0.15 || rec.timeoutTurns >= 2)) {
    riskTags.push("timeout_prone");
  }
  if (avgTtftMs !== null && avgTtftMs > 7_500) {
    riskTags.push("slow_ttft");
  }
  if (rec.hallucinationFlags >= 1 && (hallucinationRatio >= 0.15 || rec.hallucinationFlags >= 2)) {
    riskTags.push("high_hallucination");
  }
  if (toolSuccessRate !== null && toolSuccessRate < 82 && rec.toolCallsFailed >= 2) {
    riskTags.push("tool_instability");
  }
  if (rec.retryCount >= 2 && retryRatio >= 0.25) {
    riskTags.push("frequent_retry");
  }

  const grade: "S" | "A" | "B" | "C" =
    score >= 88 ? "S" : score >= 75 ? "A" : score >= 60 ? "B" : "C";
  const status: "healthy" | "warning" | "degraded" =
    score >= 82 && riskTags.length === 0
      ? "healthy"
      : score >= 62 && !riskTags.includes("high_hallucination")
      ? "warning"
      : "degraded";

  return {
    providerID,
    score,
    grade,
    status,
    totalTurns: rec.totalTurns,
    successRate,
    toolSuccessRate,
    timeoutRate,
    avgTtftMs,
    avgTps,
    cacheHitRate,
    hallucinationFlags: rec.hallucinationFlags,
    revertCount: rec.revertCount,
    retryCount: rec.retryCount,
    lastProbeOk: rec.lastProbeOk,
    lastProbeLatencyMs: rec.lastProbeLatencyMs,
    riskTags,
    recentIncidents: rec.recentIncidents ?? [],
  };
}

export function findBestFallbackModel(excludeProviderID?: string): SelectedModel | null {
  const connected = new Set(workspace.connectedProviders);
  const candidates: Array<{ model: SelectedModel; score: number }> = [];

  for (const prov of workspace.providers) {
    if (excludeProviderID && prov.id === excludeProviderID) continue;
    if (!connected.has(prov.id) && prov.id !== "opencode") continue;
    const modelKeys = Object.keys(prov.models || {});
    if (modelKeys.length === 0) continue;
    const defModel = workspace.defaultModels[prov.id] || modelKeys[0];
    if (!defModel || !prov.models?.[defModel]) continue;

    const health = computeProviderHealth(prov.id);
    if (health.status === "degraded") continue;
    candidates.push({
      model: { providerID: prov.id, modelID: defModel },
      score: health.status === "healthy" ? health.score + 10 : health.score,
    });
  }

  candidates.sort((a, b) => b.score - a.score);
  return candidates[0]?.model ?? null;
}
