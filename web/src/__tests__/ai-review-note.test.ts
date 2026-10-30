import type { PermissionRequest } from "../services/sse-types";
import { aiReviewNote, aiReviewNoteSuggestsModelChange, aiReviewNoteText } from "../store/approval-mode";
import { t } from "../i18n";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

const withNote = (note: unknown) =>
  ({ id: "per_ai", sessionID: "ses_ai", permission: "bash", patterns: ["x"], metadata: { hoxiAIReview: note } }) as unknown as PermissionRequest;

describe("AI review note on approval cards", () => {
  it("reads only the outcomes Go annotates", () => {
    assertEqual(aiReviewNote(withNote(undefined)), null);
    assertEqual(aiReviewNote(withNote({ status: "allow" })), null);
    assertEqual(aiReviewNote(withNote("unavailable")), null);
    assertDeepEqual(aiReviewNote(withNote({ status: "unavailable", failure: "timeout", timeoutSeconds: 10, detail: " " })), {
      status: "unavailable",
      failure: "timeout",
      detail: undefined,
      reason: undefined,
      timeoutSeconds: 10,
    });
  });

  it("explains each failure and the model's own answer", () => {
    const text = (note: unknown) => aiReviewNoteText(aiReviewNote(withNote(note))!);
    assertEqual(text({ status: "ask", reason: "writes files" }), t("approval.aiReviewNoteAsk", { reason: "writes files" }));
    assertEqual(text({ status: "ask" }), t("approval.aiReviewNoteAskNoReason"));
    assertEqual(text({ status: "unavailable", failure: "empty" }), t("approval.aiReviewNoteEmpty"));
    assertEqual(text({ status: "unavailable", failure: "timeout", timeoutSeconds: 10 }), t("approval.aiReviewNoteTimeout", { seconds: 10 }));
    assertEqual(text({ status: "unavailable", failure: "invalid" }), t("approval.aiReviewNoteInvalid"));
    assertEqual(text({ status: "unavailable", failure: "unready" }), t("approval.aiReviewNoteUnready"));
    assertEqual(text({ status: "unavailable", failure: "error", detail: "HTTP 503" }), t("approval.aiReviewNoteError", { detail: "HTTP 503" }));
    assertEqual(text({ status: "unavailable", failure: "error" }), t("approval.aiReviewNoteErrorNoDetail"));
  });

  it("offers a model change only when the model is the likely cause", () => {
    const suggests = (note: unknown) => aiReviewNoteSuggestsModelChange(aiReviewNote(withNote(note))!);
    assertEqual(suggests({ status: "unavailable", failure: "empty" }), true);
    assertEqual(suggests({ status: "unavailable", failure: "timeout" }), true);
    assertEqual(suggests({ status: "unavailable", failure: "error" }), false);
    assertEqual(suggests({ status: "ask", reason: "writes files" }), false);
  });
});
