import { requestJSON } from "./runner-api";

export interface ForgeCheck {
  name: string;
  state: "success" | "failure" | "pending" | "neutral";
  url?: string;
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
}

export interface ForgePullRequestResponse {
  available: boolean;
  reason?: string;
  branch?: string;
  pullRequest?: ForgePullRequest;
}

interface CacheEntry {
  expiresAt: number;
  response: ForgePullRequestResponse;
}

const CACHE_TTL_MS = 30_000;
const cache = new Map<string, CacheEntry>();
const inflight = new Map<string, Promise<ForgePullRequestResponse>>();

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
    if (cached && cached.expiresAt > now) return Promise.resolve(cached.response);
    const pending = inflight.get(key);
    if (pending) return pending;
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
