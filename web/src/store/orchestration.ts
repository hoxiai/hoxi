// web/src/store/orchestration.ts
import {
  cancelConvoyPlan as apiCancelConvoyPlan,
  createConvoyPlan as apiCreateConvoyPlan,
  fetchConvoyPlan as apiFetchConvoyPlan,
  type ConvoyPlan,
  type ConvoyPlanRequest,
  type ConvoyTaskSpec,
  type ConvoyTaskState,
  type ConvoyTaskStatus,
} from "../services/orchestration-api";

export type {
  ConvoyPlan,
  ConvoyPlanRequest,
  ConvoyTaskSpec,
  ConvoyTaskState,
  ConvoyTaskStatus,
};

export function createConvoyPlan(request: ConvoyPlanRequest): Promise<ConvoyPlan> {
  return apiCreateConvoyPlan(request);
}

export function fetchConvoyPlan(planID: string): Promise<ConvoyPlan> {
  return apiFetchConvoyPlan(planID);
}

export function cancelConvoyPlan(planID: string): Promise<ConvoyPlan> {
  return apiCancelConvoyPlan(planID);
}
