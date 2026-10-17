// web/src/services/api.ts
import type {
  MessageInfo,
  MessagePart,
  QuestionRequest,
  SessionInfo,
  SnapshotFileDiff,
} from "./sse-types";
import type {
  AgentInfo,
  BootstrapInfo,
  CodeAuditRequest,
  CodeAuditResult,
  ProjectInfo,
  PromptAttachment,
  ProviderAuthAuthorization,
  ProviderAuthMethod,
  ProviderListResponse,
  ProviderTelemetryStats,
  SaveWorkspaceInput,
  SlashCommandInfo,
  SystemRuntimeInfo,
  WorkspaceProfile,
  WorktreeMeta,
  WorktreeStatus,
} from "./api-types";

export * from "./api-types";

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

  getSystemInfo(): Promise<SystemRuntimeInfo> {
    return requestJSON("/api/hoxi/system-info");
  },

  // Sessions
  listSessions(directory?: string): Promise<SessionInfo[]> {
    return requestJSON(withDir("/session?limit=50", directory));
  },

  createSession(title?: string, directory?: string): Promise<SessionInfo> {
    return requestJSON(withDir("/session", directory), {
      method: "POST",
      body: JSON.stringify(title ? { title } : {}),
    });
  },

  renameSession(sessionID: string, title: string, directory?: string): Promise<SessionInfo> {
    return requestJSON(withDir(`/session/${sessionID}`, directory), {
      method: "PATCH",
      body: JSON.stringify({ title }),
    });
  },

  archiveSession(
    sessionID: string,
    archived: boolean,
    directory?: string
  ): Promise<SessionInfo> {
    return requestJSON(withDir(`/session/${sessionID}`, directory), {
      method: "PATCH",
      body: JSON.stringify({
        time: { archived: archived ? Date.now() : 0 },
      }),
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
      system?: string;
      attachments?: PromptAttachment[];
    }
  ): Promise<void> {
    const parts: Array<{ type: "text"; text: string } | PromptAttachment> = [];
    if (options?.attachments && options.attachments.length > 0) {
      parts.push(...options.attachments);
    }
    if (text || parts.length === 0) {
      parts.push({ type: "text", text });
    }
    return requestJSON(withDir(`/session/${sessionID}/prompt_async`, options?.directory), {
      method: "POST",
      body: JSON.stringify({
        agent: options?.agent || "build",
        ...(options?.model ? { model: options.model } : {}),
        ...(options?.variant ? { variant: options.variant } : {}),
        ...(options?.system ? { system: options.system } : {}),
        parts,
      }),
    });
  },

  abortSession(sessionID: string, directory?: string): Promise<void> {
    return requestJSON(withDir(`/session/${sessionID}/abort`, directory), {
      method: "POST",
      body: JSON.stringify({}),
    });
  },

  summarizeSession(
    sessionID: string,
    providerID: string,
    modelID: string,
    directory?: string
  ): Promise<boolean> {
    return requestJSON(withDir(`/session/${sessionID}/summarize`, directory), {
      method: "POST",
      body: JSON.stringify({ providerID, modelID }),
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

  // Interactive Question Dock (Kilo QuestionDock)
  listQuestions(directory?: string): Promise<QuestionRequest[]> {
    return requestJSON(withDir("/question", directory));
  },

  replyQuestion(requestID: string, answers: string[][], directory?: string): Promise<void> {
    return requestJSON(withDir(`/question/${requestID}/reply`, directory), {
      method: "POST",
      body: JSON.stringify({ answers }),
    });
  },

  rejectQuestion(requestID: string, directory?: string): Promise<void> {
    return requestJSON(withDir(`/question/${requestID}/reject`, directory), {
      method: "POST",
      body: JSON.stringify({}),
    });
  },

  // Providers, Models & Auth (Direct OpenCode endpoints used by Kilo)
  listProviders(directory?: string): Promise<ProviderListResponse> {
    return requestJSON(withDir("/provider", directory));
  },

  getProviderAuthMethods(directory?: string): Promise<Record<string, ProviderAuthMethod[]>> {
    return requestJSON(withDir("/provider/auth", directory));
  },

  authorizeProviderOAuth(
    providerID: string,
    method: number,
    inputs?: Record<string, string>,
    directory?: string
  ): Promise<ProviderAuthAuthorization> {
    return requestJSON(
      withDir(`/provider/${encodeURIComponent(providerID)}/oauth/authorize`, directory),
      {
        method: "POST",
        body: JSON.stringify({ method, ...(inputs ? { inputs } : {}) }),
      }
    );
  },

  completeProviderOAuth(
    providerID: string,
    method: number,
    code?: string,
    directory?: string
  ): Promise<boolean> {
    return requestJSON(
      withDir(`/provider/${encodeURIComponent(providerID)}/oauth/callback`, directory),
      {
        method: "POST",
        body: JSON.stringify({ method, ...(code ? { code } : {}) }),
      }
    );
  },

  setProviderAuth(
    providerID: string,
    apiKey: string,
    metadata?: Record<string, string>
  ): Promise<void> {
    return requestJSON(`/auth/${encodeURIComponent(providerID)}`, {
      method: "PUT",
      body: JSON.stringify({
        type: "api",
        key: apiKey.trim(),
        ...(metadata && Object.keys(metadata).length > 0 ? { metadata } : {}),
      }),
    });
  },

  removeProviderAuth(providerID: string): Promise<void> {
    return requestJSON(`/auth/${encodeURIComponent(providerID)}`, { method: "DELETE" });
  },

  fetchCustomProviderModels(opts: {
    baseURL: string;
    apiKey?: string;
    providerID?: string;
    headers?: Record<string, string>;
    directory?: string;
  }): Promise<{ ok?: boolean; models?: Array<{ id: string; name: string }>; error?: string; auth?: boolean }> {
    return requestJSON("/api/hoxi/provider/fetch-models", {
      method: "POST",
      body: JSON.stringify(opts),
    });
  },

  getGlobalConfig(): Promise<Record<string, any>> {
    return requestJSON("/global/config");
  },

  updateGlobalConfig(config: Record<string, any>): Promise<Record<string, any>> {
    return requestJSON("/global/config", { method: "PATCH", body: JSON.stringify(config) });
  },

  getHoxiModelsConfig(): Promise<{
    hoxiProviders: Record<string, any>;
    openCodeProviders: string[];
    configPath: string;
  }> {
    return requestJSON("/api/hoxi/models-config");
  },

  saveHoxiProviderConfig(
    providerID: string,
    config: Record<string, any>
  ): Promise<{ ok: boolean; hoxiProviders: Record<string, any>; configPath: string }> {
    return requestJSON("/api/hoxi/models-config", {
      method: "POST",
      body: JSON.stringify({ providerID, config }),
    });
  },

  deleteHoxiProviderConfig(
    providerID: string
  ): Promise<{ ok: boolean; hoxiProviders: Record<string, any> }> {
    return requestJSON(`/api/hoxi/models-config?providerID=${encodeURIComponent(providerID)}`, {
      method: "DELETE",
    });
  },

  disposeGlobal(): Promise<void> {
    return requestJSON("/global/dispose", { method: "POST", body: JSON.stringify({}) });
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

  listFiles(path = "", directory?: string): Promise<Array<{ path: string; name?: string; type?: string }>> {
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
  createWorktree(sessionID: string, directory?: string): Promise<WorktreeMeta> {
    return requestJSON("/api/hoxi/worktree/create", {
      method: "POST",
      body: JSON.stringify({ sessionID, ...(directory ? { directory } : {}) }),
    });
  },

  getWorktreeStatus(sessionID: string, directory?: string): Promise<WorktreeStatus> {
    const q = `sessionID=${encodeURIComponent(sessionID)}${directory ? `&directory=${encodeURIComponent(directory)}` : ""}`;
    return requestJSON(`/api/hoxi/worktree/status?${q}`);
  },

  getWorktreeDiff(sessionID: string, directory?: string): Promise<SnapshotFileDiff[]> {
    const q = `sessionID=${encodeURIComponent(sessionID)}${directory ? `&directory=${encodeURIComponent(directory)}` : ""}`;
    return requestJSON(`/api/hoxi/worktree/diff?${q}`);
  },

  applyWorktree(sessionID: string, mode: "patch" | "merge" = "patch"): Promise<{ ok: boolean }> {
    return requestJSON("/api/hoxi/worktree/apply", {
      method: "POST",
      body: JSON.stringify({ sessionID, mode }),
    });
  },

  revertWorktreeFile(sessionID: string, file: string): Promise<{ ok: boolean }> {
    return requestJSON("/api/hoxi/worktree/revert-file", {
      method: "POST",
      body: JSON.stringify({ sessionID, file }),
    });
  },

  commitWorktree(sessionID: string, message: string): Promise<{ ok: boolean; commit?: string }> {
    return requestJSON("/api/hoxi/worktree/commit", {
      method: "POST",
      body: JSON.stringify({ sessionID, message }),
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

  listWorkspaces(): Promise<{ workspaces: WorkspaceProfile[] }> {
    return requestJSON("/api/hoxi/workspaces");
  },

  createWorkspace(input: SaveWorkspaceInput): Promise<WorkspaceProfile> {
    return requestJSON("/api/hoxi/workspaces", { method: "POST", body: JSON.stringify(input) });
  },

  updateWorkspace(id: string, input: SaveWorkspaceInput): Promise<WorkspaceProfile> {
    return requestJSON(`/api/hoxi/workspaces/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(input),
    });
  },

  deleteWorkspace(id: string): Promise<{ ok: boolean }> {
    return requestJSON(`/api/hoxi/workspaces/${encodeURIComponent(id)}`, { method: "DELETE" });
  },

  openDirectoryDialog(): Promise<{ path: string; error?: string }> {
    return requestJSON("/api/hoxi/dialog/open-directory", { method: "POST" });
  },

  testProviderModel(opts: {
    providerID?: string;
    baseURL?: string;
    apiKey?: string;
    npm?: string;
    modelID: string;
    headers?: Record<string, string>;
  }): Promise<{ ok: boolean; latencyMs?: number; error?: string }> {
    return requestJSON("/api/hoxi/provider/test-model", {
      method: "POST",
      body: JSON.stringify(opts),
    });
  },

  getProviderTelemetry(): Promise<{ providers: Record<string, ProviderTelemetryStats> }> {
    return requestJSON("/api/hoxi/provider/telemetry");
  },

  saveProviderTelemetry(
    providers: Record<string, ProviderTelemetryStats>
  ): Promise<{ ok: boolean }> {
    return requestJSON("/api/hoxi/provider/telemetry", {
      method: "POST",
      body: JSON.stringify({ providers }),
    });
  },

  clearProviderTelemetry(providerID?: string): Promise<{ ok: boolean }> {
    const q = providerID ? `?providerID=${encodeURIComponent(providerID)}` : "";
    return requestJSON(`/api/hoxi/provider/telemetry${q}`, {
      method: "DELETE",
    });
  },

  auditProviderCode(req: CodeAuditRequest): Promise<CodeAuditResult> {
    return requestJSON("/api/hoxi/provider/audit", {
      method: "POST",
      body: JSON.stringify(req),
    });
  },

  summarizeSessionTitle(req: {
    providerID: string;
    modelID: string;
    baseURL?: string;
    npm?: string;
    prompt: string;
    locale: string;
  }): Promise<{ ok: boolean; title: string; error?: string }> {
    return requestJSON("/api/hoxi/provider/summarize-title", {
      method: "POST",
      body: JSON.stringify(req),
    });
  },
};

