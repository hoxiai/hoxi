import { requestJSON } from "./http";

/** Non-secret metadata for a local relay key. The token is shown only once. */
export interface RelayKeyInfo {
  id: string;
  prefix: string;
  label?: string;
  createdAt: number;
  lastUsed?: number;
}

export interface RelayKeyCreateResponse {
  key: RelayKeyInfo;
  token: string;
}

export interface RelayStatus {
  ok: boolean;
  baseURL: string;
  endpoints: { chatCompletions: string; models: string };
  keys: RelayKeyInfo[];
}

export const relayAPI = {
  getStatus(): Promise<RelayStatus> {
    return requestJSON("/api/hoxi/provider/relay/keys");
  },
  createKey(label: string): Promise<RelayKeyCreateResponse> {
    return requestJSON("/api/hoxi/provider/relay/keys", {
      method: "POST",
      body: JSON.stringify({ label }),
    });
  },
  revokeKey(id: string): Promise<{ ok: boolean; keys: RelayKeyInfo[] }> {
    return requestJSON(`/api/hoxi/provider/relay/keys?id=${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
  },
};
