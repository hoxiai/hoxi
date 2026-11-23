// web/src/__tests__/orchestrator-mode.test.ts
import {
  clearSessionOrchestratorOverride,
  getEffectiveOrchestratorPair,
  initializeSessionAI,
  isOrchestratorConfigured,
  isOrchestratorModel,
  isOrchestratorSamePair,
  ORCHESTRATOR_MODEL_ID,
  ORCHESTRATOR_PROVIDER_ID,
  resolveOrchestratorDispatchTarget,
  resolveSessionTargetRuntime,
  setPreferredAgentEngine,
  setWorkspace,
  updateSessionOrchestratorOverride,
} from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("orchestrator dual-model store & dispatch resolution", () => {
  it("resolves per-session overrides, clamps efforts, and rejects disconnected or CLI providers", () => {
    setWorkspace("providers", [
      {
        id: "anthropic",
        name: "Anthropic",
        models: {
          "claude-opus-4-6": {
            id: "claude-opus-4-6",
            name: "Claude Opus 4.6",
            capabilities: { reasoning: true, toolcall: true },
          },
        },
      },
      {
        id: "google",
        name: "Google",
        models: {
          "gemini-2.5-flash": {
            id: "gemini-2.5-flash",
            name: "Gemini 2.5 Flash",
            capabilities: { reasoning: true, toolcall: true },
          },
        },
      },
      {
        id: "claude-code",
        name: "Claude Code CLI",
        source: "runner",
        models: {
          "claude-sonnet-4-5": {
            id: "claude-sonnet-4-5",
            name: "Claude Sonnet 4.5",
            capabilities: { reasoning: true, toolcall: true },
          },
        },
      },
    ]);
    setWorkspace("connectedProviders", ["anthropic", "google", "claude-code"]);
    setWorkspace("hoxiProviders", {
      anthropic: { options: { baseURL: "https://api.anthropic.com" } },
      google: { options: { baseURL: "https://generativelanguage.googleapis.com" } },
    });

    assertTrue(
      isOrchestratorModel({ providerID: ORCHESTRATOR_PROVIDER_ID, modelID: ORCHESTRATOR_MODEL_ID })
    );
    assertEqual(isOrchestratorModel({ providerID: "anthropic", modelID: "claude-opus-4-6" }), false);

    const sid = "ses_orch_test_1";
    clearSessionOrchestratorOverride(sid);

    // Unconfigured pair must throw instead of silently falling back.
    let threwUnconfigured = false;
    try {
      resolveOrchestratorDispatchTarget(sid);
    } catch {
      threwUnconfigured = true;
    }
    assertTrue(threwUnconfigured);

    // Configure per-session override with valid native API models.
    updateSessionOrchestratorOverride(
      {
        orchestratorModel: "anthropic/claude-opus-4-6",
        orchestratorEffort: "xhigh",
        executorModel: "google/gemini-2.5-flash",
        executorEffort: "medium",
      },
      sid
    );
    assertTrue(isOrchestratorConfigured(sid));
    assertEqual(isOrchestratorSamePair(sid), false);
    const pair = getEffectiveOrchestratorPair(sid);
    assertTrue(pair.hasOverride);
    assertEqual(pair.orchestratorModel, "anthropic/claude-opus-4-6");
    assertEqual(pair.executorModel, "google/gemini-2.5-flash");

    const resolved = resolveOrchestratorDispatchTarget(sid);
    assertEqual(resolved.model.providerID, "anthropic");
    assertEqual(resolved.model.modelID, "claude-opus-4-6");
    assertEqual(resolved.effort, "xhigh");
    assertEqual(resolved.orchestrator.executorProviderID, "google");
    assertEqual(resolved.orchestrator.executorModelID, "gemini-2.5-flash");
    assertEqual(resolved.orchestrator.executorEffort, "medium");

    // External CLI runner provider must be rejected (native API models only).
    updateSessionOrchestratorOverride(
      { executorModel: "claude-code/claude-sonnet-4-5" },
      sid
    );
    let threwCLI = false;
    try {
      resolveOrchestratorDispatchTarget(sid);
    } catch {
      threwCLI = true;
    }
    assertTrue(threwCLI);

    // Identical model + effort triggers same-pair warning flag.
    updateSessionOrchestratorOverride(
      {
        orchestratorModel: "anthropic/claude-opus-4-6",
        orchestratorEffort: "high",
        executorModel: "anthropic/claude-opus-4-6",
        executorEffort: "high",
      },
      sid
    );
    assertTrue(isOrchestratorSamePair(sid));

    clearSessionOrchestratorOverride(sid);
    assertEqual(getEffectiveOrchestratorPair(sid).hasOverride, false);

    // resolveSessionTargetRuntime always forces native for hoxi-orchestrator even if opencode is preferred.
    setPreferredAgentEngine("opencode");
    assertEqual(resolveSessionTargetRuntime(sid, ORCHESTRATOR_PROVIDER_ID), "native");
    setPreferredAgentEngine("native");

    // initializeSessionAI clones orchestratorOverride so sessions stay isolated from draft/defaultAI.
    updateSessionOrchestratorOverride(
      { orchestratorModel: "anthropic/claude-opus-4-6", orchestratorEffort: "high" },
      null
    );
    initializeSessionAI("ses_orch_clone_a");
    clearSessionOrchestratorOverride(null);
    assertEqual(
      getEffectiveOrchestratorPair("ses_orch_clone_a").orchestratorModel,
      "anthropic/claude-opus-4-6"
    );
    assertEqual(getEffectiveOrchestratorPair("ses_orch_clone_a").orchestratorEffort, "high");
  });
});
