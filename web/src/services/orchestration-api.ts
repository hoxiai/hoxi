// web/src/services/orchestration-api.ts
import { requestJSON } from "./http";

export type ConvoyTaskStatus =
  | "queued"
  | "running"
  | "succeeded"
  | "failed"
  | "blocked"
  | "cancelled";

export interface ConvoyTaskSpec {
  id: string;
  prompt: string;
  dependsOn?: string[];
}

export interface ConvoyTaskState extends ConvoyTaskSpec {
  status: ConvoyTaskStatus;
  sessionID?: string;
  summary?: string;
  error?: string;
  startedAt?: number;
  endedAt?: number;
}

export interface ConvoyPlan {
  id: string;
  title: string;
  directory: string;
  mode?: string;
  provider?: string;
  model?: string;
  concurrency: number;
  /** True when every task runs in its own git worktree (false: shared folder, serial). */
  isolated?: boolean;
  status: ConvoyTaskStatus;
  tasks: ConvoyTaskState[];
  createdAt: number;
  updatedAt: number;
  error?: string;
}

export interface ConvoyPlanRequest {
  directory: string;
  title?: string;
  mode?: string;
  model?: string;
  providerID?: string;
  concurrency: number;
  tasks: ConvoyTaskSpec[];
}

export interface ConvoyDraftRequest {
  directory: string;
  goal: string;
  locale?: string;
  providerID?: string;
  modelID?: string;
  maxTasks?: number;
}

export async function draftConvoyPlan(request: ConvoyDraftRequest): Promise<ConvoyPlanRequest> {
  return requestJSON<ConvoyPlanRequest>("/api/hoxi/orchestration/draft", {
    method: "POST",
    body: JSON.stringify(request),
  });
}

export async function createConvoyPlan(request: ConvoyPlanRequest): Promise<ConvoyPlan> {
  return requestJSON<ConvoyPlan>("/api/hoxi/orchestration/plans", {
    method: "POST",
    body: JSON.stringify(request),
  });
}

export async function fetchConvoyPlans(directory: string): Promise<ConvoyPlan[]> {
  const query = directory ? `?directory=${encodeURIComponent(directory)}` : "";
  return requestJSON<ConvoyPlan[]>(`/api/hoxi/orchestration/plans${query}`);
}

export async function fetchConvoyPlansForSession(sessionID: string): Promise<ConvoyPlan[]> {
  return requestJSON<ConvoyPlan[]>(
    `/api/hoxi/orchestration/plans?session=${encodeURIComponent(sessionID)}`
  );
}

export async function fetchConvoyPlan(planID: string): Promise<ConvoyPlan> {
  return requestJSON<ConvoyPlan>(`/api/hoxi/orchestration/plans/${encodeURIComponent(planID)}`);
}

export async function cancelConvoyPlan(planID: string): Promise<ConvoyPlan> {
  return requestJSON<ConvoyPlan>(
    `/api/hoxi/orchestration/plans/${encodeURIComponent(planID)}/cancel`,
    { method: "POST" }
  );
}

export async function retryConvoyPlan(planID: string): Promise<ConvoyPlan> {
  return requestJSON<ConvoyPlan>(
    `/api/hoxi/orchestration/plans/${encodeURIComponent(planID)}/retry`,
    { method: "POST" }
  );
}
