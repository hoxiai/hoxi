import { startTerminalConnection } from "../components/terminal/terminal-connection";
import { trackWebglContexts } from "../components/terminal/webgl-context";
import { hoxiAPI } from "../services/api";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function fixture(directory = "/repo/worktree A") {
  const pending = deferred<{ id: string }>();
  const deleted: string[][] = [];
  const resized: unknown[][] = [];
  const connected: string[][] = [];
  const writes: unknown[] = [];
  const errors: string[] = [];
  let ready = 0;
  let disposed = 0;
  let closed = 0;
  const sent: string[] = [];
  const socket = {
    readyState: 1,
    onopen: null, onmessage: null, onerror: null, onclose: null,
    close() { closed++; },
    send(chunk: string) { sent.push(chunk); },
  } as unknown as WebSocket;
  const options = {
    directory,
    api: {
      createPTY: async (dir: string) => {
        assertEqual(dir, directory);
        return pending.promise;
      },
      deletePTY: async (id: string, dir: string) => { deleted.push([id, dir]); },
      resizePTY: async (id: string, cols: number, rows: number, dir: string) => {
        resized.push([id, cols, rows, dir]);
      },
    },
    connect: (id: string, dir: string) => { connected.push([id, dir]); return socket; },
    size: () => ({ cols: 80, rows: 24 }),
    write: (data: unknown) => { writes.push(data); },
    ready: () => { ready++; },
    error: (reason: string) => { errors.push(reason); },
    disposeView: () => { disposed++; },
  };
  return {
    pending, deleted, resized, connected, writes, errors, socket, options, sent,
    counts: () => ({ ready, disposed, closed }),
  };
}

const settle = async () => { for (let i = 0; i < 5; i++) await Promise.resolve(); };
function event(socket: WebSocket, name: "onopen" | "onclose" | "onerror", value = {}) {
  (socket[name] as ((value: unknown) => void) | null)?.(value);
}

describe("terminal lifecycle", () => {
  it("cleans a PTY allocated after unmount without opening a socket", async () => {
    const f = fixture();
    const connection = startTerminalConnection(f.options);
    connection.dispose();
    connection.dispose();
    f.pending.resolve({ id: "late" });
    await settle();
    assertDeepEqual(f.deleted, [["late", f.options.directory]]);
    assertEqual(f.connected.length, 0);
    assertEqual(f.counts().disposed, 1);
  });

  it("isolates rapid switches/reconnects even when creates finish out of order", async () => {
    const old = fixture("/repo/old");
    const next = fixture("/repo/new");
    const a = startTerminalConnection(old.options);
    a.dispose();
    const b = startTerminalConnection(next.options);
    next.pending.resolve({ id: "new" });
    await settle();
    event(next.socket, "onopen");
    old.pending.resolve({ id: "old" });
    await settle();
    assertDeepEqual(old.deleted, [["old", "/repo/old"]]);
    assertEqual(old.counts().ready, 0);
    assertDeepEqual(next.resized, [["new", 80, 24, "/repo/new"]]);
    b.dispose();
    assertDeepEqual(next.deleted, [["new", "/repo/new"]]);
  });

  it("ignores queued socket callbacks and input/resize after disposal", async () => {
    const f = fixture();
    const c = startTerminalConnection(f.options);
    f.pending.resolve({ id: "pty" });
    await settle();
    const open = f.socket.onopen!;
    const message = f.socket.onmessage!;
    const error = f.socket.onerror!;
    const close = f.socket.onclose!;
    c.send("before");
    c.dispose();
    open.call(f.socket, {} as Event);
    message.call(f.socket, { data: "stale" } as MessageEvent);
    error.call(f.socket, {} as Event);
    close.call(f.socket, {} as CloseEvent);
    c.send("after");
    c.resize();
    assertDeepEqual(f.sent, ["before"]);
    assertDeepEqual(f.writes, []);
    assertDeepEqual(f.errors, []);
    assertDeepEqual(f.resized, []);
    assertDeepEqual(f.counts(), { ready: 0, disposed: 1, closed: 1 });
  });

  it("reconnects in the same directory without retaining the previous PTY", async () => {
    const old = fixture();
    const a = startTerminalConnection(old.options);
    old.pending.resolve({ id: "first" });
    await settle();
    event(old.socket, "onopen");
    a.dispose();
    const next = fixture();
    const b = startTerminalConnection(next.options);
    next.pending.resolve({ id: "second" });
    await settle();
    event(next.socket, "onopen");
    assertDeepEqual(old.deleted, [["first", old.options.directory]]);
    assertEqual(old.counts().closed, 1);
    assertEqual(next.counts().ready, 1);
    assertEqual(next.deleted.length, 0);
    b.dispose();
  });

  for (const failure of ["onclose", "onerror"] as const) {
    it(`cleans resources and exposes a retryable error on ${failure}`, async () => {
      const f = fixture();
      const c = startTerminalConnection(f.options);
      f.pending.resolve({ id: "pty" });
      await settle();
      event(f.socket, failure);
      c.dispose();
      assertDeepEqual(f.errors, [failure === "onclose" ? "disconnected" : "error"]);
      assertEqual(f.deleted.length, 1);
      assertEqual(f.counts().disposed, 1);
    });
  }

  it("handles failed create and failed WebSocket construction", async () => {
    const f = fixture();
    startTerminalConnection(f.options);
    f.pending.reject(new Error("create failed"));
    await settle();
    assertDeepEqual(f.errors, ["error"]);
    assertEqual(f.deleted.length, 0);
    assertEqual(f.counts().disposed, 1);

    const g = fixture();
    startTerminalConnection({ ...g.options, connect: () => { throw new Error("socket failed"); } });
    g.pending.resolve({ id: "allocated" });
    await settle();
    assertDeepEqual(g.deleted, [["allocated", g.options.directory]]);
    assertEqual(g.counts().disposed, 1);
  });

  it("ignores a late create rejection after unmount and tolerates failed deletion", async () => {
    const f = fixture();
    const c = startTerminalConnection(f.options);
    c.dispose();
    f.pending.reject(new Error("late failure"));
    await settle();
    assertDeepEqual(f.errors, []);
    const g = fixture();
    const d = startTerminalConnection({ ...g.options, api: {
      ...g.options.api, deletePTY: async () => { throw new Error("already gone"); },
    } });
    g.pending.resolve({ id: "gone" });
    await settle();
    d.dispose();
    await settle();
    assertEqual(g.counts().disposed, 1);
  });

  it("scopes the server DELETE to the captured directory and encodes the PTY ID", async () => {
    const saved = globalThis.fetch;
    let request: unknown;
    globalThis.fetch = async (url, init) => {
      request = [url, init?.method];
      return new Response(null, { status: 204 });
    };
    try {
      await hoxiAPI.deletePTY("pty/id", "/repo/worktree A");
      assertDeepEqual(request, ["/pty/pty%2Fid?directory=%2Frepo%2Fworktree%20A", "DELETE"]);
    } finally {
      globalThis.fetch = saved;
    }
  });
});

describe("terminal WebGL context release", () => {
  function fakeCanvas(kind: "webgl2" | "2d") {
    let lost = 0;
    const gl = {
      isContextLost: () => lost > 0,
      getExtension: (name: string) => (name === "WEBGL_lose_context" ? { loseContext: () => { lost++; } } : null),
    };
    const canvas = {
      getContext: (type: string) => (type === kind && kind === "webgl2" ? gl : null),
    };
    return { canvas, lost: () => lost };
  }

  it("loses only the contexts the addon added, once", () => {
    const existing = fakeCanvas("webgl2");
    const added = fakeCanvas("webgl2");
    const linkLayer = fakeCanvas("2d");
    const canvases: unknown[] = [existing.canvas];
    const root = { querySelectorAll: () => canvases.slice() } as unknown as ParentNode;
    const release = trackWebglContexts(root, () => { canvases.push(added.canvas, linkLayer.canvas); });
    assertEqual(added.lost(), 0);
    release();
    release();
    assertEqual(added.lost(), 1);
    assertEqual(existing.lost(), 0);
  });

  it("does nothing when the addon added no WebGL canvas", () => {
    const root = { querySelectorAll: () => [] } as unknown as ParentNode;
    const release = trackWebglContexts(root, () => {});
    release();
  });
});
