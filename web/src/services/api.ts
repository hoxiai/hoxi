// web/src/services/api.ts
import type {
  ApprovalAuditEntry,
  ApprovalGrant,
  ApprovalPolicy,
  ApprovalPolicyState,
  MessageInfo,
  MessagePart,
  PermissionRequest,
  QuestionRequest,
  SessionInfo,
  SessionRuntime,
  SessionStatus,
  SnapshotFileDiff,
} from "./sse-types";

export type {
  ApprovalAuditEntry,
  ApprovalGrant,
  ApprovalPolicy,
  ApprovalPolicyState,
  MessageInfo,
  MessagePart,
  SessionInfo,
};
import type {
  AgentInfo,
  BootstrapInfo,
  DesktopIntent,
  ProjectResolveResponse,
  PromptAttachment,
  SlashCommandInfo,
  SystemRuntimeInfo,
  OldSessionsDeleteResult,
  OldSessionsPreview,
  SystemStorageCleanResult,
  SystemStorageStats,
} from "./api-types";
import { getGatewayToken, requestJSON, setGatewayToken, withDir } from "./http";
import { authAPI, leaderboardAPI, officialChannelAPI, providerMcpOAuthAPI, updaterAPI } from "./platform-api";
import { providerAndAssistAPI } from "./provider-api";
import {
  forgetRunnerSession,
  inferRunnerRuntimeFromSessionID,
  isRunnerAuthMode,
  isRunnerPermissionID,
  isRunnerSessionID,
  registerRunnerSessions,
  resolveRunnerRuntimeForProvider,
  runnerAPI,
} from "./runner-api";
import { fileAndPtyAPI, worktreeAndWorkspaceAPI } from "./worktree-api";

export * from "./api-types";
export * from "./skills-types";
export { skillsAPI } from "./skills";
export {
  authAPI,
  getGatewayToken,
  inferRunnerRuntimeFromSessionID,
  isRunnerAuthMode,
  isRunnerPermissionID,
  isRunnerSessionID,
  leaderboardAPI,
  providerMcpOAuthAPI,
  officialChannelAPI,
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

  getSystemStorage(): Promise<SystemStorageStats> {
    return requestJSON("/api/hoxi/system/storage");
  },

  cleanSystemStorage(): Promise<SystemStorageCleanResult> {
    return requestJSON("/api/hoxi/system/storage/clean", {
      method: "POST",
    });
  },

  previewOldSessions(olderThanDays: number): Promise<OldSessionsPreview> {
    return requestJSON(`/api/hoxi/system/storage/old-sessions?olderThanDays=${encodeURIComponent(String(olderThanDays))}`);
  },

  async deleteOldSessions(olderThanDays: number, sessionIDs: string[]): Promise<OldSessionsDeleteResult> {
    const res = await requestJSON<OldSessionsDeleteResult>("/api/hoxi/system/storage/old-sessions/delete", {
      method: "POST",
      body: JSON.stringify({ olderThanDays, sessionIDs }),
    });
    for (const id of res.deleted) forgetRunnerSession(id);
    return res;
  },

  installEngine(restart = false): Promise<{
    ok: boolean;
    openCodeBinPath: string;
    openCodeVersion: string;
    ready: boolean;
  }> {
    return requestJSON("/api/hoxi/engine/install", {
      method: "POST",
      body: JSON.stringify({ restart }),
    });
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
    sessionID?: string,
    force = false
  ): Promise<import("./api-types").GitDailyPulseResponse> {
    const params = new URLSearchParams();
    if (directory) params.set("directory", directory);
    if (sessionID) params.set("sessionID", sessionID);
    if (force) params.set("refresh", "1");
    const qs = params.toString();
    return requestJSON(`/api/hoxi/dev-pulse${qs ? `?${qs}` : ""}`);
  },

  getRequestLog(limit = 200): Promise<import("./api-types").RequestLogResponse> {
    return requestJSON(`/api/hoxi/request-log?limit=${limit}`);
  },

  getRequestLogContent(id: string, sessionID?: string): Promise<import("./api-types").RequestLogContent> {
    const params = new URLSearchParams({ id });
    if (sessionID) params.set("sessionID", sessionID);
    return requestJSON(`/api/hoxi/request-log/content?${params.toString()}`);
  },

  getDevPulseHistory(
    days = 30,
    directory?: string,
    window?: { startDate?: string; endDate?: string; sessionID?: string }
  ): Promise<import("./api-types").DevPulseHistoryResponse> {
    const params = new URLSearchParams();
    params.set("days", String(days));
    if (window?.startDate) params.set("start", window.startDate);
    if (window?.endDate) params.set("end", window.endDate);
    if (directory) params.set("directory", directory);
    if (window?.sessionID) params.set("sessionID", window.sessionID);
    return requestJSON(`/api/hoxi/dev-pulse/history?${params.toString()}`);
  },

  // Sessions: one Hoxi endpoint. The gateway routes each session to its owner
  // (Hoxi conversation store or a not-yet-adopted OpenCode session).
  async listSessions(directory?: string, projectID?: string, surface?: "code" | "work"): Promise<SessionInfo[]> {
    const params = new URLSearchParams();
    if (directory) params.set("directory", directory);
    if (projectID) params.set("projectID", projectID);
    if (surface) params.set("surface", surface);
    const qs = params.toString();
    const list = await requestJSON<SessionInfo[]>(`${SESSIONS}${qs ? `?${qs}` : ""}`);
    if (!Array.isArray(list)) return [];
    registerRunnerSessions(list);
    return list;
  },

  async resolveProject(directory: string): Promise<ProjectResolveResponse> {
    return requestJSON<ProjectResolveResponse>(
      `/api/hoxi/projects/resolve?directory=${encodeURIComponent(directory)}`
    );
  },

  async getSessionStatuses(directory?: string): Promise<Record<string, SessionStatus>> {
    return (await requestJSON<Record<string, SessionStatus>>(withDir(`${SESSIONS}/status`, directory))) || {};
  },

  async getSession(sessionID: string, directory?: string): Promise<SessionInfo> {
    const info = await requestJSON<SessionInfo>(withDir(sessionPath(sessionID), directory));
    if (info?.id) registerRunnerSessions([info]);
    return info;
  },

  // Desktop deep links, the CLI, and the native status bar menu queue navigation
  // intents in the gateway. Consuming clears the pending intent so a reload never replays it.
  async consumeDesktopIntent(): Promise<DesktopIntent | null> {
    const res = await requestJSON<{ intent?: DesktopIntent | null }>("/api/hoxi/desktop/intent", {
      method: "POST",
    });
    const intent = res?.intent;
    if (!intent) return null;
    const action =
      typeof intent.action === "string" && intent.action.trim()
        ? (intent.action.trim() as import("./api-types").DesktopIntentAction)
        : undefined;
    const sessionID =
      typeof intent.sessionID === "string" && intent.sessionID.trim()
        ? intent.sessionID.trim()
        : undefined;
    if (!action && !sessionID) return null;
    return {
      action,
      sessionID,
      messageID: typeof intent.messageID === "string" && intent.messageID.trim() ? intent.messageID.trim() : undefined,
      directory: typeof intent.directory === "string" && intent.directory.trim() ? intent.directory.trim() : undefined,
    };
  },

  async createSession(
    title?: string,
    directory?: string,
    options?: {
      surface?: "code" | "work";
      runtime?: SessionRuntime;
      modelID?: string;
      providerID?: string;
      mode?: string;
      sandboxMode?: "local" | "worktree";
      commandSandbox?: boolean;
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

  // `limit` returns the newest N turns and `before` pages backwards from that
  // message; "all" omits the limit so the server returns the whole transcript.
  getMessages(
    sessionID: string,
    directory?: string,
    limit: number | "all" = 100,
    before?: string
  ): Promise<Array<{ info: MessageInfo; parts: MessagePart[] }>> {
    const query = new URLSearchParams();
    if (limit !== "all") {
      query.set("limit", String(Number.isFinite(limit) && limit > 0 ? Math.min(2000, Math.floor(limit)) : 100));
    }
    if (before) query.set("before", before);
    const qs = query.toString();
    return requestJSON(withDir(`${sessionPath(sessionID)}/message${qs ? `?${qs}` : ""}`, directory));
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

  stopToolCommand(
    sessionID: string,
    input: { callID?: string; partID?: string; command?: string },
    directory?: string
  ): Promise<{ ok: boolean; stopped: number }> {
    return requestJSON(withDir(`${sessionPath(sessionID)}/stop-tool`, directory), {
      method: "POST",
      body: JSON.stringify(input),
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
  replyPermission(
    requestID: string,
    reply: "once" | "always" | "reject",
    directory?: string,
    message?: string,
    grantClass?: boolean
  ): Promise<void> {
    return requestJSON(withDir(`/api/hoxi/permission/${encodeURIComponent(requestID)}/reply`, directory), {
      method: "POST",
      body: JSON.stringify({ reply, ...(message ? { message } : {}), ...(grantClass ? { grantClass: true } : {}) }),
    });
  },
  async listApprovalGrants(params?: { sessionID?: string; directory?: string }): Promise<ApprovalGrant[]> {
    const q = new URLSearchParams();
    if (params?.sessionID) q.set("sessionID", params.sessionID);
    if (params?.directory) q.set("directory", params.directory);
    const qs = q.toString();
    const res = await requestJSON<{ grants?: ApprovalGrant[] }>(`/api/hoxi/permission/grants${qs ? `?${qs}` : ""}`);
    return Array.isArray(res?.grants) ? res.grants : [];
  },
  async revokeApprovalGrants(params: { id?: string; sessionID?: string; all?: boolean }): Promise<{ revoked: number; grants: ApprovalGrant[] }> {
    const q = new URLSearchParams();
    if (params.id) q.set("id", params.id);
    if (params.sessionID) q.set("sessionID", params.sessionID);
    if (params.all) q.set("all", "1");
    const qs = q.toString();
    const res = await requestJSON<{ revoked?: number; grants?: ApprovalGrant[] }>(`/api/hoxi/permission/grants${qs ? `?${qs}` : ""}`, {
      method: "DELETE",
    });
    return {
      revoked: Number(res?.revoked ?? 0),
      grants: Array.isArray(res?.grants) ? res.grants : [],
    };
  },
  async listApprovalAudit(params?: { sessionID?: string; limit?: number }): Promise<ApprovalAuditEntry[]> {
    const q = new URLSearchParams();
    if (params?.sessionID) q.set("sessionID", params.sessionID);
    if (params?.limit) q.set("limit", String(params.limit));
    const qs = q.toString();
    const res = await requestJSON<{ entries?: ApprovalAuditEntry[] }>(`/api/hoxi/permission/audit${qs ? `?${qs}` : ""}`);
    return Array.isArray(res?.entries) ? res.entries : [];
  },
  async clearApprovalAudit(): Promise<void> {
    await requestJSON("/api/hoxi/permission/audit", { method: "DELETE" });
  },
  fetchApprovalPolicy(): Promise<ApprovalPolicyState> {
    return requestJSON("/api/hoxi/permission/policy");
  },
  updateApprovalPolicy(policy: ApprovalPolicy): Promise<ApprovalPolicyState> {
    return requestJSON("/api/hoxi/permission/policy", { method: "PUT", body: JSON.stringify(policy) });
  },
  listDeferred(): Promise<Array<{ id: string; sessionID: string; category?: string; command: string; reason?: string; createdAt: number; resolved?: boolean }>> {
    return requestJSON("/api/hoxi/permission/deferred");
  },
  resolveDeferred(id: string): Promise<void> {
    return requestJSON("/api/hoxi/permission/deferred", { method: "POST", body: JSON.stringify({ id }) });
  },
  // Interactive Question Dock (Kilo QuestionDock)
  listQuestions(directory?: string): Promise<QuestionRequest[]> {
    return requestJSON(withDir("/api/hoxi/question", directory));
  },
  replyQuestion(requestID: string, answers: string[][], directory?: string): Promise<void> {
    return requestJSON(withDir(`/api/hoxi/question/${encodeURIComponent(requestID)}/reply`, directory), {
      method: "POST",
      body: JSON.stringify({ answers }),
    });
  },
  rejectQuestion(requestID: string, directory?: string): Promise<void> {
    return requestJSON(withDir(`/api/hoxi/question/${encodeURIComponent(requestID)}/reject`, directory), {
      method: "POST",
      body: JSON.stringify({}),
    });
  },
  // Agents / Modes & Commands
  listAgents(directory?: string): Promise<AgentInfo[]> {
    return requestJSON(withDir("/agent", directory));
  },
  listCommands(directory?: string): Promise<SlashCommandInfo[]> {
    return requestJSON(withDir("/command", directory));
  },
  getLSPStatus(directory?: string): Promise<any[]> {
    return requestJSON(withDir("/lsp", directory));
  },

  ...providerAndAssistAPI,
  ...fileAndPtyAPI,
  ...worktreeAndWorkspaceAPI,
};
