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

  it("sends runner replies to the unified permission endpoint with the steering note", async () => {
    const m = mockFetch(() => ({ status: 200, body: true }));
    try {
      await hoxiAPI.replyPermission("per_rnr_abc", "reject", "/project", "use pnpm instead");
      await hoxiAPI.replyPermission("per_rnr_def", "once", "/project");
      assertEqual(m.calls[0].url, "/api/hoxi/permission/per_rnr_abc/reply?directory=%2Fproject");
      assertEqual(m.calls[0].method, "POST");
      assertDeepEqual(m.calls[0].body, { reply: "reject", message: "use pnpm instead" });
      assertDeepEqual(m.calls[1].body, { reply: "once" });
    } finally { m.restore(); }
  });

  it("sends OpenCode replies through the unified permission endpoint", async () => {
    const m = mockFetch(() => ({ status: 200, body: true }));
    try {
      await hoxiAPI.replyPermission("per_01JABC", "always", "/project");
      assertEqual(m.calls[0].url, "/api/hoxi/permission/per_01JABC/reply?directory=%2Fproject");
      assertDeepEqual(m.calls[0].body, { reply: "always" });
    } finally { m.restore(); }
  });

  it("fetches pending permissions from the unified backend endpoint", async () => {
    const m = mockFetch(() => ({
      status: 200,
      body: [req("per_engine", "ses_oc"), req("per_rnr_1", "ses_agy_1")],
    }));
    try {
      const merged = await hoxiAPI.listPermissions("/project");
      assertDeepEqual(merged.map((p) => p.id), ["per_engine", "per_rnr_1"]);
      assertEqual(m.calls.length, 1);
      assertEqual(m.calls[0].url, "/api/hoxi/permission?directory=%2Fproject");
    } finally { m.restore(); }
  });

  it("lists and revokes session grants and queries/clears the approval audit log", async () => {
    const m = mockFetch((url) => {
      if (url.startsWith("/api/hoxi/permission/grants")) {
        return {
          status: 200,
          body: {
            revoked: 1,
            grants: [
              {
                id: "gr_1",
                sessionID: "ses_1",
                kind: "bash",
                tool: "bash",
                pattern: "npm install",
                scope: "session_memory",
                source: "desktop",
                createdAt: 1000,
              },
            ],
          },
        };
      }
      return {
        status: 200,
        body: {
          entries: [
            {
              id: "aud_1",
              timestamp: 1000,
              requestID: "per_rnr_1",
              sessionID: "ses_1",
              permission: "bash",
              summary: "npm install",
              decision: "always",
              source: "desktop",
            },
          ],
        },
      };
    });
    try {
      const grants = await hoxiAPI.listApprovalGrants({ sessionID: "ses_1" });
      assertEqual(grants.length, 1);
      assertEqual(grants[0].id, "gr_1");
      assertEqual(m.calls[0].url, "/api/hoxi/permission/grants?sessionID=ses_1");

      const revoked = await hoxiAPI.revokeApprovalGrants({ id: "gr_1" });
      assertEqual(revoked.revoked, 1);
      assertEqual(m.calls[1].url, "/api/hoxi/permission/grants?id=gr_1");
      assertEqual(m.calls[1].method, "DELETE");

      const audit = await hoxiAPI.listApprovalAudit({ limit: 50 });
      assertEqual(audit.length, 1);
      assertEqual(audit[0].id, "aud_1");
      assertEqual(m.calls[2].url, "/api/hoxi/permission/audit?limit=50");

      await hoxiAPI.clearApprovalAudit();
      assertEqual(m.calls[3].url, "/api/hoxi/permission/audit");
      assertEqual(m.calls[3].method, "DELETE");
    } finally {
      m.restore();
    }
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

  it("judges commands wrapped by the Hoxi sandbox plugin by their inner command", () => {
    const quote = (v: string) => `'${v.replaceAll("'", `'\\''`)}'`;
    const wrap = (c: string) =>
      `${quote("/usr/bin/sandbox-exec")} -f ${quote("/tmp/p.sb")} -- /bin/sh -c ${quote(c)}`;
    for (const c of policyCases) {
      if (!c.command) continue;
      const input = { permission: "bash", patterns: [wrap(c.command)] };
      assertEqual(decidePermission(input), c.decision, `sandboxed ${c.command}`);
      assertEqual(isDestructivePermissionRequest(input), c.destructive, `sandboxed ${c.command}`);
    }
  });
});
