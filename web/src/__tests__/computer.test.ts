import { computerAPI } from "../services/computer";
import { getGatewayToken, setGatewayToken } from "../services/http";
import { resolveElementWindowBounds } from "../store/computer";
import { extractDescription, isComputerTool, kiloToolIconName } from "../components/timeline/tool-card-helpers";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

describe("desktop computer transport", () => {
  it("keeps app grants and observations within the selected chat and worktree", async () => {
    const previous = { fetch: globalThis.fetch, token: getGatewayToken() };
    const calls: Array<{ url: URL; init: RequestInit }> = [];
    const scope = { sessionID: "ses_computer", directory: "/repo/.hoxi/worktrees/\u4e2d\u6587 space" };
    const signal = new AbortController().signal;
    setGatewayToken("ui-token");
    globalThis.fetch = (async (url, init) => {
      calls.push({ url: new URL(String(url), "http://127.0.0.1"), init: init || {} });
      return Response.json({ grant: null });
    }) as typeof fetch;
    try {
      await computerAPI.grant(scope, "app-one", "zh-CN", signal);
      await computerAPI.observe(scope, "window-one", signal);
      await computerAPI.observe(scope, "window-one", signal, false);
      await computerAPI.pause(scope, true, signal);
      await computerAPI.revoke(scope, signal);
      for (const call of calls) {
        assertEqual(call.url.searchParams.get("sessionID"), scope.sessionID);
        assertEqual(call.url.searchParams.get("directory"), scope.directory);
        assertEqual(call.init.signal, signal);
        assertEqual(call.init.cache, "no-store");
        const headers = call.init.headers as Record<string, string>;
        assertEqual(headers["X-Hoxi-Computer"], "true");
        assertEqual(headers["X-Hoxi-Token"], "ui-token");
        assertEqual(headers.Authorization, undefined);
      }
      assertDeepEqual(JSON.parse(calls[0].init.body as string), { appID: "app-one", locale: "zh-CN" });
      assertDeepEqual(JSON.parse(calls[1].init.body as string), { action: "screenshot", windowID: "window-one" });
      assertDeepEqual(JSON.parse(calls[2].init.body as string), { action: "state", windowID: "window-one" });
      assertDeepEqual(JSON.parse(calls[3].init.body as string), { paused: true });
      await computerAPI.stop(signal);
      assertEqual(calls[5].url.pathname, "/api/hoxi/computer/stop");
      assertEqual(calls[5].url.search, "");
      assertTrue(!("input" in computerAPI), "UI must not expose an input bypass");
    } finally { globalThis.fetch = previous.fetch; setGatewayToken(previous.token); }
  });

  it("propagates native consent cancellation and system permission errors", async () => {
    const previous = globalThis.fetch;
    globalThis.fetch = (async () => new Response('{"error":"app_permission_not_granted"}', { status: 403 })) as typeof fetch;
    let error: unknown;
    try {
      await computerAPI.grant({ sessionID: "ses_one", directory: "/repo" }, "one", "en-US", new AbortController().signal);
    } catch (err) { error = err; }
    finally { globalThis.fetch = previous; }
    assertTrue(error instanceof Error && error.message.includes("app_permission_not_granted"));
  });

  it("computes window-relative element bounds and formats hoxi_computer tool descriptions", () => {
    const wb = resolveElementWindowBounds(
      { id: 2, role: "AXButton", name: "Run", bounds: { x: 250, y: 180, width: 80, height: 32 } },
      { x: 100, y: 80, width: 800, height: 600 }
    );
    assertDeepEqual(wb, { x: 150, y: 100, width: 80, height: 32 });

    assertTrue(isComputerTool("hoxi_computer"));
    assertEqual(kiloToolIconName("hoxi_computer"), "window-cursor");
    assertEqual(
      extractDescription({
        id: "p1",
        sessionID: "s1",
        messageID: "m1",
        type: "tool",
        tool: "hoxi_computer",
        callID: "c1",
        state: { status: "running", input: { action: "click", elementID: 2, clickCount: 2 }, time: { start: 1 } },
      }),
      "click ×2 #2"
    );
    assertEqual(
      extractDescription({
        id: "p2",
        sessionID: "s1",
        messageID: "m1",
        type: "tool",
        tool: "hoxi_computer",
        callID: "c2",
        state: { status: "running", input: { action: "type", elementID: 1, text: "hello world" }, time: { start: 1 } },
      }),
      'type #1 "hello world"'
    );
  });
});
