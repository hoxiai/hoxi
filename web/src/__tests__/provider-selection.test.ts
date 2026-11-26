import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI, authAPI, runnerAPI, type LocalCLIStatusItem, type ProviderInfo } from "../services/api";
import { loadProviders } from "../store/workspace-providers";
import { resolveSessionTargetRuntime } from "../store/session-query";
import {
  clearFreeOpenCodeSetupError,
  connectProviderWithKey,
  disconnectLocalCLIModel,
  disconnectProvider,
  enableFreeOpenCodeModels,
  freeOpenCodeSetupState,
  saveCustomProvider,
  syncLocalCLIModel,
} from "../store/provider-connections";
import { localCLIStatusItems, setLocalCLIStatusItems, setWorkspace, workspace } from "../store/workspace";
import { extractFlattenedModels, resolveEffectiveModelEffort } from "../store/provider-catalog";
import { desktopAuth, logoutDesktopAccount, setDesktopAuth } from "../store/auth-state";
import {
  defaultProviderNpm,
  extractCustomProviderFormState,
  PROVIDER_VARIANTS,
} from "../components/models/custom-endpoint-helpers";
import { assertDeepEqual, assertTrue, describe, it } from "./test-harness";

const model = { providerID: "ollama", modelID: "local-model" };
const providers: ProviderInfo[] = [
  { id: "opencode", name: "OpenCode", models: { "deepseek-v4.1-flash-free": { id: "deepseek-v4.1-flash-free" } } },
  { id: "unconnected", name: "Unconnected", models: { remote: { id: "remote" } } },
  { id: "ollama", name: "Ollama", models: { "local-model": { id: "local-model" } } },
];

async function withProviders(run: () => Promise<void>) {
  const saved = structuredClone(unwrap(workspace));
  const originalAPI = { ...hoxiAPI };
  const originalListModels = runnerAPI.listModels;
  try {
    setWorkspace("defaultAI", "model", null);
    setWorkspace("activeSessionID", null);
    hoxiAPI.listProviders = async () => ({ all: providers, connected: ["opencode"], default: {} });
    hoxiAPI.getGlobalConfig = async () => ({});
    hoxiAPI.getProviderAuthMethods = async () => ({});
    hoxiAPI.getHoxiModelsConfig = async () => ({ hoxiProviders: {}, openCodeProviders: [], configPath: "test" });
    runnerAPI.listModels = async () => { throw new Error("not installed"); };
    await run();
  } finally {
    Object.assign(hoxiAPI, originalAPI);
    runnerAPI.listModels = originalListModels;
    setWorkspace(reconcile(saved));
  }
}

describe("BYOK provider selection", () => {
  it("preserves runtime xhigh/max tiers when a saved Codex config only contains legacy tiers", async () => {
    await withProviders(async () => {
      const modelID = "gpt-6.1-sol";
      const variants = Object.fromEntries(["low", "medium", "high", "xhigh", "max"].map(
        (effort) => [effort, { reasoningEffort: effort }]
      ));
      const configuredVariants = {
        low: { reasoningEffort: "low" }, medium: { reasoningEffort: "medium" },
        high: { reasoningEffort: "high", reasoningSummary: "detailed" },
      };
      hoxiAPI.listProviders = async () => ({
        all: [{ id: "openai", name: "Codex", models: {
          [modelID]: { id: modelID, reasoning: true, variants },
        } }], connected: ["openai"], default: {},
      });
      hoxiAPI.getHoxiModelsConfig = async () => ({
        hoxiProviders: { openai: { models: {
          [modelID]: { reasoning: true, variants: configuredVariants },
        } } }, openCodeProviders: [], configPath: "test",
      });
      await loadProviders(true);
      const merged = workspace.providers.find((p) => p.id === "openai")!.models[modelID];
      assertDeepEqual(merged.variants?.high, configuredVariants.high);
      const catalog = extractFlattenedModels(workspace.providers, new Set(["openai"]), workspace.globalConfig);
      assertDeepEqual(catalog[0].efforts, ["low", "medium", "high", "xhigh", "max"]);
      for (const effort of ["xhigh", "max"] as const) {
        assertDeepEqual(resolveEffectiveModelEffort({ providerID: "openai", modelID }, effort), effort);
      }
    });
  });

  it("preserves Codex priority modelOrder and defaultModel over alphabetical JSON map keys", async () => {
    await withProviders(async () => {
      hoxiAPI.listProviders = async () => ({
        all: [{ id: "openai", name: "OpenAI", models: { "gpt-5": { id: "gpt-5" } } }],
        connected: ["openai"],
        default: { openai: "gpt-5" },
      });
      hoxiAPI.getHoxiModelsConfig = async () => ({
        hoxiProviders: {
          openai: {
            name: "OpenAI Codex (Local)",
            npm: "@ai-sdk/openai",
            options: {
              defaultModel: "gpt-6.1-sol",
              modelOrder: ["gpt-6.1-sol", "gpt-6-astra", "gpt-5.5"],
            },
            models: {
              "gpt-5.5": { name: "GPT-5.5 (Codex)", reasoning: true },
              "gpt-6-astra": { name: "GPT-6-Astra (Codex)", reasoning: true },
              "gpt-6.1-sol": { name: "GPT-6.1-Sol (Codex)", reasoning: true },
            },
          },
        },
        openCodeProviders: [],
        configPath: "test",
      });
      await loadProviders(true);
      assertDeepEqual(workspace.defaultModels.openai, "gpt-6.1-sol");
      assertDeepEqual(workspace.defaultAI.model, { providerID: "openai", modelID: "gpt-6.1-sol" });
      const catalog = extractFlattenedModels(workspace.providers, new Set(["openai"]), workspace.globalConfig);
      assertDeepEqual(
        catalog.map((m) => m.modelID),
        ["gpt-6.1-sol", "gpt-6-astra", "gpt-5.5"]
      );
    });
  });

  it("does not automatically select free OpenCode or an unconnected catalog model", async () => {
    await withProviders(async () => {
      await loadProviders();
      assertDeepEqual(workspace.defaultAI.model, null);
      hoxiAPI.listProviders = async () => ({ all: providers, connected: [], default: {} });
      await loadProviders();
      assertDeepEqual(workspace.defaultAI.model, null);
    });
  });

  it("preserves an explicitly selected no-key local model even without connected status", async () => {
    await withProviders(async () => {
      setWorkspace("defaultAI", "model", model);
      await loadProviders();
      assertDeepEqual(workspace.defaultAI.model, model);
    });
  });

  it("uses an explicitly configured default without requiring an API key", async () => {
    await withProviders(async () => {
      hoxiAPI.getGlobalConfig = async () => ({ model: "ollama/local-model" });
      await loadProviders();
      assertDeepEqual(workspace.defaultAI.model, model);
    });
  });

  it("can automatically select a connected user provider", async () => {
    await withProviders(async () => {
      hoxiAPI.listProviders = async () => ({ all: providers, connected: ["opencode", "ollama"], default: { ollama: "local-model" } });
      await loadProviders();
      assertDeepEqual(workspace.defaultAI.model, model);
    });
  });
});

const cli: LocalCLIStatusItem = {
  id: "codex-cli", name: "Codex", installed: true, authenticated: true,
  connected: true, providerID: "openai", defaultModel: "stale", sessionCount: 0,
};

describe("local CLI model sync", () => {
  it("selects the freshly read CLI model even when the current selection is valid", async () => {
    await withProviders(async () => {
      setWorkspace("defaultAI", "model", { providerID: "unconnected", modelID: "remote" });
      hoxiAPI.importLocalCLI = async () => ({ ok: true, toolID: cli.id, providerID: "ollama" });
      hoxiAPI.getLocalCLIStatus = async () => ({ tools: [{ ...cli, defaultModel: "local-model" }] });
      await syncLocalCLIModel(cli);
      assertDeepEqual(workspace.selectedModel, model);
      assertDeepEqual(workspace.recentModels[0], model);
    });
  });

  it("switches to a runner inside the current conversation without importing credentials", async () => {
    await withProviders(async () => {
      const runner = { ...cli, id: "claude-code", providerID: "claude-code", authMode: "claude-runner" } as LocalCLIStatusItem;
      setWorkspace("activeSessionID", "ses_existing");
      setWorkspace("aiBySession", "ses_existing", { ...workspace.defaultAI, model });
      hoxiAPI.importLocalCLI = async () => { throw new Error("must not import runner credentials"); };
      runnerAPI.listModels = async () => ({ installed: true, available: true, models: [{ id: "sonnet", name: "Sonnet" }], default: "sonnet" });
      hoxiAPI.getLocalCLIStatus = async () => ({ tools: [{ ...runner, defaultModel: "sonnet" }] });
      await syncLocalCLIModel(runner);
      assertDeepEqual(workspace.selectedModel, { providerID: "claude-code", modelID: "sonnet" });
      assertDeepEqual(workspace.activeSessionID, "ses_existing");
      assertDeepEqual(workspace.aiBySession.ses_existing.model, { providerID: "claude-code", modelID: "sonnet" });
    });
  });

  it("keeps the selected model when the CLI model is unavailable", async () => {
    await withProviders(async () => {
      setWorkspace("defaultAI", "model", model);
      hoxiAPI.importLocalCLI = async () => ({ ok: true, toolID: cli.id, providerID: "ollama" });
      hoxiAPI.getLocalCLIStatus = async () => ({ tools: [{ ...cli, defaultModel: "missing" }] });
      let failed = false;
      try { await syncLocalCLIModel(cli); } catch { failed = true; }
      assertDeepEqual(failed, true);
      assertDeepEqual(workspace.selectedModel, model);
    });
  });

  it("does not switch models after an import failure", async () => {
    await withProviders(async () => {
      setWorkspace("defaultAI", "model", model);
      hoxiAPI.importLocalCLI = async () => ({ ok: false, toolID: cli.id, providerID: "", error: "import failed" });
      let message = "";
      try { await syncLocalCLIModel(cli); } catch (err) { message = (err as Error).message; }
      assertDeepEqual(message, "import failed");
      assertDeepEqual(workspace.selectedModel, model);
    });
  });

  it("does not overwrite a running session model when syncing CLI or saving providers", async () => {
    await withProviders(async () => {
      const runningModel = { providerID: "ollama", modelID: "local-model" };
      setWorkspace("activeSessionID", "ses_busy");
      setWorkspace("statusBySession", "ses_busy", { type: "busy" });
      setWorkspace("aiBySession", "ses_busy", { ...workspace.defaultAI, model: runningModel });

      const runner = { ...cli, id: "claude-code", providerID: "claude-code", authMode: "claude-runner" } as LocalCLIStatusItem;
      runnerAPI.listModels = async () => ({
        installed: true,
        available: true,
        models: [{ id: "sonnet", name: "Sonnet" }],
        default: "sonnet",
      });
      hoxiAPI.getLocalCLIStatus = async () => ({ tools: [{ ...runner, defaultModel: "sonnet" }] });
      await syncLocalCLIModel(runner);

      // Active busy session keeps its model, while defaultAI and recentModels receive the synced model
      assertDeepEqual(workspace.aiBySession.ses_busy.model, runningModel);
      assertDeepEqual(workspace.selectedModel, runningModel);
      assertDeepEqual(workspace.defaultAI.model, { providerID: "claude-code", modelID: "sonnet" });
      assertDeepEqual(workspace.recentModels[0], { providerID: "claude-code", modelID: "sonnet" });

      hoxiAPI.setProviderAuth = async () => {};
      hoxiAPI.saveHoxiProviderConfig = async () => ({
        ok: true,
        hoxiProviders: {},
        configPath: "test",
      });
      hoxiAPI.getHoxiModelsConfig = async () => ({
        hoxiProviders: {
          "custom-gw": {
            name: "Custom GW",
            npm: "@ai-sdk/openai-compatible",
            options: { baseURL: "https://gw.example/v1" },
            models: { "gw-m1": { name: "GW M1", reasoning: true } },
          },
        },
        openCodeProviders: [],
        configPath: "test",
      });
      await connectProviderWithKey({
        providerID: "custom-gw",
        apiKey: "sk-test",
        baseURL: "https://gw.example/v1",
        customModelID: "gw-m1",
      });
      assertDeepEqual(workspace.aiBySession.ses_busy.model, runningModel);
      assertDeepEqual(workspace.defaultAI.model, { providerID: "custom-gw", modelID: "gw-m1" });
    });
  });

  it("does not hijack an unrelated provider selection when saving a custom provider", async () => {
    await withProviders(async () => {
      setWorkspace("activeSessionID", "ses_idle");
      setWorkspace("statusBySession", "ses_idle", { type: "idle" });
      setWorkspace("aiBySession", "ses_idle", { ...workspace.defaultAI, model });

      hoxiAPI.setProviderAuth = async () => {};
      hoxiAPI.saveHoxiProviderConfig = async () => ({
        ok: true,
        hoxiProviders: {},
        configPath: "test",
      });
      hoxiAPI.getHoxiModelsConfig = async () => ({
        hoxiProviders: {
          "other-custom": {
            name: "Other Custom",
            npm: "@ai-sdk/openai-compatible",
            options: { baseURL: "https://other.example/v1" },
            models: { "other-m1": { name: "Other M1", reasoning: true } },
          },
        },
        openCodeProviders: [],
        configPath: "test",
      });

      await saveCustomProvider({
        providerID: "other-custom",
        name: "Other Custom",
        npm: "@ai-sdk/openai-compatible",
        baseURL: "https://other.example/v1",
        apiKey: "sk-other",
        models: [{ id: "other-m1", name: "Other M1", reasoning: true, image: false }],
      });

      assertDeepEqual(workspace.selectedModel, model);
      assertDeepEqual(workspace.recentModels[0], { providerID: "other-custom", modelID: "other-m1" });
    });
  });

  it("persists apiKey in hoxi provider config and succeeds even if setProviderAuth fails", async () => {
    await withProviders(async () => {
      let savedCfg = null as Record<string, any> | null;
      let authKeyPassed = "";
      hoxiAPI.setProviderAuth = async (_pid, key) => {
        authKeyPassed = key;
        throw new Error("HTTP 502: opencode service unavailable");
      };
      hoxiAPI.saveHoxiProviderConfig = async (_pid, cfg) => {
        savedCfg = cfg;
        return { ok: true, hoxiProviders: { "win-custom": cfg }, configPath: "test" };
      };
      hoxiAPI.getHoxiModelsConfig = async () => ({
        hoxiProviders: savedCfg ? { "win-custom": savedCfg } : {},
        openCodeProviders: [],
        configPath: "test",
      });

      await saveCustomProvider({
        providerID: "win-custom",
        name: "Win Custom",
        npm: "@ai-sdk/openai-compatible",
        baseURL: "https://win.example/v1",
        apiKey: "sk-key-1, sk-key-2",
        models: [{ id: "win-m1", name: "Win M1", reasoning: true, image: true }],
      });

      assertDeepEqual(authKeyPassed, "sk-key-1");
      assertDeepEqual(savedCfg?.options?.apiKey, "sk-key-1, sk-key-2");
      assertDeepEqual(workspace.selectedModel, { providerID: "win-custom", modelID: "win-m1" });
    });
  });

  it("includes Google Gemini (@ai-sdk/google) in protocol variants and defaults google provider to @ai-sdk/google", async () => {
    await withProviders(async () => {
      assertTrue(PROVIDER_VARIANTS.some((v) => v.value === "@ai-sdk/google"));
      assertDeepEqual(defaultProviderNpm("google"), "@ai-sdk/google");
      assertDeepEqual(defaultProviderNpm("gemini"), "@ai-sdk/google");
      assertDeepEqual(defaultProviderNpm("anthropic"), "@ai-sdk/anthropic");
      assertDeepEqual(defaultProviderNpm("custom"), "@ai-sdk/openai-compatible");

      let savedGoogleCfg = null as Record<string, any> | null;
      hoxiAPI.setProviderAuth = async () => {};
      hoxiAPI.saveHoxiProviderConfig = async (_pid, cfg) => {
        savedGoogleCfg = cfg;
        return { ok: true, hoxiProviders: { google: cfg }, configPath: "test" };
      };
      await connectProviderWithKey({
        providerID: "google",
        apiKey: "AIzaSyTest",
        customModelID: "gemini-2.5-pro",
      });
      assertDeepEqual(savedGoogleCfg?.npm, "@ai-sdk/google");
      assertDeepEqual(extractCustomProviderFormState("google").npmPkg, "@ai-sdk/google");
    });
  });
});


describe("provider loading account isolation", () => {
  it("does not restore a provider snapshot after logout", async () => {
    const oldLogout = authAPI.logout;
    const oldAuth = { ...desktopAuth };
    try {
      await withProviders(async () => {
        setDesktopAuth({ isLoggedIn: true });
        let resolveOld!: (value: Awaited<ReturnType<typeof hoxiAPI.listProviders>>) => void;
        hoxiAPI.listProviders = () => new Promise((resolve) => { resolveOld = resolve; });
        authAPI.logout = async () => ({ success: true });
        const pending = loadProviders(true);
        await logoutDesktopAccount();
        setWorkspace("providers", []);
        resolveOld({ all: providers, connected: ["ollama"], default: {} });
        await pending;
        assertDeepEqual(workspace.providers, []);
        assertDeepEqual(workspace.defaultAI.model, null);
      });
    } finally {
      authAPI.logout = oldLogout;
      setDesktopAuth(oldAuth);
    }
  });
});

describe("provider disconnect and CLI re-import lifecycle", () => {
  it("disconnects Hoxi or OpenCode-imported providers, refreshes CLI status, and reconciles active session model", async () => {
    const savedCLI = localCLIStatusItems();
    try {
      await withProviders(async () => {
        let disconnectedID = "";
        let isGrokConnected = true;
        hoxiAPI.disconnectProvider = async (pid: string) => {
          disconnectedID = pid;
          isGrokConnected = false;
          return { ok: true, providerID: pid };
        };
        hoxiAPI.getLocalCLIStatus = async () => ({
          tools: [
            {
              id: "grok-cli",
              name: "Grok CLI",
              installed: true,
              authenticated: true,
              connected: isGrokConnected,
              sessionCount: 2,
              authMode: "xai-grok-cli",
              providerID: "grok-cli",
              defaultModel: "grok-4",
              models: ["grok-4"],
            },
          ],
        });
        hoxiAPI.listProviders = async () => ({
          all: [
            { id: "grok-cli", name: "Grok CLI", models: { "grok-4": { id: "grok-4" } } },
            { id: "ollama", name: "Ollama", models: { "local-model": { id: "local-model" } } },
          ] as ProviderInfo[],
          connected: isGrokConnected ? ["grok-cli", "ollama"] : ["ollama"],
          default: { ollama: "local-model" },
        });

        setLocalCLIStatusItems([
          {
            id: "grok-cli",
            name: "Grok CLI",
            installed: true,
            authenticated: true,
            connected: true,
            sessionCount: 2,
            authMode: "xai-grok-cli",
            providerID: "grok-cli",
          },
        ]);
        await loadProviders(true);
        setWorkspace("defaultAI", "model", { providerID: "grok-cli", modelID: "grok-4" });
        setWorkspace("activeSessionID", "ses_test_disconnect");
        setWorkspace("aiBySession", "ses_test_disconnect", {
          model: { providerID: "grok-cli", modelID: "grok-4" },
          agent: "build",
          effort: "",
        });

        await disconnectProvider("grok-cli");

        assertDeepEqual(disconnectedID, "grok-cli");
        assertDeepEqual(localCLIStatusItems()[0]?.connected, false);
        assertDeepEqual(workspace.defaultAI.model, { providerID: "ollama", modelID: "local-model" });
        assertDeepEqual(workspace.selectedModel, { providerID: "ollama", modelID: "local-model" });
      });
    } finally {
      setLocalCLIStatusItems(savedCLI);
    }
  });

  it("supports disconnecting a local CLI import and re-importing with overwrite", async () => {
    const savedCLI = localCLIStatusItems();
    try {
      await withProviders(async () => {
        const calls: Array<{ op: string; overwrite?: boolean }> = [];
        let cliConnected = true;
        const tool: LocalCLIStatusItem = {
          id: "opencode-cli",
          name: "OpenCode CLI",
          installed: true,
          authenticated: true,
          connected: true,
          sessionCount: 1,
          authMode: "session-handoff",
          providerID: "mygw",
          defaultModel: "m-fresh",
          models: ["m-fresh"],
        };

        hoxiAPI.disconnectLocalCLI = async (toolID: string) => {
          calls.push({ op: `disconnect:${toolID}` });
          cliConnected = false;
          return { ok: true, toolID, disconnected: ["mygw"] };
        };
        hoxiAPI.importLocalCLI = async (toolID: string, opts?: { overwrite?: boolean }) => {
          calls.push({ op: `import:${toolID}`, overwrite: opts?.overwrite });
          cliConnected = true;
          return { ok: true, toolID, providerID: "mygw", models: ["m-fresh"] };
        };
        hoxiAPI.getLocalCLIStatus = async () => ({
          tools: [{ ...tool, connected: cliConnected }],
        });
        hoxiAPI.listProviders = async () => ({
          all: [
            ...(cliConnected
              ? [{ id: "mygw", name: "MyGW", models: { "m-fresh": { id: "m-fresh" } } }]
              : []),
            { id: "ollama", name: "Ollama", models: { "local-model": { id: "local-model" } } },
          ] as ProviderInfo[],
          connected: cliConnected ? ["mygw", "ollama"] : ["ollama"],
          default: { mygw: "m-fresh", ollama: "local-model" },
        });

        setWorkspace("defaultAI", "model", { providerID: "mygw", modelID: "m-stale" });
        const afterDisconnect = await disconnectLocalCLIModel(tool);
        assertDeepEqual(afterDisconnect[0]?.connected, false);
        assertDeepEqual(workspace.defaultAI.model, { providerID: "ollama", modelID: "local-model" });

        await syncLocalCLIModel({ ...tool, connected: false }, { overwrite: true });
        assertDeepEqual(calls, [
          { op: "disconnect:opencode-cli" },
          { op: "import:opencode-cli", overwrite: true },
        ]);
        assertDeepEqual(localCLIStatusItems()[0]?.connected, true);
        assertDeepEqual(workspace.selectedModel, { providerID: "mygw", modelID: "m-fresh" });
      });
    } finally {
      setLocalCLIStatusItems(savedCLI);
    }
  });
});

describe("one-click free OpenCode model setup", () => {
  it("reuses an existing OpenCode binary without reinstalling and selects its 0x free model", async () => {
    await withProviders(async () => {
      let installCalls = 0;
      hoxiAPI.listProviders = async () => ({ all: [], connected: [], default: {} });
      hoxiAPI.getLocalCLIStatus = async () => ({ tools: [] });
      hoxiAPI.installEngine = async () => {
        installCalls += 1;
        return { ok: true, openCodeBinPath: "/opt/homebrew/bin/opencode", openCodeVersion: "1.19.3", ready: false, cliReady: true };
      };
      runnerAPI.listModels = async (rt) =>
        rt === "opencode"
          ? {
              installed: true,
              available: true,
              default: "opencode/beam-free",
              models: [
                { id: "opencode/beam-free", name: "Beam Free", free: true, multiplier: "0x", reasoning: true },
                { id: "anthropic/claude-sonnet-4-5", name: "Claude Sonnet 4.5", free: false },
              ],
            }
          : { installed: false, available: false, models: [], default: "" };

      const res = await enableFreeOpenCodeModels();
      assertDeepEqual(installCalls, 0);
      assertDeepEqual(res, {
        providerID: "opencode",
        modelID: "opencode/beam-free",
        installedNow: false,
        freeCount: 1,
      });
      assertDeepEqual(workspace.selectedModel, { providerID: "opencode", modelID: "opencode/beam-free" });
      assertDeepEqual(workspace.defaultAI.model, { providerID: "opencode", modelID: "opencode/beam-free" });
      assertDeepEqual(freeOpenCodeSetupState.phase, "idle");
    });
  });

  it("installs OpenCode when absent, re-enables it if disabled, and selects a free model", async () => {
    await withProviders(async () => {
      let installed = false;
      let installCalls = 0;
      let disabledProviders = ["opencode"];

      hoxiAPI.listProviders = async () => ({ all: [], connected: [], default: {} });
      hoxiAPI.getGlobalConfig = async () => ({ disabled_providers: [...disabledProviders] });
      hoxiAPI.updateGlobalConfig = async (patch: Record<string, any>) => {
        if (Array.isArray(patch.disabled_providers)) {
          disabledProviders = [...patch.disabled_providers];
        }
        return { disabled_providers: disabledProviders };
      };
      hoxiAPI.disposeGlobal = async () => {};
      hoxiAPI.getLocalCLIStatus = async () => ({ tools: [] });
      hoxiAPI.installEngine = async () => {
        installCalls += 1;
        installed = true;
        return { ok: true, openCodeBinPath: "~/.hoxi/bin/opencode", openCodeVersion: "1.19.3", ready: false, cliReady: true };
      };
      runnerAPI.listModels = async (rt) =>
        rt === "opencode" && installed
          ? {
              installed: true,
              available: true,
              default: "opencode/big-pickle",
              models: [{ id: "opencode/big-pickle", name: "Big Pickle", free: true, multiplier: "0x" }],
            }
          : { installed: false, available: false, models: [], default: "" };

      setWorkspace("globalConfig", { disabled_providers: ["opencode"] });
      const res = await enableFreeOpenCodeModels();
      assertDeepEqual(installCalls, 1);
      assertDeepEqual(disabledProviders, []);
      assertDeepEqual(res.installedNow, true);
      assertDeepEqual(res.modelID, "opencode/big-pickle");
      assertDeepEqual(workspace.selectedModel, { providerID: "opencode", modelID: "opencode/big-pickle" });
    });
  });

  it("records setup error and preserves empty selection when OpenCode install fails", async () => {
    await withProviders(async () => {
      hoxiAPI.listProviders = async () => ({ all: [], connected: [], default: {} });
      hoxiAPI.installEngine = async () => {
        throw new Error("mirror download failed");
      };
      runnerAPI.listModels = async () => ({ installed: false, available: false, models: [], default: "" });

      let caught = "";
      try {
        await enableFreeOpenCodeModels();
      } catch (err: any) {
        caught = err?.message || "";
      }
      assertTrue(caught.includes("mirror download failed"), "install error must propagate");
      assertDeepEqual(freeOpenCodeSetupState.phase, "error");
      assertTrue((freeOpenCodeSetupState.error || "").includes("mirror download failed"), "error state must be recorded");
      assertDeepEqual(workspace.selectedModel, null);
      clearFreeOpenCodeSetupError();
      assertDeepEqual(freeOpenCodeSetupState.phase, "idle");
      assertDeepEqual(freeOpenCodeSetupState.error, null);
    });
  });

  it("ignores installed-but-unauthenticated runners and does not let an empty hoxi opencode shell shadow CLI free models", async () => {
    await withProviders(async () => {
      hoxiAPI.listProviders = async () => ({ all: [], connected: [], default: {} });
      hoxiAPI.getHoxiModelsConfig = async () => ({
        hoxiProviders: {
          opencode: { name: "OpenCode", options: {}, models: {} },
        },
        openCodeProviders: [],
        configPath: "test",
      });
      hoxiAPI.getLocalCLIStatus = async () => ({ tools: [] });
      runnerAPI.listModels = async (rt) => {
        if (rt === "qoder") {
          return {
            installed: true,
            available: false,
            default: "Auto",
            models: [{ id: "Auto", name: "Auto" }],
          };
        }
        if (rt === "opencode") {
          return {
            installed: true,
            available: true,
            default: "opencode/beam-free",
            models: [{ id: "opencode/beam-free", name: "Beam Free", free: true, multiplier: "0x" }],
          };
        }
        return { installed: false, available: false, models: [], default: "" };
      };

      await loadProviders(true);
      assertTrue(!workspace.connectedProviders.includes("qoder"), "unauthenticated Qoder must not be connected");
      assertTrue(workspace.connectedProviders.includes("opencode"), "OpenCode runner must override empty hoxi shell");
      assertDeepEqual(resolveSessionTargetRuntime(null, "opencode"), "opencode");

      const res = await enableFreeOpenCodeModels();
      assertDeepEqual(res.modelID, "opencode/beam-free");
      assertDeepEqual(res.installedNow, false);
      assertDeepEqual(workspace.selectedModel, { providerID: "opencode", modelID: "opencode/beam-free" });
    });
  });
});
