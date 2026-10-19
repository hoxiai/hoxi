// web/src/services/runner-api.ts
import type { PermissionRequest, SessionInfo, SessionRuntime } from "./sse-types";

const knownRunnerSessionIDs = new Set<string>();

export function isRunnerSessionID(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  if (
    sessionID.startsWith("ses_agy_") ||
    sessionID.startsWith("ses_cld_") ||
    sessionID.startsWith("ses_hoxi_")
  ) {
    return true;
  }
  return knownRunnerSessionIDs.has(sessionID);
}

// Antigravity approval-hook requests are minted by the Go runner gate.
export function isRunnerPermissionID(requestID?: string | null): boolean {
  return !!requestID && requestID.startsWith("per_rnr_");
}

export function forgetRunnerSession(sessionID: string): void {
  knownRunnerSessionIDs.delete(sessionID);
}

export function registerRunnerSessions(sessions: SessionInfo[]): void {
  for (const s of sessions) {
    if (s && s.id && s.runtime && (s.managed || s.runtime !== "opencode")) {
      knownRunnerSessionIDs.add(s.id);
    }
  }
}

export function resolveRunnerRuntimeForProvider(
  providerID?: string | null,
  hasConfiguredProvider = false
): SessionRuntime {
  if (providerID === "antigravity") return "antigravity";
  if (providerID === "claude-code" && !hasConfiguredProvider) return "claude-code";
  return "opencode";
}

function withDir(path: string, directory?: string): string {
  if (!directory) return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}directory=${encodeURIComponent(directory)}`;
}

export async function requestJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers || {}),
    },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`HTTP ${res.status}: ${text}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

// `installed && !current` means hooks.json still points at an old Hoxi binary.
export interface AgyHookStatus {
  path: string;
  executable: string;
  installed: boolean;
  current: boolean;
  error?: string;
}

export interface RunnerModelItem {
  id: string;
  name: string;
  reasoning?: boolean;
  default?: boolean;
}

export interface RunnerModelsResponse {
  installed?: boolean;
  available?: boolean;
  binaryPath?: string;
  default?: string;
  models: RunnerModelItem[];
}

export const runnerAPI = {
  listModels(
    runtime: "antigravity" | "claude-code",
    forceRefresh = false
  ): Promise<RunnerModelsResponse> {
    const qs = forceRefresh ? "?refresh=1" : "";
    return requestJSON(`/api/hoxi/runner/${runtime}/models${qs}`);
  },

  listPermissions(directory?: string): Promise<PermissionRequest[]> {
    return requestJSON(withDir("/api/hoxi/runner/permission", directory));
  },

  // `message` reaches the model as the deny reason, so rejecting can steer
  // the running agy turn without a follow-up prompt.
  replyPermission(requestID: string, reply: "once" | "always" | "reject", message?: string): Promise<void> {
    return requestJSON(`/api/hoxi/runner/permission/${encodeURIComponent(requestID)}/reply`, {
      method: "POST",
      body: JSON.stringify({ reply, message: message || undefined }),
    });
  },

  getAgyHookStatus(): Promise<AgyHookStatus> {
    return requestJSON("/api/hoxi/runner/antigravity/hook");
  },

  installAgyHook(): Promise<AgyHookStatus> {
    return requestJSON("/api/hoxi/runner/antigravity/hook", { method: "POST" });
  },

  uninstallAgyHook(): Promise<AgyHookStatus> {
    return requestJSON("/api/hoxi/runner/antigravity/hook", { method: "DELETE" });
  },
};

export const authAPI = {
  getStatus(): Promise<import("./api-types").AuthStatusSnapshot> {
    return requestJSON("/api/hoxi/auth/status");
  },
  startAuth(): Promise<import("./api-types").StartAuthResponse> {
    return requestJSON("/api/hoxi/auth/start", { method: "POST" });
  },
  exchange(
    handoffToken: string,
    state: string
  ): Promise<{ success: boolean; user: import("./api-types").AuthUserProfile }> {
    return requestJSON("/api/hoxi/auth/exchange", {
      method: "POST",
      body: JSON.stringify({ handoffToken, state }),
    });
  },
  logout(): Promise<{ success: boolean }> {
    return requestJSON("/api/hoxi/auth/logout", { method: "POST" });
  },
};

export const updaterAPI = {
  nativeStatus(): Promise<import("./api-types").NativeUpdateStatus> {
    return requestJSON("/api/hoxi/updater/native");
  },
  install(): Promise<{ started: true }> {
    return requestJSON("/api/hoxi/updater/install", { method: "POST" });
  },
  checkUpdate(): Promise<import("./api-types").UpdateCheckResult> {
    return requestJSON("/api/hoxi/updater/check");
  },
};

export const leaderboardAPI = {
  async getLeaderboard(
    sort: string = "overall",
    limit: number = 10,
    lang: string = "en"
  ): Promise<import("./api-types").LeaderboardModelItem[]> {
    const json = await requestJSON<import("./api-types").LeaderboardResponse>(
      `/api/hoxi/models/leaderboard?sort=${encodeURIComponent(sort)}&limit=${limit}&lang=${encodeURIComponent(lang)}`
    );
    return json.data?.items || [];
  },
  async getConnectableGateways(
    lang: string = "en"
  ): Promise<import("./api-types").ConnectableGatewayItem[]> {
    const json = await requestJSON<import("./api-types").ConnectableGatewaysResponse>(
      `/api/hoxi/gateways/connectable?lang=${encodeURIComponent(lang)}`
    );
    return json.data?.items || [];
  },
};
