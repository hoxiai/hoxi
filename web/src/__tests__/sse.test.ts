// web/src/__tests__/sse.test.ts
import { connectEventStream } from "../services/sse";
import type { OpenCodeEvent } from "../services/sse-types";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

class MockEventSource {
  static latest: MockEventSource | null = null;
  url: string;
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((msg: { data: string }) => void) | null = null;

  constructor(url: string) {
    this.url = url;
    MockEventSource.latest = this;
  }

  close(): void {
    this.closed = true;
  }
}

describe("SSE client & 120Hz rAF delta batching", () => {
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

      const src = MockEventSource.latest!;
      assertTrue(src.url.includes("directory=%2Ftmp%2Fworktree-1"), "url should include encoded directory");

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
});
