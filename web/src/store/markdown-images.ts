import { hoxiAPI } from "../services/api";

export const MAX_MARKDOWN_IMAGE_CACHE_BYTES = 16 * 1024 * 1024;
const MAX_CACHED_IMAGES = 40;
const MAX_CONCURRENT_LOADS = 2;
const cache = new Map<string, { url: string; bytes: number; sessionID?: string }>();
const inflight = new Map<string, { promise: Promise<string | null>; sessionID?: string; valid: boolean }>();
const queue: Array<() => void> = [];
let cacheBytes = 0;
let activeLoads = 0;

function imageKey(path: string, directory: string, sessionID?: string): string {
  return `${directory}\0${sessionID ?? ""}\0${path}`;
}

function deleteImage(key: string): void {
  const entry = cache.get(key);
  if (!entry) return;
  cache.delete(key);
  cacheBytes -= entry.bytes;
}

export function getCachedMarkdownImage(path: string, directory: string, sessionID?: string): string | undefined {
  const key = imageKey(path, directory, sessionID);
  const entry = cache.get(key);
  if (!entry) return undefined;
  cache.delete(key);
  cache.set(key, entry);
  return entry.url;
}

export function markdownImageCacheUsage(): { entries: number; bytes: number } {
  return { entries: cache.size, bytes: cacheBytes };
}

export function forgetSessionMarkdownImages(sessionIDs: Iterable<string>): void {
  const ids = new Set(sessionIDs);
  for (const [key, entry] of cache) if (entry.sessionID && ids.has(entry.sessionID)) deleteImage(key);
  for (const [key, entry] of inflight) {
    if (entry.sessionID && ids.has(entry.sessionID)) {
      entry.valid = false;
      inflight.delete(key);
    }
  }
}

function cacheImage(key: string, url: string, sessionID?: string): void {
  const bytes = url.length * 2; // Conservative UTF-16 budget, independent of engine string compression.
  deleteImage(key);
  // A large image may still be displayed by its consumer without pinning it in the shared cache.
  if (bytes > MAX_MARKDOWN_IMAGE_CACHE_BYTES) return;
  while (cache.size >= MAX_CACHED_IMAGES || cacheBytes + bytes > MAX_MARKDOWN_IMAGE_CACHE_BYTES) {
    const oldest = cache.keys().next().value;
    if (oldest === undefined) break;
    deleteImage(oldest);
  }
  cache.set(key, { url, bytes, sessionID });
  cacheBytes += bytes;
}

function runImageLoad<T>(task: () => Promise<T>): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const run = () => {
      activeLoads++;
      Promise.resolve().then(task).then(resolve, reject).finally(() => {
        activeLoads--;
        queue.shift()?.();
      });
    };
    if (activeLoads < MAX_CONCURRENT_LOADS) run();
    else queue.push(run);
  });
}

export function loadMarkdownImage(path: string, directory: string, sessionID?: string): Promise<string | null> {
  const key = imageKey(path, directory, sessionID);
  const hit = getCachedMarkdownImage(path, directory, sessionID);
  if (hit) return Promise.resolve(hit);
  const pending = inflight.get(key);
  if (pending) return pending.promise;
  const entry = { promise: Promise.resolve<string | null>(null), sessionID, valid: true };
  const request = runImageLoad(async () => {
    if (!entry.valid) return null;
    return hoxiAPI.readFileContent(path, directory || undefined, sessionID)
    .then((res) => {
      if (res.type !== "binary" || res.encoding !== "base64") return null;
      if (typeof res.size === "number" && res.size > 8 * 1024 * 1024) return null;
      const mime = (res.mimeType || "").toLowerCase();
      if (!mime.startsWith("image/") || mime.startsWith("image/svg")) return null;
      const url = `data:${mime};base64,${res.content}`;
      if (entry.valid) cacheImage(key, url, sessionID);
      return url;
    }).catch(() => null);
  }).finally(() => { if (inflight.get(key) === entry) inflight.delete(key); });
  entry.promise = request;
  inflight.set(key, entry);
  return request;
}
