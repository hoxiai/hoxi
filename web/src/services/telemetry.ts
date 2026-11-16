import { requestJSON } from "./http";

export interface TelemetrySettings {
  enabled: boolean;
  envOverride: boolean;
  offline?: boolean;
  offlineEnvOverride?: boolean;
}

export interface TelemetryPatch {
  enabled?: boolean;
  offline?: boolean;
}

export const telemetryAPI = {
  get(): Promise<TelemetrySettings> {
    return requestJSON("/api/hoxi/telemetry");
  },
  put(patchOrEnabled: boolean | TelemetryPatch): Promise<TelemetrySettings> {
    const payload: TelemetryPatch =
      typeof patchOrEnabled === "boolean" ? { enabled: patchOrEnabled } : patchOrEnabled;
    return requestJSON("/api/hoxi/telemetry", {
      method: "PUT",
      body: JSON.stringify(payload),
    });
  },
};
