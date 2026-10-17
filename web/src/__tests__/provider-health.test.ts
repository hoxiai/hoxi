// web/src/__tests__/provider-health.test.ts
import {
  deriveHierarchicalRules,
  extractPermissionDiffs,
} from "../components/approval/approval-helpers";
import {
  countDiffLines,
  extractTargetFile,
  processCarriageReturns,
} from "../components/timeline/tool-card-helpers";
import type { PermissionRequest, ToolPart } from "../services/sse-types";
import { computeProviderHealth } from "../store/provider-health";
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
    assertEqual(untested.grade, "—");

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
    assertTrue(healthy.score >= 90, "expected healthy provider score >= 90");
    assertEqual(healthy.grade, "S");
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
    assertEqual(probeOnly.score, 0);

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
    assertTrue(scoredModel.score >= 90, "expected scored model to have score >= 90");

    const unscoredSiblingModel = computeProviderHealth("multi_model_provider", "model-never-run");
    assertEqual(unscoredSiblingModel.status, "untested");
    assertEqual(unscoredSiblingModel.score, 0);
    assertEqual(unscoredSiblingModel.grade, "—");
  });
});

