// web/src/store/provider-health.ts
import type { ModelTelemetryStats, ProviderIncident, ProviderTelemetryStats } from "../services/api";
import { workspace, type SelectedModel } from "./workspace";

export type ProviderRiskTag =
  | "timeout_prone"
  | "slow_ttft"
  | "high_hallucination"
  | "tool_instability"
  | "frequent_retry"
  | "low_pass_rate";

export interface ProviderHealthSummary {
  providerID: string;
  modelID?: string;
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
  auditTotalCount: number;
  auditPassCount: number;
  codePassRate: number | null;
  avgAuditScore: number | null;
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
    score: 0,
    grade: "—",
    status: "untested",
    totalTurns: 0,
    successRate: 100,
    toolSuccessRate: null,
    timeoutRate: 0,
    avgTtftMs: null,
    avgTps: null,
    cacheHitRate: null,
    auditTotalCount: 0,
    auditPassCount: 0,
    codePassRate: null,
    avgAuditScore: null,
    hallucinationFlags: 0,
    revertCount: 0,
    retryCount: 0,
    riskTags: [],
    recentIncidents: [],
  };
}

/**
 * Computes health & quality score for a provider or a specific model.
 * Only models/providers that have actually been run (`totalTurns > 0`) or
 * scored by the Observer (`auditTotalCount > 0`) receive a score;
 * untested models return `status: "untested"` so no score badge is shown.
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

  const auditTotal = target?.auditTotalCount ?? 0;
  const totalTurns = target?.totalTurns ?? 0;

  // Strictly require actual execution turns or Observer code audits before showing any score
  if (!target || (totalTurns === 0 && auditTotal === 0)) {
    return emptyUntestedSummary(providerID, trimmedModel || undefined);
  }

  const turns = Math.max(1, totalTurns);
  const successRate =
    totalTurns > 0 ? Math.round((target.successTurns / totalTurns) * 100) : 100;

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

  const auditPass = target.auditPassCount ?? 0;
  const codePassRate =
    auditTotal > 0 ? Math.round((auditPass / auditTotal) * 100) : null;

  const auditScoreList = target.auditScores ?? [];
  const avgAuditScore =
    auditScoreList.length > 0
      ? Math.round(auditScoreList.reduce((a, b) => a + b, 0) / auditScoreList.length)
      : target.lastAuditScore ?? null;

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

  // Compute composite 0–100 score starting from Observer avgAuditScore (if audited) or 100
  let score = avgAuditScore !== null ? avgAuditScore : 100;
  const errorRatio = target.errorTurns / turns;
  const timeoutRatio = target.timeoutTurns / turns;
  const retryRatio = target.retryCount / turns;
  const toolFailRatio =
    target.toolCallsTotal > 0 ? target.toolCallsFailed / target.toolCallsTotal : 0;
  const hallucinationRatio = target.hallucinationFlags / turns;

  score -= Math.min(40, Math.round(errorRatio * 45));
  score -= Math.min(25, Math.round(timeoutRatio * 35));
  score -= Math.min(15, Math.round(retryRatio * 18));
  score -= Math.min(25, Math.round(toolFailRatio * 35));
  score -= Math.min(25, Math.round(hallucinationRatio * 30));

  if (codePassRate !== null && auditTotal > 0 && avgAuditScore === null) {
    const failAuditRatio = (auditTotal - auditPass) / auditTotal;
    score -= Math.min(35, Math.round(failAuditRatio * 38));
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
  if (codePassRate !== null && codePassRate < 65 && auditTotal > auditPass) {
    riskTags.push("low_pass_rate");
  }
  if (target.timeoutTurns >= 1 && (timeoutRatio >= 0.15 || target.timeoutTurns >= 2)) {
    riskTags.push("timeout_prone");
  }
  if (avgTtftMs !== null && avgTtftMs > 7_500) {
    riskTags.push("slow_ttft");
  }
  if (
    target.hallucinationFlags >= 1 &&
    (hallucinationRatio >= 0.15 || target.hallucinationFlags >= 2)
  ) {
    riskTags.push("high_hallucination");
  }
  if (toolSuccessRate !== null && toolSuccessRate < 82 && target.toolCallsFailed >= 2) {
    riskTags.push("tool_instability");
  }
  if (target.retryCount >= 2 && retryRatio >= 0.25) {
    riskTags.push("frequent_retry");
  }

  const grade: "S" | "A" | "B" | "C" =
    score >= 88 ? "S" : score >= 75 ? "A" : score >= 60 ? "B" : "C";
  const status: "healthy" | "warning" | "degraded" =
    score >= 82 && riskTags.length === 0
      ? "healthy"
      : score >= 62 &&
        !riskTags.includes("high_hallucination") &&
        !(riskTags.includes("low_pass_rate") && (codePassRate ?? 100) <= 50)
      ? "warning"
      : "degraded";

  const incidents = trimmedModel
    ? (providerRec.recentIncidents ?? []).filter((inc) => inc.modelID === trimmedModel)
    : providerRec.recentIncidents ?? [];

  return {
    providerID,
    modelID: trimmedModel || undefined,
    score,
    grade,
    status,
    totalTurns,
    successRate,
    toolSuccessRate,
    timeoutRate,
    avgTtftMs,
    avgTps,
    cacheHitRate,
    auditTotalCount: auditTotal,
    auditPassCount: auditPass,
    codePassRate,
    avgAuditScore,
    hallucinationFlags: target.hallucinationFlags,
    revertCount: target.revertCount,
    retryCount: target.retryCount,
    lastProbeOk: providerRec.lastProbeOk,
    lastProbeLatencyMs: providerRec.lastProbeLatencyMs,
    riskTags,
    recentIncidents: incidents,
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

    const health = computeProviderHealth(prov.id, defModel);
    if (health.status === "degraded") continue;
    candidates.push({
      model: { providerID: prov.id, modelID: defModel },
      score:
        health.status === "healthy"
          ? health.score + 10
          : health.status === "untested"
          ? 80
          : health.score,
    });
  }

  candidates.sort((a, b) => b.score - a.score);
  return candidates[0]?.model ?? null;
}
