import { createStore } from "solid-js/store";
import { t } from "../i18n";
import { telemetryAPI, type TelemetrySettings } from "../services/telemetry";

export const [telemetry, setTelemetry] = createStore({
  enabled: false,
  envOverride: false,
  offline: false,
  offlineEnvOverride: false,
  loaded: false,
  saving: false,
  error: "",
});

let loading: Promise<void> | undefined;

function applySettings(settings: TelemetrySettings) {
  setTelemetry({
    enabled: Boolean(settings.enabled),
    envOverride: Boolean(settings.envOverride),
    offline: Boolean(settings.offline),
    offlineEnvOverride: Boolean(settings.offlineEnvOverride),
    loaded: true,
    error: "",
  });
}

export function loadTelemetrySettings(): Promise<void> {
  if (loading) return loading;
  loading = (async () => {
    try {
      const settings = await telemetryAPI.get();
      applySettings(settings);
    } catch {
      setTelemetry({ loaded: true, error: t("privacy.telemetryLoadError") });
    } finally {
      loading = undefined;
    }
  })();
  return loading;
}

export async function saveTelemetryEnabled(enabled: boolean): Promise<void> {
  if (telemetry.saving || telemetry.envOverride) return;
  setTelemetry({ saving: true, error: "" });
  try {
    const settings = await telemetryAPI.put(enabled);
    applySettings(settings);
  } catch {
    setTelemetry("error", t("privacy.telemetrySaveError"));
  } finally {
    setTelemetry("saving", false);
  }
}

export async function saveOfflineMode(offline: boolean): Promise<void> {
  if (telemetry.saving || telemetry.offlineEnvOverride) return;
  setTelemetry({ saving: true, error: "" });
  try {
    const settings = await telemetryAPI.put({ offline });
    applySettings(settings);
  } catch {
    setTelemetry("error", t("privacy.offlineSaveError"));
  } finally {
    setTelemetry("saving", false);
  }
}
