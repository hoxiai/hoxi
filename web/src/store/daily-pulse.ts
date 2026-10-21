// web/src/store/daily-pulse.ts
import { createSignal } from "solid-js";
import { hoxiAPI, type GitDailyPulseResponse, type ModelPricingItem } from "../services/api";
import { leaderboardState, syncOfficialModelPricing } from "./leaderboard-state";
import { canonicalModelKey } from "./model-limits";
import { workspace } from "./workspace-state";

export interface DailyModelUsageItem {
  providerID: string;
  modelID: string;
  turns: number;
  totalTokens: number;
  cost: number;
}

export interface DailyTokenBreakdown {
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite: number;
}

export interface DailyDevPulseStats {
  dateKey: string;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  cacheHitRate: number;
  totalCost: number;
  totalAdditions: number;
  totalDeletions: number;
  netLines: number;
  changedFilesCount: number;
  todayCommits: number;
  committedAdditions: number;
  committedDeletions: number;
  uncommittedAdditions: number;
  uncommittedDeletions: number;
  todaySessionsCount: number;
  todayTurnsCount: number;
  toolCallsTotal: number;
  toolSuccessRate: number;
  modelsUsed: DailyModelUsageItem[];
}

export function getTodayDateKey(): string {
  const now = new Date();
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
}

export function getStartOfTodayMs(): number {
  const now = new Date();
  return new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
}

function normalizeTs(ts?: number): number {
  if (!ts || ts <= 0) return 0;
  return ts < 1e12 ? ts * 1000 : ts;
}

const MARKET_CNY_PER_USD = 7.2;
const MARKET_CACHE_READ_RATIO = 0.1;
const MARKET_CACHE_WRITE_RATIO = 1.25;

interface MarketPriceSpec {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}

function convertToUsdPerMillion(value: number, currency?: string): number {
  return (currency || "").toUpperCase() === "CNY" ? value / MARKET_CNY_PER_USD : value;
}

let cachedMarketPriceIndex: { source: ModelPricingItem[]; index: Map<string, MarketPriceSpec> } | null =
  null;

function getMarketPriceIndex(): Map<string, MarketPriceSpec> {
  const source = leaderboardState.pricingCatalog;
  if (cachedMarketPriceIndex && cachedMarketPriceIndex.source === source) {
    return cachedMarketPriceIndex.index;
  }
  const index = new Map<string, MarketPriceSpec>();
  for (const item of source || []) {
    if (!item) continue;
    let spec: MarketPriceSpec;
    if (item.isFree) {
      spec = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
    } else {
      const rawInput = item.price?.input || 0;
      const rawOutput = item.price?.output > 0 ? item.price.output : rawInput;
      if (rawInput <= 0 && rawOutput <= 0) continue;
      const input = convertToUsdPerMillion(rawInput, item.price?.currency);
      const output = convertToUsdPerMillion(rawOutput, item.price?.currency);
      const cacheRead =
        item.price?.cacheRead && item.price.cacheRead > 0
          ? convertToUsdPerMillion(item.price.cacheRead, item.price?.currency)
          : input * MARKET_CACHE_READ_RATIO;
      const cacheWrite =
        item.price?.cacheWrite && item.price.cacheWrite > 0
          ? convertToUsdPerMillion(item.price.cacheWrite, item.price?.currency)
          : input * MARKET_CACHE_WRITE_RATIO;
      spec = { input, output, cacheRead, cacheWrite };
    }
    for (const rawKey of [item.slug, item.apiName, item.gatewayModelName, item.aaSlug, item.name]) {
      const key = canonicalModelKey(rawKey);
      if (key) index.set(key, spec);
    }
  }
  cachedMarketPriceIndex = { source, index };
  return index;
}

export function estimateModelMarketCostUsd(
  modelID: string,
  tokens: DailyTokenBreakdown
): number | null {
  const spec = getMarketPriceIndex().get(canonicalModelKey(modelID));
  if (!spec) return null;
  return (
    (tokens.input * spec.input +
      (tokens.output + tokens.reasoning) * spec.output +
      tokens.cacheRead * spec.cacheRead +
      tokens.cacheWrite * spec.cacheWrite) /
    1_000_000
  );
}

const [gitDailyPulse, setGitDailyPulse] = createSignal<GitDailyPulseResponse>({
  todayCommits: 0,
  committedAdditions: 0,
  committedDeletions: 0,
  committedFiles: 0,
  uncommittedAdditions: 0,
  uncommittedDeletions: 0,
  uncommittedFiles: 0,
});
const [syncingPulse, setSyncingPulse] = createSignal(false);

let pulseInFlight = false;
let lastPulseSignature = "";

export async function refreshDailyDevPulse(manual = false): Promise<void> {
  if (pulseInFlight && !manual) return;
  pulseInFlight = true;
  if (manual) setSyncingPulse(true);
  void syncOfficialModelPricing();
  try {
    const res = await hoxiAPI.getGitDailyPulse(
      workspace.rootDir || undefined,
      workspace.activeSessionID || undefined
    );
    if (res) {
      const sig = JSON.stringify(res);
      if (sig !== lastPulseSignature) {
        lastPulseSignature = sig;
        setGitDailyPulse(res);
      }
    }
  } catch {
    /* ignore */
  } finally {
    pulseInFlight = false;
    if (manual) setSyncingPulse(false);
  }
}

export function isDailyPulseSyncing(): boolean {
  return syncingPulse();
}

export function computeDailyDevPulseStats(): DailyDevPulseStats {
  const todayKey = getTodayDateKey();
  const startOfToday = getStartOfTodayMs();
  const pulse = gitDailyPulse();

  let inputTokens = pulse.dbInputTokens || 0;
  let outputTokens = pulse.dbOutputTokens || 0;
  let reasoningTokens = pulse.dbReasoningTokens || 0;
  let cacheReadTokens = pulse.dbCacheReadTokens || 0;
  let cacheWriteTokens = pulse.dbCacheWriteTokens || 0;
  let totalCost = 0;
  let todayTurnsCount = pulse.dbTurnsCount || 0;
  const latestDbTs = pulse.dbLatestTurnTs || 0;

  const byModel = new Map<string, DailyModelUsageItem>();
  const dbModels = pulse.dbModels || [];
  for (const m of dbModels) {
    const tokens: DailyTokenBreakdown = {
      input: m.inputTokens || 0,
      output: m.outputTokens || 0,
      reasoning: m.reasoningTokens || 0,
      cacheRead: m.cacheReadTokens || 0,
      cacheWrite: m.cacheWriteTokens || 0,
    };
    const hasBreakdown = Object.values(tokens).some((v) => v > 0);
    const cost = hasBreakdown
      ? (estimateModelMarketCostUsd(m.modelID, tokens) ?? (m.cost || 0))
      : m.cost || 0;
    totalCost += cost;
    const key = `${m.providerID}/${m.modelID}`;
    byModel.set(key, {
      providerID: m.providerID,
      modelID: m.modelID,
      turns: m.turns,
      totalTokens: m.totalTokens,
      cost,
    });
  }
  if (dbModels.length === 0) totalCost = pulse.dbTotalCost || 0;

  // Merge any live in-memory turns from the active session newer than the last SQLite snapshot
  const seenLiveTurnKeys = new Set<string>();
  const activeSid = workspace.activeSessionID;
  const recentActiveTurns = activeSid ? (workspace.messagesBySession[activeSid] ?? []).slice(-4) : [];
  for (const turn of recentActiveTurns) {
    const info = turn.info;
    if (info.role !== "assistant") continue;
    const createdTs = normalizeTs(info.time?.created);
    if (createdTs < startOfToday || (latestDbTs > 0 && createdTs <= latestDbTs)) continue;

    const tk = info.tokens;
    if (!tk || ((tk.input || 0) === 0 && (tk.output || 0) === 0 && (tk.cache?.read || 0) === 0)) {
      continue;
    }
    const dedupKey = `${createdTs}:${info.modelID || ""}:${tk.input || 0}:${tk.output || 0}`;
    if (seenLiveTurnKeys.has(dedupKey)) continue;
    seenLiveTurnKeys.add(dedupKey);

    const inTok = tk.input || 0;
    const outTok = tk.output || 0;
    const reasonTok = tk.reasoning || 0;
    const cRead = tk.cache?.read || 0;
    const cWrite = tk.cache?.write || 0;
    const turnTotal = inTok + outTok + reasonTok + cRead + cWrite;

    const pid = info.providerID || workspace.selectedModel?.providerID || "unknown";
    const mid = info.modelID || workspace.selectedModel?.modelID || "auto";
    const mKey = `${pid}/${mid}`;
    const turnCost =
      estimateModelMarketCostUsd(mid, {
        input: inTok,
        output: outTok,
        reasoning: reasonTok,
        cacheRead: cRead,
        cacheWrite: cWrite,
      }) ??
      (typeof info.cost === "number" && info.cost > 0 ? info.cost : 0);

    inputTokens += inTok;
    outputTokens += outTok;
    reasoningTokens += reasonTok;
    cacheReadTokens += cRead;
    cacheWriteTokens += cWrite;
    totalCost += turnCost;
    todayTurnsCount += 1;

    const existing = byModel.get(mKey) || {
      providerID: pid,
      modelID: mid,
      turns: 0,
      totalTokens: 0,
      cost: 0,
    };
    existing.turns += 1;
    existing.totalTokens += turnTotal;
    existing.cost += turnCost;
    byModel.set(mKey, existing);
  }

  const totalTokens = inputTokens + outputTokens + reasoningTokens + cacheReadTokens + cacheWriteTokens;
  const totalInputPool = inputTokens + cacheReadTokens + cacheWriteTokens;
  const cacheHitRate = totalInputPool > 0 ? Math.round((cacheReadTokens / totalInputPool) * 100) : 0;

  const toolCallsTotal = pulse.dbToolCallsTotal || 0;
  const toolCallsFailed = pulse.dbToolCallsFailed || 0;
  const toolSuccessRate =
    toolCallsTotal > 0 ? Math.round(((toolCallsTotal - toolCallsFailed) / toolCallsTotal) * 1000) / 10 : 100;

  const committedAdditions = pulse.committedAdditions || 0;
  const committedDeletions = pulse.committedDeletions || 0;
  const uncommittedAdditions = pulse.uncommittedAdditions || 0;
  const uncommittedDeletions = pulse.uncommittedDeletions || 0;
  const totalAdditions = committedAdditions + uncommittedAdditions;
  const totalDeletions = committedDeletions + uncommittedDeletions;
  const netLines = totalAdditions - totalDeletions;
  const changedFilesCount = (pulse.committedFiles || 0) + (pulse.uncommittedFiles || 0);

  const modelsUsed = Array.from(byModel.values())
    .sort((a, b) => b.totalTokens - a.totalTokens || b.cost - a.cost)
    .slice(0, 3);

  return {
    dateKey: todayKey,
    totalTokens,
    inputTokens,
    outputTokens,
    reasoningTokens,
    cacheReadTokens,
    cacheWriteTokens,
    cacheHitRate,
    totalCost,
    totalAdditions,
    totalDeletions,
    netLines,
    changedFilesCount,
    todayCommits: pulse.todayCommits || 0,
    committedAdditions,
    committedDeletions,
    uncommittedAdditions,
    uncommittedDeletions,
    todaySessionsCount: pulse.dbSessionsCount || 0,
    todayTurnsCount,
    toolCallsTotal,
    toolSuccessRate,
    modelsUsed,
  };
}
