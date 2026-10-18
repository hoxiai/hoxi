import {
  createSessionLedger,
  ingestMessages,
  ledgerHasEvidence,
  noteDiffs,
  noteTodos,
  renderEightDimMemory,
} from "../../../.opencode/plugins/hoxi-memory.js";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("8-dim session memory", () => {
  it("writes observed goals, edits, errors, and todos instead of empty headings", () => {
    const ledger = createSessionLedger();
    assertEqual(ledgerHasEvidence(ledger), false);
    ingestMessages(ledger, [
      {
        info: { id: "msg_user", role: "user" },
        parts: [{ type: "text", messageID: "msg_user", text: "Fix the apply path. Never swallow git errors." }],
      },
      {
        info: { id: "msg_assistant", role: "assistant" },
        parts: [
          {
            type: "tool",
            tool: "edit",
            state: { status: "completed", input: { filePath: "internal/worktree/manager.go" } },
          },
          {
            type: "tool",
            tool: "bash",
            state: { status: "error", input: { command: "go test" }, error: "snapshot commit failed" },
          },
          { type: "text", messageID: "msg_assistant", text: "Apply now stops when the snapshot commit fails." },
        ],
      },
    ]);
    noteDiffs(ledger, [{ file: "internal/worktree/manager.go", additions: 4, deletions: 1 }]);
    noteTodos(ledger, [{ content: "Cover locked worktree removal", status: "pending" }]);

    const markdown = renderEightDimMemory("ses_test", ledger, "2026-10-18T00:00:00.000Z");
    assertTrue(ledgerHasEvidence(ledger));
    assertTrue(markdown.includes("Fix the apply path"));
    assertTrue(markdown.includes("Never swallow git errors"));
    assertTrue(markdown.includes("internal/worktree/manager.go"));
    assertTrue(markdown.includes("snapshot commit failed"));
    assertTrue(markdown.includes("Cover locked worktree removal"));
    assertTrue(markdown.includes("Apply now stops when the snapshot commit fails."));
    assertEqual(markdown.includes("## 1. Goals\n## 2. Constraints"), false);
  });
});
