// web/src/store/model-pricing.ts
// Single source of truth for turning synced hoxi.ai market prices into USD cost.
//
// Every number here originates from the synced pricing catalog. Nothing is
// hardcoded: unknown models fall back to the median of their price band, which
// is recomputed from the catalog on each sync.
import type { ModelPricingItem } from "../services/api";
import { leaderboardState } from "./leaderboard-state";

// Pricing owns its own id normalization so the accounting path does not depend
// on the context-window selector's heuristics: a limit change must never move
// money, and a pricing change must never move a window.
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
  "-preview",
  "-high",
  "-medium",
  "-low",
];

const MODEL_CHANNEL_SUFFIXES = ["@eu", "@us", "@cn", ":thinking", ":free", ":fast"];

/** Exact identity key used for catalog lookups. */
function canonicalModelKey(raw?: string): string {
  if (!raw) return "";
  let s = (raw || "").trim().toLowerCase();
  for (const prefix of ["cn:", "intl:", "global:", "models/"]) {
    if (s.startsWith(prefix)) s = s.slice(prefix.length);
  }
  const slash = s.lastIndexOf("/");
  if (slash >= 0 && slash < s.length - 1) s = s.slice(slash + 1);
  return s
    .replace(/（.*?）|\(.*?\)/g, "")
    .trim()
    .replace(/[._\s]+/g, "-")
    .replace(/--+/g, "-")
    .replace(/^~/, "")
    .replace(/-\d{8}$/, "")
    .replace(/-thinking$/, "");
}

/** Family key used for variant-insensitive fallbacks. */
function modelFamilyKey(raw?: string): string {
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

/**
 * Fallback CNY→USD rate for catalog rows published in CNY. The catalog mixes
 * currencies; all accounting must happen in one unit because the engine's own
 * message cost has no currency field.
 */
export const CNY_PER_USD = 7.0;

export const CACHE_READ_RATIO = 0.1;
export const CACHE_WRITE_RATIO = 1.25;

export interface TokenCounts {
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite: number;
}

/** Per-million-token USD rates. */
export interface UsdPriceSpec {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}

export type PriceTier = "budget" | "standard" | "frontier";

const BUDGET_TOKENS = [
  "flash",
  "lite",
  "mini",
  "nano",
  "free",
  "turbo",
  "air",
  "haiku",
  "spark",
  "fast",
];

const FRONTIER_TOKENS = [
  "pro",
  "max",
  "opus",
  "fable",
  "astra",
  "ultra",
  "reasoner",
  "sol",
  "argon",
];

/** Vendor/family tokens that justify estimating an otherwise unknown id. */
const FAMILY_TOKENS = [
  "claude",
  "gpt",
  "codex",
  "gemini",
  "deepseek",
  "qwen",
  "qwq",
  "glm",
  "codegeex",
  "kimi",
  "moonshot",
  "mimo",
  "minimax",
  "grok",
  "doubao",
  "seed",
  "mistral",
  "codestral",
  "pixtral",
  "hunyuan",
  "step",
  "ling",
  "motif",
  "muse",
  "agnes",
  "apodex",
  "doubao",
  "ernie",
  "baichuan",
  "yi",
];

export function convertToUsdPerMillion(value: number, currency?: string): number {
  return (currency || "").toUpperCase() === "CNY" ? value / CNY_PER_USD : value;
}

function tokens(key: string): string[] {
  return key.split("-").filter(Boolean);
}

/**
 * Words that identify a model family within a vendor, ignoring version
 * numbers. `deepseek-v4-1-flash` → ["deepseek","flash"], which lets
 * `deepseek-flash` (a version-less reseller id) find its real price instead of
 * a band median.
 */
function traitWords(familyKey: string): string[] {
  return tokens(familyKey).filter((part) => !/^\d+$/.test(part));
}

export function priceTierFor(modelID: string): PriceTier {
  const parts = tokens(modelFamilyKey(modelID));
  if (parts.some((part) => BUDGET_TOKENS.includes(part))) return "budget";
  if (parts.some((part) => FRONTIER_TOKENS.includes(part))) return "frontier";
  return "standard";
}

/** The first known vendor/family token in the id, or "" when none matches. */
function vendorTokenFor(modelID: string): string {
  const parts = tokens(modelFamilyKey(modelID));
  for (const part of parts) {
    if (FAMILY_TOKENS.includes(part)) return part;
  }
  return "";
}

interface PricingIndex {
  source: ModelPricingItem[];
  exact: Map<string, UsdPriceSpec>;
  family: Map<string, UsdPriceSpec>;
  tierMedian: Record<PriceTier, UsdPriceSpec>;
  /** `${vendorToken}/${tier}` → median rate of that vendor's band. */
  vendorTierMedian: Map<string, UsdPriceSpec>;
  /** Family key → the name tokens a partial id must cover (see `matchByTraits`). */
  traitIndex: Array<{ key: string; traits: string[]; spec: UsdPriceSpec }>;
}

let cachedIndex: PricingIndex | null = null;

function specFromItem(item: ModelPricingItem): UsdPriceSpec | null {
  if (item.isFree) return { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
  const rawInput = item.price?.input || 0;
  const rawOutput = item.price?.output > 0 ? item.price.output : rawInput;
  if (rawInput <= 0 && rawOutput <= 0) return null;
  const input = convertToUsdPerMillion(rawInput, item.price?.currency);
  const output = convertToUsdPerMillion(rawOutput, item.price?.currency);
  const cacheRead =
    item.price?.cacheRead && item.price.cacheRead > 0
      ? convertToUsdPerMillion(item.price.cacheRead, item.price?.currency)
      : input * CACHE_READ_RATIO;
  const cacheWrite =
    item.price?.cacheWrite && item.price.cacheWrite > 0
      ? convertToUsdPerMillion(item.price.cacheWrite, item.price?.currency)
      : input * CACHE_WRITE_RATIO;
  return { input, output, cacheRead, cacheWrite };
}

function median(values: number[]): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 0 ? (sorted[mid - 1] + sorted[mid]) / 2 : sorted[mid];
}

function buildIndex(source: ModelPricingItem[]): PricingIndex {
  const exact = new Map<string, UsdPriceSpec>();
  const family = new Map<string, UsdPriceSpec>();
  const bands: Record<PriceTier, { input: number[]; output: number[]; cacheRead: number[]; cacheWrite: number[] }> = {
    budget: { input: [], output: [], cacheRead: [], cacheWrite: [] },
    standard: { input: [], output: [], cacheRead: [], cacheWrite: [] },
    frontier: { input: [], output: [], cacheRead: [], cacheWrite: [] },
  };
  const vendorBands = new Map<string, { input: number[]; output: number[]; cacheRead: number[]; cacheWrite: number[] }>();
  const traitRows: Array<{ key: string; traits: string[]; spec: UsdPriceSpec }> = [];
  const paid: UsdPriceSpec[] = [];

  for (const item of source || []) {
    if (!item) continue;
    const spec = specFromItem(item);
    if (!spec) continue;
    for (const rawKey of [item.slug, item.apiName, item.gatewayModelName, item.aaSlug, item.name]) {
      const key = canonicalModelKey(rawKey);
      if (key) exact.set(key, spec);
      const familyKey = modelFamilyKey(rawKey);
      if (familyKey && !family.has(familyKey)) family.set(familyKey, spec);
      // Only the canonical slug contributes traits: aliases repeat the same row.
      if (rawKey === item.slug && familyKey) {
        traitRows.push({ key: familyKey, traits: traitWords(familyKey), spec });
      }
    }
    // Free rows carry no pricing signal and would drag every median to zero.
    if (spec.input <= 0 && spec.output <= 0) continue;
    paid.push(spec);
    const tier = priceTierFor(item.slug);
    const band = bands[tier];
    band.input.push(spec.input);
    band.output.push(spec.output);
    band.cacheRead.push(spec.cacheRead);
    band.cacheWrite.push(spec.cacheWrite);

    // Vendor-scoped bands keep `deepseek` estimates in DeepSeek territory
    // instead of averaging a budget model into frontier prices.
    for (const vendor of new Set(tokens(item.slug).filter((t) => FAMILY_TOKENS.includes(t)))) {
      const key = `${vendor}/${tier}`;
      let vBand = vendorBands.get(key);
      if (!vBand) {
        vBand = { input: [], output: [], cacheRead: [], cacheWrite: [] };
        vendorBands.set(key, vBand);
      }
      vBand.input.push(spec.input);
      vBand.output.push(spec.output);
      vBand.cacheRead.push(spec.cacheRead);
      vBand.cacheWrite.push(spec.cacheWrite);
    }
  }

  const overall = {
    input: median(paid.map((s) => s.input)),
    output: median(paid.map((s) => s.output)),
    cacheRead: median(paid.map((s) => s.cacheRead)),
    cacheWrite: median(paid.map((s) => s.cacheWrite)),
  };
  const tierMedian = {} as Record<PriceTier, UsdPriceSpec>;
  for (const tier of ["budget", "standard", "frontier"] as PriceTier[]) {
    const band = bands[tier];
    tierMedian[tier] =
      band.input.length === 0
        ? overall
        : {
            input: median(band.input),
            output: median(band.output),
            cacheRead: median(band.cacheRead),
            cacheWrite: median(band.cacheWrite),
          };
  }
  const vendorTierMedian = new Map<string, UsdPriceSpec>();
  for (const [key, band] of vendorBands) {
    vendorTierMedian.set(key, {
      input: median(band.input),
      output: median(band.output),
      cacheRead: median(band.cacheRead),
      cacheWrite: median(band.cacheWrite),
    });
  }
  return { source, exact, family, tierMedian, vendorTierMedian, traitIndex: traitRows };
}

function getIndex(): PricingIndex {
  const source = leaderboardState.pricingCatalog;
  if (cachedIndex && cachedIndex.source === source) return cachedIndex;
  cachedIndex = buildIndex(source);
  return cachedIndex;
}

/** Strips a trailing 4-digit snapshot label (`deepseek-v4.1-flash-1017`). */
function withoutSnapshotSuffix(modelID: string): string {
  return modelID.replace(/-\d{4}$/, "");
}

// Resellers prefix an upstream id with their own vendor label
// (`CodeArts-deepseek-v4.1-flash`, `QoderCN-DeepSeek-Flash`). Strip leading
// dash-separated tokens and retry, longest candidate first.
function matchResellerFamily(family: Map<string, UsdPriceSpec>, modelID: string): UsdPriceSpec | undefined {
  const parts = tokens(modelFamilyKey(modelID));
  for (let i = 1; i < parts.length; i++) {
    const spec = family.get(parts.slice(i).join("-"));
    if (spec) return spec;
  }
  return undefined;
}

/**
 * Matches a version-less id to a real catalog row when every non-numeric word
 * of the id is present in that row's family key. `deepseek-flash` covers
 * `deepseek-v4-flash`, so it inherits a real price instead of a band median.
 * When several rows qualify, the one with the fewest extra traits and then the
 * shortest family key wins: a version-less id most likely names the base model,
 * not a point release or a variant (`-vision`, `-thinking`).
 */
function matchByTraits(
  rows: Array<{ key: string; traits: string[]; spec: UsdPriceSpec }>,
  modelID: string
): UsdPriceSpec | undefined {
  const want = traitWords(modelFamilyKey(modelID));
  if (want.length === 0) return undefined;
  let best: { extra: number; length: number; spec: UsdPriceSpec } | undefined;
  for (const row of rows) {
    if (!want.every((word) => row.traits.includes(word))) continue;
    const extra = row.traits.length - want.length;
    const length = tokens(row.key).length;
    if (!best || extra < best.extra || (extra === best.extra && length < best.length)) {
      best = { extra, length, spec: row.spec };
    }
  }
  return best?.spec;
}

/**
 * Resolves the USD rate for a model id, or undefined when the catalog cannot
 * describe it at all. Order: exact → family → reseller prefix → snapshot-stripped
 * → vendor+tier median → band median (only for ids that name a known vendor).
 */
export function resolveUsdPriceSpec(modelID: string): UsdPriceSpec | undefined {
  // A `-free` variant is free regardless of what its paid sibling costs. This
  // must run first: family matching strips `-free` and would otherwise bill it.
  if (tokens(canonicalModelKey(modelID)).includes("free")) {
    return { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
  }

  const index = getIndex();
  if (index.exact.size === 0) return undefined;

  const direct =
    index.exact.get(canonicalModelKey(modelID)) ??
    index.family.get(modelFamilyKey(modelID)) ??
    matchResellerFamily(index.family, modelID);
  if (direct) return direct;

  const trimmed = withoutSnapshotSuffix(modelID);
  if (trimmed !== modelID) {
    const viaTrim =
      index.exact.get(canonicalModelKey(trimmed)) ??
      index.family.get(modelFamilyKey(trimmed)) ??
      matchResellerFamily(index.family, trimmed);
    if (viaTrim) return viaTrim;
  }

  // A version-less reseller id (`deepseek-flash`, `QoderCN-DeepSeek-Flash`)
  // still names a real upstream model; prefer that exact row over any median.
  const byTraits = matchByTraits(index.traitIndex, modelID);
  if (byTraits) return byTraits;

  // Unknown variant of a known vendor: use that vendor's median for the same
  // price band, so a budget model is never billed at frontier rates (or vice
  // versa) and a DeepSeek id is not priced as Claude.
  const vendor = vendorTokenFor(modelID);
  if (!vendor) return undefined;
  const tier = priceTierFor(modelID);
  return index.vendorTierMedian.get(`${vendor}/${tier}`) ?? index.tierMedian[tier];
}

/** Estimated USD cost for one turn (or any token bundle). */
export function estimateTokenCostUsd(modelID: string, counts: TokenCounts): number | null {
  const spec = resolveUsdPriceSpec(modelID);
  if (!spec) return null;
  return (
    (counts.input * spec.input +
      (counts.output + counts.reasoning) * spec.output +
      counts.cacheRead * spec.cacheRead +
      counts.cacheWrite * spec.cacheWrite) /
    1_000_000
  );
}

/** Test/diagnostic helper: the band medians currently derived from the catalog. */
export function marketPriceBandMedians(): Record<PriceTier, UsdPriceSpec> {
  return getIndex().tierMedian;
}
