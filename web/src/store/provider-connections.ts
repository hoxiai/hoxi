// web/src/store/provider-connections.ts
import { createStore } from "solid-js/store";
import { t } from "../i18n";
import {
  hoxiAPI,
  isRunnerAuthMode,
  type LocalCLIStatusItem,
  type ProviderAuthAuthorization,
} from "../services/api";
import type { ProviderMcpOAuthPollResponse } from "../services/platform-api";
import { disposeOpenCodeInstance, patchGlobalConfig } from "./global-config";
import {
  isNonChatUtilityModelID,
  type FreeTierGuideItem,
} from "./auto-models";
import { isImportedOpenCodeProvider } from "./provider-catalog";
import { parseModelString } from "./session-ai";
import {
  loadAgents,
  loadProviders,
  MASKED_SECRET,
  pushRecentModel,
  selectDefaultModel,
  selectModel,
  selectProviderModelPreservingBusySession,
  shouldAutoSelectProvisionedModel,
} from "./workspace-providers";
import { workspace } from "./workspace-state";

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
            npm:
              existingProvider.npm ||
              (pid === "google" || pid === "gemini"
                ? "@ai-sdk/google"
                : pid === "anthropic"
                  ? "@ai-sdk/anthropic"
                  : "@ai-sdk/openai-compatible"),
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
        if (parsed) selectModel(parsed, false);
      } else if (nextAgentModel === null || nextAgentModel === "") {
        if (workspace.defaultAI.model) {
          selectModel(workspace.defaultAI.model, false);
        }
      }
    }
  }
}

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
  const pid = (opts.providerID || "hoxi-gateway").trim();
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
          if (shouldAutoSelectProvisionedModel(pid)) {
            selectProviderModelPreservingBusySession({ providerID: pid, modelID: firstModelID });
          } else {
            pushRecentModel({ providerID: pid, modelID: firstModelID });
          }
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
