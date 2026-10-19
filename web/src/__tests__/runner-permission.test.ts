import { hoxiAPI, isRunnerPermissionID } from "../services/api";
import type { PermissionRequest } from "../services/sse-types";
import {
  decidePermission,
  isDestructivePermissionRequest,
} from "../../../.opencode/lib/permission-policy.js";
import policyCases from "../../../.opencode/lib/permission-policy.cases.json";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

type Call = { url: string; method: string; body: any };

function mockFetch(responses: (url: string) => { status: number; body: unknown }) {
  const calls: Call[] = [];
  const saved = globalThis.fetch;
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    calls.push({ url, method: init?.method || "GET", body: init?.body ? JSON.parse(String(init.body)) : undefined });
    const res = responses(url);
    return {
      ok: res.status >= 200 && res.status < 300,
      status: res.status,
      json: async () => res.body,
      text: async () => JSON.stringify(res.body),
    } as Response;
  }) as typeof fetch;
  return { calls, restore: () => { globalThis.fetch = saved; } };
}

const req = (id: string, sessionID: string): PermissionRequest => ({
  id, sessionID, permission: "bash", patterns: ["rm -rf build"], metadata: {}, always: [],
});

describe("Antigravity runner permission routing", () => {
  it("recognizes runner gate request IDs only", () => {
    assertTrue(isRunnerPermissionID("per_rnr_abc"));
    assertTrue(!isRunnerPermissionID("per_01JABC"));
    assertTrue(!isRunnerPermissionID(undefined));
  });

  it("sends runner replies to the Go gate with the steering note", async () => {
    const m = mockFetch(() => ({ status: 200, body: true }));
    try {
      await hoxiAPI.replyPermission("per_rnr_abc", "reject", "/project", "use pnpm instead");
      await hoxiAPI.replyPermission("per_rnr_def", "once", "/project");
      assertEqual(m.calls[0].url, "/api/hoxi/runner/permission/per_rnr_abc/reply");
      assertEqual(m.calls[0].method, "POST");
      assertDeepEqual(m.calls[0].body, { reply: "reject", message: "use pnpm instead" });
      assertDeepEqual(m.calls[1].body, { reply: "once" });
    } finally { m.restore(); }
  });

  it("keeps OpenCode replies on the engine endpoint", async () => {
    const m = mockFetch(() => ({ status: 200, body: true }));
    try {
      await hoxiAPI.replyPermission("per_01JABC", "always", "/project");
      assertEqual(m.calls[0].url, "/permission/per_01JABC/reply?directory=%2Fproject");
      assertDeepEqual(m.calls[0].body, { reply: "always" });
    } finally { m.restore(); }
  });

  it("merges pending runner requests and tolerates a missing runner endpoint", async () => {
    let runnerStatus = 200;
    const m = mockFetch((url) =>
      url.startsWith("/api/hoxi/runner/permission")
        ? { status: runnerStatus, body: [req("per_rnr_1", "ses_agy_1")] }
        : { status: 200, body: [req("per_engine", "ses_oc")] }
    );
    try {
      const merged = await hoxiAPI.listPermissions("/project");
      assertDeepEqual(merged.map((p) => p.id), ["per_engine", "per_rnr_1"]);
      assertEqual(m.calls[1].url, "/api/hoxi/runner/permission?directory=%2Fproject");
      runnerStatus = 404;
      const engineOnly = await hoxiAPI.listPermissions("/project");
      assertDeepEqual(engineOnly.map((p) => p.id), ["per_engine"]);
    } finally { m.restore(); }
  });
});

// internal/runner/permission_policy_test.go runs the same fixture against the Go port.
describe("permission policy shared fixture", () => {
  it("matches every Go/JS parity case", () => {
    assertTrue(policyCases.length >= 20, "fixture unexpectedly small");
    for (const c of policyCases) {
      const input = { permission: "bash", patterns: c.command ? [c.command] : [] };
      assertEqual(decidePermission(input), c.decision, c.command);
      assertEqual(isDestructivePermissionRequest(input), c.destructive, c.command);
    }
  });
});
