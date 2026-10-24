import assert from "node:assert/strict";
import { test } from "node:test";
import BrowserPlugin from "./browser_plugin.mjs";

test("standalone browser plugin", async (t) => {
  const previous = { fetch: globalThis.fetch, url: process.env.HOXI_BROWSER_URL, token: process.env.HOXI_BROWSER_TOKEN };
  t.after(() => {
    globalThis.fetch = previous.fetch;
    for (const [key, value] of [["HOXI_BROWSER_URL", previous.url], ["HOXI_BROWSER_TOKEN", previous.token]]) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  });
  process.env.HOXI_BROWSER_URL = "http://127.0.0.1:9000/";
  process.env.HOXI_BROWSER_TOKEN = "secret";
  const hooks = await BrowserPlugin({ directory: "/wrong/factory" });
  await t.test("config hook forces browser approval and preserves other permissions", async () => {
    for (const permission of [undefined, "allow", "ask", "deny",
      { hoxi_browser: "allow", "*": "allow", edit: "deny", bash: { "git *": "allow", "*": "ask" } },
      { "*": "allow", hoxi_browser: { "*": "allow" }, read: "allow" }]) {
      const cfg = { model: "test/model", permission };
      const expected = typeof permission === "string" ? { "*": permission } : { ...permission };
      delete expected.hoxi_browser;
      expected.hoxi_browser = "ask";
      await hooks.config(cfg);
      assert.deepEqual(cfg.permission, expected);
      assert.equal(Object.keys(cfg.permission).at(-1), "hoxi_browser");
      assert.equal(cfg.model, "test/model");
      await hooks.config(cfg);
      assert.deepEqual(cfg.permission, expected);
    }
  });
  const execute = hooks.tool.hoxi_browser.execute;
  let requests = [], asks = [];
  const controller = new AbortController();
  const context = {
    sessionID: "session&1", directory: "/work tree/\u771f\u5b9e", abort: controller.signal,
    ask: async (request) => { asks.push(request); },
  };
  const state = { title: "Example", url: "https://hoxi.ai", tabs: [], manual: false };
  globalThis.fetch = async (url, options) => {
    requests.push({ url, options });
    return Response.json(state);
  };

  await t.test("all mutations ask before scoped authenticated fetch", async () => {
    for (const action of ["navigate", "back", "forward", "reload", "click", "type", "key", "scroll", "evaluate", "tab_new", "tab_select", "tab_close", "close"]) {
      requests = []; asks = [];
      const result = await execute({ action, text: "document.title", sessionID: "spoof", directory: "/spoof", manual: false }, context);
      assert.equal(asks.length, 1);
      assert.equal(asks[0].permission, "hoxi_browser");
      assert.ok(asks[0].patterns[0].startsWith(action + " "));
      assert.deepEqual(asks[0].always, []);
      assert.equal(asks[0].metadata.sessionID, context.sessionID);
      assert.equal(asks[0].metadata.directory, context.directory);
      assert.equal(requests.length, 1);
      const { url, options } = requests[0];
      assert.equal(url.pathname, "/api/hoxi/browser/action");
      assert.equal(url.searchParams.get("sessionID"), context.sessionID);
      assert.equal(url.searchParams.get("directory"), context.directory);
      assert.equal(options.method, "POST");
      assert.equal(options.headers["X-Hoxi-Agent"], "true");
      assert.equal(options.headers.Authorization, "Bearer secret");
      assert.equal(options.signal, controller.signal);
      assert.equal(options.redirect, "error");
      assert.deepEqual(JSON.parse(options.body), { action, text: "document.title" });
      assert.deepEqual(JSON.parse(result.output), state);
    }
  });

  await t.test("denied approvals cannot reach fetch", async () => {
    requests = [];
    await assert.rejects(execute({ action: "evaluate", text: "fetch('/delete')" }, {
      ...context, ask: async () => { throw new Error("denied"); },
    }), /denied/);
    assert.equal(requests.length, 0);
  });

  await t.test("snapshot and screenshot skip approvals; images only in attachments", async () => {
    asks = [];
    globalThis.fetch = async () => Response.json({ ...state, image: "/9j/BASE64", snapshot: { text: "untrusted" } });
    for (const action of ["snapshot", "screenshot"]) {
      const result = await execute({ action }, context);
      assert.equal(asks.length, 0);
      assert.equal(result.output.includes("BASE64"), false);
      assert.equal(JSON.parse(result.output).snapshot.text, "untrusted");
      assert.deepEqual(result.attachments, [{ type: "file", mime: "image/jpeg", filename: "browser.jpg", url: "data:image/jpeg;base64,/9j/BASE64" }]);
    }
  });

  await t.test("abort before execution and during approval prevents requests", async () => {
    let fetched = false;
    globalThis.fetch = async () => { fetched = true; return Response.json(state); };
    const abort = new AbortController();
    abort.abort();
    await assert.rejects(execute({ action: "snapshot" }, { ...context, abort: abort.signal }), { name: "AbortError" });
    const pending = new AbortController();
    await assert.rejects(execute({ action: "reload" }, {
      ...context, abort: pending.signal, ask: async () => pending.abort(),
    }), { name: "AbortError" });
    assert.equal(fetched, false);
  });

  await t.test("in-flight fetch receives cancellation", async () => {
    const abort = new AbortController();
    globalThis.fetch = async (_url, { signal }) => new Promise((_resolve, reject) => {
      signal.addEventListener("abort", () => reject(signal.reason), { once: true });
      abort.abort();
    });
    await assert.rejects(execute({ action: "snapshot" }, { ...context, abort: abort.signal }), { name: "AbortError" });
  });

  await t.test("manual mode and HTTP errors propagate; no bypass action", async () => {
    globalThis.fetch = async () => new Response("manual control is active", { status: 403 });
    await assert.rejects(execute({ action: "snapshot" }, context), /403: manual control is active/);
    await assert.rejects(execute({ action: "control", manual: false }, context));
    await assert.rejects(execute({ action: "snapshot" }, { ...context, directory: "" }), /scope/);
  });

  await t.test("system transform covers tools, untrusted content, approval and manual mode", async () => {
    const output = { system: ["existing"] };
    await hooks["experimental.chat.system.transform"]({}, output);
    assert.equal(output.system[0], "existing");
    for (const word of ["hoxi_code_index", "hoxi_browser", "untrusted", "prompt injection", "approval", "evaluate", "Manual mode"]) {
      assert.ok(output.system[1].includes(word));
    }
  });

  await t.test("hoxi_code_index executes search, inspect, callers and refresh bound to context.directory", async () => {
    const execIndex = hooks.tool.hoxi_code_index.execute;
    requests = [];
    asks = [];
    globalThis.fetch = async (url, options) => {
      requests.push({ url, options });
      return Response.json({ ok: true, results: [] });
    };
    const searchRes = await execIndex({ action: "search", query: "HandleRequest", limit: 8, refresh: true, directory: "/spoof" }, context);
    assert.equal(asks.length, 0);
    assert.equal(requests.length, 1);
    assert.equal(requests[0].url.pathname, "/api/hoxi/code-index/search");
    assert.equal(requests[0].url.searchParams.get("directory"), context.directory);
    assert.equal(requests[0].url.searchParams.get("q"), "HandleRequest");
    assert.equal(requests[0].url.searchParams.get("limit"), "8");
    assert.equal(requests[0].url.searchParams.get("refresh"), "1");
    assert.equal(searchRes.title, "CodeIndex: search");

    await execIndex({ action: "inspect", path: "main.go", symbol: "HandleRequest", startLine: 3, endLine: 10 }, context);
    assert.equal(requests[1].url.pathname, "/api/hoxi/code-index/inspect");
    assert.equal(requests[1].url.searchParams.get("directory"), context.directory);
    assert.equal(requests[1].url.searchParams.get("path"), "main.go");
    assert.equal(requests[1].url.searchParams.get("symbol"), "HandleRequest");

    await execIndex({ action: "callers", symbol: "HandleRequest", path: "main.go" }, context);
    assert.equal(requests[2].url.pathname, "/api/hoxi/code-index/callers");
    assert.equal(requests[2].url.searchParams.get("directory"), context.directory);
    assert.equal(requests[2].url.searchParams.get("symbol"), "HandleRequest");

    await execIndex({ action: "refresh" }, context);
    assert.equal(requests[3].url.pathname, "/api/hoxi/code-index/refresh");
    assert.equal(requests[3].options.method, "POST");
  });
});
