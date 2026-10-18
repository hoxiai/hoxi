// web/src/services/runner-api.ts
import type {
  MessageInfo,
  MessagePart,
  SessionInfo,
  SessionRuntime,
  SessionStatus,
} from "./sse-types";
import type { PromptAttachment } from "./api-types";

const knownRunnerSessionIDs = new Set<string>();

export function isRunnerSessionID(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  if (sessionID.startsWith("ses_agy_") || sessionID.startsWith("ses_cld_")) {
    return true;
  }
  return knownRunnerSessionIDs.has(sessionID);
}

export function registerRunnerSessions(sessions: SessionInfo[]): void {
  for (const s of sessions) {
    if (s && s.id && s.runtime && s.runtime !== "opencode") {
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
  async listSessions(directory?: string): Promise<SessionInfo[]> {
    const list = await requestJSON<SessionInfo[]>(
      withDir("/api/hoxi/runner/sessions", directory)
    );
    if (Array.isArray(list)) {
      registerRunnerSessions(list);
      return list;
    }
    return [];
  },

  getSessionStatuses(): Promise<Record<string, SessionStatus>> {
    return requestJSON("/api/hoxi/runner/sessions/status");
  },

  async getSession(sessionID: string, directory?: string): Promise<SessionInfo> {
    const info = await requestJSON<SessionInfo>(
      withDir(`/api/hoxi/runner/sessions/${encodeURIComponent(sessionID)}`, directory)
    );
    if (info?.id) registerRunnerSessions([info]);
    return info;
  },

  async createSession(opts: {
    runtime: "antigravity" | "claude-code";
    title?: string;
    directory?: string;
    modelID?: string;
    providerID?: string;
    mode?: string;
  }): Promise<SessionInfo> {
    const info = await requestJSON<SessionInfo>("/api/hoxi/runner/sessions", {
      method: "POST",
      body: JSON.stringify(opts),
    });
    if (info?.id) registerRunnerSessions([info]);
    return info;
  },

  renameSession(sessionID: string, title: string): Promise<SessionInfo> {
    return requestJSON(`/api/hoxi/runner/sessions/${encodeURIComponent(sessionID)}`, {
      method: "PATCH",
      body: JSON.stringify({ title }),
    });
  },

  archiveSession(sessionID: string, archived: boolean): Promise<SessionInfo> {
    return requestJSON(`/api/hoxi/runner/sessions/${encodeURIComponent(sessionID)}`, {
      method: "PATCH",
      body: JSON.stringify({
        time: { archived: archived ? Date.now() : 0 },
      }),
    });
  },

  deleteSession(sessionID: string): Promise<void> {
    knownRunnerSessionIDs.delete(sessionID);
    return requestJSON(`/api/hoxi/runner/sessions/${encodeURIComponent(sessionID)}`, {
      method: "DELETE",
    });
  },

  getMessages(
    sessionID: string
  ): Promise<Array<{ info: MessageInfo; parts: MessagePart[] }>> {
    return requestJSON(
      `/api/hoxi/runner/sessions/${encodeURIComponent(sessionID)}/message`
    );
  },

  sendPromptAsync(
    sessionID: string,
    text: string,
    options?: {
      agent?: string;
      model?: { providerID: string; modelID: string };
      variant?: string;
      attachments?: PromptAttachment[];
    }
  ): Promise<void> {
    const parts: Array<{ type: "text"; text: string } | PromptAttachment> = [];
    if (options?.attachments?.length) parts.push(...options.attachments);
    if (text || parts.length === 0) parts.push({ type: "text", text });
    return requestJSON(
      `/api/hoxi/runner/sessions/${encodeURIComponent(sessionID)}/prompt`,
      {
        method: "POST",
        body: JSON.stringify({
          text,
          agent: options?.agent || "build",
          ...(options?.model ? { model: options.model } : {}),
          ...(options?.variant ? { variant: options.variant } : {}),
          parts,
        }),
      }
    );
  },

  abortSession(sessionID: string): Promise<void> {
    return requestJSON(
      `/api/hoxi/runner/sessions/${encodeURIComponent(sessionID)}/abort`,
      {
        method: "POST",
        body: JSON.stringify({}),
      }
    );
  },

  listModels(
    runtime: "antigravity" | "claude-code",
    forceRefresh = false
  ): Promise<RunnerModelsResponse> {
    const qs = forceRefresh ? "?refresh=1" : "";
    return requestJSON(`/api/hoxi/runner/${runtime}/models${qs}`);
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
