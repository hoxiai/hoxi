import { requestJSON } from "./http";

export type RemoteTunnelState = "disabled" | "connecting" | "online" | "offline";
export type RemoteTunnelPendingState = "waiting" | "approved" | "denied" | "expired";

/** In-flight one-click pairing request, waiting for an admin to approve it in GoPanel. */
export interface RemoteTunnelPending {
  requestId: string;
  userCode: string;
  expiresAt: string;
  state: RemoteTunnelPendingState;
}

/** Desktop ⇄ GoPanel reverse tunnel status. The credential token never leaves the backend. */
export interface RemoteTunnelStatus {
  enabled: boolean;
  state: RemoteTunnelState;
  controllerUrl?: string;
  nodeId?: number;
  name?: string;
  message?: string;
  connectedAt?: string;
  pending?: RemoteTunnelPending;
}

/** Manual fallback: the user pasted a 32-char code issued in GoPanel → Nodes → Tunnel. */
export interface RemoteTunnelPairRequest {
  controllerUrl: string;
  code: string;
  name: string;
}

/** One-click: the desktop asks GoPanel to open a request the admin then approves. */
export interface RemoteTunnelRequestPairRequest {
  controllerUrl: string;
  name: string;
}

export const remoteTunnelAPI = {
  getStatus(): Promise<RemoteTunnelStatus> {
    return requestJSON("/api/hoxi/tunnel/status");
  },
  requestPair(req: RemoteTunnelRequestPairRequest): Promise<RemoteTunnelStatus> {
    return requestJSON("/api/hoxi/tunnel/pair/request", { method: "POST", body: JSON.stringify(req) });
  },
  cancelPair(): Promise<RemoteTunnelStatus> {
    return requestJSON("/api/hoxi/tunnel/pair/cancel", { method: "POST", body: "{}" });
  },
  pair(req: RemoteTunnelPairRequest): Promise<RemoteTunnelStatus> {
    return requestJSON("/api/hoxi/tunnel/pair", { method: "POST", body: JSON.stringify(req) });
  },
  disable(): Promise<RemoteTunnelStatus> {
    return requestJSON("/api/hoxi/tunnel/disable", { method: "POST", body: "{}" });
  },
};
