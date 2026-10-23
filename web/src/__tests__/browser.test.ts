import { browserAPI, browserPoint, browserStreamURL, browserURL, connectBrowserStream } from "../services/browser";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

describe("Browser inspector", () => {
  it("creates a Chrome connection code only through the scoped UI action", async () => {
    const original = globalThis.fetch;
    const scope = { sessionID: "ses_chrome", directory: "/work trees/chrome" };
    const signal = new AbortController().signal;
    try {
      globalThis.fetch = async (url, init) => {
        assertEqual(String(url), browserURL("action", scope));
        assertEqual(init?.method, "POST");
        assertEqual(init?.signal, signal);
        assertEqual(init?.body, JSON.stringify({ action: "pair" }));
        assertEqual((init?.headers as Record<string, string>)["X-Hoxi-Browser"], "true");
        return new Response(JSON.stringify({ code: "one-use-code", expiresIn: 120 }));
      };
      assertDeepEqual(await browserAPI.pair(scope, signal), { code: "one-use-code", expiresIn: 120 });
    } finally { globalThis.fetch = original; }
  });
  it("encodes session and worktree scope on every endpoint", () => {
    const scope = { sessionID: "session &?", directory: "/work trees/\u4e2d\u6587?&" };
    for (const endpoint of ["state", "frame", "action", "stream"] as const) {
      const url = new URL(browserURL(endpoint, scope), "http://localhost");
      assertEqual(url.pathname, `/api/hoxi/browser/${endpoint}`);
      assertEqual(url.searchParams.get("sessionID"), scope.sessionID);
      assertEqual(url.searchParams.get("directory"), scope.directory);
    }
  });

  it("uses the loopback WebSocket bridge and ACKs only after display finishes", async () => {
    const original = globalThis.WebSocket;
    const sockets: MockSocket[] = [];
    class MockSocket {
      static OPEN = 1;
      readyState = 1;
      onmessage: ((event: { data: string }) => Promise<void>) | null = null;
      onerror: (() => void) | null = null;
      onclose: (() => void) | null = null;
      sent: string[] = [];
      closed = false;
      constructor(public url: string) { sockets.push(this); }
      send(text: string) { this.sent.push(text); }
      close() { this.closed = true; this.readyState = 3; this.onclose?.(); }
    }
    globalThis.WebSocket = MockSocket as unknown as typeof WebSocket;
    let stop = () => {};
    try {
      const scope = { sessionID: "ses_test", directory: "/work tree" };
      let displayed = 0;
      let finish = () => {};
      stop = connectBrowserStream(scope, "ws://127.0.0.1:42817/", async frame => {
        assertEqual(frame.width, 375);
        displayed++;
        await new Promise<void>(resolve => { finish = resolve; });
      }, error => { throw new Error(error); });
      const socket = sockets[0];
      assertEqual(socket.url, browserStreamURL(scope, "ws://127.0.0.1:42817"));
      const message = JSON.stringify({ image: "YWJj", width: 375, height: 667, tabID: "tab" });
      const pending = socket.onmessage!({ data: message });
      assertEqual(displayed, 1);
      assertDeepEqual(socket.sent, []);
      finish();
      await pending;
      assertDeepEqual(socket.sent, ["ack"]);
      const late = socket.onmessage!({ data: message });
      stop();
      finish();
      await late;
      assertDeepEqual(socket.sent, ["ack"]);
      assertEqual(socket.closed, true);
      await socket.onmessage!({ data: message });
      assertEqual(displayed, 2);
    } finally {
      stop();
      globalThis.WebSocket = original;
    }
  });

  it("maps scaled image bounds to the actual viewport and clamps the last pixel", () => {
    const bounds = { left: 100, top: 50, width: 640, height: 400 };
    const viewport = { width: 1280, height: 800 };
    assertDeepEqual(browserPoint(bounds, 420, 250, viewport), { x: 640, y: 400 });
    assertDeepEqual(browserPoint(bounds, 100, 50, viewport), { x: 0, y: 0 });
    assertDeepEqual(browserPoint(bounds, 740, 450, viewport), { x: 1279, y: 799 });
    assertEqual(browserPoint(bounds, 99, 50, viewport), null);
    assertEqual(browserPoint(bounds, 740, 451, viewport), null);
    assertEqual(browserPoint({ ...bounds, width: 0 }, 100, 50, viewport), null);
    assertDeepEqual(browserPoint(bounds, 420, 250, { width: 375, height: 700 }), { x: 187, y: 350 });
    assertDeepEqual(browserPoint(bounds, 740, 450, { width: 375, height: 700 }), { x: 374, y: 699 });
    assertEqual(browserPoint(bounds, 420, 250, { width: 0, height: 700 }), null);
  });

  it("sends same-origin JSON actions and treats missing frames as empty", async () => {
    const original = globalThis.fetch;
    const scope = { sessionID: "s1", directory: "/worktree" };
    const signal = new AbortController().signal;
    try {
      globalThis.fetch = async (url, init) => {
        assertEqual(String(url), browserURL("action", scope));
        assertEqual(init?.method, "POST");
        assertEqual(init?.signal, signal);
        assertEqual(init?.body, JSON.stringify({ action: "control", manual: true }));
        assertEqual((init?.headers as Record<string, string>)["Content-Type"], "application/json");
        assertEqual((init?.headers as Record<string, string>)["X-Hoxi-Browser"], "true");
        return new Response(JSON.stringify({ manual: true }));
      };
      assertEqual((await browserAPI.action(scope, { action: "control", manual: true }, signal)).manual, true);
      globalThis.fetch = async () => new Response(null, { status: 404 });
      assertEqual(await browserAPI.frame(scope, signal), null);
    } finally {
      globalThis.fetch = original;
    }
  });

  it("sends viewport dimensions through the scoped resize action", async () => {
    const original = globalThis.fetch;
    const scope = { sessionID: "s2", directory: "/another-worktree" };
    const signal = new AbortController().signal;
    try {
      globalThis.fetch = async (url, init) => {
        assertEqual(String(url), browserURL("action", scope));
        assertEqual(init?.method, "POST");
        assertEqual(init?.signal, signal);
        assertEqual(init?.body, JSON.stringify({ action: "resize", width: 375, height: 667 }));
        return new Response(JSON.stringify({ tabs: [], manual: false }));
      };
      assertDeepEqual((await browserAPI.action(scope, { action: "resize", width: 375, height: 667 }, signal)).tabs, []);
    } finally {
      globalThis.fetch = original;
    }
  });
});
