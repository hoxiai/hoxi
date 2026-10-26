// web/src/__tests__/session-capabilities.test.ts
import { describe, it, assertEqual } from "./test-harness";
import {
  canApproveSession,
  canAttachToSession,
  canForkSession,
  canRevertSession,
  canSteerSession,
} from "../store/session-capabilities";
import {
  isCompletedStandaloneReasoningTurn,
  resolveActiveExecutionCycle,
  resolveMergedTurnParts,
} from "../components/timeline/context-activity-turns";
import {
  hasLeakedReasoningToolMarkup,
  normalizeReasoningText,
  stripLeakedReasoningToolMarkup,
} from "../components/timeline/ThoughtBox";
import { setWorkspace, workspace, type MessageTurn } from "../store/workspace";

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

      // Cross-runtime model target on an OpenCode session disables immediate steering
      assertEqual(
        canSteerSession("ses_opencode_1", { providerID: "openai", modelID: "gpt-5" }),
        true,
        "opencode model on opencode session supports steer"
      );
      assertEqual(
        canSteerSession("ses_opencode_1", { providerID: "antigravity", modelID: "gemini-3-pro" }),
        false,
        "antigravity model target cannot steer running opencode session"
      );
      assertEqual(
        canSteerSession("ses_opencode_1", { providerID: "claude-code", modelID: "claude-sonnet" }),
        false,
        "claude-code model target cannot steer running opencode session"
      );
    } finally {
      setWorkspace("sessions", prevSessions);
    }
  });

  it("keeps the in-flight assistant turn live when a steer user prompt is appended mid-turn", () => {
    const turns: MessageTurn[] = [
      {
        info: { id: "msg_u1", sessionID: "ses_1", role: "user", time: { created: 1000 } },
        parts: [{ id: "prt_u1", sessionID: "ses_1", messageID: "msg_u1", type: "text", text: "task 1" }],
      },
      {
        info: { id: "msg_a1", sessionID: "ses_1", role: "assistant", time: { created: 1001 } },
        parts: [
          {
            id: "prt_t1",
            sessionID: "ses_1",
            messageID: "msg_a1",
            type: "tool",
            tool: "bash",
            callID: "c1",
            state: { status: "running", input: { command: "npm test" }, time: { start: 1001 } },
          },
        ],
      },
      {
        info: { id: "optimistic_2000", sessionID: "ses_1", role: "user", time: { created: 2000 } },
        parts: [{ id: "prt_u2", sessionID: "ses_1", messageID: "optimistic_2000", type: "text", text: "steer" }],
      },
    ];

    const mergedWhileRunning = resolveMergedTurnParts(turns, 1, 0);
    assertEqual(mergedWhileRunning.ownsLiveTail, true, "in-flight assistant turn keeps live tail across trailing steer prompt");

    const activeCycleWhileRunning = resolveActiveExecutionCycle(turns);
    assertEqual(activeCycleWhileRunning?.userTurn.info.id, "msg_u1");
    assertEqual(activeCycleWhileRunning?.cycleTurns.length, 1);
    assertEqual(activeCycleWhileRunning?.cycleTurns[0].info.id, "msg_a1");

    // Once the preceding assistant step completes, the active cycle advances to the steer prompt
    turns[1].info.time = { created: 1001, completed: 2500 };
    const mergedAfterComplete = resolveMergedTurnParts(turns, 1, 0);
    assertEqual(mergedAfterComplete.ownsLiveTail, false, "completed assistant turn yields live tail");

    const activeCycleAfterComplete = resolveActiveExecutionCycle(turns);
    assertEqual(activeCycleAfterComplete?.userTurn.info.id, "optimistic_2000");
    assertEqual(activeCycleAfterComplete?.cycleTurns.length, 0);
  });

  it("strips leaked DSML tool markup and preserves completed cycle-ending reasoning-only turns", () => {
    const loop = "</invoke>\n</parameter>\n".repeat(10);
    const leakedReasoning = `Checked 6 modules. Should I continue in order or focus on a specific module?\n\n<invoke name="bash">\n<parameter name="command">ls</parameter>\n${loop}`;
    assertEqual(hasLeakedReasoningToolMarkup(leakedReasoning), true);
    assertEqual(
      stripLeakedReasoningToolMarkup(leakedReasoning),
      "Checked 6 modules. Should I continue in order or focus on a specific module?"
    );
    assertEqual(
      normalizeReasoningText(leakedReasoning),
      "Checked 6 modules. Should I continue in order or focus on a specific module?"
    );

    const turns: MessageTurn[] = [
      {
        info: { id: "msg_u1", sessionID: "ses_1", role: "user", time: { created: 100 } },
        parts: [{ id: "prt_u1", sessionID: "ses_1", messageID: "msg_u1", type: "text", text: "Continue" }],
      },
      {
        info: { id: "msg_a1", sessionID: "ses_1", role: "assistant", time: { created: 101, completed: 102 } },
        parts: [
          {
            id: "prt_t1",
            sessionID: "ses_1",
            messageID: "msg_a1",
            type: "tool",
            tool: "read",
            callID: "c1",
            state: {
              status: "completed",
              input: { filePath: "a.liquid" },
              output: "ok",
              title: "a.liquid",
              metadata: {},
              time: { start: 101, end: 102 },
            },
          },
        ],
      },
      {
        info: { id: "msg_a2", sessionID: "ses_1", role: "assistant", time: { created: 103, completed: 104 } },
        parts: [
          {
            id: "prt_r2",
            sessionID: "ses_1",
            messageID: "msg_a2",
            type: "reasoning",
            text: "Final reply emitted in reasoning; must not fold into the earlier tool group.",
            time: { start: 103, end: 104 },
          },
        ],
      },
    ];

    assertEqual(isCompletedStandaloneReasoningTurn(turns, 2), true);
    const firstTurnMerged = resolveMergedTurnParts(turns, 1, 0);
    assertEqual(
      firstTurnMerged.effectiveParts.map((p) => p.id).join(","),
      "prt_t1",
      "tool turn must not swallow completed cycle-ending reasoning-only turn"
    );
    const finalTurnMerged = resolveMergedTurnParts(turns, 2, 0);
    assertEqual(finalTurnMerged.isVisuallyEmpty, false);
    assertEqual(
      finalTurnMerged.effectiveParts.map((p) => p.id).join(","),
      "prt_r2",
      "final reasoning-only turn renders its own reasoning part"
    );
  });
});
