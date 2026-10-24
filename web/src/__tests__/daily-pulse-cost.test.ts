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

  it("converts CNY market prices to USD with the shared fallback rate", () => {
    withPricingCatalog(
      [
        pricingItem({
          slug: "glm-5.3-flash",
          apiName: "glm-5.3-flash",
          price: { currency: "CNY", input: 7, output: 14 },
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

  it("returns null when the id names no known vendor", () => {
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

  it("prices an unknown variant of a known vendor from its vendor price band", () => {
    withPricingCatalog(
      [
        pricingItem({ slug: "deepseek-v4-flash", price: { currency: "CNY", input: 3, output: 9, cacheRead: 0.1 } }),
        pricingItem({ slug: "deepseek-v4-pro", price: { currency: "CNY", input: 9, output: 27, cacheRead: 0.3 } }),
        pricingItem({ slug: "claude-opus-5", price: { currency: "USD", input: 5, output: 25 } }),
      ],
      () => {
        // `deepseek-flash` is not in the catalog under any alias, but it names
        // a vendor and a budget tier, so it must land in DeepSeek budget rates
        // instead of the cross-vendor mean (which is ~7x too high).
        approx(
          estimateModelMarketCostUsd("deepseek-flash", {
            input: 1_000_000,
            output: 0,
            reasoning: 0,
            cacheRead: 0,
            cacheWrite: 0,
          }),
          3 / 7,
          "vendor budget band"
        );
      }
    );
  });

  it("treats an unknown free variant as free rather than pricing it", () => {
    withPricingCatalog([pricingItem({ slug: "deepseek-v4-flash", price: { currency: "USD", input: 1, output: 2 } })], () => {
      assertEqual(
        estimateModelMarketCostUsd("deepseek-v9-flash-free", {
          input: 1_000_000,
          output: 100_000,
          reasoning: 0,
          cacheRead: 0,
          cacheWrite: 0,
        }),
        0,
        "free variant"
      );
    });
  });

  it("strips a 4-digit snapshot label before falling back", () => {
    withPricingCatalog(
      [pricingItem({ slug: "deepseek-v4-1-flash", price: { currency: "USD", input: 0.3, output: 1.2 } })],
      () => {
        approx(
          estimateModelMarketCostUsd("deepseek-v4.1-flash-1017", {
            input: 1_000_000,
            output: 0,
            reasoning: 0,
            cacheRead: 0,
            cacheWrite: 0,
          }),
          0.3,
          "mmdd suffix"
        );
      }
    );
  });

  it("matches a version-less reseller id to the real upstream row", () => {
    withPricingCatalog(
      [
        pricingItem({ slug: "deepseek-v4-flash", price: { currency: "USD", input: 0.43, output: 1.29, cacheRead: 0.014 } }),
        pricingItem({ slug: "deepseek-v4-pro", price: { currency: "USD", input: 1.29, output: 3.86, cacheRead: 0.043 } }),
      ],
      () => {
        // `deepseek-flash` has no version, but every word of it appears in
        // `deepseek-v4-flash`, so it must inherit that row (including the cheap
        // cache rate) rather than a band median.
        approx(
          estimateModelMarketCostUsd("deepseek-flash", {
            input: 1_000_000,
            output: 0,
            reasoning: 0,
            cacheRead: 10_000_000,
            cacheWrite: 0,
          }),
          0.57,
          "version-less flash"
        );
        approx(
          estimateModelMarketCostUsd("QoderCN-DeepSeek-Flash", {
            input: 1_000_000,
            output: 0,
            reasoning: 0,
            cacheRead: 0,
            cacheWrite: 0,
          }),
          0.43,
          "prefixed version-less id"
        );
      }
    );
  });

  it("prefers the base model over a point release when traits tie", () => {
    withPricingCatalog(
      [
        pricingItem({ slug: "gpt-6-sol", price: { currency: "USD", input: 2, output: 10 } }),
        pricingItem({ slug: "gpt-6-1-sol", price: { currency: "USD", input: 2, output: 10 } }),
        pricingItem({ slug: "gpt-6-sol-vision", price: { currency: "USD", input: 9, output: 40 } }),
      ],
      () => {
        approx(
          estimateModelMarketCostUsd("gpt-6.2-sol", {
            input: 1_000_000,
            output: 0,
            reasoning: 0,
            cacheRead: 0,
            cacheWrite: 0,
          }),
          2,
          "version drift"
        );
      }
    );
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
      // The UI must be able to say the total is partial instead of silently
      // showing a number that omits an unpriced model.
      assertEqual(stats.unpricedModels.join(","), "my-private-model", "unpriced models");
    } finally {
      hoxiAPI.getGitDailyPulse = originalGetGitDailyPulse;
      setLeaderboardState("pricingCatalog", savedCatalog);
    }
  });

  it("prices deployment variants and reseller ids from the family row", () => {
    withPricingCatalog(
      [
        pricingItem({
          slug: "deepseek-v4.1-flash",
          price: { currency: "USD", input: 0.3, output: 1.2 },
        }),
      ],
      () => {
        // Provider never reports a recorded cost, so an exact-key miss used to
        // show $0.00 for the model that produced almost all of the day's spend.
        approx(
          estimateModelMarketCostUsd("deepseek-v4.1-flash-solo", {
            input: 1_000_000,
            output: 0,
            reasoning: 0,
            cacheRead: 0,
            cacheWrite: 0,
          }),
          0.3,
          "deployment suffix"
        );
        approx(
          estimateModelMarketCostUsd("CodeArts-deepseek-v4.1-flash", {
            input: 1_000_000,
            output: 0,
            reasoning: 0,
            cacheRead: 0,
            cacheWrite: 0,
          }),
          0.3,
          "reseller prefix"
        );
      }
    );
  });

  it("strips Antigravity effort suffixes (-high/-medium/-low) to match catalog models", () => {
    withPricingCatalog(
      [
        pricingItem({
          slug: "claude-opus-5-5",
          price: { currency: "USD", input: 5, output: 25 },
        }),
        pricingItem({
          slug: "gemini-3.8-flash",
          price: { currency: "USD", input: 0.15, output: 0.6 },
        }),
      ],
      () => {
        approx(
          estimateModelMarketCostUsd("claude-opus-5-5-high", {
            input: 1_000_000,
            output: 100_000,
            reasoning: 0,
            cacheRead: 0,
            cacheWrite: 0,
          }),
          7.5,
          "claude-opus-5-5-high matches claude-opus-5-5"
        );
        approx(
          estimateModelMarketCostUsd("gemini-3.8-flash-low", {
            input: 1_000_000,
            output: 0,
            reasoning: 0,
            cacheRead: 0,
            cacheWrite: 0,
          }),
          0.15,
          "gemini-3.8-flash-low matches gemini-3.8-flash"
        );
      }
    );
  });
});
