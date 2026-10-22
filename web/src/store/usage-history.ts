// web/src/store/usage-history.ts
// Daily usage history for the settings panel. The backend returns raw token
// counters per model per day; pricing happens here with the same resolver that
// bills today's pulse, so the two views can never disagree.
import { createSignal } from "solid-js";
import {
  hoxiAPI,
  type DevPulseHistoryDay,
  type DevPulseHistoryResponse,
} from "../services/api";
import { syncOfficialModelPricing } from "./leaderboard-state";
import { estimateTokenCostUsd } from "./model-pricing";
import { workspace } from "./workspace-state";

export interface UsageHistoryDay extends DevPulseHistoryDay {
  totalTokens: number;
  /** Estimated USD cost; models the catalog cannot price are excluded. */
  cost: number;
  unpricedModels: string[];
}

export type UsageHistoryRange = 7 | 14 | 30 | 90;

export const USAGE_HISTORY_RANGES: UsageHistoryRange[] = [7, 14, 30, 90];

const [usageHistory, setUsageHistory] = createSignal<UsageHistoryDay[]>([]);
const [usageHistoryRange, setUsageHistoryRange] = createSignal<UsageHistoryRange>(30);
const [usageHistoryLoading, setUsageHistoryLoading] = createSignal(false);
const [usageHistoryError, setUsageHistoryError] = createSignal("");

let inFlight = false;

export { usageHistory, usageHistoryRange, usageHistoryLoading, usageHistoryError, setUsageHistoryRange };

/** Prices one backend day and flattens its token counters. */
export function priceHistoryDay(day: DevPulseHistoryDay): UsageHistoryDay {
  let cost = 0;
  const unpriced = new Set<string>();
  for (const model of day.models || []) {
    const estimated = estimateTokenCostUsd(model.modelID, {
      input: model.inputTokens || 0,
      output: model.outputTokens || 0,
      reasoning: model.reasoningTokens || 0,
      cacheRead: model.cacheReadTokens || 0,
      cacheWrite: model.cacheWriteTokens || 0,
    });
    if (estimated === null) {
      unpriced.add(model.modelID);
      continue;
    }
    cost += estimated;
  }
  const totalTokens =
    (day.inputTokens || 0) +
    (day.outputTokens || 0) +
    (day.reasoningTokens || 0) +
    (day.cacheReadTokens || 0) +
    (day.cacheWriteTokens || 0);
  return { ...day, totalTokens, cost, unpricedModels: Array.from(unpriced) };
}

export async function loadUsageHistory(
  days: UsageHistoryRange = usageHistoryRange(),
  manual = false
): Promise<void> {
  if (inFlight && !manual) return;
  inFlight = true;
  setUsageHistoryRange(days);
  setUsageHistoryLoading(true);
  setUsageHistoryError("");
  try {
    // Pricing must be present before the days are priced.
    await syncOfficialModelPricing();
    const res = await hoxiAPI.getDevPulseHistory(days, workspace.rootDir || undefined);
    if (!res || !Array.isArray(res.days)) {
      setUsageHistory([]);
      return;
    }
    setUsageHistory(res.days.map(priceHistoryDay));
  } catch (err: any) {
    setUsageHistory([]);
    setUsageHistoryError(String(err?.message || err || ""));
  } finally {
    inFlight = false;
    setUsageHistoryLoading(false);
  }
}

export interface UsageHistoryTotals {
  days: number;
  turns: number;
  sessions: number;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cost: number;
  commits: number;
  additions: number;
  deletions: number;
  toolCallsTotal: number;
  toolCallsFailed: number;
  unpricedModels: string[];
}

export function usageHistoryTotals(days: UsageHistoryDay[]): UsageHistoryTotals {
  const totals: UsageHistoryTotals = {
    days: days.length,
    turns: 0,
    sessions: 0,
    totalTokens: 0,
    inputTokens: 0,
    outputTokens: 0,
    reasoningTokens: 0,
    cacheReadTokens: 0,
    cost: 0,
    commits: 0,
    additions: 0,
    deletions: 0,
    toolCallsTotal: 0,
    toolCallsFailed: 0,
    unpricedModels: [],
  };
  const unpriced = new Set<string>();
  for (const day of days) {
    totals.turns += day.turns || 0;
    // Sessions are counted per day; summing would double count a session that
    // spans midnight, so the UI labels this as "session-days".
    totals.sessions += day.sessions || 0;
    totals.totalTokens += day.totalTokens || 0;
    totals.inputTokens += day.inputTokens || 0;
    totals.outputTokens += day.outputTokens || 0;
    totals.reasoningTokens += day.reasoningTokens || 0;
    totals.cacheReadTokens += day.cacheReadTokens || 0;
    totals.cost += day.cost || 0;
    totals.commits += day.commits || 0;
    totals.additions += day.additions || 0;
    totals.deletions += day.deletions || 0;
    totals.toolCallsTotal += day.toolCallsTotal || 0;
    totals.toolCallsFailed += day.toolCallsFailed || 0;
    for (const id of day.unpricedModels || []) unpriced.add(id);
  }
  totals.unpricedModels = Array.from(unpriced);
  return totals;
}
