import { browserAPI, browserPoint, browserURL } from "../services/browser";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

describe("Browser inspector", () => {
  it("encodes session and worktree scope on every endpoint", () => {
    const scope = { sessionID: "session &?", directory: "/work trees/\u4e2d\u6587?&" };
    for (const endpoint of ["state", "frame", "action"] as const) {
      const url = new URL(browserURL(endpoint, scope), "http://localhost");
      assertEqual(url.pathname, `/api/hoxi/browser/${endpoint}`);
      assertEqual(url.searchParams.get("sessionID"), scope.sessionID);
      assertEqual(url.searchParams.get("directory"), scope.directory);
    }
  });

  it("maps scaled image bounds to the fixed viewport and clamps the last pixel", () => {
    const bounds = { left: 100, top: 50, width: 640, height: 400 };
    assertDeepEqual(browserPoint(bounds, 420, 250), { x: 640, y: 400 });
    assertDeepEqual(browserPoint(bounds, 100, 50), { x: 0, y: 0 });
    assertDeepEqual(browserPoint(bounds, 740, 450), { x: 1279, y: 799 });
    assertEqual(browserPoint(bounds, 99, 50), null);
    assertEqual(browserPoint(bounds, 740, 451), null);
    assertEqual(browserPoint({ ...bounds, width: 0 }, 100, 50), null);
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
});
