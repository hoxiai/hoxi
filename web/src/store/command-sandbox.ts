// web/src/store/command-sandbox.ts
import {
  fetchSandboxSession as apiFetchSandboxSession,
  updateSandboxSession as apiUpdateSandboxSession,
  type SandboxSessionState,
} from "../services/sandbox-api";

export type { SandboxSessionState };

export function fetchSandboxSession(sessionID: string): Promise<SandboxSessionState> {
  return apiFetchSandboxSession(sessionID);
}

export function updateSandboxSession(input: {
  sessionID: string;
  directory?: string;
  enabled: boolean;
  network?: "allow" | "deny";
}): Promise<SandboxSessionState> {
  return apiUpdateSandboxSession(input);
}
