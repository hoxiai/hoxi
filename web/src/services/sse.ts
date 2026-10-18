// web/src/services/sse.ts
import type { MessageInfo, MessagePart, OpenCodeEvent } from "./sse-types";

export interface StreamFrameBatch {
  infos: MessageInfo[];
  parts: MessagePart[];
  deltas: Array<{
    sessionID: string;
    messageID: string;
    partID: string;
    field: string;
    delta: string;
  }>;
}

export interface SSEHandlers {
  onEvent: (event: OpenCodeEvent, directory?: string) => void;
  onDeltaBatch: (
    batches: Array<{
      sessionID: string;
      messageID: string;
      partID: string;
      field: string;
      delta: string;
    }>
  ) => void;
  onFrameBatch?: (batch: StreamFrameBatch) => void;
  onConnectionChange?: (connected: boolean) => void;
}

export function connectEventStream(handlers: SSEHandlers, directory?: string): () => void {
  const url = directory
    ? `/event?directory=${encodeURIComponent(directory)}`
    : "/global/event";
  const source = new EventSource(url);
  const runnerSource = new EventSource("/api/hoxi/runner/event");

  const infoMap = new Map<string, MessageInfo>();
  const partMap = new Map<string, MessagePart>();
  const deltaMap = new Map<
    string,
    { sessionID: string; messageID: string; partID: string; field: string; delta: string }
  >();
  let rafID: number | null = null;
  let flushTimer: ReturnType<typeof setTimeout> | null = null;

  const scheduleFlush = () => {
    if (rafID === null) {
      rafID = requestAnimationFrame(flushFrame);
      // WebViews may suspend rAF while hidden; keep draining the stream in batches.
      flushTimer = setTimeout(flushFrame, 100);
    }
  };

  const flushFrame = () => {
    if (rafID !== null) cancelAnimationFrame(rafID);
    if (flushTimer !== null) clearTimeout(flushTimer);
    rafID = null;
    flushTimer = null;
    if (infoMap.size === 0 && partMap.size === 0 && deltaMap.size === 0) return;

    const infos = Array.from(infoMap.values());
    const parts = Array.from(partMap.values());
    const deltas = Array.from(deltaMap.values());
    infoMap.clear();
    partMap.clear();
    deltaMap.clear();

    if (handlers.onFrameBatch) {
      handlers.onFrameBatch({ infos, parts, deltas });
    } else {
      for (const info of infos) {
        handlers.onEvent({ type: "message.updated", properties: { info } });
      }
      for (const part of parts) {
        handlers.onEvent({ type: "message.part.updated", properties: { part } });
      }
      if (deltas.length > 0) {
        handlers.onDeltaBatch(deltas);
      }
    }
  };

  source.onopen = () => {
    handlers.onConnectionChange?.(true);
  };

  source.onerror = () => {
    handlers.onConnectionChange?.(false);
  };

  const handleMessage = (msg: MessageEvent, streamDirectory?: string) => {
    if (!msg.data) return;
    try {
      const parsed = JSON.parse(msg.data);
      // `/global/event` wraps events in `{ directory, payload: { type, properties } }`
      const wrapped = parsed?.payload && typeof parsed.payload.type === "string";
      const eventDirectory = wrapped
        ? (typeof parsed.directory === "string" ? parsed.directory : undefined)
        : streamDirectory;
      const evt = (wrapped
        ? parsed.payload
        : parsed) as OpenCodeEvent;
      if (!evt || typeof evt.type !== "string") return;

      if (evt.type === "message.updated") {
        const info = evt.properties.info;
        infoMap.set(`${info.sessionID}:${info.id}`, info);
        scheduleFlush();
        return;
      }

      if (evt.type === "message.part.updated") {
        const part = evt.properties.part;
        partMap.set(`${part.sessionID}:${part.messageID}:${part.id}`, part);
        // A full snapshot supersedes earlier deltas, including when text is reset.
        if (
          (part.type === "text" || part.type === "reasoning") &&
          typeof part.text === "string"
        ) {
          deltaMap.delete(`${part.sessionID}:${part.messageID}:${part.id}:text`);
        }
        scheduleFlush();
        return;
      }

      if (evt.type === "message.part.delta") {
        const { sessionID, messageID, partID, field, delta } = evt.properties;
        const key = `${sessionID}:${messageID}:${partID}:${field}`;
        const existing = deltaMap.get(key);
        if (existing) {
          existing.delta += delta;
        } else {
          deltaMap.set(key, { sessionID, messageID, partID, field, delta });
        }
        scheduleFlush();
        return;
      }

      // Preserve stream ordering before status/removal events observe the store.
      flushFrame();
      handlers.onEvent(evt, eventDirectory);
    } catch {
      // Ignore malformed keepalive frames
    }
  };

  source.onmessage = (msg) => handleMessage(msg, directory);
  runnerSource.onmessage = (msg) => handleMessage(msg);

  return () => {
    if (rafID !== null) {
      cancelAnimationFrame(rafID);
    }
    if (flushTimer !== null) clearTimeout(flushTimer);
    infoMap.clear();
    partMap.clear();
    deltaMap.clear();
    source.close();
    runnerSource.close();
  };
}
