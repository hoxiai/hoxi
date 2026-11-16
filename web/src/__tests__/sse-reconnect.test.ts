// web/src/__tests__/sse-reconnect.test.ts
import { connectEventStream } from "../services/sse";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readyState = 0;
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((msg: { data: string }) => void) | null = null;

  constructor(public url: string) {
    FakeEventSource.instances.push(this);
  }

  close(): void {
    this.closed = true;
    this.readyState = 2;
  }

  emit(type: string): void {
    this.onmessage?.({ data: JSON.stringify({ type, properties: {} }) });
  }
}

type Timer = { at: number; fn: () => void };

// Runs the stream against fake EventSource and timers; `advance` moves the clock.
function withFakes(body: (advance: (ms: number) => void) => void): void {
  const g = globalThis as Record<string, unknown>;
  const originals = { EventSource: g.EventSource, setTimeout: g.setTimeout, clearTimeout: g.clearTimeout };
  const timers = new Map<number, Timer>();
  let now = 0;
  let seq = 0;
  g.EventSource = FakeEventSource;
  g.setTimeout = (fn: () => void, ms = 0) => {
    timers.set(++seq, { at: now + ms, fn });
    return seq;
  };
  g.clearTimeout = (id: number) => {
    timers.delete(id);
  };
  const advance = (ms: number) => {
    const end = now + ms;
    for (;;) {
      const due = [...timers.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
      if (!due) break;
      timers.delete(due[0]);
      now = due[1].at;
      due[1].fn();
    }
    now = end;
  };
  FakeEventSource.instances = [];
  try {
    body(advance);
  } finally {
    Object.assign(g, originals);
  }
}

describe("SSE client heartbeat watchdog & resync", () => {
  it("replaces a silent connection and reports the drop", () => {
    withFakes((advance) => {
      const changes: boolean[] = [];
      const stop = connectEventStream({
        onEvent: () => {},
        onDeltaBatch: () => {},
        onConnectionChange: (c) => changes.push(c),
      });
      const first = FakeEventSource.instances[0];
      first.onopen?.();
      advance(20_000);
      first.emit("server.heartbeat");
      advance(20_000);
      assertEqual(FakeEventSource.instances.length, 1, "heartbeats keep the connection");
      advance(15_000);
      assertTrue(first.closed, "a silent connection must be closed");
      advance(300);
      assertEqual(FakeEventSource.instances.length, 2, "a fresh connection must be opened");
      FakeEventSource.instances[1].onopen?.();
      assertEqual(changes.join(","), "true,false,true");
      stop();
      assertTrue(FakeEventSource.instances[1].closed);
    });
  });

  it("retries itself once EventSource gives up, with backoff", () => {
    withFakes((advance) => {
      const stop = connectEventStream({ onEvent: () => {}, onDeltaBatch: () => {} });
      for (const expected of [2, 3, 4]) {
        const latest = FakeEventSource.instances[FakeEventSource.instances.length - 1];
        latest.readyState = 2;
        latest.onerror?.();
        advance(5_000);
        assertEqual(FakeEventSource.instances.length, expected);
      }
      stop();
      advance(60_000);
      assertEqual(FakeEventSource.instances.length, 4, "no reconnect after disposal");
    });
  });

  it("routes resync and swallows heartbeats", () => {
    withFakes(() => {
      const seen: string[] = [];
      let resyncs = 0;
      const stop = connectEventStream({
        onEvent: (evt) => seen.push(evt.type),
        onDeltaBatch: () => {},
        onResync: () => resyncs++,
      });
      const source = FakeEventSource.instances[0];
      source.emit("server.heartbeat");
      source.emit("hoxi.stream.resync");
      source.emit("session.idle");
      assertEqual(resyncs, 1);
      assertEqual(seen.join(","), "session.idle");
      stop();
    });
  });
});
