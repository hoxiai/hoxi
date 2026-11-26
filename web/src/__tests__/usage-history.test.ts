// web/src/__tests__/usage-history.test.ts
import { hoxiAPI, type DevPulseHistoryDay, type DevPulseHistoryResponse, type ModelPricingItem } from "../services/api";
import { leaderboardState, setLeaderboardState } from "../store/leaderboard-state";
import { setWorkspace, workspace } from "../store/workspace-state";
import {
  clearUsageHistoryCache,
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

  it("queries global usage history without scoping to a single project", async () => {
    const origHistory = hoxiAPI.getDevPulseHistory;
    const savedRoot = workspace.rootDir;
    const savedSid = workspace.activeSessionID;
    let seenDir: string | undefined = "unset";
    let seenSid: string | undefined = "unset";
    try {
      hoxiAPI.getDevPulseHistory = async (_days, dir, window) => {
        seenDir = dir;
        seenSid = window?.sessionID;
        return {
          days: [historyDay({ date: "2026-11-05", additions: 88, deletions: 22, commits: 2 })],
          startDate: "2025-11-06",
          endDate: "2026-11-05",
          timezone: "UTC",
        };
      };

      setWorkspace({ rootDir: "/repo-a", activeSessionID: "ses_a" });
      await loadUsageHistory({ kind: "overview" }, true);
      assertEqual(seenDir, undefined, "usage history must not scope to a single project rootDir");
      assertEqual(seenSid, undefined, "usage history must not scope to a single sessionID");
      assertEqual(usageHistory()[0].additions, 88);
      assertEqual(usageHistory()[0].deletions, 22);
    } finally {
      hoxiAPI.getDevPulseHistory = origHistory;
      setWorkspace({ rootDir: savedRoot, activeSessionID: savedSid });
    }
  });

  it("slices 7d/14d/30d and covered custom windows from fresh cached overview without refetching", async () => {
    const origHistory = hoxiAPI.getDevPulseHistory;
    let fetchCalls = 0;
    let sawIncludeTools: boolean | undefined = true;
    const now = new Date();
    const fmt = (offset: number) => {
      const d = new Date(now.getFullYear(), now.getMonth(), now.getDate() + offset);
      return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
    };
    const days30: DevPulseHistoryDay[] = [];
    for (let i = -29; i <= 0; i++) {
      days30.push(historyDay({ date: fmt(i), turns: i + 30 }));
    }
    clearUsageHistoryCache();
    try {
      hoxiAPI.getDevPulseHistory = async (_days, _dir, window) => {
        fetchCalls++;
        sawIncludeTools = window?.includeTools;
        return {
          days: days30,
          startDate: days30[0].date,
          endDate: days30[days30.length - 1].date,
          timezone: "UTC",
        };
      };

      await loadUsageHistory({ kind: "overview" }, false);
      assertEqual(fetchCalls, 1, "initial overview fetches once");
      assertEqual(sawIncludeTools, false, "usage history skips unused SQLite tool scans");
      assertEqual(usageHistory().length, 30);

      // Re-entering overview within TTL reuses cached overview with zero backend calls.
      await loadUsageHistory({ kind: "overview" }, false);
      assertEqual(fetchCalls, 1, "fresh overview remount hits cache");

      // Switching to 7d slices locally from cached overview.
      await loadUsageHistory({ kind: "days", days: 7 }, false);
      assertEqual(fetchCalls, 1, "7d slices locally from cached overview");
      assertEqual(usageHistory().length, 7);
      assertEqual(usageHistory()[6].date, fmt(0));

      // Covered custom range slices locally too.
      await loadUsageHistory({ kind: "custom", range: { start: fmt(-4), end: fmt(-2) } }, false);
      assertEqual(fetchCalls, 1, "covered custom range slices locally");
      assertEqual(usageHistory().length, 3);
      assertEqual(usageHistory()[0].date, fmt(-4));
      assertEqual(usageHistory()[2].date, fmt(-2));

      hoxiAPI.getDevPulseHistory = async () => {
        fetchCalls++;
        return { days: [historyDay({ date: fmt(0), turns: 99 })], startDate: fmt(0), endDate: fmt(0), timezone: "UTC" };
      };
      await loadUsageHistory({ kind: "days", days: 7 }, true);
      await loadUsageHistory({ kind: "days", days: 14 }, false);
      assertEqual(fetchCalls, 2, "switching ranges reuses the updated overview");
      assertEqual(usageHistory().length, 14);
      assertEqual(usageHistory()[13].turns, 99, "a wider range must not restore stale usage");

      await loadUsageHistory({ kind: "overview" }, true);
      assertEqual(fetchCalls, 3, "manual refresh hits backend");
    } finally {
      clearUsageHistoryCache();
      hoxiAPI.getDevPulseHistory = origHistory;
    }
  });

  it("clears stale data when switching to an uncovered selection and preserves it on same-selection refresh", async () => {
    const origHistory = hoxiAPI.getDevPulseHistory;
    const now = new Date();
    const fmt = (offset: number) => {
      const d = new Date(now.getFullYear(), now.getMonth(), now.getDate() + offset);
      return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
    };
    const overviewDays = [historyDay({ date: fmt(-1), turns: 2 }), historyDay({ date: fmt(0), turns: 5 })];
    let rejectCustom!: (err: Error) => void;
    let resolveRefresh!: (res: DevPulseHistoryResponse) => void;
    clearUsageHistoryCache();
    try {
      hoxiAPI.getDevPulseHistory = (_days, _dir, window) => {
        if (window?.startDate === "2024-01-01") {
          return new Promise<DevPulseHistoryResponse>((_, reject) => {
            rejectCustom = reject;
          });
        }
        if (window?.refresh) {
          return new Promise<DevPulseHistoryResponse>((resolve) => {
            resolveRefresh = resolve;
          });
        }
        return Promise.resolve({
          days: overviewDays,
          startDate: overviewDays[0].date,
          endDate: overviewDays[1].date,
          timezone: "UTC",
        });
      };

      await loadUsageHistory({ kind: "overview" }, false);
      assertEqual(usageHistory().length, 2);

      // Manual refresh of the same selection keeps existing rows visible while loading.
      const pRefresh = loadUsageHistory({ kind: "overview" }, true);
      assertEqual(usageHistoryLoading(), true);
      assertEqual(usageHistory().length, 2, "same-selection refresh preserves existing data");
      resolveRefresh({
        days: overviewDays,
        startDate: overviewDays[0].date,
        endDate: overviewDays[1].date,
        timezone: "UTC",
      });
      await pRefresh;

      // Switching to an uncovered older custom range clears stale overview data immediately and stays empty on error.
      const pCustom = loadUsageHistory({ kind: "custom", range: { start: "2024-01-01", end: "2024-01-07" } }, false);
      assertEqual(usageHistoryLoading(), true);
      assertEqual(usageHistory().length, 0, "uncovered selection must not render previous selection rows while loading");
      rejectCustom(new Error("boom"));
      await pCustom;
      assertEqual(usageHistory().length, 0, "failed uncovered selection must remain empty");
    } finally {
      clearUsageHistoryCache();
      hoxiAPI.getDevPulseHistory = origHistory;
    }
  });
});
