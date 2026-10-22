// web/src/store/auto-breaker.ts
//
// Client view of the shared candidate circuit breaker owned by the Go provider
// layer (~/.hoxi/provider-breakers.json, exposed at /api/hoxi/provider/candidates).
// The desktop app is the authoritative writer for session-plane outcomes; this
// module pulls the snapshot so auto-pool selection can skip cooling candidates
// and rank the faster, healthier ones.
import { createSignal } from "solid-js";
import { hoxiAPI, type CandidateBreakerSnapshot, type CandidateOutcomeInput } from "../services/api";

export const AUTO_BREAKERS_STORAGE_KEY = "hoxi-auto-breakers:v1";

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
    // Optimistically reflect the open circuit so selection reacts immediately.
    const key = candidateBreakerKey(input.providerID, input.modelID);
    const next: BreakerMap = { ...providerBreakersSignal() };
    const prev = next[key];
    next[key] = {
      providerID: input.providerID,
      modelID: input.modelID,
      phase: "open",
      open: true,
      openUntil: prev?.openUntil,
      score: 0,
      latencyEwmaMs: prev?.latencyEwmaMs,
      consecutiveFailures: (prev?.consecutiveFailures ?? 0) + 1,
      lastClass: input.class,
      updatedAt: Date.now(),
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
