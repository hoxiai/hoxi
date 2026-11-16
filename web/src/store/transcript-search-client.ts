import type { SearchPattern, SearchRequest, SearchResponse, SearchTurn } from "./transcript-search-engine";

/** A search owns its worker so even a pathological RegExp can be terminated. */
export function createTranscriptSearchClient(timeoutMs = 1500) {
  let worker: Worker | undefined;
  let sequence = 0;
  const pending = new Map<number, { resolve: (response: SearchResponse) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> }>();
  const dispose = (reason = "cancelled") => {
    worker?.terminate(); worker = undefined;
    for (const request of pending.values()) { clearTimeout(request.timer); request.reject(new Error(reason)); }
    pending.clear();
  };
  function request(payload: Omit<Extract<SearchRequest, { turns: SearchTurn[] }>, "id"> |
    Omit<Extract<SearchRequest, { texts: string[] }>, "id">): Promise<SearchResponse> {
    if (!worker) {
      worker = new Worker(new URL("./transcript-search-worker.ts", import.meta.url), { type: "module" });
      worker.onmessage = (event: MessageEvent<SearchResponse>) => {
        const job = pending.get(event.data.id);
        if (!job) return;
        pending.delete(event.data.id); clearTimeout(job.timer); job.resolve(event.data);
      };
      worker.onerror = (event) => { event.preventDefault(); dispose("unavailable"); };
      worker.onmessageerror = () => dispose("unavailable");
    }
    const id = ++sequence;
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject, timer: setTimeout(() => dispose("timeout"), timeoutMs) });
      try { worker!.postMessage({ ...payload, id }); } catch { dispose("unavailable"); }
    });
  }
  return {
    async search(turns: SearchTurn[], pattern: SearchPattern) { return (await request({ turns, pattern })).matches ?? []; },
    async ranges(texts: string[], pattern: SearchPattern) { return (await request({ texts, pattern, limit: 300 })).ranges ?? []; },
    dispose,
  };
}
