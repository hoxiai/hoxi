import { browserURL, type BrowserEndpoint, type BrowserScope } from "./browser";
import { getGatewayToken } from "./http";

export interface BrowserDownload {
  id: string; filename: string; status: "inProgress" | "completed" | "canceled";
  received: number; total: number; saved?: string; canOpen: boolean; error?: string;
}
export interface BrowserWorkflows {
  upload?: { id: string; multiple: boolean };
  dialog?: { id: string; type: string; message: string; default?: string };
  downloads: BrowserDownload[];
  error?: string;
}
export interface BrowserDiagnostic { id: number; kind: "console" | "request"; level: string; message: string; url?: string; time: number }

async function request<T>(scope: BrowserScope, endpoint: BrowserEndpoint, signal: AbortSignal, body?: object | FormData, tabID?: string): Promise<T> {
  const token = getGatewayToken();
  const multipart = body instanceof FormData;
  const url = browserURL(endpoint, scope) + (tabID ? `&tabID=${encodeURIComponent(tabID)}` : "");
  const response = await fetch(url, { method: body ? "POST" : "GET", signal, cache: "no-store",
    headers: { "X-Hoxi-Browser": "true", ...(multipart ? {} : { "Content-Type": "application/json" }), ...(token ? { "X-Hoxi-Token": token } : {}) },
    body: body ? multipart ? body as FormData : JSON.stringify(body) : undefined });
  if (!response.ok) throw new Error(`HTTP ${response.status}: ${await response.text()}`);
  return response.json();
}

export const browserWorkflowAPI = {
  state: (scope: BrowserScope, tab: string, signal: AbortSignal) => request<BrowserWorkflows>(scope, "workflows", signal, undefined, tab),
  upload(scope: BrowserScope, id: string, files: File[], signal: AbortSignal) {
    const body = new FormData(); body.append("id", id);
    for (const file of files) body.append("files", file, file.name);
    return request<BrowserWorkflows>(scope, "upload", signal, body);
  },
  answer: (scope: BrowserScope, tab: string, id: string, accept: boolean, text: string, signal: AbortSignal) =>
    request<BrowserWorkflows>(scope, "workflows", signal, { id, accept, text }, tab),
  download: (scope: BrowserScope, action: "save" | "open" | "reveal" | "cancel", id: string, title: string, signal: AbortSignal) =>
    request<BrowserWorkflows | { canceled: true }>(scope, `download-${action}`, signal, { id, title }),
  diagnostics: (scope: BrowserScope, signal: AbortSignal) => request<{ diagnostics: BrowserDiagnostic[] }>(scope, "diagnostics", signal),
  clearDiagnostics: (scope: BrowserScope, signal: AbortSignal) => request<{ diagnostics: BrowserDiagnostic[] }>(scope, "diagnostics", signal, {}),
};
