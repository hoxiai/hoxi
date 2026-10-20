// web/src/__tests__/auto-models.test.ts
import {
  AUTO_PROVIDER_ID,
  buildAutoVirtualProvider,
  clearAutoPoolCooldowns,
  getEffectivePoolCandidates,
  isAutoModel,
  isCandidateCoolingDown,
  markAutoCandidateCooldown,
  resetAutoPoolToDefault,
  resolveAutoModelTarget,
  saveAutoPool,
  setWorkspace,
  stepAutoPoolNext,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("auto virtual models & smart rotation engine", () => {
  it("populates default free/daily/frontier pools and rotates round-robin with 429 cooldown failover", async () => {
    setWorkspace("providers", [
      {
        id: "openrouter",
        name: "OpenRouter",
        models: {
          "qwen/qwen3-coder:free": {
            id: "qwen/qwen3-coder:free",
            name: "Qwen3 Coder (Free)",
            cost: { input: 0, output: 0 },
            capabilities: { toolcall: true, reasoning: true },
          },
          "deepseek/deepseek-chat-v3:free": {
            id: "deepseek/deepseek-chat-v3:free",
            name: "DeepSeek V3 (Free)",
            cost: { input: 0, output: 0 },
            capabilities: { toolcall: true, reasoning: true },
          },
        },
      },
      {
        id: "google",
        name: "Google",
        models: {
          "gemini-2.5-pro": {
            id: "gemini-2.5-pro",
            name: "Gemini 2.5 Pro",
            cost: { input: 1.25, output: 10 },
            capabilities: { toolcall: true, reasoning: true },
          },
        },
      },
    ]);
    setWorkspace("connectedProviders", ["openrouter", "google"]);

    const virt = buildAutoVirtualProvider();
    assertEqual(virt.id, AUTO_PROVIDER_ID);
    assertTrue(Boolean(virt.models.free));
    assertTrue(Boolean(virt.models.daily));
    assertTrue(Boolean(virt.models.frontier));
    assertTrue(isAutoModel({ providerID: AUTO_PROVIDER_ID, modelID: "free" }));

    await resetAutoPoolToDefault("free");
    clearAutoPoolCooldowns("free");

    const freeCands = getEffectivePoolCandidates("free");
    assertEqual(freeCands.length, 2);

    // Customize free pool with deterministic order and round-robin strategy
    await saveAutoPool("free", {
      id: "free",
      preset: true,
      strategy: "round-robin",
      customized: true,
      candidates: [
        { providerID: "openrouter", modelID: "qwen/qwen3-coder:free" },
        { providerID: "openrouter", modelID: "deepseek/deepseek-chat-v3:free" },
      ],
    });

    const first = resolveAutoModelTarget("free", { advance: true });
    const second = resolveAutoModelTarget("free", { advance: true });
    assertDeepEqual(first, { providerID: "openrouter", modelID: "qwen/qwen3-coder:free" });
    assertDeepEqual(second, { providerID: "openrouter", modelID: "deepseek/deepseek-chat-v3:free" });

    // Mark first candidate as 429 rate-limited; pool must skip it and return the healthy one
    markAutoCandidateCooldown("openrouter", "qwen/qwen3-coder:free", "429 Too Many Requests", 429);
    assertTrue(isCandidateCoolingDown("openrouter", "qwen/qwen3-coder:free"));

    const third = resolveAutoModelTarget("free", { advance: true });
    assertDeepEqual(third, { providerID: "openrouter", modelID: "deepseek/deepseek-chat-v3:free" });

    clearAutoPoolCooldowns("free");
    assertEqual(isCandidateCoolingDown("openrouter", "qwen/qwen3-coder:free"), false);

    const stepped = stepAutoPoolNext("free");
    assertTrue(Boolean(stepped));
  });
});
