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
  const key = `list:${directory}`;
  const now = Date.now();
  if (!force) {
    const cached = cache.get(key);
    if (cached && cached.expiresAt > now) {
      return Promise.resolve(cached.response as ForgePullRequestListResponse);
    }
    const pending = inflight.get(key);
    if (pending) return pending as Promise<ForgePullRequestListResponse>;
  }

  const params = new URLSearchParams({ directory });
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
