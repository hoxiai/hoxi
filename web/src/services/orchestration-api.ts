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
  concurrency: number;
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

export async function createConvoyPlan(request: ConvoyPlanRequest): Promise<ConvoyPlan> {
  return requestJSON<ConvoyPlan>("/api/hoxi/orchestration/plans", {
    method: "POST",
    body: JSON.stringify(request),
  });
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
