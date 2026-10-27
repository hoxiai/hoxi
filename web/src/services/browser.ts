import { getGatewayToken } from "./http";

export interface BrowserScope {
  sessionID: string;
  directory: string;
}

export interface BrowserState {
  mode?: "chrome";
  connected?: boolean;
  pairing?: boolean;
  url: string;
  title: string;
  tabs: Array<{ id: string; title: string; url: string }>;
  tabID: string;
  manual: boolean;
  // The scope's Chrome was stopped after idleMinutes without use.
  idleClosed?: boolean;
  idleMinutes?: number;
}

export type BrowserPointerButton = "left" | "middle" | "right";

export interface BrowserAction {
  action: "navigate" | "back" | "forward" | "reload" | "click" | "type" | "key" |
    "scroll" | "wheel" | "zoom" | "pointer" | "tab_new" | "tab_select" | "tab_close" | "control" | "resize" | "close";
  url?: string;
  selector?: string;
  text?: string;
  key?: string;
  x?: number;
  y?: number;
  deltaX?: number;
  deltaY?: number;
  tabID?: string;
  manual?: boolean;
  width?: number;
  height?: number;
  deviceScaleFactor?: number;
  scaleFactor?: number;
  // UI-only pointer input; hover is a move with no button held.
  phase?: "down" | "move" | "up" | "cancel";
  button?: BrowserPointerButton;
  clickCount?: number;
  modifiers?: number;
}

// WebKit reports native trackpad pinches as gesture events; Chromium uses
// Ctrl+wheel. Both listeners must be non-passive to keep zoom inside the page.
export function observeBrowserZoom(element: HTMLElement, zoom: (scale: number, clientX: number, clientY: number) => void): () => void {
  type PinchEvent = Event & { scale: number; clientX: number; clientY: number };
  let previous = 1;
  const start = (raw: Event) => { raw.preventDefault(); previous = 1; };
  const change = (raw: Event) => {
    raw.preventDefault();
    const event = raw as PinchEvent;
    if (!Number.isFinite(event.scale) || event.scale <= 0) return;
    zoom(Math.max(.25, Math.min(4, event.scale / previous)), event.clientX, event.clientY);
    previous = event.scale;
  };
  const end = (raw: Event) => { raw.preventDefault(); previous = 1; };
  const wheel = (event: WheelEvent) => {
    if (!event.ctrlKey) return;
    event.preventDefault();
    event.stopPropagation();
    zoom(Math.exp(Math.max(-1, Math.min(1, -event.deltaY * .01))), event.clientX, event.clientY);
  };
  element.addEventListener("gesturestart", start, { passive: false });
  element.addEventListener("gesturechange", change, { passive: false });
  element.addEventListener("gestureend", end, { passive: false });
  element.addEventListener("wheel", wheel, { passive: false });
  return () => {
    element.removeEventListener("gesturestart", start);
    element.removeEventListener("gesturechange", change);
    element.removeEventListener("gestureend", end);
    element.removeEventListener("wheel", wheel);
  };
}

export function observeBrowserInputCancellation(cancel: () => void): () => void {
  const hidden = () => { if (document.hidden) cancel(); };
  window.addEventListener("blur", cancel);
  document.addEventListener("visibilitychange", hidden);
  return () => {
    window.removeEventListener("blur", cancel);
    document.removeEventListener("visibilitychange", hidden);
  };
}

export function browserURL(endpoint: "state" | "action" | "frame" | "stream", scope: BrowserScope): string {
  const query = new URLSearchParams({ sessionID: scope.sessionID, directory: scope.directory });
  return `/api/hoxi/browser/${endpoint}?${query}`;
}

export interface BrowserFrame {
  image: string;
  width: number;
  height: number;
  tabID: string;
}

export function browserStreamURL(scope: BrowserScope, base: string): string {
  return `${base.replace(/\/$/, "")}${browserURL("stream", scope)}`;
}

// Display ACKs bound network buffering; CDP keeps only the latest pending frame.
export function connectBrowserStream(scope: BrowserScope, base: string,
  display: (frame: BrowserFrame) => Promise<void>, onError: (error: string) => void): () => void {
  let disposed = false;
  let socket: WebSocket | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  function connect() {
    if (disposed) return;
    const current = new WebSocket(browserStreamURL(scope, base));
    socket = current;
    let displaying = false;
    current.onmessage = async event => {
      if (disposed || socket !== current) return;
      try {
        if (displaying || typeof event.data !== "string" || event.data.length > (16 << 20) + 1024) throw new Error("Invalid browser frame");
        const frame = JSON.parse(event.data) as BrowserFrame;
        if (typeof frame.image !== "string" || !/^[A-Za-z0-9+/=]+$/.test(frame.image) ||
          !Number.isInteger(frame.width) || frame.width < 1 || frame.width > 16384 ||
          !Number.isInteger(frame.height) || frame.height < 1 || frame.height > 16384 ||
          frame.width * frame.height > 8388608 || typeof frame.tabID !== "string") throw new Error("Invalid browser frame");
        displaying = true;
        await display(frame);
        if (!disposed && socket === current && current.readyState === WebSocket.OPEN) current.send("ack");
        displaying = false;
      } catch (error) {
        if (!disposed) onError(String(error));
        current.close();
      }
    };
    current.onerror = () => { if (!disposed) onError("Browser stream disconnected"); };
    current.onclose = () => {
      if (!disposed && socket === current) timer = setTimeout(connect, 500);
    };
  }
  connect();
  return () => {
    disposed = true;
    clearTimeout(timer);
    socket?.close();
  };
}

// Bounds belong to the image itself, not its letterboxed container. With
// clamp, a captured drag outside the image maps to the nearest viewport edge.
export function browserPoint(bounds: { left: number; top: number; width: number; height: number },
  clientX: number, clientY: number, viewport: { width: number; height: number }, clamp = false): { x: number; y: number } | null {
  if (bounds.width <= 0 || bounds.height <= 0 || viewport.width <= 0 || viewport.height <= 0 ||
    !Number.isFinite(clientX + clientY + viewport.width + viewport.height)) return null;
  let x = (clientX - bounds.left) / bounds.width;
  let y = (clientY - bounds.top) / bounds.height;
  if (clamp) {
    x = Math.min(1, Math.max(0, x));
    y = Math.min(1, Math.max(0, y));
  }
  if (x < 0 || y < 0 || x > 1 || y > 1) return null;
  return { x: Math.min(viewport.width - 1, Math.floor(x * viewport.width)),
    y: Math.min(viewport.height - 1, Math.floor(y * viewport.height)) };
}

async function request(scope: BrowserScope, endpoint: "state" | "action" | "frame",
  signal: AbortSignal, action?: BrowserAction | { action: "pair" }): Promise<Response> {
  const token = getGatewayToken();
  const response = await fetch(browserURL(endpoint, scope), {
    method: action ? "POST" : "GET",
    headers: {
      "Content-Type": "application/json",
      "X-Hoxi-Browser": "true",
      ...(token ? { "X-Hoxi-Token": token } : {}),
    },
    body: action ? JSON.stringify(action) : undefined,
    cache: "no-store",
    signal,
  });
  if (!response.ok && !(endpoint === "frame" && response.status === 404)) {
    throw new Error(`HTTP ${response.status}: ${await response.text()}`);
  }
  return response;
}

export const browserAPI = {
  async pair(scope: BrowserScope, signal: AbortSignal): Promise<{ code: string; expiresIn: number }> {
    return (await request(scope, "action", signal, { action: "pair" })).json();
  },
  async state(scope: BrowserScope, signal: AbortSignal): Promise<BrowserState> {
    return (await request(scope, "state", signal)).json();
  },
  async action(scope: BrowserScope, action: BrowserAction, signal: AbortSignal): Promise<BrowserState> {
    return (await request(scope, "action", signal, action)).json();
  },
  async frame(scope: BrowserScope, signal: AbortSignal): Promise<Blob | null> {
    const response = await request(scope, "frame", signal);
    return response.status === 404 ? null : response.blob();
  },
};
