import { computerAPI } from "../services/computer";
import { getGatewayToken, setGatewayToken } from "../services/http";
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
      assertDeepEqual(JSON.parse(calls[2].init.body as string), { paused: true });
      await computerAPI.stop(signal);
      assertEqual(calls[4].url.pathname, "/api/hoxi/computer/stop");
      assertEqual(calls[4].url.search, "");
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
});
