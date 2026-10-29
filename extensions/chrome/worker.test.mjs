import { test } from "node:test";
import assert from "node:assert/strict";

test("worker confines commands to the selected tab and detaches without closing it", async () => {
  const oldChrome = globalThis.chrome, oldSocket = globalThis.WebSocket;
  let onMessage, onDetach;
  const commands = [], detached = [], sockets = [];
  let finishSlow;
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
        if (method === "Page.navigate" && params.url === "https://example.com/slow") return new Promise(resolve => { finishSlow = resolve; });
        if (method === "Runtime.evaluate") {
          if (params.expression.includes("function browserClipboard")) return { result: { value: { clipboardText: "selected text" } } };
          return { result: { value: { url: "https://example.com", title: "Signed in" } } };
        }
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
    assert.equal(commands.filter(c => !["Runtime.evaluate", "Page.enable", "Network.enable", "Runtime.enable", "Page.getFrameTree"].includes(c.method)).length, 0);
    await action({ action: "navigate", url: "https://example.com/account" });
    await action({ action: "type", text: "hello" });
    await action({ action: "key", key: "Control++" });
    assert.ok(commands.some(c => c.method === "Input.insertText" && c.params.text === "hello"));
    assert.ok(commands.some(c => c.method === "Input.dispatchKeyEvent" && c.params.key === "+"));
    assert.deepEqual(hello.features, ["pointer", "zoom", "navigation", "clipboard", "developer"]);
    assert.equal((await action({ action: "copy" })).result.clipboardText, "selected text");
    assert.match((await action({ action: "paste", x: -1, y: 2, text: "no" })).error, /Invalid clipboard/);
    assert.match((await action({ action: "paste", x: 1, y: 2, text: "x".repeat(65537) })).error, /Invalid clipboard/);
    await action({ action: "paste", x: 1, y: 2, text: "clipboard paste" });
    assert.ok(commands.some(c => c.method === "Input.insertText" && c.params.text === "clipboard paste"));
    await action({ action: "zoom", x: 20, y: 30, scaleFactor: 1.5 });
    assert.deepEqual(commands.filter(c => c.method === "Input.synthesizePinchGesture").at(-1).params,
      { x: 20, y: 30, scaleFactor: 1.5, gestureSourceType: "mouse" });
    const zoomCount = commands.length;
    for (const bad of [{ x: -1 }, { y: 20000 }, { scaleFactor: 0 }, { scaleFactor: 5 }, { scaleFactor: "2" }]) {
      assert.match((await action({ action: "zoom", x: 20, y: 30, scaleFactor: 1.5, ...bad })).error, /Invalid zoom/);
    }
    assert.equal(commands.length, zoomCount);
    const loading = await action({ action: "navigate", url: "https://example.com/slow", async: true });
    assert.equal(loading.result.navigation.status, "loading");
    const stopped = await action({ action: "stop_loading" });
    assert.equal(stopped.result.navigation.status, "stopped");
    assert.ok(commands.some(c => c.method === "Page.stopLoading"));
    finishSlow({});
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.equal((await action({ action: "state" })).result.navigation.status, "stopped");
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

test("worker supports persistent linkToken reconnect and auto-follows child tabs", async () => {
  const oldChrome = globalThis.chrome, oldSocket = globalThis.WebSocket;
  let onMessage, onCreated;
  const attached = new Set(), removed = [], sockets = [];
  const store = {};
  const tabMeta = new Map([
    [10, { id: 10, title: "Main Tab", url: "https://example.com" }],
    [11, { id: 11, title: "Popup Tab", url: "https://example.com/popup" }],
    [12, { id: 12, title: "New Tab", url: "https://example.com/new" }],
  ]);
  globalThis.chrome = {
    runtime: { id: "hoxi-test", getURL: path => `chrome-extension://hoxi-test/${path}`,
      onMessage: { addListener: fn => { onMessage = fn; } } },
    storage: {
      local: {
        get: async key => ({ [key]: store[key] }),
        set: async obj => { Object.assign(store, obj); },
      },
    },
    debugger: {
      attach: async target => { attached.add(target.tabId); },
      detach: async target => { attached.delete(target.tabId); },
      onDetach: { addListener: () => {} },
      sendCommand: async (target, method) => {
        if (method === "Runtime.evaluate") {
          const info = tabMeta.get(target.tabId) || { url: "https://example.com", title: "Tab" };
          return { result: { value: { url: info.url, title: info.title } } };
        }
        return {};
      },
    },
    tabs: {
      get: async id => tabMeta.get(id) || { id, title: "Tab", url: "https://example.com" },
      create: async ({ url }) => ({ id: 12, title: "New Tab", url }),
      update: async () => {},
      remove: async id => { removed.push(id); },
      onCreated: { addListener: fn => { onCreated = fn; } },
    },
  };
  const persistentToken = "b".repeat(64);
  class Socket {
    static OPEN = 1;
    readyState = 1;
    sent = [];
    lastToken = "";
    constructor(url) {
      this.url = url;
      sockets.push(this);
      setTimeout(() => this.onopen(), 0);
    }
    send(text) {
      const data = JSON.parse(text);
      if (data.token) {
        this.lastToken = data.token;
        queueMicrotask(() => this.onmessage({ data: JSON.stringify({ connected: true, linkToken: persistentToken }) }));
      } else {
        this.sent.push(data);
      }
    }
    close() { if (this.readyState !== 3) { this.readyState = 3; this.onclose?.(); } }
  }
  globalThis.WebSocket = Socket;
  const sender = { id: "hoxi-test", url: "chrome-extension://hoxi-test/popup.html" };
  const message = data => new Promise(resolve => onMessage(data, sender, resolve));
  try {
    await import(`./worker.js?test=${Date.now() + 1}`);
    const oneTime = "a".repeat(64);
    const code = Buffer.from(`http://127.0.0.1:5420/api/hoxi/browser/extension#${oneTime}`).toString("base64url");
    assert.deepEqual(await message({ type: "connect", code, tabID: 10 }), { ok: true });
    assert.equal(sockets[0].lastToken, oneTime);
    await message({ type: "disconnect" });

    // Reconnect with an empty code using the persisted linkToken.
    const status = await message({ type: "status" });
    assert.equal(status.hasSavedEndpoint, true);
    assert.deepEqual(await message({ type: "connect", code: "", tabID: 10 }), { ok: true });
    const socket = sockets[1];
    assert.equal(socket.lastToken, persistentToken);

    async function action(input) {
      const count = socket.sent.length;
      socket.onmessage({ data: JSON.stringify(input) });
      for (let i = 0; i < 20 && socket.sent.length === count; i++) await new Promise(resolve => setTimeout(resolve, 1));
      return socket.sent.at(-1);
    }
    // Auto-follow child tab opened from tab 10.
    onCreated({ id: 11, openerTabId: 10, url: "https://example.com/popup", title: "Popup Tab" });
    await new Promise(resolve => setTimeout(resolve, 10));
    assert.equal(attached.has(11), true);
    const afterPopup = await action({ action: "state" });
    assert.equal(afterPopup.result.tabID, "11");
    assert.equal(afterPopup.result.tabs.length, 2);

    // Create a new tab via tab_new, switch back to root tab 10, and close secondary tabs.
    const created = await action({ action: "tab_new", url: "https://example.com/new" });
    assert.equal(created.result.tabID, "12");
    assert.equal(created.result.tabs.length, 3);
    const selected = await action({ action: "tab_select", tabID: "10" });
    assert.equal(selected.result.tabID, "10");
    const closed = await action({ action: "tab_close", tabID: "12" });
    assert.equal(closed.result.tabs.length, 2);
    assert.deepEqual(removed, [12]);
  } finally {
    await message({ type: "disconnect" });
    globalThis.chrome = oldChrome;
    globalThis.WebSocket = oldSocket;
  }
});
