import type { MessageTurn } from "../store/workspace-state";
import { computeSessionContextWatermark } from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";

function makeAssistantTurn(
  id: string,
  tokens: NonNullable<MessageTurn["info"]["tokens"]>,
  reasoningPartCount = 1,
  cost = 0,
  model?: { providerID?: string; modelID?: string }
): MessageTurn {
  const parts: MessageTurn["parts"] = [];
  for (let i = 0; i < reasoningPartCount; i++) {
    parts.push({
      id: `rsn_${id}_${i}`,
      sessionID: "ses_ctx",
      messageID: id,
      type: "reasoning",
      text: "step thought",
      time: { start: 1, end: 2 },
    });
  }
  return {
    info: {
      id,
      sessionID: "ses_ctx",
      role: "assistant",
      providerID: model?.providerID,
      modelID: model?.modelID,
      time: { created: 1, completed: 2 },
      cost,
      tokens,
    },
    parts,
  };
}

describe("Session context watermark calculation", () => {
  it("uses single-step tokens.context instead of multi-step cumulative billing buckets", () => {
    const turns: MessageTurn[] = [
      makeAssistantTurn(
        "msg_multi",
        {
          total: 32_000,
          context: 32_000,
          input: 64_000,
          output: 8_000,
          reasoning: 4_000,
          cache: { read: 320_000, write: 12_000 },
        },
        12,
        0.42
      ),
    ];

    const stats = computeSessionContextWatermark(turns, 200_000);
    assertEqual(stats.used, 32_000);
    assertEqual(stats.percent, 16);
    assertEqual(stats.overLimit, false);
    // Cumulative billing breakdown remains intact for turn telemetry.
    assertEqual(stats.input, 64_000);
    assertEqual(stats.cacheRead, 320_000);
    assertEqual(stats.totalCost, 0.42);
  });

  it("normalizes legacy persisted multi-step native turns lacking tokens.context", () => {
    const turns: MessageTurn[] = [
      makeAssistantTurn(
        "msg_legacy",
        {
          total: 400_000,
          input: 80_000,
          output: 12_000,
          reasoning: 8_000,
          cache: { read: 300_000, write: 0 },
        },
        10,
        0.25
      ),
    ];

    const stats = computeSessionContextWatermark(turns, 200_000);
    assertEqual(stats.used, 40_000);
    assertEqual(stats.percent, 20);
    assertEqual(stats.overLimit, false);
  });

  it("preserves authoritative single-step tokens.total on Claude/OpenCode runtimes", () => {
    const turns: MessageTurn[] = [
      makeAssistantTurn(
        "msg_claude",
        {
          total: 46_000,
          input: 15_000,
          output: 3_000,
          reasoning: 2_000,
          cache: { read: 180_000, write: 10_000 },
        },
        5,
        0.18
      ),
    ];

    const stats = computeSessionContextWatermark(turns, 200_000);
    assertEqual(stats.used, 46_000);
    assertEqual(stats.percent, 23);
    assertEqual(stats.overLimit, false);
  });
});

describe("cache feedback on the latest metered turn", () => {
  it("uses disjoint input buckets, distinguishes zero from unavailable, and surfaces partial zero-omitting telemetry", () => {
    const drop = { cause: "local_prefix_unchanged", before: 3000, after: 0 };
    const measured = { input: 100, output: 800, reasoning: 10, cache: { read: 200, write: 100 }, reportedCacheInput: 400 };
    const turn = makeAssistantTurn("first", { ...measured, cacheDrop: drop }, 1, 0, {
      providerID: "subapi",
      modelID: "openai-codex-gpt-6.2-sol",
    });
    const firstStats = computeSessionContextWatermark([turn], 8000);
    assertEqual(firstStats.cacheHitPercent, 50);
    assertEqual(firstStats.cacheHitPartial, false);

    const cold = makeAssistantTurn("next", { ...measured, cache: { read: 0, write: 0 }, reportedCacheInput: 100 }, 1, 0, {
      providerID: "subapi",
      modelID: "openai-codex-gpt-6.2-sol",
    });
    const next = computeSessionContextWatermark([turn, cold], 8000);
    assertEqual(next.cacheHitPercent, 0);
    assertEqual(next.cacheHitPartial, false);
    assertEqual(next.lastCacheDrop?.cause, drop.cause);

    // Partial coverage (e.g. omitempty/proto3 gateways that omit cached_tokens on misses)
    // and non-native turns with cache reads remain visible as partial estimates instead of "Unknown".
    for (const reportedCacheInput of [undefined, 200]) {
      const partial = makeAssistantTurn("partial", { ...measured, reportedCacheInput });
      const stats = computeSessionContextWatermark([partial], 8000);
      assertEqual(stats.cacheHitPercent, 50);
      assertEqual(stats.cacheHitPartial, true);
    }

    // Same model omitting cache telemetry on a follow-up all-miss turn reports ~0% instead of hiding it.
    const omittedMissSameModel = makeAssistantTurn(
      "miss_same",
      { input: 90_000, output: 400, reasoning: 0, cache: { read: 0, write: 0 }, reportedCacheInput: 0 },
      1,
      0,
      { providerID: "subapi", modelID: "openai-codex-gpt-6.2-sol" }
    );
    const sameModelStats = computeSessionContextWatermark([turn, omittedMissSameModel], 200_000);
    assertEqual(sameModelStats.cacheHitPercent, 0);
    assertEqual(sameModelStats.cacheHitPartial, true);

    // Switching to a different model that never reports cache telemetry stays Unknown (null).
    const otherModel = makeAssistantTurn(
      "other_model",
      { input: 10_000, output: 200, reasoning: 0, cache: { read: 0, write: 0 } },
      1,
      0,
      { providerID: "ollama", modelID: "qwen3" }
    );
    const otherModelStats = computeSessionContextWatermark([turn, otherModel], 200_000);
    assertEqual(otherModelStats.cacheHitPercent, null);
    assertEqual(otherModelStats.cacheHitPartial, false);

    // Corrupted reportedCacheInput > inputPool is rejected as Unknown.
    const corrupted = makeAssistantTurn("corrupt", { ...measured, reportedCacheInput: 9999 });
    const corruptStats = computeSessionContextWatermark([corrupted], 8000);
    assertEqual(corruptStats.cacheHitPercent, null);
    assertEqual(corruptStats.cacheHitPartial, false);
  });
});
