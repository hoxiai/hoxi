// web/src/services/schedules-api.ts
import { requestJSON } from "./http";

export type ScheduleTriggerKind = "cron" | "interval";
export type ScheduleMisfirePolicy = "skip" | "coalesce";
export type ScheduleNotifyPolicy = "alert_or_error" | "always" | "never";
export type ScheduleRunStatus =
  | "running"
  | "ok"
  | "alert"
  | "blocked"
  | "error"
  | "skipped_busy"
  | "interrupted";

export interface ScheduleTriggerSpec {
  kind: ScheduleTriggerKind;
  cron?: string;
  intervalMinutes?: number;
  timezone?: string;
  misfire?: ScheduleMisfirePolicy;
}

export interface ScheduleSkillRef {
  key: string;
  name: string;
  runtime?: string;
}

export interface ScheduleExecutionSpec {
  mode?: "" | "build" | "plan";
  sandboxMode?: "local" | "worktree";
  commandSandbox?: boolean;
  runtime?: string;
  providerID?: string;
  modelID?: string;
  variant?: string;
  timeoutSeconds?: number;
}

export interface ScheduleItem {
  id: string;
  name: string;
  enabled: boolean;
  projectID?: string;
  directory: string;
  sessionID?: string;
  trigger: ScheduleTriggerSpec;
  execution: ScheduleExecutionSpec;
  skillSelection?: ScheduleSkillRef;
  prompt: string;
  injectLastSummary: boolean;
  notifyOn: ScheduleNotifyPolicy;
  maxConsecutiveFails: number;

  lastRunAt?: number;
  nextRunAt?: number;
  lastOutcome?: ScheduleRunStatus | string;
  lastSummary?: string;
  lastError?: string;
  lastSessionID?: string;
  consecutiveFails?: number;
  pausedReason?: string;
  alertUnseen?: boolean;
  running?: boolean;
  createdAt: number;
  updatedAt: number;
}

export interface ScheduleRunRecord {
  id: string;
  scheduleID: string;
  sessionID?: string;
  trigger: "scheduled" | "manual" | string;
  status: ScheduleRunStatus | string;
  summary?: string;
  error?: string;
  startedAt: number;
  completedAt?: number;
}

export interface ScheduleUpsertPayload {
  name: string;
  enabled?: boolean;
  projectID?: string;
  directory: string;
  sessionID?: string;
  trigger: ScheduleTriggerSpec;
  execution: ScheduleExecutionSpec;
  skillSelection?: ScheduleSkillRef;
  prompt: string;
  injectLastSummary?: boolean;
  notifyOn?: ScheduleNotifyPolicy;
  maxConsecutiveFails?: number;
}

export interface SchedulePatchPayload {
  name?: string;
  enabled?: boolean;
  projectID?: string;
  directory?: string;
  sessionID?: string;
  trigger?: ScheduleTriggerSpec;
  execution?: ScheduleExecutionSpec;
  skillSelection?: ScheduleSkillRef;
  clearSkill?: boolean;
  prompt?: string;
  injectLastSummary?: boolean;
  notifyOn?: ScheduleNotifyPolicy;
  maxConsecutiveFails?: number;
}

export interface SchedulePreviewResponse {
  valid: boolean;
  error?: string;
  timezone?: string;
  nextRuns?: number[];
}

export const schedulesAPI = {
  list(params?: { projectID?: string; directory?: string }): Promise<ScheduleItem[]> {
    const q = new URLSearchParams();
    if (params?.projectID?.trim()) q.set("projectID", params.projectID.trim());
    if (params?.directory?.trim()) q.set("directory", params.directory.trim());
    const qs = q.toString();
    return requestJSON<ScheduleItem[]>(`/api/hoxi/schedules${qs ? `?${qs}` : ""}`);
  },

  create(payload: ScheduleUpsertPayload): Promise<ScheduleItem> {
    return requestJSON<ScheduleItem>("/api/hoxi/schedules", {
      method: "POST",
      body: JSON.stringify(payload),
    });
  },

  update(id: string, payload: SchedulePatchPayload): Promise<ScheduleItem> {
    return requestJSON<ScheduleItem>(`/api/hoxi/schedules/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(payload),
    });
  },

  remove(id: string): Promise<{ deleted: boolean; id: string }> {
    return requestJSON<{ deleted: boolean; id: string }>(
      `/api/hoxi/schedules/${encodeURIComponent(id)}`,
      { method: "DELETE" }
    );
  },

  runNow(id: string): Promise<ScheduleRunRecord> {
    return requestJSON<ScheduleRunRecord>(
      `/api/hoxi/schedules/${encodeURIComponent(id)}/run`,
      { method: "POST", body: "{}" }
    );
  },

  stopRun(id: string): Promise<{ stopped: boolean; id: string }> {
    return requestJSON<{ stopped: boolean; id: string }>(
      `/api/hoxi/schedules/${encodeURIComponent(id)}/stop`,
      { method: "POST", body: "{}" }
    );
  },

  acknowledge(id: string): Promise<ScheduleItem> {
    return requestJSON<ScheduleItem>(
      `/api/hoxi/schedules/${encodeURIComponent(id)}/ack`,
      { method: "POST", body: "{}" }
    );
  },

  listRuns(id: string): Promise<ScheduleRunRecord[]> {
    return requestJSON<ScheduleRunRecord[]>(
      `/api/hoxi/schedules/${encodeURIComponent(id)}/runs`
    );
  },

  previewTrigger(trigger: ScheduleTriggerSpec, count = 4): Promise<SchedulePreviewResponse> {
    return requestJSON<SchedulePreviewResponse>("/api/hoxi/schedules/preview-cron", {
      method: "POST",
      body: JSON.stringify({ trigger, count }),
    });
  },
};
