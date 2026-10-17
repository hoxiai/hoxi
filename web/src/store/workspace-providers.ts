// web/src/store/workspace-providers.ts
import { hoxiAPI, type AgentInfo, type ProviderInfo } from "../services/api";
import {
  EFFORT_KEY,
  FAVORITE_MODELS_KEY,
  RECENT_MODELS_KEY,
  SELECTED_MODEL_KEY,
  setWorkspace,
  workspace,
  type SelectedModel,
} from "./workspace";

export const MASKED_SECRET = "********";

export const POPULAR_PROVIDER_IDS = [
  "opencode",
  "anthropic",
  "openai",
  "google",
  "deepseek",
  "openrouter",
  "xai",
  "zai",
  "moonshotai",
  "alibaba",
  "groq",
  "mistral",
  "ollama",
] as const;

export interface CustomProviderModelDraft {
  id: string;
  name: string;
  reasoning: boolean;
  image: boolean;
}

export interface SaveCustomProviderInput {
  providerID: string;
  name: string;
  npm: string;
  baseURL: string;
  apiKey?: string;
  preserveExistingKey?: boolean;
  headers?: Record<string, string>;
  models: CustomProviderModelDraft[];
}

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

function pushRecentModel(model: SelectedModel) {
  const filtered = workspace.recentModels.filter(
    (m) => !(m.providerID === model.providerID && m.modelID === model.modelID)
  );
  const next = [model, ...filtered].slice(0, 6);
  setWorkspace("recentModels", next);
  try {
    localStorage.setItem(RECENT_MODELS_KEY, JSON.stringify(next));
  } catch {
    /* ignore */
  }
}

export function selectModel(model: SelectedModel, autoRetryOnError = true) {
  const prev = workspace.selectedModel;
  const changed =
    !prev || prev.providerID !== model.providerID || prev.modelID !== model.modelID;

  setWorkspace("selectedModel", model);
  pushRecentModel(model);
  try {
    localStorage.setItem(SELECTED_MODEL_KEY, JSON.stringify(model));
  } catch {
    /* ignore */
  }

  // If the user switches models mid-conversation right after an API error or during retry backoff,
  // immediately re-run the last user prompt with the newly selected model.
  if (changed && autoRetryOnError && workspace.activeSessionID) {
    const sid = workspace.activeSessionID;
    const turns = workspace.messagesBySession[sid] ?? [];
    const lastTurn = turns[turns.length - 1];
    const status = workspace.statusBySession[sid];
    if (
      (lastTurn && lastTurn.info.role === "assistant" && lastTurn.info.error) ||
      status?.type === "retry"
    ) {
      void import("./workspace-sessions").then(async ({ retryLastUserPrompt, sessionDirectory }) => {
        if (status?.type === "retry") {
          await hoxiAPI.abortSession(sid, sessionDirectory(sid)).catch(() => {});
        }
        await retryLastUserPrompt(sid);
      });
    }
  }
}

export function activeModelInfo() {
  const sel = workspace.selectedModel;
  if (!sel) return undefined;
  const prov = workspace.providers.find((p) => p.id === sel.providerID);
  return prov?.models?.[sel.modelID];
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

export function isCustomOrConfiguredProvider(provider: ProviderInfo | string): boolean {
  const pid = typeof provider === "string" ? provider : provider.id;
  const cfgProv = workspace.globalConfig?.provider?.[pid];
  if (!cfgProv || typeof cfgProv !== "object") return false;
  return Boolean(cfgProv.npm || cfgProv.options?.baseURL || cfgProv.models);
}

export function parseModelString(val?: string | null): SelectedModel | null {
  if (!val || typeof val !== "string") return null;
  const slashIdx = val.indexOf("/");
  if (slashIdx <= 0 || slashIdx >= val.length - 1) return null;
  return {
    providerID: val.slice(0, slashIdx),
    modelID: val.slice(slashIdx + 1),
  };
}

export async function loadProviders(): Promise<void> {
  try {
    const dir = workspace.rootDir || undefined;
    const [res, cfg, authMethods] = await Promise.all([
      hoxiAPI.listProviders(dir),
      hoxiAPI.getGlobalConfig().catch(() => ({} as Record<string, any>)),
      hoxiAPI.getProviderAuthMethods(dir).catch(() => ({})),
    ]);

    const all = Array.isArray(res?.all) ? res.all : [];
    const connected = Array.isArray(res?.connected) ? res.connected : [];
    const defaults = res?.default ?? {};

    setWorkspace({
      providers: all,
      connectedProviders: connected,
      defaultModels: defaults,
      globalConfig: cfg || {},
      providerAuthMethods: authMethods || {},
    });

    // Ensure a valid selectedModel is active if none is chosen yet
    const current = workspace.selectedModel;
    const isValid =
      current &&
      all.some((p) => p.id === current.providerID && p.models && p.models[current.modelID]);

    if (!isValid && all.length > 0) {
      // 1. Prefer configured global default model (`cfg.model`) if valid
      const cfgModel = parseModelString(cfg?.model);
      if (
        cfgModel &&
        all.some((p) => p.id === cfgModel.providerID && p.models?.[cfgModel.modelID])
      ) {
        selectModel(cfgModel, false);
        return;
      }

      // 2. Prefer connected providers' default models
      for (const pid of connected) {
        const defModel = defaults[pid];
        const prov = all.find((p) => p.id === pid);
        if (prov && defModel && prov.models?.[defModel]) {
          selectModel({ providerID: pid, modelID: defModel }, false);
          return;
        }
        if (prov && prov.models) {
          const firstKey = Object.keys(prov.models)[0];
          if (firstKey) {
            selectModel({ providerID: pid, modelID: firstKey }, false);
            return;
          }
        }
      }

      // 3. Fallback to OpenCode free model if present
      const openCodeProv = all.find((p) => p.id === "opencode");
      if (openCodeProv?.models?.["deepseek-v4.1-flash-free"]) {
        selectModel({ providerID: "opencode", modelID: "deepseek-v4.1-flash-free" }, false);
        return;
      }

      // 4. Fallback to first provider with models
      for (const prov of all) {
        const firstKey = Object.keys(prov.models || {})[0];
        if (firstKey) {
          selectModel({ providerID: prov.id, modelID: firstKey }, false);
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
    selectModel({ providerID: pid, modelID: opts.customModelID.trim() }, false);
  } else if (!workspace.selectedModel) {
    const prov = workspace.providers.find((p) => p.id === pid);
    const defModel = workspace.defaultModels[pid] || Object.keys(prov?.models || {})[0];
    if (defModel) {
      selectModel({ providerID: pid, modelID: defModel }, false);
    }
  }
}

export async function saveCustomProvider(input: SaveCustomProviderInput): Promise<void> {
  const pid = input.providerID.trim();
  const currentCfg: Record<string, any> = await hoxiAPI.getGlobalConfig().catch(() => ({}));
  const previous = currentCfg?.provider?.[pid] ?? {};

  // Build next models map + null deletion sentinels for removed models (Kilo's withCustomProviderDeletions)
  const nextModels: Record<string, any> = {};
  for (const m of input.models) {
    const mid = m.id.trim();
    if (!mid) continue;
    nextModels[mid] = {
      name: (m.name || mid).trim(),
      reasoning: Boolean(m.reasoning),
      modalities: {
        input: m.image ? ["text", "image"] : ["text"],
        output: ["text"],
      },
    };
  }
  const prevModels = previous?.models ?? {};
  for (const oldModelID of Object.keys(prevModels)) {
    if (!(oldModelID in nextModels)) {
      nextModels[oldModelID] = null;
    }
  }

  // Build next headers map + null deletion sentinels for removed headers
  const nextHeaders: Record<string, string | null> = {};
  if (input.headers) {
    for (const [k, v] of Object.entries(input.headers)) {
      const hk = k.trim();
      const hv = v.trim();
      if (hk && hv) nextHeaders[hk] = hv;
    }
  }
  const prevHeaders = previous?.options?.headers ?? {};
  for (const oldHeaderKey of Object.keys(prevHeaders)) {
    if (!(oldHeaderKey in nextHeaders)) {
      nextHeaders[oldHeaderKey] = null;
    }
  }

  const nextOptions: Record<string, any> = {
    ...(previous?.options || {}),
    baseURL: input.baseURL.trim(),
  };
  if (Object.keys(nextHeaders).length > 0) {
    nextOptions.headers = nextHeaders;
  } else if (previous?.options?.headers) {
    nextOptions.headers = null;
  }

  // Handle API key updates
  const rawKey = (input.apiKey ?? "").trim();
  if (rawKey && rawKey !== MASKED_SECRET) {
    if (/^\{env:[^}]+\}$/.test(rawKey)) {
      nextOptions.apiKey = rawKey;
      await hoxiAPI.removeProviderAuth(pid).catch(() => {});
    } else {
      if (previous?.options?.apiKey) {
        nextOptions.apiKey = null;
      }
      await hoxiAPI.setProviderAuth(pid, rawKey);
    }
  } else if (!rawKey && !input.preserveExistingKey) {
    if (previous?.options?.apiKey) {
      nextOptions.apiKey = null;
    }
    await hoxiAPI.removeProviderAuth(pid).catch(() => {});
  }

  const nextProviderConfig: Record<string, any> = {
    ...previous,
    name: input.name.trim() || pid,
    npm: input.npm || "@ai-sdk/openai-compatible",
    options: nextOptions,
    models: nextModels,
  };

  await hoxiAPI.updateGlobalConfig({
    provider: {
      [pid]: nextProviderConfig,
    },
  });

  await hoxiAPI.disposeGlobal().catch(() => {});
  await loadProviders();

  const firstModelID = input.models[0]?.id?.trim();
  const cur = workspace.selectedModel;
  const curStillExists =
    cur &&
    (cur.providerID !== pid || Boolean(nextModels[cur.modelID]));
  if (firstModelID && !curStillExists) {
    selectModel({ providerID: pid, modelID: firstModelID }, false);
  }
}

export async function disconnectProvider(providerID: string): Promise<void> {
  await hoxiAPI.removeProviderAuth(providerID).catch(() => {});
  const currentCfg: Record<string, any> = await hoxiAPI.getGlobalConfig().catch(() => ({}));
  if (currentCfg?.provider && providerID in currentCfg.provider) {
    await hoxiAPI
      .updateGlobalConfig({
        provider: {
          [providerID]: null,
        },
      })
      .catch(() => {});
  }
  await hoxiAPI.disposeGlobal().catch(() => {});
  await loadProviders();
}

export async function toggleDisabledProvider(
  providerID: string,
  disabled: boolean
): Promise<void> {
  const currentCfg: Record<string, any> = await hoxiAPI.getGlobalConfig().catch(() => ({}));
  const currentList: string[] = Array.isArray(currentCfg?.disabled_providers)
    ? currentCfg.disabled_providers
    : [];
  const nextList = disabled
    ? Array.from(new Set([...currentList, providerID]))
    : currentList.filter((id) => id !== providerID);

  await hoxiAPI.updateGlobalConfig({
    disabled_providers: nextList,
  });
  await hoxiAPI.disposeGlobal().catch(() => {});
  await loadProviders();
}

export async function updateGlobalModelConfig(patch: Record<string, any>): Promise<void> {
  const updated = await hoxiAPI.updateGlobalConfig(patch);
  setWorkspace("globalConfig", updated || {});
  if (typeof patch.model === "string") {
    const parsed = parseModelString(patch.model);
    if (parsed) {
      selectModel(parsed, false);
    }
  }
}
