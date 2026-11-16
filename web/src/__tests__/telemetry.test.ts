import { telemetryAPI } from "../services/telemetry";
import {
  loadTelemetrySettings,
  saveOfflineMode,
  saveTelemetryEnabled,
  setTelemetry,
  telemetry,
} from "../store/telemetry";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

function reset() {
  setTelemetry({
    enabled: false,
    envOverride: false,
    offline: false,
    offlineEnvOverride: false,
    loaded: false,
    saving: false,
    error: "",
  });
}

describe("telemetry & silent offline preferences", () => {
  it("loads the stored values and persists telemetry and offline changes", async () => {
    const api = { ...telemetryAPI };
    try {
      telemetryAPI.get = async () => ({
        enabled: false,
        envOverride: false,
        offline: false,
        offlineEnvOverride: false,
      });
      await loadTelemetrySettings();
      assertEqual(telemetry.enabled, false);
      assertEqual(telemetry.offline, false);
      assertEqual(telemetry.loaded, true);

      telemetryAPI.put = async (patch) => ({
        enabled: typeof patch === "boolean" ? patch : Boolean(patch.enabled ?? telemetry.enabled),
        envOverride: false,
        offline: typeof patch === "boolean" ? telemetry.offline : Boolean(patch.offline ?? telemetry.offline),
        offlineEnvOverride: false,
      });
      await saveTelemetryEnabled(true);
      assertEqual(telemetry.enabled, true);
      assertEqual(telemetry.error, "");

      await saveOfflineMode(true);
      assertEqual(telemetry.offline, true);
      assertEqual(telemetry.enabled, true);
    } finally {
      Object.assign(telemetryAPI, api);
      reset();
    }
  });

  it("keeps the previous value and reports save failures", async () => {
    const api = { ...telemetryAPI };
    try {
      setTelemetry({ enabled: true });
      telemetryAPI.put = async () => {
        throw new Error("offline");
      };
      await saveTelemetryEnabled(false);
      assertEqual(telemetry.enabled, true);
      assertTrue(Boolean(telemetry.error));
      assertEqual(telemetry.saving, false);
    } finally {
      Object.assign(telemetryAPI, api);
      reset();
    }
  });

  it("does not write when environment variables override the choice", async () => {
    const api = { ...telemetryAPI };
    let writes = 0;
    try {
      setTelemetry({
        enabled: false,
        envOverride: true,
        offline: true,
        offlineEnvOverride: true,
        loaded: true,
        error: "",
      });
      telemetryAPI.put = async () => {
        writes++;
        return { enabled: true, envOverride: true, offline: false, offlineEnvOverride: true };
      };
      await saveTelemetryEnabled(true);
      await saveOfflineMode(false);
      assertEqual(writes, 0);
      assertEqual(telemetry.enabled, false);
      assertEqual(telemetry.offline, true);
    } finally {
      Object.assign(telemetryAPI, api);
      reset();
    }
  });
});
