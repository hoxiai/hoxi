// web/src/store/provider-catalog.ts
import { t } from "../i18n";
import type { ProviderInfo } from "../services/api";
import { AUTO_PROVIDER_ID, buildAutoVirtualProvider, isModelFree } from "./auto-models";
import { leaderboardState } from "./leaderboard-state";
import {
  canonicalModelKey,
  inferOfficialModelPricingAndLimits,
  normalizeModelLookupKey,
  resolveModelContextLimit,
  type InferredModelSpec,
} from "./model-limits";
import { workspace, type ReasoningEffort, type SelectedModel } from "./workspace-state";

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
    /^o\d+(-|\b)/.test(s) ||
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

export function getProviderSourceLabel(p: ProviderInfo): string {
  if (p.source === "runner") {
    if (p.id === "antigravity") return t("timeline.runtimeAntigravityLocal");
    if (p.id === "codebuddy") return t("timeline.runtimeCodeBuddyLocal");
    if (p.id === "qoder") return t("timeline.runtimeQoderLocal");
    return t("timeline.runtimeClaudeCodeLocal");
  }
  if (isHoxiCustomProvider(p.id)) return t("models.sourceHoxi");
  if (isImportedOpenCodeProvider(p.id)) return t("models.sourceOpenCode");
  if (isCustomOrConfiguredProvider(p)) return t("models.sourceCustom");
  if (p.source === "env") return t("models.sourceEnv");
  if (p.source === "config") return t("models.sourceConfig");
  return t("models.sourceApi");
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
      const outLim = mInfo.limit?.output;
      if (inCost !== undefined || outCost !== undefined || outLim) {
        const key = canonicalModelKey(mID);
        if (!key) continue;
        const prev = catalogSpecs.get(key);
        catalogSpecs.set(key, {
          inputCost: inCost !== undefined && inCost > 0 ? inCost : prev?.inputCost,
          outputCost: outCost !== undefined && outCost > 0 ? outCost : prev?.outputCost,
          currency: prev?.currency || "USD",
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
          mInfo.capabilities?.attachment ||
          mInfo.attachment ||
          cfgModel?.attachment ||
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

      const contextLimit = resolveModelContextLimit({
        providerID: p.id,
        modelID: mID,
        modelName,
        model: mInfo,
        configured: cfgModel,
      });
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

export interface SearchableModelEntry {
  providerID: string;
  providerName: string;
  modelID: string;
  modelName: string;
  free?: boolean;
  multiplier?: string;
  connected?: boolean;
  isCustom?: boolean;
}

export function rankModelSearch<T extends SearchableModelEntry>(models: T[], rawQuery: string): T[] {
  const q = rawQuery.trim().toLowerCase();
  if (!q) return models;
  const tokens = q.split(/\s+/).filter(Boolean);

  const scored: Array<{ item: T; score: number }> = [];
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
    if (provLower.includes(q)) score += 15;
    if (m.connected) score += 40;
    if (m.free) score += 25;
    if (m.isCustom) score += 20;

    scored.push({ item: m, score });
  }

  return scored
    .sort((a, b) => b.score - a.score || a.item.modelName.localeCompare(b.item.modelName))
    .map((s) => s.item);
}

export function resolveRecentModelEntries<T extends { providerID: string; modelID: string }>(
  models: T[],
  limit = Infinity
): T[] {
  const byKey = new Map(models.map((m) => [`${m.providerID}/${m.modelID}`, m]));
  const seen = new Set<string>();
  const out: T[] = [];
  for (const item of workspace.recentModels) {
    const key = `${item.providerID}/${item.modelID}`;
    const found = byKey.get(key);
    if (!found || seen.has(key)) continue;
    seen.add(key);
    out.push(found);
    if (out.length >= limit) break;
  }
  return out;
}

export function resolveFavoriteModelEntries<T extends { providerID: string; modelID: string }>(
  models: T[],
  options?: { limit?: number; excludeKey?: string }
): T[] {
  const limit = options?.limit ?? Infinity;
  const excludeKey = options?.excludeKey || "";
  const byKey = new Map(models.map((m) => [`${m.providerID}/${m.modelID}`, m]));
  const seen = new Set<string>();
  const out: T[] = [];
  for (const item of workspace.favoriteModels) {
    const key = `${item.providerID}/${item.modelID}`;
    const found = byKey.get(key);
    if (!found || key === excludeKey || seen.has(key)) continue;
    seen.add(key);
    out.push(found);
    if (out.length >= limit) break;
  }
  return out;
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

  const sorted = groups.sort((a, b) => {
    const aCustom = isCustomOrConfiguredProvider(a.providerID);
    const bCustom = isCustomOrConfiguredProvider(b.providerID);
    if (aCustom !== bCustom) return aCustom ? -1 : 1;
    return a.providerName.localeCompare(b.providerName);
  });

  const autoVirt = buildAutoVirtualProvider();
  const autoModels = Object.entries(autoVirt.models || {}).map(([mID, mInfo]) => ({
    id: mID,
    name: mInfo?.name || mID,
    fullKey: `${AUTO_PROVIDER_ID}/${mID}`,
    free: mID === "free",
    multiplier: mInfo?.multiplier,
  }));

  if (autoModels.length > 0) {
    return [
      {
        providerID: AUTO_PROVIDER_ID,
        providerName: autoVirt.name || AUTO_PROVIDER_ID,
        models: autoModels,
      },
      ...sorted,
    ];
  }

  return sorted;
}

export const LOCAL_CLI_RUNTIME_PROVIDERS: ReadonlySet<string> = new Set(["antigravity", "claude-code", "codebuddy", "qoder"]);

/**
 * Internal LLM calls (session titles, commit messages, observer audits) go
 * through Hoxi's HTTP provider client. Local CLI runtime models (CodeBuddy,
 * Claude Code, Antigravity, Qoder) can only be driven by the runner, so they
 * must never be selected for those roles.
 */
export function isInternalLLMModel(model?: { providerID?: string; modelID?: string } | null): boolean {
  const providerID = model?.providerID?.trim();
  const modelID = model?.modelID?.trim();
  if (!providerID || !modelID) return false;
  if (providerID === AUTO_PROVIDER_ID) return false;
  if (LOCAL_CLI_RUNTIME_PROVIDERS.has(providerID)) return false;
  const provider = workspace.providers.find((p) => p.id === providerID);
  // Unknown or not-yet-loaded providers are rejected: background calls must
  // only reach a provider the user has visibly configured.
  if (!provider) return false;
  if (provider.source === "runner") return false;
  return Boolean(provider.models?.[modelID]);
}

/** First candidate that can actually serve an internal HTTP LLM call. */
export function resolveInternalLLMModel(
  ...candidates: Array<{ providerID?: string; modelID?: string } | null | undefined>
): SelectedModel | null {
  for (const candidate of candidates) {
    if (candidate?.providerID && candidate.modelID && isInternalLLMModel(candidate)) {
      return { providerID: candidate.providerID, modelID: candidate.modelID };
    }
  }
  return null;
}

/** Connected model groups for internal LLM roles, excluding CLI runtimes and auto pools. */
export function getInternalLLMProviderModelGroups(): ConnectedProviderGroup[] {
  const internalIDs = new Set(
    workspace.providers
      .filter((p) => p.source !== "runner" && !LOCAL_CLI_RUNTIME_PROVIDERS.has(p.id))
      .map((p) => p.id)
  );
  return getConnectedProviderModelGroups().filter(
    (group) => group.providerID !== AUTO_PROVIDER_ID && internalIDs.has(group.providerID)
  );
}

export function isHoxiCustomProvider(provider: ProviderInfo | string): boolean {
  const pid = typeof provider === "string" ? provider : provider.id;
  return Boolean(workspace.hoxiProviders?.[pid]);
}

export function isImportedOpenCodeProvider(provider: ProviderInfo | string): boolean {
  const pid = typeof provider === "string" ? provider : provider.id;
  if (workspace.hoxiProviders?.[pid]) return false;
  if (workspace.openCodeImportedProviders.includes(pid)) return true;
  const cfgProv = workspace.globalConfig?.provider?.[pid];
  if (!cfgProv || typeof cfgProv !== "object") return false;
  return Boolean(cfgProv.npm || cfgProv.options?.baseURL || cfgProv.models);
}

export function isCustomOrConfiguredProvider(provider: ProviderInfo | string): boolean {
  return isHoxiCustomProvider(provider) || isImportedOpenCodeProvider(provider);
}
