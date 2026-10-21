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
import { computeProviderHealth } from "../store/provider-health";
import { emptyStats, pushSample } from "../store/provider-telemetry";
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
    assertEqual(untested.qualityScore, null);

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
    assertEqual(healthy.qualityScore, null);
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
      auditTotalCount: 4,
      auditPassCount: 1,
      auditScores: [85, 52, 48, 55],
      ttftSamplesMs: [380, 410],
      tpsSamples: [70, 80],
      recentIncidents: [],
      updatedAt: Date.now(),
    });

    const downgraded = computeProviderHealth("relay_downgraded");
    assertEqual(downgraded.codePassRate, 25);
    assertEqual(downgraded.avgAuditScore, 60);
    assertEqual(downgraded.qualityScore, 60);
    assertEqual(downgraded.stabilityScore, 100);
    assertTrue(
      downgraded.riskTags.includes("low_pass_rate"),
      "expected low_pass_rate risk tag when codePassRate < 65%"
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
    assertEqual(probeOnly.qualityScore, null);

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
    assertEqual(scoredModel.qualityScore, null);

    const unscoredSiblingModel = computeProviderHealth("multi_model_provider", "model-never-run");
    assertEqual(unscoredSiblingModel.status, "untested");
    assertEqual(unscoredSiblingModel.stabilityScore, null);
    assertEqual(unscoredSiblingModel.qualityScore, null);
  });

  it("keeps request failures, retries and speed separate from result quality", () => {
    setWorkspace("providerTelemetry", "independent_scores", {
      ...emptyStats("independent_scores"),
      totalTurns: 10,
      successTurns: 5,
      errorTurns: 5,
      timeoutTurns: 8,
      retryCount: 9,
      toolCallsTotal: 5,
      toolCallsFailed: 5,
      hallucinationFlags: 3,
      ttftSamplesMs: [25000],
      tpsSamples: [5],
      auditTotalCount: 2,
      auditPassCount: 2,
      auditScores: [90, 100],
    });
    const health = computeProviderHealth("independent_scores");
    assertEqual(health.stabilityScore, 50);
    assertEqual(health.qualityScore, 95);
    assertEqual(health.qualitySampleCount, 2);
  });

  it("keeps failure-only quality unrated and accepts genuine zero quality", () => {
    setWorkspace("providerTelemetry", "failure_only", {
      ...emptyStats("failure_only"), totalTurns: 3, errorTurns: 3,
    });
    assertEqual(computeProviderHealth("failure_only").stabilityScore, 0);
    assertEqual(computeProviderHealth("failure_only").qualityScore, null);
    setWorkspace("providerTelemetry", "audit_only", {
      ...emptyStats("audit_only"), auditTotalCount: 1, auditScores: [0], lastAuditScore: 0,
    });
    const health = computeProviderHealth("audit_only");
    assertEqual(health.stabilityScore, null);
    assertEqual(health.qualityScore, 0);
    assertEqual(health.qualitySampleCount, 1);
    assertEqual(health.status, "degraded");
    assertDeepEqual(pushSample([90], 0, true), [90, 0]);
    assertDeepEqual(pushSample([90], 0), [90]);
  });
});
