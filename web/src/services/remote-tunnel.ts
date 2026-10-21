import { requestJSON } from "./http";

export type RemoteTunnelState = "disabled" | "connecting" | "online" | "offline";

/** Desktop ⇄ GoPanel reverse tunnel status. The credential token never leaves the backend. */
export interface RemoteTunnelStatus {
  enabled: boolean;
  state: RemoteTunnelState;
  controllerUrl?: string;
  nodeId?: number;
  name?: string;
  message?: string;
  connectedAt?: string;
}

export interface RemoteTunnelPairRequest {
  controllerUrl: string;
  code: string;
  name: string;
}

export const remoteTunnelAPI = {
  getStatus(): Promise<RemoteTunnelStatus> {
    return requestJSON("/api/hoxi/tunnel/status");
  },
  pair(req: RemoteTunnelPairRequest): Promise<RemoteTunnelStatus> {
    return requestJSON("/api/hoxi/tunnel/pair", { method: "POST", body: JSON.stringify(req) });
  },
  disable(): Promise<RemoteTunnelStatus> {
    return requestJSON("/api/hoxi/tunnel/disable", { method: "POST", body: "{}" });
  },
};
