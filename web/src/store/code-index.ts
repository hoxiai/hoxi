// web/src/store/code-index.ts
import {
  fetchCodeIndexStatus as apiFetchCodeIndexStatus,
  refreshCodeIndex as apiRefreshCodeIndex,
  searchCodeIndex as apiSearchCodeIndex,
  type CodeIndexResult,
  type CodeIndexSearchResponse,
  type CodeIndexStatus,
} from "../services/code-index-api";
import {
  searchSessionRecall as apiSearchSessionRecall,
  type RecallResult,
  type RecallSearchResponse,
} from "../services/recall-api";

export type {
  CodeIndexResult,
  CodeIndexSearchResponse,
  CodeIndexStatus,
  RecallResult,
  RecallSearchResponse,
};

export function fetchCodeIndexStatus(directory: string): Promise<CodeIndexStatus> {
  return apiFetchCodeIndexStatus(directory);
}

export function refreshCodeIndex(directory: string): Promise<CodeIndexStatus> {
  return apiRefreshCodeIndex(directory);
}

export function searchCodeIndex(
  directory: string,
  query: string,
  limit = 12
): Promise<CodeIndexSearchResponse> {
  return apiSearchCodeIndex(directory, query, limit);
}

export function searchSessionRecall(
  directory: string,
  query: string,
  limit = 8
): Promise<RecallSearchResponse> {
  return apiSearchSessionRecall(directory, query, limit);
}
