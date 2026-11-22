import { onDesktopIntent } from "./desktop-events";
import { requestJSON } from "./http";

const base = "/api/hoxi/app-grants";

export interface AppGrant {
  id: string;
  appId: string;
  name: string;
  scopes: string[];
  allowedWorkspaces: string[];
  ephemeral?: boolean;
}

export interface AppConnectRequest {
  requestId: string;
  appId: string;
  name: string;
  scopes: string[];
  redirectUri?: string;
  expiresAt: number;
}

export interface AppApprovalRequest {
  approvalId: string;
  appId: string;
  appName: string;
  title: string;
  summary: string;
  riskLevel: string;
  details?: Record<string, string>;
  expiresAt: number;
}

const decision = (approve: boolean, scopes?: string[]) => ({
  method: "POST", body: JSON.stringify({ approve, scopes }),
});

export const appGrantsAPI = {
  onChange: onDesktopIntent,
  list: () => requestJSON<AppGrant[]>(base),
  pending: () => requestJSON<AppConnectRequest[]>(`${base}/pending`),
  approvals: () => requestJSON<AppApprovalRequest[]>(`${base}/approvals`),
  decideConnect: (id: string, approve: boolean, scopes?: string[]) =>
    requestJSON<unknown>(`${base}/pending/${encodeURIComponent(id)}/decision`, decision(approve, scopes)),
  decideApproval: (id: string, approve: boolean) =>
    requestJSON<unknown>(`${base}/approvals/${encodeURIComponent(id)}/decision`, decision(approve)),
  revoke: (id: string) => requestJSON<void>(`${base}/${encodeURIComponent(id)}`, { method: "DELETE" }),
};
