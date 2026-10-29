import { requestJSON } from "./http";

export interface ComputerScope { sessionID: string; directory: string }
export interface ComputerPermissions { supported: boolean; platform: string; accessibility: boolean; screenRecording: boolean; foregroundOnly: boolean }
export interface ComputerApp { id: string; pid: number; name: string; bundleID: string; path: string; started: string; blocked: boolean }
export interface ComputerGrant { app: ComputerApp; scope: ComputerScope; paused: boolean; active: boolean; lastAction?: string; lastActionAt?: number }
export interface ComputerRect { x: number; y: number; width: number; height: number }
export interface ComputerWindow { id: string; title: string; bounds: ComputerRect }
export interface ComputerElement { id: number; role: string; name: string; value?: string; secure?: boolean; bounds: ComputerRect; windowBounds?: ComputerRect }
export interface ComputerSnapshot { app: ComputerApp; windows: ComputerWindow[]; elements: ComputerElement[]; windowID: string; observationID: string; image?: string; width: number; height: number }

export function computerURL(endpoint: string, scope?: ComputerScope): string {
  const query = scope ? `?${new URLSearchParams({ sessionID: scope.sessionID, directory: scope.directory })}` : "";
  return `/api/hoxi/computer/${endpoint}${query}`;
}
async function request<T>(endpoint: string, signal: AbortSignal, scope?: ComputerScope, body?: object): Promise<T> {
  return requestJSON<T>(computerURL(endpoint, scope), { method: body ? "POST" : "GET", cache: "no-store", signal,
    headers: { "X-Hoxi-Computer": "true" }, body: body ? JSON.stringify(body) : undefined });
}
export const computerAPI = {
  permissions: (signal: AbortSignal) => request<ComputerPermissions>("permissions", signal),
  requestPermission: (permission: "screen" | "accessibility", signal: AbortSignal) => request<ComputerPermissions>("permissions", signal, undefined, { permission }),
  apps: (signal: AbortSignal) => request<{ apps: ComputerApp[] }>("apps", signal),
  state: (scope: ComputerScope, signal: AbortSignal) => request<{ grant: ComputerGrant | null }>("state", signal, scope),
  grant: (scope: ComputerScope, appID: string, locale: string, signal: AbortSignal) => request<{ grant: ComputerGrant | null }>("grant", signal, scope, { appID, locale }),
  revoke: (scope: ComputerScope, signal: AbortSignal) => request<{ grant: null }>("revoke", signal, scope, {}),
  pause: (scope: ComputerScope, paused: boolean, signal: AbortSignal) => request<{ grant: ComputerGrant | null }>("pause", signal, scope, { paused }),
  observe: (scope: ComputerScope, windowID: string, signal: AbortSignal, withImage = true) => request<ComputerSnapshot>("action", signal, scope, { action: withImage ? "screenshot" : "state", ...(windowID ? { windowID } : {}) }),
  stop: (signal: AbortSignal) => request<{ stopped: boolean }>("stop", signal, undefined, {}),
};
