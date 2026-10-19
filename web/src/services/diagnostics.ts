import { requestJSON } from "./runner-api";

export type DiagnosticKind = "system" | "request";
export type DiagnosticLevel = "ERROR" | "WARN" | "INFO";

export interface DiagnosticEntry {
  id: string;
  time: string;
  kind: DiagnosticKind;
  level: DiagnosticLevel;
  source: string;
  message: string;
  sessionID?: string;
  providerID?: string;
  modelID?: string;
  status?: number;
  exitCode?: number;
  durationMs?: number;
}

export interface DiagnosticsFilters {
  kind: DiagnosticKind | "";
  level: DiagnosticLevel | "";
  sessionID: string;
  providerID: string;
  q: string;
}

export interface DiagnosticsResponse {
  entries: DiagnosticEntry[];
  directory: string;
  error?: string;
}

const BASE = "/api/hoxi/diagnostics";

export const diagnosticsAPI = {
  list(filters: DiagnosticsFilters): Promise<DiagnosticsResponse> {
    const query = new URLSearchParams({ limit: "200" });
    for (const [key, value] of Object.entries(filters)) {
      if (value.trim()) query.set(key, value.trim());
    }
    return requestJSON(`${BASE}?${query}`);
  },
  async export(): Promise<Blob> {
    const response = await fetch(`${BASE}/export`);
    if (!response.ok) throw new Error(`HTTP ${response.status}: ${await response.text()}`);
    return response.blob();
  },
  openDirectory(): Promise<{ error?: string } | undefined> {
    return requestJSON(`${BASE}/open-directory`, { method: "POST" });
  },
};
