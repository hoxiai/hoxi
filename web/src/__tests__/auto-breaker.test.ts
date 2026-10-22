// web/src/__tests__/auto-breaker.test.ts
import { reconcile } from "solid-js/store";
import { hoxiAPI, type CandidateBreakerSnapshot } from "../services/api";
import {
  applyProviderBreakers,
  AUTO_BREAKERS_STORAGE_KEY,
  AUTO_PROVIDER_ID,
  clearAutoPoolCooldowns,
  dispatchSessionPrompt,
  getCandidateBreaker,
  handleSessionErrorEvent,
  isCandidateBreakerOpen,
  isCandidateCoolingDown,
  reportAutoCandidateOutcome,
  resolveAutoModelTarget,
  resetAutoPoolToDefault,
  saveAutoPool,
  setWorkspace,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

function snap(
  providerID: string,
  modelID: string,
  score: number,
  latencyMs?: number
): CandidateBreakerSnapshot {
  return {
    providerID,
    modelID,
    phase: "closed",
    open: false,
    score,
    latencyEwmaMs: latencyMs,
    consecutiveFailures: 0,
    updatedAt: Date.now(),
  };
}

describe("shared candidate circuit breaker in auto-pool selection", () => {
  it("skips a candidate whose breaker is open and reports failures optimistically", async () => {
    setWorkspace("providers", [
      { id: "alpha", name: "Alpha", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
      { id: "beta", name: "Beta", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
    ]);
    setWorkspace("connectedProviders", ["alpha", "beta"]);
    applyProviderBreakers({});

    await saveAutoPool("free", {
      id: "free",
      preset: true,
      strategy: "round-robin",
      customized: true,
      candidates: [
        { providerID: "alpha", modelID: "m" },
        { providerID: "beta", modelID: "m" },
      ],
    });

    reportAutoCandidateOutcome({ providerID: "alpha", modelID: "m", ok: false, message: "429", statusCode: 429 });
    assertTrue(isCandidateBreakerOpen("alpha", "m"));
    assertTrue(isCandidateCoolingDown("alpha", "m"));
    // The optimistic open must carry a bounded expiry, never an indefinite one.
    const optimistic = getCandidateBreaker("alpha", "m");
    assertTrue(Boolean(optimistic?.openUntil && optimistic.openUntil > Date.now()));

    const target = resolveAutoModelTarget("free", { advance: true });
    assertDeepEqual(target, { providerID: "beta", modelID: "m" });

    await resetAutoPoolToDefault("free");
    applyProviderBreakers({});
  });

  it("ranks the higher-scoring candidate first in a smart-health pool", async () => {
    setWorkspace("providers", [
      { id: "alpha", name: "Alpha", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
      { id: "beta", name: "Beta", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
    ]);
    setWorkspace("connectedProviders", ["alpha", "beta"]);

    await saveAutoPool("frontier", {
      id: "frontier",
      preset: true,
      strategy: "smart-health",
      customized: true,
      candidates: [
        { providerID: "alpha", modelID: "m" },
        { providerID: "beta", modelID: "m" },
      ],
    });

    applyProviderBreakers({
      "alpha/m": snap("alpha", "m", 0.92),
      "beta/m": snap("beta", "m", 0.18),
    });

    const target = resolveAutoModelTarget("frontier", { advance: true });
    assertDeepEqual(target, { providerID: "alpha", modelID: "m" });

    await resetAutoPoolToDefault("frontier");
    applyProviderBreakers({});
    assertEqual(isCandidateBreakerOpen("alpha", "m"), false);
  });

  it("skips a much slower candidate even in a round-robin pool", async () => {
    setWorkspace("providers", [
      { id: "alpha", name: "Alpha", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
      { id: "beta", name: "Beta", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
    ]);
    setWorkspace("connectedProviders", ["alpha", "beta"]);

    await saveAutoPool("free", {
      id: "free",
      preset: true,
      strategy: "round-robin",
      customized: true,
      candidates: [
        { providerID: "alpha", modelID: "m" },
        { providerID: "beta", modelID: "m" },
      ],
    });

    applyProviderBreakers({
      "alpha/m": snap("alpha", "m", 0.9, 9000),
      "beta/m": snap("beta", "m", 0.9, 700),
    });

    const target = resolveAutoModelTarget("free", { advance: true });
    assertDeepEqual(target, { providerID: "beta", modelID: "m" });

    await resetAutoPoolToDefault("free");
    applyProviderBreakers({});
  });

  it("ignores HTTP 400 locally, clears optimistic open on ok=true, and syncs backend breakers", async () => {
    assertEqual(AUTO_BREAKERS_STORAGE_KEY, "hoxi:auto-breakers:v1");
    const origReport = hoxiAPI.reportCandidateOutcome;
    const origGet = hoxiAPI.getProviderCandidates;
    try {
      applyProviderBreakers({});
      hoxiAPI.reportCandidateOutcome = async () => ({ ok: true });
      hoxiAPI.getProviderCandidates = async () => ({ ok: true, breakers: {} });

      reportAutoCandidateOutcome({
        providerID: "alpha",
        modelID: "m",
        ok: false,
        statusCode: 400,
        message: "bad request",
      });
      assertEqual(isCandidateBreakerOpen("alpha", "m"), false, "400 must not open breaker locally");

      reportAutoCandidateOutcome({
        providerID: "alpha",
        modelID: "m",
        ok: false,
        statusCode: 503,
        message: "overloaded",
      });
      assertEqual(isCandidateBreakerOpen("alpha", "m"), true, "503 opens breaker optimistically");

      reportAutoCandidateOutcome({
        providerID: "alpha",
        modelID: "m",
        ok: true,
        latencyMs: 320,
      });
      assertEqual(isCandidateBreakerOpen("alpha", "m"), false, "ok=true clears optimistic open state");

      let synced = 0;
      hoxiAPI.getProviderCandidates = async () => {
        synced++;
        return {
          ok: true,
          breakers: {
            "alpha/m": snap("alpha", "m", 0.95, 310),
          },
        };
      };
      reportAutoCandidateOutcome({
        providerID: "alpha",
        modelID: "m",
        ok: true,
        latencyMs: 310,
      });
      await new Promise((r) => setTimeout(r, 10));
      assertTrue(synced >= 1, "reportAutoCandidateOutcome must reconcile via syncProviderBreakers");
      assertEqual(getCandidateBreaker("alpha", "m")?.latencyEwmaMs, 310);
    } finally {
      hoxiAPI.reportCandidateOutcome = origReport;
      hoxiAPI.getProviderCandidates = origGet;
      applyProviderBreakers({});
    }
  });

  it("cools down and skips broken auto-pool candidates on synchronous sendPromptAsync 409 or engine-not-loaded error event", async () => {
    const origSend = hoxiAPI.sendPromptAsync;
    const origGetMessages = hoxiAPI.getMessages;
    const origReport = hoxiAPI.reportCandidateOutcome;
    const origGet = hoxiAPI.getProviderCandidates;
    const origSummarize = hoxiAPI.summarizeSessionTitle;
    try {
      hoxiAPI.reportCandidateOutcome = async () => ({ ok: true });
      hoxiAPI.getProviderCandidates = async () => ({ ok: true, breakers: {} });
      hoxiAPI.getMessages = async () => [];
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: true, title: "Test" });

      setWorkspace("providers", [
        { id: "alpha", name: "Alpha", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
        { id: "beta", name: "Beta", models: { m: { id: "m", name: "M", capabilities: { toolcall: true } } } },
      ]);
      setWorkspace("connectedProviders", ["alpha", "beta"]);
      setWorkspace("sessions", [
        { id: "ses_auto_err_1", title: "Existing Session 1", time: { created: 1, updated: 1 } } as any,
        { id: "ses_auto_err_2", title: "Existing Session 2", time: { created: 1, updated: 1 } } as any,
      ]);
      setWorkspace("activeSessionID", "ses_auto_err_1");
      setWorkspace("aiBySession", "ses_auto_err_1", {
        agent: "build",
        model: { providerID: AUTO_PROVIDER_ID, modelID: "free" },
        effort: "",
      });
      setWorkspace("aiBySession", "ses_auto_err_2", {
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
        ],
      });
      clearAutoPoolCooldowns("free");
      applyProviderBreakers({});

      const attempted: string[] = [];
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        const key = `${opts?.model?.providerID}/${opts?.model?.modelID}`;
        attempted.push(key);
        if (key === "alpha/m") {
          throw new Error('HTTP 409: {"error":"model alpha/m is not loaded in the running engine"}');
        }
      };

      let rejected = false;
      try {
        await dispatchSessionPrompt("hello", "ses_auto_err_1", { forceImmediate: true });
      } catch {
        rejected = true;
      }
      assertEqual(rejected, true);
      assertTrue(isCandidateCoolingDown("alpha", "m"), "synchronous 409 must cool down alpha/m");
      assertTrue(isCandidateBreakerOpen("alpha", "m"), "synchronous 409 must open breaker for alpha/m");

      // Next dispatch skips alpha/m and uses beta/m
      await dispatchSessionPrompt("hello again", "ses_auto_err_1", { forceImmediate: true });
      assertDeepEqual(attempted, ["alpha/m", "beta/m"]);

      // Reset and verify async handleSessionErrorEvent on a fresh session also fails over on "is not loaded in the running engine"
      clearAutoPoolCooldowns("free");
      applyProviderBreakers({});
      attempted.length = 0;
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        attempted.push(`${opts?.model?.providerID}/${opts?.model?.modelID}`);
      };
      await dispatchSessionPrompt("turn 3", "ses_auto_err_2", {
        model: { providerID: AUTO_PROVIDER_ID, modelID: "free" },
        forceImmediate: true,
      });
      assertEqual(attempted[0], "alpha/m");
      await handleSessionErrorEvent("ses_auto_err_2", {
        name: "UnknownError",
        data: { message: "model alpha/m is not loaded in the running engine" },
      });
      assertDeepEqual(attempted, ["alpha/m", "beta/m"], "error event must fail over to beta/m");
      assertTrue(isCandidateBreakerOpen("alpha", "m"));
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
});
