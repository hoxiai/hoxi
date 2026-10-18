// web/src/store/workspace-providers.ts
import { t } from "../i18n";
import {
  hoxiAPI,
  resolveRunnerRuntimeForProvider,
  runnerAPI,
  type AgentInfo,
  type ProviderInfo,
} from "../services/api";
import type { SessionRuntime } from "../services/sse-types";
import { updateSessionAI } from "./session-ai";
import {
  EFFORT_KEY,
  FAVORITE_MODELS_KEY,
  RECENT_MODELS_KEY,
  SELECTED_MODEL_KEY,
  setWorkspace,
  workspace,
  type SelectedModel,
} from "./workspace";

export * from "./provider-catalog";

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
  free?: boolean;
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

export function sessionRuntimeForID(sessionID?: string | null): SessionRuntime {
  if (!sessionID) {
    const draftProv = workspace.defaultAI.model?.providerID;
    return resolveRunnerRuntimeForProvider(draftProv, Boolean(workspace.hoxiProviders?.[draftProv || ""]));
  }
  if (sessionID.startsWith("ses_agy_")) return "antigravity";
  if (sessionID.startsWith("ses_cld_")) return "claude-code";
  const found = workspace.sessions.find((s) => s.id === sessionID);
  if (found?.runtime) return found.runtime;
  return "opencode";
}

export function selectModel(model: SelectedModel, _autoRetryOnError = false) {
  const activeID = workspace.activeSessionID;
  const targetRuntime = resolveRunnerRuntimeForProvider(
    model.providerID,
    Boolean(workspace.hoxiProviders?.[model.providerID])
  );
  if (activeID && sessionRuntimeForID(activeID) !== targetRuntime) {
    updateSessionAI({ model }, null);
    pushRecentModel(model);
    try {
      localStorage.setItem(SELECTED_MODEL_KEY, JSON.stringify(model));
    } catch {
      /* ignore */
    }
    setWorkspace({
      activeSessionID: null,
      sessionSetupError: null,
      newThreadRequest: workspace.newThreadRequest + 1,
    });
    return;
  }
  updateSessionAI({ model });
  pushRecentModel(model);
  try {
    if (!workspace.activeSessionID) localStorage.setItem(SELECTED_MODEL_KEY, JSON.stringify(model));
  } catch {
    /* ignore */
  }
}

export function activeModelInfo() {
  const sel = workspace.selectedModel;
  if (!sel) return undefined;
  const prov = workspace.providers.find((p) => p.id === sel.providerID);
  return prov?.models?.[sel.modelID];
}

function selectDefaultModel(model: SelectedModel) {
  updateSessionAI({ model }, null);
  try { localStorage.setItem(SELECTED_MODEL_KEY, JSON.stringify(model)); } catch { /* storage unavailable */ }
}

export function setReasoningEffort(effort: "" | "low" | "medium" | "high") {
  updateSessionAI({ effort });
  try {
    if (!workspace.activeSessionID) localStorage.setItem(EFFORT_KEY, effort);
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

export function isHoxiCustomProvider(provider: ProviderInfo | string): boolean {
  const pid = typeof provider === "string" ? provider : provider.id;
  return Boolean(workspace.hoxiProviders?.[pid]);
}

export function isImportedOpenCodeProvider(provider: ProviderInfo | string): boolean {
  const pid = typeof provider === "string" ? provider : provider.id;
  if (workspace.hoxiProviders?.[pid]) return false;
  if (workspace.openCodeImportedProviders.includes(pid)) return true;
  const cfgProv = workspace.globalConfig?.provider?.[pid];
  if (!cfgProv || typeof cfgProv !== "object") return false;
  return Boolean(cfgProv.npm || cfgProv.options?.baseURL || cfgProv.models);
}

export function isCustomOrConfiguredProvider(provider: ProviderInfo | string): boolean {
  return isHoxiCustomProvider(provider) || isImportedOpenCodeProvider(provider);
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
    const [res, cfg, authMethods, hoxiCfg, agyRunner, claudeRunner] = await Promise.all([
      hoxiAPI.listProviders(dir),
      hoxiAPI.getGlobalConfig().catch(() => ({} as Record<string, any>)),
      hoxiAPI.getProviderAuthMethods(dir).catch(() => ({})),
      hoxiAPI.getHoxiModelsConfig().catch(() => ({
        hoxiProviders: {} as Record<string, any>,
        openCodeProviders: [] as string[],
        configPath: ".hoxi/models.json",
      })),
      runnerAPI.listModels("antigravity").catch(() => null),
      runnerAPI.listModels("claude-code").catch(() => null),
    ]);

    const hoxiMap = hoxiCfg?.hoxiProviders ?? {};
    const openCodeList = Array.isArray(hoxiCfg?.openCodeProviders)
      ? hoxiCfg.openCodeProviders
      : [];

    const mergedCfg: Record<string, any> = {
      ...(cfg || {}),
      provider: {
        ...(cfg?.provider || {}),
        ...hoxiMap,
      },
    };

    const rawAll = Array.isArray(res?.all) ? res.all : [];
    const connected = Array.isArray(res?.connected) ? [...res.connected] : [];
    const defaults = res?.default ?? {};

    const byId = new Map<string, ProviderInfo>();
    for (const p of rawAll) {
      byId.set(p.id, { ...p, models: { ...(p.models || {}) } });
    }
    for (const [pid, hProv] of Object.entries<any>(hoxiMap)) {
      if (!hProv || typeof hProv !== "object") continue;
      const rawModels = hProv.models || {};
      const existingProv = byId.get(pid);
      const exactModels: Record<string, any> = {};
      for (const [mid, mCfg] of Object.entries<any>(rawModels)) {
        if (!mCfg || typeof mCfg !== "object") continue;
        const rtModel = existingProv?.models?.[mid];
        exactModels[mid] = {
          ...(rtModel || {}),
          id: mid,
          name: mCfg.name || rtModel?.name || mid,
          reasoning: Boolean(mCfg.reasoning ?? rtModel?.reasoning),
          capabilities: {
            ...(rtModel?.capabilities || {}),
            reasoning: Boolean(mCfg.reasoning ?? rtModel?.capabilities?.reasoning),
            input: {
              ...(rtModel?.capabilities?.input || {}),
              image: Boolean(
                Array.isArray(mCfg.modalities?.input) &&
                  mCfg.modalities.input.includes("image")
              ),
            },
          },
        };
      }
      byId.set(pid, {
        ...(existingProv || { id: pid, name: hProv.name || pid }),
        id: pid,
        name: hProv.name || existingProv?.name || pid,
        npm: hProv.npm || existingProv?.npm || "@ai-sdk/openai-compatible",
        options: {
          ...(existingProv?.options || {}),
          ...(hProv.options || {}),
        },
        models: exactModels,
      });
      if (!connected.includes(pid)) {
        connected.push(pid);
      }
    }

    const registerRunnerProvider = (pid: string, name: string, r: any) => {
      if (!r?.installed || !Array.isArray(r.models) || r.models.length === 0) return;
      const models: Record<string, any> = {};
      for (const m of r.models) {
        if (!m?.id) continue;
        models[m.id] = {
          id: m.id,
          name: m.name || m.id,
          reasoning: Boolean(m.reasoning),
          capabilities: { reasoning: Boolean(m.reasoning), toolcall: true },
        };
      }
      byId.set(pid, { id: pid, name, source: "runner", models });
      if (!connected.includes(pid)) connected.unshift(pid);
      if (r.default) defaults[pid] = r.default;
    };

    registerRunnerProvider("antigravity", t("timeline.runtimeAntigravityLocal"), agyRunner);
    if (!hoxiMap["claude-code"]) {
      registerRunnerProvider("claude-code", t("timeline.runtimeClaudeCodeLocal"), claudeRunner);
    }

    const all = Array.from(byId.values());

    setWorkspace({
      providers: all,
      connectedProviders: connected,
      defaultModels: defaults,
      globalConfig: mergedCfg,
      hoxiProviders: hoxiMap,
      openCodeImportedProviders: openCodeList,
      providerAuthMethods: authMethods || {},
    });

    const current = workspace.defaultAI.model;
    const isValid =
      current &&
      all.some((p) => p.id === current.providerID && p.models && p.models[current.modelID]);

    if (!isValid && all.length > 0) {
      const cfgModel = parseModelString(mergedCfg?.model);
      if (cfgModel && all.some((p) => p.id === cfgModel.providerID && p.models?.[cfgModel.modelID])) {
        selectDefaultModel(cfgModel);
        return;
      }
      for (const pid of connected) {
        const defModel = defaults[pid];
        const prov = all.find((p) => p.id === pid);
        if (prov && defModel && prov.models?.[defModel]) {
          selectDefaultModel({ providerID: pid, modelID: defModel });
          return;
        }
        const firstKey = prov?.models ? Object.keys(prov.models)[0] : undefined;
        if (prov && firstKey) {
          selectDefaultModel({ providerID: pid, modelID: firstKey });
          return;
        }
      }
      const openCodeProv = all.find((p) => p.id === "opencode");
      if (openCodeProv?.models?.["deepseek-v4.1-flash-free"]) {
        selectDefaultModel({ providerID: "opencode", modelID: "deepseek-v4.1-flash-free" });
        return;
      }
      for (const prov of all) {
        const firstKey = Object.keys(prov.models || {})[0];
        if (firstKey) {
          selectDefaultModel({ providerID: prov.id, modelID: firstKey });
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
  if (key) await hoxiAPI.setProviderAuth(pid, key);

  if (opts.baseURL?.trim() || opts.customModelID?.trim()) {
    const existingProvider = workspace.hoxiProviders?.[pid] ?? {};
    const updatedProvider: Record<string, any> = {
      ...existingProvider,
      ...(opts.customName ? { name: opts.customName.trim() } : {}),
      ...(opts.customModelID
        ? {
            npm: existingProvider.npm || "@ai-sdk/openai-compatible",
            models: { ...(existingProvider.models || {}), [opts.customModelID.trim()]: { name: opts.customModelID.trim() } },
          }
        : {}),
      ...(opts.baseURL?.trim()
        ? { options: { ...(existingProvider.options || {}), baseURL: opts.baseURL.trim() } }
        : {}),
    };
    await hoxiAPI.saveHoxiProviderConfig(pid, updatedProvider);
  } else {
    await hoxiAPI.disposeGlobal().catch(() => {});
  }

  await loadProviders();

  if (opts.customModelID?.trim()) {
    selectModel({ providerID: pid, modelID: opts.customModelID.trim() }, false);
  } else if (!workspace.selectedModel) {
    const prov = workspace.providers.find((p) => p.id === pid);
    const defModel = workspace.defaultModels[pid] || Object.keys(prov?.models || {})[0];
    if (defModel) selectModel({ providerID: pid, modelID: defModel }, false);
  }
}

export async function saveCustomProvider(input: SaveCustomProviderInput): Promise<void> {
  const pid = input.providerID.trim();
  const previous = workspace.hoxiProviders?.[pid] ?? workspace.globalConfig?.provider?.[pid] ?? {};

  const nextModels: Record<string, any> = {};
  for (const m of input.models) {
    const mid = m.id.trim();
    if (!mid) continue;
    nextModels[mid] = {
      name: (m.name || mid).trim(),
      reasoning: Boolean(m.reasoning),
      modalities: { input: m.image ? ["text", "image"] : ["text"], output: ["text"] },
    };
  }

  const nextHeaders: Record<string, string> = {};
  if (input.headers) {
    for (const [k, v] of Object.entries(input.headers)) {
      if (k.trim() && v.trim()) nextHeaders[k.trim()] = v.trim();
    }
  }

  const nextOptions: Record<string, any> = { baseURL: input.baseURL.trim() };
  if (Object.keys(nextHeaders).length > 0) nextOptions.headers = nextHeaders;

  const rawKey = (input.apiKey ?? "").trim();
  if (rawKey && rawKey !== MASKED_SECRET) {
    if (/^\{env:[^}]+\}$/.test(rawKey)) {
      nextOptions.apiKey = rawKey;
      await hoxiAPI.removeProviderAuth(pid).catch(() => {});
    } else {
      await hoxiAPI.setProviderAuth(pid, rawKey);
    }
  } else if (input.preserveExistingKey && typeof previous?.options?.apiKey === "string") {
    nextOptions.apiKey = previous.options.apiKey;
  } else if (!rawKey && !input.preserveExistingKey) {
    await hoxiAPI.removeProviderAuth(pid).catch(() => {});
  }

  await hoxiAPI.saveHoxiProviderConfig(pid, {
    name: input.name.trim() || pid,
    npm: input.npm || "@ai-sdk/openai-compatible",
    options: nextOptions,
    models: nextModels,
  });
  await loadProviders();

  const firstModelID = input.models[0]?.id?.trim();
  const cur = workspace.selectedModel;
  if (firstModelID && (!cur || cur.providerID === pid || !nextModels[cur.modelID])) {
    const targetModelID = cur?.providerID === pid && nextModels[cur.modelID] ? cur.modelID : firstModelID;
    selectModel({ providerID: pid, modelID: targetModelID }, false);
  }
}

export async function disconnectProvider(providerID: string): Promise<void> {
  if (isImportedOpenCodeProvider(providerID)) throw new Error("OpenCode imported provider is read-only");
  await hoxiAPI.removeProviderAuth(providerID).catch(() => {});
  await hoxiAPI.deleteHoxiProviderConfig(providerID).catch(() => {});
  await loadProviders();
}

export async function toggleDisabledProvider(providerID: string, disabled: boolean): Promise<void> {
  const currentCfg: Record<string, any> = await hoxiAPI.getGlobalConfig().catch(() => ({}));
  const currentList: string[] = Array.isArray(currentCfg?.disabled_providers) ? currentCfg.disabled_providers : [];
  const nextList = disabled
    ? Array.from(new Set([...currentList, providerID]))
    : currentList.filter((id) => id !== providerID);

  await hoxiAPI.updateGlobalConfig({ disabled_providers: nextList });
  await hoxiAPI.disposeGlobal().catch(() => {});
  await loadProviders();
}

export async function updateGlobalModelConfig(patch: Record<string, any>): Promise<void> {
  const updated = await hoxiAPI.updateGlobalConfig(patch);
  setWorkspace("globalConfig", updated || {});
  if (typeof patch.model === "string") {
    const parsed = parseModelString(patch.model);
    if (parsed) selectDefaultModel(parsed);
  }
}

export const refreshProviders = loadProviders;
