import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { collectCycleChangedFiles, collectSessionChangedFiles } from "../components/timeline/tool-card-state";
import {
  collectCurrentTaskTurns, findTaskCycleStart, forgetSessionTaskHistory,
  dispatchSessionPrompt, resumePausedSession, upsertMessageInfo,
  loadCurrentTaskHistory, noteSessionHistoryTail, removeSessionFromStore,
  setWorkspace, workspace, type MessageTurn,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

function turn(sid: string, index: number, role: "user" | "assistant" = "assistant", continueTask = false): MessageTurn {
  return {
    info: { id: `msg_${String(index).padStart(5, "0")}`, sessionID: sid, role, time: { created: index + 1 },
      ...(continueTask ? { continueTask: true } : {}) },
    parts: [],
  };
}

function edit(message: MessageTurn, filePath: string): MessageTurn {
  message.parts.push({
    id: `prt_${message.info.id}`, sessionID: message.info.sessionID, messageID: message.info.id,
    type: "tool", tool: "edit", callID: message.info.id,
    state: { status: "completed", input: { filePath, oldString: "old", newString: "new" },
      output: "", title: "", metadata: {}, time: { start: 1, end: 2 } },
  });
  return message;
}

async function fixture(run: (sid: string) => Promise<void>): Promise<void> {
  const saved = structuredClone(unwrap(workspace));
  const originalAPI = { ...hoxiAPI };
  const sid = `ses_hoxi_task_history_${Math.random().toString(36).slice(2)}`;
  try {
    setWorkspace({ rootDir: "/repo", activeSessionID: sid,
      sessions: [{ id: sid, title: "Long task", directory: "/repo", time: { created: 1, updated: 1 } }] });
    setWorkspace("statusBySession", sid, { type: "busy" });
    await run(sid);
  } finally {
    forgetSessionTaskHistory(sid);
    removeSessionFromStore(sid);
    Object.assign(hoxiAPI, originalAPI);
    setWorkspace(reconcile(saved));
  }
}

const files = (sid: string) => collectCycleChangedFiles(collectCurrentTaskTurns(sid), sid)
  .map((diff) => ({ file: diff.file, additions: diff.additions, deletions: diff.deletions }));

describe("current task history", () => {
  it("restores edits before the newest 200 messages without loading older chat or unrelated tasks", async () => {
    await fixture(async (sid) => {
      const all = Array.from({ length: 455 }, (_, i) => turn(sid, i));
      all[0] = turn(sid, 0, "user");
      all[1] = edit(all[1], "unrelated.ts");
      all[2] = turn(sid, 2, "user");
      all[3] = edit(all[3], "early.ts");
      all[400] = edit(all[400], "recent.ts");
      const tail = all.slice(-200);
      setWorkspace("messagesBySession", sid, tail);
      noteSessionHistoryTail(sid, 200, 200, 0);
      const cursors: string[] = [];
      hoxiAPI.getMessages = async (id, directory, limit, before) => {
        assertEqual(id, sid);
        assertEqual(directory, "/repo");
        assertEqual(limit, 200);
        cursors.push(before!);
        const end = all.findIndex((message) => message.info.id === before);
        return structuredClone(all.slice(Math.max(0, end - 200), end));
      };
      assertDeepEqual(files(sid).map((diff) => diff.file), ["recent.ts"]);
      await Promise.all([loadCurrentTaskHistory(sid), loadCurrentTaskHistory(sid)]);
      assertDeepEqual(files(sid), [
        { file: "early.ts", additions: 1, deletions: 1 },
        { file: "recent.ts", additions: 1, deletions: 1 },
      ]);
      assertEqual(workspace.messagesBySession[sid].length, 200, "chat remains paged");
      assertEqual(workspace.messagesBySession[sid][0].info.id, tail[0].info.id);
      assertEqual(cursors.length, 2, "reads stop at the current task boundary and deduplicate requests");
      await loadCurrentTaskHistory(sid);
      assertEqual(cursors.length, 2, "streaming updates reuse the task prefix");

      setWorkspace("messagesBySession", sid, structuredClone(all));
      assertEqual(files(sid).length, 2, "loading chat history must not double-count edits");
      assertEqual(files(sid)[0].additions, 1);
    });
  });

  it("keeps edits through explicit continue/steer messages and resets on a new task", async () => {
    await fixture(async (sid) => {
      const turns = [turn(sid, 0, "user"), edit(turn(sid, 1), "first.ts"),
        turn(sid, 2, "user", true), turn(sid, 3)];
      setWorkspace("messagesBySession", sid, turns);
      assertEqual(findTaskCycleStart(turns), 0);
      assertDeepEqual(files(sid).map((diff) => diff.file), ["first.ts"]);
      assertDeepEqual(collectSessionChangedFiles(turns, sid).map((diff) => diff.file), ["first.ts"]);
      setWorkspace("messagesBySession", sid, [...turns, turn(sid, 4, "user"), edit(turn(sid, 5), "second.ts")]);
      assertDeepEqual(files(sid).map((diff) => diff.file), ["second.ts"]);
    });
  });

  it("reads past a paged continuation to recover the original task's edits", async () => {
    await fixture(async (sid) => {
      const all = Array.from({ length: 210 }, (_, i) => turn(sid, i));
      all[0] = turn(sid, 0, "user");
      all[1] = edit(all[1], "before-pause.ts");
      all[209] = turn(sid, 209, "user", true);
      setWorkspace("messagesBySession", sid, all.slice(-200));
      noteSessionHistoryTail(sid, 200, 200, 0);
      hoxiAPI.getMessages = async () => structuredClone(all.slice(0, 10));
      await loadCurrentTaskHistory(sid);
      assertDeepEqual(files(sid).map((diff) => diff.file), ["before-pause.ts"]);
    });
  });

  it("marks resumed and steered prompts in both optimistic state and the API, leaving new tasks independent", async () => {
    await fixture(async (sid) => {
      setWorkspace("providers", []);
      setWorkspace("sessions", 0, "runtime", "antigravity");
      setWorkspace("aiBySession", sid, { agent: "build", model: { providerID: "antigravity", modelID: "gemini-3-flash" } });
      setWorkspace("messagesBySession", sid, [turn(sid, 0, "user"), edit(turn(sid, 1), "existing.ts")]);
      setWorkspace("statusBySession", sid, { type: "idle" });
      const flags: Array<boolean | undefined> = [];
      hoxiAPI.sendPromptAsync = async (_id, _text, options) => { flags.push(options?.continueTask); };
      await resumePausedSession(sid);
      assertEqual(workspace.messagesBySession[sid].at(-1)?.info.continueTask, true);
      assertDeepEqual(files(sid).map((diff) => diff.file), ["existing.ts"]);
      upsertMessageInfo({ id: "msg_resumed", sessionID: sid, role: "user", time: { created: Date.now() } });
      assertEqual(workspace.messagesBySession[sid].at(-1)?.info.continueTask, true, "SSE promotion preserves the marker");
      await dispatchSessionPrompt("Steer the running task", sid, { forceImmediate: true });
      assertEqual(workspace.messagesBySession[sid].at(-1)?.info.continueTask, true);
      setWorkspace("statusBySession", sid, { type: "idle" });
      await dispatchSessionPrompt("A separate task", sid);
      assertTrue(!workspace.messagesBySession[sid].at(-1)?.info.continueTask);
      assertDeepEqual(files(sid), []);
      assertDeepEqual(flags, [true, true, false]);
    });
  });

  for (const invalidation of ["new task", "directory", "cleanup", "deletion", "revert"] as const) {
    it(`discards a late history response after ${invalidation}`, async () => {
      await fixture(async (sid) => {
        setWorkspace("messagesBySession", sid, [turn(sid, 200)]);
        noteSessionHistoryTail(sid, 200, 200, 0);
        let resolve!: (turns: MessageTurn[]) => void;
        let started!: () => void;
        const requested = new Promise<void>((done) => { started = done; });
        hoxiAPI.getMessages = () => { started(); return new Promise((done) => { resolve = done; }); };
        const pending = loadCurrentTaskHistory(sid);
        await requested;
        if (invalidation === "new task") setWorkspace("messagesBySession", sid, [turn(sid, 200), turn(sid, 201, "user")]);
        if (invalidation === "directory") setWorkspace("sessions", 0, "directory", "/other");
        if (invalidation === "cleanup") forgetSessionTaskHistory(sid);
        if (invalidation === "deletion") removeSessionFromStore(sid);
        if (invalidation === "revert") setWorkspace("sessions", 0, "revert", { messageID: "msg_00100" });
        resolve([turn(sid, 0, "user"), edit(turn(sid, 1), "stale.ts")]);
        await pending;
        assertDeepEqual(files(sid), []);
      });
    });
  }

  it("does not loop when an upstream backend ignores the history cursor", async () => {
    await fixture(async (sid) => {
      const tail = Array.from({ length: 200 }, (_, i) => turn(sid, i + 200));
      setWorkspace("messagesBySession", sid, tail);
      noteSessionHistoryTail(sid, 200, 200, 0);
      let calls = 0;
      hoxiAPI.getMessages = async () => { calls++; return structuredClone(tail); };
      await loadCurrentTaskHistory(sid);
      assertEqual(calls, 1);
      assertDeepEqual(files(sid), []);
    });
  });

  it("only accepts turns before the cursor when an upstream response includes the whole conversation", async () => {
    await fixture(async (sid) => {
      const tail = Array.from({ length: 200 }, (_, i) => turn(sid, i + 200));
      setWorkspace("messagesBySession", sid, tail);
      noteSessionHistoryTail(sid, 200, 200, 0);
      hoxiAPI.getMessages = async () => [turn(sid, 0, "user"), edit(turn(sid, 1), "early.ts"),
        ...tail, turn(sid, 500, "user"), edit(turn(sid, 501), "future-task.ts")];
      await loadCurrentTaskHistory(sid);
      assertDeepEqual(files(sid).map((diff) => diff.file), ["early.ts"]);
    });
  });

  for (const tool of ["task", "spawn_agent"]) it(`recovers ${tool} links before discarding large historical command output`, async () => {
    await fixture(async (sid) => {
      const childID = `${sid}_child`;
      const user = turn(sid, 0, "user");
      const task = turn(sid, 1);
      task.parts.push({ id: "prt_task", messageID: task.info.id, sessionID: sid, type: "tool",
        tool, callID: "task", state: { status: "completed", input: {},
          output: `${"x".repeat(100_000)}\nsession_id: ${childID}`, title: "", metadata: {}, time: { start: 1, end: 2 } } });
      setWorkspace("messagesBySession", sid, [turn(sid, 200)]);
      noteSessionHistoryTail(sid, 200, 200, 0);
      hoxiAPI.getMessages = async () => [user, task];
      await loadCurrentTaskHistory(sid);
      const restored = collectCurrentTaskTurns(sid).flatMap((message) => message.parts).find((part) => part.type === "tool");
      assertTrue(restored?.type === "tool");
      if (restored?.type === "tool" && restored.state.status === "completed") {
        assertEqual(restored.state.output, "");
        assertEqual(restored.state.metadata.sessionID, childID);
      }
    });
  });
});
