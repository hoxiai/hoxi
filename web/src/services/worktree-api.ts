// web/src/services/worktree-api.ts
import type { SnapshotFileDiff } from "./sse-types";
import type {
  BranchListResponse,
  CommitAndPushResponse,
  ExternalSessionDetail,
  FileContentResponse,
  FileValidateResponse,
  HandoffImportResponse,
  HandoffSessionsResponse,
  LocalCLIImportResponse,
  LocalCLIStatusResponse,
  ProjectInfo,
  SaveWorkspaceInput,
  WorkspaceProfile,
  WorktreeMeta,
  WorktreeStatus,
  WorktreeVerifyConfig,
  WorktreeVerifyState,
} from "./api-types";
import { requestJSON, withDir } from "./http";

export const fileAndPtyAPI = {
  searchFiles(
    query: string,
    directory?: string,
    sessionID?: string,
    limit = 60
  ): Promise<Array<{ path: string; name: string; type: string }>> {
    const params = new URLSearchParams({ q: query });
    if (directory) params.set("directory", directory);
    if (sessionID) params.set("sessionID", sessionID);
    if (limit > 0) params.set("limit", String(limit));
    return requestJSON(`/api/hoxi/file/search?${params.toString()}`);
  },
  listFiles(
    path = "",
    directory?: string,
    sessionID?: string
  ): Promise<Array<{ path: string; name?: string; type?: string }>> {
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
      body: JSON.stringify({
        paths,
        ...(directory ? { directory } : {}),
        ...(sessionID ? { sessionID } : {}),
      }),
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
  deletePTY(ptyID: string, directory?: string): Promise<void> {
    return requestJSON(withDir(`/pty/${encodeURIComponent(ptyID)}`, directory), {
      method: "DELETE",
    });
  },
};

export const worktreeAndWorkspaceAPI = {
  // Git Worktree Sandbox Manager
  createWorktree(sessionID: string, directory?: string): Promise<WorktreeMeta> {
    return requestJSON("/api/hoxi/worktree/create", {
      method: "POST",
      body: JSON.stringify({ sessionID, ...(directory ? { directory } : {}) }),
    });
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
  applyWorktree(sessionID: string, mode: "patch" | "merge" = "patch", message?: string): Promise<{ ok: boolean }> {
    return requestJSON("/api/hoxi/worktree/apply", {
      method: "POST",
      body: JSON.stringify({
        sessionID,
        mode,
        ...(message?.trim() ? { message: message.trim() } : {}),
      }),
    });
  },
  syncWorktree(sessionID: string, directory?: string): Promise<WorktreeStatus> {
    return requestJSON("/api/hoxi/worktree/sync", {
      method: "POST",
      body: JSON.stringify({ sessionID, ...(directory ? { directory } : {}) }),
    });
  },
  revertWorktreeFile(sessionID: string, file: string, directory?: string): Promise<{ ok: boolean }> {
    return requestJSON("/api/hoxi/worktree/revert-file", {
      method: "POST",
      body: JSON.stringify({ sessionID, file, ...(directory ? { directory } : {}) }),
    });
  },
  commitWorktree(
    sessionID: string,
    message: string,
    opts?: { push?: boolean; directory?: string; sessionDiffs?: SnapshotFileDiff[] }
  ): Promise<CommitAndPushResponse> {
    return requestJSON("/api/hoxi/worktree/commit", {
      method: "POST",
      body: JSON.stringify({
        sessionID,
        message,
        ...(opts?.push ? { push: true } : {}),
        ...(opts?.directory ? { directory: opts.directory } : {}),
        ...(opts?.sessionDiffs !== undefined ? { sessionDiffs: opts.sessionDiffs } : {}),
      }),
    });
  },
  pushWorktree(sessionID: string, directory?: string): Promise<CommitAndPushResponse> {
    return requestJSON("/api/hoxi/worktree/push", {
      method: "POST",
      body: JSON.stringify({ sessionID, ...(directory ? { directory } : {}) }),
    });
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
    return requestJSON("/api/hoxi/worktree/checkout", {
      method: "POST",
      body: JSON.stringify({ branch, create, ...(directory ? { directory } : {}) }),
    });
  },
  renameBranch(sessionID: string, newBranch: string, directory?: string): Promise<{ ok: boolean; branch: string }> {
    return requestJSON("/api/hoxi/worktree/rename-branch", {
      method: "POST",
      body: JSON.stringify({ sessionID, newBranch, ...(directory ? { directory } : {}) }),
    });
  },
  pruneSandboxes(excludeSessionID?: string, directory?: string): Promise<{ ok: boolean; removedCount: number }> {
    return requestJSON("/api/hoxi/worktree/prune-sandboxes", {
      method: "POST",
      body: JSON.stringify({
        ...(excludeSessionID ? { excludeSessionID } : {}),
        ...(directory ? { directory } : {}),
      }),
    });
  },
  retryWorktreeSetup(sessionID: string, directory?: string): Promise<WorktreeVerifyState> {
    return requestJSON("/api/hoxi/worktree/setup-retry", {
      method: "POST",
      body: JSON.stringify({ sessionID, ...(directory ? { directory } : {}) }),
    });
  },
  getWorktreeVerifyStatus(sessionID: string, directory?: string): Promise<WorktreeVerifyState> {
    const q = `sessionID=${encodeURIComponent(sessionID)}${directory ? `&directory=${encodeURIComponent(directory)}` : ""}`;
    return requestJSON(`/api/hoxi/worktree/verify-status?${q}`);
  },
  saveWorktreeVerifyConfig(input: {
    directory?: string;
    runCommand: string;
    testCommand: string;
  }): Promise<WorktreeVerifyConfig> {
    return requestJSON("/api/hoxi/worktree/verify-config", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },
  runWorktreeVerify(input: {
    sessionID: string;
    directory?: string;
    kind: "run" | "test";
    command?: string;
  }): Promise<WorktreeVerifyState> {
    return requestJSON("/api/hoxi/worktree/verify-run", {
      method: "POST",
      body: JSON.stringify(input),
    });
  },
  stopWorktreeVerify(sessionID: string, directory?: string): Promise<WorktreeVerifyState> {
    return requestJSON("/api/hoxi/worktree/verify-stop", {
      method: "POST",
      body: JSON.stringify({ sessionID, ...(directory ? { directory } : {}) }),
    });
  },

  // OpenCode Project / Workspace Manager (Kilo Desktop)
  listProjects(): Promise<ProjectInfo[]> {
    return requestJSON("/project");
  },
  listWorkspaces(): Promise<{ workspaces: WorkspaceProfile[]; dismissed?: string[] }> {
    return requestJSON("/api/hoxi/workspaces");
  },
  createWorkspace(input: SaveWorkspaceInput): Promise<WorkspaceProfile> {
    return requestJSON("/api/hoxi/workspaces", { method: "POST", body: JSON.stringify(input) });
  },
  updateWorkspace(id: string, input: SaveWorkspaceInput): Promise<WorkspaceProfile> {
    const byBody = id.includes("/") || id.includes("\\");
    return requestJSON(byBody ? "/api/hoxi/workspaces" : `/api/hoxi/workspaces/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify({ ...input, id }),
    });
  },
  deleteWorkspace(id: string): Promise<{ ok: boolean; deleted?: boolean; dismissed?: string[] }> {
    const byQuery = id.includes("/") || id.includes("\\");
    return requestJSON(
      byQuery ? `/api/hoxi/workspaces?id=${encodeURIComponent(id)}` : `/api/hoxi/workspaces/${encodeURIComponent(id)}`,
      { method: "DELETE" }
    );
  },
  openDirectoryDialog(): Promise<{ path: string; error?: string }> {
    return requestJSON("/api/hoxi/dialog/open-directory", { method: "POST" });
  },

  // Cross-Tool Session Handoff & Local CLI Bridge
  listExternalSessions(opts?: {
    directory?: string;
    query?: string;
    source?: string;
    onlyCurrent?: boolean;
  }): Promise<HandoffSessionsResponse> {
    const params = new URLSearchParams();
    if (opts?.directory) params.set("directory", opts.directory);
    if (opts?.query) params.set("query", opts.query);
    if (opts?.source) params.set("source", opts.source);
    if (opts?.onlyCurrent) params.set("onlyCurrent", "1");
    return requestJSON(`/api/hoxi/handoff/sessions?${params.toString()}`);
  },
  getExternalSessionPreview(source: string, id: string, cwd?: string): Promise<ExternalSessionDetail> {
    const params = new URLSearchParams({ source, id, ...(cwd ? { cwd } : {}) });
    return requestJSON(`/api/hoxi/handoff/preview?${params.toString()}`);
  },
  importExternalSession(req: {
    source: string;
    sessionID: string;
    sourceCwd?: string;
    targetDirectory?: string;
    preferredProviderID?: string;
    preferredModelID?: string;
  }): Promise<HandoffImportResponse> {
    return requestJSON("/api/hoxi/handoff/import", { method: "POST", body: JSON.stringify(req) });
  },
  getLocalCLIStatus(): Promise<LocalCLIStatusResponse> {
    return requestJSON("/api/hoxi/local-cli/status");
  },
  importLocalCLI(toolID: string): Promise<LocalCLIImportResponse> {
    return requestJSON("/api/hoxi/local-cli/import", { method: "POST", body: JSON.stringify({ toolID }) });
  },
};
