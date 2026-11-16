// web/src/__tests__/provider-health.test.ts
import {
  deriveHierarchicalRules,
  exactAllowRules,
  extractPermissionDiffs,
  isExactRule,
  isPersistableRule,
  rulesForReply,
} from "../components/approval/approval-helpers";
import {
  countDiffLines,
  extractTargetFile,
  processCarriageReturns,
} from "../components/timeline/tool-card-helpers";
import type { PermissionRequest, ToolPart } from "../services/sse-types";
import { getCandidateBreaker } from "../store/auto-breaker";
import { computeProviderHealth } from "../store/provider-health";
import {
  emptyStats,
  isBalanceError,
  isContextOverflowError,
  isModelUnavailableError,
  pushSample,
  recordAssistantTurnUpdated,
} from "../store/provider-telemetry";
import { setWorkspace } from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("approval & toolcard pure helpers", () => {
  it("extracts structured diffs and hierarchical command rules from PermissionRequest", () => {
    const req: PermissionRequest = {
      id: "perm_1",
      sessionID: "ses_1",
      permission: "bash",
      patterns: ["git push --force origin main"],
      always: [],
      metadata: {
        filediff: {
          file: "src/main.ts",
          before: "const a = 1;\n",
          after: "const a = 2;\n",
          additions: 1,
          deletions: 1,
        },
      },
    };

    const diffs = extractPermissionDiffs(req);
    assertEqual(diffs.length, 1);
    assertEqual(diffs[0].file, "src/main.ts");
    assertEqual(diffs[0].additions, 1);

    const rules = deriveHierarchicalRules(req, "git push --force origin main");
    assertDeepEqual(rules, ["git push *", "git *"]);
  });

  it("only persists exact permission rules and never wildcard prefixes", () => {
    assertEqual(isExactRule("git status"), true);
    assertEqual(isExactRule("git *"), false);
    assertEqual(isExactRule("src/*.ts"), false);
    assertEqual(isExactRule("  "), false);

    const shell: PermissionRequest = {
      id: "perm_shell",
      sessionID: "ses_1",
      permission: "bash",
      patterns: ["git status"],
      always: ["git *"],
      metadata: {},
    };
    assertDeepEqual(exactAllowRules(shell), { "git status": "allow" });

    // The broad "always" patterns OpenCode suggests must never be persisted,
    // otherwise `git push --force` would skip the approval gate.
    const force: PermissionRequest = {
      ...shell,
      patterns: ["git push --force origin main"],
      always: ["git push *", "git *"],
    };
    assertDeepEqual(exactAllowRules(force), { "git push --force origin main": "allow" });
    const install: PermissionRequest = {
      ...shell,
      patterns: ["npm install lodash"],
      always: ["npm *", "npm install *"],
    };
    assertDeepEqual(exactAllowRules(install), { "npm install lodash": "allow" });
    assertDeepEqual(
      exactAllowRules({ ...shell, patterns: ["git *", "*"] }),
      {},
      "wildcards must not become global allow rules"
    );

    const fileEdit: PermissionRequest = {
      ...shell,
      permission: "edit",
      patterns: ["src/main.ts"],
      always: ["*"],
    };
    assertDeepEqual(
      exactAllowRules(fileEdit),
      {},
      "relative file paths must not leak into the global config"
    );
    assertEqual(isPersistableRule("*", "deny"), false);
    assertEqual(isPersistableRule("git *", "deny"), true);
    assertEqual(isPersistableRule("git *", "allow"), false);
  });

  it("persists exact rules on always, deny-only rules on reject, and nothing for destructive requests", () => {
    const req: PermissionRequest = {
      id: "perm_reply",
      sessionID: "ses_1",
      permission: "bash",
      patterns: ["npm install lodash"],
      always: ["npm *"],
      metadata: {},
    };
    assertDeepEqual(rulesForReply(req, "once", false, {}), {});
    assertDeepEqual(rulesForReply(req, "always", false, {}), {
      "npm install lodash": "allow",
    });
    assertDeepEqual(
      rulesForReply(req, "always", false, { "npm install lodash": "deny" }),
      {},
      "an explicit deny must win over the implicit allow"
    );
    assertDeepEqual(
      rulesForReply(req, "reject", false, { "npm install lodash": "allow", "npm *": "deny" }),
      { "npm *": "deny" },
      "only deny picks persist on reject"
    );
    assertDeepEqual(rulesForReply(req, "always", true, {}), {});
    assertDeepEqual(rulesForReply(req, "always", true, { "npm *": "deny" }), {});
    assertDeepEqual(rulesForReply(req, "always", false, {}, { "npm *": "allow" }), {
      "npm *": "allow",
    });
  });

  it("processes terminal carriage returns and counts diff lines accurately", () => {
    assertEqual(processCarriageReturns("progress 10%\rprogress 100%\ndone"), "progress 100%\ndone");
    assertDeepEqual(
      countDiffLines("@@ -1,2 +1,2 @@\n-old line\n+new line 1\n+new line 2"),
      { additions: 2, deletions: 1 }
    );
    const toolPart: ToolPart = {
      id: "part_1",
      sessionID: "ses_1",
      messageID: "msg_1",
      type: "tool",
      callID: "call_1",
      tool: "edit",
      state: {
        status: "completed",
        input: { filePath: "/Users/test/app.tsx" },
        output: "",
        title: "Edit app.tsx",
        metadata: {},
        time: { start: 100, end: 200 },
      },
    };
    assertEqual(extractTargetFile(toolPart), "/Users/test/app.tsx");
  });
});

describe("provider telemetry health scoring", () => {
  it("grades untested, healthy, and degraded providers accurately", () => {
    const untested = computeProviderHealth("non_existent_provider");
    assertEqual(untested.status, "untested");
    assertEqual(untested.stabilityScore, null);

    setWorkspace("providerTelemetry", "openai_test", {
      providerID: "openai_test",
      totalTurns: 10,
      successTurns: 10,
      errorTurns: 0,
      timeoutTurns: 0,
      retryCount: 0,
      revertCount: 0,
      hallucinationFlags: 0,
      toolCallsTotal: 8,
      toolCallsFailed: 0,
      cacheHitTurns: 5,
      ttftSamplesMs: [420, 480],
      tpsSamples: [55, 65],
      recentIncidents: [],
      updatedAt: Date.now(),
    });

    const healthy = computeProviderHealth("openai_test");
    assertEqual(healthy.status, "healthy");
    assertEqual(healthy.stabilityScore, 100);
    assertEqual(healthy.avgTtftMs, 450);

    setWorkspace("providerTelemetry", "relay_downgraded", {
      providerID: "relay_downgraded",
      totalTurns: 6,
      successTurns: 6,
      errorTurns: 0,
      timeoutTurns: 0,
      retryCount: 0,
      revertCount: 0,
      hallucinationFlags: 2,
      toolCallsTotal: 6,
      toolCallsFailed: 0,
      cacheHitTurns: 0,
      reasoningTurns: 0,
      ttftSamplesMs: [380, 410],
      tpsSamples: [70, 80],
      recentIncidents: [],
      updatedAt: Date.now(),
    });

    const downgraded = computeProviderHealth("relay_downgraded");
    assertEqual(downgraded.stabilityScore, 100);
    assertTrue(
      downgraded.riskTags.includes("high_hallucination"),
      "expected high_hallucination risk tag when hallucination ratio >= 15%"
    );
    assertEqual(downgraded.status, "degraded");
  });

  it("isolates per-model scores and hides scores for un-run or probe-only models", () => {
    setWorkspace("providerTelemetry", "probe_only_provider", {
      providerID: "probe_only_provider",
      totalTurns: 0,
      successTurns: 0,
      errorTurns: 0,
      timeoutTurns: 0,
      retryCount: 0,
      revertCount: 0,
      hallucinationFlags: 0,
      toolCallsTotal: 0,
      toolCallsFailed: 0,
      cacheHitTurns: 0,
      ttftSamplesMs: [],
      tpsSamples: [],
      lastProbeOk: true,
      lastProbeLatencyMs: 220,
      recentIncidents: [],
      updatedAt: Date.now(),
    });

    const probeOnly = computeProviderHealth("probe_only_provider");
    assertEqual(probeOnly.status, "untested");
    assertEqual(probeOnly.stabilityScore, null);

    setWorkspace("providerTelemetry", "multi_model_provider", {
      providerID: "multi_model_provider",
      totalTurns: 5,
      successTurns: 5,
      errorTurns: 0,
      timeoutTurns: 0,
      retryCount: 0,
      revertCount: 0,
      hallucinationFlags: 0,
      toolCallsTotal: 4,
      toolCallsFailed: 0,
      cacheHitTurns: 2,
      ttftSamplesMs: [350],
      tpsSamples: [60],
      models: {
        "model-scored": {
          modelID: "model-scored",
          totalTurns: 5,
          successTurns: 5,
          errorTurns: 0,
          timeoutTurns: 0,
          retryCount: 0,
          revertCount: 0,
          hallucinationFlags: 0,
          toolCallsTotal: 4,
          toolCallsFailed: 0,
          cacheHitTurns: 2,
          ttftSamplesMs: [350],
          tpsSamples: [60],
          updatedAt: Date.now(),
        },
      },
      recentIncidents: [],
      updatedAt: Date.now(),
    });

    const scoredModel = computeProviderHealth("multi_model_provider", "model-scored");
    assertEqual(scoredModel.status, "healthy");
    assertEqual(scoredModel.stabilityScore, 100);

    const unscoredSiblingModel = computeProviderHealth("multi_model_provider", "model-never-run");
    assertEqual(unscoredSiblingModel.status, "untested");
    assertEqual(unscoredSiblingModel.stabilityScore, null);
  });

  it("grades failure-only providers as degraded with zero stability", () => {
    setWorkspace("providerTelemetry", "failure_only", {
      ...emptyStats("failure_only"), totalTurns: 3, errorTurns: 3,
    });
    const health = computeProviderHealth("failure_only");
    assertEqual(health.stabilityScore, 0);
    assertEqual(health.status, "degraded");
    assertDeepEqual(pushSample([90], 0, true), [90, 0]);
    assertDeepEqual(pushSample([90], 0), [90]);
  });

  it("does not permanently flag high-volume providers when lifetime timeout or hallucination ratio is low", () => {
    setWorkspace("providerTelemetry", "high_volume_subapi", {
      ...emptyStats("high_volume_subapi"),
      totalTurns: 40_000,
      successTurns: 39_920,
      errorTurns: 80,
      timeoutTurns: 6,
      hallucinationFlags: 70,
      toolCallsTotal: 12_000,
      toolCallsFailed: 10,
      ttftSamplesMs: [420, 480],
      tpsSamples: [60, 75],
    });
    const health = computeProviderHealth("high_volume_subapi");
    assertEqual(health.stabilityScore, 100);
    assertDeepEqual(health.riskTags, []);
    assertEqual(health.status, "healthy");
  });

  it("does not flag high_hallucination when tool success rate is healthy and revertCount is zero", () => {
    setWorkspace("providerTelemetry", "agy_agentic", {
      ...emptyStats("agy_agentic"),
      totalTurns: 50,
      successTurns: 49,
      errorTurns: 1,
      revertCount: 0,
      hallucinationFlags: 10,
      toolCallsTotal: 400,
      toolCallsFailed: 8,
    });
    const health = computeProviderHealth("agy_agentic");
    assertEqual(health.stabilityScore, 98);
    assertDeepEqual(health.riskTags, []);
    assertEqual(health.status, "healthy");
  });
});

describe("context overflow detection", () => {
  const info = (error?: { name: string; message?: string; statusCode?: number }) =>
    ({
      id: "msg_1",
      sessionID: "ses_1",
      role: "assistant",
      time: { created: 1 },
      error: error && {
        name: error.name,
        data: { message: error.message, statusCode: error.statusCode },
      },
    } as any);

  it("recognizes context-window failures by error name and by message", () => {
    assertEqual(isContextOverflowError(info({ name: "ContextOverflowError" })), true);
    assertEqual(isContextOverflowError(info({ name: "MessageOutputLengthError" })), true);
    assertEqual(
      isContextOverflowError(
        info({ name: "APIError", message: "This model's maximum context length is 128000 tokens" })
      ),
      true
    );
    assertEqual(
      isContextOverflowError(info({ name: "APIError", message: "prompt is too long: 200001 tokens" })),
      true
    );
    assertEqual(isContextOverflowError(info({ name: "APIError", message: "Insufficient balance" })), false);
    assertEqual(isContextOverflowError(info({ name: "APIError", statusCode: 429 })), false);
    assertEqual(isContextOverflowError(info(undefined)), false);
    assertEqual(isContextOverflowError(null), false);
  });

  it("keeps aborts and watchdog stalls out of the overflow path", () => {
    assertEqual(isContextOverflowError(info({ name: "MessageAbortedError", message: "Aborted" })), false);
    assertEqual(isContextOverflowError(info({ name: "RunnerStalledError" })), false);
  });
});

describe("model unavailable detection", () => {
  const info = (error?: { name: string; message?: string; statusCode?: number }) =>
    ({
      id: "msg_1",
      sessionID: "ses_1",
      role: "assistant",
      time: { created: 1 },
      error: error && {
        name: error.name,
        data: { message: error.message, statusCode: error.statusCode },
      },
    } as any);

  it("recognizes a stale engine model catalog", () => {
    assertEqual(isModelUnavailableError(info({ name: "ProviderModelNotFoundError" })), true);
    assertEqual(
      isModelUnavailableError(
        info({ name: "UnknownError", message: "Model not found: subapi/QoderCN-DeepSeek-Flash." })
      ),
      true
    );
    assertEqual(
      isModelUnavailableError(
        info({ name: "OpenCodeError", message: 'model "subapi/x" is not loaded in the running engine' })
      ),
      true
    );
  });

  it("does not misclassify unrelated failures", () => {
    assertEqual(isModelUnavailableError(info({ name: "APIError", message: "Insufficient balance" })), false);
    assertEqual(isModelUnavailableError(info({ name: "MessageAbortedError", message: "Aborted" })), false);
    assertEqual(isModelUnavailableError(info(undefined)), false);
    assertEqual(isModelUnavailableError(null), false);
  });
});

describe("balance exhausted detection", () => {
  const info = (error?: { name: string; message?: string; statusCode?: number }, providerID = "") =>
    ({
      id: `msg_${Math.random().toString(36).slice(2)}`,
      sessionID: "ses_balance",
      role: "assistant",
      providerID,
      modelID: "",
      time: { created: 1 },
      error: error && {
        name: error.name,
        data: { message: error.message, statusCode: error.statusCode },
      },
    } as any);

  it("recognizes 402 and out-of-balance wording", () => {
    assertEqual(isBalanceError(info({ name: "APIError", message: "Insufficient balance", statusCode: 402 })), true);
    assertEqual(isBalanceError(info({ name: "APIError", statusCode: 402 })), true);
    assertEqual(
      isBalanceError(info({ name: "APIError", message: "You exceeded your current quota", statusCode: 429 })),
      true
    );
    assertEqual(isBalanceError(info({ name: "APIError", message: "\u8d26\u6237\u4f59\u989d\u4e0d\u8db3" })), true);
    assertEqual(isBalanceError(info({ name: "APIError", message: "Your credit balance is too low" })), true);
    assertEqual(
      isBalanceError(
        info({
          name: "RunnerError",
          message: "You've reached your daily usage limit for Chat. Come back tomorrow to continue working with me.",
        })
      ),
      true
    );
  });

  it("does not treat rate limits or other failures as balance", () => {
    assertEqual(isBalanceError(info({ name: "APIError", message: "Too many requests", statusCode: 429 })), false);
    assertEqual(isBalanceError(info({ name: "APIError", message: "Bad gateway", statusCode: 502 })), false);
    assertEqual(isBalanceError(info({ name: "ContextOverflowError" })), false);
    assertEqual(isBalanceError(info(undefined)), false);
    assertEqual(isBalanceError(null), false);
  });

  it("keeps balance failures out of provider stability", () => {
    setWorkspace("providerTelemetry", "balance_test", undefined as any);
    const turn = info({ name: "APIError", message: "Insufficient balance", statusCode: 402 }, "balance_test");
    turn.modelID = "m1";
    recordAssistantTurnUpdated(turn);
    assertEqual(computeProviderHealth("balance_test").status, "untested");
    assertEqual(getCandidateBreaker("balance_test", "m1")?.lastClass, "quota");

    recordAssistantTurnUpdated(info({ name: "APIError", message: "Bad gateway", statusCode: 502 }, "balance_test"));
    assertEqual(computeProviderHealth("balance_test").stabilityScore, 0);
  });
});
