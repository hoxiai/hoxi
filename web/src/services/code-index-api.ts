// web/src/services/code-index-api.ts
import { requestJSON } from "./http";

export interface CodeIndexResult {
  path: string;
  symbol?: string;
  startLine: number;
  endLine: number;
  score: number;
  snippet: string;
}

export interface CodeIndexSearchResponse {
  directory: string;
  query: string;
  results: CodeIndexResult[];
  files: number;
  chunks: number;
  indexedAt: number;
}

export interface CodeIndexStatus {
  directory: string;
  ready: boolean;
  files: number;
  chunks: number;
  indexedAt: number;
  root: string;
}

function dirQuery(path: string): string {
  return `directory=${encodeURIComponent(path)}`;
}

export async function searchCodeIndex(
  directory: string,
  query: string,
  limit = 12
): Promise<CodeIndexSearchResponse> {
  return requestJSON<CodeIndexSearchResponse>(
    `/api/hoxi/code-index/search?${dirQuery(directory)}&q=${encodeURIComponent(query)}&limit=${limit}`
  );
}

export async function fetchCodeIndexStatus(directory: string): Promise<CodeIndexStatus> {
  return requestJSON<CodeIndexStatus>(`/api/hoxi/code-index/status?${dirQuery(directory)}`);
}

export async function refreshCodeIndex(directory: string): Promise<CodeIndexStatus> {
  return requestJSON<CodeIndexStatus>("/api/hoxi/code-index/refresh", {
    method: "POST",
    body: JSON.stringify({ directory }),
  });
}
