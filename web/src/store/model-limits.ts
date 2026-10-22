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

export function canonicalModelKey(raw?: string): string {
  if (!raw) return "";
  return normalizeModelLookupKey(raw)
    .replace(/（.*?）|\(.*?\)/g, "")
    .trim()
    .replace(/[._\s]+/g, "-")
    .replace(/-\d{8}$/, "")
    .replace(/-thinking$/, "");
}

export function inferOfficialModelPricingAndLimits(
  modelID: string,
  modelName?: string
): InferredModelSpec {
  const s = `${normalizeModelLookupKey(modelID)} ${(modelName || "").toLowerCase()}`;

  if (s.includes("claude")) {
    if (s.includes("opus") || s.includes("fable")) {
      return { inputCost: 5.0, outputCost: 25.0, contextLimit: 200_000, outputLimit: 64_000 };
    }
    if (s.includes("haiku")) {
      return { inputCost: 0.8, outputCost: 4.0, contextLimit: 200_000, outputLimit: 64_000 };
    }
    return { inputCost: 3.0, outputCost: 15.0, contextLimit: 200_000, outputLimit: 64_000 };
  }

  if (s.includes("gemini")) {
    if (s.includes("lite")) {
      return { inputCost: 0.1, outputCost: 0.4, contextLimit: 1_000_000, outputLimit: 65_536 };
    }
    if (s.includes("flash")) {
      return { inputCost: 0.35, outputCost: 1.4, contextLimit: 1_000_000, outputLimit: 65_536 };
    }
    return { inputCost: 1.25, outputCost: 10.0, contextLimit: 1_000_000, outputLimit: 65_536 };
  }

  if (s.startsWith("gpt") || s.includes(" gpt-") || /^o\d+(-|\b)/.test(s) || s.includes("codex")) {
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

  if (s.includes("deepseek")) {
    if (s.includes("flash") || s.includes("lite")) {
      return { inputCost: 0.14, outputCost: 0.28, contextLimit: 1_000_000, outputLimit: 64_000 };
    }
    if (s.includes("pro") || s.includes("reasoner") || /\b(r\d+|v[4-9]\d*)\b/.test(s)) {
      return { inputCost: 0.55, outputCost: 2.19, contextLimit: 1_000_000, outputLimit: 128_000 };
    }
    return { inputCost: 0.27, outputCost: 1.1, contextLimit: 128_000, outputLimit: 32_000 };
  }

  if (s.includes("qwen") || s.startsWith("qwq")) {
    if (s.includes("flash") || s.includes("turbo")) {
      return { inputCost: 0.15, outputCost: 0.6, contextLimit: 1_000_000, outputLimit: 64_000 };
    }
    if (s.includes("plus") || s.includes("coder") || s.startsWith("qwq")) {
      return { inputCost: 0.4, outputCost: 1.2, contextLimit: 1_000_000, outputLimit: 64_000 };
    }
    return { inputCost: 1.2, outputCost: 6.0, contextLimit: 1_000_000, outputLimit: 64_000 };
  }

  if (s.includes("glm") || s.includes("codegeex")) {
    if (s.includes("flash") || s.includes("air")) {
      return { inputCost: 0.1, outputCost: 0.4, contextLimit: 1_000_000, outputLimit: 64_000 };
    }
    return { inputCost: 0.7, outputCost: 2.8, contextLimit: 1_000_000, outputLimit: 64_000 };
  }

  if (s.includes("kimi") || s.includes("moonshot")) {
    if (s.includes("code")) {
      return { inputCost: 0.6, outputCost: 2.5, contextLimit: 256_000, outputLimit: 32_000 };
    }
    if (s.includes("preview") || s.includes("k2")) {
      return { inputCost: 0.8, outputCost: 3.2, contextLimit: 1_000_000, outputLimit: 64_000 };
    }
    return { inputCost: 1.5, outputCost: 6.0, contextLimit: 1_000_000, outputLimit: 64_000 };
  }

  if (/^hy\d/.test(s) || s.includes("hunyuan")) {
    if (s.startsWith("hy3")) {
      return { inputCost: 0.08, outputCost: 0.3, contextLimit: 192_000, outputLimit: 64_000 };
    }
    return { inputCost: 0.3, outputCost: 1.2, contextLimit: 1_000_000, outputLimit: 64_000 };
  }

  if (s.includes("grok")) {
    if (s.includes("mini") || s.includes("fast")) {
      return { inputCost: 0.3, outputCost: 0.5, contextLimit: 131_072, outputLimit: 32_000 };
    }
    return { inputCost: 3.0, outputCost: 15.0, contextLimit: 256_000, outputLimit: 64_000 };
  }

  if (s.includes("doubao") || s.includes("seed-")) {
    if (s.includes("lite") || s.includes("flash")) {
      return { inputCost: 0.1, outputCost: 0.3, contextLimit: 256_000, outputLimit: 32_000 };
    }
    return { inputCost: 0.3, outputCost: 1.2, contextLimit: 256_000, outputLimit: 64_000 };
  }

  if (s.includes("mistral") || s.includes("codestral") || s.includes("pixtral")) {
    if (s.includes("codestral") || s.includes("small")) {
      return { inputCost: 0.3, outputCost: 0.9, contextLimit: 256_000, outputLimit: 32_000 };
    }
    return { inputCost: 2.0, outputCost: 6.0, contextLimit: 128_000, outputLimit: 32_000 };
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
  for (const item of leaderboardState.pricingCatalog || []) {
    const context = positive(item?.contextWindow);
    if (!context) continue;
    for (const key of pricingAliasKeys(item)) {
      if (!pricing.has(key)) pricing.set(key, context);
    }
  }

  return { catalog, pricing };
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
  return undefined;
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
      index.pricing.get(canonicalModelKey(modelName))
    : contextFromPricing(modelID, modelName);
  return (
    positive(source.model?.limit?.context) ??
    positive(source.configured?.limit?.context) ??
    fromPricing ??
    fromCatalog ??
    positive(inferOfficialModelPricingAndLimits(modelID, modelName).contextLimit)
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
