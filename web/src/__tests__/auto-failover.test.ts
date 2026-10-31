// web/src/__tests__/auto-failover.test.ts
import { reconcile } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import {
  AUTO_PROVIDER_ID,
  applyProviderBreakers,
  clearAutoFailoverPending,
  getAutoCandidateCooldown,
  clearAutoPoolCooldowns,
  decideAutoFailover,
  dispatchSessionPrompt,
  findAutoFailoverNotice,
  handleSessionErrorEvent,
  isAutoFailoverPending,
  isCandidateCoolingDown,
  parseQuotaResetMs,
  resetAutoPoolToDefault,
  saveAutoPool,
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
    const origSend = hoxiAPI.sendPromptAsync;
    const origGetMessages = hoxiAPI.getMessages;
    const origReport = hoxiAPI.reportCandidateOutcome;
    const origGet = hoxiAPI.getProviderCandidates;
    const origSummarize = hoxiAPI.summarizeSessionTitle;
    const sid = "ses_failover_429";
    try {
      hoxiAPI.reportCandidateOutcome = async () => ({ ok: true });
      hoxiAPI.getProviderCandidates = async () => ({ ok: true, breakers: {} });
      hoxiAPI.getMessages = async () => [];
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: true, title: "Test" });
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
      hoxiAPI.sendPromptAsync = origSend;
      hoxiAPI.getMessages = origGetMessages;
      hoxiAPI.reportCandidateOutcome = origReport;
      hoxiAPI.getProviderCandidates = origGet;
      hoxiAPI.summarizeSessionTitle = origSummarize;
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
    const origSend = hoxiAPI.sendPromptAsync;
    const origGetMessages = hoxiAPI.getMessages;
    const origReport = hoxiAPI.reportCandidateOutcome;
    const origGet = hoxiAPI.getProviderCandidates;
    const origSummarize = hoxiAPI.summarizeSessionTitle;
    const sid = "ses_failover_sync_reject";
    try {
      hoxiAPI.reportCandidateOutcome = async () => ({ ok: true });
      hoxiAPI.getProviderCandidates = async () => ({ ok: true, breakers: {} });
      hoxiAPI.getMessages = async () => [];
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: true, title: "Test" });
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
      hoxiAPI.sendPromptAsync = origSend;
      hoxiAPI.getMessages = origGetMessages;
      hoxiAPI.reportCandidateOutcome = origReport;
      hoxiAPI.getProviderCandidates = origGet;
      hoxiAPI.summarizeSessionTitle = origSummarize;
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

  it("retries a non-auto single-model turn up to 2 times on transient network or server errors", async () => {
    const origSend = hoxiAPI.sendPromptAsync;
    const origGetMessages = hoxiAPI.getMessages;
    const origSummarize = hoxiAPI.summarizeSessionTitle;
    const sid = "ses_same_model_retry";
    try {
      hoxiAPI.getMessages = async () => [];
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: true, title: "Test" });
      const model = (id: string) => ({ id, name: id, capabilities: { toolcall: true } });
      setWorkspace("providers", [{ id: "openai", name: "OpenAI", models: { "gpt-5": model("gpt-5") } }]);
      setWorkspace("connectedProviders", ["openai"]);
      setWorkspace("sessions", [{ id: sid, title: "Retry", time: { created: 1, updated: 1 } } as never]);
      setWorkspace("activeSessionID", sid);
      setWorkspace("aiBySession", sid, {
        agent: "build",
        model: { providerID: "openai", modelID: "gpt-5" },
        effort: "",
      });

      const attempted: string[] = [];
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        attempted.push(`${opts?.model?.providerID}/${opts?.model?.modelID}`);
      };

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
    } finally {
      clearAutoFailoverPending(sid);
      hoxiAPI.sendPromptAsync = origSend;
      hoxiAPI.getMessages = origGetMessages;
      hoxiAPI.summarizeSessionTitle = origSummarize;
      setWorkspace("activeSessionID", null);
      setWorkspace("sessions", []);
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("messagesBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
    }
  });
});
