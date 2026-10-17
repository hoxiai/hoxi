// web/src/services/api.ts
import type { MessageInfo, MessagePart, SessionInfo, SnapshotFileDiff } from "./sse-types";

export interface WorktreeMeta {
  sessionID: string;
  isolated: boolean;
  directory: string;
  branch?: string;
  baseCommit?: string;
  createdAt: string;
}

export interface ProjectInfo {
  id: string;
  worktree: string;
  vcs?: string;
  icon?: { color?: string; url?: string };
  time: {
    created: number;
    updated: number;
  };
  sandboxes?: string[];
}

export interface WorktreeStatus {
  sessionID: string;
  isolated: boolean;
  directory: string;
  branch?: string;
  baseCommit?: string;
  dirty: boolean;
  changedFiles: number;
  uncommittedLines: number;
}

export interface BootstrapInfo {
  rootDir: string;
  wsBaseURL: string;
  status: {
    attention: number;
    running: number;
    queued: number;
    available: boolean;
  };
}

export interface ProviderModelInfo {
  id: string;
  name?: string;
  reasoning?: boolean;
  attachment?: boolean;
  temperature?: boolean;
  tool_call?: boolean;
  cost?: {
    input?: number;
    output?: number;
  };
  limit?: {
    context?: number;
    output?: number;
  };
  variants?: Record<string, any>;
}

export interface ProviderInfo {
  id: string;
  name: string;
  env?: string[];
  npm?: string;
  models: Record<string, ProviderModelInfo>;
}

export interface ProviderListResponse {
  all: ProviderInfo[];
  default: Record<string, string>;
  connected: string[];
}

export interface SlashCommandInfo {
  name: string;
  description?: string;
  agent?: string;
}

export type CommandInfo = SlashCommandInfo;

export interface AgentInfo {
  name: string;
  description?: string;
  mode?: "primary" | "subagent" | "all";
  hidden?: boolean;
  native?: boolean;
  prompt?: string;
  temperature?: number;
}

function withDir(path: string, directory?: string): string {
  if (!directory) return path;
  const sep = path.includes("?") ? "&" : "?";
  return `${path}${sep}directory=${encodeURIComponent(directory)}`;
}

async function requestJSON<T>(url: string, init?: RequestInit): Promise<T> {
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

export const hoxiAPI = {
  getBootstrap(): Promise<BootstrapInfo> {
    return requestJSON("/api/hoxi/bootstrap");
  },

  // Sessions
  listSessions(directory?: string): Promise<SessionInfo[]> {
    return requestJSON(withDir("/session?limit=50", directory));
  },

  createSession(title?: string, directory?: string): Promise<SessionInfo> {
    return requestJSON(withDir("/session", directory), {
      method: "POST",
      body: JSON.stringify({ title }),
    });
  },

  deleteSession(sessionID: string, directory?: string): Promise<void> {
    return requestJSON(withDir(`/session/${sessionID}`, directory), {
      method: "DELETE",
    });
  },

  getMessages(
    sessionID: string,
    directory?: string
  ): Promise<Array<{ info: MessageInfo; parts: MessagePart[] }>> {
    return requestJSON(withDir(`/session/${sessionID}/message?limit=100`, directory));
  },

  // Prompting & Live Mid-Stream Steering
  sendPromptAsync(
    sessionID: string,
    text: string,
    options?: {
      agent?: "build" | "plan" | string;
      model?: { providerID: string; modelID: string };
      variant?: string;
      directory?: string;
    }
  ): Promise<void> {
    return requestJSON(withDir(`/session/${sessionID}/prompt_async`, options?.directory), {
      method: "POST",
      body: JSON.stringify({
        agent: options?.agent || "build",
        ...(options?.model ? { model: options.model } : {}),
        ...(options?.variant ? { variant: options.variant } : {}),
        parts: [{ type: "text", text }],
      }),
    });
  },

  abortSession(sessionID: string, directory?: string): Promise<void> {
    return requestJSON(withDir(`/session/${sessionID}/abort`, directory), {
      method: "POST",
      body: JSON.stringify({}),
    });
  },

  // Checkpoint Time-Travel Revert & Session Fork
  revertSession(
    sessionID: string,
    messageID: string,
    partID?: string,
    directory?: string
  ): Promise<SessionInfo> {
    return requestJSON(withDir(`/session/${sessionID}/revert`, directory), {
      method: "POST",
      body: JSON.stringify({ messageID, partID }),
    });
  },

  unrevertSession(sessionID: string, directory?: string): Promise<SessionInfo> {
    return requestJSON(withDir(`/session/${sessionID}/unrevert`, directory), {
      method: "POST",
      body: JSON.stringify({}),
    });
  },

  forkSession(sessionID: string, messageID?: string, directory?: string): Promise<SessionInfo> {
    return requestJSON(withDir(`/session/${sessionID}/fork`, directory), {
      method: "POST",
      body: JSON.stringify({ messageID }),
    });
  },

  // Diffs & VCS
  getSessionDiff(sessionID: string, directory?: string): Promise<SnapshotFileDiff[]> {
    return requestJSON(withDir(`/session/${sessionID}/diff`, directory));
  },

  // Interactive Permission Approval Gate
  replyPermission(
    requestID: string,
    reply: "once" | "always" | "reject",
    directory?: string
  ): Promise<void> {
    return requestJSON(withDir(`/permission/${requestID}/reply`, directory), {
      method: "POST",
      body: JSON.stringify({ reply }),
    });
  },

  // Providers, Models & Auth (Direct OpenCode endpoints used by Kilo)
  listProviders(directory?: string): Promise<ProviderListResponse> {
    return requestJSON(withDir("/provider", directory));
  },

  setProviderAuth(providerID: string, apiKey: string): Promise<void> {
    return requestJSON(`/auth/${encodeURIComponent(providerID)}`, {
      method: "PUT",
      body: JSON.stringify({ type: "api", key: apiKey.trim() }),
    });
  },

  removeProviderAuth(providerID: string): Promise<void> {
    return requestJSON(`/auth/${encodeURIComponent(providerID)}`, {
      method: "DELETE",
    });
  },

  getGlobalConfig(): Promise<Record<string, any>> {
    return requestJSON("/global/config");
  },

  updateGlobalConfig(config: Record<string, any>): Promise<Record<string, any>> {
    return requestJSON("/global/config", {
      method: "PATCH",
      body: JSON.stringify(config),
    });
  },

  disposeGlobal(): Promise<void> {
    return requestJSON("/global/dispose", {
      method: "POST",
      body: JSON.stringify({}),
    });
  },

  // Agents / Modes (Kilo Mode Switcher)
  listAgents(directory?: string): Promise<AgentInfo[]> {
    return requestJSON(withDir("/agent", directory));
  },

  // Commands, LSP Diagnostics & File Search
  listCommands(directory?: string): Promise<SlashCommandInfo[]> {
    return requestJSON(withDir("/command", directory));
  },

  getLSPStatus(directory?: string): Promise<any[]> {
    return requestJSON(withDir("/lsp", directory));
  },

  listFiles(
    path = "",
    directory?: string
  ): Promise<Array<{ path: string; name?: string; type?: string }>> {
    return requestJSON(withDir(`/file?path=${encodeURIComponent(path)}`, directory));
  },

  findFiles(query: string, directory?: string): Promise<string[]> {
    return requestJSON(withDir(`/find/file?query=${encodeURIComponent(query)}&limit=15`, directory));
  },

  readFileContent(filePath: string, directory?: string): Promise<{ content: string }> {
    return requestJSON(withDir(`/file/content?path=${encodeURIComponent(filePath)}`, directory));
  },

  // Built-in PTY Terminal
  createPTY(directory?: string): Promise<{ id: string; title: string; cwd: string }> {
    return requestJSON(withDir("/pty", directory), {
      method: "POST",
      body: JSON.stringify({ cwd: directory, title: "Hoxi Terminal" }),
    });
  },

  resizePTY(ptyID: string, cols: number, rows: number, directory?: string): Promise<void> {
    return requestJSON(withDir(`/pty/${ptyID}`, directory), {
      method: "PUT",
      body: JSON.stringify({ size: { cols, rows } }),
    });
  },

  // Git Worktree Sandbox Manager
  createWorktree(sessionID: string): Promise<WorktreeMeta> {
    return requestJSON("/api/hoxi/worktree/create", {
      method: "POST",
      body: JSON.stringify({ sessionID }),
    });
  },

  getWorktreeStatus(sessionID: string): Promise<WorktreeStatus> {
    return requestJSON(`/api/hoxi/worktree/status?sessionID=${encodeURIComponent(sessionID)}`);
  },

  applyWorktree(sessionID: string, mode: "patch" | "merge" = "patch"): Promise<{ ok: boolean }> {
    return requestJSON("/api/hoxi/worktree/apply", {
      method: "POST",
      body: JSON.stringify({ sessionID, mode }),
    });
  },

  removeWorktree(sessionID: string, force = false): Promise<{ ok: boolean }> {
    return requestJSON(
      `/api/hoxi/worktree/${encodeURIComponent(sessionID)}?force=${force ? "true" : "false"}`,
      { method: "DELETE" }
    );
  },

  // OpenCode Project / Workspace Manager (Kilo Desktop)
  listProjects(): Promise<ProjectInfo[]> {
    return requestJSON("/project");
  },

  openDirectoryDialog(): Promise<{ path: string; error?: string }> {
    return requestJSON("/api/hoxi/dialog/open-directory", { method: "POST" });
  },
};
