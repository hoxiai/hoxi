import { reconcile, unwrap } from "solid-js/store";
import { setWorkspace, workspace, type MessageTurn } from "../store/workspace";
import {
  beginSessionTranscriptRead,
  estimateTranscriptBytes,
  evictCachedSessionTurns,
  hydratedSessionIDs,
  persistCachedSessionTurns,
  touchSessionTranscript,
  trimSessionTranscripts,
} from "../store/session-transcripts";
import {
  getFullToolOutput,
  normalizeMessagePartInPlace,
} from "../store/message-lookup";
import { hoxiAPI } from "../services/api";
import { cancelScheduledWorktreeStatus } from "../store/session-diffs";
import { assertEqual, assertTrue, describe, it } from "./test-harness";
import { refreshSettledSessionMessages, resyncResidentTranscripts } from "../store/workspace-sessions";

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
  it("shares the pending history read for both completion events of the same turn", async () => {
    const saved = structuredClone(unwrap(workspace));
    const originalAPI = { ...hoxiAPI };
    const sid = "ses_settled_refresh";
    const initial = turn(sid);
    initial.info.time.completed = 10;
    let calls = 0;
    let release!: (turns: MessageTurn[]) => void;
    hoxiAPI.getMessages = () => {
      calls++;
      return new Promise((resolve) => { release = resolve; });
    };
    hoxiAPI.getSessionDiff = async () => [];
    try {
      setWorkspace("messagesBySession", sid, [initial]);
      const first = refreshSettledSessionMessages(sid);
      const second = refreshSettledSessionMessages(sid);
      assertEqual(first, second);
      assertEqual(calls, 1);
      release([initial]);
      await first;
      const next = refreshSettledSessionMessages(sid);
      assertEqual(calls, 2);
      release([initial]);
      await next;
    } finally {
      release?.([initial]);
      cancelScheduledWorktreeStatus(sid);
      hydratedSessionIDs.delete(sid);
      evictCachedSessionTurns([sid]);
      Object.assign(hoxiAPI, originalAPI);
      setWorkspace(reconcile(saved));
    }
  });

  it("keeps usable recent snapshots when one session exceeds the storage budget", async () => {
    const storageDescriptor = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
    let raw = "";
    Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
      getItem: () => null,
      setItem: (_key: string, value: string) => { raw = value; },
    } });
    const recent = 'ses_cached_"recent';
    const huge = "ses_cached_huge";
    const oversized = turn(huge);
    oversized.parts = [{
      id: "prt_oversized", sessionID: huge, messageID: oversized.info.id, type: "tool",
      callID: "write", tool: "write", state: {
        status: "completed", input: { content: "x".repeat(600_000) }, output: "ok", metadata: {},
        time: { start: 1, end: 2 }, title: "Write",
      },
    }];
    try {
      persistCachedSessionTurns(recent, [turn(recent)]);
      persistCachedSessionTurns(huge, [oversized]);
      await new Promise((resolve) => setTimeout(resolve, 375));
      assertTrue(raw.length <= 600_000);
      const stored = JSON.parse(raw);
      assertTrue(stored.order.includes(recent));
      assertEqual(stored.entries[recent][0].info.sessionID, recent);
      assertEqual(stored.entries[huge], undefined);
    } finally {
      evictCachedSessionTurns([recent, huge]);
      if (storageDescriptor) Object.defineProperty(globalThis, "localStorage", storageDescriptor);
      else Reflect.deleteProperty(globalThis, "localStorage");
    }
  });

  it("recovers busy transcripts after overflow with bounded, directory-scoped snapshot reads", async () => {
    const saved = structuredClone(unwrap(workspace));
    const getMessages = hoxiAPI.getMessages;
    const ids = Array.from({ length: 8 }, (_, i) => `recover_${i}`);
    let active = 0;
    let peak = 0;
    let calls = 0;
    setWorkspace(reconcile({ ...saved, rootDir: "/repo", activeSessionID: ids[0],
      sessions: ids.map((id) => ({ id, title: id, directory: `/repo/${id}`, time: { created: 1, updated: 1 } })),
      worktreeBySession: {}, messagesBySession: Object.fromEntries(ids.map((id) => [id, [turn(id, 2)]])),
      statusBySession: Object.fromEntries(ids.map((id) => [id, { type: "busy" }])) }));
    hoxiAPI.getMessages = async (id, directory) => {
      assertEqual(directory, `/repo/${id}`);
      calls++;
      active++;
      peak = Math.max(peak, active);
      await Promise.resolve();
      active--;
      return [turn(id, 20)];
    };
    try {
      await resyncResidentTranscripts();
      assertEqual(calls, ids.length);
      assertTrue(peak <= 4, "snapshot recovery must not launch an unbounded request burst");
      for (const id of ids) assertEqual((workspace.messagesBySession[id][0].parts[0] as { text: string }).text.length, 20);
    } finally {
      hoxiAPI.getMessages = getMessages;
      for (const id of ids) hydratedSessionIDs.delete(id);
      setWorkspace(reconcile(saved));
    }
  });
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

  it("bounds cached full tool outputs by total bytes and evicts them with their session transcript", () => fixture(() => {
    for (let i = 0; i < 8; i++) {
      const part: any = {
        id: `prt_bounded_${i}`,
        sessionID: "ses_tool_burst",
        messageID: "msg_tool_burst",
        type: "tool",
        callID: `call_${i}`,
        tool: "bash",
        state: {
          status: "completed",
          input: { command: "cat big.log" },
          output: "X".repeat(512 * 1024),
          metadata: {},
          time: { start: 1, end: 2 },
          title: "cat big.log",
        },
      };
      normalizeMessagePartInPlace(part);
    }
    assertEqual(getFullToolOutput("prt_bounded_0"), undefined);
    assertTrue(!!getFullToolOutput("prt_bounded_7"));

    const toolTurn: MessageTurn = {
      info: { id: "msg_tool_burst", sessionID: "ses_tool_burst", role: "assistant", time: { created: 1 } },
      parts: [{ id: "prt_bounded_7", sessionID: "ses_tool_burst", messageID: "msg_tool_burst", type: "text", text: "done" }],
    };
    assertTrue(estimateTranscriptBytes([toolTurn], "ses_tool_burst") > 512 * 1024 * 2);

    setWorkspace("sessions", [
      { id: "ses_tool_burst", title: "burst", time: { created: 1, updated: 1 } },
      { id: "active", title: "active", time: { created: 1, updated: 2 } },
    ]);
    setWorkspace("messagesBySession", {
      ses_tool_burst: [toolTurn],
      active: [turn("active")],
    });
    setWorkspace("activeSessionID", "active");
    assertEqual(trimSessionTranscripts(1).join(), "ses_tool_burst");
    assertEqual(getFullToolOutput("prt_bounded_7"), undefined);
  }));
});
