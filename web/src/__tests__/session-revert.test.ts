import { reconcile, unwrap } from "solid-js/store";
import { canRevertSession } from "../store/session-capabilities";
import { hasRevertedFiles, revertToHere, undoRevert } from "../store/session-revert";
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
});
