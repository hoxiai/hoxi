import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
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
  await t.test("config hook forces browser and tools approval and preserves other permissions", async () => {
    for (const permission of [undefined, "allow", "ask", "deny",
      { hoxi_browser: "allow", hoxi_tools: "allow", "*": "allow", edit: "deny", bash: { "git *": "allow", "*": "ask" } },
      { "*": "allow", hoxi_browser: { "*": "allow" }, hoxi_tools: { "*": "allow" }, read: "allow" }]) {
      const cfg = { model: "test/model", permission };
      const expected = typeof permission === "string" ? { "*": permission } : { ...permission };
      delete expected.hoxi_browser;
      delete expected.hoxi_tools;
      delete expected.hoxi_computer;
      expected.hoxi_browser = "ask";
      expected.hoxi_tools = "ask";
      expected.hoxi_computer = "ask";
      await hooks.config(cfg);
      assert.deepEqual(cfg.permission, expected);
      assert.deepEqual(Object.keys(cfg.permission).slice(-3), ["hoxi_browser", "hoxi_tools", "hoxi_computer"]);
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

    await execIndex({ action: "callees", symbol: "HandleRequest", path: "main.go" }, context);
    assert.equal(requests[4].url.pathname, "/api/hoxi/code-index/callees");
    assert.equal(requests[4].url.searchParams.get("directory"), context.directory);
    assert.equal(requests[4].url.searchParams.get("symbol"), "HandleRequest");

    await execIndex({ action: "impact", symbol: "HandleRequest", depth: 2 }, context);
    assert.equal(requests[5].url.pathname, "/api/hoxi/code-index/impact");
    assert.equal(requests[5].url.searchParams.get("depth"), "2");

    await execIndex({ action: "context", symbol: "HandleRequest", contextLines: 2 }, context);
    assert.equal(requests[6].url.pathname, "/api/hoxi/code-index/context");
    assert.equal(requests[6].url.searchParams.get("symbol"), "HandleRequest");
    assert.equal(requests[6].url.searchParams.get("contextLines"), "2");

    // Verify compact plain-text rendering of inspect response (no JSON string escaping or directory metadata bloat)
    globalThis.fetch = async () =>
      Response.json({
        directory: "/work/tree",
        path: "internal/foo.go",
        symbol: "HandleFoo",
        startLine: 10,
        endLine: 12,
        totalLines: 80,
        content: '10: func HandleFoo() string {\n11: \treturn "ok"\n12: }',
      });
    const inspectRes = await execIndex({ action: "inspect", path: "internal/foo.go", symbol: "HandleFoo" }, context);
    assert.equal(
      inspectRes.output,
      '# internal/foo.go:10-12 (of 80) [HandleFoo]\n10: func HandleFoo() string {\n11: \treturn "ok"\n12: }',
    );

    // Sensitive / protected paths require read approval first and are rejected before reaching fetch
    for (const blockedPath of [".hoxi/cloudflared.json", ".claude/.credentials.json", ".env", "../outside.go", "/etc/passwd"]) {
      requests = [];
      asks = [];
      await assert.rejects(
        execIndex({ action: "inspect", path: blockedPath }, context),
        /protected or secret path|denied/i,
      );
      assert.equal(asks.length, 1, `expected approval ask for ${blockedPath}`);
      assert.equal(asks[0].permission, "read");
      assert.equal(requests.length, 0, `expected no HTTP fetch for ${blockedPath}`);
    }
  });

  await t.test("experimental.session.compacting injects 8-dimension compaction prompt", async () => {
    const compOut = { prompt: "", context: [] };
    await hooks["experimental.session.compacting"]({ sessionID: "ses_1" }, compOut);
    assert.ok(compOut.prompt.includes("## Goals"));
    assert.ok(compOut.prompt.includes("## User constraints"));
    assert.ok(compOut.prompt.includes("## Changed files and reasons"));
    assert.ok(compOut.prompt.includes("## Next steps"));
  });

  await t.test("experimental.chat.messages.transform compacts verbose passing test logs while preserving failures and original part objects", async () => {
    const passingLines = Array.from({ length: 100 }, (_, i) => `ok  \tgithub.com/hoxiai/hoxi/pkg_${i}\t0.02s`).join("\n");
    const passingPart = {
      type: "tool",
      tool: "bash",
      state: {
        status: "completed",
        input: { command: "go test ./..." },
        output: passingLines,
      },
    };
    const failingLines = Array.from({ length: 80 }, (_, i) =>
      i === 45 ? "--- FAIL: TestAuthToken (0.01s)\n    auth_test.go:88: token mismatch" : `=== RUN   TestCase_${i}`
    ).join("\n");
    const failingPart = {
      type: "tool",
      tool: "bash",
      state: {
        status: "completed",
        input: { command: "go test ./..." },
        output: failingLines,
      },
    };
    const messages = [{ parts: [passingPart, failingPart] }];
    await hooks["experimental.chat.messages.transform"]({}, { messages });

    // Original object is not mutated in place
    assert.equal(passingPart.state.output, passingLines);
    // Transformed passing part is deterministically compacted
    assert.ok(messages[0].parts[0].state.output.includes("lines of passing check output omitted"));
    assert.ok(messages[0].parts[0].state.output.length < passingLines.length / 2);
    // Failing test log under 16 KB is preserved verbatim so the model sees all failure details
    assert.equal(messages[0].parts[1].state.output, failingLines);

    // ANSI escape sequences and carriage-return progress frames are stripped losslessly even on short outputs
    const noisyShortPart = {
      type: "tool",
      tool: "bash",
      state: {
        status: "completed",
        input: { command: "npm test" },
        output: "Downloading 10%\rDownloading 60%\rDownloading 100%\n\x1b[32mPASS\x1b[0m src/app.test.ts",
      },
    };
    const shortMessages = [{ parts: [noisyShortPart] }];
    await hooks["experimental.chat.messages.transform"]({}, { messages: shortMessages });
    assert.equal(shortMessages[0].parts[0].state.output, "Downloading 100%\nPASS src/app.test.ts");

    // Package manager install logs > 2400 chars without failure are also compacted
    const installLines = Array.from({ length: 90 }, (_, i) => `npm http fetch GET 200 https://registry.npmjs.org/pkg-${i} 12ms`).join("\n");
    const installPart = {
      type: "tool",
      tool: "bash",
      state: {
        status: "completed",
        input: { command: "npm install" },
        output: installLines,
      },
    };
    const installMessages = [{ parts: [installPart] }];
    await hooks["experimental.chat.messages.transform"]({}, { messages: installMessages });
    assert.ok(installMessages[0].parts[0].state.output.includes("lines of passing check output omitted"));
  });

  await t.test("system.transform deduplicates <project_memory> across plugins and redacts secrets", async () => {
    const tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "hoxi-mem-dedup-"));
    try {
      await fs.mkdir(path.join(tmpDir, ".hoxi", "memory"), { recursive: true });
      await fs.writeFile(
        path.join(tmpDir, ".hoxi", "memory", "project.md"),
        "Updated: 2026-10-24T00:00:00Z\n# Project Rules\nNever leak sk-1234567890abcdefghijklmno.",
        "utf8",
      );
      const { HoxiMemoryPlugin } = await import("../../.opencode/plugins/hoxi-memory.js");
      const memHooks = await HoxiMemoryPlugin({ directory: tmpDir });
      const out = { system: [] };
      await hooks["experimental.chat.system.transform"]({ sessionID: "ses_dedup", directory: tmpDir }, out);
      await memHooks["experimental.chat.system.transform"]({ sessionID: "ses_dedup", directory: tmpDir }, out);

      const memBlocks = out.system.filter((s) => s.includes("<project_memory>"));
      assert.equal(memBlocks.length, 1, "expected exactly 1 <project_memory> block when both plugins run");
      assert.ok(!memBlocks[0].includes("Updated:"));
      assert.ok(!memBlocks[0].includes("sk-1234567890abcdefghijklmno"));
      assert.ok(memBlocks[0].includes("[REDACTED_SECRET]"));
    } finally {
      await fs.rm(tmpDir, { recursive: true, force: true });
    }
  });

  await t.test("hoxi_tools executes discover and inspect without approval and requires approval for run", async () => {
    const execTools = hooks.tool.hoxi_tools.execute;
    requests = [];
    asks = [];
    globalThis.fetch = async (url, options) => {
      requests.push({ url, options });
      return Response.json({ code: 0, data: { ok: true } });
    };

    const discRes = await execTools({ action: "discover", query: "amazon reviews", limit: 5 }, context);
    assert.equal(asks.length, 0);
    assert.equal(requests.length, 1);
    assert.equal(requests[0].url.pathname, "/api/hoxi/tools/discover");
    assert.deepEqual(JSON.parse(requests[0].options.body), { query: "amazon reviews", limit: 5 });
    assert.equal(discRes.title, "HoxiTools: discover");

    const inspRes = await execTools({ action: "inspect", provider: "apify", endpoint: "/apidojo/tweet-scraper" }, context);
    assert.equal(asks.length, 0);
    assert.equal(requests.length, 2);
    assert.equal(requests[1].url.pathname, "/api/hoxi/tools/inspect");
    assert.deepEqual(JSON.parse(requests[1].options.body), { provider: "apify", endpoint: "/apidojo/tweet-scraper" });
    assert.equal(inspRes.title, "HoxiTools: inspect (apify)");

    const runRes = await execTools({
      action: "run",
      provider: "apify",
      endpoint: "/apidojo/tweet-scraper",
      body: { handle: "hoxiai" },
      wait: true,
    }, context);
    assert.equal(asks.length, 1);
    assert.equal(asks[0].permission, "hoxi_tools");
    assert.deepEqual(asks[0].patterns, ["run apify /apidojo/tweet-scraper"]);
    assert.equal(requests.length, 3);
    assert.equal(requests[2].url.pathname, "/api/hoxi/tools/run");
    assert.deepEqual(JSON.parse(requests[2].options.body), {
      provider: "apify",
      endpoint: "/apidojo/tweet-scraper",
      body: { handle: "hoxiai" },
      wait: true,
    });
    assert.equal(runRes.title, "HoxiTools: run (apify)");
  });
});
