// web/src/store/auto-models.ts
import { createSignal } from "solid-js";
import { t } from "../i18n";
import { hoxiAPI, leaderboardAPI, type ProviderInfo, type ProviderModelInfo } from "../services/api";
import { setWorkspace, workspace, type SelectedModel } from "./workspace-state";
import {
  candidateBreakerRemainingMs,
  candidateBreakerScore,
  clearCandidateBreaker,
  filterSlowCandidates,
  isCandidateBreakerOpen,
} from "./auto-breaker";

export const AUTO_PROVIDER_ID = "hoxi-auto";
export const AUTO_POOLS_STORAGE_KEY = "hoxi-auto-pools:v1";
export const AUTO_COOLDOWNS_STORAGE_KEY = "hoxi-auto-cooldowns:v1";

export const PRESET_AUTO_POOL_IDS = ["free", "daily", "frontier"] as const;
export type PresetAutoPoolID = (typeof PRESET_AUTO_POOL_IDS)[number];
export type AutoRotationStrategy = "round-robin" | "priority" | "smart-health";

export interface AutoPoolCandidate {
  providerID: string;
  modelID: string;
}

export interface AutoModelPoolConfig {
  id: string;
  name?: string;
  preset?: boolean;
  strategy: AutoRotationStrategy;
  customized: boolean;
  candidates: AutoPoolCandidate[];
}

export interface AutoCandidateCooldown {
  until: number;
  reason: string;
  statusCode?: number;
}

export interface FreeTierGuideItem {
  id: string;
  providerID: string;
  customMode?: boolean;
  baseURL?: string;
  defaultModels?: Array<{ id: string; name: string; reasoning: boolean; free: boolean }>;
  signupURL: string;
  title: string;
  badge?: string;
  quota: string;
  step: string;
  recommendedModels?: string[];
}

const [remoteFreeTierGuides, setRemoteFreeTierGuides] = createSignal<FreeTierGuideItem[]>([]);
let lastFreeTierFetchLang = "";

export function getFreeTierGuideItems(): readonly FreeTierGuideItem[] {
  return remoteFreeTierGuides();
}

export async function fetchFreeTierGuides(lang: string = "zh"): Promise<readonly FreeTierGuideItem[]> {
  const normalizedLang = lang.toLowerCase().startsWith("zh") ? "zh" : "en";
  if (remoteFreeTierGuides().length > 0 && lastFreeTierFetchLang === normalizedLang) {
    return remoteFreeTierGuides();
  }
  try {
    const items = await leaderboardAPI.getFreeTiers(normalizedLang);
    if (Array.isArray(items)) {
      const mapped: FreeTierGuideItem[] = items.map((item) => {
        const recModels = Array.isArray(item.recommendedModels) ? item.recommendedModels : [];
        return {
          id: item.id,
          providerID: item.providerID,
          customMode: Boolean(item.customMode),
          baseURL: item.baseUrl,
          signupURL: item.signupUrl,
          title: item.name,
          badge: item.badge,
          quota: item.quotaLabel,
          step: Array.isArray(item.steps) && item.steps.length > 0
            ? item.steps.map((s: string, idx: number) => `${idx + 1}. ${s}`).join(" · ")
            : "",
          recommendedModels: recModels,
          defaultModels: recModels.map((m: string) => ({
            id: m,
            name: m,
            reasoning: /r1|qwq|reason|thinking/i.test(m),
            free: true,
          })),
        };
      });
      lastFreeTierFetchLang = normalizedLang;
      setRemoteFreeTierGuides(mapped);
      return mapped;
    }
  } catch {
    /* ignore */
  }
  return remoteFreeTierGuides();
}

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
  try {
    const raw = localStorage.getItem(AUTO_POOLS_STORAGE_KEY);
    if (raw) return normalizeAutoPoolsRecord(JSON.parse(raw));
  } catch {
    /* ignore */
  }
  return createDefaultAutoPools();
}

function loadInitialCooldowns(): Record<string, AutoCandidateCooldown> {
  try {
    const raw = localStorage.getItem(AUTO_COOLDOWNS_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
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
  } catch {
    return {};
  }
}

const [autoPoolsSignal, setAutoPoolsSignal] = createSignal<Record<string, AutoModelPoolConfig>>(
  loadInitialAutoPools()
);
const [autoCooldownsSignal, setAutoCooldownsSignal] = createSignal<
  Record<string, AutoCandidateCooldown>
>(loadInitialCooldowns());
const [providerKeyCountsSignal, setProviderKeyCountsSignal] = createSignal<Record<string, number>>(
  {}
);
const [poolCursorsSignal, setPoolCursorsSignal] = createSignal<Record<string, number>>({});
const [lastResolvedByPoolSignal, setLastResolvedByPoolSignal] = createSignal<
  Record<string, SelectedModel>
>({});

// Session affinity: once a session resolves an Auto pool it keeps reusing the
// same candidate turn after turn while that candidate stays healthy, so the
// upstream prompt-cache prefix stays warm instead of failing over to a cold
// model on every turn. Rotation resumes only when the pinned candidate is gone,
// cooling down or excluded. Keyed by sessionID, value is pool + candidate.
const sessionAutoTarget = new Map<string, { poolID: string; providerID: string; modelID: string }>();

function persistAutoPoolsLocal(pools: Record<string, AutoModelPoolConfig>) {
  try {
    localStorage.setItem(AUTO_POOLS_STORAGE_KEY, JSON.stringify(pools));
  } catch {
    /* ignore */
  }
}

function persistCooldownsLocal(cooldowns: Record<string, AutoCandidateCooldown>) {
  try {
    localStorage.setItem(AUTO_COOLDOWNS_STORAGE_KEY, JSON.stringify(cooldowns));
  } catch {
    /* ignore */
  }
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

function isRealModelAvailable(providerID: string, modelID: string): boolean {
  if (!providerID || !modelID || providerID === AUTO_PROVIDER_ID) return false;
  return workspace.providers.some(
    (p) => p.id === providerID && p.models && Boolean(p.models[modelID])
  );
}

export function computeSmartDefaultCandidates(poolID: string): AutoPoolCandidate[] {
  const connSet = new Set(workspace.connectedProviders);
  const disSet = new Set<string>(
    Array.isArray(workspace.globalConfig?.disabled_providers)
      ? workspace.globalConfig.disabled_providers
      : []
  );
  const cfgProviders = workspace.globalConfig?.provider ?? {};

  const freeList: Array<{ cand: AutoPoolCandidate; score: number }> = [];
  const dailyList: Array<{ cand: AutoPoolCandidate; score: number }> = [];
  const frontierList: Array<{ cand: AutoPoolCandidate; score: number }> = [];

  for (const p of workspace.providers) {
    if (p.id === AUTO_PROVIDER_ID || disSet.has(p.id)) continue;
    const isConn = connSet.has(p.id) || Boolean(cfgProviders[p.id]);
    for (const [mID, mInfo] of Object.entries(p.models || {})) {
      if (!mInfo || typeof mInfo !== "object") continue;
      const free = isModelFree(p.id, mID, mInfo);
      if (!isConn && !(p.id === "opencode" && free)) continue;
      const toolcall = mInfo.capabilities?.toolcall ?? mInfo.tool_call ?? true;
      if (!toolcall) continue;

      const lowerKey = `${p.id}/${mID}`.toLowerCase();
      const cand: AutoPoolCandidate = { providerID: p.id, modelID: mID };

      if (free) {
        let s = 50;
        if (/coder|claude|sonnet|gemini|deepseek|qwen|glm|kimi|minimax/i.test(lowerKey)) s += 40;
        if (p.source === "runner" || p.id !== "opencode") s += 20;
        freeList.push({ cand, score: s });
      }

      if (
        /deepseek|coder|sonnet|gemini.*flash|glm|gpt-.*(mini|nano|luna)|gpt-4|kimi|minimax|qwen.*(plus|coder|flash)/i.test(
          lowerKey
        ) ||
        free
      ) {
        let s = free ? 45 : 80;
        if (/sonnet|deepseek|qwen.*coder|gemini.*flash|glm/i.test(lowerKey)) s += 30;
        dailyList.push({ cand, score: s });
      }

      if (
        /opus|fable|sonnet|gpt-(?!.*(mini|nano|luna))|\/o\d+(-|\b)|gemini.*(pro|ultra|argon)|deepseek-(r\d+|reasoner|.*pro)|qwen.*max|qwq/i.test(
          lowerKey
        )
      ) {
        let s = 70;
        if (/opus|fable|gpt-(?!.*(mini|nano|luna))|gemini.*(pro|ultra|argon)|sonnet/i.test(lowerKey))
          s += 35;
        if (!free) s += 15;
        frontierList.push({ cand, score: s });
      }
    }
  }

  const pickSorted = (arr: Array<{ cand: AutoPoolCandidate; score: number }>, limit = 12) =>
    arr
      .sort((a, b) => b.score - a.score)
      .slice(0, limit)
      .map((x) => x.cand);

  if (poolID === "free") {
    const res = pickSorted(freeList, 14);
    return res.length > 0 ? res : pickSorted(dailyList, 6);
  }
  if (poolID === "frontier") {
    const res = pickSorted(frontierList, 10);
    return res.length > 0 ? res : pickSorted(dailyList, 8);
  }
  const res = pickSorted(dailyList, 12);
  return res.length > 0 ? res : pickSorted(freeList, 8);
}

export function getEffectivePoolCandidates(poolID: string): AutoPoolCandidate[] {
  const pool = autoPoolsSignal()[poolID];
  // A customized pool is authoritative, including when the user deleted every
  // candidate. Falling back to the smart defaults here would resurrect the
  // models that were just removed and make deletion look like a no-op loop.
  if (pool?.customized) {
    return pool.candidates.filter((c) => isRealModelAvailable(c.providerID, c.modelID));
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
  if (typeof mStats.lastAuditScore === "number" && mStats.lastAuditScore > 0) {
    score = score * 0.7 + mStats.lastAuditScore * 0.3;
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

const NON_CHAT_UTILITY_MODEL_RE =
  /embedding|embed|rerank|bge-|whisper|tts|speech|moderation/i;

export function isNonChatUtilityModelID(modelID?: string | null): boolean {
  if (!modelID) return false;
  return NON_CHAT_UTILITY_MODEL_RE.test(modelID.trim());
}

export function modelSupportsImageInput(providerID: string, modelID: string): boolean {
  if (!providerID || !modelID) return true;
  if (isNonChatUtilityModelID(modelID)) {
    return false;
  }
  const hoxiModel = workspace.hoxiProviders?.[providerID]?.models?.[modelID];
  if (hoxiModel && typeof hoxiModel === "object") {
    if (hoxiModel.attachment === false) return false;
    if (
      Array.isArray(hoxiModel.modalities?.input) &&
      !hoxiModel.modalities.input.includes("image") &&
      hoxiModel.attachment !== true
    ) {
      return false;
    }
    return true;
  }
  const prov = workspace.providers.find((p) => p.id === providerID);
  const mInfo = prov?.models?.[modelID];
  if (!mInfo || typeof mInfo !== "object") return true;
  if (mInfo.attachment === false) return false;
  if (
    mInfo.capabilities?.input?.image === false &&
    Array.isArray(mInfo.modalities?.input) &&
    !mInfo.modalities.input.includes("image")
  ) {
    return false;
  }
  return true;
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
  // strategy, so a slow-but-working model stops absorbing the rotation.
  const activePool = filterSlowCandidates(basePool);

  // Session affinity short-circuit: reuse the candidate already pinned to this
  // session when it survived the exclude / image / cooldown / slowness filters
  // above. This keeps the prompt cache warm and does not consume a pool cursor
  // slot, so other sessions still rotate fairly.
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
  if (
    workspace.selectedModel?.providerID === AUTO_PROVIDER_ID &&
    workspace.selectedModel?.modelID === poolID
  ) {
    setWorkspace("defaultAI", "model", { providerID: AUTO_PROVIDER_ID, modelID: "free" });
  }
  refreshWorkspaceAutoProvider();
  await hoxiAPI.deleteAutoPoolConfig(poolID).catch(() => {});
}

export function isModelFree(
  providerID: string,
  modelID: string,
  mInfo?: Record<string, any> | null
): boolean {
  if (mInfo?.free === true || mInfo?.multiplier === "0x") return true;
  if (modelID.endsWith("-free") || modelID.endsWith(":free")) return true;
  if (
    mInfo?.cost &&
    mInfo.cost.input === 0 &&
    (mInfo.cost.output === 0 || mInfo.cost.output === undefined)
  ) {
    return true;
  }
  if (providerID !== "opencode" && providerID !== "opencode-go") return false;
  return !mInfo?.cost;
}
