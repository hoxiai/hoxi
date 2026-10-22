// web/src/store/auto-candidate-status.ts
//
// Why a single Auto-pool candidate is (or is not) contributing to rotation.
// Derived purely from state the store already holds — the shared candidate
// breaker snapshot (~/.hoxi/provider-breakers.json) plus the local session
// cooldown — so the pool list can show a truthful per-candidate status instead
// of a single merged "cooling" flag.
import {
  candidateBreakerRemainingMs,
  candidateEffectiveLatencyMs,
  getCandidateBreaker,
  isCandidateBreakerOpen,
} from "./auto-breaker";
import { getAutoCandidateCooldown } from "./auto-models";

// `ready` and `slow` are both "healthy enough to try"; `slow` additionally means
// the pool's generational filter (`filterSlowCandidates`) will sidestep it while
// a faster peer stays healthy, so the list must not paint it as plainly ready.
// `breaker` carries the failure class and count so the badge can say why the
// circuit opened.
export type CandidatePoolState = "ready" | "breaker" | "cooldown" | "slow";

export interface CandidatePoolStatus {
  state: CandidatePoolState;
  remainMs: number;
  failClass?: string;
  consecutiveFailures?: number;
  reason?: string;
  latencyMs: number | null;
}

export function getCandidatePoolStatus(
  providerID: string,
  modelID: string
): CandidatePoolStatus {
  const latencyMs = candidateEffectiveLatencyMs(providerID, modelID);
  if (isCandidateBreakerOpen(providerID, modelID)) {
    const snap = getCandidateBreaker(providerID, modelID);
    return {
      state: "breaker",
      remainMs: candidateBreakerRemainingMs(providerID, modelID),
      failClass: snap?.lastClass,
      consecutiveFailures: snap?.consecutiveFailures,
      latencyMs,
    };
  }
  const entry = getAutoCandidateCooldown(providerID, modelID);
  if (entry && entry.until > Date.now()) {
    return {
      state: "cooldown",
      remainMs: Math.max(0, entry.until - Date.now()),
      reason: entry.reason,
      latencyMs,
    };
  }
  return { state: "ready", remainMs: 0, latencyMs };
}
