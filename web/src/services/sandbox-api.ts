// web/src/services/sandbox-api.ts
import { requestJSON } from "./http";

export interface SandboxSessionState {
  sessionID: string;
  enabled: boolean;
  available: boolean;
  network?: "allow" | "deny";
  reason?: string;
}

export async function fetchSandboxSession(sessionID: string): Promise<SandboxSessionState> {
  return requestJSON<SandboxSessionState>(
    `/api/hoxi/sandbox/session?sessionID=${encodeURIComponent(sessionID)}`
  );
}

export async function updateSandboxSession(input: {
  sessionID: string;
  directory?: string;
  enabled: boolean;
  network?: "allow" | "deny";
}): Promise<SandboxSessionState> {
  return requestJSON<SandboxSessionState>("/api/hoxi/sandbox/session", {
    method: "POST",
    body: JSON.stringify(input),
  });
}
