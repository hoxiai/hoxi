import { reconcile, unwrap } from "solid-js/store";
import { createComputed, createRoot } from "solid-js";
import { isServer } from "solid-js/web";
import { hoxiAPI } from "../services/api";
import {
  initializeSessionAI,
  persistSessionAI,
  restoreSessionAI,
  sessionAIConfig,
  setActiveAgent,
} from "../store/session-ai";
import { initialSessionAI, SESSION_AI_KEY, type SessionAIConfig } from "../store/workspace-state";
import {
  EFFORT_KEY, SELECTED_MODEL_KEY, selectModel, setReasoningEffort,
  setWorkspace, workspace, type MessageTurn,
  inferModelEffortCapabilities, clampReasoningEffort,
} from "../store/workspace";
import { forkFromMessage, removeSessionFromStore, selectSession } from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";
import { submitSessionPrompt } from "../store/session-goal";
import { drainNextQueuedPrompt, sendQueuedPromptNow } from "../store/workspace-session-messages";
import { enqueueSessionPrompt } from "../store/prompt-queue";

const defaults: SessionAIConfig = {
  model: { providerID: "default-provider", modelID: "default-model" }, agent: "build", effort: "medium",
};
const configA: SessionAIConfig = {
  model: { providerID: "provider-a", modelID: "model-a" }, agent: "plan", effort: "high",
};
const configB: SessionAIConfig = {
  model: { providerID: "provider-b", modelID: "model-b" }, agent: "review", effort: "low",
};
let nextID = 0;

async function withAIFixture(run: (a: string, b: string, storage: Storage) => void | Promise<void>) {
  const saved = structuredClone(unwrap(workspace));
  const originalAPI = { ...hoxiAPI };
  const storageDescriptor = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  const originalTimeout = globalThis.setTimeout;
  const originalFetch = globalThis.fetch;
  const values = new Map<string, string>();
  const storage: Storage = {
    get length() { return values.size; },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => { values.delete(key); },
    setItem: (key, value) => { values.set(key, String(value)); },
  };
  const a = `ses_ai_${++nextID}_a`;
  const b = `ses_ai_${nextID}_b`;
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
  // Follow-up refreshes must not run after the fixture has restored the real API.
  globalThis.setTimeout = (() => 0) as unknown as typeof setTimeout;
  globalThis.fetch = async () => { throw new Error("Unexpected network request in session AI test"); };
  try {
    setWorkspace("defaultAI", reconcile(structuredClone(defaults)));
    setWorkspace("aiBySession", reconcile({}));
    setWorkspace("queuedPromptsBySession", reconcile({}));
    setWorkspace("messagesBySession", reconcile({}));
    setWorkspace("statusBySession", reconcile({}));
    setWorkspace("worktreeBySession", reconcile({}));
    setWorkspace("providers", []);
    setWorkspace("loadingMessagesBySession", reconcile({}));
    setWorkspace({ rootDir: "/ai-test", activeSessionID: a, sessions: [a, b].map((id) => ({
      id, parentID: "fixture-parent", title: "Existing AI conversation", directory: `/ai-test/${id}`,
      time: { created: 1, updated: 1 },
    })) });
    hoxiAPI.getMessages = async () => [];
    hoxiAPI.getSessionDiff = async () => [];
    hoxiAPI.sendPromptAsync = async () => {};
    // An existing user turn prevents unrelated title-generation requests on dispatch.
    for (const id of [a, b]) setWorkspace("messagesBySession", id, [turn(id, { role: "user" })]);
    await run(a, b, storage);
  } finally {
    Object.assign(hoxiAPI, originalAPI);
    setWorkspace(reconcile(saved));
    globalThis.setTimeout = originalTimeout;
    globalThis.fetch = originalFetch;
    if (storageDescriptor) Object.defineProperty(globalThis, "localStorage", storageDescriptor);
    else Reflect.deleteProperty(globalThis, "localStorage");
  }
}

function turn(sessionID: string, info: Partial<MessageTurn["info"]> = {}): MessageTurn {
  return { info: { id: `msg_${sessionID}`, sessionID, role: "assistant", time: { created: 1 }, ...info }, parts: [] };
}

function assertDisplayed(config: SessionAIConfig) {
  assertDeepEqual(workspace.selectedModel, config.model);
  assertEqual(workspace.activeMode, config.agent);
  assertEqual(workspace.reasoningEffort, config.effort);
}

describe("session AI configuration", () => {
  it("rejects model-less OpenCode prompts before creation, queueing or dispatch", async () => {
    await withAIFixture(async (a) => {
      setWorkspace("defaultAI", "model", null);
      initializeSessionAI(a, { ...defaults, model: null });
      setWorkspace("statusBySession", a, { type: "busy" });
      let sends = 0;
      let creates = 0;
      hoxiAPI.sendPromptAsync = async () => { sends++; };
      hoxiAPI.createSession = async () => { creates++; throw new Error("must not create"); };
      const messages = structuredClone(unwrap(workspace.messagesBySession));
      for (const target of [a, null]) {
        setWorkspace("activeSessionID", target);
        let error: unknown;
        try { await submitSessionPrompt("no implicit model", target); }
        catch (caught) { error = caught; }
        assertTrue(error instanceof Error);
      }
      assertEqual(sends, 0);
      assertEqual(creates, 0);
      assertDeepEqual(workspace.queuedPromptsBySession, {});
      assertDeepEqual(workspace.messagesBySession, messages);
    });
  });

  it("restores the historical model while preserving effort and agent patched during history loading", async () => {
    await withAIFixture(async (a) => {
      let resolveHistory!: (turns: MessageTurn[]) => void;
      setWorkspace("messagesBySession", reconcile({}));
      hoxiAPI.getMessages = () => new Promise((resolve) => { resolveHistory = resolve; });
      const loading = selectSession(a);
      const wasUninitialized = workspace.aiBySession[a] === undefined;
      const wasLoading = workspace.loadingMessagesBySession[a];
      setReasoningEffort("low");
      setActiveAgent("user-patched-agent");
      resolveHistory([turn(a, { ...configA.model!, agent: "historical-agent" })]);
      await loading;
      assertEqual(wasUninitialized, true);
      assertEqual(wasLoading, true);
      const expected: SessionAIConfig = { model: configA.model, agent: "user-patched-agent", effort: "low" };
      assertDisplayed(expected);
      assertDeepEqual(initialSessionAI()[a], expected);
      assertDeepEqual(workspace.defaultAI, defaults);
      assertEqual(workspace.loadingMessagesBySession[a], false);
      restoreSessionAI(a, [turn(a, { ...configB.model!, agent: "stale-agent" })]);
      assertDeepEqual(sessionAIConfig(a), expected);
    });
  });

  it("blocks unavailable providers and models without silently replacing the session AI", async () => {
    await withAIFixture(async (a, b) => {
      initializeSessionAI(a, configA);
      initializeSessionAI(b, configB);
      setWorkspace("providers", [{ id: "provider-b", name: "Available provider",
        models: { "model-b": { id: "model-b" } } }]);
      let sends = 0;
      hoxiAPI.sendPromptAsync = async () => { sends++; };
      for (const model of [configA.model!, { providerID: "provider-b", modelID: "removed-model" }]) {
        selectModel(model);
        const ai = structuredClone(unwrap(workspace.aiBySession));
        const messages = structuredClone(unwrap(workspace.messagesBySession[a]));
        const persisted = initialSessionAI();
        let error: unknown;
        try { await submitSessionPrompt("must not send", a, { forceImmediate: true }); }
        catch (caught) { error = caught; }
        assertTrue(error instanceof Error, "unavailable model must reject dispatch");
        assertTrue((error as Error).message.includes(`${model.providerID}/${model.modelID}`));
        assertEqual(sends, 0);
        assertDeepEqual(workspace.aiBySession, ai);
        assertDeepEqual(initialSessionAI(), persisted);
        assertDeepEqual(workspace.defaultAI, defaults);
        assertDeepEqual(workspace.messagesBySession[a], messages);
        assertEqual(workspace.statusBySession[a], undefined);
      }
      await submitSessionPrompt("valid background model", b, { forceImmediate: true });
      assertEqual(sends, 1);
    });
  });

  it("tracks getter changes with Solid's client runtime, including switching dependency branches", async () => {
    await withAIFixture((a, b) => {
      initializeSessionAI(a, configA);
      initializeSessionAI(b, configB);
      createRoot((dispose) => {
        let displayed: SessionAIConfig | undefined;
        createComputed(() => {
          displayed = { model: workspace.selectedModel && { ...workspace.selectedModel },
            agent: workspace.activeMode, effort: workspace.reasoningEffort };
        });
        try {
          assertDeepEqual(displayed, configA);
          // Node's SSR runtime intentionally does not rerun computations.
          if (isServer) return;
          setWorkspace("activeSessionID", b);
          assertDeepEqual(displayed, configB);
          setReasoningEffort("high");
          assertDeepEqual(displayed, { ...configB, effort: "high" });
          setWorkspace("activeSessionID", null);
          assertDeepEqual(displayed, defaults);
          setActiveAgent("new-default");
          assertDeepEqual(displayed, { ...defaults, agent: "new-default" });
        } finally { dispose(); }
      });
    });
  });

  it("uses background B's multi-folder system context even when active project A changes before send", async () => {
    await withAIFixture(async (a, b) => {
      initializeSessionAI(a, configA);
      initializeSessionAI(b, configB);
      setWorkspace("rootDir", `/ai-test/${a}`);
      setWorkspace("savedWorkspaces", [a, b].map((id) => ({
        id, name: id, color: "blue", createdAt: "test", updatedAt: "test",
        folders: [
          { id: "primary", path: `/ai-test/${id}`, isPrimary: true, position: 0 },
          { id: "extra", path: `/extra/${id}`, isPrimary: false, position: 1 },
        ],
      })));
      let sentSystem = "";
      hoxiAPI.sendPromptAsync = async (_id, _text, options) => { sentSystem = options?.system || ""; };
      await submitSessionPrompt("background context", b, {
        forceImmediate: true,
        beforeSend: async () => { setWorkspace("rootDir", "/unrelated-project"); },
      });
      assertTrue(sentSystem.includes(`/ai-test/${b}`));
      assertTrue(sentSystem.includes(`/extra/${b}`));
      assertTrue(!sentSystem.includes(`/extra/${a}`));
      assertTrue(!sentSystem.includes("/unrelated-project"));
    });
  });

  it("keeps caller system instructions out of the visible user message", async () => {
    await withAIFixture(async (a) => {
      initializeSessionAI(a, configA);
      setWorkspace("rootDir", `/ai-test/${a}`);
      let sentText = "";
      let sentSystem = "";
      let optimistic = "";
      hoxiAPI.sendPromptAsync = async (_id, text, options) => {
        sentText = text;
        sentSystem = options?.system || "";
        optimistic = String(workspace.messagesBySession[a]?.at(-1)?.parts[0]?.type === "text"
          ? (workspace.messagesBySession[a]?.at(-1)?.parts[0] as any).text
          : "");
      };
      await submitSessionPrompt("commit the pending files", a, {
        forceImmediate: true,
        system: "End the commit message with the Hoxi trailer",
      });
      assertEqual(sentText, "commit the pending files");
      assertTrue(sentSystem.includes("End the commit message with the Hoxi trailer"));
      assertEqual(optimistic.includes("Hoxi trailer"), false, "system text must not leak into the timeline message");
    });
  });

  it("copies fork configuration independently and removes only the deleted session's persisted config", async () => {
    await withAIFixture(async (a, b) => {
      initializeSessionAI(a, configA);
      initializeSessionAI(b, configB);
      const child = `${b}_fork`;
      hoxiAPI.forkSession = async (id, messageID, directory) => {
        assertEqual(id, b);
        assertEqual(messageID, "boundary");
        assertEqual(directory, `/ai-test/${b}`);
        return { id: child, parentID: b, title: "Fork", directory, time: { created: 1, updated: 1 } };
      };
      assertEqual(await forkFromMessage(b, "boundary"), child);
      assertDisplayed(configB);
      setWorkspace("aiBySession", child, "model", "modelID", "fork-model");
      setActiveAgent("fork-agent");
      assertDeepEqual(sessionAIConfig(b), configB);
      removeSessionFromStore(child);
      assertEqual(workspace.aiBySession[child], undefined);
      assertEqual(initialSessionAI()[child], undefined);
      assertDeepEqual(sessionAIConfig(a), configA);
      assertDeepEqual(sessionAIConfig(b), configB);
    });
  });

  it("retains the target model, agent and effort across an asynchronous active-session switch", async () => {
    await withAIFixture(async (a, b) => {
      initializeSessionAI(a, configA);
      initializeSessionAI(b, configB);
      let release!: () => void;
      let started!: () => void;
      const gate = new Promise<void>((resolve) => { release = resolve; });
      const waiting = new Promise<void>((resolve) => { started = resolve; });
      const calls: Parameters<typeof hoxiAPI.sendPromptAsync>[] = [];
      hoxiAPI.sendPromptAsync = async (...args) => { calls.push(args); };
      const sending = submitSessionPrompt("async switch", b, {
        forceImmediate: true, beforeSend: async () => { started(); await gate; },
      });
      await waiting;
      setWorkspace("activeSessionID", a);
      setReasoningEffort("");
      setActiveAgent("changed-active-agent");
      release();
      await sending;
      assertEqual(calls[0][0], b);
      assertDeepEqual(calls[0][2]?.model, configB.model);
      assertEqual(calls[0][2]?.agent, configB.agent);
      assertEqual(calls[0][2]?.variant, configB.effort);
    });
  });

  it("does not let in-place model edits mutate an already enqueued snapshot", async () => {
    await withAIFixture((_a, b) => {
      initializeSessionAI(b, configB);
      enqueueSessionPrompt(b, "immutable snapshot");
      setWorkspace("aiBySession", b, "model", "modelID", "later-model");
      assertDeepEqual(workspace.queuedPromptsBySession[b][0].model, configB.model);
    });
  });

  it("keeps A and B model, agent and effort independent and restores them on switching", async () => {
    await withAIFixture(async (a, b) => {
      initializeSessionAI(a);
      initializeSessionAI(b);
      selectModel(configA.model!);
      setActiveAgent(configA.agent);
      setReasoningEffort(configA.effort);
      await selectSession(b);
      assertDisplayed(defaults);
      selectModel(configB.model!);
      setActiveAgent(configB.agent);
      setReasoningEffort(configB.effort);
      assertDisplayed(configB);
      await selectSession(a);
      assertDisplayed(configA);
      assertDeepEqual(sessionAIConfig(b), configB);
      assertDeepEqual(workspace.defaultAI, defaults);
      await selectSession(b);
      assertDisplayed(configB);
    });
  });

  it("updates an explicitly targeted background agent without changing the active session", async () => {
    await withAIFixture((a, b) => {
      initializeSessionAI(a, configA);
      initializeSessionAI(b, configB);
      setActiveAgent("background-agent", b);
      assertDisplayed(configA);
      assertDeepEqual(sessionAIConfig(b), { ...configB, agent: "background-agent" });
      assertDeepEqual(sessionAIConfig(), configA);
      assertDeepEqual(sessionAIConfig(null), defaults);
    });
  });

  it("changes defaults only for new sessions and persists default model and effort separately", async () => {
    await withAIFixture((a, b, storage) => {
      initializeSessionAI(a);
      setWorkspace("activeSessionID", null);
      selectModel(configB.model!);
      setActiveAgent(configB.agent);
      setReasoningEffort(configB.effort);
      assertDisplayed(configB);
      assertDeepEqual(sessionAIConfig(a), defaults);
      initializeSessionAI(b);
      assertDeepEqual(sessionAIConfig(b), configB);
      assertDeepEqual(JSON.parse(storage.getItem(SELECTED_MODEL_KEY)!), configB.model);
      assertEqual(storage.getItem(EFFORT_KEY), configB.effort);
      setWorkspace("activeSessionID", a);
      selectModel(configA.model!);
      setReasoningEffort(configA.effort);
      assertDeepEqual(workspace.defaultAI, configB);
      assertEqual(storage.getItem(EFFORT_KEY), configB.effort);
      assertDeepEqual(JSON.parse(storage.getItem(SELECTED_MODEL_KEY)!), configB.model);
    });
  });

  it("initializes once and copies the default model rather than sharing mutable state", async () => {
    await withAIFixture((a) => {
      initializeSessionAI(a);
      initializeSessionAI(a, configB);
      setWorkspace("defaultAI", "model", "modelID", "changed-default");
      assertDeepEqual(sessionAIConfig(a), defaults);
      setWorkspace("aiBySession", a, "model", "modelID", "changed-session");
      assertEqual(workspace.defaultAI.model?.modelID, "changed-default");
    });
  });

  it("restores the latest complete historical model and latest agent independently", async () => {
    await withAIFixture((a) => {
      const history = [
        turn(a, { providerID: "old-provider", modelID: "old-model", agent: "build" }),
        turn(a, { providerID: configA.model!.providerID, modelID: configA.model!.modelID }),
        turn(a, { agent: "historical-agent", modelID: "incomplete-model" }),
        turn(a),
      ];
      const before = structuredClone(history);
      restoreSessionAI(a, history);
      assertDeepEqual(sessionAIConfig(a), { ...defaults, model: configA.model, agent: "historical-agent" });
      assertDeepEqual(history, before);
      restoreSessionAI(a, [turn(a, { providerID: "stale", modelID: "stale", agent: "stale" })]);
      assertEqual(sessionAIConfig(a).agent, "historical-agent");
      assertDeepEqual(sessionAIConfig(a).model, configA.model);
    });
  });

  it("falls back to defaults for absent history and preserves saved configuration on history load", async () => {
    await withAIFixture(async (a, b) => {
      restoreSessionAI(a, []);
      assertDeepEqual(sessionAIConfig(a), defaults);
      initializeSessionAI(b, configB);
      hoxiAPI.getMessages = async (id, directory) => {
        assertEqual(directory, `/ai-test/${id}`);
        return [turn(id, { providerID: "history", modelID: "history", agent: "history" })];
      };
      await selectSession(b);
      assertDisplayed(configB);
      setWorkspace("aiBySession", reconcile({}));
      await selectSession(a);
      assertDisplayed({ ...defaults, model: { providerID: "history", modelID: "history" }, agent: "history" });
    });
  });

  it("round-trips persisted session configurations including a null model and automatic effort", async () => {
    await withAIFixture((a, b, storage) => {
      initializeSessionAI(a, configA);
      initializeSessionAI(b, { model: null, agent: "build", effort: "" });
      persistSessionAI();
      assertDeepEqual(JSON.parse(storage.getItem(SESSION_AI_KEY)!), unwrap(workspace.aiBySession));
      const restored = initialSessionAI();
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("aiBySession", reconcile(restored));
      assertDisplayed(configA);
      assertDeepEqual(sessionAIConfig(b), { model: null, agent: "build", effort: "" });
      setActiveAgent("updated-agent", a);
      assertEqual(initialSessionAI()[a].agent, "updated-agent");
    });
  });

  it("rejects malformed storage roots and filters invalid configurations without losing valid entries", async () => {
    await withAIFixture((_a, _b, storage) => {
      for (const raw of ["{", "null", "[]", '"text"', "42"]) {
        storage.setItem(SESSION_AI_KEY, raw);
        assertDeepEqual(initialSessionAI(), {});
      }
      storage.setItem(SESSION_AI_KEY, JSON.stringify({
        valid: configA, automatic: { model: null, agent: "build", effort: "" },
        nullEntry: null, missing: {}, emptyAgent: { ...configA, agent: "" },
        badAgent: { ...configA, agent: 1 }, badEffort: { ...configA, effort: "extreme" },
        badProvider: { ...configA, model: { providerID: 1, modelID: "model" } },
        badModel: { ...configA, model: { providerID: "provider" } },
      }));
      assertDeepEqual(initialSessionAI(), {
        valid: configA, automatic: { model: null, agent: "build", effort: "" },
      });
    });
  });

  it("tolerates unavailable storage while keeping in-memory updates usable", async () => {
    await withAIFixture((a, _b, storage) => {
      storage.getItem = () => { throw new Error("read denied"); };
      storage.setItem = () => { throw new Error("write denied"); };
      assertDeepEqual(initialSessionAI(), {});
      initializeSessionAI(a, configA);
      persistSessionAI();
      setActiveAgent("offline-agent");
      assertEqual(sessionAIConfig(a).agent, "offline-agent");
    });
  });

  it("dispatches to background B using B's model, agent, effort and directory rather than active A", async () => {
    await withAIFixture(async (a, b) => {
      initializeSessionAI(a, configA);
      initializeSessionAI(b, configB);
      const calls: Parameters<typeof hoxiAPI.sendPromptAsync>[] = [];
      hoxiAPI.sendPromptAsync = async (...args) => { calls.push(args); };
      await submitSessionPrompt("background prompt", b, { forceImmediate: true });
      assertEqual(calls.length, 1);
      const [id, text, options] = calls[0];
      assertEqual(id, b);
      assertEqual(text, "background prompt");
      assertDeepEqual(options?.model, configB.model);
      assertEqual(options?.agent, configB.agent);
      assertEqual(options?.variant, configB.effort);
      assertEqual(options?.directory, `/ai-test/${b}`);
      assertDisplayed(configA);
    });
  });

  it("snapshots the target configuration at enqueue and preserves it for manual and automatic dispatch", async () => {
    await withAIFixture(async (a, b) => {
      initializeSessionAI(a, configA);
      initializeSessionAI(b, configB);
      const manual = enqueueSessionPrompt(b, "manual snapshot");
      const automatic = enqueueSessionPrompt(b, "automatic snapshot");
      setWorkspace("activeSessionID", b);
      selectModel(configA.model!);
      setActiveAgent(configA.agent);
      setReasoningEffort(configA.effort);
      setWorkspace("activeSessionID", a);
      const calls: Parameters<typeof hoxiAPI.sendPromptAsync>[] = [];
      hoxiAPI.sendPromptAsync = async (...args) => { calls.push(args); };
      await sendQueuedPromptNow(b, manual.id);
      setWorkspace("statusBySession", b, { type: "idle" });
      await drainNextQueuedPrompt(b);
      assertEqual(calls.length, 2);
      for (const [id, , options] of calls) {
        assertEqual(id, b);
        assertDeepEqual(options?.model, configB.model);
        assertEqual(options?.agent, configB.agent);
        assertEqual(options?.variant, configB.effort);
      }
      assertEqual(calls[0][1], manual.text);
      assertEqual(calls[1][1], automatic.text);
      assertEqual(workspace.queuedPromptsBySession[b].length, 0);
    });
  });

  it("restores the queued prompt and preserves busy status when sendQueuedPromptNow fails on a running session", async () => {
    await withAIFixture(async (a) => {
      initializeSessionAI(a, configA);
      setWorkspace("statusBySession", a, { type: "busy" });
      const first = enqueueSessionPrompt(a, "first queued");
      const second = enqueueSessionPrompt(a, "second queued");

      hoxiAPI.sendPromptAsync = async () => {
        throw new Error("simulated network failure");
      };

      await sendQueuedPromptNow(a, second.id);

      assertEqual(workspace.statusBySession[a]?.type, "busy", "running session must remain busy after failed steer");
      const remaining = workspace.queuedPromptsBySession[a] ?? [];
      assertEqual(remaining.length, 2, "failed steer prompt is restored to the queue");
      assertEqual(remaining[0].id, first.id);
      assertEqual(remaining[1].id, second.id);
    });
  });

  it("infers vendor-level reasoning effort capabilities and clamps unsupported efforts", () => {
    const claude = inferModelEffortCapabilities("claude-code", "claude-sonnet", true);
    assertDeepEqual(claude.efforts, ["low", "medium", "high", "xhigh", "max"]);
    assertEqual(claude.defaultEffort, "high");

    const openai = inferModelEffortCapabilities("openai", "gpt-codex", true);
    assertDeepEqual(openai.efforts, ["low", "medium", "high", "xhigh"]);
    assertEqual(openai.defaultEffort, "high");
    assertEqual(clampReasoningEffort("max", openai.efforts), "xhigh");

    const haiku = inferModelEffortCapabilities("claude-code", "claude-haiku", false);
    assertDeepEqual(haiku.efforts, []);
    assertEqual(clampReasoningEffort("high", haiku.efforts), "");

    const agyLow = inferModelEffortCapabilities("antigravity", "gemini-pro-low", true);
    assertDeepEqual(agyLow.efforts, ["low", "medium", "high"]);
    assertEqual(agyLow.defaultEffort, "low");

    const gptOss = inferModelEffortCapabilities("antigravity", "gpt-oss-120b-medium", true);
    assertDeepEqual(gptOss.efforts, ["medium"]);
    assertEqual(gptOss.defaultEffort, "medium");
    assertEqual(clampReasoningEffort("max", gptOss.efforts), "medium");
  });

  it("synchronizes Antigravity effort-suffixed model slugs with reasoningEffort", async () => {
    await withAIFixture(async (a) => {
      setWorkspace("providers", [
        {
          id: "antigravity",
          name: "Antigravity",
          models: {
            "gemini-3-pro-low": { id: "gemini-3-pro-low", reasoning: true, efforts: ["low", "medium", "high", "max"], defaultEffort: "low" },
            "gemini-3-pro-high": { id: "gemini-3-pro-high", reasoning: true, efforts: ["low", "medium", "high", "max"], defaultEffort: "high" },
          },
        },
      ]);
      setWorkspace("activeSessionID", a);
      selectModel({ providerID: "antigravity", modelID: "gemini-3-pro-low" });
      assertEqual(workspace.reasoningEffort, "low");

      setReasoningEffort("high");
      assertEqual(workspace.reasoningEffort, "high");
      assertDeepEqual(workspace.selectedModel, { providerID: "antigravity", modelID: "gemini-3-pro-high" });
    });
  });

  it("synchronizes agent dedicated models when switching active agent", async () => {
    await withAIFixture(async (a) => {
      setWorkspace("globalConfig", {
        model: "default-provider/default-model",
        agent: {
          plan: { model: "anthropic/claude-3-7-sonnet" },
          build: { model: null },
        },
      });
      setWorkspace("activeSessionID", a);
      selectModel({ providerID: "default-provider", modelID: "default-model" });

      assertEqual(workspace.activeMode, "build");
      assertDeepEqual(workspace.selectedModel, { providerID: "default-provider", modelID: "default-model" });

      // Switch to plan mode which has a dedicated model
      setActiveAgent("plan", a);
      assertEqual(workspace.activeMode, "plan");
      assertDeepEqual(workspace.selectedModel, { providerID: "anthropic", modelID: "claude-3-7-sonnet" });

      // Switch back to build mode which follows default
      setActiveAgent("build", a);
      assertEqual(workspace.activeMode, "build");
      assertDeepEqual(workspace.selectedModel, { providerID: "default-provider", modelID: "default-model" });
    });
  });
});
