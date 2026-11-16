// web/src/__tests__/session-error-recovery.test.ts
import { reconcile } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import {
  clearAutoFailoverPending,
  clearTurnDispatchState,
  dispatchSessionPrompt,
  handleSessionErrorEvent,
  retryLastUserPrompt,
  setWorkspace,
  workspace,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("session error recovery on single-model turns", () => {
  it("attaches error turn after a failed follow-up prompt without swallowing the user prompt or overwriting prior assistant turns", async () => {
    const sid = "ses_followup_error_keep";
    const orig = {
      send: hoxiAPI.sendPromptAsync,
      msgs: hoxiAPI.getMessages,
      diff: hoxiAPI.getSessionDiff,
      ren: hoxiAPI.renameSession,
      sum: hoxiAPI.summarizeSessionTitle,
    };
    try {
      const priorTurns = [
        {
          info: { id: "msg_u_prior", sessionID: sid, role: "user" as const, time: { created: 1000 } },
          parts: [{ id: "prt_u_prior", sessionID: sid, messageID: "msg_u_prior", type: "text" as const, text: "initial request" }],
        },
        {
          info: {
            id: "msg_a_prior",
            sessionID: sid,
            role: "assistant" as const,
            parentID: "msg_u_prior",
            time: { created: 1005, completed: 1010 },
            error: { name: "OldError", data: { message: "prior turn warning" } },
          },
          parts: [{ id: "prt_a_prior", sessionID: sid, messageID: "msg_a_prior", type: "text" as const, text: "prior answer" }],
        },
      ];
      hoxiAPI.getMessages = async () => structuredClone(priorTurns);
      hoxiAPI.getSessionDiff = async () => [];
      hoxiAPI.renameSession = async () => ({ ok: true } as never);
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: false, title: "" });

      const sentTexts: string[] = [];
      hoxiAPI.sendPromptAsync = async (_id, text) => {
        sentTexts.push(text);
      };

      setWorkspace("providers", [
        { id: "openai", name: "OpenAI", models: { "gpt-5": { id: "gpt-5", name: "GPT-5" } } },
      ]);
      setWorkspace("connectedProviders", ["openai"]);
      setWorkspace("sessions", [{ id: sid, title: "Followup", time: { created: 1, updated: 1 } } as never]);
      setWorkspace("activeSessionID", sid);
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: "openai", modelID: "gpt-5" },
        effort: "",
      });
      setWorkspace("messagesBySession", sid, structuredClone(priorTurns));

      await dispatchSessionPrompt("follow-up question after crash", sid, { forceImmediate: true });
      assertDeepEqual(sentTexts, ["follow-up question after crash"]);

      // Non-retryable error on single-model prompt settles immediately via loadSessionMessages + error card.
      await handleSessionErrorEvent(sid, {
        name: "ProviderModelNotFoundError",
        data: { message: "Model not found: openai/gpt-5", statusCode: 500, kind: "model" },
      });

      const turns = workspace.messagesBySession[sid] ?? [];
      assertEqual(turns.length, 4, "prior user + prior assistant + new user + new assistant error");
      assertEqual(turns[1].info.error?.name, "OldError", "prior assistant error must stay untouched");
      assertEqual(turns[2].info.role, "user");
      const userText = turns[2].parts.find((p) => p.type === "text");
      assertEqual(userText && userText.type === "text" ? userText.text : "", "follow-up question after crash");
      assertEqual(turns[3].info.role, "assistant");
      assertEqual(turns[3].info.error?.name, "ProviderModelNotFoundError");
      assertEqual(turns[3].info.error?.data?.message, "Model not found: openai/gpt-5");

      // Clicking retry must resend the failed follow-up prompt rather than the older turn.
      await retryLastUserPrompt(sid);
      assertDeepEqual(sentTexts, ["follow-up question after crash", "follow-up question after crash"]);
    } finally {
      clearAutoFailoverPending(sid);
      clearTurnDispatchState(sid);
      Object.assign(hoxiAPI, {
        sendPromptAsync: orig.send,
        getMessages: orig.msgs,
        getSessionDiff: orig.diff,
        renameSession: orig.ren,
        summarizeSessionTitle: orig.sum,
      });
      setWorkspace("activeSessionID", null);
      setWorkspace("sessions", []);
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("messagesBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
    }
  });

  it("reconstructs missing user turn from dispatch state if optimistic bubble was cleared before error settled", async () => {
    const sid = "ses_followup_error_reconstruct";
    const orig = {
      send: hoxiAPI.sendPromptAsync,
      msgs: hoxiAPI.getMessages,
      diff: hoxiAPI.getSessionDiff,
      sum: hoxiAPI.summarizeSessionTitle,
    };
    try {
      const now = Date.now();
      const priorTurns = [
        {
          info: { id: "msg_u_same_ms", sessionID: sid, role: "user" as const, time: { created: now } },
          parts: [{ id: "prt_u_same_ms", sessionID: sid, messageID: "msg_u_same_ms", type: "text" as const, text: "older prompt" }],
        },
        {
          info: { id: "msg_a_same_ms", sessionID: sid, role: "assistant" as const, time: { created: now, completed: now } },
          parts: [{ id: "prt_a_same_ms", sessionID: sid, messageID: "msg_a_same_ms", type: "text" as const, text: "older reply" }],
        },
      ];
      hoxiAPI.getMessages = async () => structuredClone(priorTurns);
      hoxiAPI.getSessionDiff = async () => [];
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: false, title: "" });
      hoxiAPI.sendPromptAsync = async () => {};

      setWorkspace("providers", [
        { id: "openai", name: "OpenAI", models: { "gpt-5": { id: "gpt-5", name: "GPT-5" } } },
      ]);
      setWorkspace("connectedProviders", ["openai"]);
      setWorkspace("sessions", [{ id: sid, title: "Reconstruct", time: { created: 1, updated: 1 } } as never]);
      setWorkspace("activeSessionID", sid);
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: "openai", modelID: "gpt-5" },
        effort: "",
      });
      setWorkspace("messagesBySession", sid, structuredClone(priorTurns));

      await dispatchSessionPrompt("second prompt", sid, { forceImmediate: true });
      // Simulate external snapshot replacing messagesBySession with only priorTurns before error arrives.
      setWorkspace("messagesBySession", sid, structuredClone(priorTurns));

      await handleSessionErrorEvent(sid, {
        name: "ContextOverflowError",
        data: { message: "prompt is too long", statusCode: 400, kind: "context" },
      });

      const turns = workspace.messagesBySession[sid] ?? [];
      assertEqual(turns.length, 4);
      assertEqual(turns[1].info.error, undefined, "prior assistant turn must not absorb new turn error");
      assertEqual(turns[2].info.role, "user");
      const textPart = turns[2].parts.find((p) => p.type === "text");
      assertEqual(textPart && textPart.type === "text" ? textPart.text : "", "second prompt");
      assertEqual(turns[3].info.role, "assistant");
      assertEqual(turns[3].info.error?.name, "ContextOverflowError");
    } finally {
      clearAutoFailoverPending(sid);
      clearTurnDispatchState(sid);
      Object.assign(hoxiAPI, {
        sendPromptAsync: orig.send,
        getMessages: orig.msgs,
        getSessionDiff: orig.diff,
        summarizeSessionTitle: orig.sum,
      });
      setWorkspace("activeSessionID", null);
      setWorkspace("sessions", []);
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("messagesBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
    }
  });

  it("suppresses stale error card if a new prompt is dispatched while getMessages is in flight", async () => {
    const sid = "ses_stale_error_suppressed";
    const orig = {
      send: hoxiAPI.sendPromptAsync,
      msgs: hoxiAPI.getMessages,
      diff: hoxiAPI.getSessionDiff,
      sum: hoxiAPI.summarizeSessionTitle,
    };
    try {
      let triggeredNewPrompt = false;
      hoxiAPI.getSessionDiff = async () => [];
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: false, title: "" });
      hoxiAPI.sendPromptAsync = async () => {};
      hoxiAPI.getMessages = async () => {
        if (!triggeredNewPrompt) {
          triggeredNewPrompt = true;
          await dispatchSessionPrompt("fresh prompt during reload", sid, { forceImmediate: true });
        }
        return [];
      };

      setWorkspace("providers", [
        { id: "openai", name: "OpenAI", models: { "gpt-5": { id: "gpt-5", name: "GPT-5" } } },
      ]);
      setWorkspace("connectedProviders", ["openai"]);
      setWorkspace("sessions", [{ id: sid, title: "Supersede", time: { created: 1, updated: 1 } } as never]);
      setWorkspace("activeSessionID", sid);
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: "openai", modelID: "gpt-5" },
        effort: "",
      });

      await dispatchSessionPrompt("first failing prompt", sid, { forceImmediate: true });
      await handleSessionErrorEvent(sid, {
        name: "ContextOverflowError",
        data: { message: "prompt is too long", statusCode: 400, kind: "context" },
      });

      const turns = workspace.messagesBySession[sid] ?? [];
      assertTrue(
        !turns.some((t) => t.info.error?.name === "ContextOverflowError"),
        "stale error must not attach after a newer prompt has been dispatched"
      );
      assertEqual(workspace.statusBySession[sid]?.type, "busy");
    } finally {
      clearAutoFailoverPending(sid);
      clearTurnDispatchState(sid);
      Object.assign(hoxiAPI, {
        sendPromptAsync: orig.send,
        getMessages: orig.msgs,
        getSessionDiff: orig.diff,
        summarizeSessionTitle: orig.sum,
      });
      setWorkspace("activeSessionID", null);
      setWorkspace("sessions", []);
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("messagesBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
    }
  });
});
