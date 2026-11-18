// web/src/services/host-profiles-api.ts
import { requestJSON } from "./http";

export type DataProfileKind = "http_api" | "sqlite" | "postgres" | "mysql" | "ssh";

export interface DataProfileItem {
  id: string;
  kind: DataProfileKind;
  description?: string;
  baseUrl?: string;
  dbPath?: string;
  dsnMasked?: string;
  hasDSN?: boolean;
  sshHost?: string;
  sshPort?: number;
  sshUser?: string;
  defaultCwd?: string;
  authMode?: string;
  hasSecret: boolean;
  readOnly: boolean;
  updatedAt: number;
}

export interface DataProfileUpsertPayload {
  id: string;
  kind: DataProfileKind;
  description?: string;
  baseUrl?: string;
  dbPath?: string;
  dsn?: string;
  sshHost?: string;
  sshPort?: number;
  sshUser?: string;
  defaultCwd?: string;
  authMode?: string;
  secret?: string;
  readOnly: boolean;
}

export interface RemoteHostItem {
  id: string;
  name: string;
  url: string;
  hasToken: boolean;
  defaultDir?: string;
  platform?: string;
  arch?: string;
  version?: string;
  lastSeenAt?: number;
  lastLatencyMs?: number;
  updatedAt: number;
}

export interface RemoteHostUpsertPayload {
  id?: string;
  name?: string;
  url?: string;
  token?: string;
  pairUri?: string;
  defaultDir?: string;
}

export interface RemoteHostProbeResponse {
  ok: boolean;
  latencyMs: number;
  host: RemoteHostItem;
  error?: string;
  info?: {
    hostId?: string;
    name: string;
    hostname: string;
    version: string;
    platform: string;
    arch: string;
    rootDir: string;
    engine: string;
  };
}

export const hostProfilesAPI = {
  async listDataProfiles(): Promise<DataProfileItem[]> {
    const res = await requestJSON<{ profiles?: DataProfileItem[] }>("/api/hoxi/dataprofiles");
    return Array.isArray(res?.profiles) ? res.profiles : [];
  },

  saveDataProfile(payload: DataProfileUpsertPayload): Promise<DataProfileItem> {
    return requestJSON<DataProfileItem>("/api/hoxi/dataprofiles", {
      method: "POST",
      body: JSON.stringify(payload),
    });
  },

  async deleteDataProfile(id: string): Promise<void> {
    await requestJSON(`/api/hoxi/dataprofiles?id=${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
  },

  testDataProfile(id: string): Promise<Record<string, any>> {
    return requestJSON<Record<string, any>>("/api/hoxi/dataprofiles/test", {
      method: "POST",
      body: JSON.stringify({ profile: id }),
    });
  },

  async listRemoteHosts(): Promise<RemoteHostItem[]> {
    const res = await requestJSON<{ hosts?: RemoteHostItem[] }>("/api/hoxi/hosts");
    return Array.isArray(res?.hosts) ? res.hosts : [];
  },

  saveRemoteHost(payload: RemoteHostUpsertPayload): Promise<RemoteHostItem> {
    return requestJSON<RemoteHostItem>("/api/hoxi/hosts", {
      method: "POST",
      body: JSON.stringify(payload),
    });
  },

  async deleteRemoteHost(id: string): Promise<void> {
    await requestJSON(`/api/hoxi/hosts?id=${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
  },

  probeRemoteHost(id: string): Promise<RemoteHostProbeResponse> {
    return requestJSON<RemoteHostProbeResponse>("/api/hoxi/hosts/probe", {
      method: "POST",
      body: JSON.stringify({ id }),
    });
  },
};
