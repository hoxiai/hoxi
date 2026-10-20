import { createStore } from "solid-js/store";
import { t } from "../i18n";
import { telemetryAPI } from "../services/telemetry";

export const [telemetry, setTelemetry] = createStore({
  enabled: true,
  envOverride: false,
  loaded: false,
  saving: false,
  error: "",
});

let loading: Promise<void> | undefined;

export function loadTelemetrySettings(): Promise<void> {
  if (loading) return loading;
  loading = (async () => {
    try {
      const settings = await telemetryAPI.get();
      setTelemetry({ enabled: settings.enabled, envOverride: settings.envOverride, loaded: true, error: "" });
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
    setTelemetry({ enabled: settings.enabled, envOverride: settings.envOverride, loaded: true });
  } catch {
    setTelemetry("error", t("privacy.telemetrySaveError"));
  } finally {
    setTelemetry("saving", false);
  }
}
