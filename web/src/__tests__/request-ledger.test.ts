import {
  commitWorktreeWithRecovery,
  replyPermissionWithRecovery,
  RequestOutcomeUnknownError,
  shouldInspectAfterFailure,
} from "../services/request-ledger";
import { assertEqual, describe, it } from "./test-harness";

describe("request-ledger client recovery", () => {
  it("recovers completed worktree commit via GET /api/hoxi/requests/{id} when POST response drops", async () => {
    const origFetch = globalThis.fetch;
    const calls: Array<{ url: string; method: string; body?: string }> = [];
    try {
      globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method || "GET";
        const body = typeof init?.body === "string" ? init.body : undefined;
        calls.push({ url, method, body });
        if (method === "POST" && url === "/api/hoxi/worktree/commit") {
          throw new TypeError("Failed to fetch");
        }
        if (method === "GET" && url.startsWith("/api/hoxi/requests/commit_mob_1?")) {
          return new Response(
            JSON.stringify({
              ok: true,
              kind: "worktree_commit",
              scope: "ses_1",
              clientRequestId: "commit_mob_1",
              status: "completed",
              result: {
                ok: true,
                commit: "a1b2c3d",
                branch: "main",
                pushed: false,
              },
            }),
            { status: 200, headers: { "Content-Type": "application/json" } }
          );
        }
        return new Response("not found", { status: 404 });
      }) as typeof fetch;

      const res = await commitWorktreeWithRecovery("ses_1", "feat: resilient commit", {
        directory: "/workspace/repo",
        clientRequestId: "commit_mob_1",
      });
      assertEqual(res.commit, "a1b2c3d");
      assertEqual(res.deduplicated, true);
      assertEqual(res.clientRequestId, "commit_mob_1");
      assertEqual(calls.length, 2);
      assertEqual(calls[1].url.includes("kind=worktree_commit"), true, "inspects worktree_commit kind");
      assertEqual(calls[1].url.includes("scope=ses_1"), true, "inspects session scope");
    } finally {
      globalThis.fetch = origFetch;
    }
  });

  it("surfaces RequestOutcomeUnknownError with live git evidence without re-executing commit", async () => {
    const origFetch = globalThis.fetch;
    let postCalls = 0;
    try {
      globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method || "GET";
        if (method === "POST" && url === "/api/hoxi/worktree/commit") {
          postCalls++;
          return new Response(
            JSON.stringify({
              error: "request was interrupted by a Hoxi restart; its outcome is unknown",
              code: "outcome_unknown",
            }),
            { status: 409, headers: { "Content-Type": "application/json" } }
          );
        }
        if (method === "GET" && url.startsWith("/api/hoxi/requests/commit_crash_1?")) {
          return new Response(
            JSON.stringify({
              ok: true,
              kind: "worktree_commit",
              scope: "ses_1",
              clientRequestId: "commit_crash_1",
              status: "outcome_unknown",
              errorCode: "outcome_unknown",
              errorMessage: "request was interrupted by a Hoxi restart; its outcome is unknown",
              evidence: { headAdvanced: true, workingTreeClean: true },
            }),
            { status: 200, headers: { "Content-Type": "application/json" } }
          );
        }
        return new Response("not found", { status: 404 });
      }) as typeof fetch;

      let caught: unknown = null;
      try {
        await commitWorktreeWithRecovery("ses_1", "fix: crash test", {
          clientRequestId: "commit_crash_1",
        });
      } catch (err) {
        caught = err;
      }
      assertEqual(caught instanceof RequestOutcomeUnknownError, true, "throws RequestOutcomeUnknownError");
      assertEqual(postCalls, 1, "does not auto-retry POST on outcome_unknown");
      assertEqual(
        (caught as Error).message.includes("HEAD already advanced"),
        true,
        "surfaces live git HEAD evidence"
      );
    } finally {
      globalThis.fetch = origFetch;
    }
  });

  it("recovers completed permission reply on network drop and skips inspect on 409 request_conflict", async () => {
    assertEqual(
      shouldInspectAfterFailure(
        new Error('HTTP 409: {"error":"different content","code":"request_conflict"}')
      ),
      false
    );
    assertEqual(shouldInspectAfterFailure(new TypeError("NetworkError")), true);

    const origFetch = globalThis.fetch;
    try {
      globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if ((init?.method || "GET") === "POST") {
          throw new TypeError("socket hang up");
        }
        if (url.startsWith("/api/hoxi/requests/perm_req_1?")) {
          return new Response(
            JSON.stringify({
              ok: true,
              kind: "permission_reply",
              scope: "per_rnr_1",
              clientRequestId: "perm_req_1",
              status: "completed",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } }
          );
        }
        return new Response("not found", { status: 404 });
      }) as typeof fetch;

      await replyPermissionWithRecovery("per_rnr_1", "once", "/repo", undefined, false, "perm_req_1");
    } finally {
      globalThis.fetch = origFetch;
    }
  });
});
