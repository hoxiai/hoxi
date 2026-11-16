import { reconcile, unwrap } from "solid-js/store";
import type { ProviderInfo } from "../services/api";
import { AUTO_PROVIDER_ID, buildAutoVirtualProvider } from "../store/auto-models";
import { computeSmartDefaultCandidates, isModelFree } from "../store/auto-pool-defaults";
import {
  extractFlattenedModels,
  inferModelEffortCapabilities,
  resolveEffectiveModelEffort,
} from "../store/provider-catalog";
import { setWorkspace, workspace } from "../store/workspace-state";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("Model catalog reasoning and pricing", () => {
  it("offers extreme efforts for every auto pool and preserves them until concrete routing", () => {
    const levels = ["low", "medium", "high", "xhigh", "max"];
    const provider = buildAutoVirtualProvider();
    const models = extractFlattenedModels([provider], new Set([AUTO_PROVIDER_ID]), {});
    for (const model of models) {
      assertDeepEqual(model.efforts, levels);
      assertEqual(model.defaultEffort, "high");
      for (const effort of ["xhigh", "max"] as const) {
        assertEqual(resolveEffectiveModelEffort(model, effort), effort);
      }
    }
    assertDeepEqual(inferModelEffortCapabilities(AUTO_PROVIDER_ID, "custom-high", true).efforts, levels);
  });

  it("honors explicit model capabilities and only clamps after selecting a concrete model", () => {
    const saved = structuredClone(unwrap(workspace.providers));
    try {
      setWorkspace("providers", [{
        id: "imported-codex", name: "Codex", models: {
          "custom-codex": { id: "custom-codex", reasoning: true, efforts: ["low", "high", "xhigh"] },
        },
      }]);
      assertEqual(resolveEffectiveModelEffort({ providerID: "imported-codex", modelID: "custom-codex" }, "max"), "xhigh");
      assertEqual(resolveEffectiveModelEffort({ providerID: "imported-codex", modelID: "custom-codex" }, "xhigh"), "xhigh");
      assertEqual(resolveEffectiveModelEffort({ providerID: "openai", modelID: "gpt-6.1-sol" }, "max"), "max");
      assertDeepEqual(inferModelEffortCapabilities("custom", "claude-opus", true, {
        variants: { low: {}, high: {}, max: {} },
      }).efforts, ["low", "high", "max"]);
    } finally {
      setWorkspace("providers", saved);
    }
  });

  it("does not treat non-Antigravity model suffixes as a three-tier capability limit", () => {
    assertDeepEqual(inferModelEffortCapabilities("subapi", "gemini-web-gemini-4-carbon-thinking-medium", true, {
      variants: { low: {}, medium: {}, high: {}, max: {} },
    }).efforts, ["low", "medium", "high", "max"]);
    assertDeepEqual(inferModelEffortCapabilities("custom", "gpt-6.1-sol-high", true, {
      variants: { low: {}, medium: {}, high: {}, xhigh: {}, max: {} },
    }).efforts, ["low", "medium", "high", "xhigh", "max"]);
  });

  it("respects explicitly disabled runtime tiers when merging configured variants", () => {
    assertDeepEqual(inferModelEffortCapabilities("openai", "gpt-6.1-sol", true, {
      variants: { low: {}, medium: {}, high: {}, xhigh: {}, max: {} },
    }, { variants: { max: { disabled: true } } }).efforts, ["low", "medium", "high", "xhigh"]);
  });

  it("does not label imported zero-price placeholders as free or add them to the free pool", () => {
    const saved = structuredClone(unwrap(workspace));
    const providers: ProviderInfo[] = [{
      id: "imported-codex", name: "Codex", models: {
        "gpt-5-codex": { id: "gpt-5-codex", cost: { input: 0, output: 0 }, reasoning: true },
        "unknown-subscription": { id: "unknown-subscription", cost: { input: 0, output: 0 } },
      },
    }];
    try {
      setWorkspace({ providers, connectedProviders: ["imported-codex"], globalConfig: {} });
      const models = extractFlattenedModels(providers, new Set(["imported-codex"]), {
        provider: { "imported-codex": { models: { "gpt-5-codex": { cost: { input: 0, output: 0 } } } } },
      });
      assertTrue(models.every((model) => !model.free));
      assertTrue((models[0].inputCost ?? 0) > 0, "known Codex prices should replace zero placeholders");
      assertEqual(models[1].inputCost, undefined);
      assertEqual(models[1].outputCost, undefined);
      assertDeepEqual(computeSmartDefaultCandidates("free"), []);
      assertTrue(computeSmartDefaultCandidates("frontier").length > 0);
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("keeps explicit free tiers and built-in free models while respecting free=false", () => {
    assertEqual(isModelFree("openrouter", "qwen:free", { cost: { input: 0, output: 0 } }), true);
    assertEqual(isModelFree("custom", "model", { free: true }), true);
    assertEqual(isModelFree("custom", "model", { multiplier: "0x" }), true);
    assertEqual(isModelFree("opencode", "model", { cost: { input: 0, output: 0 } }), true);
    assertEqual(isModelFree("opencode", "model", {}), true);
    assertEqual(isModelFree("claude-code", "claude-sonnet", { cost: { input: 0, output: 0 } }), false);
    assertEqual(isModelFree("custom", "model:free", { free: false, cost: { input: 0, output: 0 } }), false);
  });
});
