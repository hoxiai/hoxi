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
  isFocusedSession?: (sessionID: string) => boolean;
}

const BACKGROUND_FLUSH_MS = 180;

export function connectEventStream(handlers: SSEHandlers, directory?: string, wsBaseURL = ""): () => void {
  // Wails' macOS scheme handler has no http.Flusher; use the existing loopback server.
  const base = wsBaseURL.replace(/^ws:/, "http:").replace(/^wss:/, "https:").replace(/\/$/, "");
  const url = directory
    ? `/api/hoxi/event?directory=${encodeURIComponent(directory)}`
    : "/api/hoxi/event";
  const source = new EventSource(`${base}${url}`);

  const infoMap = new Map<string, MessageInfo>();
  const partMap = new Map<string, MessagePart>();
  const deltaMap = new Map<
    string,
    { sessionID: string; messageID: string; partID: string; field: string; delta: string }
  >();
  let rafID: number | null = null;
  let flushTimer: ReturnType<typeof setTimeout> | null = null;
  let bgFlushTimer: ReturnType<typeof setTimeout> | null = null;

  const isFocused = (sessionID: string): boolean =>
    handlers.isFocusedSession ? handlers.isFocusedSession(sessionID) : true;

  const scheduleFlush = (focused = true) => {
    if (focused) {
      if (bgFlushTimer !== null) {
        clearTimeout(bgFlushTimer);
        bgFlushTimer = null;
      }
      if (rafID === null) {
        rafID = requestAnimationFrame(flushFrame);
        // WebViews may suspend rAF while hidden; keep draining the stream in batches.
        flushTimer = setTimeout(flushFrame, 100);
      }
      return;
    }
    if (rafID === null && bgFlushTimer === null) {
      bgFlushTimer = setTimeout(flushFrame, BACKGROUND_FLUSH_MS);
    }
  };

  const flushFrame = () => {
    if (rafID !== null) cancelAnimationFrame(rafID);
    if (flushTimer !== null) clearTimeout(flushTimer);
    if (bgFlushTimer !== null) clearTimeout(bgFlushTimer);
    rafID = null;
    flushTimer = null;
    bgFlushTimer = null;
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
        scheduleFlush(isFocused(info.sessionID));
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
        scheduleFlush(isFocused(part.sessionID));
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
        scheduleFlush(isFocused(sessionID));
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

  return () => {
    if (rafID !== null) {
      cancelAnimationFrame(rafID);
    }
    if (flushTimer !== null) clearTimeout(flushTimer);
    if (bgFlushTimer !== null) clearTimeout(bgFlushTimer);
    infoMap.clear();
    partMap.clear();
    deltaMap.clear();
    source.close();
  };
}
