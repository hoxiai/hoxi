import { requestJSON } from "./http";

export interface TelemetrySettings {
  enabled: boolean;
  envOverride: boolean;
}

export const telemetryAPI = {
  get(): Promise<TelemetrySettings> {
    return requestJSON("/api/hoxi/telemetry");
  },
  put(enabled: boolean): Promise<TelemetrySettings> {
    return requestJSON("/api/hoxi/telemetry", {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    });
  },
};
