// web/src/store/auto-models.ts
import { createSignal } from "solid-js";
import { produce } from "solid-js/store";
import { t } from "../i18n";
import { hoxiAPI, type ProviderInfo, type ProviderModelInfo } from "../services/api";
import {
  readStorageJSON,
  setWorkspace,
  workspace,
  writeStorageJSON,
  type SelectedModel,
} from "./workspace-state";
import {
  candidateBreakerRemainingMs, candidateBreakerScore, clearCandidateBreaker,
  filterNoCacheCandidates, filterSlowCandidates, isCandidateBreakerOpen,
} from "./auto-breaker";
import {
  AUTO_PROVIDER_ID,
  PRESET_AUTO_POOL_IDS,
  computeSmartDefaultCandidates,
  evaluatePromptModalityIntent,
  fetchFreeTierGuides,
  getFreeTierGuideItems,
  inferDedicatedMediaModality,
  isModelFree,
  isNonChatUtilityModelID,
  modelSupportsImageInput,
  type AutoCandidateCooldown,
  type AutoModelPoolConfig,
  type AutoPoolCandidate,
  type AutoRotationStrategy,
  type DedicatedMediaModality,
  type FreeTierGuideItem,
  type PresetAutoPoolID,
  type PromptIntentEvaluation,
  type PromptIntentKind,
} from "./auto-pool-defaults";

export {
  AUTO_PROVIDER_ID,
  PRESET_AUTO_POOL_IDS,
  computeSmartDefaultCandidates,
  evaluatePromptModalityIntent,
  fetchFreeTierGuides,
  getFreeTierGuideItems,
  inferDedicatedMediaModality,
  isModelFree,
  isNonChatUtilityModelID,
  modelSupportsImageInput,
  type AutoCandidateCooldown,
  type AutoModelPoolConfig,
  type AutoPoolCandidate,
  type AutoRotationStrategy,
  type DedicatedMediaModality,
  type FreeTierGuideItem,
  type PresetAutoPoolID,
  type PromptIntentEvaluation,
  type PromptIntentKind,
};

export const AUTO_POOLS_STORAGE_KEY = "hoxi-auto-pools:v1";
export const AUTO_COOLDOWNS_STORAGE_KEY = "hoxi-auto-cooldowns:v1";

function createDefaultAutoPools(): Record<string, AutoModelPoolConfig> {
  return {
    free: {
      id: "free",
      preset: true,
      strategy: "round-robin",
      customized: false,
      candidates: [],
    },
    daily: {
      id: "daily",
      preset: true,
      strategy: "priority",
      customized: false,
      candidates: [],
    },
    frontier: {
      id: "frontier",
      preset: true,
      strategy: "smart-health",
      customized: false,
      candidates: [],
    },
  };
}

function normalizeAutoPoolsRecord(raw: unknown): Record<string, AutoModelPoolConfig> {
  const base = createDefaultAutoPools();
  if (!raw || typeof raw !== "object") return base;
  for (const [k, val] of Object.entries(raw as Record<string, any>)) {
    const id = k.trim();
    if (!id || !val || typeof val !== "object") continue;
    const isPreset = (PRESET_AUTO_POOL_IDS as readonly string[]).includes(id);
    const strat: AutoRotationStrategy =
      val.strategy === "round-robin" || val.strategy === "priority" || val.strategy === "smart-health"
        ? val.strategy
        : base[id]?.strategy || "round-robin";
    const cands: AutoPoolCandidate[] = Array.isArray(val.candidates)
      ? val.candidates
          .filter(
            (c: any) =>
              c &&
              typeof c.providerID === "string" &&
              typeof c.modelID === "string" &&
              c.providerID.trim() !== "" &&
              c.modelID.trim() !== "" &&
              c.providerID.trim() !== AUTO_PROVIDER_ID
          )
          .map((c: any) => ({
            providerID: c.providerID.trim(),
            modelID: c.modelID.trim(),
          }))
      : [];
    base[id] = {
      id,
      name: typeof val.name === "string" && val.name.trim() ? val.name.trim() : undefined,
      preset: isPreset,
      strategy: strat,
      customized: Boolean(val.customized),
      candidates: cands,
    };
  }
  return base;
}

function loadInitialAutoPools(): Record<string, AutoModelPoolConfig> {
  const parsed = readStorageJSON(AUTO_POOLS_STORAGE_KEY);
  return parsed ? normalizeAutoPoolsRecord(parsed) : createDefaultAutoPools();
}

function loadInitialCooldowns(): Record<string, AutoCandidateCooldown> {
  const parsed = readStorageJSON<Record<string, any>>(AUTO_COOLDOWNS_STORAGE_KEY);
  if (!parsed || typeof parsed !== "object") return {};
  const now = Date.now();
  const out: Record<string, AutoCandidateCooldown> = {};
  for (const [k, v] of Object.entries<any>(parsed)) {
    if (v && typeof v.until === "number" && v.until > now) {
      out[k] = {
        until: v.until,
        reason: String(v.reason || "rate_limit"),
        statusCode: typeof v.statusCode === "number" ? v.statusCode : undefined,
      };
    }
  }
  return out;
}

const [autoPoolsSignal, setAutoPoolsSignal] = createSignal<Record<string, AutoModelPoolConfig>>(
  loadInitialAutoPools()
);
const [autoCooldownsSignal, setAutoCooldownsSignal] = createSignal<
  Record<string, AutoCandidateCooldown>
>(loadInitialCooldowns());
const [providerKeyCountsSignal, setProviderKeyCountsSignal] = createSignal<Record<string, number>>({});
const [poolCursorsSignal, setPoolCursorsSignal] = createSignal<Record<string, number>>({});
const [lastResolvedByPoolSignal, setLastResolvedByPoolSignal] = createSignal<Record<string, SelectedModel>>({});

// Session affinity: reuse the resolved candidate across turns while healthy so
// the upstream prompt-cache prefix stays warm.
const MAX_SESSION_AUTO_TARGETS = 500;
const sessionAutoTarget = new Map<string, { poolID: string; providerID: string; modelID: string }>();

export function clearSessionAutoTarget(sessionID: string): void {
  if (sessionID) sessionAutoTarget.delete(sessionID);
}

function persistAutoPoolsLocal(pools: Record<string, AutoModelPoolConfig>) {
  writeStorageJSON(AUTO_POOLS_STORAGE_KEY, pools);
}

function persistCooldownsLocal(cooldowns: Record<string, AutoCandidateCooldown>) {
  writeStorageJSON(AUTO_COOLDOWNS_STORAGE_KEY, cooldowns);
}

export function autoPools(): Record<string, AutoModelPoolConfig> {
  return autoPoolsSignal();
}

export function providerKeyCounts(): Record<string, number> {
  return providerKeyCountsSignal();
}

export function isAutoModel(model?: SelectedModel | null): boolean {
  return Boolean(model && model.providerID === AUTO_PROVIDER_ID && model.modelID);
}

export function getAutoPoolDisplayName(poolID: string): string {
  const pool = autoPoolsSignal()[poolID];
  if (pool?.name && !pool.preset) return pool.name;
  if (poolID === "free") return t("models.autoPoolFreeName");
  if (poolID === "daily") return t("models.autoPoolDailyName");
  if (poolID === "frontier") return t("models.autoPoolFrontierName");
  return pool?.name || `Auto · ${poolID}`;
}

export function getAutoPoolDescription(poolID: string): string {
  if (poolID === "free") return t("models.autoPoolFreeDesc");
  if (poolID === "daily") return t("models.autoPoolDailyDesc");
  if (poolID === "frontier") return t("models.autoPoolFrontierDesc");
  return t("models.autoPoolFreeDesc");
}

function resolveAvailableModelID(providerID: string, modelID: string): string | null {
  if (!providerID || !modelID || providerID === AUTO_PROVIDER_ID) return null;
  const models = workspace.providers.find((p) => p.id === providerID)?.models;
  if (!models) return null;
  if (models[modelID]) return modelID;
  const matches = Object.keys(models).filter((id) => id.toLowerCase() === modelID.toLowerCase());
  return matches.length === 1 ? matches[0] : null;
}

export function getEffectivePoolCandidates(poolID: string): AutoPoolCandidate[] {
  const pool = autoPoolsSignal()[poolID];
  if (!pool && !(PRESET_AUTO_POOL_IDS as readonly string[]).includes(poolID)) {
    return [];
  }
  // A customized pool is authoritative, including when the user deleted every
  // candidate. Falling back to the smart defaults here would resurrect the
  // models that were just removed and make deletion look like a no-op loop.
  if (pool?.customized) {
    return pool.candidates.flatMap((c) => {
      const canonical = resolveAvailableModelID(c.providerID, c.modelID);
      return canonical ? [{ providerID: c.providerID, modelID: canonical }] : [];
    });
  }
  return computeSmartDefaultCandidates(poolID);
}

// Stored order for a pool, including entries whose provider/model is currently
// unavailable. Mutations must target this list by identity: the visible list is
// filtered, so using its indices would delete the wrong stored entry.
export function getStoredPoolCandidates(poolID: string): AutoPoolCandidate[] {
  const pool = autoPoolsSignal()[poolID];
  if (pool?.customized) return pool.candidates.map((c) => ({ ...c }));
  return getEffectivePoolCandidates(poolID).map((c) => ({ ...c }));
}

export function isCandidateCoolingDown(providerID: string, modelID: string): boolean {
  const key = `${providerID}/${modelID}`;
  const entry = autoCooldownsSignal()[key];
  if (entry && entry.until > Date.now()) return true;
  return isCandidateBreakerOpen(providerID, modelID);
}

export function getCandidateCooldownRemainingMs(providerID: string, modelID: string): number {
  const key = `${providerID}/${modelID}`;
  const entry = autoCooldownsSignal()[key];
  const local = entry ? Math.max(0, entry.until - Date.now()) : 0;
  return Math.max(local, candidateBreakerRemainingMs(providerID, modelID));
}

/** Raw local cooldown entry (reason + expiry) for a candidate, if any. */
export function getAutoCandidateCooldown(
  providerID: string,
  modelID: string
): AutoCandidateCooldown | undefined {
  return autoCooldownsSignal()[`${providerID}/${modelID}`];
}

export function markAutoCandidateCooldown(
  providerID: string,
  modelID: string,
  reason = "rate_limit",
  statusCode?: number,
  durationMs?: number
): void {
  if (!providerID || !modelID || providerID === AUTO_PROVIDER_ID) return;
  let ttl = durationMs ?? 90_000;
  if (!durationMs) {
    const isQuota = /daily|day|insufficient_quota|balance|credit|exhausted/i.test(reason);
    const isRateLimit = statusCode === 429 || /429|rate.?limit|too many requests/i.test(reason);
    if (isQuota) ttl = 2 * 60 * 60 * 1000;
    else if (isRateLimit) ttl = 3 * 60 * 1000;
  }
  const key = `${providerID}/${modelID}`;
  const next = {
    ...autoCooldownsSignal(),
    [key]: {
      until: Date.now() + ttl,
      reason,
      statusCode,
    },
  };
  setAutoCooldownsSignal(next);
  persistCooldownsLocal(next);
}

export function clearAutoPoolCooldowns(poolID?: string): void {
  if (!poolID) {
    setAutoCooldownsSignal({});
    persistCooldownsLocal({});
    void clearCandidateBreaker();
    return;
  }
  const cands = getEffectivePoolCandidates(poolID);
  const next = { ...autoCooldownsSignal() };
  for (const c of cands) {
    delete next[`${c.providerID}/${c.modelID}`];
  }
  setAutoCooldownsSignal(next);
  persistCooldownsLocal(next);
  void (async () => {
    for (const c of cands) {
      await clearCandidateBreaker(c.providerID, c.modelID);
    }
  })();
}

function candidateHealthScore(c: AutoPoolCandidate): number {
  // The shared breaker's health score (reliability + latency EWMA) is the
  // backend's own measurement. When local telemetry has no evidence yet, rely on
  // it alone so a backend-measured fast model still outranks a slow one.
  const backend = candidateBreakerScore(c.providerID, c.modelID);
  const pStats = workspace.providerTelemetry?.[c.providerID];
  if (!pStats) return backend !== null ? backend * 100 : 75;
  const mStats = pStats.models?.[c.modelID] || pStats;
  let score = 80;
  if (mStats.totalTurns > 0) {
    const successRate = (mStats.successTurns / mStats.totalTurns) * 100;
    score = successRate;
  }
  if (Array.isArray(mStats.ttftSamplesMs) && mStats.ttftSamplesMs.length > 0) {
    const avgTtft =
      mStats.ttftSamplesMs.reduce((a, b) => a + b, 0) / mStats.ttftSamplesMs.length;
    if (avgTtft < 1500) score += 8;
    else if (avgTtft > 6000) score -= 12;
  }
  if (backend !== null) {
    score = score * 0.5 + backend * 100 * 0.5;
  }
  return score;
}

export function resolveAutoModelTarget(
  poolIDOrModel: string | SelectedModel,
  opts?: {
    advance?: boolean;
    excludeKeys?: Set<string>;
    requireImage?: boolean;
    /** Pin the resolved candidate to this session across turns (cache warmth). */
    sessionID?: string;
  }
): SelectedModel | null {
  const poolID =
    typeof poolIDOrModel === "string"
      ? poolIDOrModel.replace(/^hoxi-auto\//, "").trim()
      : poolIDOrModel.modelID;
  const pool = autoPoolsSignal()[poolID] || createDefaultAutoPools()[poolID] || {
    id: poolID,
    strategy: "round-robin" as AutoRotationStrategy,
    customized: false,
    candidates: [],
  };

  let allCands = getEffectivePoolCandidates(poolID).filter(
    (c) => !opts?.excludeKeys?.has(`${c.providerID}/${c.modelID}`)
  );
  if (opts?.requireImage) {
    const imgCands = allCands.filter((c) => modelSupportsImageInput(c.providerID, c.modelID));
    if (imgCands.length > 0) {
      allCands = imgCands;
    }
  }
  if (allCands.length === 0) return null;

  const readyCands = allCands.filter((c) => !isCandidateCoolingDown(c.providerID, c.modelID));
  const basePool =
    readyCands.length > 0
      ? readyCands
      : [...allCands].sort(
          (a, b) =>
            getCandidateCooldownRemainingMs(a.providerID, a.modelID) -
            getCandidateCooldownRemainingMs(b.providerID, b.modelID)
        );
  // Skip a candidate that is drastically slower than a healthy peer in every
  // strategy, so a slow-but-working model stops absorbing the rotation, and one
  // that never reports prompt-cache hits while a peer does (it would re-bill the
  // whole context each step). Runs before the pin so such a pin is dropped.
  const activePool = filterNoCacheCandidates(filterSlowCandidates(basePool));

  // Session affinity short-circuit: reuse the candidate already pinned to this
  // session when it survived the filters above.
  if (opts?.sessionID) {
    const pinned = sessionAutoTarget.get(opts.sessionID);
    if (pinned && pinned.poolID === poolID) {
      const match = activePool.find(
        (c) => c.providerID === pinned.providerID && c.modelID === pinned.modelID
      );
      if (match) {
        return { providerID: match.providerID, modelID: match.modelID };
      }
    }
  }

  let chosen: AutoPoolCandidate;
  const cursor = poolCursorsSignal()[poolID] ?? 0;

  if (pool.strategy === "priority") {
    chosen = activePool[0];
  } else if (pool.strategy === "smart-health") {
    const ranked = [...activePool].sort(
      (a, b) => candidateHealthScore(b) - candidateHealthScore(a)
    );
    chosen = ranked[0];
  } else {
    chosen = activePool[cursor % activePool.length];
    if (opts?.advance) {
      setPoolCursorsSignal((prev) => ({ ...prev, [poolID]: cursor + 1 }));
    }
  }

  if (opts?.sessionID) {
    if (!sessionAutoTarget.has(opts.sessionID) && sessionAutoTarget.size >= MAX_SESSION_AUTO_TARGETS) {
      const oldest = sessionAutoTarget.keys().next().value;
      if (oldest !== undefined) sessionAutoTarget.delete(oldest);
    }
    sessionAutoTarget.set(opts.sessionID, {
      poolID,
      providerID: chosen.providerID,
      modelID: chosen.modelID,
    });
  }
  if (opts?.advance) {
    setLastResolvedByPoolSignal((prev) => ({
      ...prev,
      [poolID]: { providerID: chosen.providerID, modelID: chosen.modelID },
    }));
  }
  return { providerID: chosen.providerID, modelID: chosen.modelID };
}

export function peekAutoModelTarget(poolID: string): SelectedModel | null {
  const last = lastResolvedByPoolSignal()[poolID];
  if (
    last &&
    !isCandidateCoolingDown(last.providerID, last.modelID) &&
    getEffectivePoolCandidates(poolID).some(
      (c) => c.providerID === last.providerID && c.modelID === last.modelID
    )
  ) {
    return last;
  }
  return resolveAutoModelTarget(poolID, { advance: false });
}

export function stepAutoPoolNext(poolID: string): SelectedModel | null {
  const cands = getEffectivePoolCandidates(poolID);
  if (cands.length === 0) return null;
  const current = peekAutoModelTarget(poolID);
  const ready = cands.filter((c) => !isCandidateCoolingDown(c.providerID, c.modelID));
  const list = ready.length > 0 ? ready : cands;
  const curIdx = current
    ? list.findIndex((c) => c.providerID === current.providerID && c.modelID === current.modelID)
    : -1;
  const nextIdx = (curIdx + 1) % list.length;
  const next = list[nextIdx];
  setPoolCursorsSignal((prev) => ({ ...prev, [poolID]: nextIdx + 1 }));
  setLastResolvedByPoolSignal((prev) => ({
    ...prev,
    [poolID]: { providerID: next.providerID, modelID: next.modelID },
  }));
  return { providerID: next.providerID, modelID: next.modelID };
}

export function buildAutoVirtualProvider(): ProviderInfo {
  const pools = autoPoolsSignal();
  const models: Record<string, ProviderModelInfo> = {};
  const orderedIDs = [
    ...PRESET_AUTO_POOL_IDS,
    ...Object.keys(pools).filter((id) => !(PRESET_AUTO_POOL_IDS as readonly string[]).includes(id)),
  ];
  for (const id of orderedIDs) {
    const isFree = id === "free";
    models[id] = {
      id,
      name: getAutoPoolDisplayName(id),
      reasoning: true,
      efforts: ["low", "medium", "high"],
      defaultEffort: "medium",
      multiplier: isFree ? "0x" : "Auto",
      cost: isFree ? { input: 0, output: 0, currency: "USD" } : undefined,
      capabilities: {
        reasoning: true,
        toolcall: true,
        input: { text: true, image: true },
      },
    };
  }
  return {
    id: AUTO_PROVIDER_ID,
    name: t("models.autoProviderName"),
    source: "auto",
    models,
  };
}

function refreshWorkspaceAutoProvider() {
  if (
    workspace.providers.some((p) => p.id === AUTO_PROVIDER_ID) ||
    workspace.connectedProviders.includes(AUTO_PROVIDER_ID)
  ) {
    setWorkspace({
      providers: workspace.providers.filter((p) => p.id !== AUTO_PROVIDER_ID),
      connectedProviders: workspace.connectedProviders.filter((id) => id !== AUTO_PROVIDER_ID),
    });
  }
}

export async function syncAutoPoolsFromServer(): Promise<void> {
  try {
    const res = await hoxiAPI.getAutoPoolsConfig();
    if (res?.autoPools) {
      const normalized = normalizeAutoPoolsRecord(res.autoPools);
      setAutoPoolsSignal(normalized);
      persistAutoPoolsLocal(normalized);
    }
    if (res?.keyCounts) {
      setProviderKeyCountsSignal(res.keyCounts);
    }
    refreshWorkspaceAutoProvider();
  } catch {
    /* ignore */
  }
}

export async function saveAutoPool(
  poolID: string,
  patch: Partial<AutoModelPoolConfig>
): Promise<void> {
  const current = autoPoolsSignal()[poolID] || {
    id: poolID,
    preset: (PRESET_AUTO_POOL_IDS as readonly string[]).includes(poolID),
    strategy: "round-robin" as AutoRotationStrategy,
    customized: true,
    candidates: [],
  };
  const updated: AutoModelPoolConfig = {
    ...current,
    ...patch,
    id: poolID,
  };
  const next = { ...autoPoolsSignal(), [poolID]: updated };
  setAutoPoolsSignal(next);
  persistAutoPoolsLocal(next);
  refreshWorkspaceAutoProvider();
  await hoxiAPI.saveAutoPoolConfig(poolID, updated).catch(() => {});
}

export async function resetAutoPoolToDefault(poolID: string): Promise<void> {
  const defaults = createDefaultAutoPools();
  const defPool = defaults[poolID] || {
    id: poolID,
    strategy: "round-robin" as AutoRotationStrategy,
    customized: false,
    candidates: [],
  };
  const next = { ...autoPoolsSignal(), [poolID]: defPool };
  setAutoPoolsSignal(next);
  persistAutoPoolsLocal(next);
  refreshWorkspaceAutoProvider();
  await hoxiAPI.deleteAutoPoolConfig(poolID).catch(() => {});
}

export async function createCustomAutoPool(
  rawID: string,
  name: string,
  strategy: AutoRotationStrategy = "round-robin"
): Promise<string> {
  const cleanID = rawID
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9-_]/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
  if (!cleanID) throw new Error("Pool ID is required");
  const initialCands = computeSmartDefaultCandidates("free").slice(0, 4);
  await saveAutoPool(cleanID, {
    id: cleanID,
    name: name.trim() || cleanID,
    preset: false,
    strategy,
    customized: true,
    candidates: initialCands,
  });
  return cleanID;
}

export async function deleteCustomAutoPool(poolID: string): Promise<void> {
  if ((PRESET_AUTO_POOL_IDS as readonly string[]).includes(poolID)) {
    await resetAutoPoolToDefault(poolID);
    return;
  }
  const next = { ...autoPoolsSignal() };
  delete next[poolID];
  setAutoPoolsSignal(next);
  persistAutoPoolsLocal(next);
  for (const [sid, pinned] of sessionAutoTarget.entries()) {
    if (pinned.poolID === poolID) sessionAutoTarget.delete(sid);
  }
  setWorkspace(
    produce((state) => {
      if (
        state.defaultAI.model?.providerID === AUTO_PROVIDER_ID &&
        state.defaultAI.model?.modelID === poolID
      ) {
        state.defaultAI.model = { providerID: AUTO_PROVIDER_ID, modelID: "free" };
      }
      for (const [sid, cfg] of Object.entries(state.aiBySession)) {
        if (cfg?.model?.providerID === AUTO_PROVIDER_ID && cfg.model.modelID === poolID) {
          state.aiBySession[sid] = {
            ...cfg,
            model: { providerID: AUTO_PROVIDER_ID, modelID: "free" },
          };
        }
      }
    })
  );
  refreshWorkspaceAutoProvider();
  await hoxiAPI.deleteAutoPoolConfig(poolID).catch(() => {});
}
