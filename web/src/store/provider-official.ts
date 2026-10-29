// web/src/store/provider-official.ts
import { createStore } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { OfficialAinodeModel } from "../services/platform-api";
import { t } from "../i18n";
import {
  desktopAuth,
  onDesktopAuthLoggedOut,
  purgeOfficialProviderFromWorkspace,
} from "./auth-state";
import {
  loadProviders,
  selectProviderModelPreservingBusySession,
  shouldAutoSelectProvisionedModel,
  waitForInFlightProvidersLoad,
} from "./workspace-providers";
import { OFFICIAL_PROVIDER_ID, workspace } from "./workspace-state";

export interface OfficialAinodeConnectResult {
  providerId: string;
  baseUrl: string;
  modelCount: number;
}

export interface OfficialAinodeModelsUIState {
  loaded: boolean;
  loading: boolean;
  baseUrl: string;
  models: OfficialAinodeModel[];
  error: string | null;
}

export const [officialAinodeState, setOfficialAinodeState] = createStore<OfficialAinodeModelsUIState>({
  loaded: false,
  loading: false,
  baseUrl: "",
  models: [],
  error: null,
});

export function resetOfficialAinodeState(): void {
  setOfficialAinodeState({
    loaded: false,
    loading: false,
    baseUrl: "",
    models: [],
    error: null,
  });
}

onDesktopAuthLoggedOut(resetOfficialAinodeState);

// loadOfficialAinodeModels refreshes the model catalog the signed-in Hoxi
// account may call on the official AINode channel.
export async function loadOfficialAinodeModels(force = false): Promise<void> {
  if (!desktopAuth.isLoggedIn) {
    resetOfficialAinodeState();
    return;
  }
  if (officialAinodeState.loading) return;
  if (officialAinodeState.loaded && !force) return;
  setOfficialAinodeState({ loading: true, error: null });
  try {
    const res = await hoxiAPI.fetchOfficialAinodeModels();
    if (!res.authenticated) {
      purgeOfficialProviderFromWorkspace();
    }
    setOfficialAinodeState({
      loaded: true,
      loading: false,
      baseUrl: res.baseUrl || "",
      models: res.authenticated ? res.models || [] : [],
      error: null,
    });
  } catch (err: any) {
    setOfficialAinodeState({
      loading: false,
      error: err?.message || t("models.officialLoadFailed"),
    });
  }
}

// connectOfficialAinode provisions/refreshes the official AINode provider from
// the signed-in Hoxi account device token, then adopts its authorized models.
export async function connectOfficialAinode(): Promise<OfficialAinodeConnectResult> {
  const res = await hoxiAPI.connectOfficialAinodeProvider();
  if (!res?.authenticated) {
    throw new Error(t("models.officialConnectLoginRequired"));
  }
  await loadProviders(true);
  const pid = (res.providerId || OFFICIAL_PROVIDER_ID).trim();
  const firstModelID =
    res.models?.[0]?.id?.trim() ||
    Object.keys(workspace.hoxiProviders?.[pid]?.models || {})[0];
  if (firstModelID && shouldAutoSelectProvisionedModel(pid)) {
    selectProviderModelPreservingBusySession({ providerID: pid, modelID: firstModelID });
  }
  await loadOfficialAinodeModels(true);
  return {
    providerId: pid,
    baseUrl: res.baseUrl,
    modelCount: res.modelCount ?? res.models?.length ?? 0,
  };
}

let officialAutoConnectInFlight = false;

// ensureOfficialProviderConnected provisions the official Hoxi channel once the
// account is signed in so its models always appear as a real, selectable
// provider in the model picker. When it already exists it only refreshes the
// account's callable-model catalog (no config rewrite).
export async function ensureOfficialProviderConnected(): Promise<void> {
  if (!desktopAuth.isLoggedIn || officialAutoConnectInFlight) return;
  await waitForInFlightProvidersLoad();
  if (!desktopAuth.isLoggedIn || officialAutoConnectInFlight) return;
  const connected =
    Boolean(workspace.hoxiProviders?.[OFFICIAL_PROVIDER_ID]) ||
    Boolean(workspace.connectedProviders?.includes(OFFICIAL_PROVIDER_ID));
  if (connected) {
    await loadOfficialAinodeModels();
    return;
  }
  officialAutoConnectInFlight = true;
  try {
    await connectOfficialAinode();
  } catch {
    // Silent: the user can retry from the Hoxi official settings entry.
  } finally {
    officialAutoConnectInFlight = false;
  }
}
