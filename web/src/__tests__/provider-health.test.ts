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
  });
});
