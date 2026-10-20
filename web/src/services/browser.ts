import { getGatewayToken } from "./http";

export interface BrowserScope {
  sessionID: string;
  directory: string;
}

export interface BrowserState {
  url: string;
  title: string;
  tabs: Array<{ id: string; title: string; url: string }>;
  tabID: string;
  manual: boolean;
}

export interface BrowserAction {
  action: "navigate" | "back" | "forward" | "reload" | "click" | "type" | "key" |
    "scroll" | "tab_new" | "tab_select" | "tab_close" | "control" | "resize" | "close";
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
          !Number.isInteger(frame.width) || frame.width < 1 || frame.width > 4096 ||
          !Number.isInteger(frame.height) || frame.height < 1 || frame.height > 4096 || typeof frame.tabID !== "string") throw new Error("Invalid browser frame");
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

// Bounds belong to the image itself, not its letterboxed container.
export function browserPoint(bounds: { left: number; top: number; width: number; height: number },
  clientX: number, clientY: number, viewport: { width: number; height: number }): { x: number; y: number } | null {
  if (bounds.width <= 0 || bounds.height <= 0 || viewport.width <= 0 || viewport.height <= 0 ||
    !Number.isFinite(clientX + clientY + viewport.width + viewport.height)) return null;
  const x = (clientX - bounds.left) / bounds.width;
  const y = (clientY - bounds.top) / bounds.height;
  if (x < 0 || y < 0 || x > 1 || y > 1) return null;
  return { x: Math.min(viewport.width - 1, Math.floor(x * viewport.width)),
    y: Math.min(viewport.height - 1, Math.floor(y * viewport.height)) };
}

async function request(scope: BrowserScope, endpoint: "state" | "action" | "frame",
  signal: AbortSignal, action?: BrowserAction): Promise<Response> {
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
