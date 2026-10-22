// web/src/__tests__/auto-breaker.test.ts
import type { CandidateBreakerSnapshot } from "../services/api";
import {
  applyProviderBreakers,
  isCandidateBreakerOpen,
  isCandidateCoolingDown,
  reportAutoCandidateOutcome,
  resolveAutoModelTarget,
  resetAutoPoolToDefault,
  saveAutoPool,
  setWorkspace,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

function snap(providerID: string, modelID: string, score: number): CandidateBreakerSnapshot {
  return {
    providerID,
    modelID,
    phase: "closed",
    open: false,
    score,
    consecutiveFailures: 0,
    updatedAt: Date.now(),
  };
}

describe("shared candidate circuit breaker in auto-pool selection", () => {
  it("skips a candidate whose breaker is open and reports failures optimistically", async () => {
    setWorkspace("providers", [
      { id: "alpha", name: "Alpha", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
      { id: "beta", name: "Beta", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
    ]);
    setWorkspace("connectedProviders", ["alpha", "beta"]);
    applyProviderBreakers({});

    await saveAutoPool("free", {
      id: "free",
      preset: true,
      strategy: "round-robin",
      customized: true,
      candidates: [
        { providerID: "alpha", modelID: "m" },
        { providerID: "beta", modelID: "m" },
      ],
    });

    reportAutoCandidateOutcome({ providerID: "alpha", modelID: "m", ok: false, message: "429", statusCode: 429 });
    assertTrue(isCandidateBreakerOpen("alpha", "m"));
    assertTrue(isCandidateCoolingDown("alpha", "m"));

    const target = resolveAutoModelTarget("free", { advance: true });
    assertDeepEqual(target, { providerID: "beta", modelID: "m" });

    await resetAutoPoolToDefault("free");
    applyProviderBreakers({});
  });

  it("ranks the higher-scoring candidate first in a smart-health pool", async () => {
    setWorkspace("providers", [
      { id: "alpha", name: "Alpha", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
      { id: "beta", name: "Beta", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
    ]);
    setWorkspace("connectedProviders", ["alpha", "beta"]);

    await saveAutoPool("frontier", {
      id: "frontier",
      preset: true,
      strategy: "smart-health",
      customized: true,
      candidates: [
        { providerID: "alpha", modelID: "m" },
        { providerID: "beta", modelID: "m" },
      ],
    });

    applyProviderBreakers({
      "alpha/m": snap("alpha", "m", 0.92),
      "beta/m": snap("beta", "m", 0.18),
    });

    const target = resolveAutoModelTarget("frontier", { advance: true });
    assertDeepEqual(target, { providerID: "alpha", modelID: "m" });

    await resetAutoPoolToDefault("frontier");
    applyProviderBreakers({});
    assertEqual(isCandidateBreakerOpen("alpha", "m"), false);
  });
});
