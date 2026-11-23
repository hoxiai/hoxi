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
const MAX_CONCURRENT_LIST_REQUESTS = 2;
const cache = new Map<string, CacheEntry>();
const inflight = new Map<string, Promise<unknown>>();

interface QueuedListRequest {
  key: string;
  repoDir: string;
  started: boolean;
  waiters: number;
  promise: Promise<ForgePullRequestListResponse>;
  start: () => void;
  abortBeforeStart: () => void;
}

const listInflight = new Map<string, QueuedListRequest>();
const listQueue: QueuedListRequest[] = [];
let activeListRequests = 0;

function pumpListQueue(): void {
  while (activeListRequests < MAX_CONCURRENT_LIST_REQUESTS && listQueue.length > 0) {
    const next = listQueue.shift()!;
    if (next.waiters <= 0) {
      listInflight.delete(next.key);
      next.abortBeforeStart();
      continue;
    }
    next.started = true;
    activeListRequests += 1;
    next.start();
  }
}

function makeAbortError(): Error {
  const err = new Error("Pull request list request aborted");
  err.name = "AbortError";
  return err;
}

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
  force = false,
  signal?: AbortSignal
): Promise<ForgePullRequestListResponse> {
  if (signal?.aborted) {
    return Promise.reject(makeAbortError());
  }
  const repoDir = canonicalRepoDirectory(directory);
  const key = `list:${repoDir}`;
  const now = Date.now();
  if (!force) {
    const cached = cache.get(key);
    if (cached && cached.expiresAt > now) {
      return Promise.resolve(cached.response as ForgePullRequestListResponse);
    }
    const existing = listInflight.get(key);
    if (existing) {
      return attachListWaiter(existing, signal);
    }
  }

  let resolveShared!: (value: ForgePullRequestListResponse) => void;
  let rejectShared!: (reason?: unknown) => void;
  const sharedPromise = new Promise<ForgePullRequestListResponse>((resolve, reject) => {
    resolveShared = resolve;
    rejectShared = reject;
  });

  const entry: QueuedListRequest = {
    key,
    repoDir,
    started: false,
    waiters: 0,
    promise: sharedPromise,
    start: () => {
      const params = new URLSearchParams({ directory: repoDir });
      requestJSON<ForgePullRequestListResponse>(`/api/hoxi/forge/prs?${params.toString()}`)
        .then((response) => {
          cache.set(key, { expiresAt: Date.now() + CACHE_TTL_MS, response });
          resolveShared(response);
        })
        .catch((err) => {
          rejectShared(err);
        })
        .finally(() => {
          activeListRequests = Math.max(0, activeListRequests - 1);
          if (listInflight.get(key) === entry) {
            listInflight.delete(key);
          }
          pumpListQueue();
        });
    },
    abortBeforeStart: () => {
      rejectShared(makeAbortError());
    },
  };

  if (!force) {
    listInflight.set(key, entry);
  }
  const waiterPromise = attachListWaiter(entry, signal);
  listQueue.push(entry);
  pumpListQueue();
  return waiterPromise;
}

function attachListWaiter(
  entry: QueuedListRequest,
  signal?: AbortSignal
): Promise<ForgePullRequestListResponse> {
  entry.waiters += 1;
  if (!signal) {
    return entry.promise;
  }
  return new Promise<ForgePullRequestListResponse>((resolve, reject) => {
    let settled = false;
    const onAbort = () => {
      if (settled) return;
      settled = true;
      entry.waiters = Math.max(0, entry.waiters - 1);
      if (!entry.started && entry.waiters === 0) {
        const idx = listQueue.indexOf(entry);
        if (idx >= 0) listQueue.splice(idx, 1);
        if (listInflight.get(entry.key) === entry) {
          listInflight.delete(entry.key);
        }
        entry.abortBeforeStart();
      }
      reject(makeAbortError());
    };
    signal.addEventListener("abort", onAbort, { once: true });
    entry.promise.then(
      (res) => {
        if (settled) return;
        settled = true;
        signal.removeEventListener("abort", onAbort);
        resolve(res);
      },
      (err) => {
        if (settled) return;
        settled = true;
        signal.removeEventListener("abort", onAbort);
        reject(err);
      }
    );
  });
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
