// web/src/services/sse-types.ts
// Verified against OpenCode v1.18.25 OpenAPI 3.x specification

export type SessionRuntime = "opencode" | "antigravity" | "gemini-cli" | "claude-code" | "codebuddy" | "qoder";
export type SessionSurface = "code" | "work";

export interface SessionCapabilities {
  approval?: boolean;
  steer?: boolean;
  resume?: boolean;
  revert?: boolean;
  fork?: boolean;
  attachments?: boolean;
  costStats?: boolean;
}

export interface SessionInfo {
  id: string;
  title: string;
  slug?: string;
  projectID?: string;
  parentID?: string;
  surface?: SessionSurface;
  directory?: string;
  runtime?: SessionRuntime;
  managed?: boolean;
  sandboxMode?: "local" | "worktree";
  branch?: string;
  modelID?: string;
  providerID?: string;
  time: {
    created: number;
    updated: number;
    archived?: number;
  };
  revert?: {
    messageID: string;
    partID?: string;
    snapshot?: string;
    diff?: string;
    codeChanged?: boolean;
  };
  capabilities?: SessionCapabilities;
}

export interface MessageError {
  name: string;
  data?: {
    message?: string;
    statusCode?: number;
    isRetryable?: boolean;
    [key: string]: any;
  };
}

export interface MessageTokens {
  total?: number;
  input: number;
  output: number;
  reasoning: number;
  cache: {
    read: number;
    write: number;
  };
}

export interface MessageInfo {
  id: string;
  sessionID: string;
  role: "user" | "assistant";
  time: {
    created: number;
    completed?: number;
  };
  modelID?: string;
  providerID?: string;
  agent?: string;
  mode?: string;
  cost?: number;
  tokens?: MessageTokens;
  finish?: string;
  error?: MessageError;
}

export type ToolState =
  | {
      status: "pending";
      input: Record<string, any>;
      raw: string;
      title?: string;
      metadata?: Record<string, any>;
    }
  | {
      status: "running";
      input: Record<string, any>;
      title?: string;
      metadata?: Record<string, any>;
      time: { start: number };
    }
  | {
      status: "completed";
      input: Record<string, any>;
      output: string;
      title: string;
      metadata: Record<string, any>;
      attachments?: Array<{ type?: string; mime: string; filename?: string; url: string }>;
      time: { start: number; end: number; compacted?: number };
    }
  | {
      status: "error";
      input: Record<string, any>;
      error: string;
      title?: string;
      metadata?: Record<string, any>;
      time: { start: number; end: number };
    };

export type MessagePart =
  | {
      id: string;
      sessionID: string;
      messageID: string;
      type: "text";
      text: string;
      metadata?: Record<string, any>;
    }
  | {
      id: string;
      sessionID: string;
      messageID: string;
      type: "file";
      mime: string;
      filename?: string;
      url: string;
    }
  | {
      id: string;
      sessionID: string;
      messageID: string;
      type: "reasoning";
      text: string;
      time: { start: number; end?: number };
    }
  | {
      id: string;
      sessionID: string;
      messageID: string;
      type: "tool";
      callID: string;
      tool: string;
      state: ToolState;
    }
  | {
      id: string;
      sessionID: string;
      messageID: string;
      type: "patch";
      hash: string;
      files: string[];
    };

export type ToolPart = Extract<MessagePart, { type: "tool" }>;

export interface SnapshotFileDiff {
  file: string;
  patch?: string;
  before?: string;
  after?: string;
  additions: number;
  deletions: number;
  status?: "added" | "deleted" | "modified";
  repo?: string;
  repoDir?: string;
  branch?: string;
  generatedLike?: boolean;
  binary?: boolean;
  large?: boolean;
  stamp?: string;
}

export interface PermissionRequest {
  id: string;
  sessionID: string;
  permission: string;
  patterns: string[];
  metadata: Record<string, any>;
  always: string[];
  createdAt?: number;
  expiresAt?: number;
  tool?: {
    messageID: string;
    callID: string;
  };
}

export interface ApprovalGrant {
  id: string;
  sessionID: string;
  sessionTitle?: string;
  runtime?: string;
  kind: "bash" | "tool" | "doom_loop" | string;
  tool: string;
  // The raw lookup key (command text or tool+args hash) stays server-side:
  // it can contain credentials that `pattern` redacts.
  pattern: string;
  scope: "session_memory" | string;
  source: "desktop" | string;
  createdAt: number;
}

export interface ApprovalAuditEntry {
  id: string;
  timestamp: number;
  requestID: string;
  sessionID: string;
  sessionTitle?: string;
  runtime?: string;
  permission: string;
  tool?: string;
  summary: string;
  directory?: string;
  destructive?: boolean;
  decision: "once" | "always" | "reject" | "timeout" | "interrupted" | string;
  source: "desktop" | "tunnel" | "system" | "policy" | "ai_review" | string;
  message?: string;
}

/** Approval mode and per-tool overrides; the Go gate applies them to every runtime. */
export interface ApprovalPolicy {
  mode: "auto" | "smart" | "manual" | "trusted" | "unattended";
  edit?: "" | "allow" | "ask";
  webfetch?: "" | "allow" | "ask";
  bash?: "" | "allow" | "smart" | "ask";
  aiReview?: boolean;
  aiReviewModel?: string;
  approvalTimeoutSeconds?: number;
  updatedAt?: number;
}

export interface ApprovalPolicyState {
  policy: ApprovalPolicy;
  /** False until the policy was saved once (the desktop migrates legacy settings then). */
  exists: boolean;
}

export interface QuestionOption {
  label: string;
  description: string;
}

export interface QuestionInfo {
  question: string;
  header: string;
  options: QuestionOption[];
  multiple?: boolean;
  custom?: boolean;
}

export interface QuestionRequest {
  id: string;
  sessionID: string;
  questions: QuestionInfo[];
  tool?: {
    messageID: string;
    callID: string;
  };
}

export type SessionStatus =
  | { type: "idle" }
  | { type: "busy"; stalledSince?: number }
  | { type: "retry"; attempt: number; message: string; next: number };

export type OpenCodeEvent =
  | {
      type: "message.updated";
      properties: { sessionID?: string; info: MessageInfo };
    }
  | {
      type: "message.part.updated";
      properties: { sessionID?: string; part: MessagePart };
    }
  | {
      type: "message.part.delta";
      properties: {
        sessionID: string;
        messageID: string;
        partID: string;
        field: string;
        delta: string;
      };
    }
  | {
      type: "permission.asked";
      properties: PermissionRequest;
    }
  | {
      type: "permission.replied";
      properties: {
        sessionID: string;
        requestID: string;
        reply: "once" | "always" | "reject";
      };
    }
  | {
      type: "question.asked" | "question.v2.asked";
      properties: QuestionRequest;
    }
  | {
      type: "question.replied" | "question.v2.replied";
      properties: {
        sessionID: string;
        requestID: string;
        answers: string[][];
      };
    }
  | {
      type: "question.rejected" | "question.v2.rejected";
      properties: {
        sessionID: string;
        requestID: string;
      };
    }
  | {
      type: "session.updated" | "session.created";
      properties: {
        info: SessionInfo;
      };
    }
  | {
      type: "session.deleted";
      properties: {
        info?: SessionInfo;
        sessionID?: string;
      };
    }
  | {
      type: "session.status";
      properties: {
        sessionID: string;
        status: SessionStatus;
      };
    }
  | {
      type: "session.idle";
      properties: {
        sessionID: string;
      };
    }
  | {
      // Hoxi runner watchdog: a busy turn has produced no output since
      // `stalledSince` (unix ms); 0 clears the flag.
      type: "session.stall";
      properties: {
        sessionID: string;
        stalledSince: number;
      };
    }
  | {
      type: "session.diff";
      properties: {
        sessionID: string;
        diff: SnapshotFileDiff[];
      };
    }
  | {
      type: "session.error";
      properties: {
        sessionID?: string;
        error?: { name: string; data: Record<string, any> };
      };
    }
  | {
      // Stream control from /api/hoxi/event: `server.heartbeat` proves the
      // connection is alive; `hoxi.stream.resync` means OpenCode events may
      // have been missed while its upstream leg reconnected.
      type: "server.heartbeat" | "hoxi.stream.resync";
      properties: Record<string, never>;
    }
  | {
      // Authoritative busy set for every Hoxi-managed session across all
      // projects. `scope: "managed"` means the runner's process table, so an
      // omitted session really is idle. It lets the sidebar keep showing
      // running work after the user switches projects.
      type: "hoxi.status.snapshot";
      properties: {
        scope: "managed";
        busy: string[];
        // sessionID -> unix ms when the turn was flagged as stalled.
        stalled?: Record<string, number>;
      };
    };
