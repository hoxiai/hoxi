// web/src/store/workspace-providers.ts
import { createStore } from "solid-js/store";
import { t } from "../i18n";
import {
  hoxiAPI,
  isRunnerAuthMode,
  runnerAPI,
  type LocalCLIStatusItem,
  type ProviderInfo,
  type ProviderAuthAuthorization,
} from "../services/api";
import type { ProviderMcpOAuthPollResponse } from "../services/platform-api";
import { disposeOpenCodeInstance, patchGlobalConfig } from "./global-config";
import { syncOfficialModelPricing } from "./leaderboard-state";
import {
  AUTO_PROVIDER_ID,
  buildAutoVirtualProvider,
  isNonChatUtilityModelID,
  syncAutoPoolsFromServer,
  type FreeTierGuideItem,
} from "./auto-models";
import {
  isImportedOpenCodeProvider,
  splitAgyModelEffortSuffix,
} from "./provider-catalog";
import { parseModelString, updateSessionAI } from "./session-ai";
import {
  DEFAULT_AGENTS,
  EFFORT_KEY,
  FAVORITE_MODELS_KEY,
  RECENT_MODELS_KEY,
  SELECTED_MODEL_KEY,
  setWorkspace,
  workspace,
  type ReasoningEffort,
  type SelectedModel,
} from "./workspace-state";

export const MASKED_SECRET = "********";

export const POPULAR_PROVIDER_IDS = [
  "opencode",
  "opencode-go",
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
  const patch: { model: SelectedModel; effort?: ReasoningEffort } = { model };
  if (model.providerID === "antigravity" && workspace.reasoningEffort) {
    const agySplit = splitAgyModelEffortSuffix(model.modelID);
    if (agySplit && agySplit.effort !== workspace.reasoningEffort) {
      patch.effort = agySplit.effort;
    }
  }
  updateSessionAI(patch);
  pushRecentModel(model);
  try {
    if (!workspace.activeSessionID) {
      localStorage.setItem(SELECTED_MODEL_KEY, JSON.stringify(model));
      if (patch.effort !== undefined) localStorage.setItem(EFFORT_KEY, patch.effort);
    }
  } catch {
    /* ignore */
  }
}

export function activeModelInfo() {
  const sel = workspace.selectedModel;
  if (!sel) return undefined;
  if (sel.providerID === AUTO_PROVIDER_ID) {
    return buildAutoVirtualProvider().models?.[sel.modelID];
  }
  const prov = workspace.providers.find((p) => p.id === sel.providerID);
  return prov?.models?.[sel.modelID];
}

function selectDefaultModel(model: SelectedModel) {
  updateSessionAI({ model }, null);
  try { localStorage.setItem(SELECTED_MODEL_KEY, JSON.stringify(model)); } catch { /* storage unavailable */ }
}

function isActiveSessionBusy(): boolean {
  const sid = workspace.activeSessionID;
  if (!sid) return false;
  const st = workspace.statusBySession[sid]?.type;
  return st === "busy" || st === "retry";
}

export function selectProviderModelPreservingBusySession(model: SelectedModel) {
  if (isActiveSessionBusy()) {
    selectDefaultModel(model);
    pushRecentModel(model);
    return;
  }
  selectModel(model, false);
}

export function setReasoningEffort(effort: ReasoningEffort) {
  const patch: { effort: ReasoningEffort; model?: SelectedModel } = { effort };
  const sel = workspace.selectedModel;
  if (sel?.providerID === "antigravity" && effort) {
    const agySplit = splitAgyModelEffortSuffix(sel.modelID);
    if (agySplit) {
      const siblingID = `${agySplit.base}-${effort}`;
      const prov = workspace.providers.find((p) => p.id === "antigravity");
      if (prov?.models?.[siblingID]) {
        patch.model = { providerID: "antigravity", modelID: siblingID };
      }
    }
  }
  updateSessionAI(patch);
  try {
    if (!workspace.activeSessionID) {
      localStorage.setItem(EFFORT_KEY, effort);
      if (patch.model) localStorage.setItem(SELECTED_MODEL_KEY, JSON.stringify(patch.model));
    }
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

export async function loadProviders(forceRefresh = false): Promise<void> {
  try {
    const dir = workspace.rootDir || undefined;
    const [res, cfg, authMethods, hoxiCfg, agyRunner, geminiRunner, claudeRunner, codeBuddyRunner, qoderRunner] = await Promise.all([
      hoxiAPI
        .listProviders(dir)
        .catch(() => ({ all: [], connected: [] as string[], default: {} as Record<string, string> })),
      hoxiAPI.getGlobalConfig().catch(() => ({} as Record<string, any>)),
      hoxiAPI.getProviderAuthMethods(dir).catch(() => ({})),
      hoxiAPI.getHoxiModelsConfig().catch(() => ({
        hoxiProviders: {} as Record<string, any>,
        openCodeProviders: [] as string[],
        configPath: ".hoxi/models.json",
      })),
      runnerAPI.listModels("antigravity", forceRefresh).catch(() => null),
      runnerAPI.listModels("gemini-cli", forceRefresh).catch(() => null),
      runnerAPI.listModels("claude-code", forceRefresh).catch(() => null),
      runnerAPI.listModels("codebuddy", forceRefresh).catch(() => null),
      runnerAPI.listModels("qoder", forceRefresh).catch(() => null),
      syncOfficialModelPricing(forceRefresh).catch(() => {}),
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
        const hasImgModality = Boolean(
          (Array.isArray(mCfg.modalities?.input) && mCfg.modalities.input.includes("image")) ||
            mCfg.attachment ||
            mCfg.capabilities?.attachment ||
            mCfg.capabilities?.input?.image ||
            rtModel?.capabilities?.input?.image
        );
        exactModels[mid] = {
          ...(rtModel || {}),
          id: mid,
          name: mCfg.name || rtModel?.name || mid,
          limit: mCfg.limit || rtModel?.limit,
          cost: mCfg.cost || rtModel?.cost,
          free: typeof mCfg.free === "boolean" ? mCfg.free : rtModel?.free,
          attachment: hasImgModality,
          reasoning: Boolean(mCfg.reasoning ?? rtModel?.reasoning),
          variants: mCfg.variants || rtModel?.variants,
          modalities: mCfg.modalities || rtModel?.modalities,
          capabilities: {
            ...(rtModel?.capabilities || {}),
            attachment: hasImgModality,
            reasoning: Boolean(mCfg.reasoning ?? rtModel?.capabilities?.reasoning),
            input: {
              ...(rtModel?.capabilities?.input || {}),
              image: hasImgModality,
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
      const isInstalled = Boolean(r?.installed ?? r?.available);
      if (!isInstalled || !Array.isArray(r?.models) || r.models.length === 0) return;
      const models: Record<string, any> = {};
      for (const m of r.models) {
        if (!m?.id) continue;
        const isFree = Boolean(m.free);
        const hasCost = isFree || m.inputCost !== undefined || m.outputCost !== undefined;
        models[m.id] = {
          id: m.id,
          name: m.name || m.id,
          reasoning: Boolean(m.reasoning),
          efforts: Array.isArray(m.efforts) ? m.efforts : undefined,
          defaultEffort: m.defaultEffort || undefined,
          free: isFree,
          multiplier: m.multiplier || undefined,
          limit:
            m.contextLimit || m.outputLimit
              ? {
                  context: m.contextLimit || undefined,
                  output: m.outputLimit || undefined,
                }
              : undefined,
          cost: hasCost
            ? {
                input: isFree ? 0 : m.inputCost,
                output: isFree ? 0 : m.outputCost,
              }
            : undefined,
          capabilities: {
            reasoning: Boolean(m.reasoning),
            toolcall: true,
            input: { image: Boolean(m.image) },
          },
        };
      }
      byId.set(pid, { id: pid, name, source: "runner", models });
      if (!connected.includes(pid)) connected.unshift(pid);
      if (r.default) defaults[pid] = r.default;
    };

    registerRunnerProvider("antigravity", t("timeline.runtimeAntigravityLocal"), agyRunner);
    if (!hoxiMap["gemini-cli"]) {
      registerRunnerProvider("gemini-cli", t("timeline.runtimeGeminiCLILocal"), geminiRunner);
    }
    if (!hoxiMap["claude-code"]) {
      registerRunnerProvider("claude-code", t("timeline.runtimeClaudeCodeLocal"), claudeRunner);
    }
    if (!hoxiMap["codebuddy"]) {
      registerRunnerProvider("codebuddy", t("timeline.runtimeCodeBuddyLocal"), codeBuddyRunner);
    }
    if (!hoxiMap["qoder"]) {
      registerRunnerProvider("qoder", t("timeline.runtimeQoderLocal"), qoderRunner);
    }

    const all = Array.from(byId.values()).filter((p) => p.id !== AUTO_PROVIDER_ID);
    const cleanConnected = connected.filter((id) => id !== AUTO_PROVIDER_ID);

    setWorkspace({
      providers: all,
      connectedProviders: cleanConnected,
      defaultModels: defaults,
      globalConfig: mergedCfg,
      hoxiProviders: hoxiMap,
      openCodeImportedProviders: openCodeList,
      providerAuthMethods: authMethods || {},
    });
    void syncAutoPoolsFromServer();

    const current = workspace.defaultAI.model;
    const autoVirt = buildAutoVirtualProvider();
    const isValid =
      Boolean(
        current &&
          ((current.providerID === AUTO_PROVIDER_ID && autoVirt.models?.[current.modelID]) ||
            all.some((p) => p.id === current.providerID && p.models && p.models[current.modelID]))
      );

    if (!isValid && all.length > 0) {
      const cfgModel = parseModelString(mergedCfg?.model);
      if (
        cfgModel &&
        ((cfgModel.providerID === AUTO_PROVIDER_ID && autoVirt.models?.[cfgModel.modelID]) ||
          all.some((p) => p.id === cfgModel.providerID && p.models?.[cfgModel.modelID]))
      ) {
        selectDefaultModel(cfgModel);
        return;
      }
      for (const pid of cleanConnected) {
        if (pid === AUTO_PROVIDER_ID) continue;
        // OpenCode's built-in free service is not a user-configured provider.
        if (pid === "opencode" && !hoxiMap[pid] && !mergedCfg.provider?.[pid]) continue;
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
            models: {
              ...(existingProvider.models || {}),
              [opts.customModelID.trim()]: {
                name: opts.customModelID.trim(),
                attachment: !isNonChatUtilityModelID(opts.customModelID),
                reasoning: !isNonChatUtilityModelID(opts.customModelID),
                modalities: {
                  input: !isNonChatUtilityModelID(opts.customModelID)
                    ? ["text", "image"]
                    : ["text"],
                  output: ["text"],
                },
              },
            },
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
    selectProviderModelPreservingBusySession({ providerID: pid, modelID: opts.customModelID.trim() });
  } else if (!workspace.selectedModel) {
    const prov = workspace.providers.find((p) => p.id === pid);
    const defModel = workspace.defaultModels[pid] || Object.keys(prov?.models || {})[0];
    if (defModel) selectProviderModelPreservingBusySession({ providerID: pid, modelID: defModel });
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
      attachment: Boolean(m.image),
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
    nextOptions.apiKey = rawKey;
    if (/^\{env:[^}]+\}$/.test(rawKey)) {
      await hoxiAPI.removeProviderAuth(pid).catch(() => {});
    } else {
      const primaryKey =
        rawKey
          .split(/[\s,;\n]+/)
          .map((s) => s.trim())
          .find(Boolean) || rawKey;
      await hoxiAPI.setProviderAuth(pid, primaryKey).catch(() => {});
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
  if (firstModelID) {
    if (!cur || cur.providerID === pid) {
      const targetModelID = cur?.providerID === pid && nextModels[cur.modelID] ? cur.modelID : firstModelID;
      selectProviderModelPreservingBusySession({ providerID: pid, modelID: targetModelID });
    } else {
      pushRecentModel({ providerID: pid, modelID: firstModelID });
    }
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

/** Restarts the OpenCode instance and reloads providers and agents from it. */
/** Installs the pinned engine when missing, or restarts the supervised daemon. */
export async function installOpenCodeEngine(restart = false): Promise<void> {
  await hoxiAPI.installEngine(restart);
  await Promise.allSettled([
    loadProviders(true),
    loadAgents(workspace.rootDir || undefined),
  ]);
}

export function authorizeProviderOAuth(
  providerID: string,
  method: number
): Promise<ProviderAuthAuthorization> {
  return hoxiAPI.authorizeProviderOAuth(providerID, method, undefined, workspace.rootDir || undefined);
}

/** Finishes an OAuth flow, restarts OpenCode so it loads the credential, and reloads providers. */
export async function completeProviderOAuth(providerID: string, method: number, code?: string): Promise<void> {
  await hoxiAPI.completeProviderOAuth(providerID, method, code, workspace.rootDir || undefined);
  await disposeOpenCodeInstance().catch(() => {});
  await loadProviders();
}

export async function updateGlobalModelConfig(patch: Record<string, any>): Promise<void> {
  await patchGlobalConfig(patch);
  if (typeof patch.model === "string") {
    const parsed = parseModelString(patch.model);
    if (parsed) selectDefaultModel(parsed);
  }
  if (patch.agent && typeof patch.agent === "object") {
    const active = workspace.activeMode;
    if (active && patch.agent[active] !== undefined) {
      const nextAgentModel = patch.agent[active]?.model;
      if (typeof nextAgentModel === "string" && nextAgentModel) {
        const parsed = parseModelString(nextAgentModel);
        if (parsed) selectModel(parsed);
      } else if (nextAgentModel === null || nextAgentModel === "") {
        if (workspace.defaultAI.model) {
          selectModel(workspace.defaultAI.model);
        }
      }
    }
  }
}

export const refreshProviders = loadProviders;

export async function applyFreeTierGuideProvider(item: FreeTierGuideItem): Promise<void> {
  if (!item.customMode || !item.baseURL) return;
  if (workspace.hoxiProviders?.[item.providerID]) return;
  const modelsObj: Record<string, any> = {};
  for (const m of item.defaultModels || []) {
    modelsObj[m.id] = {
      name: m.name,
      reasoning: m.reasoning,
      cost: m.free ? { input: 0, output: 0 } : undefined,
    };
  }
  try {
    await hoxiAPI.saveHoxiProviderConfig(item.providerID, {
      name: item.title || (item.providerID === "siliconflow" ? "SiliconFlow" : item.providerID),
      npm: "@ai-sdk/openai-compatible",
      options: { baseURL: item.baseURL },
      models: modelsObj,
    });
    await loadProviders();
  } catch {
    /* ignore */
  }
}

export async function syncLocalCLIModel(tool: LocalCLIStatusItem) {
  const isRunner = isRunnerAuthMode(tool.authMode);
  let providerID = tool.providerID;
  if (!isRunner) {
    const result = await hoxiAPI.importLocalCLI(tool.id);
    if (!result.ok) throw new Error(result.error || t("common.error"));
    providerID = result.providerID;
  }
  await loadProviders(true);
  const status = await hoxiAPI.getLocalCLIStatus();
  const current = status.tools.find((item) => item.id === tool.id);
  const modelID = current?.defaultModel || workspace.defaultModels[providerID] || current?.models?.[0];
  if (!modelID || !workspace.providers.some((p) => p.id === providerID && p.models?.[modelID])) {
    throw new Error(t("leftNav.handoffCliModelUnavailable", { model: modelID || tool.name }));
  }
  selectProviderModelPreservingBusySession({ providerID, modelID });
  return status;
}

export interface ProviderMcpOAuthUIState {
  authorizingProviderID: string | null;
  pendingState: string | null;
  authURL: string | null;
  error: string | null;
}

export const [providerMcpOAuthState, setProviderMcpOAuthState] = createStore<ProviderMcpOAuthUIState>({
  authorizingProviderID: null,
  pendingState: null,
  authURL: null,
  error: null,
});

let activeMcpOAuthRunID = 0;

export function cancelProviderMcpOAuth(): void {
  activeMcpOAuthRunID++;
  setProviderMcpOAuthState({ authorizingProviderID: null, pendingState: null, authURL: null, error: null });
}

export async function connectProviderWithMcpOAuth(opts: {
  providerID: string;
  providerName?: string;
  siteURL?: string;
  baseURL?: string;
  recommendedModels?: string[];
}): Promise<ProviderMcpOAuthPollResponse | null> {
  const pid = (opts.providerID || "ainode").trim();
  const runID = ++activeMcpOAuthRunID;
  setProviderMcpOAuthState({ authorizingProviderID: pid, pendingState: null, authURL: null, error: null });

  try {
    const startRes = await hoxiAPI.startProviderMcpOAuth({
      providerId: pid,
      providerName: opts.providerName || pid,
      siteUrl: opts.siteURL,
      baseUrl: opts.baseURL,
      recommendedModels: opts.recommendedModels,
    });
    if (runID !== activeMcpOAuthRunID) return null;
    setProviderMcpOAuthState({ pendingState: startRes.state, authURL: startRes.authUrl });

    const deadline = Date.now() + 180_000;
    while (Date.now() < deadline && runID === activeMcpOAuthRunID) {
      await new Promise((resolve) => setTimeout(resolve, 1600));
      if (runID !== activeMcpOAuthRunID) return null;

      const pollRes = await hoxiAPI.pollProviderMcpOAuth(startRes.state);
      if (runID !== activeMcpOAuthRunID) return null;
      if (pollRes.status === "pending") continue;
      if (pollRes.status === "expired" || pollRes.status === "error") {
        const msg = pollRes.error || t("models.mcpOAuthFailed");
        setProviderMcpOAuthState({ authorizingProviderID: null, pendingState: null, error: msg });
        throw new Error(msg);
      }

      if (pollRes.status === "authorized") {
        await loadProviders(true);
        const firstModelID =
          pollRes.models?.[0]?.id?.trim() ||
          Object.keys(workspace.hoxiProviders?.[pid]?.models || {})[0] ||
          opts.recommendedModels?.[0]?.trim();
        if (firstModelID) {
          selectProviderModelPreservingBusySession({ providerID: pid, modelID: firstModelID });
        }

        setProviderMcpOAuthState({ authorizingProviderID: null, pendingState: null, authURL: null, error: null });
        return pollRes;
      }
    }

    if (runID === activeMcpOAuthRunID) {
      const timeoutMsg = t("models.mcpOAuthExpired");
      setProviderMcpOAuthState({ authorizingProviderID: null, pendingState: null, error: timeoutMsg });
      throw new Error(timeoutMsg);
    }
    return null;
  } catch (err: any) {
    if (runID === activeMcpOAuthRunID) {
      setProviderMcpOAuthState({ authorizingProviderID: null, pendingState: null, error: err?.message || t("models.mcpOAuthFailed") });
    }
    throw err;
  }
}

