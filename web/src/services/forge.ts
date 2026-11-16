import { requestJSON } from "./http";

export interface ForgeCheck {
  name: string;
  state: "success" | "failure" | "pending" | "neutral";
  url?: string;
}

export interface ForgeReviewComment {
  author?: string;
  body: string;
}

export interface ForgeReviewThread {
  path: string;
  line: number;
  isResolved: boolean;
  isOutdated: boolean;
  comments: ForgeReviewComment[];
}

export interface ForgePullRequest {
  number: number;
  url: string;
  title: string;
  state: string;
  isDraft: boolean;
  reviewDecision?: string;
  mergeStateStatus?: string;
  mergeable?: string;
  additions: number;
  deletions: number;
  changedFiles: number;
  baseRefName?: string;
  headRefName?: string;
  checks: ForgeCheck[];
  checksPassed: number;
  checksFailed: number;
  checksPending: number;
  checksTotal: number;
  checksState: "success" | "failure" | "pending" | "none" | string;
  unresolvedThreads?: number;
  reviewThreads?: ForgeReviewThread[];
}

export interface ForgePullRequestResponse {
  available: boolean;
  reason?: string;
  branch?: string;
  pullRequest?: ForgePullRequest;
}

export interface ForgePullRequestListResponse {
  available: boolean;
  reason?: string;
  pullRequests: Record<string, ForgePullRequest>;
}

export interface CreateForgePullRequestInput {
  directory: string;
  title?: string;
  body?: string;
  base?: string;
  head?: string;
  draft?: boolean;
}

export interface CreateForgePullRequestResponse {
  ok: boolean;
  available: boolean;
  reason?: string;
  error?: string;
  url?: string;
  branch?: string;
  pullRequest?: ForgePullRequest;
}

interface CacheEntry {
  expiresAt: number;
  response: unknown;
}

const CACHE_TTL_MS = 30_000;
const cache = new Map<string, CacheEntry>();
const inflight = new Map<string, Promise<unknown>>();

function cacheKey(directory: string, branch: string): string {
  return `${directory}\n${branch}`;
}

function canonicalRepoDirectory(directory: string): string {
  const normalized = (directory || "").trim().replace(/\\/g, "/");
  const idx = normalized.indexOf("/.hoxi/worktrees/");
  return idx > 0 ? normalized.slice(0, idx) : directory.trim();
}

export function fetchPullRequest(
  directory: string,
  branch: string,
  force = false
): Promise<ForgePullRequestResponse> {
  const key = cacheKey(directory, branch);
  const now = Date.now();
  if (!force) {
    const cached = cache.get(key);
    if (cached && cached.expiresAt > now) {
      return Promise.resolve(cached.response as ForgePullRequestResponse);
    }
    const pending = inflight.get(key);
    if (pending) return pending as Promise<ForgePullRequestResponse>;
  }

  const params = new URLSearchParams({ directory });
  if (branch) params.set("branch", branch);
  const request = requestJSON<ForgePullRequestResponse>(`/api/hoxi/forge/pr?${params.toString()}`)
    .then((response) => {
      cache.set(key, { expiresAt: Date.now() + CACHE_TTL_MS, response });
      return response;
    })
    .finally(() => {
      inflight.delete(key);
    });

  inflight.set(key, request);
  return request;
}

export function fetchPullRequests(
  directory: string,
  force = false
): Promise<ForgePullRequestListResponse> {
  const repoDir = canonicalRepoDirectory(directory);
  const key = `list:${repoDir}`;
  const now = Date.now();
  if (!force) {
    const cached = cache.get(key);
    if (cached && cached.expiresAt > now) {
      return Promise.resolve(cached.response as ForgePullRequestListResponse);
    }
    const pending = inflight.get(key);
    if (pending) return pending as Promise<ForgePullRequestListResponse>;
  }

  const params = new URLSearchParams({ directory: repoDir });
  const request = requestJSON<ForgePullRequestListResponse>(`/api/hoxi/forge/prs?${params.toString()}`)
    .then((response) => {
      cache.set(key, { expiresAt: Date.now() + CACHE_TTL_MS, response });
      return response;
    })
    .finally(() => {
      inflight.delete(key);
    });

  inflight.set(key, request);
  return request;
}

export function fetchPullRequestDetail(
  directory: string,
  branch: string,
  force = false
): Promise<ForgePullRequestResponse> {
  const key = `detail:${cacheKey(directory, branch)}`;
  const now = Date.now();
  if (!force) {
    const cached = cache.get(key);
    if (cached && cached.expiresAt > now) {
      return Promise.resolve(cached.response as ForgePullRequestResponse);
    }
    const pending = inflight.get(key);
    if (pending) return pending as Promise<ForgePullRequestResponse>;
  }

  const params = new URLSearchParams({ directory });
  if (branch) params.set("branch", branch);
  const request = requestJSON<ForgePullRequestResponse>(`/api/hoxi/forge/pr/detail?${params.toString()}`)
    .then((response) => {
      cache.set(key, { expiresAt: Date.now() + CACHE_TTL_MS, response });
      return response;
    })
    .finally(() => {
      inflight.delete(key);
    });

  inflight.set(key, request);
  return request;
}

export async function createPullRequest(
  input: CreateForgePullRequestInput
): Promise<CreateForgePullRequestResponse> {
  const response = await requestJSON<CreateForgePullRequestResponse>("/api/hoxi/forge/pr", {
    method: "POST",
    body: JSON.stringify(input),
  });
  if (response?.ok && input.directory) {
    const branch = (response.branch || input.head || "").trim();
    const key = cacheKey(input.directory, branch);
    cache.delete(key);
    cache.delete(`detail:${key}`);
    cache.delete(`list:${input.directory}`);
    cache.delete(`list:${canonicalRepoDirectory(input.directory)}`);
    if (response.pullRequest) {
      cache.set(key, {
        expiresAt: Date.now() + CACHE_TTL_MS,
        response: {
          available: true,
          branch,
          pullRequest: response.pullRequest,
        } satisfies ForgePullRequestResponse,
      });
    }
  }
  return response;
}
