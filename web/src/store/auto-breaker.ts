// web/src/store/auto-breaker.ts
//
// Client view of the shared candidate circuit breaker owned by the Go provider
// layer (~/.hoxi/provider-breakers.json, exposed at /api/hoxi/provider/candidates).
// The desktop app is the authoritative writer for session-plane outcomes; this
// module pulls the snapshot so auto-pool selection can skip cooling candidates
// and rank the faster, healthier ones.
import { createSignal } from "solid-js";
import { hoxiAPI, type CandidateBreakerSnapshot, type CandidateOutcomeInput } from "../services/api";
import { workspace } from "./workspace-state";

export const AUTO_BREAKERS_STORAGE_KEY = "hoxi-auto-breakers:v1";

// A locally reported failure opens the circuit optimistically for a short
// window; the authoritative backend snapshot (with its class-aware cooldown)
// replaces it on the next sync. A bounded window prevents a candidate from
// showing as cooling forever when the app stays offline.
const OPTIMISTIC_OPEN_TTL_MS = 90_000;

type BreakerMap = Record<string, CandidateBreakerSnapshot>;

function loadInitialBreakers(): BreakerMap {
  try {
    const raw = localStorage.getItem(AUTO_BREAKERS_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return {};
    return parsed as BreakerMap;
  } catch {
    return {};
  }
}

const [providerBreakersSignal, setProviderBreakersSignal] = createSignal<BreakerMap>(
  loadInitialBreakers()
);

function persistBreakers(next: BreakerMap) {
  try {
    localStorage.setItem(AUTO_BREAKERS_STORAGE_KEY, JSON.stringify(next));
  } catch {
    /* ignore */
  }
}

export function candidateBreakerKey(providerID: string, modelID: string): string {
  return `${providerID}/${modelID}`;
}

export function providerBreakers(): BreakerMap {
  return providerBreakersSignal();
}

export function getCandidateBreaker(
  providerID: string,
  modelID: string
): CandidateBreakerSnapshot | undefined {
  if (!providerID || !modelID) return undefined;
  return providerBreakersSignal()[candidateBreakerKey(providerID, modelID)];
}

/** True while the backend breaker is holding this candidate out of rotation. */
export function isCandidateBreakerOpen(providerID: string, modelID: string): boolean {
  const snap = getCandidateBreaker(providerID, modelID);
  if (!snap) return false;
  return snap.open && (!snap.openUntil || snap.openUntil > Date.now());
}

export function candidateBreakerRemainingMs(providerID: string, modelID: string): number {
  const snap = getCandidateBreaker(providerID, modelID);
  if (!snap || !snap.openUntil) return 0;
  return Math.max(0, snap.openUntil - Date.now());
}

/**
 * Backend health score (0..1) for a candidate, or null when the backend has no
 * evidence yet. Selection blends this with local telemetry so a fast, reliable
 * candidate outranks a slow one.
 */
export function candidateBreakerScore(
  providerID: string,
  modelID: string
): number | null {
  const snap = getCandidateBreaker(providerID, modelID);
  if (!snap || typeof snap.score !== "number") return null;
  return snap.score;
}

/**
 * Best available latency estimate for a candidate in milliseconds: the shared
 * breaker's EWMA (backend-observed) first, then the local time-to-first-token
 * average. Returns null when neither has evidence.
 */
export function candidateEffectiveLatencyMs(
  providerID: string,
  modelID: string
): number | null {
  const snap = getCandidateBreaker(providerID, modelID);
  if (snap && typeof snap.latencyEwmaMs === "number" && snap.latencyEwmaMs > 0) {
    return snap.latencyEwmaMs;
  }
  const rec = workspace.providerTelemetry?.[providerID];
  if (!rec) return null;
  const m = rec.models?.[modelID] || rec;
  const samples = m.ttftSamplesMs;
  if (Array.isArray(samples) && samples.length > 0) {
    const avg = samples.reduce((a, b) => a + b, 0) / samples.length;
    return avg > 0 ? avg : null;
  }
  return null;
}

const SLOW_FACTOR = 3;
const SLOW_FLOOR_MS = 3000;

/**
 * Drops candidates that are drastically slower than the fastest measured peer,
 * so a slow-but-working model stops absorbing the rotation while a faster one is
 * healthy. Candidates with no latency sample are always kept, and the result is
 * never empty. This runs for every strategy, not just smart-health.
 */
export function filterSlowCandidates<T extends { providerID: string; modelID: string }>(
  cands: T[]
): T[] {
  if (cands.length <= 1) return cands;
  const lats = cands.map((c) => candidateEffectiveLatencyMs(c.providerID, c.modelID));
  let fastest = Number.POSITIVE_INFINITY;
  for (const l of lats) {
    if (l !== null && l > 0 && l < fastest) fastest = l;
  }
  if (!Number.isFinite(fastest)) return cands;
  const threshold = Math.max(fastest * SLOW_FACTOR, SLOW_FLOOR_MS);
  const kept = cands.filter((_, i) => lats[i] === null || (lats[i] as number) <= threshold);
  return kept.length > 0 ? kept : cands;
}

export async function syncProviderBreakers(): Promise<void> {
  try {
    const res = await hoxiAPI.getProviderCandidates();
    if (res?.breakers && typeof res.breakers === "object") {
      applyProviderBreakers(res.breakers as BreakerMap);
    }
  } catch {
    /* keep the last known snapshot when offline */
  }
}

/** Replaces the in-memory breaker snapshot; used by sync, clear and tests. */
export function applyProviderBreakers(next: BreakerMap): void {
  setProviderBreakersSignal(next);
  persistBreakers(next);
}

/**
 * Reports a turn outcome to the shared breaker. Fire-and-forget: the local
 * cooldown already guards the current session, and the next sync reconciles.
 */
export function reportAutoCandidateOutcome(input: CandidateOutcomeInput): void {
  if (!input.providerID || !input.modelID) return;
  void hoxiAPI.reportCandidateOutcome(input).catch(() => {});
  if (!input.ok) {
    // Optimistically reflect the open circuit so selection reacts immediately,
    // bounded by a TTL so it can never linger past the backend's own cooldown.
    const key = candidateBreakerKey(input.providerID, input.modelID);
    const now = Date.now();
    const next: BreakerMap = { ...providerBreakersSignal() };
    const prev = next[key];
    const existingUntil = prev?.openUntil && prev.openUntil > now ? prev.openUntil : 0;
    next[key] = {
      providerID: input.providerID,
      modelID: input.modelID,
      phase: "open",
      open: true,
      openUntil: existingUntil || now + OPTIMISTIC_OPEN_TTL_MS,
      score: 0,
      latencyEwmaMs: prev?.latencyEwmaMs,
      consecutiveFailures: (prev?.consecutiveFailures ?? 0) + 1,
      lastClass: input.class,
      updatedAt: now,
    };
    setProviderBreakersSignal(next);
    persistBreakers(next);
  }
}

/** Clears breaker state for a candidate, a provider, or everything. */
export async function clearCandidateBreaker(providerID?: string, modelID?: string): Promise<void> {
  try {
    const res = await hoxiAPI.clearProviderCandidates(providerID, modelID);
    if (res?.breakers && typeof res.breakers === "object") {
      applyProviderBreakers(res.breakers as BreakerMap);
    }
  } catch {
    /* ignore */
  }
}
