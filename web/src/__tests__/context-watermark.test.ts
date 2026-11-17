import type { MessageTurn } from "../store/workspace-state";
import { computeSessionContextWatermark } from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";

function makeAssistantTurn(
  id: string,
  tokens: NonNullable<MessageTurn["info"]["tokens"]>,
  reasoningPartCount = 1,
  cost = 0
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
