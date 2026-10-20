// web/src/__tests__/context-mentions.test.ts
import { assertEqual, assertTrue, describe, it } from "./test-harness";
import {
  appendTerminalOutput,
  clearTerminalOutput,
  expandContextMentions,
  inspectPromptContextMentions,
  setWorkspace,
  stripAnsiEscapeCodes,
  workspace,
} from "../store/workspace";

describe("context-mentions", () => {
  it("stripAnsiEscapeCodes removes ANSI color and OSC escape sequences", () => {
    const raw = "\x1b[31mError:\x1b[0m build failed\r\n\x1b]0;title\x07Done";
    assertEqual(stripAnsiEscapeCodes(raw), "Error: build failed\nDone");
  });

  it("expandContextMentions expands @terminal from PTY buffer and strips ANSI", () => {
    const prevRoot = workspace.rootDir;
    clearTerminalOutput();
    setWorkspace("rootDir", "/tmp/hoxi-test-term");
    try {
      appendTerminalOutput("/tmp/hoxi-test-term", "\x1b[32m$ npm test\x1b[0m\nFAIL src/app.spec.ts\n");
      const expanded = expandContextMentions("Please fix @terminal failure", null);
      assertTrue(expanded.includes("<terminal_context"), "should include terminal_context tag");
      assertTrue(expanded.includes("FAIL src/app.spec.ts"), "should include terminal output");
      assertTrue(!expanded.includes("\x1b[32m"), "should strip ANSI escape sequences");

      const chips = inspectPromptContextMentions("Please fix @terminal failure", null);
      assertEqual(chips.length, 1);
      assertEqual(chips[0].kind, "terminal");
      assertEqual(chips[0].status, "ready");
    } finally {
      clearTerminalOutput();
      setWorkspace("rootDir", prevRoot);
    }
  });

  it("expandContextMentions expands @changes with actual unified diff patches", () => {
    const sid = "ses_ctx_changes_test";
    setWorkspace("diffsBySession", sid, [
      {
        file: "src/index.ts",
        additions: 2,
        deletions: 1,
        patch: "@@ -1,2 +1,3 @@\n-const a = 1;\n+const a = 2;\n+const b = 3;",
      },
    ]);
    try {
      const expanded = expandContextMentions("Review @changes", sid);
      assertTrue(expanded.includes("<git_changes_context"), "should include git_changes_context");
      assertTrue(expanded.includes("`src/index.ts` (+2 -1)"), "should include file summary");
      assertTrue(expanded.includes("+const a = 2;"), "should include actual unified diff patch");

      const chips = inspectPromptContextMentions("Review @changes", sid);
      assertEqual(chips.length, 1);
      assertEqual(chips[0].kind, "changes");
      assertEqual(chips[0].status, "ready");
    } finally {
      setWorkspace("diffsBySession", sid, undefined as any);
    }
  });

  it("expandContextMentions expands @session with assistant conclusions and tool errors", () => {
    const refID = "ses_ctx_ref_1";
    const prevSessions = workspace.sessions;
    setWorkspace("sessions", [
      ...prevSessions,
      {
        id: refID,
        title: "Auth Migration",
        directory: "/tmp/repo",
        time: { created: 1, updated: 2 },
      } as any,
    ]);
    setWorkspace("messagesBySession", refID, [
      {
        info: { id: "msg_1", sessionID: refID, role: "assistant", time: { created: 1 } } as any,
        parts: [
          {
            id: "prt_1",
            sessionID: refID,
            messageID: "msg_1",
            type: "text",
            text: "We migrated the token verifier to RS256.",
          } as any,
          {
            id: "prt_2",
            sessionID: refID,
            messageID: "msg_1",
            type: "tool",
            tool: "bash",
            state: {
              status: "error",
              error: "jwt_test.go:42: invalid key type",
            },
          } as any,
        ],
      },
    ]);
    try {
      const expanded = expandContextMentions(`Continue from @session:${refID}`, "ses_current");
      assertTrue(expanded.includes(`<session_context id="${refID}"`), "should include session_context");
      assertTrue(expanded.includes("migrated the token verifier to RS256"), "should include conclusion");
      assertTrue(expanded.includes("jwt_test.go:42: invalid key type"), "should include tool error");
    } finally {
      setWorkspace("sessions", prevSessions);
      setWorkspace("messagesBySession", refID, undefined as any);
    }
  });
});
