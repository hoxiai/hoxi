// web/src/store/workspace-providers.ts
import { t } from "../i18n";
import {
  hoxiAPI,
  runnerAPI,
  type ProviderInfo,
} from "../services/api";
import { syncOfficialModelPricing } from "./leaderboard-state";
import {
  AUTO_PROVIDER_ID,
  buildAutoVirtualProvider,
  syncAutoPoolsFromServer,
} from "./auto-models";
import { splitAgyModelEffortSuffix } from "./provider-catalog";
import { parseModelString, updateSessionAI } from "./session-ai";
import { desktopAuth, desktopAuthGeneration } from "./auth-state";
import {
  DEFAULT_AGENTS,
  EFFORT_KEY,
  FAVORITE_MODELS_KEY,
  OFFICIAL_PROVIDER_ID,
  RECENT_MODELS_KEY,
  SELECTED_MODEL_KEY,
  setWorkspace,
  workspace,
  writeStorageItem,
  writeStorageJSON,
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

export function pushRecentModel(model: SelectedModel) {
  const filtered = workspace.recentModels.filter(
    (m) => !(m.providerID === model.providerID && m.modelID === model.modelID)
  );
  const next = [model, ...filtered].slice(0, 6);
  setWorkspace("recentModels", next);
  writeStorageJSON(RECENT_MODELS_KEY, next);
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
  if (!workspace.activeSessionID) {
    writeStorageJSON(SELECTED_MODEL_KEY, model);
    if (patch.effort !== undefined) writeStorageItem(EFFORT_KEY, patch.effort);
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

export function selectDefaultModel(model: SelectedModel) {
  updateSessionAI({ model }, null);
  writeStorageJSON(SELECTED_MODEL_KEY, model);
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
  if (!workspace.activeSessionID) {
    writeStorageItem(EFFORT_KEY, effort);
    if (patch.model) writeStorageJSON(SELECTED_MODEL_KEY, patch.model);
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
  writeStorageJSON(FAVORITE_MODELS_KEY, next);
}

let inFlightLoadProviders: Promise<void> | null = null;

export async function waitForInFlightProvidersLoad(): Promise<void> {
  if (inFlightLoadProviders) {
    await inFlightLoadProviders;
  }
}

export function isModelSelectionValid(model: SelectedModel | null | undefined): boolean {
  if (!model?.providerID || !model?.modelID) return false;
  if (model.providerID === AUTO_PROVIDER_ID) {
    return Boolean(buildAutoVirtualProvider().models?.[model.modelID]);
  }
  return Boolean(
    workspace.providers.some((p) => p.id === model.providerID && p.models?.[model.modelID]) ||
      workspace.hoxiProviders?.[model.providerID]?.models?.[model.modelID]
  );
}

export function shouldAutoSelectProvisionedModel(providerID: string): boolean {
  const cur = workspace.selectedModel;
  if (!cur) return true;
  if (cur.providerID === providerID) {
    return !isModelSelectionValid(cur);
  }
  if (isModelSelectionValid(cur)) return false;
  return workspace.providers.some((p) => p.id !== providerID);
}

export async function loadProviders(forceRefresh = false): Promise<void> {
  const account = desktopAuthGeneration();
  const run = async () => {
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

    if (account !== desktopAuthGeneration() || dir !== (workspace.rootDir || undefined)) return;
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
      // Prefer the signed-in account's official channel when nothing valid is selected.
      if (desktopAuth.isLoggedIn) {
        const official = all.find((p) => p.id === OFFICIAL_PROVIDER_ID);
        const officialModel = official ? Object.keys(official.models || {})[0] : undefined;
        if (official && officialModel) {
          selectDefaultModel({ providerID: OFFICIAL_PROVIDER_ID, modelID: officialModel });
          return;
        }
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
    }  } catch (err) {
    console.error("Failed to load providers:", err);
  }
  };
  const promise = run();
  inFlightLoadProviders = promise;
  try {
    await promise;
  } finally {
    if (inFlightLoadProviders === promise) {
      inFlightLoadProviders = null;
    }
  }
}

export const refreshProviders = loadProviders;

