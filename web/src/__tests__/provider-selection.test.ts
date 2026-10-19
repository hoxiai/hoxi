import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI, runnerAPI, type LocalCLIStatusItem, type ProviderInfo } from "../services/api";
import { loadProviders, syncLocalCLIModel } from "../store/workspace-providers";
import { setWorkspace, workspace } from "../store/workspace";
import { assertDeepEqual, describe, it } from "./test-harness";

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
});
