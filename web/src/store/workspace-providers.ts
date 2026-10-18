// web/src/store/workspace-providers.ts
import { t } from "../i18n";
import { hoxiAPI, type AgentInfo, type ProviderInfo } from "../services/api";
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

export function selectModel(model: SelectedModel, _autoRetryOnError = false) {
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
    const [res, cfg, authMethods, hoxiCfg] = await Promise.all([
      hoxiAPI.listProviders(dir),
      hoxiAPI.getGlobalConfig().catch(() => ({} as Record<string, any>)),
      hoxiAPI.getProviderAuthMethods(dir).catch(() => ({})),
      hoxiAPI.getHoxiModelsConfig().catch(() => ({
        hoxiProviders: {} as Record<string, any>,
        openCodeProviders: [] as string[],
        configPath: ".hoxi/models.json",
      })),
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

    // Ensure Hoxi-managed providers in .hoxi/models.json reflect their exact configured models
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

    // Ensure a valid selectedModel is active
    const current = workspace.defaultAI.model;
    const isValid =
      current &&
      all.some((p) => p.id === current.providerID && p.models && p.models[current.modelID]);

    if (!isValid && all.length > 0) {
      const cfgModel = parseModelString(mergedCfg?.model);
      if (
        cfgModel &&
        all.some((p) => p.id === cfgModel.providerID && p.models?.[cfgModel.modelID])
      ) {
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
        if (prov && prov.models) {
          const firstKey = Object.keys(prov.models)[0];
          if (firstKey) {
            selectDefaultModel({ providerID: pid, modelID: firstKey });
            return;
          }
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

  if (key) {
    await hoxiAPI.setProviderAuth(pid, key);
  }

  if (opts.baseURL?.trim() || opts.customModelID?.trim()) {
    const existingProvider = workspace.hoxiProviders?.[pid] ?? {};
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
    if (defModel) {
      selectModel({ providerID: pid, modelID: defModel }, false);
    }
  }
}

export async function saveCustomProvider(input: SaveCustomProviderInput): Promise<void> {
  const pid = input.providerID.trim();
  const previous = workspace.hoxiProviders?.[pid] ?? workspace.globalConfig?.provider?.[pid] ?? {};

  // Build exact models map for .hoxi/models.json (no null sentinels)
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

  // Build exact headers map
  const nextHeaders: Record<string, string> = {};
  if (input.headers) {
    for (const [k, v] of Object.entries(input.headers)) {
      const hk = k.trim();
      const hv = v.trim();
      if (hk && hv) nextHeaders[hk] = hv;
    }
  }

  const nextOptions: Record<string, any> = {
    baseURL: input.baseURL.trim(),
  };
  if (Object.keys(nextHeaders).length > 0) {
    nextOptions.headers = nextHeaders;
  }

  // Handle API key updates
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

  const nextProviderConfig: Record<string, any> = {
    name: input.name.trim() || pid,
    npm: input.npm || "@ai-sdk/openai-compatible",
    options: nextOptions,
    models: nextModels,
  };

  await hoxiAPI.saveHoxiProviderConfig(pid, nextProviderConfig);
  await loadProviders();

  const firstModelID = input.models[0]?.id?.trim();
  const cur = workspace.selectedModel;
  if (firstModelID && (!cur || cur.providerID === pid || !nextModels[cur.modelID])) {
    const targetModelID =
      cur?.providerID === pid && nextModels[cur.modelID] ? cur.modelID : firstModelID;
    selectModel({ providerID: pid, modelID: targetModelID }, false);
  }
}

export async function disconnectProvider(providerID: string): Promise<void> {
  if (isImportedOpenCodeProvider(providerID)) {
    throw new Error("OpenCode imported provider is read-only");
  }
  await hoxiAPI.removeProviderAuth(providerID).catch(() => {});
  await hoxiAPI.deleteHoxiProviderConfig(providerID).catch(() => {});
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
      selectDefaultModel(parsed);
    }
  }
}

export const refreshProviders = loadProviders;

export interface FlattenedModel {
  providerID: string;
  providerName: string;
  modelID: string;
  modelName: string;
  connected: boolean;
  isCustom: boolean;
  source?: string;
  baseURL?: string;
  reasoning: boolean;
  image: boolean;
  toolcall: boolean;
  contextLimit?: number;
  outputLimit?: number;
  inputCost?: number;
  outputCost?: number;
  free: boolean;
}

export interface ModelSourceInfo {
  label: string;
  configHint?: string;
  isImported: boolean;
}

export interface ConnectedProviderGroup {
  providerID: string;
  providerName: string;
  models: Array<{ id: string; name: string; fullKey: string; free: boolean }>;
}

export function getModelSourceInfo(model: FlattenedModel): ModelSourceInfo {
  const pid = model.providerID;
  if (isHoxiCustomProvider(pid)) {
    return {
      label: t("models.sourceHoxi"),
      configHint: ".hoxi/models.json",
      isImported: false,
    };
  }
  if (isImportedOpenCodeProvider(pid)) {
    return {
      label: t("models.sourceOpenCode"),
      configHint: "opencode",
      isImported: true,
    };
  }
  if (model.isCustom) {
    return {
      label: t("models.sourceCustom"),
      isImported: false,
    };
  }
  if (pid === "opencode" && model.free && !workspace.connectedProviders.includes("opencode")) {
    return {
      label: t("models.sourceBuiltInFree"),
      isImported: false,
    };
  }
  if (model.source === "env") {
    return { label: t("models.sourceEnv"), isImported: false };
  }
  if (model.source === "config") {
    return { label: t("models.sourceConfig"), isImported: false };
  }
  return { label: t("models.sourceApi"), isImported: false };
}

export function formatTokenLimit(n?: number): string | null {
  if (!n || n <= 0) return null;
  if (n >= 1_000_000) {
    const m = n / 1_000_000;
    return `${Number.isInteger(m) ? m : m.toFixed(1)}M`;
  }
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(n);
}

export function formatPricePerMillion(cost?: number): string | null {
  if (cost === undefined || cost === null) return null;
  if (cost === 0) return "$0";
  const val = cost < 0.001 ? cost * 1_000_000 : cost;
  return `$${val < 1 ? val.toFixed(2) : val.toFixed(2).replace(/\.00$/, "")}/M`;
}

export function isModelFree(
  providerID: string,
  modelID: string,
  mInfo?: Record<string, any> | null
): boolean {
  if (providerID !== "opencode") return false;
  const inCost = mInfo?.cost?.input;
  return !mInfo?.cost || inCost === 0 || modelID.endsWith("-free");
}

export function extractFlattenedModels(
  providers: ProviderInfo[],
  connectedIds: Set<string>,
  globalConfig: Record<string, any>
): FlattenedModel[] {
  const disabledSet = new Set<string>(
    Array.isArray(globalConfig?.disabled_providers) ? globalConfig.disabled_providers : []
  );
  const cfgProviders = globalConfig?.provider ?? {};
  const out: FlattenedModel[] = [];

  for (const p of providers) {
    if (disabledSet.has(p.id)) continue;
    const isCustom = isCustomOrConfiguredProvider(p);
    const baseConn = connectedIds.has(p.id) || Boolean(cfgProviders[p.id]);
    const baseURL =
      workspace.hoxiProviders?.[p.id]?.options?.baseURL ||
      cfgProviders?.[p.id]?.options?.baseURL ||
      p.options?.baseURL;

    for (const [mID, mInfo] of Object.entries(p.models || {})) {
      if (!mInfo || typeof mInfo !== "object") continue;
      const cfgModel = cfgProviders?.[p.id]?.models?.[mID];
      const reasoning = Boolean(
        mInfo.capabilities?.reasoning ?? mInfo.reasoning ?? cfgModel?.reasoning
      );
      const image = Boolean(
        mInfo.capabilities?.input?.image ||
          (Array.isArray(mInfo.modalities?.input) && mInfo.modalities.input.includes("image")) ||
          (Array.isArray(cfgModel?.modalities?.input) &&
            cfgModel.modalities.input.includes("image"))
      );
      const toolcall = Boolean(mInfo.capabilities?.toolcall ?? mInfo.tool_call ?? true);
      const inCost = mInfo.cost?.input ?? cfgModel?.cost?.input;
      const outCost = mInfo.cost?.output ?? cfgModel?.cost?.output;
      const free = isModelFree(p.id, mID, mInfo);
      const isConn = baseConn || (p.id === "opencode" && free);

      out.push({
        providerID: p.id,
        providerName: p.name || p.id,
        modelID: mID,
        modelName: mInfo.name || cfgModel?.name || mID,
        connected: isConn,
        isCustom,
        source: p.source,
        baseURL,
        reasoning,
        image,
        toolcall,
        contextLimit: mInfo.limit?.context,
        outputLimit: mInfo.limit?.output,
        inputCost: inCost,
        outputCost: outCost,
        free,
      });
    }
  }
  return out;
}

export function rankModelSearch(models: FlattenedModel[], rawQuery: string): FlattenedModel[] {
  const q = rawQuery.trim().toLowerCase();
  if (!q) return models;
  const tokens = q.split(/\s+/).filter(Boolean);

  const scored: Array<{ item: FlattenedModel; score: number }> = [];
  for (const m of models) {
    const idLower = m.modelID.toLowerCase();
    const nameLower = m.modelName.toLowerCase();
    const provLower = `${m.providerName} ${m.providerID}`.toLowerCase();
    const freeTag = m.free ? `free ${t("models.freeModel").toLowerCase()}` : "";
    const haystack = `${idLower} ${nameLower} ${provLower} ${freeTag}`;

    if (!tokens.every((tok) => haystack.includes(tok))) continue;

    let score = 0;
    if (idLower === q || nameLower === q) score += 200;
    else if (idLower.startsWith(q) || nameLower.startsWith(q)) score += 120;
    else if (idLower.includes(q) || nameLower.includes(q)) score += 70;
    if (m.connected) score += 40;
    if (m.free) score += 25;
    if (m.isCustom) score += 20;

    scored.push({ item: m, score });
  }

  return scored
    .sort((a, b) => b.score - a.score || a.item.modelName.localeCompare(b.item.modelName))
    .map((s) => s.item);
}

export function getConnectedProviderModelGroups(): ConnectedProviderGroup[] {
  const connSet = new Set(workspace.connectedProviders);
  const disSet = new Set<string>(
    Array.isArray(workspace.globalConfig?.disabled_providers)
      ? workspace.globalConfig.disabled_providers
      : []
  );
  const cfgProviders = workspace.globalConfig?.provider ?? {};
  const groups: ConnectedProviderGroup[] = [];

  for (const p of workspace.providers) {
    if (disSet.has(p.id)) continue;
    const isConn = connSet.has(p.id) || Boolean(cfgProviders[p.id]) || p.id === "opencode";
    if (!isConn) continue;

    const models = Object.entries(p.models || {}).map(([mID, mInfo]) => ({
      id: mID,
      name: mInfo?.name || mID,
      fullKey: `${p.id}/${mID}`,
      free: isModelFree(p.id, mID, mInfo),
    }));

    if (models.length > 0) {
      groups.push({
        providerID: p.id,
        providerName: p.name || p.id,
        models,
      });
    }
  }

  return groups.sort((a, b) => {
    const aCustom = isCustomOrConfiguredProvider(a.providerID);
    const bCustom = isCustomOrConfiguredProvider(b.providerID);
    if (aCustom !== bCustom) return aCustom ? -1 : 1;
    return a.providerName.localeCompare(b.providerName);
  });
}
