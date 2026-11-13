import { highlightCode, type HighlightWorkerRequest, type HighlightWorkerResponse } from "./syntax-highlight";

type Request = Extract<HighlightWorkerRequest, { type: "highlight" }>;
const scope = globalThis as unknown as {
  onmessage: ((event: MessageEvent<HighlightWorkerRequest>) => void) | null;
  postMessage(message: HighlightWorkerResponse): void;
};
const pending = new Map<number, Request>();
let running = false;
let current: Request | undefined;
let cancelled = false;

async function drain() {
  if (running) return;
  running = true;
  try {
    while (pending.size > 0) {
      const request = pending.values().next().value!;
      pending.delete(request.clientID);
      current = request;
      cancelled = false;
      const result = await highlightCode(request.code, request.language, request.theme);
      if (!cancelled && !pending.has(request.clientID)) {
        scope.postMessage({ clientID: request.clientID, requestID: request.requestID, result });
      }
      current = undefined;
    }
  } finally { running = false; }
}

scope.onmessage = (event) => {
  const request = event.data;
  if (request.type === "cancel") {
    pending.delete(request.clientID);
    if (current?.clientID === request.clientID) cancelled = true;
    return;
  }
  if (pending.size >= 64 && !pending.has(request.clientID)) {
    const oldest = pending.values().next().value!;
    pending.delete(oldest.clientID);
    scope.postMessage({ clientID: oldest.clientID, requestID: oldest.requestID });
  }
  pending.set(request.clientID, request);
  void drain();
};
