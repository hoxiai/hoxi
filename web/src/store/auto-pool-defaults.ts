// web/src/store/auto-pool-defaults.ts
import { createSignal } from "solid-js";
import { leaderboardAPI } from "../services/api";
import { workspace } from "./workspace-state";

export const AUTO_PROVIDER_ID = "hoxi-auto";

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
