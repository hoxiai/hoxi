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
    "scroll" | "tab_new" | "tab_select" | "tab_close" | "control" | "close";
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
}

export function browserURL(endpoint: "state" | "action" | "frame", scope: BrowserScope): string {
  const query = new URLSearchParams({ sessionID: scope.sessionID, directory: scope.directory });
  return `/api/hoxi/browser/${endpoint}?${query}`;
}

// Bounds belong to the image itself, not its letterboxed container.
export function browserPoint(bounds: { left: number; top: number; width: number; height: number },
  clientX: number, clientY: number): { x: number; y: number } | null {
  if (bounds.width <= 0 || bounds.height <= 0 || !Number.isFinite(clientX + clientY)) return null;
  const x = (clientX - bounds.left) / bounds.width;
  const y = (clientY - bounds.top) / bounds.height;
  if (x < 0 || y < 0 || x > 1 || y > 1) return null;
  return { x: Math.min(1279, Math.floor(x * 1280)), y: Math.min(799, Math.floor(y * 800)) };
}

async function request(scope: BrowserScope, endpoint: "state" | "action" | "frame",
  signal: AbortSignal, action?: BrowserAction): Promise<Response> {
  const response = await fetch(browserURL(endpoint, scope), {
    method: action ? "POST" : "GET",
    headers: { "Content-Type": "application/json", "X-Hoxi-Browser": "true" },
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
