// web/src/__tests__/session-capabilities.test.ts
import { describe, it, assertEqual } from "./test-harness";
import {
  canApproveSession,
  canAttachToSession,
  canForkSession,
  canRevertSession,
  canSteerSession,
} from "../store/session-capabilities";
import { setWorkspace, workspace } from "../store/workspace";

describe("session capabilities matrix", () => {
  it("respects explicit capabilities and runtime defaults", () => {
    const prevSessions = workspace.sessions;
    try {
      setWorkspace("sessions", [
        {
          id: "ses_opencode_1",
          title: "OpenCode Task",
          runtime: "opencode",
          time: { created: 1000, updated: 1000 },
          capabilities: {
            approval: true,
            steer: true,
            resume: true,
            revert: true,
            fork: true,
            attachments: true,
            costStats: true,
          },
        },
        {
          id: "ses_claude_1",
          title: "Claude Task",
          runtime: "claude-code",
          time: { created: 1000, updated: 1000 },
          capabilities: {
            approval: true,
            steer: false,
            resume: true,
            revert: true,
            fork: true,
            attachments: true,
            costStats: true,
          },
        },
        {
          id: "ses_codebuddy_1",
          title: "CodeBuddy Task",
          runtime: "codebuddy",
          time: { created: 1000, updated: 1000 },
          capabilities: {
            approval: false,
            steer: false,
            resume: true,
            revert: true,
            fork: true,
            attachments: false,
            costStats: true,
          },
        },
      ]);

      // OpenCode: approval & steer & attach are true
      assertEqual(canApproveSession("ses_opencode_1"), true, "opencode supports approval");
      assertEqual(canSteerSession("ses_opencode_1"), true, "opencode supports steer");
      assertEqual(canAttachToSession("ses_opencode_1"), true, "opencode supports attachments");
      assertEqual(canRevertSession("ses_opencode_1"), true, "opencode supports revert");
      assertEqual(canForkSession("ses_opencode_1"), true, "opencode supports fork");

      // Claude: approval is true, steer is false
      assertEqual(canApproveSession("ses_claude_1"), true, "claude supports approval");
      assertEqual(canSteerSession("ses_claude_1"), false, "claude does not support steer");
      assertEqual(canAttachToSession("ses_claude_1"), true, "claude supports attachments");

      // CodeBuddy: approval is false, steer is false, attachments false
      assertEqual(canApproveSession("ses_codebuddy_1"), false, "codebuddy has no approval gate");
      assertEqual(canSteerSession("ses_codebuddy_1"), false, "codebuddy does not support steer");
      assertEqual(canAttachToSession("ses_codebuddy_1"), false, "codebuddy attachments unsupported");
    } finally {
      setWorkspace("sessions", prevSessions);
    }
  });
});
