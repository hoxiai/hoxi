// web/src/services/api.ts
import type {
  MessageInfo,
  MessagePart,
  PermissionRequest,
  QuestionRequest,
  SessionInfo,
  SessionRuntime,
  SessionStatus,
  SnapshotFileDiff,
} from "./sse-types";

export type { MessageInfo, MessagePart };
import type {
  AgentInfo,
  BootstrapInfo,
  BranchListResponse,
  CodeAuditRequest,
  CodeAuditResult,
  FileContentResponse,
  FileValidateResponse,
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
import { getGatewayToken, requestJSON, setGatewayToken, withDir } from "./http";
import { authAPI, leaderboardAPI, updaterAPI } from "./platform-api";
import {
  forgetRunnerSession,
  isRunnerPermissionID,
  isRunnerSessionID,
  registerRunnerSessions,
  resolveRunnerRuntimeForProvider,
  runnerAPI,
} from "./runner-api";

export * from "./api-types";
export * from "./skills-types";
export { skillsAPI } from "./skills";
export {
  authAPI,
  getGatewayToken,
  isRunnerPermissionID,
  isRunnerSessionID,
  leaderboardAPI,
  requestJSON,
  resolveRunnerRuntimeForProvider,
  runnerAPI,
  setGatewayToken,
  updaterAPI,
};

const SESSIONS = "/api/hoxi/sessions";

function sessionPath(sessionID: string): string {
  return `${SESSIONS}/${encodeURIComponent(sessionID)}`;
}

// Hands the conversation to another engine before dispatch. Unadopted OpenCode
// sessions staying on OpenCode need no handoff.
async function prepareRuntime(
  sessionID: string,
  runtime: SessionRuntime,
  model: { providerID: string; modelID: string } | undefined,
  directory?: string
): Promise<void> {
  if (runtime === "opencode" && !isRunnerSessionID(sessionID)) return;
  const info = await requestJSON<SessionInfo>(withDir(`${sessionPath(sessionID)}/runtime`, directory), {
    method: "POST",
    body: JSON.stringify({ runtime, ...model }),
  });
  registerRunnerSessions([info]);
}

export const hoxiAPI = {
  async getBootstrap(): Promise<BootstrapInfo> {
    const info = await requestJSON<BootstrapInfo>("/api/hoxi/bootstrap");
    if (info?.gatewayToken) {
      setGatewayToken(info.gatewayToken);
    }
    return info;
  },

  getSystemInfo(): Promise<SystemRuntimeInfo> {
    return requestJSON("/api/hoxi/system-info");
  },

  getKeepAwake(): Promise<import("./api-types").KeepAwakeStatusResponse> {
    return requestJSON("/api/hoxi/keep-awake");
  },

  setKeepAwake(enabled: boolean): Promise<import("./api-types").KeepAwakeStatusResponse> {
    return requestJSON("/api/hoxi/keep-awake", {
      method: "POST",
      body: JSON.stringify({ enabled }),
    });
  },

  async readClipboardImages(opts?: {
    filesOnly?: boolean;
  }): Promise<Array<{ filename: string; mime: string; dataUrl: string }>> {
    const qs = opts?.filesOnly ? "?filesOnly=1" : "";
    const res = await requestJSON<{
      images?: Array<{ filename: string; mime: string; dataUrl: string }>;
    }>(`/api/hoxi/clipboard/images${qs}`);
    return Array.isArray(res?.images) ? res.images : [];
  },

  uploadAttachment(req: {
    sessionID?: string;
    directory?: string;
    filename: string;
    mime: string;
    dataUrl: string;
  }): Promise<{
    id: string;
    filename: string;
    mime: string;
    size: number;
    kind: "image" | "pdf" | "spreadsheet" | "document" | "text";
    url: string;
    textPreview?: string;
  }> {
    return requestJSON(withDir("/api/hoxi/attachments", req.directory), {
      method: "POST",
      body: JSON.stringify(req),
    });
  },

  getGitDailyPulse(
    directory?: string,
    sessionID?: string
  ): Promise<import("./api-types").GitDailyPulseResponse> {
    const params = new URLSearchParams();
    if (directory) params.set("directory", directory);
    if (sessionID) params.set("sessionID", sessionID);
    const qs = params.toString();
    return requestJSON(`/api/hoxi/dev-pulse${qs ? `?${qs}` : ""}`);
  },

  // Sessions: one Hoxi endpoint. The gateway routes each session to its owner
  // (Hoxi conversation store or a not-yet-adopted OpenCode session).
  async listSessions(directory?: string): Promise<SessionInfo[]> {
    const list = await requestJSON<SessionInfo[]>(withDir(SESSIONS, directory));
    if (!Array.isArray(list)) return [];
    registerRunnerSessions(list);
    return list;
  },

  async getSessionStatuses(directory?: string): Promise<Record<string, SessionStatus>> {
    return (await requestJSON<Record<string, SessionStatus>>(withDir(`${SESSIONS}/status`, directory))) || {};
  },

  async getSession(sessionID: string, directory?: string): Promise<SessionInfo> {
    const info = await requestJSON<SessionInfo>(withDir(sessionPath(sessionID), directory));
    if (info?.id) registerRunnerSessions([info]);
    return info;
  },

  async createSession(
    title?: string,
    directory?: string,
    options?: {
      runtime?: SessionRuntime;
      modelID?: string;
      providerID?: string;
      mode?: string;
      sandboxMode?: "local" | "worktree";
    }
  ): Promise<SessionInfo> {
    const info = await requestJSON<SessionInfo>(withDir(SESSIONS, directory), {
      method: "POST",
      body: JSON.stringify({ ...options, ...(title ? { title } : {}), ...(directory ? { directory } : {}) }),
    });
    if (info?.id) registerRunnerSessions([info]);
    return info;
  },

  renameSession(sessionID: string, title: string, directory?: string): Promise<SessionInfo> {
    return requestJSON(withDir(sessionPath(sessionID), directory), {
      method: "PATCH",
      body: JSON.stringify({ title }),
    });
  },

  setSandboxMode(
    sessionID: string,
    sandboxMode: "local" | "worktree",
    options?: { force?: boolean; keepBranch?: boolean },
    directory?: string
  ): Promise<SessionInfo> {
    return requestJSON(withDir(sessionPath(sessionID), directory), {
      method: "PATCH",
      body: JSON.stringify({ sandboxMode, ...options }),
    });
  },

  archiveSession(
    sessionID: string,
    archived: boolean,
    directory?: string
  ): Promise<SessionInfo> {
    return requestJSON(withDir(sessionPath(sessionID), directory), {
      method: "PATCH",
      body: JSON.stringify({
        time: { archived: archived ? Date.now() : 0 },
      }),
    });
  },

  async deleteSession(sessionID: string, directory?: string): Promise<void> {
    await requestJSON(withDir(sessionPath(sessionID), directory), {
      method: "DELETE",
    });
    forgetRunnerSession(sessionID);
  },

  getMessages(
    sessionID: string,
    directory?: string,
    limit = 100
  ): Promise<Array<{ info: MessageInfo; parts: MessagePart[] }>> {
    const safeLimit = Number.isFinite(limit) && limit > 0 ? Math.min(2000, Math.floor(limit)) : 100;
    return requestJSON(withDir(`${sessionPath(sessionID)}/message?limit=${safeLimit}`, directory));
  },

  // Prompting & Live Mid-Stream Steering
  async sendPromptAsync(
    sessionID: string,
    text: string,
    options?: {
      agent?: "build" | "plan" | string;
      model?: { providerID: string; modelID: string };
      variant?: string;
      directory?: string;
      system?: string;
      attachments?: PromptAttachment[];
      runtime?: SessionRuntime;
      skillSelection?: import("./skills-types").SkillSelection;
    }
  ): Promise<void> {
    if (options?.runtime) await prepareRuntime(sessionID, options.runtime, options.model, options.directory);
    const parts: Array<{ type: "text"; text: string } | PromptAttachment> = [];
    if (options?.attachments && options.attachments.length > 0) {
      parts.push(...options.attachments);
    }
    if (text || parts.length === 0) {
      parts.push({ type: "text", text });
    }
    return requestJSON(withDir(`${sessionPath(sessionID)}/prompt_async`, options?.directory), {
      method: "POST",
      body: JSON.stringify({
        agent: options?.agent || "build",
        ...(options?.model ? { model: options.model } : {}),
        ...(options?.variant ? { variant: options.variant } : {}),
        ...(options?.system ? { system: options.system } : {}),
        ...(options?.skillSelection ? { skillSelection: options.skillSelection } : {}),
        parts,
      }),
    });
  },

  abortSession(sessionID: string, directory?: string): Promise<void> {
    return requestJSON(withDir(`${sessionPath(sessionID)}/abort`, directory), {
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
    return requestJSON(withDir(`${sessionPath(sessionID)}/summarize`, directory), {
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
    return requestJSON(withDir(`${sessionPath(sessionID)}/revert`, directory), {
      method: "POST",
      body: JSON.stringify({ messageID, partID }),
    });
  },

  unrevertSession(sessionID: string, directory?: string): Promise<SessionInfo> {
    return requestJSON(withDir(`${sessionPath(sessionID)}/unrevert`, directory), {
      method: "POST",
      body: JSON.stringify({}),
    });
  },

  forkSession(sessionID: string, messageID?: string, directory?: string): Promise<SessionInfo> {
    return requestJSON(withDir(`${sessionPath(sessionID)}/fork`, directory), {
      method: "POST",
      body: JSON.stringify({ messageID }),
    });
  },

  // Diffs & VCS
  getSessionDiff(sessionID: string, directory?: string): Promise<SnapshotFileDiff[]> {
    return requestJSON(withDir(`${sessionPath(sessionID)}/diff`, directory));
  },

  // Interactive Permission Approval Gate
  listPermissions(directory?: string): Promise<PermissionRequest[]> {
    return requestJSON(withDir("/api/hoxi/permission", directory));
  },
  replyPermission(requestID: string, reply: "once" | "always" | "reject", directory?: string, message?: string): Promise<void> {
    return requestJSON(withDir(`/api/hoxi/permission/${encodeURIComponent(requestID)}/reply`, directory), {
      method: "POST",
      body: JSON.stringify({ reply, ...(message ? { message } : {}) }),
    });
  },
  // Interactive Question Dock (Kilo QuestionDock)
  listQuestions(directory?: string): Promise<QuestionRequest[]> {
    return requestJSON(withDir("/question", directory));
  },
  replyQuestion(requestID: string, answers: string[][], directory?: string): Promise<void> {
    return requestJSON(withDir(`/question/${requestID}/reply`, directory), { method: "POST", body: JSON.stringify({ answers }) });
  },
  rejectQuestion(requestID: string, directory?: string): Promise<void> {
    return requestJSON(withDir(`/question/${requestID}/reject`, directory), { method: "POST", body: JSON.stringify({}) });
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
    return requestJSON(withDir(`/provider/${encodeURIComponent(providerID)}/oauth/authorize`, directory), {
      method: "POST",
      body: JSON.stringify({ method, ...(inputs ? { inputs } : {}) }),
    });
  },
  completeProviderOAuth(providerID: string, method: number, code?: string, directory?: string): Promise<boolean> {
    return requestJSON(withDir(`/provider/${encodeURIComponent(providerID)}/oauth/callback`, directory), {
      method: "POST",
      body: JSON.stringify({ method, ...(code ? { code } : {}) }),
    });
  },
  setProviderAuth(providerID: string, apiKey: string, metadata?: Record<string, string>): Promise<void> {
    return requestJSON(`/auth/${encodeURIComponent(providerID)}`, {
      method: "PUT",
      body: JSON.stringify({ type: "api", key: apiKey.trim(), ...(metadata && Object.keys(metadata).length > 0 ? { metadata } : {}) }),
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
    return requestJSON("/api/hoxi/provider/fetch-models", { method: "POST", body: JSON.stringify(opts) });
  },
  getGlobalConfig(): Promise<Record<string, any>> {
    return requestJSON("/global/config");
  },
  updateGlobalConfig(config: Record<string, any>): Promise<Record<string, any>> {
    return requestJSON("/global/config", { method: "PATCH", body: JSON.stringify(config) });
  },
  getHoxiModelsConfig(): Promise<{ hoxiProviders: Record<string, any>; openCodeProviders: string[]; configPath: string }> {
    return requestJSON("/api/hoxi/models-config");
  },
  saveHoxiProviderConfig(
    providerID: string,
    config: Record<string, any>
  ): Promise<{ ok: boolean; hoxiProviders: Record<string, any>; configPath: string }> {
    return requestJSON("/api/hoxi/models-config", { method: "POST", body: JSON.stringify({ providerID, config }) });
  },
  deleteHoxiProviderConfig(providerID: string): Promise<{ ok: boolean; hoxiProviders: Record<string, any> }> {
    return requestJSON(`/api/hoxi/models-config?providerID=${encodeURIComponent(providerID)}`, { method: "DELETE" });
  },
  getAutoPoolsConfig(): Promise<{ ok: boolean; autoPools: Record<string, any>; keyCounts?: Record<string, number> }> {
    return requestJSON("/api/hoxi/provider/auto-pools");
  },
  saveAutoPoolConfig(poolID: string, pool: Record<string, any>): Promise<{ ok: boolean; autoPools: Record<string, any> }> {
    return requestJSON("/api/hoxi/provider/auto-pools", { method: "POST", body: JSON.stringify({ poolID, pool }) });
  },
  deleteAutoPoolConfig(poolID: string): Promise<{ ok: boolean; autoPools: Record<string, any> }> {
    return requestJSON(`/api/hoxi/provider/auto-pools?poolID=${encodeURIComponent(poolID)}`, { method: "DELETE" });
  },
  disposeGlobal(): Promise<void> {
    return requestJSON("/global/dispose", { method: "POST", body: JSON.stringify({}) });
  },

  // Agents / Modes (Kilo Mode Switcher)
  listAgents(directory?: string): Promise<AgentInfo[]> { return requestJSON(withDir("/agent", directory)); },

  // Commands, LSP Diagnostics & File Search
  listCommands(directory?: string): Promise<SlashCommandInfo[]> { return requestJSON(withDir("/command", directory)); },
  getLSPStatus(directory?: string): Promise<any[]> { return requestJSON(withDir("/lsp", directory)); },
  searchFiles(query: string, directory?: string, sessionID?: string, limit = 60): Promise<Array<{ path: string; name: string; type: string }>> {
    const params = new URLSearchParams({ q: query });
    if (directory) params.set("directory", directory);
    if (sessionID) params.set("sessionID", sessionID);
    if (limit > 0) params.set("limit", String(limit));
    return requestJSON(`/api/hoxi/file/search?${params.toString()}`);
  },
  listFiles(path = "", directory?: string, sessionID?: string): Promise<Array<{ path: string; name?: string; type?: string }>> {
    const q = `path=${encodeURIComponent(path)}${sessionID ? `&sessionID=${encodeURIComponent(sessionID)}` : ""}`;
    return requestJSON(withDir(`/api/hoxi/file/list?${q}`, directory));
  },
  findFiles(query: string, directory?: string): Promise<string[]> {
    return requestJSON(withDir(`/find/file?query=${encodeURIComponent(query)}&limit=15`, directory));
  },
  readFileContent(filePath: string, directory?: string, sessionID?: string): Promise<FileContentResponse> {
    const q = `path=${encodeURIComponent(filePath)}${sessionID ? `&sessionID=${encodeURIComponent(sessionID)}` : ""}`;
    return requestJSON(withDir(`/api/hoxi/file/content?${q}`, directory));
  },
  validateFiles(paths: string[], directory?: string, sessionID?: string): Promise<FileValidateResponse> {
    const q = sessionID ? `?sessionID=${encodeURIComponent(sessionID)}` : "";
    return requestJSON(withDir(`/api/hoxi/file/validate${q}`, directory), {
      method: "POST",
      body: JSON.stringify({ paths, ...(directory ? { directory } : {}), ...(sessionID ? { sessionID } : {}) }),
    });
  },
  openExternalFile(
    filePath: string,
    opts?: { directory?: string; sessionID?: string; action?: "open" | "reveal" }
  ): Promise<{ ok: boolean; path: string; action: string }> {
    return requestJSON(withDir("/api/hoxi/file/open", opts?.directory), {
      method: "POST",
      body: JSON.stringify({
        path: filePath,
        ...(opts?.directory ? { directory: opts.directory } : {}),
        ...(opts?.sessionID ? { sessionID: opts.sessionID } : {}),
        ...(opts?.action ? { action: opts.action } : {}),
      }),
    });
  },

  // Built-in PTY Terminal
  createPTY(directory?: string): Promise<{ id: string; title: string; cwd: string }> {
    return requestJSON(withDir("/pty", directory), { method: "POST", body: JSON.stringify({ cwd: directory, title: "Hoxi Terminal" }) });
  },
  resizePTY(ptyID: string, cols: number, rows: number, directory?: string): Promise<void> {
    return requestJSON(withDir(`/pty/${ptyID}`, directory), { method: "PUT", body: JSON.stringify({ size: { cols, rows } }) });
  },
  deletePTY(ptyID: string, directory?: string): Promise<void> {
    return requestJSON(withDir(`/pty/${encodeURIComponent(ptyID)}`, directory), { method: "DELETE" });
  },

  // Git Worktree Sandbox Manager
  createWorktree(sessionID: string, directory?: string): Promise<WorktreeMeta> {
    return requestJSON("/api/hoxi/worktree/create", { method: "POST", body: JSON.stringify({ sessionID, ...(directory ? { directory } : {}) }) });
  },
  getWorktreeStatus(sessionID: string, directory?: string, extraDirs?: string[]): Promise<WorktreeStatus> {
    const extra = extraDirs?.length ? extraDirs.map((d) => `&extraDir=${encodeURIComponent(d)}`).join("") : "";
    const q = `sessionID=${encodeURIComponent(sessionID)}${directory ? `&directory=${encodeURIComponent(directory)}` : ""}${extra}`;
    return requestJSON(`/api/hoxi/worktree/status?${q}`);
  },
  getWorktreeDiff(sessionID: string, directory?: string, extraDirs?: string[]): Promise<SnapshotFileDiff[]> {
    const extra = extraDirs?.length ? extraDirs.map((d) => `&extraDir=${encodeURIComponent(d)}`).join("") : "";
    const q = `sessionID=${encodeURIComponent(sessionID)}${directory ? `&directory=${encodeURIComponent(directory)}` : ""}${extra}`;
    return requestJSON(`/api/hoxi/worktree/diff?${q}`);
  },
  getWorktreeFileDiff(sessionID: string, file: string, directory?: string): Promise<SnapshotFileDiff> {
    const q = `sessionID=${encodeURIComponent(sessionID)}&file=${encodeURIComponent(file)}${directory ? `&directory=${encodeURIComponent(directory)}` : ""}`;
    return requestJSON(`/api/hoxi/worktree/diff?${q}`);
  },
  applyWorktree(sessionID: string, mode: "patch" | "merge" = "patch"): Promise<{ ok: boolean }> {
    return requestJSON("/api/hoxi/worktree/apply", { method: "POST", body: JSON.stringify({ sessionID, mode }) });
  },
  syncWorktree(sessionID: string, directory?: string): Promise<WorktreeStatus> {
    return requestJSON("/api/hoxi/worktree/sync", { method: "POST", body: JSON.stringify({ sessionID, ...(directory ? { directory } : {}) }) });
  },
  revertWorktreeFile(sessionID: string, file: string, directory?: string): Promise<{ ok: boolean }> {
    return requestJSON("/api/hoxi/worktree/revert-file", { method: "POST", body: JSON.stringify({ sessionID, file, ...(directory ? { directory } : {}) }) });
  },
  commitWorktree(sessionID: string, message: string, opts?: { push?: boolean; directory?: string; sessionDiffs?: SnapshotFileDiff[] }): Promise<import("./api-types").CommitAndPushResponse> {
    return requestJSON("/api/hoxi/worktree/commit", {
      method: "POST",
      body: JSON.stringify({ sessionID, message, ...(opts?.push ? { push: true } : {}), ...(opts?.directory ? { directory: opts.directory } : {}), ...(opts?.sessionDiffs !== undefined ? { sessionDiffs: opts.sessionDiffs } : {}) }),
    });
  },
  pushWorktree(sessionID: string, directory?: string): Promise<import("./api-types").CommitAndPushResponse> {
    return requestJSON("/api/hoxi/worktree/push", { method: "POST", body: JSON.stringify({ sessionID, ...(directory ? { directory } : {}) }) });
  },
  removeWorktree(sessionID: string, force = false, keepBranch = false): Promise<{ ok: boolean }> {
    return requestJSON(
      `/api/hoxi/worktree/${encodeURIComponent(sessionID)}?force=${force ? "true" : "false"}&keepBranch=${keepBranch ? "true" : "false"}`,
      { method: "DELETE" }
    );
  },
  listBranches(directory?: string): Promise<BranchListResponse> {
    return requestJSON(`/api/hoxi/worktree/branches${directory ? `?directory=${encodeURIComponent(directory)}` : ""}`);
  },
  checkoutBranch(branch: string, create = false, directory?: string): Promise<WorktreeStatus> {
    return requestJSON("/api/hoxi/worktree/checkout", { method: "POST", body: JSON.stringify({ branch, create, ...(directory ? { directory } : {}) }) });
  },
  renameBranch(sessionID: string, newBranch: string, directory?: string): Promise<{ ok: boolean; branch: string }> {
    return requestJSON("/api/hoxi/worktree/rename-branch", { method: "POST", body: JSON.stringify({ sessionID, newBranch, ...(directory ? { directory } : {}) }) });
  },
  pruneSandboxes(excludeSessionID?: string, directory?: string): Promise<{ ok: boolean; removedCount: number }> {
    return requestJSON("/api/hoxi/worktree/prune-sandboxes", { method: "POST", body: JSON.stringify({ ...(excludeSessionID ? { excludeSessionID } : {}), ...(directory ? { directory } : {}) }) });
  },
  retryWorktreeSetup(sessionID: string, directory?: string): Promise<import("./api-types").WorktreeVerifyState> {
    return requestJSON("/api/hoxi/worktree/setup-retry", {
      method: "POST",
      body: JSON.stringify({ sessionID, ...(directory ? { directory } : {}) }),
    });
  },
  getWorktreeVerifyStatus(sessionID: string, directory?: string): Promise<import("./api-types").WorktreeVerifyState> {
    const q = `sessionID=${encodeURIComponent(sessionID)}${directory ? `&directory=${encodeURIComponent(directory)}` : ""}`;
    return requestJSON(`/api/hoxi/worktree/verify-status?${q}`);
  },
  saveWorktreeVerifyConfig(input: { directory?: string; runCommand: string; testCommand: string }): Promise<import("./api-types").WorktreeVerifyConfig> {
    return requestJSON("/api/hoxi/worktree/verify-config", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },
  runWorktreeVerify(input: { sessionID: string; directory?: string; kind: "run" | "test"; command?: string }): Promise<import("./api-types").WorktreeVerifyState> {
    return requestJSON("/api/hoxi/worktree/verify-run", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },
  stopWorktreeVerify(sessionID: string, directory?: string): Promise<import("./api-types").WorktreeVerifyState> {
    return requestJSON("/api/hoxi/worktree/verify-stop", {
      method: "POST",
      body: JSON.stringify({ sessionID, ...(directory ? { directory } : {}) }),
    });
  },

  // OpenCode Project / Workspace Manager (Kilo Desktop)
  listProjects(): Promise<ProjectInfo[]> { return requestJSON("/project"); },
  listWorkspaces(): Promise<{ workspaces: WorkspaceProfile[]; dismissed?: string[] }> { return requestJSON("/api/hoxi/workspaces"); },
  createWorkspace(input: SaveWorkspaceInput): Promise<WorkspaceProfile> { return requestJSON("/api/hoxi/workspaces", { method: "POST", body: JSON.stringify(input) }); },
  updateWorkspace(id: string, input: SaveWorkspaceInput): Promise<WorkspaceProfile> { return requestJSON(`/api/hoxi/workspaces/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(input) }); },
  deleteWorkspace(id: string): Promise<{ ok: boolean; deleted?: boolean; dismissed?: string[] }> { return requestJSON(`/api/hoxi/workspaces/${encodeURIComponent(id)}`, { method: "DELETE" }); },
  openDirectoryDialog(): Promise<{ path: string; error?: string }> { return requestJSON("/api/hoxi/dialog/open-directory", { method: "POST" }); },
  testProviderModel(opts: { providerID?: string; baseURL?: string; apiKey?: string; npm?: string; modelID: string; headers?: Record<string, string> }): Promise<{ ok: boolean; latencyMs?: number; error?: string }> {
    return requestJSON("/api/hoxi/provider/test-model", { method: "POST", body: JSON.stringify(opts) });
  },
  getProviderTelemetry(): Promise<{ providers: Record<string, ProviderTelemetryStats> }> { return requestJSON("/api/hoxi/provider/telemetry"); },
  saveProviderTelemetry(providers: Record<string, ProviderTelemetryStats>): Promise<{ ok: boolean }> { return requestJSON("/api/hoxi/provider/telemetry", { method: "POST", body: JSON.stringify({ providers }) }); },
  clearProviderTelemetry(providerID?: string): Promise<{ ok: boolean }> {
    return requestJSON(`/api/hoxi/provider/telemetry${providerID ? `?providerID=${encodeURIComponent(providerID)}` : ""}`, { method: "DELETE" });
  },
  auditProviderCode(req: CodeAuditRequest): Promise<CodeAuditResult> { return requestJSON("/api/hoxi/provider/audit", { method: "POST", body: JSON.stringify(req) }); },
  summarizeSessionTitle(req: { providerID: string; modelID: string; baseURL?: string; npm?: string; prompt: string; locale: string }): Promise<{ ok: boolean; title: string; error?: string }> {
    return requestJSON("/api/hoxi/provider/summarize-title", { method: "POST", body: JSON.stringify(req) });
  },
  generateCommitMessage(req: { providerID: string; modelID: string; baseURL?: string; npm?: string; sessionDigest?: string; sessionTitle?: string; lastPrompt?: string; diffSummary: string; locale: string }): Promise<{ ok: boolean; message: string; error?: string }> {
    return requestJSON("/api/hoxi/provider/generate-commit-message", { method: "POST", body: JSON.stringify(req) });
  },
  enhancePrompt(req: { providerID: string; modelID: string; baseURL?: string; npm?: string; prompt: string; locale: string }): Promise<{ ok: boolean; prompt: string; error?: string }> {
    return requestJSON("/api/hoxi/provider/enhance-prompt", { method: "POST", body: JSON.stringify(req) });
  },

  // Cross-Tool Session Handoff & Local CLI Bridge
  listExternalSessions(opts?: { directory?: string; query?: string; source?: string; onlyCurrent?: boolean }): Promise<import("./api-types").HandoffSessionsResponse> {
    const params = new URLSearchParams();
    if (opts?.directory) params.set("directory", opts.directory);
    if (opts?.query) params.set("query", opts.query);
    if (opts?.source) params.set("source", opts.source);
    if (opts?.onlyCurrent) params.set("onlyCurrent", "1");
    return requestJSON(`/api/hoxi/handoff/sessions?${params.toString()}`);
  },
  getExternalSessionPreview(source: string, id: string, cwd?: string): Promise<import("./api-types").ExternalSessionDetail> {
    const params = new URLSearchParams({ source, id, ...(cwd ? { cwd } : {}) });
    return requestJSON(`/api/hoxi/handoff/preview?${params.toString()}`);
  },
  importExternalSession(req: { source: string; sessionID: string; sourceCwd?: string; targetDirectory?: string; preferredProviderID?: string; preferredModelID?: string }): Promise<import("./api-types").HandoffImportResponse> {
    return requestJSON("/api/hoxi/handoff/import", { method: "POST", body: JSON.stringify(req) });
  },
  getLocalCLIStatus(): Promise<import("./api-types").LocalCLIStatusResponse> { return requestJSON("/api/hoxi/local-cli/status"); },
  importLocalCLI(toolID: string): Promise<import("./api-types").LocalCLIImportResponse> { return requestJSON("/api/hoxi/local-cli/import", { method: "POST", body: JSON.stringify({ toolID }) }); },
};
