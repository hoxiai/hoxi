// web/src/__tests__/usage-history.test.ts
import { hoxiAPI, type DevPulseHistoryDay, type DevPulseHistoryResponse, type ModelPricingItem } from "../services/api";
import { leaderboardState, setLeaderboardState } from "../store/leaderboard-state";
import {
  loadUsageHistory,
  priceHistoryDay,
  usageHistory,
  usageHistoryLoading,
  usageHistoryModels,
  usageSelection,
  usageHistoryTotals,
  USAGE_OVERVIEW_DAYS,
  type UsageHistoryDay,
} from "../store/usage-history";
import { assertEqual, assertTrue, describe, it } from "./test-harness";
import { formatDurationMs } from "../components/settings/usage-format";
import { locale, setLocale } from "../i18n";

function pricingItem(slug: string, input: number, output: number, currency = "USD"): ModelPricingItem {
  return {
    slug,
    name: slug,
    vendor: "test",
    contextWindow: 200_000,
    price: { currency, input, output },
  };
}

function withPricingCatalog(items: ModelPricingItem[], run: () => void) {
  const saved = leaderboardState.pricingCatalog;
  setLeaderboardState("pricingCatalog", items);
  try {
    run();
  } finally {
    setLeaderboardState("pricingCatalog", saved);
  }
}

function historyDay(partial: Partial<DevPulseHistoryDay>): DevPulseHistoryDay {
  return {
    date: "2026-10-22",
    turns: 0,
    sessions: 0,
    inputTokens: 0,
    outputTokens: 0,
    reasoningTokens: 0,
    cacheReadTokens: 0,
    cacheWriteTokens: 0,
    toolCallsTotal: 0,
    toolCallsFailed: 0,
    commits: 0,
    additions: 0,
    deletions: 0,
    files: 0,
    models: [],
    ...partial,
  };
}

function usageDay(
  partial: Partial<UsageHistoryDay> & { date: string }
): UsageHistoryDay {
  return { ...historyDay({ date: partial.date }), totalTokens: 0, cost: 0, unpricedModels: [], ...partial };
}

describe("usage history pricing", () => {
  it("formats rounded durations with valid minute and second fields", () => {
    const previous = locale();
    setLocale("en-US");
    try {
      assertEqual(formatDurationMs(119_999), "2m 0s", "duration carry");
      assertEqual(formatDurationMs(59_999), "1m 0s", "second carry");
    } finally {
      setLocale(previous);
    }
  });

  it("prices every model in a day with the shared resolver", () => {
    withPricingCatalog(
      [pricingItem("claude-opus-5", 5, 25), pricingItem("deepseek-v4-flash", 3, 9, "CNY")],
      () => {
        const day = priceHistoryDay(
          historyDay({
            inputTokens: 2_000_000,
            outputTokens: 100_000,
            models: [
              {
                providerID: "anthropic",
                modelID: "claude-opus-5",
                turns: 1,
                inputTokens: 1_000_000,
                outputTokens: 100_000,
                reasoningTokens: 0,
                cacheReadTokens: 0,
                cacheWriteTokens: 0,
              },
              {
                providerID: "subapi",
                modelID: "deepseek-flash",
                turns: 1,
                inputTokens: 1_000_000,
                outputTokens: 0,
                reasoningTokens: 0,
                cacheReadTokens: 0,
                cacheWriteTokens: 0,
              },
            ],
          })
        );
        // 1M×5 + 100k×25 = 7.5 ; `deepseek-flash` resolves to `deepseek-v4-flash`
        // at ¥3/M ÷ 7 = $0.4286/M.
        const want = 7.5 + 3 / 7;
        assertTrue(Math.abs(day.cost - want) < 1e-9, `cost = ${day.cost}, want ${want}`);
        assertEqual(day.totalTokens, 2_100_000, "total tokens");
        assertEqual(day.unpricedModels.length, 0, "no unpriced models");
      }
    );
  });

  it("reports models the catalog cannot price instead of billing them at zero", () => {
    withPricingCatalog([pricingItem("claude-opus-5", 5, 25)], () => {
      const day = priceHistoryDay(
        historyDay({
          inputTokens: 1_000_000,
          models: [
            {
              providerID: "custom",
              modelID: "my-private-model",
              turns: 1,
              inputTokens: 1_000_000,
              outputTokens: 0,
              reasoningTokens: 0,
              cacheReadTokens: 0,
              cacheWriteTokens: 0,
            },
          ],
        })
      );
      assertEqual(day.cost, 0, "unpriced model contributes nothing");
      assertEqual(day.unpricedModels.join(","), "my-private-model", "flagged as unpriced");
    });
  });

  it("sums totals across days without re-pricing", () => {
    const days = [
      usageDay({ date: "2026-10-21", turns: 3, commits: 1, additions: 10, deletions: 2, totalTokens: 1000, cost: 1.5 }),
      usageDay({
        date: "2026-10-22",
        turns: 5,
        commits: 2,
        additions: 4,
        deletions: 1,
        totalTokens: 2000,
        cost: 2.5,
        unpricedModels: ["x"],
      }),
    ];
    const totals = usageHistoryTotals(days);
    assertEqual(totals.days, 2, "day count");
    assertEqual(totals.turns, 8, "turns");
    assertEqual(totals.totalTokens, 3000, "tokens");
    assertEqual(totals.cost, 4, "cost");
    assertEqual(totals.commits, 3, "commits");
    assertEqual(totals.additions, 14, "additions");
    assertEqual(totals.unpricedModels.join(","), "x", "unpriced union");
  });

  it("rolls per-day model stats into one priced row per provider/model", () => {
    withPricingCatalog([pricingItem("claude-opus-5", 5, 25)], () => {
      const days: UsageHistoryDay[] = [
        usageDay({
          date: "2026-10-21",
          models: [
            {
              providerID: "anthropic",
              modelID: "claude-opus-5",
              turns: 1,
              inputTokens: 1_000_000,
              outputTokens: 0,
              reasoningTokens: 0,
              cacheReadTokens: 0,
              cacheWriteTokens: 0,
              durationSumMs: 1000,
              durationSamples: 1,
            },
          ],
        }),
        usageDay({
          date: "2026-10-22",
          models: [
            {
              providerID: "anthropic",
              modelID: "claude-opus-5",
              turns: 1,
              inputTokens: 0,
              outputTokens: 100_000,
              reasoningTokens: 0,
              cacheReadTokens: 0,
              cacheWriteTokens: 0,
              durationSumMs: 3000,
              durationSamples: 1,
            },
            {
              providerID: "custom",
              modelID: "unknown-x",
              turns: 1,
              inputTokens: 500,
              outputTokens: 0,
              reasoningTokens: 0,
              cacheReadTokens: 0,
              cacheWriteTokens: 0,
            },
          ],
        }),
      ];
      const rows = usageHistoryModels(days);
      const opus = rows.find((row) => row.modelID === "claude-opus-5")!;
      assertEqual(opus.turns, 2, "same model merges across days");
      assertEqual(opus.totalTokens, 1_100_000, "merged tokens");
      assertEqual(opus.cost, 7.5, "merged cost must not be re-priced per day");
      assertEqual(opus.avgDurationMs, 2000, "averages duration across days");
      const unknown = rows.find((row) => row.modelID === "unknown-x")!;
      assertEqual(unknown.cost, null, "unpriced model keeps a null cost");
      assertEqual(unknown.avgDurationMs, null, "no samples means no average");
      assertEqual(rows[0].modelID, "claude-opus-5", "sorted by tokens desc");
    });
  });

  it("loads the full year for the overview selection", async () => {
    const origHistory = hoxiAPI.getDevPulseHistory;
    let seenDays = -1;
    try {
      hoxiAPI.getDevPulseHistory = async (days) => {
        seenDays = days ?? -1;
        return { days: [], startDate: "2025-10-23", endDate: "2026-10-23", timezone: "UTC" };
      };
      await loadUsageHistory({ kind: "overview" }, true);
      assertEqual(seenDays, USAGE_OVERVIEW_DAYS, "overview fetches the full year");
    } finally {
      hoxiAPI.getDevPulseHistory = origHistory;
    }
  });

  it("ignores stale loadUsageHistory responses when range changes", async () => {
    const origHistory = hoxiAPI.getDevPulseHistory;
    let resolve7!: (res: DevPulseHistoryResponse) => void;
    let resolve30!: (res: DevPulseHistoryResponse) => void;
    try {
      hoxiAPI.getDevPulseHistory = (days) => {
        if (days === 7) return new Promise<DevPulseHistoryResponse>((r) => { resolve7 = r; });
        return new Promise<DevPulseHistoryResponse>((r) => { resolve30 = r; });
      };

      const p7 = loadUsageHistory({ kind: "days", days: 7 }, true);
      await new Promise((r) => setTimeout(r, 10));
      const p30 = loadUsageHistory({ kind: "days", days: 30 }, true);
      await new Promise((r) => setTimeout(r, 10));
      const sel = usageSelection();
      assertTrue(sel.kind === "days" && sel.days === 30, "selection tracks the 30d window");
      assertEqual(usageHistoryLoading(), true);

      resolve30({
        days: [historyDay({ date: "2026-10-22", turns: 9 })],
        startDate: "2026-09-23",
        endDate: "2026-10-22",
        timezone: "UTC",
      });
      await p30;
      assertEqual(usageHistory().length, 1);
      assertEqual(usageHistory()[0].turns, 9);
      assertEqual(usageHistoryLoading(), false);

      // Stale 7-day response finishes later and must be ignored
      resolve7({
        days: [historyDay({ date: "2026-10-22", turns: 1 })],
        startDate: "2026-10-16",
        endDate: "2026-10-22",
        timezone: "UTC",
      });
      await p7;
      assertEqual(usageHistory()[0].turns, 9, "stale 7d response must not overwrite 30d state");
    } finally {
      hoxiAPI.getDevPulseHistory = origHistory;
    }
  });
});
