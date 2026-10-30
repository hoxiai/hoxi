// web/src/store/provider-official.ts
import { createStore } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { OfficialChannelModel } from "../services/platform-api";
import { t } from "../i18n";
import {
  desktopAuth,
  desktopAuthGeneration,
  onDesktopAuthChanged,
  refreshAuthStatus,
} from "./auth-state";
import {
  loadProviders,
  selectProviderModelPreservingBusySession,
  shouldAutoSelectProvisionedModel,
  waitForInFlightProvidersLoad,
} from "./workspace-providers";
import { OFFICIAL_PROVIDER_ID, SELECTED_MODEL_KEY, setWorkspace, workspace } from "./workspace-state";
import { persistSessionAI, updateSessionAI } from "./session-ai";

export interface OfficialChannelConnectResult {
  providerId: string;
  baseUrl: string;
  modelCount: number;
}

export interface OfficialChannelModelsUIState {
  loaded: boolean;
  loading: boolean;
  baseUrl: string;
  models: OfficialChannelModel[];
  error: string | null;
}

export const [officialChannelState, setOfficialChannelState] = createStore<OfficialChannelModelsUIState>({
  loaded: false,
  loading: false,
  baseUrl: "",
  models: [],
  error: null,
});

let catalogGeneration = 0;
let officialAutoConnectGeneration: number | null = null;

export function purgeOfficialProviderFromWorkspace(): void {
  setWorkspace("hoxiProviders", OFFICIAL_PROVIDER_ID, undefined as any);
  if (workspace.globalConfig?.provider && OFFICIAL_PROVIDER_ID in workspace.globalConfig.provider) {
    setWorkspace("globalConfig", "provider", OFFICIAL_PROVIDER_ID, undefined);
  }
  setWorkspace("connectedProviders", workspace.connectedProviders.filter((id) => id !== OFFICIAL_PROVIDER_ID));
  setWorkspace("providers", workspace.providers.filter((p) => p.id !== OFFICIAL_PROVIDER_ID));
  if (workspace.defaultAI.model?.providerID === OFFICIAL_PROVIDER_ID) {
    updateSessionAI({ model: null }, null);
    try { localStorage.removeItem(SELECTED_MODEL_KEY); } catch { /* storage unavailable */ }
  }
  for (const [sid, ai] of Object.entries(workspace.aiBySession)) {
    if (ai?.model?.providerID === OFFICIAL_PROVIDER_ID) {
      updateSessionAI({ model: null }, sid);
    }
  }
  persistSessionAI();
}

export function resetOfficialChannelState(): void {
  catalogGeneration++;
  setOfficialChannelState({
    loaded: false,
    loading: false,
    baseUrl: "",
    models: [],
    error: null,
  });
}

onDesktopAuthChanged(() => {
  officialAutoConnectGeneration = null;
  resetOfficialChannelState();
  purgeOfficialProviderFromWorkspace();
});

// loadOfficialChannelModels refreshes the model catalog the signed-in Hoxi
// account may call on the official gateway channel.
export async function loadOfficialChannelModels(force = false): Promise<void> {
  if (!desktopAuth.isLoggedIn) {
    resetOfficialChannelState();
    return;
  }
  if (officialChannelState.loading) return;
  if (officialChannelState.loaded && !force) return;
  const account = desktopAuthGeneration();
  const request = ++catalogGeneration;
  const current = () => account === desktopAuthGeneration() && request === catalogGeneration && desktopAuth.isLoggedIn;
  setOfficialChannelState({ loading: true, error: null });
  try {
    const res = await hoxiAPI.fetchOfficialChannelModels();
    if (!current()) return;
    if (!res.authenticated) {
      purgeOfficialProviderFromWorkspace();
      resetOfficialChannelState();
      await refreshAuthStatus();
      return;
    }
    setOfficialChannelState({
      loaded: true,
      loading: false,
      baseUrl: res.baseUrl || "",
      models: res.authenticated ? res.models || [] : [],
      error: null,
    });
  } catch (err: any) {
    if (!current()) return;
    setOfficialChannelState({
      loading: false,
      error: err?.message || t("models.officialLoadFailed"),
    });
  }
}

// connectOfficialChannel provisions/refreshes the official gateway provider from
// the signed-in Hoxi account device token, then adopts its authorized models.
export async function connectOfficialChannel(): Promise<OfficialChannelConnectResult> {
  const account = desktopAuthGeneration();
  const current = () => account === desktopAuthGeneration() && desktopAuth.isLoggedIn;
  if (!current()) throw new Error(t("models.officialConnectLoginRequired"));
  const res = await hoxiAPI.connectOfficialChannelProvider();
  if (!current() || !res?.authenticated) {
    throw new Error(t("models.officialConnectLoginRequired"));
  }
  await loadProviders(true);
  if (!current()) throw new Error(t("models.officialConnectLoginRequired"));
  const pid = (res.providerId || OFFICIAL_PROVIDER_ID).trim();
  const firstModelID =
    res.models?.[0]?.id?.trim() ||
    Object.keys(workspace.hoxiProviders?.[pid]?.models || {})[0];
  if (firstModelID && shouldAutoSelectProvisionedModel(pid)) {
    selectProviderModelPreservingBusySession({ providerID: pid, modelID: firstModelID });
  }
  await loadOfficialChannelModels(true);
  return {
    providerId: pid,
    baseUrl: res.baseUrl,
    modelCount: res.modelCount ?? res.models?.length ?? 0,
  };
}


// ensureOfficialProviderConnected provisions the official Hoxi channel once the
// account is signed in so its models always appear as a real, selectable
// provider in the model picker. When it already exists it only refreshes the
// account's callable-model catalog (no config rewrite).
export async function ensureOfficialProviderConnected(): Promise<void> {
  const account = desktopAuthGeneration();
  if (!desktopAuth.isLoggedIn || officialAutoConnectGeneration === account) return;
  await waitForInFlightProvidersLoad();
  if (!desktopAuth.isLoggedIn || account !== desktopAuthGeneration() || officialAutoConnectGeneration === account) return;
  const connected =
    Boolean(workspace.hoxiProviders?.[OFFICIAL_PROVIDER_ID]) ||
    Boolean(workspace.connectedProviders?.includes(OFFICIAL_PROVIDER_ID));
  if (connected) {
    await loadOfficialChannelModels();
    return;
  }
  officialAutoConnectGeneration = account;
  try {
    await connectOfficialChannel();
  } catch (err: any) {
    // Keep the failure visible in the official settings entry instead of only
    // surfacing it later as a generic engine 500 on the selected model.
    setOfficialChannelState({ error: err?.message || t("models.officialConnectFailed") });
  } finally {
    if (officialAutoConnectGeneration === account) officialAutoConnectGeneration = null;
  }
}
