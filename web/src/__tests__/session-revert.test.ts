import { reconcile, unwrap } from "solid-js/store";
import { filterVisibleTurnsByRevert } from "../components/timeline/turn-window";
import { canRevertSession } from "../store/session-capabilities";
import { upsertMessageInfo, upsertMessagePart } from "../store/session-stream";
import { hasRevertedFiles, revertToHere, undoRevert } from "../store/session-revert";
import { dispatchSessionPrompt } from "../store/workspace-session-messages";
import { replaceSessionFromEvent } from "../store/workspace-sessions";
import { setWorkspace, workspace } from "../store/workspace-state";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("session revert", () => {
  it("keeps busy and retry tasks running without sending abort or revert requests", async () => {
    const saved = structuredClone(unwrap(workspace));
    const savedFetch = globalThis.fetch;
    const calls: string[] = [];
    globalThis.fetch = (async (url: string) => {
      calls.push(url);
      throw new Error("busy revert must not make requests");
    }) as typeof fetch;
    try {
      const id = "ses_hoxi_busy_revert";
      setWorkspace("sessions", [{ id, title: "Task", time: { created: 1, updated: 1 }, capabilities: { revert: true } }]);
      for (const type of ["busy", "retry"] as const) {
        setWorkspace("statusBySession", id, type === "busy" ? { type } : { type, attempt: 1, message: "retry", next: 2 });
        assertEqual(canRevertSession(id), false);
        for (const action of [() => revertToHere(id, "msg_followup", undefined, { skipConfirm: true }), () => undoRevert(id)]) {
          let rejected = false;
          try { await action(); } catch { rejected = true; }
          assertTrue(rejected);
          assertEqual(workspace.statusBySession[id].type, type);
          assertEqual(workspace.sessions[0].revert, undefined);
          assertEqual(workspace.composerDraftEditBySession[id], undefined);
        }
      }
      assertDeepEqual(calls, []);
      setWorkspace("statusBySession", id, { type: "idle" });
      assertEqual(canRevertSession(id), true);
    } finally {
      globalThis.fetch = savedFetch;
      setWorkspace(reconcile(saved));
    }
  });

  it("only shows the code revert banner when files actually changed", () => {
    const saved = structuredClone(unwrap(workspace));
    const id = "ses_hoxi_revert_banner";
    try {
      setWorkspace("sessions", [{ id, title: "Task", time: { created: 1, updated: 1 } }]);
      assertEqual(hasRevertedFiles(id), false);
      setWorkspace("sessions", 0, "revert", { messageID: "msg_1", codeChanged: false, diff: "stale diff" });
      assertEqual(hasRevertedFiles(id), false);
      setWorkspace("sessions", 0, "revert", "codeChanged", true);
      assertEqual(hasRevertedFiles(id), true);
      setWorkspace("sessions", 0, "revert", { messageID: "msg_1", codeChanged: undefined, diff: "file diff" });
      assertEqual(hasRevertedFiles(id), true, "legacy OpenCode diff remains supported");
    } finally { setWorkspace(reconcile(saved)); }
  });

  it("does not change the transcript or draft when the backend rejects a stale idle request", async () => {
    const saved = structuredClone(unwrap(workspace));
    const savedFetch = globalThis.fetch;
    const calls: string[] = [];
    globalThis.fetch = (async (url: string) => {
      calls.push(url);
      return { ok: false, status: 409, text: async () => "session is running" } as Response;
    }) as typeof fetch;
    const id = "ses_hoxi_revert_race";
    try {
      setWorkspace("sessions", [{ id, title: "Task", directory: "/project/worktree", time: { created: 1, updated: 1 } }]);
      setWorkspace("statusBySession", id, { type: "idle" });
      setWorkspace("messagesBySession", id, [{
        info: { id: "msg_followup", sessionID: id, role: "user", time: { created: 1 } },
        parts: [{ id: "prt_text", sessionID: id, messageID: "msg_followup", type: "text", text: "Follow up" }],
      }]);
      let rejected = false;
      try { await revertToHere(id, "msg_followup"); } catch { rejected = true; }
      assertTrue(rejected);
      assertEqual(workspace.sessions[0].revert, undefined);
      assertEqual(workspace.composerDraftEditBySession[id], undefined);
      assertEqual(workspace.messagesBySession[id].length, 1);
      assertEqual(calls.length, 1);
      assertTrue(calls[0].includes(`/sessions/${id}/revert?directory=%2Fproject%2Fworktree`));
    } finally {
      globalThis.fetch = savedFetch;
      setWorkspace(reconcile(saved));
    }
  });

  it("displays newly sent messages after reverting a message even when /runtime broadcasts a pre-prompt revert", async () => {
    const saved = structuredClone(unwrap(workspace));
    const savedFetch = globalThis.fetch;
    const id = "ses_hoxi_revert_then_send";
    const dir = "/project/revert-send";
    const revertedInfo = {
      id,
      title: "Revert Send",
      directory: dir,
      runtime: "antigravity" as const,
      managed: true,
      time: { created: 1000, updated: 1500 },
      revert: { messageID: "msg_u_1000_1", codeChanged: false },
    };

    globalThis.fetch = (async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes(`/sessions/${id}/revert`)) {
        return { ok: true, status: 200, json: async () => revertedInfo } as Response;
      }
      if (url.includes(`/sessions/${id}/diff`)) {
        return { ok: true, status: 200, json: async () => [] } as Response;
      }
      if (url.includes(`/sessions/${id}/runtime`)) {
        // Simulate the SSE session.updated emitted by POST .../runtime before prompt_async clears revert.
        replaceSessionFromEvent(revertedInfo, dir);
        return { ok: true, status: 200, json: async () => revertedInfo } as Response;
      }
      if (url.includes(`/sessions/${id}/prompt_async`)) {
        return { ok: true, status: 200, json: async () => ({ ok: true, messageId: "msg_u_2000_2" }) } as Response;
      }
      return { ok: true, status: 200, json: async () => ({}) } as Response;
    }) as typeof fetch;

    try {
      setWorkspace("providers", []);
      setWorkspace("rootDir", dir);
      setWorkspace("activeSessionID", id);
      setWorkspace("sessions", [{
        id,
        title: "Revert Send",
        directory: dir,
        runtime: "antigravity",
        managed: true,
        time: { created: 1000, updated: 1000 },
      }]);
      setWorkspace("aiBySession", id, {
        agent: "build",
        model: { providerID: "antigravity", modelID: "gemini-3-flash" },
      });
      setWorkspace("statusBySession", id, { type: "idle" });
      setWorkspace("messagesBySession", id, [
        {
          info: { id: "msg_u_1000_1", sessionID: id, role: "user", time: { created: 1000 } },
          parts: [{ id: "prt_u_1", sessionID: id, messageID: "msg_u_1000_1", type: "text", text: "First prompt" }],
        },
        {
          info: { id: "msg_a_1001_1", sessionID: id, role: "assistant", time: { created: 1001, completed: 1200 } },
          parts: [{ id: "prt_a_1", sessionID: id, messageID: "msg_a_1001_1", type: "text", text: "First answer" }],
        },
      ]);

      await revertToHere(id, "msg_u_1000_1", undefined, { skipConfirm: true });
      assertEqual(workspace.sessions[0].revert?.messageID, "msg_u_1000_1");
      assertEqual(
        filterVisibleTurnsByRevert(workspace.messagesBySession[id], workspace.sessions[0].revert).length,
        0,
        "reverted first turn should be hidden while revert is active"
      );

      await dispatchSessionPrompt("Second prompt after revert", id, { directory: dir });
      assertEqual(workspace.sessions[0].revert, undefined, "sending a new prompt must keep session.revert cleared");

      const visibleDuringOptimistic = filterVisibleTurnsByRevert(
        workspace.messagesBySession[id],
        workspace.sessions[0].revert
      );
      assertEqual(visibleDuringOptimistic.length, 1, "optimistic new user message must be visible");
      assertEqual(visibleDuringOptimistic[0].info.role, "user");
      assertEqual(
        (visibleDuringOptimistic[0].parts[0] as { type: "text"; text: string }).text,
        "Second prompt after revert"
      );

      // Promote the optimistic user turn to the server-assigned ID via SSE.
      upsertMessageInfo({ id: "msg_u_2000_2", sessionID: id, role: "user", time: { created: 2000 } });
      upsertMessagePart({
        id: "prt_u_2",
        sessionID: id,
        messageID: "msg_u_2000_2",
        type: "text",
        text: "Second prompt after revert",
      });

      const visibleAfterPromotion = filterVisibleTurnsByRevert(
        workspace.messagesBySession[id],
        workspace.sessions[0].revert
      );
      assertEqual(visibleAfterPromotion.length, 1, "promoted user message must remain visible");
      assertEqual(visibleAfterPromotion[0].info.id, "msg_u_2000_2");
    } finally {
      globalThis.fetch = savedFetch;
      setWorkspace(reconcile(saved));
    }
  });

  it("orders Runner user and assistant message IDs by timestamp when filtering truncated revert transcripts", () => {
    const turns = [
      {
        info: { id: "msg_u_1000_1", sessionID: "ses_1", role: "user" as const, time: { created: 1000 } },
        parts: [{ id: "prt_1", sessionID: "ses_1", messageID: "msg_u_1000_1", type: "text" as const, text: "Turn 1" }],
      },
      {
        info: { id: "msg_a_1001_1", sessionID: "ses_1", role: "assistant" as const, time: { created: 1001 } },
        parts: [{ id: "prt_2", sessionID: "ses_1", messageID: "msg_a_1001_1", type: "text" as const, text: "Reply 1" }],
      },
      {
        info: { id: "msg_u_2000_2", sessionID: "ses_1", role: "user" as const, time: { created: 2000 } },
        parts: [{ id: "prt_3", sessionID: "ses_1", messageID: "msg_u_2000_2", type: "text" as const, text: "Turn 2" }],
      },
    ];
    // msg_a_2001_2 was already truncated by the server; msg_u_1000_1 and msg_u_2000_2 precede it despite "msg_u_" > "msg_a_".
    const visible = filterVisibleTurnsByRevert(turns, { messageID: "msg_a_2001_2" });
    assertDeepEqual(
      visible.map((t) => t.info.id),
      ["msg_u_1000_1", "msg_a_1001_1", "msg_u_2000_2"]
    );
  });
});
