// web/src/services/cloudflared.ts
// Supervised Cloudflare Tunnel connector. The connector token never leaves the
// desktop backend; the status payload reports only whether one is stored.
import { requestJSON } from "./http";

export type CloudflaredState = "disabled" | "missing" | "starting" | "online" | "offline";

export interface CloudflaredStatus {
  enabled: boolean;
  state: CloudflaredState;
  hostname?: string;
  binary?: string;
  hasToken: boolean;
  ingressUrl?: string;
  message?: string;
}

export const cloudflaredAPI = {
  getStatus(): Promise<CloudflaredStatus> {
    return requestJSON("/api/hoxi/cloudflared/status");
  },
  enable(token: string, hostname: string): Promise<CloudflaredStatus> {
    return requestJSON("/api/hoxi/cloudflared/enable", {
      method: "POST",
      body: JSON.stringify({ token, hostname }),
    });
  },
  disable(): Promise<CloudflaredStatus> {
    return requestJSON("/api/hoxi/cloudflared/disable", { method: "POST", body: "{}" });
  },
};
