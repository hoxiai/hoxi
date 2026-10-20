import { telemetryAPI } from "../services/telemetry";
import {
  loadTelemetrySettings,
  saveTelemetryEnabled,
  setTelemetry,
  telemetry,
} from "../store/telemetry";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

function reset() {
  setTelemetry({ enabled: true, envOverride: false, loaded: false, saving: false, error: "" });
}

describe("telemetry preference", () => {
  it("loads the stored value and persists changes", async () => {
    const api = { ...telemetryAPI };
    try {
      telemetryAPI.get = async () => ({ enabled: false, envOverride: false });
      await loadTelemetrySettings();
      assertEqual(telemetry.enabled, false);
      assertEqual(telemetry.loaded, true);

      telemetryAPI.put = async (enabled) => ({ enabled, envOverride: false });
      await saveTelemetryEnabled(true);
      assertEqual(telemetry.enabled, true);
      assertEqual(telemetry.error, "");
    } finally {
      Object.assign(telemetryAPI, api);
      reset();
    }
  });

  it("keeps the previous value and reports save failures", async () => {
    const api = { ...telemetryAPI };
    try {
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

  it("does not write when the environment overrides the choice", async () => {
    const api = { ...telemetryAPI };
    let writes = 0;
    try {
      setTelemetry({ enabled: false, envOverride: true, loaded: true, error: "" });
      telemetryAPI.put = async (enabled) => {
        writes++;
        return { enabled, envOverride: true };
      };
      await saveTelemetryEnabled(true);
      assertEqual(writes, 0);
      assertEqual(telemetry.enabled, false);
    } finally {
      Object.assign(telemetryAPI, api);
      reset();
    }
  });
});
