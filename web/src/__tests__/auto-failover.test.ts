// web/src/__tests__/auto-failover.test.ts
import { reconcile } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import {
  AUTO_PROVIDER_ID,
  applyProviderBreakers,
  clearAutoFailoverPending,
  clearTurnDispatchState,
  getAutoCandidateCooldown,
  clearAutoPoolCooldowns,
  decideAutoFailover,
  deleteCustomAutoPool,
  dispatchSessionPrompt,
  findAutoFailoverNotice,
  handleSessionErrorEvent,
  handleSessionRetryStatus,
  isAutoFailoverPending,
  isCandidateCoolingDown,
  markSessionInterrupted,
  parseQuotaResetMs,
  purgeSessionsFromStoreState,
  resetAutoPoolToDefault,
  resolveAutoModelTarget,
  retryLastUserPrompt,
  saveAutoPool,
  selectModel,
  setWorkspace,
  workspace,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

const RATE_429 =
  "\u4e0a\u6e38\u8fd4\u56de 429: \u60a8\u7684\u4f7f\u7528\u91cf\u5df2\u8d85\u51fa\u9891\u7387\u9650\u5236\uff0c\u5c06\u5728 2026-10-30 22:48:16 UTC+8 \u91cd\u7f6e\uff0c\u60a8\u4e5f\u53ef\u4ee5\u5207\u6362\u5176\u4ed6\u6a21\u578b\u7ee7\u7eed\u4f7f\u7528\u3002\uff08\u6240\u6709\u5019\u9009\u8d26\u53f7\u5bf9\u6a21\u578b m \u5747\u5df2\u9650\u989d\u6216\u7981\u7528\uff09";

describe("auto-pool failover decision", () => {
  it("switches on endpoint failures, even slow ones", () => {
    const slow = 120_000;
    const cases: Array<[string, { name: string; data?: Record<string, unknown> }, string]> = [
      ["quota 429", { name: "APIError", data: { message: RATE_429, statusCode: 429 } }, "quota"],
      ["plain 429", { name: "APIError", data: { message: "Too Many Requests", statusCode: 429 } }, "rateLimit"],
      ["payment", { name: "APIError", data: { message: "Payment required", statusCode: 402 } }, "quota"],
      ["auth", { name: "APIError", data: { message: "Invalid API Key", statusCode: 401 } }, "auth"],
      ["model gone", { name: "ProviderModelNotFoundError" }, "model"],
      ["overloaded", { name: "APIError", data: { message: "Overloaded", statusCode: 529 } }, "server"],
      ["gateway", { name: "APIError", data: { message: "bad gateway", statusCode: 502 } }, "server"],
      ["network", { name: "UnknownError", data: { message: "fetch failed: ECONNRESET" } }, "network"],
      [
        "go tcp reset",
        {
          name: "RunnerError",
          data: {
            message:
              'API error (attempt 1): request failed: Post "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse": read tcp 172.19.0.1:58835->172.217.113.4:443: read: connection reset by peer',
          },
        },
        "network",
      ],
      ["io timeout", { name: "APIError", data: { message: "dial tcp 1.2.3.4:443: i/o timeout" } }, "network"],
      ["unexpected eof", { name: "APIError", data: { message: "unexpected EOF" } }, "network"],
      ["stream interrupted eof", { name: "APIError", data: { message: "stream was interrupted: unexpected EOF" } }, "network"],
      ["connection aborted", { name: "APIError", data: { message: "read tcp: connection aborted" } }, "network"],
    ];
    for (const [label, error, reason] of cases) {
      const d = decideAutoFailover(error as never, slow);
      assertEqual(d.failover, true, `${label} should switch`);
      assertEqual(d.penalize, true, `${label} should bench the candidate`);
      assertEqual(d.reason, reason, `${label} reason`);
    }
  });

  it("never switches or benches for errors another endpoint would repeat", () => {
    const cases = [
      { name: "MessageAbortedError", data: { message: "Aborted" } },
      { name: "RunnerStalledError" },
      { name: "ContextOverflowError", data: { message: "prompt is too long" } },
      { name: "APIError", data: { message: "This model's maximum context length is 128k", statusCode: 400 } },
      { name: "APIError", data: { message: "blocked by content policy", statusCode: 400 } },
      { name: "APIError", data: { message: "invalid tool schema", statusCode: 400 } },
    ];
    for (const error of cases) {
      const d = decideAutoFailover(error as never, 1_000);
      assertEqual(d.failover, false, `${error.name} must not switch`);
      assertEqual(d.penalize, false, `${error.name} must not bench the candidate`);
    }
  });

  it("only retries an unknown error when it failed fast", () => {
    const unknown = { name: "UnknownError", data: { message: "something odd" } };
    assertEqual(decideAutoFailover(unknown, 5_000).failover, true);
    assertEqual(decideAutoFailover(unknown, 60_000).failover, false);
  });

  it("prefers structured fault kind over opaque error text", () => {
    const structuredQuota = {
      name: "RunnerError",
      data: { message: "opaque upstream code E_9001", kind: "quota", retryAfterMs: 120_000 },
    };
    const d1 = decideAutoFailover(structuredQuota, 90_000);
    assertEqual(d1.failover, true);
    assertEqual(d1.penalize, true);
    assertEqual(d1.reason, "quota");

    const structuredPolicy = {
      name: "RunnerError",
      data: { message: "opaque upstream code E_9002", kind: "policy" },
    };
    const d2 = decideAutoFailover(structuredPolicy, 1_000);
    assertEqual(d2.failover, false);
    assertEqual(d2.penalize, false);
  });

  it("parses the stated reset time", () => {
    const now = Date.UTC(2026, 9, 30, 5, 24, 0); // 13:24 UTC+8
    assertEqual(parseQuotaResetMs(RATE_429, now), Date.UTC(2026, 9, 30, 14, 48, 16) - now);
    assertEqual(parseQuotaResetMs("Please try again in 20s", now), 20_000);
    assertEqual(parseQuotaResetMs("\u8bf7 30 \u5206\u949f\u540e\u91cd\u8bd5", now), 30 * 60_000);
    assertEqual(parseQuotaResetMs("resets at 2027-01-01T00:00:00Z", now), 24 * 60 * 60 * 1000);
    assertEqual(parseQuotaResetMs("\u5c06\u5728 2026-10-29 22:48:16 UTC+8 \u91cd\u7f6e", now), undefined);
    assertEqual(parseQuotaResetMs("429 Too Many Requests", now), undefined);
  });
});

describe("auto-pool failover dispatch", () => {
  it("resends a slow 429 to the next candidate, benches the failed one and records a notice", async () => {
    const orig = { send: hoxiAPI.sendPromptAsync, msgs: hoxiAPI.getMessages, diff: hoxiAPI.getSessionDiff, ren: hoxiAPI.renameSession, report: hoxiAPI.reportCandidateOutcome, get: hoxiAPI.getProviderCandidates, sum: hoxiAPI.summarizeSessionTitle };
    const sid = "ses_failover_429";
    try {
      Object.assign(hoxiAPI, { reportCandidateOutcome: async () => ({ ok: true }), getProviderCandidates: async () => ({ ok: true, breakers: {} }), getMessages: async () => [], getSessionDiff: async () => [], renameSession: async () => ({ ok: true } as never), summarizeSessionTitle: async () => ({ ok: true, title: "Test" }) });
      const model = (id: string) => ({ id, name: id, capabilities: { toolcall: true } });
      setWorkspace("providers", [
        { id: "alpha", name: "Alpha", models: { m: model("m") } },
        { id: "beta", name: "Beta", models: { m: model("m") } },
        { id: "gamma", name: "Gamma", models: { m: model("m") } },
      ]);
      setWorkspace("connectedProviders", ["alpha", "beta", "gamma"]);
      setWorkspace("sessions", [{ id: sid, title: "Failover", time: { created: 1, updated: 1 } } as never]);
      setWorkspace("activeSessionID", sid);
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: AUTO_PROVIDER_ID, modelID: "free" },
        effort: "",
      });
      await saveAutoPool("free", {
        id: "free",
        preset: true,
        strategy: "priority",
        customized: true,
        candidates: [
          { providerID: "alpha", modelID: "m" },
          { providerID: "beta", modelID: "m" },
          { providerID: "gamma", modelID: "m" },
        ],
      });
      clearAutoPoolCooldowns("free");
      applyProviderBreakers({});

      const attempted: string[] = [];
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        attempted.push(`${opts?.model?.providerID}/${opts?.model?.modelID}`);
      };
      const realNow = Date.now;
      const startedAt = Date.UTC(2026, 9, 30, 5, 24, 0);
      try {
        Date.now = () => startedAt;
        await dispatchSessionPrompt("build it", sid, { forceImmediate: true });
        assertEqual(attempted[0], "alpha/m");

        // The upstream spent 80s cycling its own accounts before answering 429.
        Date.now = () => startedAt + 80_000;
        await handleSessionErrorEvent(sid, {
          name: "APIError",
          data: { message: RATE_429, statusCode: 429, isRetryable: true },
        });
        assertDeepEqual(attempted, ["alpha/m", "beta/m"], "a slow 429 must still fail over");
        assertTrue(isCandidateCoolingDown("alpha", "m"));
        const entry = getAutoCandidateCooldown("alpha", "m");
        assertTrue(
          Boolean(entry && entry.until - Date.now() > 60 * 60 * 1000),
          "the stated reset must outlast the default 3-minute rate-limit cooldown"
        );

        const notice = findAutoFailoverNotice({
          sessionID: sid,
          providerID: "alpha",
          modelID: "m",
          time: { created: startedAt, completed: startedAt + 80_000 },
        });
        assertDeepEqual(notice?.to, { providerID: "beta", modelID: "m" });
        assertEqual(notice?.reason, "quota");

        // A second failure moves on to the third candidate instead of giving up.
        await handleSessionErrorEvent(sid, { name: "APIError", data: { message: "bad gateway", statusCode: 502 } });
        assertDeepEqual(attempted, ["alpha/m", "beta/m", "gamma/m"]);

        // A user stop neither switches nor benches the running candidate.
        await handleSessionErrorEvent(sid, { name: "MessageAbortedError", data: { message: "Aborted" } });
        assertEqual(attempted.length, 3);
        assertEqual(isCandidateCoolingDown("gamma", "m"), false);
      } finally {
        Date.now = realNow;
      }
    } finally {
      Object.assign(hoxiAPI, { sendPromptAsync: orig.send, getMessages: orig.msgs, getSessionDiff: orig.diff, renameSession: orig.ren, reportCandidateOutcome: orig.report, getProviderCandidates: orig.get, summarizeSessionTitle: orig.sum });
      setWorkspace("activeSessionID", null);
      setWorkspace("sessions", []);
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("messagesBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
      clearAutoPoolCooldowns("free");
      await resetAutoPoolToDefault("free");
      applyProviderBreakers({});
    }
  });

  it("advances to the next candidate when sendPromptAsync rejects synchronously during failover", async () => {
    const orig = { send: hoxiAPI.sendPromptAsync, msgs: hoxiAPI.getMessages, diff: hoxiAPI.getSessionDiff, ren: hoxiAPI.renameSession, report: hoxiAPI.reportCandidateOutcome, get: hoxiAPI.getProviderCandidates, sum: hoxiAPI.summarizeSessionTitle };
    const sid = "ses_failover_sync_reject";
    try {
      Object.assign(hoxiAPI, { reportCandidateOutcome: async () => ({ ok: true }), getProviderCandidates: async () => ({ ok: true, breakers: {} }), getMessages: async () => [], getSessionDiff: async () => [], renameSession: async () => ({ ok: true } as never), summarizeSessionTitle: async () => ({ ok: true, title: "Test" }) });
      const model = (id: string) => ({ id, name: id, capabilities: { toolcall: true } });
      setWorkspace("providers", [
        { id: "alpha", name: "Alpha", models: { m: model("m") } },
        { id: "beta", name: "Beta", models: { m: model("m") } },
        { id: "gamma", name: "Gamma", models: { m: model("m") } },
      ]);
      setWorkspace("connectedProviders", ["alpha", "beta", "gamma"]);
      setWorkspace("sessions", [{ id: sid, title: "Failover", time: { created: 1, updated: 1 } } as never]);
      setWorkspace("activeSessionID", sid);
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: AUTO_PROVIDER_ID, modelID: "free" },
        effort: "",
      });
      await saveAutoPool("free", {
        id: "free",
        preset: true,
        strategy: "priority",
        customized: true,
        candidates: [
          { providerID: "alpha", modelID: "m" },
          { providerID: "beta", modelID: "m" },
          { providerID: "gamma", modelID: "m" },
        ],
      });
      clearAutoPoolCooldowns("free");
      applyProviderBreakers({});

      const attempted: string[] = [];
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        const key = `${opts?.model?.providerID}/${opts?.model?.modelID}`;
        attempted.push(key);
        if (key === "beta/m") {
          throw new Error("HTTP 409: model beta/m is not loaded in the running engine");
        }
      };

      const startedAt = Date.now();
      await dispatchSessionPrompt("build it", sid, { forceImmediate: true });
      assertDeepEqual(attempted, ["alpha/m"]);

      await handleSessionErrorEvent(sid, {
        name: "APIError",
        data: { message: "502 Bad Gateway", statusCode: 502 },
      });

      assertDeepEqual(
        attempted,
        ["alpha/m", "beta/m", "gamma/m"],
        "synchronous failure on beta/m must advance to gamma/m"
      );
      assertTrue(isCandidateCoolingDown("alpha", "m"), "alpha/m should cool down");
      assertTrue(isCandidateCoolingDown("beta", "m"), "beta/m should cool down after sync rejection");
      assertEqual(isCandidateCoolingDown("gamma", "m"), false, "gamma/m is now running");
      assertTrue(isAutoFailoverPending(sid), "failover pending flag set while awaiting new turn busy");
      assertEqual(workspace.statusBySession[sid]?.type, "busy");

      const notice = findAutoFailoverNotice({
        sessionID: sid,
        providerID: "alpha",
        modelID: "m",
        time: { created: startedAt },
      });
      assertDeepEqual(notice?.from, { providerID: "alpha", modelID: "m" });
      assertDeepEqual(notice?.to, { providerID: "gamma", modelID: "m" });
    } finally {
      clearAutoFailoverPending(sid);
      Object.assign(hoxiAPI, { sendPromptAsync: orig.send, getMessages: orig.msgs, getSessionDiff: orig.diff, renameSession: orig.ren, reportCandidateOutcome: orig.report, getProviderCandidates: orig.get, summarizeSessionTitle: orig.sum });
      setWorkspace("activeSessionID", null);
      setWorkspace("sessions", []);
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("messagesBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
      clearAutoPoolCooldowns("free");
      await resetAutoPoolToDefault("free");
      applyProviderBreakers({});
    }
  });

  it("does not retry non-auto single-model turns and retries single-candidate auto-pool turns up to 2 times on transient errors", async () => {
    const orig = { send: hoxiAPI.sendPromptAsync, msgs: hoxiAPI.getMessages, diff: hoxiAPI.getSessionDiff, ren: hoxiAPI.renameSession, get: hoxiAPI.getProviderCandidates, report: hoxiAPI.reportCandidateOutcome, sum: hoxiAPI.summarizeSessionTitle };
    const sid = "ses_same_model_retry";
    try {
      Object.assign(hoxiAPI, { getMessages: async () => [], getSessionDiff: async () => [], renameSession: async () => ({ ok: true } as never), getProviderCandidates: async () => ({ ok: true, breakers: {} }), reportCandidateOutcome: async () => ({ ok: true }), summarizeSessionTitle: async () => ({ ok: true, title: "Test" }) });
      const model = (id: string) => ({ id, name: id, capabilities: { toolcall: true } });
      setWorkspace("providers", [{ id: "openai", name: "OpenAI", models: { "gpt-5": model("gpt-5") } }]);
      setWorkspace("connectedProviders", ["openai"]);
      setWorkspace("sessions", [{ id: sid, title: "Retry", time: { created: 1, updated: 1 } } as never]);
      setWorkspace("activeSessionID", sid);

      const attempted: string[] = [];
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        attempted.push(`${opts?.model?.providerID}/${opts?.model?.modelID}`);
      };

      // Non-auto session must NOT silently re-send the prompt on transient errors
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: "openai", modelID: "gpt-5" },
        effort: "",
      });
      await dispatchSessionPrompt("non-auto-ping", sid, { forceImmediate: true });
      assertDeepEqual(attempted, ["openai/gpt-5"]);
      await handleSessionErrorEvent(sid, {
        name: "APIError",
        data: { message: "read tcp 10.0.0.1:443: read: connection reset by peer" },
      });
      assertDeepEqual(attempted, ["openai/gpt-5"]);
      assertEqual(workspace.statusBySession[sid]?.type, "idle");

      // Single-candidate auto-pool session retries up to 2 times when no other candidate exists
      attempted.length = 0;
      await saveAutoPool("free", {
        id: "free",
        preset: true,
        strategy: "priority",
        customized: true,
        candidates: [{ providerID: "openai", modelID: "gpt-5" }],
      });
      clearAutoPoolCooldowns("free");
      applyProviderBreakers({});
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: AUTO_PROVIDER_ID, modelID: "free" },
        effort: "",
      });

      await dispatchSessionPrompt("ping", sid, { forceImmediate: true });
      assertDeepEqual(attempted, ["openai/gpt-5"]);

      // Retry #1 on transient network reset
      await handleSessionErrorEvent(sid, {
        name: "APIError",
        data: { message: "read tcp 10.0.0.1:443: read: connection reset by peer" },
      });
      assertDeepEqual(attempted, ["openai/gpt-5", "openai/gpt-5"]);
      assertEqual(workspace.statusBySession[sid]?.type, "busy");

      // Retry #2 on 504 gateway timeout
      await handleSessionErrorEvent(sid, {
        name: "APIError",
        data: { message: "504 Gateway Timeout", statusCode: 504 },
      });
      assertDeepEqual(attempted, ["openai/gpt-5", "openai/gpt-5", "openai/gpt-5"]);
      assertEqual(workspace.statusBySession[sid]?.type, "busy");

      // Exhausted after 2 same-model retries -> settles as idle with error
      await handleSessionErrorEvent(sid, {
        name: "APIError",
        data: { message: "504 Gateway Timeout", statusCode: 504 },
      });
      assertEqual(attempted.length, 3);
      assertEqual(workspace.statusBySession[sid]?.type, "idle");

      // If the user stops the session while a retry dispatch is in flight, it must stay idle.
      let abortedCount = 0;
      const origAbort = hoxiAPI.abortSession;
      hoxiAPI.abortSession = async () => {
        abortedCount++;
      };
      try {
        clearAutoPoolCooldowns("free");
        await dispatchSessionPrompt("ping2", sid, { forceImmediate: true });
        hoxiAPI.sendPromptAsync = async () => {
          markSessionInterrupted(sid, "stopped");
        };
        await handleSessionErrorEvent(sid, {
          name: "APIError",
          data: { message: "502 Bad Gateway", statusCode: 502 },
        });
        assertEqual(workspace.statusBySession[sid]?.type, "idle");
        assertEqual(isAutoFailoverPending(sid), false);
        assertEqual(abortedCount, 1);
      } finally {
        hoxiAPI.abortSession = origAbort;
      }
      // Clearing dispatch state on normal turn completion or session purge must prevent late errors from re-sending
      clearAutoPoolCooldowns("free");
      await dispatchSessionPrompt("completed-turn", sid, { forceImmediate: true });
      const countAfterSend = attempted.length;
      clearTurnDispatchState(sid);
      await handleSessionErrorEvent(sid, {
        name: "APIError",
        data: { message: "502 Bad Gateway", statusCode: 502 },
      });
      assertEqual(attempted.length, countAfterSend);

      clearAutoPoolCooldowns("free");
      await dispatchSessionPrompt("purged-session-turn", sid, { forceImmediate: true });
      const countAfterSecondSend = attempted.length;
      purgeSessionsFromStoreState(workspace, [sid]);
      await handleSessionErrorEvent(sid, {
        name: "APIError",
        data: { message: "502 Bad Gateway", statusCode: 502 },
      });
      assertEqual(attempted.length, countAfterSecondSend);
    } finally {
      clearAutoFailoverPending(sid);
      Object.assign(hoxiAPI, { sendPromptAsync: orig.send, getMessages: orig.msgs, getSessionDiff: orig.diff, renameSession: orig.ren, getProviderCandidates: orig.get, reportCandidateOutcome: orig.report, summarizeSessionTitle: orig.sum });
      setWorkspace("activeSessionID", null);
      setWorkspace("sessions", []);
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("messagesBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
      clearAutoPoolCooldowns("free");
      await resetAutoPoolToDefault("free");
      applyProviderBreakers({});
    }
  });

  it("retries the warm sticky candidate once on transient network/5xx errors in a multi-turn session before switching pool candidates", async () => {
    const orig = { send: hoxiAPI.sendPromptAsync, msgs: hoxiAPI.getMessages, diff: hoxiAPI.getSessionDiff, ren: hoxiAPI.renameSession, report: hoxiAPI.reportCandidateOutcome, get: hoxiAPI.getProviderCandidates, sum: hoxiAPI.summarizeSessionTitle };
    const sid = "ses_multiturn_cache_retry";
    try {
      Object.assign(hoxiAPI, { reportCandidateOutcome: async () => ({ ok: true }), getProviderCandidates: async () => ({ ok: true, breakers: {} }), getMessages: async () => [], getSessionDiff: async () => [], renameSession: async () => ({ ok: true } as never), summarizeSessionTitle: async () => ({ ok: true, title: "Test" }) });
      const model = (id: string) => ({ id, name: id, capabilities: { toolcall: true } });
      setWorkspace("providers", [
        { id: "alpha", name: "Alpha", models: { m: model("m") } },
        { id: "beta", name: "Beta", models: { m: model("m") } },
      ]);
      setWorkspace("connectedProviders", ["alpha", "beta"]);
      setWorkspace("sessions", [{ id: sid, title: "WarmCache", time: { created: 1, updated: 1 } } as never]);
      setWorkspace("activeSessionID", sid);
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: AUTO_PROVIDER_ID, modelID: "free" },
        effort: "",
      });
      setWorkspace("messagesBySession", sid, [
        {
          info: { id: "msg_u1", sessionID: sid, role: "user", time: { created: 1 } },
          parts: [{ id: "prt_u1", sessionID: sid, messageID: "msg_u1", type: "text", text: "first turn" }],
        },
        {
          info: {
            id: "msg_a1",
            sessionID: sid,
            role: "assistant",
            providerID: "alpha",
            modelID: "m",
            time: { created: 2, completed: 3 },
          },
          parts: [{ id: "prt_a1", sessionID: sid, messageID: "msg_a1", type: "text", text: "done" }],
        },
      ] as never);
      await saveAutoPool("free", {
        id: "free",
        preset: true,
        strategy: "priority",
        customized: true,
        candidates: [
          { providerID: "alpha", modelID: "m" },
          { providerID: "beta", modelID: "m" },
        ],
      });
      clearAutoPoolCooldowns("free");
      applyProviderBreakers({});

      const attempted: string[] = [];
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        attempted.push(`${opts?.model?.providerID}/${opts?.model?.modelID}`);
      };

      await dispatchSessionPrompt("second turn", sid, { forceImmediate: true });
      assertDeepEqual(attempted, ["alpha/m"]);

      // First transient 502 on a warm multi-turn session retries alpha/m once to preserve KV cache.
      await handleSessionErrorEvent(sid, {
        name: "APIError",
        data: { message: "502 Bad Gateway", statusCode: 502 },
      });
      assertDeepEqual(attempted, ["alpha/m", "alpha/m"]);
      assertEqual(isCandidateCoolingDown("alpha", "m"), false);

      // If the same-model retry also fails, it cools down alpha/m and switches to beta/m.
      await handleSessionErrorEvent(sid, {
        name: "APIError",
        data: { message: "502 Bad Gateway", statusCode: 502 },
      });
      assertDeepEqual(attempted, ["alpha/m", "alpha/m", "beta/m"]);
      assertTrue(isCandidateCoolingDown("alpha", "m"));
    } finally {
      clearAutoFailoverPending(sid);
      Object.assign(hoxiAPI, { sendPromptAsync: orig.send, getMessages: orig.msgs, getSessionDiff: orig.diff, renameSession: orig.ren, reportCandidateOutcome: orig.report, getProviderCandidates: orig.get, summarizeSessionTitle: orig.sum });
      setWorkspace("activeSessionID", null);
      setWorkspace("sessions", []);
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("messagesBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
      clearAutoPoolCooldowns("free");
      await resetAutoPoolToDefault("free");
      applyProviderBreakers({});
    }
  });

  it("immediately aborts retry backoff and re-dispatches with new model when selectModel is called during retry", async () => {
    const sid = "ses_manual_switch_during_retry";
    const origSend = hoxiAPI.sendPromptAsync;
    const origAbort = hoxiAPI.abortSession;
    const origGetMessages = hoxiAPI.getMessages;
    const origSummarize = hoxiAPI.summarizeSessionTitle;

    try {
      hoxiAPI.getMessages = async () => [];
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: false, title: "" });
      setWorkspace("providers", [
        { id: "alpha", name: "Alpha", models: { m1: { id: "m1", name: "M1" } } },
        { id: "beta", name: "Beta", models: { m2: { id: "m2", name: "M2" } } },
      ]);
      setWorkspace("connectedProviders", ["alpha", "beta"]);
      setWorkspace("sessions", [
        {
          id: sid,
          title: "Manual switch during retry",
          directory: "/tmp",
          time: { created: 1, updated: 1 },
        },
      ]);
      setWorkspace("activeSessionID", sid);
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: "alpha", modelID: "m1" },
        effort: "",
      });

      const events: string[] = [];
      hoxiAPI.abortSession = async (targetID) => {
        events.push(`abort:${targetID}`);
      };
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        events.push(`send:${opts?.model?.providerID}/${opts?.model?.modelID}`);
      };

      await dispatchSessionPrompt("hello world", sid, { forceImmediate: true });
      assertDeepEqual(events, ["send:alpha/m1"]);

      // Backend enters retry countdown for alpha/m1.
      setWorkspace("statusBySession", sid, {
        type: "retry",
        attempt: 1,
        message: "HTTP 429 rate limit",
        next: Date.now() + 10_000,
      });

      // User manually switches model in the input box to beta/m2 while retrying.
      selectModel({ providerID: "beta", modelID: "m2" });
      await new Promise((r) => setTimeout(r, 20));

      assertDeepEqual(events, ["send:alpha/m1", `abort:${sid}`, "send:beta/m2"]);
      assertEqual(workspace.statusBySession[sid]?.type, "busy");

      // A late MessageAbortedError from the aborted alpha/m1 turn must not flip beta/m2 back to idle.
      await handleSessionErrorEvent(sid, {
        name: "MessageAbortedError",
        data: { message: "The operation was aborted", kind: "aborted" },
      });
      assertEqual(workspace.statusBySession[sid]?.type, "busy");
    } finally {
      clearAutoFailoverPending(sid);
      clearTurnDispatchState(sid);
      hoxiAPI.sendPromptAsync = origSend;
      hoxiAPI.abortSession = origAbort;
      hoxiAPI.getMessages = origGetMessages;
      hoxiAPI.summarizeSessionTitle = origSummarize;
      setWorkspace("activeSessionID", null);
      setWorkspace("sessions", []);
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("messagesBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
    }
  });

  it("retries with manually selected model when switched while busy before error arrives", async () => {
    const sid = "ses_manual_switch_while_busy";
    const origSend = hoxiAPI.sendPromptAsync;
    const origGetMessages = hoxiAPI.getMessages;
    const origGetSessionDiff = hoxiAPI.getSessionDiff;
    const origRenameSession = hoxiAPI.renameSession;
    const origSummarize = hoxiAPI.summarizeSessionTitle;

    try {
      hoxiAPI.getMessages = async () => [];
      hoxiAPI.getSessionDiff = async () => [];
      hoxiAPI.renameSession = async () => ({ ok: true } as never);
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: false, title: "" });
      setWorkspace("providers", [
        { id: "alpha", name: "Alpha", models: { m1: { id: "m1", name: "M1" } } },
        { id: "beta", name: "Beta", models: { m2: { id: "m2", name: "M2" } } },
      ]);
      setWorkspace("connectedProviders", ["alpha", "beta"]);
      setWorkspace("globalConfig", {
        agent: { build: { model: "alpha/m1" } },
      });
      setWorkspace("sessions", [
        {
          id: sid,
          title: "Manual switch while busy",
          directory: "/tmp",
          time: { created: 1, updated: 1 },
        },
      ]);
      setWorkspace("activeSessionID", sid);
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: "alpha", modelID: "m1" },
        effort: "",
      });

      const attempted: string[] = [];
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        attempted.push(`${opts?.model?.providerID}/${opts?.model?.modelID}`);
      };

      await dispatchSessionPrompt("first prompt", sid, { forceImmediate: true });
      assertDeepEqual(attempted, ["alpha/m1"]);

      // User manually switches model in input box while the initial turn is still busy.
      selectModel({ providerID: "beta", modelID: "m2" });
      assertEqual(attempted.length, 1);

      // Initial turn then fails with 502; instead of same-model retrying alpha/m1, it switches to beta/m2.
      await handleSessionErrorEvent(sid, {
        name: "APIError",
        data: { message: "502 Bad Gateway", statusCode: 502 },
      });
      assertDeepEqual(attempted, ["alpha/m1", "beta/m2"]);

      // Manual retryLastUserPrompt also respects the session's selected model over globalConfig.agent.build.model.
      clearAutoFailoverPending(sid);
      clearTurnDispatchState(sid);
      setWorkspace("statusBySession", sid, { type: "idle" });
      await retryLastUserPrompt(sid);
      assertDeepEqual(attempted, ["alpha/m1", "beta/m2", "beta/m2"]);
    } finally {
      clearAutoFailoverPending(sid);
      clearTurnDispatchState(sid);
      hoxiAPI.sendPromptAsync = origSend;
      hoxiAPI.getMessages = origGetMessages;
      hoxiAPI.getSessionDiff = origGetSessionDiff;
      hoxiAPI.renameSession = origRenameSession;
      hoxiAPI.summarizeSessionTitle = origSummarize;
      setWorkspace("globalConfig", {});
      setWorkspace("activeSessionID", null);
      setWorkspace("sessions", []);
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("messagesBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
    }
  });

  it("returns null for a deleted custom auto pool and resets sessions pointing to it", async () => {
    const sid = "ses_custom_pool";
    try {
      setWorkspace("providers", [{ id: "alpha", name: "Alpha", models: { m1: { id: "m1", name: "M1" } } }]);
      setWorkspace("connectedProviders", ["alpha"]);
      await saveAutoPool("my-custom", {
        id: "my-custom", name: "My Custom", preset: false, strategy: "priority", customized: true,
        candidates: [{ providerID: "alpha", modelID: "m1" }],
      });
      setWorkspace("aiBySession", sid, { agent: "build", model: { providerID: AUTO_PROVIDER_ID, modelID: "my-custom" }, effort: "" });
      assertDeepEqual(resolveAutoModelTarget("my-custom", { sessionID: sid }), { providerID: "alpha", modelID: "m1" });

      await deleteCustomAutoPool("my-custom");
      assertEqual(resolveAutoModelTarget("my-custom", { sessionID: sid }), null);
      assertDeepEqual(workspace.aiBySession[sid]?.model, { providerID: AUTO_PROVIDER_ID, modelID: "free" });
    } finally {
      setWorkspace("aiBySession", reconcile({}));
    }
  });

  it("cuts off 5000s retry countdowns and fails over immediately on auto-pool rate-limit retry", async () => {
    const sid = "ses_cutoff_5000s_retry";
    const orig = {
      send: hoxiAPI.sendPromptAsync, abort: hoxiAPI.abortSession, msgs: hoxiAPI.getMessages,
      report: hoxiAPI.reportCandidateOutcome, cands: hoxiAPI.getProviderCandidates, sum: hoxiAPI.summarizeSessionTitle,
    };
    try {
      hoxiAPI.getMessages = async () => [];
      hoxiAPI.reportCandidateOutcome = async () => ({ ok: true });
      hoxiAPI.getProviderCandidates = async () => ({ ok: true, breakers: {} });
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: false, title: "" });
      setWorkspace("providers", [
        { id: "alpha", name: "Alpha", models: { m: { id: "m", name: "M" } } },
        { id: "beta", name: "Beta", models: { m: { id: "m", name: "M" } } },
      ]);
      setWorkspace("connectedProviders", ["alpha", "beta"]);
      setWorkspace("sessions", [{ id: sid, title: "Cutoff", time: { created: 1, updated: 1 } } as never]);
      setWorkspace("activeSessionID", sid);
      setWorkspace("aiBySession", sid, { agent: "build", model: { providerID: AUTO_PROVIDER_ID, modelID: "free" }, effort: "" });
      await saveAutoPool("free", {
        id: "free", preset: true, strategy: "priority", customized: true,
        candidates: [{ providerID: "alpha", modelID: "m" }, { providerID: "beta", modelID: "m" }],
      });
      clearAutoPoolCooldowns("free");
      applyProviderBreakers({});

      const events: string[] = [];
      hoxiAPI.abortSession = async (id) => { events.push(`abort:${id}`); };
      hoxiAPI.sendPromptAsync = async (_id, _text, opts) => { events.push(`send:${opts?.model?.providerID}/${opts?.model?.modelID}`); };

      await dispatchSessionPrompt("run task", sid, { forceImmediate: true });
      assertTrue(handleSessionRetryStatus(sid, { type: "retry", attempt: 1, message: "Rate limit exceeded", next: Date.now() + 5_400_000 }));
      await new Promise((r) => setTimeout(r, 20));
      assertDeepEqual(events, ["send:alpha/m", `abort:${sid}`, "send:beta/m"]);
      assertTrue(isCandidateCoolingDown("alpha", "m"));
    } finally {
      clearAutoFailoverPending(sid);
      clearTurnDispatchState(sid);
      Object.assign(hoxiAPI, {
        sendPromptAsync: orig.send, abortSession: orig.abort, getMessages: orig.msgs,
        reportCandidateOutcome: orig.report, getProviderCandidates: orig.cands, summarizeSessionTitle: orig.sum,
      });
      setWorkspace("activeSessionID", null);
      setWorkspace("sessions", []);
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("messagesBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
      clearAutoPoolCooldowns("free");
      await resetAutoPoolToDefault("free");
      applyProviderBreakers({});
    }
  });
});
