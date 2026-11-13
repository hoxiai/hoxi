import {
  getCachedHighlight, isWithinHighlightLimits,
  type HighlightedCode, type HighlightWorkerRequest, type HighlightWorkerResponse,
} from "./syntax-highlight";

const MAX_PENDING = 64;
const TIMEOUT_MS = 8000;
let worker: Worker | undefined;
let clients = 0;
let nextClientID = 0;
let nextRequestID = 0;
let disabledUntil = 0;
const pending = new Map<number, {
  requestID: number;
  resolve: (result: HighlightedCode | undefined) => void;
  timer: ReturnType<typeof setTimeout>;
}>();

function settle(clientID: number, result?: HighlightedCode) {
  const request = pending.get(clientID);
  if (!request) return;
  pending.delete(clientID);
  clearTimeout(request.timer);
  request.resolve(result);
}

function stopWorker(failed = false) {
  worker?.terminate();
  worker = undefined;
  for (const id of pending.keys()) settle(id);
  if (failed) disabledUntil = Date.now()+2000;
}

function send(message: HighlightWorkerRequest): boolean {
  try { worker?.postMessage(message); return Boolean(worker); }
  catch { stopWorker(true); return false; }
}

function ensureWorker(): boolean {
  if (worker) return true;
  if (typeof Worker === "undefined" || Date.now() < disabledUntil) return false;
  try {
    const created = new Worker(new URL("./syntax-highlight-worker.ts", import.meta.url), { type: "module" });
    worker = created;
    created.onmessage = (event: MessageEvent<HighlightWorkerResponse>) => {
      if (worker !== created) return;
      const request = pending.get(event.data.clientID);
      if (request?.requestID === event.data.requestID) settle(event.data.clientID, event.data.result);
    };
    created.onerror = (event) => { event.preventDefault(); if (worker === created) stopWorker(true); };
    return true;
  } catch { stopWorker(true); return false; }
}

/** A shared, bounded UI-computation worker; each mounted card owns one client. */
export function createSyntaxHighlightClient() {
  const clientID = ++nextClientID;
  let counted = false;
  let disposed = false;
  return {
    highlight(code: string, language: string, theme: "dark" | "light"): Promise<HighlightedCode | undefined> {
      if (disposed) return Promise.resolve(undefined);
      if (pending.has(clientID)) { settle(clientID); send({ type: "cancel", clientID }); }
      const lang = language.trim().toLowerCase();
      if (!isWithinHighlightLimits(code, lang)) return Promise.resolve(undefined);
      const hit = getCachedHighlight(code, lang, theme);
      if (hit) return Promise.resolve(hit);
      if (!ensureWorker()) return Promise.resolve(undefined);
      if (!counted) { clients++; counted = true; }
      if (pending.size >= MAX_PENDING) {
        const oldest = pending.keys().next().value!;
        settle(oldest);
        send({ type: "cancel", clientID: oldest });
      }
      const requestID = ++nextRequestID;
      return new Promise((resolve) => {
        const timer = setTimeout(() => { if (pending.get(clientID)?.requestID === requestID) stopWorker(true); }, TIMEOUT_MS);
        pending.set(clientID, { requestID, timer, resolve });
        if (!send({ type: "highlight", clientID, requestID, code, language: lang, theme })) settle(clientID);
      });
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      settle(clientID);
      send({ type: "cancel", clientID });
      if (counted && --clients === 0) stopWorker();
    },
  };
}
