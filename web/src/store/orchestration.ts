// web/src/store/orchestration.ts
import {
  cancelConvoyPlan as apiCancelConvoyPlan,
  createConvoyPlan as apiCreateConvoyPlan,
  draftConvoyPlan as apiDraftConvoyPlan,
  fetchConvoyPlan as apiFetchConvoyPlan,
  fetchConvoyPlans as apiFetchConvoyPlans,
  fetchConvoyPlansForSession as apiFetchConvoyPlansForSession,
  retryConvoyPlan as apiRetryConvoyPlan,
  type ConvoyDraftRequest,
  type ConvoyPlan,
  type ConvoyPlanRequest,
  type ConvoyTaskSpec,
  type ConvoyTaskState,
  type ConvoyTaskStatus,
} from "../services/orchestration-api";

export type {
  ConvoyDraftRequest,
  ConvoyPlan,
  ConvoyPlanRequest,
  ConvoyTaskSpec,
  ConvoyTaskState,
  ConvoyTaskStatus,
};

export function draftConvoyPlan(request: ConvoyDraftRequest): Promise<ConvoyPlanRequest> {
  return apiDraftConvoyPlan(request);
}

export function createConvoyPlan(request: ConvoyPlanRequest): Promise<ConvoyPlan> {
  return apiCreateConvoyPlan(request);
}

export function fetchConvoyPlans(directory: string): Promise<ConvoyPlan[]> {
  return apiFetchConvoyPlans(directory);
}

export function fetchConvoyPlansForSession(sessionID: string): Promise<ConvoyPlan[]> {
  return apiFetchConvoyPlansForSession(sessionID);
}

export function fetchConvoyPlan(planID: string): Promise<ConvoyPlan> {
  return apiFetchConvoyPlan(planID);
}

export function cancelConvoyPlan(planID: string): Promise<ConvoyPlan> {
  return apiCancelConvoyPlan(planID);
}

export function retryConvoyPlan(planID: string): Promise<ConvoyPlan> {
  return apiRetryConvoyPlan(planID);
}

export interface ConvoyHistoryLoader {
  /** Resolves the directory's plans, or null when superseded or invalidated. */
  load(directory: string): Promise<ConvoyPlan[] | null>;
  /** Drops every in-flight request (modal closed, directory changed, unmount). */
  invalidate(): void;
}

/**
 * Generation-guarded convoy history loader: a late response for a previous
 * directory or a closed modal can never overwrite the current view.
 */
export function createConvoyHistoryLoader(
  fetcher: (directory: string) => Promise<ConvoyPlan[]> = fetchConvoyPlans
): ConvoyHistoryLoader {
  let generation = 0;
  return {
    async load(directory: string): Promise<ConvoyPlan[] | null> {
      const current = ++generation;
      try {
        const plans = await fetcher(directory);
        return current === generation ? plans : null;
      } catch {
        return null;
      }
    },
    invalidate() {
      generation++;
    },
  };
}
