// web/src/store/workspace-providers.ts
import { hoxiAPI, type AgentInfo } from "../services/api";
import {
  EFFORT_KEY,
  FAVORITE_MODELS_KEY,
  SELECTED_MODEL_KEY,
  setWorkspace,
  workspace,
  type SelectedModel,
} from "./workspace";

export const DEFAULT_AGENTS: AgentInfo[] = [
  {
    name: "build",
    mode: "primary",
    description: "The default agent. Executes tools based on configured permissions.",
  },
  {
    name: "plan",
    mode: "primary",
    description: "Plan mode. Disallows all edit tools.",
  },
];

export async function loadAgents(directory?: string): Promise<void> {
  try {
    const list = await hoxiAPI.listAgents(directory || workspace.rootDir || undefined);
    if (Array.isArray(list) && list.length > 0) {
      const primaries = list.filter((a) => (a.mode === "primary" || !a.mode) && !a.hidden);
      if (primaries.length > 0) {
        setWorkspace("agents", primaries);
        return;
      }
    }
  } catch (err) {
    console.error("load agents error:", err);
  }
  setWorkspace("agents", DEFAULT_AGENTS);
}

export function selectModel(model: SelectedModel) {
  setWorkspace("selectedModel", model);
  try {
    localStorage.setItem(SELECTED_MODEL_KEY, JSON.stringify(model));
  } catch {
    /* ignore */
  }
}

export function setReasoningEffort(effort: "" | "low" | "medium" | "high") {
  setWorkspace("reasoningEffort", effort);
  try {
    localStorage.setItem(EFFORT_KEY, effort);
  } catch {
    /* ignore */
  }
}

export function isFavoriteModel(providerID: string, modelID: string): boolean {
  return workspace.favoriteModels.some(
    (m) => m.providerID === providerID && m.modelID === modelID
  );
}

export function toggleFavoriteModel(providerID: string, modelID: string) {
  const exists = isFavoriteModel(providerID, modelID);
  const next = exists
    ? workspace.favoriteModels.filter(
        (m) => !(m.providerID === providerID && m.modelID === modelID)
      )
    : [...workspace.favoriteModels, { providerID, modelID }];
  setWorkspace("favoriteModels", next);
  try {
    localStorage.setItem(FAVORITE_MODELS_KEY, JSON.stringify(next));
  } catch {
    /* ignore */
  }
}

export async function loadProviders(): Promise<void> {
  try {
    const res = await hoxiAPI.listProviders(workspace.rootDir || undefined);
    const all = Array.isArray(res?.all) ? res.all : [];
    const connected = Array.isArray(res?.connected) ? res.connected : [];
    const defaults = res?.default ?? {};

    setWorkspace({
      providers: all,
      connectedProviders: connected,
      defaultModels: defaults,
    });

    // Ensure a valid selectedModel is active if none is chosen yet
    const current = workspace.selectedModel;
    const isValid =
      current &&
      all.some((p) => p.id === current.providerID && p.models && p.models[current.modelID]);

    if (!isValid && all.length > 0) {
      // Prefer a connected provider's default model
      for (const pid of connected) {
        const defModel = defaults[pid];
        const prov = all.find((p) => p.id === pid);
        if (prov && defModel && prov.models?.[defModel]) {
          selectModel({ providerID: pid, modelID: defModel });
          return;
        }
        if (prov && prov.models) {
          const firstKey = Object.keys(prov.models)[0];
          if (firstKey) {
            selectModel({ providerID: pid, modelID: firstKey });
            return;
          }
        }
      }
      // Fallback to first provider with models
      for (const prov of all) {
        const firstKey = Object.keys(prov.models || {})[0];
        if (firstKey) {
          selectModel({ providerID: prov.id, modelID: firstKey });
          return;
        }
      }
    }
  } catch (err) {
    console.error("Failed to load providers:", err);
  }
}

export async function connectProviderWithKey(opts: {
  providerID: string;
  apiKey: string;
  baseURL?: string;
  customName?: string;
  customModelID?: string;
}): Promise<void> {
  const pid = opts.providerID.trim();
  const key = opts.apiKey.trim();

  if (key) {
    await hoxiAPI.setProviderAuth(pid, key);
  }

  if (opts.baseURL?.trim() || opts.customModelID?.trim()) {
    const currentCfg: Record<string, any> = await hoxiAPI.getGlobalConfig().catch(() => ({}));
    const existingProvider = currentCfg?.provider?.[pid] ?? {};
    const updatedProvider: Record<string, any> = {
      ...existingProvider,
      ...(opts.customName ? { name: opts.customName.trim() } : {}),
      ...(opts.customModelID
        ? {
            npm: existingProvider.npm || "@ai-sdk/openai-compatible",
            models: {
              ...(existingProvider.models || {}),
              [opts.customModelID.trim()]: {
                name: opts.customModelID.trim(),
              },
            },
          }
        : {}),
      ...(opts.baseURL?.trim()
        ? {
            options: {
              ...(existingProvider.options || {}),
              baseURL: opts.baseURL.trim(),
            },
          }
        : {}),
    };

    await hoxiAPI.updateGlobalConfig({
      provider: {
        [pid]: updatedProvider,
      },
    });
  }

  await hoxiAPI.disposeGlobal().catch(() => {});
  await loadProviders();

  if (opts.customModelID?.trim()) {
    selectModel({ providerID: pid, modelID: opts.customModelID.trim() });
  } else {
    const prov = workspace.providers.find((p) => p.id === pid);
    const defModel = workspace.defaultModels[pid] || Object.keys(prov?.models || {})[0];
    if (defModel) {
      selectModel({ providerID: pid, modelID: defModel });
    }
  }
}

export async function disconnectProvider(providerID: string): Promise<void> {
  await hoxiAPI.removeProviderAuth(providerID);
  await hoxiAPI.disposeGlobal().catch(() => {});
  await loadProviders();
}
