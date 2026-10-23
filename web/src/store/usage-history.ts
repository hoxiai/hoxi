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

export type UsageHistoryRange = 7 | 14 | 30;

export const USAGE_HISTORY_RANGES: UsageHistoryRange[] = [7, 14, 30];

export interface UsageCustomRange {
  start: string;
  end: string;
}

/** Either the year overview heatmap, a fixed rolling window, or an explicit range. */
export type UsageSelection =
  | { kind: "overview" }
  | { kind: "days"; days: UsageHistoryRange }
  | { kind: "custom"; range: UsageCustomRange };

/** Days fetched for the overview heatmap (the backend's maximum window). */
export const USAGE_OVERVIEW_DAYS = 365;

const [usageHistory, setUsageHistory] = createSignal<UsageHistoryDay[]>([]);
const [usageSelection, setUsageSelection] = createSignal<UsageSelection>({ kind: "overview" });
const [usageHistoryLoading, setUsageHistoryLoading] = createSignal(false);
const [usageHistoryError, setUsageHistoryError] = createSignal("");

let inFlight = false;
let historyGeneration = 0;

export { usageHistory, usageSelection, usageHistoryLoading, usageHistoryError };

function sameSelection(a: UsageSelection, b: UsageSelection): boolean {
  if (a.kind !== b.kind) return false;
  if (a.kind === "overview" && b.kind === "overview") return true;
  if (a.kind === "days" && b.kind === "days") return a.days === b.days;
  if (a.kind === "custom" && b.kind === "custom") {
    return a.range.start === b.range.start && a.range.end === b.range.end;
  }
  return false;
}

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
  selection: UsageSelection = usageSelection(),
  manual = false
): Promise<void> {
  if (inFlight && !manual && sameSelection(selection, usageSelection())) return;
  const gen = ++historyGeneration;
  inFlight = true;
  setUsageSelection(selection);
  setUsageHistoryLoading(true);
  setUsageHistoryError("");
  try {
    // Pricing must be present before the days are priced.
    await syncOfficialModelPricing();
    if (gen !== historyGeneration) return;
    const res =
      selection.kind === "custom"
        ? await hoxiAPI.getDevPulseHistory(30, workspace.rootDir || undefined, {
            startDate: selection.range.start,
            endDate: selection.range.end,
          })
        : await hoxiAPI.getDevPulseHistory(
            selection.kind === "overview" ? USAGE_OVERVIEW_DAYS : selection.days,
            workspace.rootDir || undefined
          );
    if (gen !== historyGeneration) return;
    if (!res || !Array.isArray(res.days)) {
      setUsageHistory([]);
      return;
    }
    setUsageHistory(res.days.map(priceHistoryDay));
  } catch (err: any) {
    if (gen !== historyGeneration) return;
    setUsageHistory([]);
    setUsageHistoryError(String(err?.message || err || ""));
  } finally {
    if (gen === historyGeneration) {
      inFlight = false;
      setUsageHistoryLoading(false);
    }
  }
}

export function selectUsageDays(days: UsageHistoryRange): void {
  void loadUsageHistory({ kind: "days", days });
}

export function selectUsageOverview(): void {
  void loadUsageHistory({ kind: "overview" });
}

export function selectUsageCustomRange(start: string, end: string): void {
  if (!start || !end) return;
  void loadUsageHistory({ kind: "custom", range: { start, end } });
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

export interface UsageModelTotals {
  providerID: string;
  modelID: string;
  turns: number;
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  /** Estimated USD cost across the window; `null` when the catalog cannot price it. */
  cost: number | null;
}

interface ModelAggregate {
  providerID: string;
  modelID: string;
  turns: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
}

/** Rolls the per-day `models` lists up into one row per provider/model pair. */
export function usageHistoryModels(days: UsageHistoryDay[]): UsageModelTotals[] {
  const byKey = new Map<string, ModelAggregate>();
  for (const day of days) {
    for (const model of day.models || []) {
      const key = `${model.providerID}\u0000${model.modelID}`;
      let row = byKey.get(key);
      if (!row) {
        row = {
          providerID: model.providerID,
          modelID: model.modelID,
          turns: 0,
          inputTokens: 0,
          outputTokens: 0,
          reasoningTokens: 0,
          cacheReadTokens: 0,
          cacheWriteTokens: 0,
        };
        byKey.set(key, row);
      }
      row.turns += model.turns || 0;
      row.inputTokens += model.inputTokens || 0;
      row.outputTokens += model.outputTokens || 0;
      row.reasoningTokens += model.reasoningTokens || 0;
      row.cacheReadTokens += model.cacheReadTokens || 0;
      row.cacheWriteTokens += model.cacheWriteTokens || 0;
    }
  }

  const rows: UsageModelTotals[] = [];
  for (const agg of byKey.values()) {
    const totalTokens =
      agg.inputTokens + agg.outputTokens + agg.reasoningTokens + agg.cacheReadTokens + agg.cacheWriteTokens;
    rows.push({
      providerID: agg.providerID,
      modelID: agg.modelID,
      turns: agg.turns,
      inputTokens: agg.inputTokens,
      outputTokens: agg.outputTokens,
      totalTokens,
      cost: estimateTokenCostUsd(agg.modelID, {
        input: agg.inputTokens,
        output: agg.outputTokens,
        reasoning: agg.reasoningTokens,
        cacheRead: agg.cacheReadTokens,
        cacheWrite: agg.cacheWriteTokens,
      }),
    });
  }
  rows.sort((a, b) => b.totalTokens - a.totalTokens || (b.cost ?? 0) - (a.cost ?? 0));
  return rows;
}
