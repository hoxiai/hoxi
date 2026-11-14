import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI, authAPI, runnerAPI, type LocalCLIStatusItem, type ProviderInfo } from "../services/api";
import { loadProviders } from "../store/workspace-providers";
import {
  connectProviderWithKey,
  saveCustomProvider,
  syncLocalCLIModel,
} from "../store/provider-connections";
import { setWorkspace, workspace } from "../store/workspace";
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
