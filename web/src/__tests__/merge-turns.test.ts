// web/src/__tests__/merge-turns.test.ts
import { createStore, produce } from "solid-js/store";
import {
  evictCachedSessionTurns,
  forgetSubagentLinks,
  getFullToolOutput,
  lookupSubagentParent,
  mergeServerTurnsInto,
  persistCachedSessionTurns,
  readCachedSessionTurns,
  upsertTurnInfo,
  upsertTurnPart,
} from "../store/message-lookup";
import type { MessageInfo, MessagePart } from "../services/sse-types";
import type { MessageTurn } from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

const sid = "ses_1";
const info = (id: string, role: "user" | "assistant", completed?: number): MessageInfo =>
  ({ id, sessionID: sid, role, time: completed ? { created: 1, completed } : { created: 1 } }) as MessageInfo;
const text = (id: string, messageID: string, value: string): MessagePart => ({
  id, sessionID: sid, messageID, type: "text", text: value,
});
const tool = (id: string, messageID: string, status: "running" | "completed"): MessagePart =>
  ({
    id, sessionID: sid, messageID, type: "tool", callID: id, tool: "bash",
    state: status === "running"
      ? { status, input: {}, time: { start: 1 } }
      : { status, input: {}, output: "ok", title: "", metadata: {}, time: { start: 1, end: 2 } },
  }) as MessagePart;

describe("REST snapshot merge", () => {
  it("indexes subagent parents from streamed, snapshot and oversized task parts", () => {
    const task = (id: string, state: Record<string, unknown>) =>
      ({ id, sessionID: sid, messageID: "msg_t", type: "tool", callID: id, tool: "task",
        state: { status: "completed", input: {}, title: "", metadata: {}, time: { start: 1, end: 2 }, ...state } }) as MessagePart;
    const turns: MessageTurn[] = [];
    assertEqual(lookupSubagentParent("ses_child_meta"), null);
    upsertTurnPart(turns, task("prt_meta", { metadata: { sessionId: "ses_child_meta" }, output: "done" }));
    assertEqual(lookupSubagentParent("ses_child_meta"), sid, "SSE task metadata links the child");

    const huge = "x".repeat(40 * 1024) + "\nsession_id: ses_child_tail";
    mergeServerTurnsInto(turns, [{ info: info("msg_t", "assistant"), parts: [task("prt_tail", { output: huge })] }]);
    assertEqual(lookupSubagentParent("ses_child_tail"), sid, "indexed before output compaction drops the tail");

    forgetSubagentLinks([sid]);
    assertEqual(lookupSubagentParent("ses_child_meta"), null, "deleting the parent forgets its children");
  });

  it("keeps live turn identities when an empty snapshot arrives late", () => {
    const [state, setState] = createStore<{ turns: MessageTurn[] }>({
      turns: [
        { info: info("msg_a", "user"), parts: [text("prt_u", "msg_a", "hi")] },
        { info: info("msg_b", "assistant"), parts: [text("prt_x", "msg_b", "hello")] },
      ],
    });
    const user = state.turns[0];
    const assistant = state.turns[1];
    const part = assistant.parts[0];
    setState(produce((s) => mergeServerTurnsInto(s.turns, [])));
    assertEqual(state.turns.length, 2, "an empty snapshot cannot erase streamed turns");
    assertTrue(state.turns[0] === user && state.turns[1] === assistant);
    assertTrue(state.turns[1].parts[0] === part, "existing text stays mounted");
    setState(produce((s) => upsertTurnPart(s.turns, text("prt_x", "msg_b", "hello world"))));
    assertTrue(state.turns[1].parts[0] === part, "streaming continues on the same part");
    assertEqual((state.turns[1].parts[0] as Extract<MessagePart, { type: "text" }>).text, "hello world");

    const empty: MessageTurn[] = [];
    mergeServerTurnsInto(empty, []);
    assertEqual(empty.length, 0, "a new empty session stays empty");
  });

  it("drops turns that predate an empty snapshot but keeps ones streamed during it", () => {
    const [state, setState] = createStore<{ turns: MessageTurn[] }>({
      turns: [{ info: info("msg_a", "user"), parts: [text("prt_u", "msg_a", "cached")] }],
    });
    const preRequestIDs = new Set(state.turns.map((t) => t.info.id));
    setState(produce((s) => upsertTurnPart(s.turns, text("prt_x", "msg_b", "live"))));
    setState(produce((s) => mergeServerTurnsInto(s.turns, [], preRequestIDs)));
    assertEqual(state.turns.length, 1, "stale cached turn is removed");
    assertEqual(state.turns[0].info.id, "msg_b", "turn streamed while REST was in flight stays");

    persistCachedSessionTurns("ses_stale_cache", [{ info: info("msg_a", "user"), parts: [] }]);
    persistCachedSessionTurns("ses_stale_cache", []);
    assertEqual(readCachedSessionTurns("ses_stale_cache"), undefined, "empty history evicts the cache seed");
  });

  it("merges initial text and reasoning snapshots that omit empty text", () => {
    for (const type of ["text", "reasoning"] as const) {
      const missing = { id: "prt_x", sessionID: sid, messageID: "msg_b", type } as MessagePart;
      const populated = { ...missing, text: "streamed content" } as MessagePart;
      const [state, setState] = createStore<{ turns: MessageTurn[] }>({
        turns: [{ info: info("msg_b", "assistant"), parts: [{ ...missing }] }],
      });
      const part = state.turns[0].parts[0];
      setState(produce((s) => mergeServerTurnsInto(s.turns, [
        { info: info("msg_b", "assistant"), parts: [populated] },
      ])));
      assertTrue(state.turns[0].parts[0] === part);
      assertEqual((part as Extract<MessagePart, { type: "text" | "reasoning" }>).text, "streamed content");
      setState(produce((s) => mergeServerTurnsInto(s.turns, [
        { info: info("msg_b", "assistant"), parts: [missing] },
      ])));
      assertEqual((part as Extract<MessagePart, { type: "text" | "reasoning" }>).text, "streamed content");
    }
  });

  it("normalizes omitted SSE and cached text without resetting streamed tokens", () => {
    for (const type of ["text", "reasoning"] as const) {
      const missing = { id: "prt_x", sessionID: sid, messageID: "msg_b", type } as MessagePart;
      const turns: MessageTurn[] = [];
      upsertTurnPart(turns, { ...missing });
      const part = turns[0].parts[0] as Extract<MessagePart, { type: "text" | "reasoning" }>;
      assertEqual(part.text, "");
      part.text = "first tokens";
      upsertTurnPart(turns, { ...missing });
      assertEqual(part.text, "first tokens");
      const cacheID = `ses_missing_${type}`;
      persistCachedSessionTurns(cacheID, [{ info: info("msg_b", "assistant"), parts: [{ ...missing }] }]);
      const cached = readCachedSessionTurns(cacheID)!;
      assertEqual((cached[0].parts[0] as typeof part).text, "");
      evictCachedSessionTurns([cacheID]);
    }
  });

  it("keeps streamed progress that is newer than a stale snapshot", () => {
    const [state, setState] = createStore<{ turns: MessageTurn[] }>({
      turns: [
        { info: info("msg_a", "user"), parts: [text("prt_u", "msg_a", "hi")] },
        {
          info: info("msg_b", "assistant", 9),
          parts: [tool("prt_t", "msg_b", "completed"), text("prt_x", "msg_b", "hello world")],
        },
      ],
    });
    const toolProxy = state.turns[1].parts[0];
    setState(produce((s) => mergeServerTurnsInto(s.turns, [
      { info: info("msg_a", "user"), parts: [text("prt_u", "msg_a", "hi")] },
      { info: info("msg_b", "assistant"), parts: [tool("prt_t", "msg_b", "running"), text("prt_x", "msg_b", "hel")] },
    ])));
    const b = state.turns[1];
    assertTrue(Boolean(b.info.time?.completed), "completed info is not regressed");
    assertEqual((b.parts[0] as any).state.status, "completed", "tool status is not regressed");
    assertEqual((b.parts[1] as any).text, "hello world", "streamed text is not truncated");
    assertTrue(b.parts[0] === toolProxy, "part identity is preserved");
  });

  it("applies snapshot progress and drops optimistic placeholders", () => {
    const [state, setState] = createStore<{ turns: MessageTurn[] }>({
      turns: [
        { info: info("msg_a", "user"), parts: [text("prt_1760000000000", "msg_a", "hi")] },
        { info: info("msg_b", "assistant"), parts: [tool("prt_t", "msg_b", "running")] },
        { info: info("msg_c", "assistant"), parts: [text("prt_s", "msg_c", "streaming")] },
      ],
    });
    setState(produce((s) => mergeServerTurnsInto(s.turns, [
      { info: info("msg_a", "user"), parts: [text("prt_01JREALID", "msg_a", "hi")] },
      { info: info("msg_b", "assistant", 5), parts: [tool("prt_t", "msg_b", "completed")] },
    ])));
    assertEqual(state.turns[0].parts.length, 1, "optimistic user part replaced");
    assertEqual(state.turns[0].parts[0].id, "prt_01JREALID");
    assertEqual((state.turns[1].parts[0] as any).state.status, "completed");
    assertEqual(state.turns.length, 3, "turn streamed after the snapshot is kept");
  });

  it("orders runner user IDs and OpenCode IDs by creation time, keeping turn identity", () => {
    const at = (id: string, role: "user" | "assistant", created: number) =>
      ({ id, sessionID: sid, role, time: { created } }) as MessageInfo;
    const [state, setState] = createStore<{ turns: MessageTurn[] }>({
      turns: [
        { info: at("msg_152d70", "assistant", 100), parts: [] },
        { info: at("optimistic_200", "user", 200), parts: [text("prt_200", "optimistic_200", "go")] },
      ],
    });
    const bubble = state.turns[1];
    // "msg_u_…" sorts after "msg_152d…" as a string, but was created earlier.
    setState(produce((s) => upsertTurnInfo(s.turns, at("msg_u_200_2", "user", 201))));
    setState(produce((s) => upsertTurnInfo(s.turns, at("msg_152d94", "assistant", 205))));
    assertEqual(state.turns.map((t) => t.info.id).join(","), "msg_152d70,msg_u_200_2,msg_152d94");
    assertTrue(state.turns[1] === bubble, "promoted bubble keeps its identity");

    // A snapshot taken before the new assistant turn must not drop it.
    const streamed = state.turns[2];
    setState(produce((s) => mergeServerTurnsInto(s.turns, [
      { info: at("msg_152d70", "assistant", 100), parts: [] },
      { info: at("msg_u_200_2", "user", 201), parts: [text("prt_real", "msg_u_200_2", "go")] },
    ])));
    assertEqual(state.turns.length, 3, "assistant streamed after the snapshot is kept");
    assertTrue(state.turns[2] === streamed, "streamed assistant keeps its identity");
  });

  it("promotes the optimistic bubble in place when the snapshot lands first", () => {
    const at = (id: string, role: "user" | "assistant", created: number) =>
      ({ id, sessionID: sid, role, time: { created } }) as MessageInfo;
    const [state, setState] = createStore<{ turns: MessageTurn[] }>({
      turns: [{ info: at("optimistic_300", "user", 300), parts: [text("prt_300", "optimistic_300", "go")] }],
    });
    const bubble = state.turns[0];
    setState(produce((s) => mergeServerTurnsInto(s.turns, [
      { info: at("msg_u_300_1", "user", 301), parts: [text("prt_real", "msg_u_300_1", "go")] },
    ])));
    assertEqual(state.turns.length, 1);
    assertEqual(state.turns[0].info.id, "msg_u_300_1");
    assertTrue(state.turns[0] === bubble, "bubble is not remounted");
  });

  it("compacts oversized completed tool outputs out of reactive store and caches session snapshots", () => {
    const hugeOutput = "L".repeat(48 * 1024);
    const bigTool: MessagePart = {
      id: "prt_big_tool",
      sessionID: sid,
      messageID: "msg_big",
      type: "tool",
      callID: "prt_big_tool",
      tool: "bash",
      state: {
        status: "completed",
        input: { command: "cat huge.log" },
        output: hugeOutput,
        title: "cat huge.log",
        metadata: {},
        time: { start: 1, end: 2 },
      },
    };
    const [state, setState] = createStore<{ turns: MessageTurn[] }>({ turns: [] });
    setState(produce((s) => mergeServerTurnsInto(s.turns, [
      { info: info("msg_big", "assistant", 2), parts: [bigTool] },
    ])));
    const storedPart = state.turns[0].parts[0] as Extract<MessagePart, { type: "tool" }>;
    assertEqual((storedPart.state as any).output.length, 32 * 1024);
    assertEqual(getFullToolOutput("prt_big_tool")?.length, 48 * 1024);

    persistCachedSessionTurns("ses_snap_test", state.turns);
    const snap = readCachedSessionTurns("ses_snap_test");
    assertTrue(Array.isArray(snap) && snap.length === 1);
    evictCachedSessionTurns(["ses_snap_test"]);
    assertEqual(readCachedSessionTurns("ses_snap_test"), undefined);
  });

  it("drops summary diff patches from persisted snapshots", () => {
    const withSummary = {
      ...info("msg_sum", "user"),
      summary: {
        diffs: [
          { file: "web/a.ts", patch: "x".repeat(50_000), additions: 3, deletions: 1 },
        ],
      },
    } as MessageInfo;
    persistCachedSessionTurns("ses_summary", [
      { info: withSummary, parts: [text("prt_s", "msg_sum", "hi")] },
    ]);
    const snap = readCachedSessionTurns("ses_summary");
    assertTrue(Array.isArray(snap) && snap.length === 1);
    assertEqual((snap![0].info as { summary?: unknown }).summary, undefined, "summary payload is not cached");
    assertEqual(snap![0].parts[0].type, "text");
    evictCachedSessionTurns(["ses_summary"]);
  });
});
