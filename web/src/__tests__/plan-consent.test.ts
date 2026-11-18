// web/src/__tests__/plan-consent.test.ts
import { extractPlanPreview } from "../components/approval/plan-consent-preview";
import {
  formatToolDisplayName,
  isQuestionTool,
  kiloToolIconName,
} from "../components/timeline/tool-taxonomy";
import { upsertMessageInfo } from "../store/session-stream";
import { setWorkspace, workspace } from "../store/workspace-state";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("plan_consent frontend integration", () => {
  it("classifies plan_consent as an interactive question-family tool with dedicated label and icon", () => {
    assertEqual(isQuestionTool("plan_consent"), true);
    assertEqual(kiloToolIconName("plan_consent"), "checklist");
    assertTrue(formatToolDisplayName("plan_consent").length > 0);
  });

  it("extracts structured plan preview steps, files, and risks cleanly", () => {
    const preview = extractPlanPreview({
      title: "Refactor canonical history",
      summary: "Extract base canonical package",
      steps: ["Create internal/canonical", "Delegate runner helpers"],
      files: ["internal/canonical/canonical.go", "internal/runner/canonical_history.go"],
      risks: ["Keep 800-line limit intact"],
    });
    assertTrue(preview !== null, "expected structured PlanConsentPreview");
    assertEqual(preview?.title, "Refactor canonical history");
    assertDeepEqual(preview?.steps, ["Create internal/canonical", "Delegate runner helpers"]);
    assertEqual(preview?.files?.length, 2);
    assertDeepEqual(preview?.risks, ["Keep 800-line limit intact"]);
  });

  it("returns null for empty or non-plan payloads", () => {
    assertEqual(extractPlanPreview(null), null);
    assertEqual(extractPlanPreview({ title: "", steps: [] }), null);
  });

  it("promotes session AI mode from plan to build when mode_promoted updates message.info", () => {
    const sid = "ses_plan_promo_ui";
    setWorkspace("aiBySession", sid, {
      model: { providerID: "openai", modelID: "gpt-5" },
      agent: "plan",
    });
    upsertMessageInfo({
      id: "msg_a_promo",
      sessionID: sid,
      role: "assistant",
      mode: "build",
      agent: "build",
      time: { created: 1000 },
    });
    assertEqual(workspace.aiBySession[sid]?.agent, "build");
  });
});
