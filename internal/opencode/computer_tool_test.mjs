import assert from "node:assert/strict";
import { test } from "node:test";
import HoxiPlugin from "./browser_plugin.mjs";

test("computer tool keeps authority in Go and sends only scoped actions", async t => {
  const previous = { fetch: globalThis.fetch, url: process.env.HOXI_GATEWAY_URL, token: process.env.HOXI_BROWSER_TOKEN };
  t.after(() => {
    globalThis.fetch = previous.fetch;
    for (const [key, value] of [["HOXI_GATEWAY_URL", previous.url], ["HOXI_BROWSER_TOKEN", previous.token]]) {
      if (value === undefined) delete process.env[key]; else process.env[key] = value;
    }
  });
  process.env.HOXI_GATEWAY_URL = "http://127.0.0.1:9000/";
  process.env.HOXI_BROWSER_TOKEN = "computer-secret";
  const hooks = await HoxiPlugin({ directory: "/factory" });
  const execute = hooks.tool.hoxi_computer.execute;
  const abort = new AbortController();
  const context = { sessionID: "ses_computer", directory: "/repo/.hoxi/worktrees/中文 space", abort: abort.signal,
    ask: async () => { throw new Error("Input approval must be enforced in Go"); } };
  const calls = [];
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options });
    return Response.json({ windowID: "window-one", observationID: "opaque", image: "image-bytes" });
  };
  const result = await execute({ action: "screenshot", sessionID: "spoof", directory: "/other", grant: true, manual: false }, context);
  assert.equal(calls[0].url.pathname, "/api/hoxi/computer/action");
  assert.equal(calls[0].url.searchParams.get("sessionID"), context.sessionID);
  assert.equal(calls[0].url.searchParams.get("directory"), context.directory);
  assert.equal(calls[0].options.headers.Authorization, "Bearer computer-secret");
  assert.equal(calls[0].options.headers["X-Hoxi-Agent"], "true");
  assert.equal(calls[0].options.signal, context.abort);
  assert.equal(calls[0].options.redirect, "error");
  assert.deepEqual(JSON.parse(calls[0].options.body), { action: "screenshot" });
  assert.equal(result.output.includes("image-bytes"), false);
  assert.equal(result.attachments[0].url, "data:image/jpeg;base64,image-bytes");
  await execute({ action: "click", observationID: "fresh", windowID: "one", x: 5, y: 8 }, context);
  assert.equal(calls.length, 2);
  for (const action of ["grant", "evaluate", "permissions", "resume"]) {
    await assert.rejects(execute({ action }, context));
  }
  assert.equal(calls.length, 2);
  await assert.rejects(execute({ action: "status" }, { ...context, sessionID: "" }), /scope/);
  abort.abort();
  await assert.rejects(execute({ action: "click" }, context), { name: "AbortError" });
  assert.equal(calls.length, 2);
});

test("computer tool propagates denied and paused control", async t => {
  const previous = { fetch: globalThis.fetch, url: process.env.HOXI_GATEWAY_URL, token: process.env.HOXI_BROWSER_TOKEN };
  t.after(() => {
    globalThis.fetch = previous.fetch;
    for (const [key, value] of [["HOXI_GATEWAY_URL", previous.url], ["HOXI_BROWSER_TOKEN", previous.token]]) {
      if (value === undefined) delete process.env[key]; else process.env[key] = value;
    }
  });
  process.env.HOXI_GATEWAY_URL = "http://127.0.0.1:9000";
  process.env.HOXI_BROWSER_TOKEN = "secret";
  const hooks = await HoxiPlugin({ directory: "/repo" });
  globalThis.fetch = async () => new Response("computer_input_denied", { status: 403 });
  await assert.rejects(hooks.tool.hoxi_computer.execute({ action: "click" }, {
    sessionID: "ses_one", directory: "/repo", abort: new AbortController().signal,
  }), /403: computer_input_denied/);
});
