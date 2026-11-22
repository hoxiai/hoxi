import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import {
  appSettings,
  clearSessionGoal,
  inferModelFastModeCapability,
  isPureLocalSlashCommand,
  parseComposerControlCommand,
  resolveEffectiveFastMode,
  selectModel,
  selectSession,
  sessionAIConfig,
  setAppSettings,
  setFastMode,
  setWorkspace,
  startNewThreadDraft,
  submitSessionPrompt,
  toggleFastMode,
  workspace,
  writeSessionGoal,
} from "../store/workspace";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

describe("fast mode capabilities, slash commands, and dispatch guardrails", () => {
  it("recognizes eligible frontier models and rejects economy, utility, or custom-relay endpoints", () => {
    assertEqual(inferModelFastModeCapability("anthropic", "claude-opus-5-5"), true);
    assertEqual(inferModelFastModeCapability("claude-code", "claude-opus-5-5"), true);
    assertEqual(inferModelFastModeCapability("openai", "gpt-5.4"), true);
    assertEqual(inferModelFastModeCapability("codex", "gpt-5.4-codex"), true);
    assertEqual(inferModelFastModeCapability("hoxi-auto", "coding-pool"), true);

    assertEqual(inferModelFastModeCapability("anthropic", "claude-sonnet-4-6"), false);
    assertEqual(inferModelFastModeCapability("anthropic", "claude-haiku-5-5"), false);
    assertEqual(inferModelFastModeCapability("openai", "gpt-5-mini"), false);
    assertEqual(inferModelFastModeCapability("openai", "gpt-5-nano"), false);
    assertEqual(inferModelFastModeCapability("openai", "gpt-oss-120b"), false);
    assertEqual(inferModelFastModeCapability("google", "gemini-2.5-flash"), false);
    assertEqual(
      inferModelFastModeCapability("anthropic", "claude-opus-5-5", null, null, "https://openrouter.ai/api/v1"),
      false
    );
  });

  it("parses /fast local control commands and inline prompt commands", () => {
    assertEqual(isPureLocalSlashCommand("/fast"), true);
    assertEqual(isPureLocalSlashCommand("/fast on"), true);
    assertEqual(isPureLocalSlashCommand("/fast off"), true);
    assertEqual(isPureLocalSlashCommand("/fast fix the race condition"), false);

    const parsedInline = parseComposerControlCommand("/fast fix the race condition");
    assertEqual(parsedInline?.kind, "fast");
    if (parsedInline?.kind === "fast") {
      assertEqual(parsedInline.action, "on");
      assertEqual(parsedInline.prompt, "fix the race condition");
    }
  });

  it("keeps fastMode session-scoped, suppresses unsupported models, and blocks goal/unattended spend", async () => {
    const savedWorkspace = structuredClone(unwrap(workspace));
    const savedApprovalMode = appSettings.approvalMode;
    const originalAPI = { ...hoxiAPI };
    const storageDescriptor = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
    const values = new Map<string, string>();
    const storage: Storage = {
      get length() { return values.size; },
      clear: () => values.clear(),
      getItem: (k) => values.get(k) ?? null,
      key: (i) => [...values.keys()][i] ?? null,
      removeItem: (k) => { values.delete(k); },
      setItem: (k, v) => { values.set(k, String(v)); },
    };
    Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });

    const sidA = "ses_fast_test_a";
    const sidB = "ses_fast_test_b";
    const sentCalls: Array<{ sid: string; text: string; fastMode?: boolean }> = [];

    try {
      setAppSettings("approvalMode", "auto");
      setWorkspace("aiBySession", reconcile({}));
      setWorkspace("queuedPromptsBySession", reconcile({}));
      setWorkspace("statusBySession", reconcile({}));
      setWorkspace("loadingMessagesBySession", reconcile({}));
      setWorkspace("providers", [
        {
          id: "anthropic",
          name: "Anthropic",
          models: {
            "claude-opus-5-5": { id: "claude-opus-5-5", name: "Claude Opus 5.5", reasoning: true },
            "claude-sonnet-4-6": { id: "claude-sonnet-4-6", name: "Claude Sonnet 4.6", reasoning: true },
          },
        } as any,
      ]);
      setWorkspace({
        rootDir: "/fast-ws",
        activeSessionID: sidA,
        sessions: [sidA, sidB].map((id) => ({
          id,
          parentID: "root",
          title: "Fast Test",
          directory: `/fast-ws/${id}`,
          time: { created: 1, updated: 1 },
        })),
      });
      for (const id of [sidA, sidB]) {
        setWorkspace("messagesBySession", id, [
          {
            info: { id: `u_${id}`, sessionID: id, role: "user", time: { created: 1 } },
            parts: [{ id: `p_${id}`, sessionID: id, messageID: `u_${id}`, type: "text", text: "init" }],
          },
        ]);
      }
      hoxiAPI.getMessages = async () => [];
      hoxiAPI.getSessionDiff = async () => [];
      hoxiAPI.sendPromptAsync = async (sid, text, opts) => {
        sentCalls.push({ sid, text, fastMode: opts?.fastMode });
      };

      selectModel({ providerID: "anthropic", modelID: "claude-opus-5-5" }, false);
      assertEqual(workspace.fastMode, false);

      // /fast toggles session A on without touching session B or dispatching a network prompt.
      await submitSessionPrompt("/fast", sidA);
      assertEqual(sentCalls.length, 0);
      assertEqual(Boolean(sessionAIConfig(sidA).fastMode), true);
      assertEqual(Boolean(sessionAIConfig(sidB).fastMode), false);

      await submitSessionPrompt("implement feature", sidA, { forceImmediate: true });
      assertEqual(sentCalls.length, 1);
      assertEqual(sentCalls[0].fastMode, true);

      // Switching to Sonnet suppresses fastMode on the wire even while session preference stays enabled.
      selectModel({ providerID: "anthropic", modelID: "claude-sonnet-4-6" }, false);
      assertEqual(
        resolveEffectiveFastMode({ providerID: "anthropic", modelID: "claude-sonnet-4-6" }, workspace.fastMode),
        false
      );
      await submitSessionPrompt("sonnet turn", sidA, { forceImmediate: true });
      assertEqual(sentCalls[1].fastMode, undefined);

      // Switching back to Opus restores effective fastMode, UNLESS /goal or unattended mode is active.
      selectModel({ providerID: "anthropic", modelID: "claude-opus-5-5" }, false);
      writeSessionGoal(sidA, {
        objective: "overnight refactor",
        status: "active",
        continuations: 1,
        updatedAt: 1,
      });
      await submitSessionPrompt("goal turn", sidA, { forceImmediate: true });
      assertEqual(sentCalls[2].fastMode, undefined);
      clearSessionGoal(sidA);

      setAppSettings("approvalMode", "unattended");
      await submitSessionPrompt("unattended turn", sidA, { forceImmediate: true });
      assertEqual(sentCalls[3].fastMode, undefined);
      setAppSettings("approvalMode", "auto");

      // /fast off turns it off cleanly without leaving extra false keys.
      await submitSessionPrompt("/fast off", sidA);
      assertTrue(!("fastMode" in sessionAIConfig(sidA)), "fastMode key must be omitted when disabled");

      // New-thread draft resets defaultAI.fastMode so brand-new threads never inherit 2x billing.
      setWorkspace("activeSessionID", null);
      toggleFastMode(null);
      assertEqual(workspace.fastMode, true);
      startNewThreadDraft();
      assertEqual(workspace.fastMode, false);
    } finally {
      clearSessionGoal(sidA);
      setAppSettings("approvalMode", savedApprovalMode);
      Object.assign(hoxiAPI, originalAPI);
      setWorkspace(reconcile(savedWorkspace));
      if (storageDescriptor) Object.defineProperty(globalThis, "localStorage", storageDescriptor);
    }
  });
});
