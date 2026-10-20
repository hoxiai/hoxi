// web/src/store/provider-catalog.ts
import { t } from "../i18n";
import type { ProviderInfo } from "../services/api";
import { leaderboardState } from "./leaderboard-state";
import type { ReasoningEffort } from "./session-ai";
import { workspace } from "./workspace";
import {
  isCustomOrConfiguredProvider,
  isHoxiCustomProvider,
  isImportedOpenCodeProvider,
} from "./workspace-providers";

export type ConcreteReasoningEffort = Exclude<ReasoningEffort, "">;

const CANONICAL_EFFORTS: readonly ConcreteReasoningEffort[] = [
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
];

function effortRank(e: string): number {
  const idx = CANONICAL_EFFORTS.indexOf(e as ConcreteReasoningEffort);
  return idx >= 0 ? idx + 1 : 0;
}

export function normalizeEffortList(raw?: readonly string[] | null): ConcreteReasoningEffort[] {
  if (!Array.isArray(raw) || raw.length === 0) return [];
  const set = new Set<ConcreteReasoningEffort>();
  for (const item of raw) {
    const lower = String(item || "").trim().toLowerCase() as ConcreteReasoningEffort;
    if (CANONICAL_EFFORTS.includes(lower)) set.add(lower);
  }
  return CANONICAL_EFFORTS.filter((e) => set.has(e));
}

export function clampReasoningEffort(
  requested: ReasoningEffort,
  supported?: readonly string[] | null,
  defaultEffort?: string
): ReasoningEffort {
  if (!requested) return "";
  const req = requested.trim().toLowerCase() as ConcreteReasoningEffort;
  if (!CANONICAL_EFFORTS.includes(req)) return "";
  const norm = normalizeEffortList(supported);
  if (norm.length === 0) {
    if (Array.isArray(supported)) return "";
    const def = (defaultEffort || "").trim().toLowerCase() as ConcreteReasoningEffort;
    return CANONICAL_EFFORTS.includes(def) ? def : req;
  }
  if (norm.includes(req)) return req;
  const reqRank = effortRank(req);
  let best = norm[0];
  let bestDist = Math.abs(effortRank(best) - reqRank);
  for (let i = 1; i < norm.length; i++) {
    const cand = norm[i];
    const dist = Math.abs(effortRank(cand) - reqRank);
    if (dist < bestDist || (dist === bestDist && effortRank(cand) > effortRank(best))) {
      best = cand;
      bestDist = dist;
    }
  }
  return best;
}

export function splitAgyModelEffortSuffix(
  modelID: string
): { base: string; effort: ConcreteReasoningEffort } | null {
  const trimmed = (modelID || "").trim();
  const lower = trimmed.toLowerCase();
  for (const eff of ["xhigh", "medium", "high", "low", "max"] as const) {
    const suf = `-${eff}`;
    if (lower.endsWith(suf) && trimmed.length > suf.length) {
      return { base: trimmed.slice(0, trimmed.length - suf.length), effort: eff };
    }
  }
  return null;
}

export function inferModelEffortCapabilities(
  providerID: string,
  modelID: string,
  reasoning: boolean,
  mInfo?: Record<string, any> | null,
  cfgModel?: Record<string, any> | null
): { efforts: ConcreteReasoningEffort[]; defaultEffort?: ConcreteReasoningEffort } {
  const explicitEfforts = normalizeEffortList(mInfo?.efforts ?? cfgModel?.efforts);
  const rawDef = String(mInfo?.defaultEffort ?? cfgModel?.defaultEffort ?? "")
    .trim()
    .toLowerCase() as ConcreteReasoningEffort;
  const explicitDefault = CANONICAL_EFFORTS.includes(rawDef) ? rawDef : undefined;

  if (!reasoning && explicitEfforts.length === 0) {
    return { efforts: [] };
  }
  if (explicitEfforts.length > 0) {
    return {
      efforts: explicitEfforts,
      defaultEffort:
        explicitDefault && explicitEfforts.includes(explicitDefault)
          ? explicitDefault
          : explicitEfforts.includes("medium")
          ? "medium"
          : explicitEfforts[0],
    };
  }

  const agySplit = splitAgyModelEffortSuffix(modelID);
  if (agySplit) {
    const lowerBase = agySplit.base.toLowerCase();
    if (lowerBase.includes("oss-")) {
      return { efforts: ["medium"], defaultEffort: "medium" };
    }
    return { efforts: ["low", "medium", "high"], defaultEffort: agySplit.effort };
  }

  const variantKeys = normalizeEffortList([
    ...Object.keys(mInfo?.variants || {}),
    ...Object.keys(cfgModel?.variants || {}),
  ]);
  if (variantKeys.length > 0) {
    return {
      efforts: variantKeys,
      defaultEffort:
        explicitDefault && variantKeys.includes(explicitDefault)
          ? explicitDefault
          : variantKeys.includes("high")
          ? "high"
          : variantKeys.includes("medium")
          ? "medium"
          : variantKeys[0],
    };
  }

  const p = (providerID || "").trim().toLowerCase();
  const s = normalizeModelLookupKey(modelID);

  if (s.includes("claude") || p === "claude-code" || p.includes("anthropic")) {
    if (s.includes("haiku")) return { efforts: [] };
    return { efforts: ["low", "medium", "high", "xhigh", "max"], defaultEffort: "high" };
  }

  if (p === "antigravity") {
    if (s.includes("oss-")) {
      return { efforts: ["medium"], defaultEffort: "medium" };
    }
    return { efforts: ["low", "medium", "high"], defaultEffort: "high" };
  }

  if (
    s.includes("gpt") ||
    /^o[134](-|\b)/.test(s) ||
    s.includes("codex") ||
    p.includes("openai") ||
    p.includes("codex")
  ) {
    if (s.includes("oss-")) {
      return { efforts: ["medium"], defaultEffort: "medium" };
    }
    return { efforts: ["low", "medium", "high", "xhigh"], defaultEffort: "high" };
  }

  if (s.includes("gemini") || p.includes("google")) {
    return { efforts: ["low", "medium", "high", "max"], defaultEffort: "high" };
  }

  return { efforts: ["low", "medium", "high"], defaultEffort: "medium" };
}

export interface FlattenedModel {
  providerID: string;
  providerName: string;
  modelID: string;
  modelName: string;
  connected: boolean;
  isCustom: boolean;
  source?: string;
  baseURL?: string;
  reasoning: boolean;
  efforts?: ConcreteReasoningEffort[];
  defaultEffort?: ConcreteReasoningEffort;
  image: boolean;
  toolcall: boolean;
  contextLimit?: number;
  outputLimit?: number;
  inputCost?: number;
  outputCost?: number;
  priceCurrency?: string;
  multiplier?: string;
  free: boolean;
}

export interface ModelSourceInfo {
  label: string;
  configHint?: string;
  isImported: boolean;
}

export interface ConnectedProviderGroup {
  providerID: string;
  providerName: string;
  models: Array<{ id: string; name: string; fullKey: string; free: boolean; multiplier?: string }>;
}

export function getModelSourceInfo(model: FlattenedModel): ModelSourceInfo {
  const pid = model.providerID;
  if (pid === "antigravity") {
    return {
      label: t("timeline.runtimeAntigravityLocal"),
      configHint: "agy --sandbox",
      isImported: false,
    };
  }
  if (pid === "claude-code" && !isHoxiCustomProvider(pid)) {
    return {
      label: t("timeline.runtimeClaudeCodeLocal"),
      configHint: "claude",
      isImported: false,
    };
  }
  if (pid === "codebuddy" && !isHoxiCustomProvider(pid)) {
    return {
      label: t("timeline.runtimeCodeBuddyLocal"),
      configHint: "codebuddy",
      isImported: false,
    };
  }
  if (pid === "qoder" && !isHoxiCustomProvider(pid)) {
    return {
      label: t("timeline.runtimeQoderLocal"),
      configHint: "qodercli / qoderclicn",
      isImported: false,
    };
  }
  if (isHoxiCustomProvider(pid)) {
    return {
      label: t("models.sourceHoxi"),
      configHint: ".hoxi/models.json",
      isImported: false,
    };
  }
  if (isImportedOpenCodeProvider(pid)) {
    return {
      label: t("models.sourceOpenCode"),
      configHint: "opencode",
      isImported: true,
    };
  }
  if (model.isCustom) {
    return {
      label: t("models.sourceCustom"),
      isImported: false,
    };
  }
  if (pid === "opencode" && model.free && !workspace.connectedProviders.includes("opencode")) {
    return {
      label: t("models.sourceBuiltInFree"),
      isImported: false,
    };
  }
  if (model.source === "env") {
    return { label: t("models.sourceEnv"), isImported: false };
  }
  if (model.source === "config") {
    return { label: t("models.sourceConfig"), isImported: false };
  }
  return { label: t("models.sourceApi"), isImported: false };
}

export function formatTokenLimit(n?: number): string | null {
  if (!n || n <= 0) return null;
  if (n >= 1_000_000) {
    const m = n / 1_000_000;
    return `${Number.isInteger(m) ? m : m.toFixed(1)}M`;
  }
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(n);
}

export function formatPricePerMillion(cost?: number, currency?: string): string | null {
  if (cost === undefined || cost === null) return null;
  const sym = currency?.toUpperCase() === "CNY" ? "¥" : "$";
  if (cost === 0) return `${sym}0`;
  const val = cost < 0.001 ? cost * 1_000_000 : cost;
  return `${sym}${val < 1 ? val.toFixed(2) : val.toFixed(2).replace(/\.00$/, "")}/M`;
}

export function isModelFree(
  providerID: string,
  modelID: string,
  mInfo?: Record<string, any> | null
): boolean {
  if (mInfo?.free === true || mInfo?.multiplier === "0x") return true;
  if (providerID !== "opencode") return false;
  const inCost = mInfo?.cost?.input;
  return !mInfo?.cost || inCost === 0 || modelID.endsWith("-free");
}

interface InferredModelSpec {
  inputCost?: number;
  outputCost?: number;
  currency?: string;
  contextLimit?: number;
  outputLimit?: number;
}

function normalizeModelLookupKey(raw: string): string {
  let s = (raw || "").trim().toLowerCase();
  for (const pfx of ["cn:", "intl:", "global:", "models/"]) {
    if (s.startsWith(pfx)) s = s.slice(pfx.length);
  }
  const slashIdx = s.lastIndexOf("/");
  if (slashIdx >= 0 && slashIdx < s.length - 1) {
    s = s.slice(slashIdx + 1);
  }
  return s;
}

export function canonicalModelKey(raw?: string): string {
  if (!raw) return "";
  return normalizeModelLookupKey(raw)
    .replace(/（.*?）|\(.*?\)/g, "")
    .trim()
    .replace(/[._\s]+/g, "-")
    .replace(/-\d{8}$/, "")
    .replace(/-thinking$/, "");
}

function inferOfficialModelPricingAndLimits(
  modelID: string,
  modelName?: string
): InferredModelSpec {
  const s = `${normalizeModelLookupKey(modelID)} ${(modelName || "").toLowerCase()}`;

  // Anthropic Claude family
  if (s.includes("claude")) {
    if (s.includes("opus")) {
      return { inputCost: 5.0, outputCost: 25.0, contextLimit: 200_000, outputLimit: 64_000 };
    }
    if (s.includes("haiku")) {
      return { inputCost: 0.8, outputCost: 4.0, contextLimit: 200_000, outputLimit: 64_000 };
    }
    return { inputCost: 3.0, outputCost: 15.0, contextLimit: 200_000, outputLimit: 64_000 };
  }

  // Google Gemini family
  if (s.includes("gemini")) {
    if (s.includes("lite")) {
      return { inputCost: 0.1, outputCost: 0.4, contextLimit: 1_000_000, outputLimit: 65_536 };
    }
    if (s.includes("flash")) {
      return { inputCost: 0.35, outputCost: 1.4, contextLimit: 1_000_000, outputLimit: 65_536 };
    }
    return { inputCost: 1.25, outputCost: 10.0, contextLimit: 1_000_000, outputLimit: 65_536 };
  }

  // OpenAI GPT / o-series / Codex family
  if (
    s.startsWith("gpt") ||
    s.includes(" gpt-") ||
    /^o[134](-|\b)/.test(s) ||
    s.includes("codex")
  ) {
    if (s.includes("nano")) {
      return { inputCost: 0.1, outputCost: 0.4, contextLimit: 400_000, outputLimit: 128_000 };
    }
    if (s.includes("mini") || s.includes("luna")) {
      return { inputCost: 0.25, outputCost: 1.0, contextLimit: 400_000, outputLimit: 128_000 };
    }
    if (s.includes("astra") || s.includes("gpt-6")) {
      return { inputCost: 6.0, outputCost: 24.0, contextLimit: 1_000_000, outputLimit: 128_000 };
    }
    if (s.includes("sol") || s.includes("gpt-5.5")) {
      return { inputCost: 3.0, outputCost: 12.0, contextLimit: 1_000_000, outputLimit: 128_000 };
    }
    return { inputCost: 2.0, outputCost: 8.0, contextLimit: 400_000, outputLimit: 128_000 };
  }

  // DeepSeek family
  if (s.includes("deepseek")) {
    if (s.includes("flash") || s.includes("lite")) {
      return { inputCost: 0.14, outputCost: 0.28, contextLimit: 1_000_000, outputLimit: 64_000 };
    }
    if (s.includes("pro") || s.includes("r1") || s.includes("reasoner") || s.includes("v4")) {
      return { inputCost: 0.55, outputCost: 2.19, contextLimit: 1_000_000, outputLimit: 128_000 };
    }
    return { inputCost: 0.27, outputCost: 1.1, contextLimit: 128_000, outputLimit: 32_000 };
  }

  // Alibaba Qwen / QwQ family
  if (s.includes("qwen") || s.startsWith("qwq")) {
    if (s.includes("flash") || s.includes("turbo")) {
      return { inputCost: 0.15, outputCost: 0.6, contextLimit: 1_000_000, outputLimit: 64_000 };
    }
    if (s.includes("plus") || s.includes("coder") || s.startsWith("qwq")) {
      return { inputCost: 0.4, outputCost: 1.2, contextLimit: 1_000_000, outputLimit: 64_000 };
    }
    return { inputCost: 1.2, outputCost: 6.0, contextLimit: 1_000_000, outputLimit: 64_000 };
  }

  // Zhipu GLM / CodeGeeX family
  if (s.includes("glm") || s.includes("codegeex")) {
    if (s.includes("flash") || s.includes("air")) {
      return { inputCost: 0.1, outputCost: 0.4, contextLimit: 1_000_000, outputLimit: 64_000 };
    }
    return { inputCost: 0.7, outputCost: 2.8, contextLimit: 1_000_000, outputLimit: 64_000 };
  }

  // Moonshot Kimi family
  if (s.includes("kimi") || s.includes("moonshot")) {
    if (s.includes("code") || s.includes("k2.7")) {
      return { inputCost: 0.6, outputCost: 2.5, contextLimit: 256_000, outputLimit: 32_000 };
    }
    if (s.includes("preview") || s.includes("k2")) {
      return { inputCost: 0.8, outputCost: 3.2, contextLimit: 1_000_000, outputLimit: 64_000 };
    }
    return { inputCost: 1.5, outputCost: 6.0, contextLimit: 1_000_000, outputLimit: 64_000 };
  }

  // Tencent Hunyuan (hy3 / hy4 / hunyuan) family
  if (/^hy[345]/.test(s) || s.includes("hunyuan")) {
    if (s.startsWith("hy3")) {
      return { inputCost: 0.08, outputCost: 0.3, contextLimit: 192_000, outputLimit: 64_000 };
    }
    return { inputCost: 0.3, outputCost: 1.2, contextLimit: 1_000_000, outputLimit: 64_000 };
  }

  // xAI Grok family
  if (s.includes("grok")) {
    if (s.includes("mini") || s.includes("fast")) {
      return { inputCost: 0.3, outputCost: 0.5, contextLimit: 131_072, outputLimit: 32_000 };
    }
    return { inputCost: 3.0, outputCost: 15.0, contextLimit: 256_000, outputLimit: 64_000 };
  }

  // ByteDance Doubao / Seed family
  if (s.includes("doubao") || s.includes("seed-")) {
    if (s.includes("lite") || s.includes("flash")) {
      return { inputCost: 0.1, outputCost: 0.3, contextLimit: 256_000, outputLimit: 32_000 };
    }
    return { inputCost: 0.3, outputCost: 1.2, contextLimit: 256_000, outputLimit: 64_000 };
  }

  // Mistral / Codestral family
  if (s.includes("mistral") || s.includes("codestral") || s.includes("pixtral")) {
    if (s.includes("codestral") || s.includes("small")) {
      return { inputCost: 0.3, outputCost: 0.9, contextLimit: 256_000, outputLimit: 32_000 };
    }
    return { inputCost: 2.0, outputCost: 6.0, contextLimit: 128_000, outputLimit: 32_000 };
  }

  return {};
}

export function extractFlattenedModels(
  providers: ProviderInfo[],
  connectedIds: Set<string>,
  globalConfig: Record<string, any>
): FlattenedModel[] {
  const disabledSet = new Set<string>(
    Array.isArray(globalConfig?.disabled_providers) ? globalConfig.disabled_providers : []
  );
  const cfgProviders = globalConfig?.provider ?? {};
  const out: FlattenedModel[] = [];

  // 1. Index official pricing synced from https://hoxi.ai/api/hoxi/models
  const hoxiSpecs = new Map<string, InferredModelSpec>();
  for (const item of leaderboardState.pricingCatalog || []) {
    if (!item) continue;
    const hasPrice = Boolean(item.isFree || (item.price && (item.price.input > 0 || item.price.output > 0)));
    if (!hasPrice && !item.contextWindow) continue;
    const spec: InferredModelSpec = {
      inputCost: item.isFree ? 0 : hasPrice ? item.price.input : undefined,
      outputCost: item.isFree ? 0 : hasPrice ? item.price.output : undefined,
      currency: item.price?.currency || "USD",
      contextLimit: item.contextWindow || undefined,
      outputLimit: item.maxOutputTokens || undefined,
    };
    for (const rawKey of [item.slug, item.apiName, item.gatewayModelName, item.aaSlug, item.name]) {
      const k = canonicalModelKey(rawKey);
      if (k) hoxiSpecs.set(k, spec);
    }
  }

  // 2. Index known official pricing & token limits from OpenCode's built-in catalog by canonical key.
  const catalogSpecs = new Map<string, InferredModelSpec>();
  for (const p of providers) {
    for (const [mID, mInfo] of Object.entries(p.models || {})) {
      if (!mInfo || typeof mInfo !== "object") continue;
      const inCost = mInfo.cost?.input;
      const outCost = mInfo.cost?.output;
      const ctx = mInfo.limit?.context;
      const outLim = mInfo.limit?.output;
      if (inCost !== undefined || outCost !== undefined || ctx || outLim) {
        const key = canonicalModelKey(mID);
        if (!key) continue;
        const prev = catalogSpecs.get(key);
        catalogSpecs.set(key, {
          inputCost: inCost !== undefined && inCost > 0 ? inCost : prev?.inputCost,
          outputCost: outCost !== undefined && outCost > 0 ? outCost : prev?.outputCost,
          currency: prev?.currency || "USD",
          contextLimit: ctx || prev?.contextLimit,
          outputLimit: outLim || prev?.outputLimit,
        });
      }
    }
  }

  for (const p of providers) {
    if (disabledSet.has(p.id)) continue;
    const isCustom = isCustomOrConfiguredProvider(p);
    const baseConn = connectedIds.has(p.id) || Boolean(cfgProviders[p.id]);
    const baseURL =
      workspace.hoxiProviders?.[p.id]?.options?.baseURL ||
      cfgProviders?.[p.id]?.options?.baseURL ||
      p.options?.baseURL;

    for (const [mID, mInfo] of Object.entries(p.models || {})) {
      if (!mInfo || typeof mInfo !== "object") continue;
      const cfgModel = cfgProviders?.[p.id]?.models?.[mID];
      const modelName = mInfo.name || cfgModel?.name || mID;
      const rawReasoning = Boolean(
        mInfo.capabilities?.reasoning ?? mInfo.reasoning ?? cfgModel?.reasoning
      );
      const effortCaps = inferModelEffortCapabilities(p.id, mID, rawReasoning, mInfo, cfgModel);
      const reasoning = rawReasoning || effortCaps.efforts.length > 0;
      const image = Boolean(
        mInfo.capabilities?.input?.image ||
          (Array.isArray(mInfo.modalities?.input) && mInfo.modalities.input.includes("image")) ||
          (Array.isArray(cfgModel?.modalities?.input) &&
            cfgModel.modalities.input.includes("image"))
      );
      const toolcall = Boolean(mInfo.capabilities?.toolcall ?? mInfo.tool_call ?? true);
      const multiplier = mInfo.multiplier ?? cfgModel?.multiplier;
      const free = isModelFree(p.id, mID, mInfo);
      const isConn = baseConn || (p.id === "opencode" && free);

      const canonID = canonicalModelKey(mID);
      const canonName = canonicalModelKey(modelName);
      const hoxiSpec = hoxiSpecs.get(canonID) || hoxiSpecs.get(canonName);
      const catSpec = catalogSpecs.get(canonID);
      const prefixSpec = inferOfficialModelPricingAndLimits(mID, modelName);

      let inCost: number | undefined;
      let outCost: number | undefined;
      let priceCurrency: string | undefined;

      if (free) {
        inCost = 0;
        outCost = 0;
        priceCurrency = hoxiSpec?.currency || "USD";
      } else if (cfgModel?.cost?.input !== undefined || cfgModel?.cost?.output !== undefined) {
        inCost = cfgModel?.cost?.input;
        outCost = cfgModel?.cost?.output;
        priceCurrency = cfgModel?.cost?.currency || "USD";
      } else if (hoxiSpec?.inputCost !== undefined || hoxiSpec?.outputCost !== undefined) {
        inCost = hoxiSpec.inputCost;
        outCost = hoxiSpec.outputCost;
        priceCurrency = hoxiSpec.currency || "USD";
      } else {
        inCost = mInfo.cost?.input ?? catSpec?.inputCost ?? prefixSpec.inputCost;
        outCost = mInfo.cost?.output ?? catSpec?.outputCost ?? prefixSpec.outputCost;
        priceCurrency = mInfo.cost?.currency || "USD";
      }

      const contextLimit =
        mInfo.limit?.context ??
        cfgModel?.limit?.context ??
        hoxiSpec?.contextLimit ??
        catSpec?.contextLimit ??
        prefixSpec.contextLimit;
      const outputLimit =
        mInfo.limit?.output ??
        cfgModel?.limit?.output ??
        hoxiSpec?.outputLimit ??
        catSpec?.outputLimit ??
        prefixSpec.outputLimit;

      out.push({
        providerID: p.id,
        providerName: p.name || p.id,
        modelID: mID,
        modelName,
        connected: isConn,
        isCustom,
        source: p.source,
        baseURL,
        reasoning,
        efforts: effortCaps.efforts,
        defaultEffort: effortCaps.defaultEffort,
        image,
        toolcall,
        contextLimit,
        outputLimit,
        inputCost: inCost,
        outputCost: outCost,
        priceCurrency,
        multiplier,
        free,
      });
    }
  }
  return out;
}

export function rankModelSearch(models: FlattenedModel[], rawQuery: string): FlattenedModel[] {
  const q = rawQuery.trim().toLowerCase();
  if (!q) return models;
  const tokens = q.split(/\s+/).filter(Boolean);

  const scored: Array<{ item: FlattenedModel; score: number }> = [];
  for (const m of models) {
    const idLower = m.modelID.toLowerCase();
    const nameLower = m.modelName.toLowerCase();
    const provLower = `${m.providerName} ${m.providerID}`.toLowerCase();
    const freeTag = m.free ? `free 0x ${t("models.freeModel").toLowerCase()}` : "";
    const multTag = m.multiplier ? m.multiplier.toLowerCase() : "";
    const haystack = `${idLower} ${nameLower} ${provLower} ${freeTag} ${multTag}`;

    if (!tokens.every((tok) => haystack.includes(tok))) continue;

    let score = 0;
    if (idLower === q || nameLower === q) score += 200;
    else if (idLower.startsWith(q) || nameLower.startsWith(q)) score += 120;
    else if (idLower.includes(q) || nameLower.includes(q)) score += 70;
    if (m.connected) score += 40;
    if (m.free) score += 25;
    if (m.isCustom) score += 20;

    scored.push({ item: m, score });
  }

  return scored
    .sort((a, b) => b.score - a.score || a.item.modelName.localeCompare(b.item.modelName))
    .map((s) => s.item);
}

export function getConnectedProviderModelGroups(): ConnectedProviderGroup[] {
  const connSet = new Set(workspace.connectedProviders);
  const disSet = new Set<string>(
    Array.isArray(workspace.globalConfig?.disabled_providers)
      ? workspace.globalConfig.disabled_providers
      : []
  );
  const cfgProviders = workspace.globalConfig?.provider ?? {};
  const groups: ConnectedProviderGroup[] = [];

  for (const p of workspace.providers) {
    if (disSet.has(p.id)) continue;
    const isConn = connSet.has(p.id) || Boolean(cfgProviders[p.id]) || p.id === "opencode";
    if (!isConn) continue;

    const models = Object.entries(p.models || {}).map(([mID, mInfo]) => ({
      id: mID,
      name: mInfo?.name || mID,
      fullKey: `${p.id}/${mID}`,
      free: isModelFree(p.id, mID, mInfo),
      multiplier: mInfo?.multiplier,
    }));

    if (models.length > 0) {
      groups.push({
        providerID: p.id,
        providerName: p.name || p.id,
        models,
      });
    }
  }

  return groups.sort((a, b) => {
    const aCustom = isCustomOrConfiguredProvider(a.providerID);
    const bCustom = isCustomOrConfiguredProvider(b.providerID);
    if (aCustom !== bCustom) return aCustom ? -1 : 1;
    return a.providerName.localeCompare(b.providerName);
  });
}
