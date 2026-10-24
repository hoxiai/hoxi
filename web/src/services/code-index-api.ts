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

export interface CodeIndexInspectResponse {
  directory: string;
  path: string;
  symbol?: string;
  startLine: number;
  endLine: number;
  totalLines: number;
  truncated?: boolean;
  content: string;
}

export interface CodeIndexCallerHit {
  path: string;
  symbol?: string;
  line: number;
  snippet: string;
}

export interface CodeIndexCalleeHit {
  path: string;
  symbol: string;
  startLine: number;
  endLine: number;
  callLine: number;
  snippet?: string;
}

export interface CodeIndexImpactHit {
  path: string;
  symbol?: string;
  line: number;
  depth: number;
  via: string;
  snippet: string;
}

export interface CodeIndexCalleesResponse {
  directory: string;
  path: string;
  symbol: string;
  imports?: string[];
  callees: CodeIndexCalleeHit[];
}

export interface CodeIndexImpactResponse {
  directory: string;
  path?: string;
  symbol: string;
  directCallers: CodeIndexCallerHit[];
  transitiveCallers?: CodeIndexImpactHit[];
  tests: CodeIndexCallerHit[];
  affectedFiles: string[];
}

export interface CodeIndexContextResponse {
  directory: string;
  inspect: CodeIndexInspectResponse;
  imports?: string[];
  callees: CodeIndexCalleeHit[];
  callers: CodeIndexCallerHit[];
  transitiveCallers?: CodeIndexImpactHit[];
  tests: CodeIndexCallerHit[];
  affectedFiles?: string[];
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

export async function fetchCodeIndexCallees(
  directory: string,
  symbol: string,
  path = "",
  limit = 16
): Promise<CodeIndexCalleesResponse> {
  const pathParam = path ? `&path=${encodeURIComponent(path)}` : "";
  return requestJSON<CodeIndexCalleesResponse>(
    `/api/hoxi/code-index/callees?${dirQuery(directory)}&symbol=${encodeURIComponent(symbol)}${pathParam}&limit=${limit}`
  );
}

export async function fetchCodeIndexImpact(
  directory: string,
  symbol: string,
  path = "",
  depth = 2,
  limit = 16
): Promise<CodeIndexImpactResponse> {
  const pathParam = path ? `&path=${encodeURIComponent(path)}` : "";
  return requestJSON<CodeIndexImpactResponse>(
    `/api/hoxi/code-index/impact?${dirQuery(directory)}&symbol=${encodeURIComponent(symbol)}${pathParam}&depth=${depth}&limit=${limit}`
  );
}

export async function fetchCodeIndexContext(
  directory: string,
  symbol: string,
  path = "",
  contextLines = 5,
  limit = 12
): Promise<CodeIndexContextResponse> {
  const pathParam = path ? `&path=${encodeURIComponent(path)}` : "";
  return requestJSON<CodeIndexContextResponse>(
    `/api/hoxi/code-index/context?${dirQuery(directory)}&symbol=${encodeURIComponent(symbol)}${pathParam}&contextLines=${contextLines}&limit=${limit}`
  );
}
