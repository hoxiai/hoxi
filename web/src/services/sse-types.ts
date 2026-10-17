// web/src/services/sse-types.ts
// Verified against OpenCode v1.18.25 OpenAPI 3.x specification

export interface SessionInfo {
  id: string;
  title: string;
  slug?: string;
  parentID?: string;
  directory?: string;
  time: {
    created: number;
    updated: number;
  };
  revert?: {
    messageID: string;
    partID?: string;
    snapshot?: string;
    diff?: string;
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
}

export type ToolState =
  | {
      status: "pending";
      input: Record<string, any>;
      raw: string;
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
      time: { start: number; end: number; compacted?: number };
    }
  | {
      status: "error";
      input: Record<string, any>;
      error: string;
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

export interface SnapshotFileDiff {
  file: string;
  patch?: string;
  additions: number;
  deletions: number;
  status?: "added" | "deleted" | "modified";
}

export interface PermissionRequest {
  id: string;
  sessionID: string;
  permission: string;
  patterns: string[];
  metadata: Record<string, any>;
  always: string[];
  tool?: {
    messageID: string;
    callID: string;
  };
}

export type SessionStatus =
  | { type: "idle" }
  | { type: "busy" }
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
    };
