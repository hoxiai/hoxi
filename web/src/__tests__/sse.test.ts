// web/src/__tests__/sse.test.ts
import { connectEventStream, type StreamFrameBatch } from "../services/sse";
import { findMessageIndex, findPartIndex } from "../store/message-lookup";
import type { MessageTurn } from "../store/workspace";
import { createStore, produce, reconcile, unwrap } from "solid-js/store";
import type { OpenCodeEvent, SessionInfo } from "../services/sse-types";
import { setWorkspace, workspace } from "../store/workspace";
import { replaceSessionFromEvent } from "../store/workspace-sessions";
import { applyPartDeltaBatch, applyStreamFrameBatch } from "../store/workspace-session-messages";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

class MockEventSource {
  static latest: MockEventSource | null = null;
  static instances: MockEventSource[] = [];
  url: string;
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((msg: { data: string }) => void) | null = null;

  constructor(url: string) {
    this.url = url;
    MockEventSource.latest = this;
    MockEventSource.instances.push(this);
  }

  close(): void {
    this.closed = true;
  }
}

describe("SSE client & 120Hz rAF delta batching", () => {
  for (const runtime of ["claude-code", "antigravity"]) {
    for (const type of ["text", "reasoning"]) {
      for (const omittedText of [true, false]) {
        it(`streams ${runtime} ${type} before completion with ${omittedText ? "omitted" : "empty"} initial text`, () => {
          const saved = structuredClone(unwrap(workspace));
          const g = globalThis as Record<string, unknown>;
          const originals = { EventSource: g.EventSource, requestAnimationFrame: g.requestAnimationFrame,
            cancelAnimationFrame: g.cancelAnimationFrame };
          let frame: (() => void) | undefined;
          g.EventSource = MockEventSource;
          g.requestAnimationFrame = (callback: () => void) => { frame = callback; return 1; };
          g.cancelAnimationFrame = () => { frame = undefined; };
          const sid = `ses_${runtime === "claude-code" ? "cld" : "agy"}_wire_test`;
          let disconnect: (() => void) | undefined;
          try {
            setWorkspace("messagesBySession", {});
            const start = MockEventSource.instances.length;
            disconnect = connectEventStream({ onEvent: () => {},
              onFrameBatch: applyStreamFrameBatch, onDeltaBatch: applyPartDeltaBatch });
            const source = MockEventSource.instances[start];
            assertEqual(MockEventSource.instances.length, start + 1);
            const emit = (eventType: string, properties: unknown) => source.onmessage?.({
              data: JSON.stringify({ type: eventType, properties }),
            });
            const part = { id: "part", messageID: "message", sessionID: sid, type,
              ...(omittedText ? {} : { text: "" }) };
            emit("message.updated", { info: { id: "message", sessionID: sid, role: "assistant", time: { created: 1 } } });
            emit("message.part.updated", { part });
            const delta = { sessionID: sid, messageID: "message", partID: "part", field: "text", delta: "Hello" };
            emit("message.part.delta", delta);
            frame?.();
            const text = () => (workspace.messagesBySession[sid][0].parts[0] as { text: string }).text;
            assertEqual(text(), "Hello", "first frame must render before a final snapshot");
            emit("message.part.delta", { ...delta, delta: " 🌍" });
            frame?.();
            assertEqual(text(), "Hello 🌍");
            // The non-frame consumer must accept old serialized parts as well.
            applyPartDeltaBatch([{ ...delta, delta: "!" }]);
            assertEqual(text(), "Hello 🌍!");
            emit("message.part.updated", { part: { ...part, text: "Hello 🌍!" } });
            emit("session.status", { sessionID: sid, status: { type: "idle" } });
            assertEqual(text(), "Hello 🌍!", "final snapshot must not duplicate streamed text");
          } finally {
            disconnect?.();
            Object.assign(g, originals);
            setWorkspace(reconcile(saved));
          }
        });
      }
    }
  }

  it("initializes omitted text in the standalone delta consumer", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      const sid = "ses_cld_legacy_delta";
      setWorkspace("messagesBySession", { [sid]: [{
        info: { id: "m", sessionID: sid, role: "assistant", time: { created: 1 } },
        parts: [JSON.parse('{"id":"p","messageID":"m","sessionID":"ses_cld_legacy_delta","type":"text"}')],
      }] });
      applyPartDeltaBatch([{ sessionID: sid, messageID: "m", partID: "p", field: "text", delta: "first" }]);
      assertEqual((workspace.messagesBySession[sid][0].parts[0] as { text: string }).text, "first");
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  for (const [base, expected] of [
    ["ws://127.0.0.1:42817", "http://127.0.0.1:42817"],
    ["wss://localhost:42817/", "https://localhost:42817"],
  ]) {
    it(`uses the HTTP streaming server for ${base}`, () => {
      const g = globalThis as Record<string, unknown>;
      const original = g.EventSource;
      g.EventSource = MockEventSource;
      let disconnect: (() => void) | undefined;
      try {
        const start = MockEventSource.instances.length;
        const events: string[] = [];
        disconnect = connectEventStream({ onEvent: (evt) => events.push(evt.type), onDeltaBatch: () => {} }, undefined, base);
        const created = MockEventSource.instances.slice(start);
        assertEqual(created.length, 1);
        const [source] = created;
        assertEqual(source.url, `${expected}/api/hoxi/event`);
        source.onmessage?.({ data: JSON.stringify({ type: "session.idle", properties: { sessionID: "ses_agy_test" } }) });
        assertDeepEqual(events, ["session.idle"]);
        disconnect();
        assertTrue(source.closed);
      } finally {
        disconnect?.();
        g.EventSource = original;
      }
    });
  }

  for (const type of ["session.created", "session.updated"] as const) {
    it(`isolates ${type} list admission without filtering global background events`, () => {
      const saved = structuredClone(unwrap(workspace));
      const g = globalThis as Record<string, unknown>;
      const originals = { EventSource: g.EventSource, requestAnimationFrame: g.requestAnimationFrame,
        cancelAnimationFrame: g.cancelAnimationFrame };
      g.EventSource = MockEventSource;
      g.requestAnimationFrame = () => 1;
      g.cancelAnimationFrame = () => {};
      let disconnect: (() => void) | undefined;
      const session = (id: string, directory?: string, extra?: Partial<SessionInfo>): SessionInfo => ({
        id, directory, title: "Original", time: { created: 1, updated: 1 }, ...extra,
      });
      try {
        setWorkspace({ rootDir: "/new/", sessions: [
          session("existing", "/new"), session("parent", "/new/.hoxi/worktrees/parent"),
          session("external", "/new"), session("stale", "/old"),
        ], worktreeBySession: {} });
        setWorkspace("worktreeBySession", "external", {
          sessionID: "external", directory: "/external/worktree", isolated: true, createdAt: "",
        });
        const start = MockEventSource.instances.length;
        const events: string[] = [];
        const batches: StreamFrameBatch[] = [];
        disconnect = connectEventStream({
          onEvent: (evt, directory) => {
            events.push(evt.type);
            if (evt.type === "session.created" || evt.type === "session.updated") {
              replaceSessionFromEvent(evt.properties.info, directory);
            }
          },
          onFrameBatch: (batch) => batches.push(batch), onDeltaBatch: () => {},
        });
        const created = MockEventSource.instances.slice(start);
        assertEqual(created.length, 1);
        const [source] = created;
        assertEqual(source.url, "/api/hoxi/event");
        const emit = (info: SessionInfo, directory?: string, _runnerEvent = false) => {
          const payload = { type, properties: { info } };
          source.onmessage?.({
            data: JSON.stringify(directory === undefined ? payload : { directory, payload }),
          });
        };
        emit(session("foreign", "/old"), "/old");
        emit(session("foreign-envelope"), "/old");
        emit(session("foreign-worktree", "/old/.hoxi/worktrees/runner"), undefined, true);
        emit(session("nested", "/new/another-project"));
        emit(session("prefix", "/new-other"));
        emit(session("unknown"));
        emit(session("current", "/new///"), "/old");
        emit(session("envelope"), "/new");
        emit(session("worktree", "/new/.hoxi/worktrees/other"));
        emit(session("runner", "/new/.hoxi/worktrees/runner", { runtime: "claude-code" }), undefined, true);
        emit(session("child", undefined, { parentID: "parent" }));
        emit(session("foreign-child", "/old", { parentID: "parent" }));
        emit(session("orphan", undefined, { parentID: "missing" }));
        emit(session("existing", undefined, { title: "Updated", time: { created: 1, updated: 9 } }));
        emit(session("external", "/external/worktree", { title: "External updated" }));
        emit(session("stale", undefined, { title: "Must not update" }));
        assertDeepEqual(workspace.sessions.map((s) => s.id).sort(), [
          "child", "current", "envelope", "existing", "external", "parent", "runner", "stale", "worktree",
        ].sort());
        assertEqual(workspace.sessions.find((s) => s.id === "existing")?.title, "Updated");
        assertEqual(workspace.sessions.find((s) => s.id === "existing")?.time.updated, 9);
        assertEqual(workspace.sessions.find((s) => s.id === "existing")?.directory, "/new");
        assertEqual(workspace.sessions.find((s) => s.id === "envelope")?.directory, "/new");
        assertEqual(workspace.sessions.find((s) => s.id === "external")?.title, "External updated");
        assertEqual(workspace.sessions.find((s) => s.id === "stale")?.title, "Original");
        source.onmessage?.({ data: JSON.stringify({ directory: "/old", payload: {
          type: "message.updated", properties: { info: {
            id: "background-message", sessionID: "foreign", role: "assistant", time: { created: 1 },
          } },
        } }) });
        source.onmessage?.({ data: JSON.stringify({ directory: "/old", payload: {
          type: "session.status", properties: { sessionID: "foreign", status: { type: "busy" } },
        } }) });
        assertEqual(batches[0].infos[0].sessionID, "foreign");
        assertEqual(events.at(-1), "session.status");
        // A new workspace must not be repopulated by late old-root events.
        setWorkspace({ rootDir: "/empty", sessions: [] });
        emit(session("late", "/new"));
        emit(session("runner", "/new/.hoxi/worktrees/runner"), undefined, true);
        assertEqual(workspace.sessions.length, 0);
        setWorkspace("rootDir", "");
        emit(session("no-root", "/new"));
        assertEqual(workspace.sessions.length, 0);
      } finally {
        disconnect?.();
        Object.assign(g, originals);
        setWorkspace(reconcile(saved));
      }
    });
  }

  it("drains a suspended rAF, preserves snapshot/delta ordering and cancels on disconnect", () => {
    const g = globalThis as Record<string, unknown>;
    const originals = { EventSource: g.EventSource, requestAnimationFrame: g.requestAnimationFrame,
      cancelAnimationFrame: g.cancelAnimationFrame, setTimeout: g.setTimeout, clearTimeout: g.clearTimeout };
    let frame: (() => void) | null = null;
    let timer: (() => void) | null = null;
    g.EventSource = MockEventSource;
    g.requestAnimationFrame = (cb: () => void) => { frame = cb; return 1; };
    g.cancelAnimationFrame = () => { frame = null; };
    // Only the flush timer is modelled; the 30s heartbeat watchdog is ignored.
    g.setTimeout = (cb: () => void, ms?: number) => {
      if (ms === 30_000) return 2;
      timer = cb;
      return 1;
    };
    g.clearTimeout = (id?: number) => { if (id !== 2) timer = null; };
    try {
      const batches: StreamFrameBatch[] = [];
      const order: string[] = [];
      const disconnect = connectEventStream({
        onFrameBatch: (batch) => { batches.push(batch); order.push("frame"); },
        onEvent: (event) => order.push(event.type), onDeltaBatch: () => {},
      });
      const emit = (type: string, properties: unknown) => MockEventSource.latest!.onmessage?.({
        data: JSON.stringify({ type, properties }),
      });
      const delta = (text: string) => emit("message.part.delta", {
        sessionID: "s", messageID: "m", partID: "p", field: "text", delta: text,
      });
      const snapshot = (text: string) => emit("message.part.updated", { part: {
        sessionID: "s", messageID: "m", id: "p", type: "text", text,
      } });
      snapshot("Hello");
      delta(" world");
      assertEqual(batches.length, 0);
      (timer as (() => void) | null)?.();
      assertEqual(batches.length, 1);
      assertEqual(batches[0].deltas[0].delta, " world");
      assertEqual(frame, null);
      delta("superseded");
      snapshot("");
      delta("after reset");
      emit("session.status", { sessionID: "s", status: { type: "idle" } });
      assertEqual(batches[1].deltas[0].delta, "after reset");
      assertDeepEqual(order, ["frame", "frame", "session.status"]);
      assertEqual(timer, null);
      delta("discarded on close");
      disconnect();
      assertEqual(timer, null);
      assertEqual(frame, null);
      assertEqual(batches.length, 2);
    } finally {
      Object.assign(g, originals);
    }
  });

  it("invalidates message and part lookups after same-length replacement and removal", () => {
    const turn = (id: string): MessageTurn => ({
      info: { id, sessionID: "s", role: "assistant", time: { created: 1 } },
      parts: [{ id: `p_${id}`, sessionID: "s", messageID: id, type: "text", text: "" }],
    });
    const turns = [turn("a"), turn("b")];
    assertEqual(findMessageIndex(turns, "b"), 1);
    turns.reverse();
    assertEqual(findMessageIndex(turns, "b"), 0);
    const parts = turns[0].parts;
    assertEqual(findPartIndex(parts, "p_b"), 0);
    parts[0] = turn("c").parts[0];
    assertEqual(findPartIndex(parts, "p_b"), -1);
    assertEqual(findPartIndex(parts, "p_c"), 0);
    turns.splice(0, 1);
    assertEqual(findMessageIndex(turns, "a"), 0);
    assertEqual(findMessageIndex(turns, "b"), -1);
  });

  it("reuses lookups across Solid produce calls and validates replaced store entries", () => {
    let reads = 0;
    const turns: MessageTurn[] = Array.from({ length: 100 }, (_, index) => ({
      info: { get id() { reads++; return `m_${index}`; }, sessionID: "s",
        role: "assistant", time: { created: 1 } },
      parts: [{ id: `p_${index}`, sessionID: "s", messageID: `m_${index}`, type: "text", text: "" }],
    }));
    const [state, setState] = createStore({ turns });
    const append = () => setState(produce((draft) => {
      const turn = draft.turns[findMessageIndex(draft.turns, "m_99")];
      const part = turn.parts[findPartIndex(turn.parts, "p_99")];
      if (part.type === "text") part.text += "x";
    }));
    append();
    reads = 0;
    append();
    assertTrue(reads < 10, "warm lookup should validate its position without scanning history");
    const part = state.turns[99].parts[0];
    assertEqual(part.type === "text" ? part.text : "", "xx");
    setState("turns", 99, { info: { id: "replacement", sessionID: "s", role: "assistant",
      time: { created: 1 } }, parts: [] });
    setState(produce((draft) => {
      assertEqual(findMessageIndex(draft.turns, "m_99"), -1);
      assertEqual(findMessageIndex(draft.turns, "replacement"), 99);
    }));
  });

  it("unwraps GlobalEvent envelopes and coalesces rapid message.part.delta frames per rAF", () => {
    const g = globalThis as Record<string, unknown>;
    const origES = g.EventSource;
    const origRAF = g.requestAnimationFrame;
    const origCRAF = g.cancelAnimationFrame;

    let pendingRAF: (() => void) | null = null;
    g.EventSource = MockEventSource;
    g.requestAnimationFrame = (cb: () => void) => {
      pendingRAF = cb;
      return 1;
    };
    g.cancelAnimationFrame = () => {
      pendingRAF = null;
    };

    try {
      const events: OpenCodeEvent[] = [];
      const deltaBatches: Array<
        Array<{ sessionID: string; messageID: string; partID: string; field: string; delta: string }>
      > = [];
      let connectedState = false;

      const instanceStart = MockEventSource.instances.length;
      const disconnect = connectEventStream(
        {
          onEvent: (evt) => events.push(evt),
          onDeltaBatch: (batch) => deltaBatches.push(batch),
          onConnectionChange: (c) => {
            connectedState = c;
          },
        },
        "/tmp/worktree-1"
      );

      const created = MockEventSource.instances.slice(instanceStart);
      assertEqual(created.length, 1);
      const [src] = created;
      assertEqual(src.url, "/api/hoxi/event?directory=%2Ftmp%2Fworktree-1");

      src.onopen?.();
      assertEqual(connectedState, true);

      // 1. Wrapped GlobalEvent payload
      src.onmessage?.({
        data: JSON.stringify({
          directory: "/tmp/worktree-1",
          payload: {
            type: "session.status",
            properties: { sessionID: "ses_1", status: { type: "busy" } },
          },
        }),
      });
      assertEqual(events.length, 1);
      assertEqual(events[0].type, "session.status");

      // 2. Multiple high-frequency message.part.delta frames within the same animation frame
      for (const token of ["Hello", ", ", "Hoxi!"]) {
        src.onmessage?.({
          data: JSON.stringify({
            type: "message.part.delta",
            properties: {
              sessionID: "ses_1",
              messageID: "msg_1",
              partID: "part_1",
              field: "text",
              delta: token,
            },
          }),
        });
      }

      // Before rAF fires, no batch should be emitted yet
      assertEqual(deltaBatches.length, 0);
      assertTrue(pendingRAF !== null, "rAF callback should be scheduled");

      // Trigger rAF flush
      const flush = pendingRAF as (() => void) | null;
      flush?.();
      assertEqual(deltaBatches.length, 1);
      assertDeepEqual(deltaBatches[0], [
        {
          sessionID: "ses_1",
          messageID: "msg_1",
          partID: "part_1",
          field: "text",
          delta: "Hello, Hoxi!",
        },
      ]);

      disconnect();
      assertEqual(src.closed, true);
    } finally {
      g.EventSource = origES;
      g.requestAnimationFrame = origRAF;
      g.cancelAnimationFrame = origCRAF;
    }
  });

  it("auto-opens Browser Inspector tab when applyStreamFrameBatch receives a running hoxi_browser tool part for the active session", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      const sid = "ses_browser_auto_open";
      setWorkspace({
        activeSessionID: sid,
        inspectorOpen: false,
        inspectorTab: "diffs",
        messagesBySession: {},
      });
      applyStreamFrameBatch({
        infos: [{ id: "m1", sessionID: sid, role: "assistant", time: { created: 1 } }],
        parts: [
          {
            id: "p1",
            messageID: "m1",
            sessionID: sid,
            type: "tool",
            tool: "hoxi_browser",
            callID: "c1",
            state: { status: "running", input: { action: "navigate" }, time: { start: 1 } },
          },
        ],
        deltas: [],
      });
      assertEqual(workspace.inspectorOpen, true);
      assertEqual(workspace.inspectorTab, "browser");
    } finally {
      setWorkspace(reconcile(saved));
    }
  });
});

