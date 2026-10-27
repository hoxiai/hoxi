import { test } from "node:test";
import assert from "node:assert/strict";

test("worker confines commands to the selected tab and detaches without closing it", async () => {
  const oldChrome = globalThis.chrome, oldSocket = globalThis.WebSocket;
  let onMessage, onDetach;
  const commands = [], detached = [], sockets = [];
  globalThis.chrome = {
    runtime: { id: "hoxi-test", getURL: path => `chrome-extension://hoxi-test/${path}`,
      onMessage: { addListener: fn => { onMessage = fn; } } },
    debugger: {
      attach: async target => { assert.equal(target.tabId, 42); },
      detach: async target => { detached.push(target.tabId); },
      onDetach: { addListener: fn => { onDetach = fn; } },
      sendCommand: async (target, method, params) => {
        assert.equal(target.tabId, 42);
        commands.push({ method, params });
        if (method === "Runtime.evaluate") return { result: { value: { url: "https://example.com", title: "Signed in" } } };
        return {};
      },
    },
    tabs: { get: async id => { assert.equal(id, 42); return { id, title: "Signed in", url: "https://example.com" }; } },
  };
  let hello;
  class Socket {
    static OPEN = 1;
    readyState = 1;
    sent = [];
    constructor(url) {
      this.url = url;
      sockets.push(this);
      setTimeout(() => this.onopen(), 0);
    }
    send(text) {
      const data = JSON.parse(text);
      if (data.token) hello = data;
      if (data.token) queueMicrotask(() => this.onmessage({ data: '{"connected":true}' }));
      else this.sent.push(data);
    }
    close() { if (this.readyState !== 3) { this.readyState = 3; this.onclose?.(); } }
  }
  globalThis.WebSocket = Socket;
  const sender = { id: "hoxi-test", url: "chrome-extension://hoxi-test/popup.html" };
  const message = data => new Promise(resolve => onMessage(data, sender, resolve));
  try {
    await import(`./worker.js?test=${Date.now()}`);
    assert.equal(onMessage({ type: "connect" }, { ...sender, url: "https://example.com" }, () => assert.fail()), false);
    assert.equal(onMessage({ type: "connect" }, { ...sender, id: "other" }, () => assert.fail()), false);
    const token = "a".repeat(64);
    const code = Buffer.from(`http://127.0.0.1:5420/api/hoxi/browser/extension#${token}`).toString("base64url");
    assert.deepEqual(await message({ type: "connect", code, tabID: 42 }), { ok: true });
    const socket = sockets[0];
    async function action(input) {
      const count = socket.sent.length;
      socket.onmessage({ data: JSON.stringify(input) });
      for (let i = 0; i < 20 && socket.sent.length === count; i++) await new Promise(resolve => setTimeout(resolve, 1));
      assert.equal(socket.sent.length, count + 1);
      return socket.sent.at(-1);
    }
    assert.equal((await action({ action: "state" })).result.tabs.length, 1);
    assert.match((await action({ action: "tab_select", tabID: "99" })).error, /only the selected tab/);
    assert.match((await action({ action: "tab_close", tabID: "42" })).error, /only the selected tab/);
    assert.match((await action({ action: "navigate", url: "file:///private.txt" })).error, /HTTP/);
    assert.equal(commands.filter(c => c.method !== "Runtime.evaluate").length, 0);
    await action({ action: "navigate", url: "https://example.com/account" });
    await action({ action: "type", text: "hello" });
    await action({ action: "key", key: "Control++" });
    assert.ok(commands.some(c => c.method === "Input.insertText" && c.params.text === "hello"));
    assert.ok(commands.some(c => c.method === "Input.dispatchKeyEvent" && c.params.key === "+"));
    assert.deepEqual(hello.features, ["pointer", "zoom"]);
    await action({ action: "zoom", x: 20, y: 30, scaleFactor: 1.5 });
    assert.deepEqual(commands.filter(c => c.method === "Input.synthesizePinchGesture").at(-1).params,
      { x: 20, y: 30, scaleFactor: 1.5, gestureSourceType: "mouse" });
    const zoomCount = commands.length;
    for (const bad of [{ x: -1 }, { y: 20000 }, { scaleFactor: 0 }, { scaleFactor: 5 }, { scaleFactor: "2" }]) {
      assert.match((await action({ action: "zoom", x: 20, y: 30, scaleFactor: 1.5, ...bad })).error, /Invalid zoom/);
    }
    assert.equal(commands.length, zoomCount);
    const press = { action: "pointer", type: "mousePressed", x: 10, y: 20, button: "right", buttons: 2, clickCount: 1, modifiers: 8 };
    assert.equal((await action(press)).result.tabs.length, 1);
    assert.deepEqual(commands.filter(c => c.method === "Input.dispatchMouseEvent").at(-1).params,
      { type: "mousePressed", x: 10, y: 20, button: "right", buttons: 2, clickCount: 1, modifiers: 8 });
    const dispatched = commands.length;
    for (const bad of [{ type: "mouseWheel" }, { button: "back" }, { x: -1 }, { y: Number.MAX_VALUE }, { buttons: 8 },
      { clickCount: 4 }, { modifiers: 16 }, { buttons: 1.5 }, { x: "10" }]) {
      assert.match((await action({ ...press, ...bad })).error, /Invalid pointer/, JSON.stringify(bad));
    }
    assert.equal(commands.length, dispatched);
    assert.deepEqual(await message({ type: "disconnect" }), { ok: true });
    assert.deepEqual(detached, [42]);
    assert.equal(socket.readyState, 3);
    onDetach({ tabId: 42 });
    assert.deepEqual(detached, [42]);
  } finally {
    await message({ type: "disconnect" });
    globalThis.chrome = oldChrome;
    globalThis.WebSocket = oldSocket;
  }
});
