// web/src/services/sse.ts
import type { OpenCodeEvent } from "./sse-types";

export interface SSEHandlers {
  onEvent: (event: OpenCodeEvent) => void;
  onDeltaBatch: (batches: Array<{ sessionID: string; messageID: string; partID: string; field: string; delta: string }>) => void;
  onConnectionChange?: (connected: boolean) => void;
}

export function connectEventStream(handlers: SSEHandlers, directory?: string): () => void {
  const url = directory ? `/event?directory=${encodeURIComponent(directory)}` : "/event";
  const source = new EventSource(url);

  const deltaMap = new Map<
    string,
    { sessionID: string; messageID: string; partID: string; field: string; delta: string }
  >();
  let rafID: number | null = null;

  const flushDeltas = () => {
    rafID = null;
    if (deltaMap.size === 0) return;
    const batches = Array.from(deltaMap.values());
    deltaMap.clear();
    handlers.onDeltaBatch(batches);
  };

  source.onopen = () => {
    handlers.onConnectionChange?.(true);
  };

  source.onerror = () => {
    handlers.onConnectionChange?.(false);
  };

  source.onmessage = (msg) => {
    if (!msg.data) return;
    try {
      const evt = JSON.parse(msg.data) as OpenCodeEvent;
      if (evt.type === "message.part.delta") {
        const { sessionID, messageID, partID, field, delta } = evt.properties;
        const key = `${sessionID}:${messageID}:${partID}:${field}`;
        const existing = deltaMap.get(key);
        if (existing) {
          existing.delta += delta;
        } else {
          deltaMap.set(key, { sessionID, messageID, partID, field, delta });
        }
        if (rafID === null) {
          rafID = requestAnimationFrame(flushDeltas);
        }
        return;
      }
      handlers.onEvent(evt);
    } catch {
      // Ignore malformed keepalive frames
    }
  };

  return () => {
    if (rafID !== null) {
      cancelAnimationFrame(rafID);
    }
    source.close();
  };
}
