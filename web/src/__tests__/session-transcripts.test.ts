import { reconcile, unwrap } from "solid-js/store";
import { setWorkspace, workspace, type MessageTurn } from "../store/workspace";
import { beginSessionTranscriptRead, hydratedSessionIDs, touchSessionTranscript, trimSessionTranscripts } from "../store/session-transcripts";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

function turn(id: string, chars = 20): MessageTurn {
  return { info: { id: `msg_${id}`, sessionID: id, role: "assistant", time: { created: 1 } },
    parts: [{ id: `prt_${id}`, sessionID: id, messageID: `msg_${id}`, type: "text", text: "x".repeat(chars) }] };
}

function fixture(run: () => void): void {
  const saved = structuredClone(unwrap(workspace));
  setWorkspace(reconcile({ ...saved, activeSessionID: null, sessions: [], messagesBySession: {}, statusBySession: {},
    loadingMessagesBySession: {}, queuedPromptsBySession: {}, pendingPermissions: [], pendingQuestions: [] }));
  try { run(); } finally {
    hydratedSessionIDs.clear();
    setWorkspace(reconcile(saved));
  }
}

describe("Resident session transcript budget", () => {
  it("evicts the least recently used idle body without removing metadata or drafts", () => fixture(() => {
    setWorkspace("sessions", ["old", "recent", "active"].map((id) => ({ id, title: id, time: { created: 1, updated: 1 } })));
    for (const id of ["old", "recent", "active"]) {
      setWorkspace("messagesBySession", id, [turn(id)]);
      hydratedSessionIDs.add(id);
      touchSessionTranscript(id);
    }
    setWorkspace("activeSessionID", "active");
    assertEqual(trimSessionTranscripts(2).join(), "old");
    assertEqual(workspace.messagesBySession.old, undefined);
    assertTrue(!hydratedSessionIDs.has("old"));
    assertEqual(workspace.sessions.length, 3);
    assertEqual(workspace.messagesBySession.active.length, 1);
  }));

  it("protects busy sessions, active subagents and pending reads while enforcing the byte budget", () => fixture(() => {
    setWorkspace("messagesBySession", { idle: [turn("idle", 4096)], busy: [turn("busy")], child: [turn("child")], reading: [turn("reading")] });
    setWorkspace("sessions", [{ id: "child", parentID: "parent", title: "child", time: { created: 1, updated: 1 } }]);
    setWorkspace("activeSessionID", "parent");
    setWorkspace("statusBySession", "busy", { type: "busy" });
    const finish = beginSessionTranscriptRead("reading");
    try {
      assertEqual(trimSessionTranscripts(0, 0).join(), "idle");
      assertTrue(workspace.messagesBySession.busy !== undefined);
      assertTrue(workspace.messagesBySession.child !== undefined);
      assertTrue(workspace.messagesBySession.reading !== undefined);
    } finally { finish(); }
    assertEqual(trimSessionTranscripts(0, 0).join(), "reading");
  }));
});
