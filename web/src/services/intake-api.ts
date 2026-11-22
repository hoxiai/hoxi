// web/src/services/intake-api.ts
import { requestJSON, withDir } from "./http";

export type IntakeTaskStatus =
  | "staged"
  | "running"
  | "succeeded"
  | "awaiting_retest"
  | "reopened"
  | "resolved"
  | "failed"
  | "rejected";

export interface IntakeLinkItem {
  id: string;
  directory: string;
  projectName: string;
  label: string;
  enabled: boolean;
  portalPath: string;
  localUrl?: string;
  lanUrl?: string;
  relayUrl?: string;
  createdAt: number;
  updatedAt: number;
  expiresAt?: number;
}

export interface IntakeAttachmentRef {
  id: string;
  filename: string;
  mime: string;
  size: number;
  kind: string;
  url?: string;
}

export interface IntakeTaskComment {
  id: string;
  authorRole: "tester" | "developer";
  authorLabel?: string;
  action?: string;
  text: string;
  attachments?: IntakeAttachmentRef[];
  createdAt: number;
}

export interface IntakeTaskItem {
  id: string;
  linkId: string;
  submitterLabel: string;
  directory?: string;
  projectName: string;
  title: string;
  description: string;
  steps?: string;
  attachments?: IntakeAttachmentRef[];
  comments?: IntakeTaskComment[];
  status: IntakeTaskStatus;
  sessionID?: string;
  hasTimeline?: boolean;
  developerNote?: string;
  summary?: string;
  error?: string;
  createdAt: number;
  updatedAt: number;
  approvedAt?: number;
  completedAt?: number;
}

export interface SanitizedTodoItem {
  content: string;
  status: string;
}

export interface SanitizedExecutionStep {
  id: string;
  kind: "tool" | "text" | "todo";
  category?: string;
  label?: string;
  text?: string;
  status?: "pending" | "running" | "completed" | "error" | string;
  todos?: SanitizedTodoItem[];
  startTime?: number;
  endTime?: number;
}

export interface SanitizedExecutionTurn {
  id: string;
  createdAt: number;
  completedAt?: number;
  steps: SanitizedExecutionStep[];
}

export interface SanitizedExecutionTimeline {
  taskId: string;
  busy: boolean;
  turns: SanitizedExecutionTurn[];
}

export interface PortalBootstrapInfo {
  projectName: string;
  label: string;
  tasks: IntakeTaskItem[];
}

export interface PortalStreamEvent {
  type: "connected" | "heartbeat" | "task.updated" | "timeline.updated";
  taskId?: string;
}

const PORTAL_PATH_RE = /^\/qa\/(hqa_[0-9a-f]{32,128})\/?$/;

export function extractPortalRouteToken(pathname?: string): string | null {
  const target =
    typeof pathname === "string"
      ? pathname
      : typeof window !== "undefined"
        ? window.location.pathname
        : "";
  const match = PORTAL_PATH_RE.exec(target.trim());
  return match ? match[1] : null;
}

async function portalRequestJSON<T>(token: string, url: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    "X-Hoxi-Intake-Token": token,
    ...((init?.headers as Record<string, string>) || {}),
  };
  if (!(init?.body instanceof FormData) && !headers["Content-Type"]) {
    headers["Content-Type"] = "application/json";
  }
  const res = await fetch(url, {
    ...init,
    headers,
  });
  if (!res.ok) {
    const text = await res.text();
    const err = new Error(`HTTP ${res.status}: ${text}`) as Error & { status?: number };
    err.status = res.status;
    throw err;
  }
  return res.json();
}

export const intakeAPI = {
  // Desktop owner endpoints
  async listLinks(directory: string): Promise<IntakeLinkItem[]> {
    const res = await requestJSON<{ links?: IntakeLinkItem[] }>(
      withDir("/api/hoxi/intake/links", directory)
    );
    return Array.isArray(res?.links) ? res.links : [];
  },

  async createLink(payload: {
    directory: string;
    label: string;
    projectName?: string;
    expiresInHours?: number;
  }): Promise<IntakeLinkItem> {
    return requestJSON<IntakeLinkItem>("/api/hoxi/intake/links", {
      method: "POST",
      body: JSON.stringify(payload),
    });
  },

  async deleteLink(id: string): Promise<void> {
    await requestJSON(`/api/hoxi/intake/links?id=${encodeURIComponent(id)}`, {
      method: "DELETE",
    });
  },

  async listTasks(directory: string): Promise<IntakeTaskItem[]> {
    const res = await requestJSON<{ tasks?: IntakeTaskItem[] }>(
      withDir("/api/hoxi/intake/tasks", directory)
    );
    return Array.isArray(res?.tasks) ? res.tasks : [];
  },

  async approveTask(payload: {
    taskId: string;
    directory?: string;
    mode?: string;
    providerID?: string;
    model?: string;
  }): Promise<IntakeTaskItem> {
    return requestJSON<IntakeTaskItem>("/api/hoxi/intake/tasks/approve", {
      method: "POST",
      body: JSON.stringify(payload),
    });
  },

  async updateTaskStatus(payload: {
    taskId: string;
    status: "awaiting_retest" | "rejected" | "resolved" | "cancel";
    developerNote?: string;
  }): Promise<IntakeTaskItem> {
    return requestJSON<IntakeTaskItem>("/api/hoxi/intake/tasks/status", {
      method: "POST",
      body: JSON.stringify(payload),
    });
  },

  // Tester portal endpoints (authenticated exclusively by scoped hqa_ token)
  async getPortalInfo(token: string): Promise<PortalBootstrapInfo> {
    return portalRequestJSON<PortalBootstrapInfo>(token, "/api/hoxi/intake/portal/info");
  },

  async uploadPortalFile(token: string, file: File): Promise<IntakeAttachmentRef> {
    const form = new FormData();
    form.append("file", file, file.name || "screenshot.png");
    return portalRequestJSON<IntakeAttachmentRef>(token, "/api/hoxi/intake/portal/attachments", {
      method: "POST",
      body: form,
    });
  },

  async submitPortalTask(
    token: string,
    payload: {
      title: string;
      description: string;
      steps?: string;
      attachmentIds?: string[];
    }
  ): Promise<IntakeTaskItem> {
    return portalRequestJSON<IntakeTaskItem>(token, "/api/hoxi/intake/portal/tasks", {
      method: "POST",
      body: JSON.stringify(payload),
    });
  },

  async submitPortalFeedback(
    token: string,
    payload: {
      taskId: string;
      action: "pass" | "reopen" | "comment";
      text?: string;
      attachmentIds?: string[];
    }
  ): Promise<IntakeTaskItem> {
    return portalRequestJSON<IntakeTaskItem>(token, "/api/hoxi/intake/portal/tasks/feedback", {
      method: "POST",
      body: JSON.stringify(payload),
    });
  },

  async getPortalTimeline(token: string, taskId: string): Promise<SanitizedExecutionTimeline> {
    return portalRequestJSON<SanitizedExecutionTimeline>(
      token,
      `/api/hoxi/intake/portal/timeline?taskId=${encodeURIComponent(taskId)}`
    );
  },

  connectPortalStream(
    token: string,
    handlers: {
      onEvent: (evt: PortalStreamEvent) => void;
      onConnectionChange?: (connected: boolean) => void;
    }
  ): () => void {
    if (typeof EventSource === "undefined") {
      return () => {};
    }
    let closed = false;
    let es: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

    const connect = () => {
      if (closed) return;
      const url = `/api/hoxi/intake/portal/stream?token=${encodeURIComponent(token)}`;
      es = new EventSource(url);
      es.onopen = () => handlers.onConnectionChange?.(true);
      es.onmessage = (msg) => {
        if (!msg.data) return;
        try {
          const parsed = JSON.parse(msg.data) as PortalStreamEvent;
          if (parsed && typeof parsed.type === "string") {
            handlers.onEvent(parsed);
          }
        } catch {
          // ignore malformed frame
        }
      };
      es.onerror = () => {
        handlers.onConnectionChange?.(false);
        es?.close();
        es = null;
        if (!closed && reconnectTimer === null) {
          reconnectTimer = setTimeout(() => {
            reconnectTimer = null;
            connect();
          }, 2500);
        }
      };
    };

    connect();

    return () => {
      closed = true;
      if (reconnectTimer !== null) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
      }
      es?.close();
      es = null;
    };
  },
};
