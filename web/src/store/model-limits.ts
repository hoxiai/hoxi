// web/src/store/model-limits.ts
import type { ProviderInfo } from "../services/api";
import { leaderboardState } from "./leaderboard-state";
import { workspace } from "./workspace-state";

export interface InferredModelSpec {
  inputCost?: number;
  outputCost?: number;
  currency?: string;
  contextLimit?: number;
  outputLimit?: number;
}

export function normalizeModelLookupKey(raw: string): string {
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

// Deployment labels Hoxi providers append to an upstream model id
// (`deepseek-v4.1-flash-solo`, `grok-4.7-build-fast`, ...). They never change
// the context window, so family inference strips them. Exact catalog/pricing
// matching must NOT strip them: `deepseek-v4.1-flash-free` really does have a
// smaller window than the paid variant.
const MODEL_VARIANT_SUFFIXES = [
  "-solo",
  "-fast",
  "-free",
  "-flex",
  "-marathon",
  "-priority",
  "-latest",
  "-reasoning",
  "-build",
];

const MODEL_CHANNEL_SUFFIXES = ["@eu", "@us", "@cn", ":thinking", ":free", ":fast"];

/** Exact identity key used for catalog and pricing lookups. */
export function canonicalModelKey(raw?: string): string {
  if (!raw) return "";
  return normalizeModelLookupKey(raw)
    .replace(/（.*?）|\(.*?\)/g, "")
    .trim()
    .replace(/[._\s]+/g, "-")
    .replace(/--+/g, "-")
    .replace(/^~/, "")
    .replace(/-\d{8}$/, "")
    .replace(/-thinking$/, "");
}

/** Family key used only for last-resort limit inference. */
export function modelFamilyKey(raw?: string): string {
  let s = canonicalModelKey(raw);
  if (!s) return "";
  for (const suffix of MODEL_CHANNEL_SUFFIXES) {
    if (s.endsWith(suffix) && s.length > suffix.length) s = s.slice(0, -suffix.length);
  }
  for (;;) {
    let trimmed = false;
    for (const suffix of MODEL_VARIANT_SUFFIXES) {
      if (s.endsWith(suffix) && s.length > suffix.length) {
        s = s.slice(0, -suffix.length);
        trimmed = true;
      }
    }
    if (!trimmed) break;
  }
  return s;
}

function keyTokens(key: string): string[] {
  return key.split(/[\s-]+/).filter(Boolean);
}

/** True when the key contains `token` as a whole segment. */
function hasToken(key: string, token: string): boolean {
  return keyTokens(key).includes(token);
}

function hasAnyToken(key: string, ...tokens: string[]): boolean {
  return tokens.some((token) => hasToken(key, token));
}

/** True when the dash-delimited version sequence appears as a whole segment run. */
function hasVersionRun(key: string, run: string): boolean {
  return new RegExp(`(^|-)${run}(-|$)`).test(key);
}

/** OpenAI o-series ids (`o3`, `o4-mini`) are dash-token versions. */
function isOSeriesKey(key: string): boolean {
  for (const part of keyTokens(key)) {
    for (const prefix of ["o1", "o3", "o4"]) {
      if (part === prefix) return true;
      if (part.startsWith(prefix) && /^\d+$/.test(part.slice(prefix.length))) return true;
    }
  }
  return false;
}

/** First numeric token after `family`, so `officeace-claude-opus-5-5` still reads 5. */
function familyVersion(key: string, family: string): number {
  const parts = keyTokens(key);
  const at = parts.indexOf(family);
  if (at < 0) return 0;
  for (const part of parts.slice(at + 1)) {
    const match = part.match(/^\d+/);
    if (match) return Number(match[0]);
  }
  return 0;
}

export function inferOfficialModelPricingAndLimits(
  modelID: string,
  modelName?: string
): InferredModelSpec {
  const s = `${modelFamilyKey(modelID)}-${modelFamilyKey(modelName)}`;

  if (hasToken(s, "claude")) {
    const major = familyVersion(s, "claude");
    if (hasAnyToken(s, "opus", "sonnet", "fable") && major >= 5) {
      return { inputCost: 5.0, outputCost: 25.0, contextLimit: 1_000_000, outputLimit: 128_000 };
    }
    if (hasAnyToken(s, "opus", "fable")) {
      return { inputCost: 5.0, outputCost: 25.0, contextLimit: 200_000, outputLimit: 64_000 };
    }
    if (hasToken(s, "haiku")) {
      return { inputCost: 0.8, outputCost: 4.0, contextLimit: 200_000, outputLimit: 64_000 };
    }
    return { inputCost: 3.0, outputCost: 15.0, contextLimit: 200_000, outputLimit: 64_000 };
  }

  if (hasToken(s, "gemini")) {
    if (hasToken(s, "lite")) {
      return { inputCost: 0.1, outputCost: 0.4, contextLimit: 1_000_000, outputLimit: 65_536 };
    }
    if (hasToken(s, "flash")) {
      return { inputCost: 0.35, outputCost: 1.4, contextLimit: 1_000_000, outputLimit: 65_536 };
    }
    return { inputCost: 1.25, outputCost: 10.0, contextLimit: 1_000_000, outputLimit: 65_536 };
  }

  if (hasToken(s, "gpt") || hasToken(s, "codex") || isOSeriesKey(s)) {
    if (hasToken(s, "nano")) {
      return { inputCost: 0.1, outputCost: 0.4, contextLimit: 400_000, outputLimit: 128_000 };
    }
    if (hasAnyToken(s, "mini", "luna")) {
      return { inputCost: 0.25, outputCost: 1.0, contextLimit: 400_000, outputLimit: 128_000 };
    }
    if (hasToken(s, "astra") || familyVersion(s, "gpt") >= 6 || hasVersionRun(s, "5-6")) {
      return { inputCost: 6.0, outputCost: 24.0, contextLimit: 1_050_000, outputLimit: 128_000 };
    }
    if (hasToken(s, "sol") || hasVersionRun(s, "5-5")) {
      return { inputCost: 3.0, outputCost: 12.0, contextLimit: 1_000_000, outputLimit: 128_000 };
    }
    return { inputCost: 2.0, outputCost: 8.0, contextLimit: 400_000, outputLimit: 128_000 };
  }

  if (hasToken(s, "deepseek")) {
    if (hasToken(s, "v3")) {
      return { inputCost: 0.27, outputCost: 1.1, contextLimit: 163_840, outputLimit: 65_536 };
    }
    if (hasAnyToken(s, "r1", "chat")) {
      return { inputCost: 0.27, outputCost: 1.1, contextLimit: 128_000, outputLimit: 64_000 };
    }
    if (hasAnyToken(s, "flash", "lite")) {
      return { inputCost: 0.14, outputCost: 0.28, contextLimit: 1_048_576, outputLimit: 384_000 };
    }
    if (hasAnyToken(s, "pro", "reasoner") || /(^|-)(r\d+|v[4-9]\d*)(-|$)/.test(s)) {
      return { inputCost: 0.55, outputCost: 2.19, contextLimit: 1_048_576, outputLimit: 384_000 };
    }
    return { inputCost: 0.27, outputCost: 1.1, contextLimit: 1_048_576, outputLimit: 384_000 };
  }

  if (hasAnyToken(s, "qwen", "qwq")) {
    if (hasAnyToken(s, "flash", "turbo")) {
      return { inputCost: 0.15, outputCost: 0.6, contextLimit: 1_000_000, outputLimit: 131_072 };
    }
    if (hasAnyToken(s, "plus", "coder", "qwq")) {
      return { inputCost: 0.4, outputCost: 1.2, contextLimit: 1_000_000, outputLimit: 131_072 };
    }
    return { inputCost: 1.2, outputCost: 6.0, contextLimit: 1_000_000, outputLimit: 131_072 };
  }

  if (hasAnyToken(s, "glm", "codegeex")) {
    if (hasToken(s, "4")) {
      return { inputCost: 0.1, outputCost: 0.4, contextLimit: 200_000, outputLimit: 131_072 };
    }
    if (hasAnyToken(s, "flash", "air")) {
      return { inputCost: 0.1, outputCost: 0.4, contextLimit: 1_000_000, outputLimit: 131_072 };
    }
    return { inputCost: 0.7, outputCost: 2.8, contextLimit: 1_000_000, outputLimit: 131_072 };
  }

  if (hasAnyToken(s, "kimi", "moonshot")) {
    if (hasToken(s, "k2")) {
      return { inputCost: 0.8, outputCost: 3.2, contextLimit: 131_072, outputLimit: 98_304 };
    }
    if (hasToken(s, "code")) {
      return { inputCost: 0.6, outputCost: 2.5, contextLimit: 256_000, outputLimit: 32_000 };
    }
    return { inputCost: 1.5, outputCost: 6.0, contextLimit: 1_048_576, outputLimit: 131_072 };
  }

  if (hasToken(s, "mimo")) {
    return { inputCost: 0.3, outputCost: 1.2, contextLimit: 1_048_576, outputLimit: 131_072 };
  }

  if (hasToken(s, "minimax")) {
    return { inputCost: 0.3, outputCost: 1.2, contextLimit: 1_000_000, outputLimit: 131_072 };
  }

  if (hasToken(s, "step")) {
    return { inputCost: 0.5, outputCost: 1.5, contextLimit: 1_000_000, outputLimit: 65_536 };
  }

  if (hasToken(s, "hunyuan") || hasAnyToken(s, "hy3", "hy4")) {
    if (hasToken(s, "hy3")) {
      return { inputCost: 0.08, outputCost: 0.3, contextLimit: 192_000, outputLimit: 64_000 };
    }
    return { inputCost: 0.3, outputCost: 1.2, contextLimit: 1_000_000, outputLimit: 64_000 };
  }

  if (hasToken(s, "grok")) {
    if (hasAnyToken(s, "mini", "fast")) {
      return { inputCost: 0.3, outputCost: 0.5, contextLimit: 131_072, outputLimit: 32_000 };
    }
    if (familyVersion(s, "grok") >= 4) {
      return { inputCost: 3.0, outputCost: 15.0, contextLimit: 500_000, outputLimit: 131_072 };
    }
    return { inputCost: 3.0, outputCost: 15.0, contextLimit: 256_000, outputLimit: 64_000 };
  }

  if (hasAnyToken(s, "doubao", "seed")) {
    if (hasAnyToken(s, "lite", "flash")) {
      return { inputCost: 0.1, outputCost: 0.3, contextLimit: 262_144, outputLimit: 32_000 };
    }
    return { inputCost: 0.3, outputCost: 1.2, contextLimit: 262_144, outputLimit: 131_072 };
  }

  if (hasAnyToken(s, "mistral", "codestral", "pixtral")) {
    if (hasAnyToken(s, "codestral", "small")) {
      return { inputCost: 0.3, outputCost: 0.9, contextLimit: 256_000, outputLimit: 32_000 };
    }
    return { inputCost: 2.0, outputCost: 6.0, contextLimit: 256_000, outputLimit: 32_000 };
  }

  return {};
}

export const DEFAULT_CONTEXT_LIMIT = 200_000;

export interface ModelLimitSource {
  providerID: string;
  modelID: string;
  modelName?: string;
  model?: {
    limit?: { context?: number; output?: number };
    name?: string;
  } | null;
  configured?: {
    limit?: { context?: number; output?: number };
    name?: string;
  } | null;
  limitIndex?: ModelLimitIndex;
}

export interface ModelLimitIndex {
  catalog: Map<string, number>;
  pricing: Map<string, number>;
  /** Family-key pricing fallback, used only when no exact row matches. */
  pricingFamily: Map<string, number>;
}

function positive(value: unknown): number | undefined {
  return typeof value === "number" && value > 0 ? value : undefined;
}

function pricingAliasKeys(item: { slug?: string; apiName?: string; gatewayModelName?: string; aaSlug?: string; name?: string }): string[] {
  const keys: string[] = [];
  for (const rawKey of [item?.slug, item?.apiName, item?.gatewayModelName, item?.aaSlug, item?.name]) {
    const key = canonicalModelKey(rawKey);
    if (key) keys.push(key);
  }
  return keys;
}

/**
 * Builds canonical-key -> context-window indexes for the provider catalog and the
 * synced pricing catalog in two passes. Callers that resolve limits for many
 * models at once (e.g. flattening the catalog) must pass this index instead of
 * falling back to `contextFromCatalog` / `contextFromPricing`, which rescan the
 * whole catalogs per model and degrade to O(n^2).
 *
 * `catalog` is keyed by exact identity, so `deepseek-v4.1-flash-free` never
 * borrows the paid variant's window.
 */
export function buildModelLimitIndex(providers: ProviderInfo[]): ModelLimitIndex {
  const catalog = new Map<string, number>();
  for (const provider of providers) {
    for (const [id, info] of Object.entries(provider.models || {})) {
      const context = positive(info?.limit?.context);
      if (!context) continue;
      const key = canonicalModelKey(id);
      if (!key || catalog.has(key)) continue;
      catalog.set(key, context);
    }
  }

  const pricing = new Map<string, number>();
  const pricingFamily = new Map<string, number>();
  for (const item of leaderboardState.pricingCatalog || []) {
    const context = positive(item?.contextWindow);
    if (!context) continue;
    for (const key of pricingAliasKeys(item)) {
      if (!pricing.has(key)) pricing.set(key, context);
    }
    for (const rawKey of [item?.slug, item?.apiName, item?.gatewayModelName, item?.aaSlug, item?.name]) {
      const familyKey = modelFamilyKey(rawKey);
      if (!familyKey) continue;
      const prev = pricingFamily.get(familyKey);
      // Several rows can collapse to one family; keep the largest window so a
      // small reseller row cannot shrink the family.
      if (prev === undefined || context > prev) pricingFamily.set(familyKey, context);
    }
  }

  return { catalog, pricing, pricingFamily };
}

export function contextFromCatalog(
  modelID: string,
  providers: ProviderInfo[]
): number | undefined {
  const key = canonicalModelKey(modelID);
  if (!key) return undefined;
  for (const provider of providers) {
    for (const [id, info] of Object.entries(provider.models || {})) {
      if (canonicalModelKey(id) !== key) continue;
      const context = positive(info?.limit?.context);
      if (context) return context;
    }
  }
  return undefined;
}

export function contextFromPricing(modelID: string, modelName?: string): number | undefined {
  const keys = new Set([canonicalModelKey(modelID), canonicalModelKey(modelName)].filter(Boolean));
  if (keys.size === 0) return undefined;
  for (const item of leaderboardState.pricingCatalog || []) {
    const aliases = [item?.slug, item?.apiName, item?.gatewayModelName, item?.aaSlug, item?.name];
    if (!aliases.some((alias) => keys.has(canonicalModelKey(alias)))) continue;
    const context = positive(item?.contextWindow);
    if (context) return context;
  }
  // No exact row: fall back to the family row so a reseller variant such as
  // `deepseek-v4.1-flash-solo` still sees the upstream window.
  const familyKeys = new Set([modelFamilyKey(modelID), modelFamilyKey(modelName)].filter(Boolean));
  if (familyKeys.size === 0) return undefined;
  let best: number | undefined;
  for (const item of leaderboardState.pricingCatalog || []) {
    const aliases = [item?.slug, item?.apiName, item?.gatewayModelName, item?.aaSlug, item?.name];
    if (!aliases.some((alias) => familyKeys.has(modelFamilyKey(alias)))) continue;
    const context = positive(item?.contextWindow);
    if (context && (best === undefined || context > best)) best = context;
  }
  return best;
}

/**
 * The synced pricing catalog contains placeholder rows (several unrelated
 * models share a 128k/8k stub) that are far smaller than the real window of the
 * family they claim to describe. When family knowledge says the window is much
 * larger, the stub is ignored instead of making every long session read 100%.
 * A catalog value that is larger than the family value is always kept: resellers
 * legitimately advertise bigger windows.
 */
function reconcilePricingWithFamily(
  pricing: number | undefined,
  family: number | undefined
): number | undefined {
  if (!pricing) return undefined;
  if (!family) return pricing;
  if (pricing * 4 < family) return family;
  return pricing;
}

/** Same precedence as the model selector: declared, configured, pricing, catalog, family. */
export function resolveModelContextLimit(source: ModelLimitSource): number | undefined {
  const modelID = source.modelID;
  const modelName = source.modelName || source.model?.name || source.configured?.name;
  const index = source.limitIndex;
  const fromCatalog = index
    ? index.catalog.get(canonicalModelKey(modelID))
    : contextFromCatalog(modelID, workspace.providers);
  const fromPricing = index
    ? index.pricing.get(canonicalModelKey(modelID)) ??
      index.pricing.get(canonicalModelKey(modelName)) ??
      index.pricingFamily.get(modelFamilyKey(modelID)) ??
      index.pricingFamily.get(modelFamilyKey(modelName))
    : contextFromPricing(modelID, modelName);
  const family = positive(inferOfficialModelPricingAndLimits(modelID, modelName).contextLimit);
  return (
    positive(source.model?.limit?.context) ??
    positive(source.configured?.limit?.context) ??
    reconcilePricingWithFamily(fromPricing, family) ??
    fromCatalog ??
    family
  );
}

export function lookupModelContextLimit(
  providerID?: string,
  modelID?: string
): number | undefined {
  if (!providerID || !modelID) return undefined;
  const provider = workspace.providers.find((item: ProviderInfo) => item.id === providerID);
  const configured = workspace.globalConfig?.provider?.[providerID]?.models?.[modelID];
  return resolveModelContextLimit({
    providerID,
    modelID,
    model: provider?.models?.[modelID],
    configured,
  });
}

export function contextLimitForSelection(providerID?: string, modelID?: string): number {
  return lookupModelContextLimit(providerID, modelID) ?? DEFAULT_CONTEXT_LIMIT;
}
