// web/src/store/auto-models.ts
import { createSignal } from "solid-js";
import { t } from "../i18n";
import { hoxiAPI, type ProviderInfo, type ProviderModelInfo } from "../services/api";
import { isModelFree } from "./provider-catalog";
import { setWorkspace, workspace, type SelectedModel } from "./workspace";

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
  titleKey:
    | "models.freeHubProviderOpenRouterTitle"
    | "models.freeHubProviderGeminiTitle"
    | "models.freeHubProviderGroqTitle"
    | "models.freeHubProviderSiliconTitle"
    | "models.freeHubProviderZhipuTitle"
    | "models.freeHubProviderModelScopeTitle";
  quotaKey:
    | "models.freeHubProviderOpenRouterQuota"
    | "models.freeHubProviderGeminiQuota"
    | "models.freeHubProviderGroqQuota"
    | "models.freeHubProviderSiliconQuota"
    | "models.freeHubProviderZhipuQuota"
    | "models.freeHubProviderModelScopeQuota";
  stepKey:
    | "models.freeHubProviderOpenRouterStep"
    | "models.freeHubProviderGeminiStep"
    | "models.freeHubProviderGroqStep"
    | "models.freeHubProviderSiliconStep"
    | "models.freeHubProviderZhipuStep"
    | "models.freeHubProviderModelScopeStep";
}

export const FREE_TIER_GUIDE_ITEMS: readonly FreeTierGuideItem[] = [
  {
    id: "openrouter-free",
    providerID: "openrouter",
    signupURL: "https://openrouter.ai/settings/keys",
    titleKey: "models.freeHubProviderOpenRouterTitle",
    quotaKey: "models.freeHubProviderOpenRouterQuota",
    stepKey: "models.freeHubProviderOpenRouterStep",
  },
  {
    id: "gemini-free",
    providerID: "google",
    signupURL: "https://aistudio.google.com/apikey",
    titleKey: "models.freeHubProviderGeminiTitle",
    quotaKey: "models.freeHubProviderGeminiQuota",
    stepKey: "models.freeHubProviderGeminiStep",
  },
  {
    id: "groq-free",
    providerID: "groq",
    signupURL: "https://console.groq.com/keys",
    titleKey: "models.freeHubProviderGroqTitle",
    quotaKey: "models.freeHubProviderGroqQuota",
    stepKey: "models.freeHubProviderGroqStep",
  },
  {
    id: "siliconflow-free",
    providerID: "siliconflow",
    customMode: true,
    baseURL: "https://api.siliconflow.cn/v1",
    defaultModels: [
      { id: "Qwen/Qwen2.5-Coder-7B-Instruct", name: "Qwen2.5 Coder 7B (Free)", reasoning: false, free: true },
      { id: "THUDM/GLM-4-9B-0414", name: "GLM-4 9B (Free)", reasoning: false, free: true },
      { id: "deepseek-ai/DeepSeek-R1-Distill-Qwen-7B", name: "DeepSeek R1 Distill 7B (Free)", reasoning: true, free: true },
    ],
    signupURL: "https://cloud.siliconflow.cn/account/ak",
    titleKey: "models.freeHubProviderSiliconTitle",
    quotaKey: "models.freeHubProviderSiliconQuota",
    stepKey: "models.freeHubProviderSiliconStep",
  },
  {
    id: "zhipu-free",
    providerID: "zai",
    signupURL: "https://open.bigmodel.cn/usercenter/apikeys",
    titleKey: "models.freeHubProviderZhipuTitle",
    quotaKey: "models.freeHubProviderZhipuQuota",
    stepKey: "models.freeHubProviderZhipuStep",
  },
  {
    id: "modelscope-free",
    providerID: "modelscope",
    customMode: true,
    baseURL: "https://api-inference.modelscope.cn/v1",
    defaultModels: [
      { id: "Qwen/Qwen3-Coder-480B-A35B-Instruct", name: "Qwen3 Coder 480B (Free Quota)", reasoning: true, free: true },
      { id: "deepseek-ai/DeepSeek-V3", name: "DeepSeek V3 (Free Quota)", reasoning: true, free: true },
    ],
    signupURL: "https://modelscope.cn/my/myaccesstoken",
    titleKey: "models.freeHubProviderModelScopeTitle",
    quotaKey: "models.freeHubProviderModelScopeQuota",
    stepKey: "models.freeHubProviderModelScopeStep",
  },
];

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
        if (/coder|claude|sonnet|gemini-2\.5|deepseek|qwen3|glm-4/i.test(lowerKey)) s += 40;
        if (p.source === "runner" || p.id !== "opencode") s += 20;
        freeList.push({ cand, score: s });
      }

      if (
        /deepseek-chat|deepseek-v3|coder|sonnet|gemini-2\.5-flash|gemini-3-flash|glm-4|glm-5|gpt-5-mini|gpt-4\.1|kimi|minimax/i.test(
          lowerKey
        ) ||
        free
      ) {
        let s = free ? 45 : 80;
        if (/sonnet|deepseek-v3|qwen3-coder|gemini-2\.5-flash|glm-4\.6|glm-5/i.test(lowerKey)) s += 30;
        dailyList.push({ cand, score: s });
      }

      if (
        /opus|sonnet-4|gpt-5|o3|gemini-2\.5-pro|gemini-3-pro|gemini-4|deepseek-r1|deepseek-reasoner|qwen3-max|qwq/i.test(
          lowerKey
        )
      ) {
        let s = 70;
        if (/opus|gpt-5|gemini-.*-pro|sonnet-4/i.test(lowerKey)) s += 35;
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
  if (pool?.customized && pool.candidates.length > 0) {
    const valid = pool.candidates.filter((c) => isRealModelAvailable(c.providerID, c.modelID));
    if (valid.length > 0) return valid;
  }
  return computeSmartDefaultCandidates(poolID);
}

export function isCandidateCoolingDown(providerID: string, modelID: string): boolean {
  const key = `${providerID}/${modelID}`;
  const entry = autoCooldownsSignal()[key];
  if (!entry) return false;
  return entry.until > Date.now();
}

export function getCandidateCooldownRemainingMs(providerID: string, modelID: string): number {
  const key = `${providerID}/${modelID}`;
  const entry = autoCooldownsSignal()[key];
  if (!entry) return 0;
  return Math.max(0, entry.until - Date.now());
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
    return;
  }
  const cands = getEffectivePoolCandidates(poolID);
  const next = { ...autoCooldownsSignal() };
  for (const c of cands) {
    delete next[`${c.providerID}/${c.modelID}`];
  }
  setAutoCooldownsSignal(next);
  persistCooldownsLocal(next);
}

function candidateHealthScore(c: AutoPoolCandidate): number {
  const pStats = workspace.providerTelemetry?.[c.providerID];
  if (!pStats) return 75;
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
  return score;
}

export function resolveAutoModelTarget(
  poolIDOrModel: string | SelectedModel,
  opts?: { advance?: boolean; excludeKeys?: Set<string> }
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

  const allCands = getEffectivePoolCandidates(poolID).filter(
    (c) => !opts?.excludeKeys?.has(`${c.providerID}/${c.modelID}`)
  );
  if (allCands.length === 0) return null;

  const readyCands = allCands.filter((c) => !isCandidateCoolingDown(c.providerID, c.modelID));
  const activePool =
    readyCands.length > 0
      ? readyCands
      : [...allCands].sort(
          (a, b) =>
            getCandidateCooldownRemainingMs(a.providerID, a.modelID) -
            getCandidateCooldownRemainingMs(b.providerID, b.modelID)
        );

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
  const virtualProv = buildAutoVirtualProvider();
  const others = workspace.providers.filter((p) => p.id !== AUTO_PROVIDER_ID);
  const nextConn = workspace.connectedProviders.includes(AUTO_PROVIDER_ID)
    ? workspace.connectedProviders
    : [AUTO_PROVIDER_ID, ...workspace.connectedProviders];
  setWorkspace({
    providers: [virtualProv, ...others],
    connectedProviders: nextConn,
  });
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
