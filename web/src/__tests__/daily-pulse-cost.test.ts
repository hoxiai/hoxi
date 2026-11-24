// web/src/__tests__/daily-pulse-cost.test.ts
import { hoxiAPI, type GitDailyPulseResponse, type ModelPricingItem } from "../services/api";
import {
  computeDailyDevPulseStats,
  estimateModelMarketCostUsd,
  refreshDailyDevPulse,
} from "../store/daily-pulse";
import { leaderboardState, setLeaderboardState } from "../store/leaderboard-state";
import { indexSession } from "../store/session-index";
import { setWorkspace, workspace } from "../store/workspace-state";
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

  it("queries global daily pulse without scoping to a single project and keeps code stats stable across project switches", async () => {
    const origGet = hoxiAPI.getGitDailyPulse;
    const savedRoot = workspace.rootDir;
    const savedSid = workspace.activeSessionID;
    let seenDir: string | undefined = "unset";
    let seenSid: string | undefined = "unset";
    try {
      hoxiAPI.getGitDailyPulse = async (dir, sid) => {
        seenDir = dir;
        seenSid = sid;
        return {
          todayCommits: 4,
          committedAdditions: 120,
          committedDeletions: 30,
          committedFiles: 5,
          uncommittedAdditions: 15,
          uncommittedDeletions: 5,
          uncommittedFiles: 2,
          changedFiles: 6,
        };
      };

      setWorkspace({ rootDir: "/repo-a", activeSessionID: "ses_a" });
      await refreshDailyDevPulse(true);
      assertEqual(seenDir, undefined, "daily pulse must not scope to a single project rootDir");
      assertEqual(seenSid, undefined, "daily pulse must not scope to a single sessionID");
      assertEqual(computeDailyDevPulseStats().totalAdditions, 135);
      assertEqual(computeDailyDevPulseStats().totalDeletions, 35);

      setWorkspace({ rootDir: "/repo-b", activeSessionID: "ses_b" });
      assertEqual(computeDailyDevPulseStats().totalAdditions, 135, "switching projects keeps global additions");
      assertEqual(computeDailyDevPulseStats().totalDeletions, 35, "switching projects keeps global deletions");
    } finally {
      hoxiAPI.getGitDailyPulse = origGet;
      setWorkspace({ rootDir: savedRoot, activeSessionID: savedSid });
    }
  });

  it("merges in-flight turn deltas across top-level sessions while skipping subagents and imported turns", async () => {
    const origGet = hoxiAPI.getGitDailyPulse;
    const savedActive = workspace.activeSessionID;
    const savedMessages = workspace.messagesBySession;
    const savedSessions = workspace.sessions;
    const savedIndex = workspace.sessionIndex;
    const now = Date.now();
    try {
      hoxiAPI.getGitDailyPulse = async () => ({
        todayCommits: 0,
        committedAdditions: 0,
        committedDeletions: 0,
        committedFiles: 0,
        uncommittedAdditions: 0,
        uncommittedDeletions: 0,
        uncommittedFiles: 0,
        dbTurnsCount: 2,
        dbSessionsCount: 2,
        dbInputTokens: 1000,
        dbOutputTokens: 200,
        dbReasoningTokens: 50,
        dbCacheReadTokens: 300,
        dbCacheWriteTokens: 0,
        dbTotalTokens: 1550,
        dbLatestTurnTs: now - 1000,
        dbActiveTurns: {
          msg_a_inflight: {
            input: 400,
            output: 50,
            reasoning: 10,
            cacheRead: 100,
            cacheWrite: 0,
          },
        },
        dbModels: [
          {
            providerID: "openai",
            modelID: "gpt-5.4",
            turns: 2,
            totalTokens: 1550,
            inputTokens: 1000,
            outputTokens: 200,
            reasoningTokens: 50,
            cacheReadTokens: 300,
            cacheWriteTokens: 0,
            cost: 0,
          },
        ],
      });

      setWorkspace({
        activeSessionID: "ses_main",
        sessions: [
          { id: "ses_main", title: "Main", time: { created: now - 5000, updated: now } },
          { id: "ses_bg", title: "Background", time: { created: now - 5000, updated: now } },
          { id: "ses_sub", title: "Subagent", parentID: "ses_main", time: { created: now - 5000, updated: now } },
        ],
        sessionIndex: {
          ses_sub: { parentID: "ses_main", updated: now },
        },
        messagesBySession: {
          ses_main: [
            // In-flight turn started BEFORE latestDbTs: must add only positive delta (+600 in, +100 out, +20 reasoning, +200 cacheRead) and 0 extra turns.
            {
              info: {
                id: "msg_a_inflight",
                sessionID: "ses_main",
                role: "assistant",
                providerID: "openai",
                modelID: "gpt-5.4",
                time: { created: now - 3000 },
                tokens: { input: 1000, output: 150, reasoning: 30, cache: { read: 300, write: 0 } },
              },
              parts: [],
            },
            // Imported turn newer than latestDbTs must be excluded.
            {
              info: {
                id: "msg_imported_live",
                sessionID: "ses_main",
                role: "assistant",
                providerID: "openai",
                modelID: "gpt-5.4",
                imported: true,
                time: { created: now - 200 },
                tokens: { input: 9999, output: 999, reasoning: 0, cache: { read: 0, write: 0 } },
              },
              parts: [],
            },
          ],
          // Parallel background top-level session with a brand-new turn that only has reasoning + cacheWrite.
          ses_bg: [
            {
              info: {
                id: "msg_a_bg_new",
                sessionID: "ses_bg",
                role: "assistant",
                providerID: "anthropic",
                modelID: "claude-opus-5",
                time: { created: now - 100 },
                tokens: { input: 0, output: 0, reasoning: 70, cache: { read: 0, write: 80 } },
              },
              parts: [],
            },
          ],
          // Subagent session turn must NEVER be added on top of parent.
          ses_sub: [
            {
              info: {
                id: "msg_a_sub_live",
                sessionID: "ses_sub",
                role: "assistant",
                providerID: "openai",
                modelID: "gpt-5.4",
                time: { created: now - 50 },
                tokens: { input: 5000, output: 500, reasoning: 0, cache: { read: 0, write: 0 } },
              },
              parts: [],
            },
          ],
        },
      });

      await refreshDailyDevPulse(true);
      const stats = computeDailyDevPulseStats();
      assertEqual(stats.inputTokens, 1600, "input = 1000 db + 600 in-flight delta");
      assertEqual(stats.outputTokens, 300, "output = 200 db + 100 in-flight delta");
      assertEqual(stats.reasoningTokens, 140, "reasoning = 50 db + 20 delta + 70 bg");
      assertEqual(stats.cacheReadTokens, 500, "cacheRead = 300 db + 200 delta");
      assertEqual(stats.cacheWriteTokens, 80, "cacheWrite = 80 bg");
      assertEqual(stats.totalTokens, 2620, "total = 1600 + 300 + 140 + 500 + 80");
      assertEqual(stats.todayTurnsCount, 3, "turns = 2 db + 0 in-flight + 1 bg new");
    } finally {
      hoxiAPI.getGitDailyPulse = origGet;
      setWorkspace({
        activeSessionID: savedActive,
        messagesBySession: savedMessages,
        sessions: savedSessions,
        sessionIndex: savedIndex,
      });
    }
  });

  it("skips legacy pre-fix imported turns in frontend live merge while keeping post-import Hoxi turns", async () => {
    const origGet = hoxiAPI.getGitDailyPulse;
    const savedActive = workspace.activeSessionID;
    const savedMessages = workspace.messagesBySession;
    const savedSessions = workspace.sessions;
    const savedIndex = workspace.sessionIndex;
    const now = Date.now();
    try {
      // Backend already filtered out the legacy imported turns, so dbTotals are 0.
      hoxiAPI.getGitDailyPulse = async () => ({
        todayCommits: 0,
        committedAdditions: 0,
        committedDeletions: 0,
        committedFiles: 0,
        uncommittedAdditions: 0,
        uncommittedDeletions: 0,
        uncommittedFiles: 0,
        dbTurnsCount: 0,
        dbSessionsCount: 0,
        dbInputTokens: 0,
        dbOutputTokens: 0,
        dbReasoningTokens: 0,
        dbCacheReadTokens: 0,
        dbCacheWriteTokens: 0,
        dbTotalTokens: 0,
        dbLatestTurnTs: 0,
        dbModels: [],
      });

      // Index a cross-project legacy imported session (absent from workspace.sessions).
      setWorkspace({
        activeSessionID: "ses_imp_active",
        sessions: [
          {
            id: "ses_imp_active",
            title: "[Claude Code] Imported Task",
            time: { created: now - 5000, updated: now },
          },
        ],
        sessionIndex: {},
        messagesBySession: {
          ses_imp_active: [
            // Legacy pre-fix imported turn without info.imported flag: must be skipped.
            {
              info: {
                id: "msg_019legacy_today",
                sessionID: "ses_imp_active",
                role: "assistant",
                providerID: "anthropic",
                modelID: "claude-opus-5",
                time: { created: now - 2000 },
                tokens: { input: 9000, output: 500, reasoning: 0, cache: { read: 0, write: 0 } },
              },
              parts: [],
            },
            // Post-import turn run inside Hoxi (msg_a_*): must still count.
            {
              info: {
                id: "msg_a_post_live",
                sessionID: "ses_imp_active",
                role: "assistant",
                providerID: "anthropic",
                modelID: "claude-opus-5",
                time: { created: now - 1000 },
                tokens: { input: 300, output: 40, reasoning: 10, cache: { read: 50, write: 0 } },
              },
              parts: [],
            },
          ],
          ses_imp_other_project: [
            // Legacy imported turn from another project tracked via sessionIndex.imported.
            {
              info: {
                id: "msg_019other_proj",
                sessionID: "ses_imp_other_project",
                role: "assistant",
                providerID: "openai",
                modelID: "gpt-5.4",
                time: { created: now - 1500 },
                tokens: { input: 7000, output: 300, reasoning: 0, cache: { read: 0, write: 0 } },
              },
              parts: [],
            },
          ],
        },
      });
      indexSession({
        id: "ses_imp_other_project",
        title: "[Codex] Other Project Thread",
        directory: "/other-repo",
        time: { created: now - 5000, updated: now },
      });

      await refreshDailyDevPulse(true);
      const stats = computeDailyDevPulseStats();
      assertEqual(stats.inputTokens, 300, "only post-import Hoxi turn input counted");
      assertEqual(stats.outputTokens, 40, "only post-import Hoxi turn output counted");
      assertEqual(stats.reasoningTokens, 10, "only post-import Hoxi turn reasoning counted");
      assertEqual(stats.cacheReadTokens, 50, "only post-import Hoxi turn cacheRead counted");
      assertEqual(stats.totalTokens, 400, "legacy imported turns must not leak back in");
      assertEqual(stats.todayTurnsCount, 1, "only 1 post-import turn counted");
    } finally {
      hoxiAPI.getGitDailyPulse = origGet;
      setWorkspace({
        activeSessionID: savedActive,
        messagesBySession: savedMessages,
        sessions: savedSessions,
        sessionIndex: savedIndex,
      });
    }
  });
});
