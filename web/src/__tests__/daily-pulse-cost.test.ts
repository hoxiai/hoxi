// web/src/__tests__/daily-pulse-cost.test.ts
import { hoxiAPI, type GitDailyPulseResponse, type ModelPricingItem } from "../services/api";
import {
  computeDailyDevPulseStats,
  estimateModelMarketCostUsd,
  refreshDailyDevPulse,
} from "../store/daily-pulse";
import { leaderboardState, setLeaderboardState } from "../store/leaderboard-state";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

function pricingItem(partial: Partial<ModelPricingItem> & { slug: string }): ModelPricingItem {
  return {
    name: partial.name || partial.slug,
    vendor: partial.vendor || "test",
    contextWindow: partial.contextWindow || 200_000,
    price: partial.price || { currency: "USD", input: 0, output: 0 },
    ...partial,
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

function approx(actual: number | null, expected: number, label: string) {
  assertTrue(actual !== null, `${label}: expected a market estimate, got null`);
  assertTrue(
    Math.abs((actual as number) - expected) < 1e-9,
    `${label}: expected ${expected}, got ${actual}`
  );
}

describe("daily pulse market cost", () => {
  it("prices every token class at USD market rates", () => {
    withPricingCatalog(
      [
        pricingItem({
          slug: "claude-opus-5",
          apiName: "claude-opus-5",
          price: { currency: "USD", input: 5, output: 25, cacheRead: 0.5 },
        }),
      ],
      () => {
        approx(
          estimateModelMarketCostUsd("claude-opus-5", {
            input: 1_000_000,
            output: 100_000,
            reasoning: 0,
            cacheRead: 2_000_000,
            cacheWrite: 0,
          }),
          8.5,
          "usd breakdown"
        );
      }
    );
  });

  it("converts CNY market prices to USD", () => {
    withPricingCatalog(
      [
        pricingItem({
          slug: "glm-5.3-flash",
          apiName: "glm-5.3-flash",
          price: { currency: "CNY", input: 7.2, output: 14.4 },
        }),
      ],
      () => {
        approx(
          estimateModelMarketCostUsd("glm-5.3-flash", {
            input: 1_000_000,
            output: 500_000,
            reasoning: 0,
            cacheRead: 0,
            cacheWrite: 0,
          }),
          2.0,
          "cny conversion"
        );
      }
    );
  });

  it("bills reasoning tokens at the output market rate", () => {
    withPricingCatalog(
      [
        pricingItem({
          slug: "think-model",
          price: { currency: "USD", input: 2, output: 8 },
        }),
      ],
      () => {
        approx(
          estimateModelMarketCostUsd("think-model", {
            input: 0,
            output: 0,
            reasoning: 500_000,
            cacheRead: 0,
            cacheWrite: 0,
          }),
          4.0,
          "reasoning at output rate"
        );
      }
    );
  });

  it("falls back to 0.1x/1.25x input rate when cache pricing is absent", () => {
    withPricingCatalog(
      [
        pricingItem({
          slug: "cache-model",
          price: { currency: "USD", input: 10, output: 20 },
        }),
      ],
      () => {
        approx(
          estimateModelMarketCostUsd("cache-model", {
            input: 0,
            output: 0,
            reasoning: 0,
            cacheRead: 1_000_000,
            cacheWrite: 1_000_000,
          }),
          13.5,
          "cache ratios"
        );
      }
    );
  });

  it("normalizes provider prefixes and date suffixes when matching", () => {
    withPricingCatalog(
      [
        pricingItem({
          slug: "claude-opus-5",
          apiName: "claude-opus-5",
          price: { currency: "USD", input: 5, output: 25 },
        }),
      ],
      () => {
        approx(
          estimateModelMarketCostUsd("models/claude-opus-5-20260101", {
            input: 200_000,
            output: 0,
            reasoning: 0,
            cacheRead: 0,
            cacheWrite: 0,
          }),
          1.0,
          "normalized match"
        );
      }
    );
  });

  it("returns null for models without a market price", () => {
    withPricingCatalog([pricingItem({ slug: "known-model", price: { currency: "USD", input: 1, output: 2 } })], () => {
      assertEqual(
        estimateModelMarketCostUsd("my-private-model", {
          input: 1_000_000,
          output: 0,
          reasoning: 0,
          cacheRead: 0,
          cacheWrite: 0,
        }),
        null,
        "unmatched model"
      );
    });
  });

  it("overrides recorded cost with market price and keeps recorded cost when unmatched", async () => {
    const fixture: GitDailyPulseResponse = {
      todayCommits: 0,
      committedAdditions: 0,
      committedDeletions: 0,
      committedFiles: 0,
      uncommittedAdditions: 0,
      uncommittedDeletions: 0,
      uncommittedFiles: 0,
      dbTotalTokens: 1_600_000,
      dbInputTokens: 1_500_000,
      dbOutputTokens: 100_000,
      dbTotalCost: 15.8,
      dbTurnsCount: 2,
      dbModels: [
        {
          providerID: "anthropic",
          modelID: "claude-opus-5",
          turns: 1,
          totalTokens: 1_100_000,
          inputTokens: 1_000_000,
          outputTokens: 100_000,
          reasoningTokens: 0,
          cacheReadTokens: 0,
          cacheWriteTokens: 0,
          cost: 12.3,
        },
        {
          providerID: "custom",
          modelID: "my-private-model",
          turns: 1,
          totalTokens: 500_000,
          inputTokens: 500_000,
          outputTokens: 0,
          reasoningTokens: 0,
          cacheReadTokens: 0,
          cacheWriteTokens: 0,
          cost: 3.5,
        },
      ],
    };
    const originalGetGitDailyPulse = hoxiAPI.getGitDailyPulse;
    const savedCatalog = leaderboardState.pricingCatalog;
    hoxiAPI.getGitDailyPulse = async () => fixture;
    setLeaderboardState("pricingCatalog", [
      pricingItem({
        slug: "claude-opus-5",
        apiName: "claude-opus-5",
        price: { currency: "USD", input: 5, output: 25 },
      }),
    ]);
    try {
      await refreshDailyDevPulse(true);
      const stats = computeDailyDevPulseStats();
      approx(stats.totalCost, 11.0, "total market cost");
      const matched = stats.modelsUsed.find((m) => m.modelID === "claude-opus-5");
      const unmatched = stats.modelsUsed.find((m) => m.modelID === "my-private-model");
      approx(matched?.cost ?? null, 7.5, "matched model market cost");
      approx(unmatched?.cost ?? null, 3.5, "unmatched model recorded cost");
    } finally {
      hoxiAPI.getGitDailyPulse = originalGetGitDailyPulse;
      setLeaderboardState("pricingCatalog", savedCatalog);
    }
  });
});
