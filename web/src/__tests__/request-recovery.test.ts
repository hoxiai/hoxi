import { hoxiAPI } from "../services/api";
import { RequestOutcomeUnknownError } from "../services/request-ledger";
import {
  dismissPendingPrompt,
  getPendingPrompt,
  isPendingPromptStorageCorrupt,
  recheckPendingPrompt,
  recoverPendingPrompt,
  resetCorruptPendingPrompts,
  sendRecoverablePrompt,
} from "../store/request-recovery";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

const KEY = "hoxi.pending-prompts:v1";

// Node has no browser storage. Install a real Storage-shaped in-memory fixture
// before registered suites run; production must still fail closed on quota errors.
if (typeof localStorage === "undefined") {
  const data = new Map<string, string>();
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
    get length() { return data.size; },
    clear: () => data.clear(),
    getItem: (key: string) => data.get(key) ?? null,
    key: (index: number) => [...data.keys()][index] ?? null,
    removeItem: (key: string) => { data.delete(key); },
    setItem: (key: string, value: string) => { data.set(key, value); },
  } satisfies Storage });
}

describe("durable prompt request identity", () => {
  it("persists before send, recovers a lost response with the original ID, and never replays", async () => {
    const send = hoxiAPI.sendPromptAsync;
    const fetch = globalThis.fetch;
    const saved = localStorage.getItem(KEY);
    let id = "";
    let posts = 0;
    try {
      localStorage.removeItem(KEY);
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        posts++;
        id = opts?.clientRequestId || "";
        const persisted = localStorage.getItem(KEY) || "";
        assertTrue(persisted.includes(id));
        assertEqual(persisted.includes("private prompt"), false);
        throw new RequestOutcomeUnknownError("unconfirmed", id);
      };
      try { await sendRecoverablePrompt("ses_pending", "private prompt", { directory: "/repo" }); } catch {}
      assertTrue(id.startsWith("prompt_"));
      globalThis.fetch = async (input) => {
        assertTrue(String(input).includes(encodeURIComponent(id)));
        return new Response(JSON.stringify({ ok: true, kind: "prompt", scope: "ses_pending", clientRequestId: id, status: "completed" }));
      };
      // Simulates a fresh controller reading only durable IDs, with no turn state.
      assertEqual(await recoverPendingPrompt("ses_pending"), true);
      assertEqual(posts, 1);
      assertEqual(localStorage.getItem(KEY), "[]");
    } finally {
      hoxiAPI.sendPromptAsync = send;
      globalThis.fetch = fetch;
      if (saved === null) localStorage.removeItem(KEY); else localStorage.setItem(KEY, saved);
    }
  });

  it("blocks changed payload/model while outcome remains unknown and preserves ID", async () => {
    const send = hoxiAPI.sendPromptAsync;
    const fetch = globalThis.fetch;
    const saved = localStorage.getItem(KEY);
    try {
      localStorage.setItem(KEY, JSON.stringify([{ sessionID: "ses_pending", directory: "/repo", clientRequestId: "original" }]));
      let posts = 0;
      hoxiAPI.sendPromptAsync = async () => { posts++; };
      globalThis.fetch = async () => new Response(JSON.stringify({ ok: true, kind: "prompt", scope: "ses_pending", clientRequestId: "original", status: "outcome_unknown" }));
      let caught: unknown;
      try { await sendRecoverablePrompt("ses_pending", "changed", { model: { providerID: "other", modelID: "other" } }); } catch (err) { caught = err; }
      assertTrue(caught instanceof RequestOutcomeUnknownError);
      assertEqual(posts, 0);
      assertTrue((localStorage.getItem(KEY) || "").includes("original"));
    } finally {
      hoxiAPI.sendPromptAsync = send;
      globalThis.fetch = fetch;
      if (saved === null) localStorage.removeItem(KEY); else localStorage.setItem(KEY, saved);
    }
  });

  it("refuses to send when durable reservation fails", async () => {
    const send = hoxiAPI.sendPromptAsync;
    const saved = localStorage.getItem(KEY);
    try {
      localStorage.setItem(KEY, "invalid json");
      let posts = 0;
      hoxiAPI.sendPromptAsync = async () => { posts++; };
      try { await sendRecoverablePrompt("ses_bad", "text", {}); } catch {}
      assertEqual(posts, 0);
      assertEqual(localStorage.getItem(KEY), "invalid json");
    } finally {
      hoxiAPI.sendPromptAsync = send;
      if (saved === null) localStorage.removeItem(KEY); else localStorage.setItem(KEY, saved);
    }
  });

  it("does not mistake not_admitted for proof a delayed POST cannot execute", async () => {
    const fetch = globalThis.fetch;
    const saved = localStorage.getItem(KEY);
    try {
      localStorage.setItem(KEY, JSON.stringify([{ sessionID: "ses_delayed", directory: "/repo", clientRequestId: "delayed" }]));
      globalThis.fetch = async () => new Response(JSON.stringify({ ok: true, kind: "prompt", scope: "ses_delayed", clientRequestId: "delayed", status: "not_admitted" }));
      let caught: unknown;
      try { await recoverPendingPrompt("ses_delayed"); } catch (err) { caught = err; }
      assertTrue(caught instanceof RequestOutcomeUnknownError);
      assertTrue((localStorage.getItem(KEY) || "").includes("delayed"));
    } finally {
      globalThis.fetch = fetch;
      if (saved === null) localStorage.removeItem(KEY); else localStorage.setItem(KEY, saved);
    }
  });

  it("recovers a dropped POST response through the production API without another POST", async () => {
    const fetch = globalThis.fetch;
    const saved = localStorage.getItem(KEY);
    let posts = 0;
    let id = "";
    try {
      localStorage.removeItem(KEY);
      globalThis.fetch = async (url, init) => {
        if (init?.method === "POST") {
          posts++;
          const body = JSON.parse(String(init.body));
          id = body.clientRequestId;
          throw new TypeError("socket closed after acceptance");
        }
        assertTrue(String(url).includes(id));
        return new Response(JSON.stringify({ ok: true, kind: "prompt", scope: "ses_accepted", clientRequestId: id, status: "completed" }));
      };
      await sendRecoverablePrompt("ses_accepted", "original prompt", { directory: "/repo" });
      assertEqual(posts, 1);
      assertEqual(localStorage.getItem(KEY), "[]");
    } finally {
      globalThis.fetch = fetch;
      if (saved === null) localStorage.removeItem(KEY); else localStorage.setItem(KEY, saved);
    }
  });

  it("does not report accepted work as failed when storage cleanup fails", async () => {
    const send = hoxiAPI.sendPromptAsync;
    const saved = localStorage.getItem(KEY);
    const storage = globalThis.localStorage;
    const data = new Map<string, string>();
    let failCleanup = false;
    try {
      Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
        getItem: (key: string) => data.get(key) ?? null,
        setItem: (key: string, value: string) => {
          if (failCleanup) throw new Error("quota");
          data.set(key, value);
        },
      } });
      hoxiAPI.sendPromptAsync = async () => { failCleanup = true; };
      await sendRecoverablePrompt("ses_cleanup", "text", {});
      assertTrue((data.get(KEY) || "").includes("ses_cleanup"));
    } finally {
      hoxiAPI.sendPromptAsync = send;
      Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
      if (saved === null) localStorage.removeItem(KEY); else localStorage.setItem(KEY, saved);
    }
  });

  it("gives accepted failed-turn retries a fresh ID even if their error arrives before HTTP settles", async () => {
    const send = hoxiAPI.sendPromptAsync;
    const saved = localStorage.getItem(KEY);
    const ids: string[] = [];
    let acknowledge: () => void = () => {};
    let started: () => void = () => {};
    const admitted = new Promise<void>((resolve) => { started = resolve; });
    try {
      localStorage.removeItem(KEY);
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        ids.push(opts?.clientRequestId || "");
        if (ids.length === 1) {
          started();
          await new Promise<void>((resolve) => { acknowledge = resolve; });
        }
      };
      const first = sendRecoverablePrompt("ses_order", "task", {});
      await admitted;
      const retry = sendRecoverablePrompt("ses_order", "task", { continueTask: true });
      acknowledge();
      await first;
      await retry;
      assertEqual(ids.length, 2);
      assertTrue(ids[0] !== ids[1]);
    } finally {
      acknowledge();
      hoxiAPI.sendPromptAsync = send;
      if (saved === null) localStorage.removeItem(KEY); else localStorage.setItem(KEY, saved);
    }
  });

  it("never dispatches if storage refuses the initial reservation", async () => {
    const storage = globalThis.localStorage;
    const send = hoxiAPI.sendPromptAsync;
    let posts = 0;
    try {
      Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
        getItem: () => null,
        setItem: () => { throw new Error("quota exhausted"); },
      } });
      hoxiAPI.sendPromptAsync = async () => { posts++; };
      let caught = false;
      try { await sendRecoverablePrompt("ses_quota", "task", {}); } catch { caught = true; }
      assertTrue(caught);
      assertEqual(posts, 0);
    } finally {
      hoxiAPI.sendPromptAsync = send;
      Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
    }
  });

  it("does not POST a stale retry after cancellation during admission recovery", async () => {
    const send = hoxiAPI.sendPromptAsync;
    const saved = localStorage.getItem(KEY);
    let finish: () => void = () => {};
    let started: () => void = () => {};
    const ready = new Promise<void>((resolve) => { started = resolve; });
    let current = true;
    let posts = 0;
    try {
      localStorage.removeItem(KEY);
      hoxiAPI.sendPromptAsync = async () => {
        posts++;
        started();
        await new Promise<void>((resolve) => { finish = resolve; });
      };
      const first = sendRecoverablePrompt("ses_cancel", "first", {});
      await ready;
      const retry = sendRecoverablePrompt("ses_cancel", "retry", {}, () => current);
      current = false;
      finish();
      await first;
      let rejected = false;
      try { await retry; } catch { rejected = true; }
      assertTrue(rejected);
      assertEqual(posts, 1);
    } finally {
      finish();
      hoxiAPI.sendPromptAsync = send;
      if (saved === null) localStorage.removeItem(KEY); else localStorage.setItem(KEY, saved);
    }
  });

  it("hides pending prompt while in flight, surfaces it on outcome_unknown, and supports recheck + manual unlock", async () => {
    const send = hoxiAPI.sendPromptAsync;
    const fetch = globalThis.fetch;
    const saved = localStorage.getItem(KEY);
    let releaseSend: () => void = () => {};
    let started: () => void = () => {};
    const sendStarted = new Promise<void>((resolve) => { started = resolve; });
    let capturedID = "";
    let posts = 0;
    try {
      localStorage.removeItem(KEY);
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        posts++;
        capturedID = opts?.clientRequestId || "";
        started();
        await new Promise<void>((resolve) => { releaseSend = resolve; });
        throw new RequestOutcomeUnknownError("connection reset before confirmation", capturedID);
      };

      const pendingCall = sendRecoverablePrompt("ses_unlock", "first task", { directory: "/repo" });
      await sendStarted;
      // While the POST is actively in flight, the stuck-request banner must not fire.
      assertEqual(getPendingPrompt("ses_unlock"), null);
      assertEqual(dismissPendingPrompt("ses_unlock", capturedID), false);

      releaseSend();
      let caught: unknown;
      try { await pendingCall; } catch (err) { caught = err; }
      assertTrue(caught instanceof RequestOutcomeUnknownError);

      // Once settled in outcome_unknown, UI inspection exposes the record + error.
      const stuck = getPendingPrompt("ses_unlock");
      assertTrue(stuck !== null);
      assertEqual(stuck?.clientRequestId, capturedID);
      assertEqual(stuck?.lastError, "connection reset before confirmation");

      // Recheck when still outcome_unknown keeps the lock and updates lastError.
      globalThis.fetch = async () =>
        new Response(JSON.stringify({
          ok: true,
          kind: "prompt",
          scope: "ses_unlock",
          clientRequestId: capturedID,
          status: "outcome_unknown",
          errorMessage: "still indeterminate after restart",
        }));
      assertEqual(await recheckPendingPrompt("ses_unlock"), "still_unknown");
      assertEqual(getPendingPrompt("ses_unlock")?.lastError, "still indeterminate after restart");

      // Mismatched request ID cannot dismiss the lock.
      assertEqual(dismissPendingPrompt("ses_unlock", "wrong_id"), false);
      assertTrue(getPendingPrompt("ses_unlock") !== null);

      // Exact request ID unlocks the session without replaying the old POST.
      assertEqual(dismissPendingPrompt("ses_unlock", capturedID), true);
      assertEqual(getPendingPrompt("ses_unlock"), null);
      assertEqual(posts, 1);

      // Subsequent fresh prompt succeeds with a new ID.
      let secondID = "";
      hoxiAPI.sendPromptAsync = async (_sid, _text, opts) => {
        posts++;
        secondID = opts?.clientRequestId || "";
      };
      assertEqual(await sendRecoverablePrompt("ses_unlock", "new task", { directory: "/repo" }), "sent");
      assertEqual(posts, 2);
      assertTrue(Boolean(secondID) && secondID !== capturedID);
    } finally {
      releaseSend();
      hoxiAPI.sendPromptAsync = send;
      globalThis.fetch = fetch;
      if (saved === null) localStorage.removeItem(KEY); else localStorage.setItem(KEY, saved);
    }
  });

  it("exposes corrupt storage state and supports explicit manual reset", () => {
    const saved = localStorage.getItem(KEY);
    try {
      localStorage.setItem(KEY, "{not valid json");
      // Trigger a reactive refresh through reset check after reading directly.
      assertEqual(isPendingPromptStorageCorrupt(), true);
      resetCorruptPendingPrompts();
      assertEqual(isPendingPromptStorageCorrupt(), false);
      assertEqual(localStorage.getItem(KEY), "[]");
    } finally {
      if (saved === null) localStorage.removeItem(KEY); else localStorage.setItem(KEY, saved);
    }
  });
});
