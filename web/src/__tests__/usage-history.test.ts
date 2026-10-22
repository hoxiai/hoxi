// web/src/__tests__/usage-history.test.ts
import type { DevPulseHistoryDay, ModelPricingItem } from "../services/api";
import { leaderboardState, setLeaderboardState } from "../store/leaderboard-state";
import { priceHistoryDay, usageHistoryTotals, type UsageHistoryDay } from "../store/usage-history";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

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
});
