// web/src/__tests__/merge-turns.test.ts
import { createStore, produce } from "solid-js/store";
import { mergeServerTurnsInto, upsertTurnInfo } from "../store/message-lookup";
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
});
