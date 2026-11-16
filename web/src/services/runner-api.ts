// web/src/services/runner-api.ts
import { getGatewayToken, requestJSON, setGatewayToken, withDir } from "./http";
import type { PermissionRequest, SessionInfo, SessionRuntime } from "./sse-types";

export { getGatewayToken, requestJSON, setGatewayToken };

export const RUNNER_RUNTIMES = [
  "antigravity",
  "gemini-cli",
  "claude-code",
  "codebuddy",
  "qoder",
] as const;

export type RunnerRuntimeID = (typeof RUNNER_RUNTIMES)[number];

const RUNNER_SESSION_PREFIX_MAP: ReadonlyArray<[string, RunnerRuntimeID]> = [
  ["ses_agy_", "antigravity"],
  ["ses_gem_", "gemini-cli"],
  ["ses_cld_", "claude-code"],
  ["ses_cbd_", "codebuddy"],
  ["ses_qdr_", "qoder"],
];

const RUNNER_AUTH_MODE_SET = new Set<string>([
  "antigravity-runner",
  "gemini-runner",
  "claude-runner",
  "codebuddy-runner",
  "qoder-runner",
]);

const knownRunnerSessionIDs = new Set<string>();

export function inferRunnerRuntimeFromSessionID(
  sessionID?: string | null
): RunnerRuntimeID | null {
  if (!sessionID) return null;
  for (const [prefix, runtime] of RUNNER_SESSION_PREFIX_MAP) {
    if (sessionID.startsWith(prefix)) return runtime;
  }
  return null;
}

export function isRunnerAuthMode(authMode?: string | null): boolean {
  return Boolean(authMode && RUNNER_AUTH_MODE_SET.has(authMode));
}

export function isRunnerSessionID(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  if (inferRunnerRuntimeFromSessionID(sessionID) || sessionID.startsWith("ses_hoxi_")) {
    return true;
  }
  return knownRunnerSessionIDs.has(sessionID);
}

// Antigravity approval-hook requests are minted by the Go runner gate.
export function isRunnerPermissionID(requestID?: string | null): boolean {
  return !!requestID && requestID.startsWith("per_rnr_");
}

export function forgetRunnerSession(sessionID: string): void {
  knownRunnerSessionIDs.delete(sessionID);
}

export function registerRunnerSessions(sessions: SessionInfo[]): void {
  for (const s of sessions) {
    if (s && s.id && s.runtime && (s.managed || s.runtime !== "opencode")) {
      knownRunnerSessionIDs.add(s.id);
    }
  }
}

export function resolveRunnerRuntimeForProvider(
  providerID?: string | null,
  hasConfiguredProvider = false
): SessionRuntime {
  if (providerID === "antigravity") return "antigravity";
  if (providerID === "gemini-cli" && !hasConfiguredProvider) return "gemini-cli";
  if (providerID === "claude-code" && !hasConfiguredProvider) return "claude-code";
  if (providerID === "codebuddy" && !hasConfiguredProvider) return "codebuddy";
  if (providerID === "qoder" && !hasConfiguredProvider) return "qoder";
  return "opencode";
}

// `installed && !current` means hooks.json still points at an old Hoxi binary.
export interface AgyHookStatus {
  path: string;
  executable: string;
  installed: boolean;
  current: boolean;
  error?: string;
}

export interface RunnerModelItem {
  id: string;
  name: string;
  reasoning?: boolean;
  efforts?: string[];
  defaultEffort?: string;
  image?: boolean;
  free?: boolean;
  multiplier?: string;
  contextLimit?: number;
  outputLimit?: number;
  inputCost?: number;
  outputCost?: number;
  default?: boolean;
}

export interface RunnerModelsResponse {
  installed?: boolean;
  available?: boolean;
  binaryPath?: string;
  default?: string;
  models: RunnerModelItem[];
}

export const runnerAPI = {
  listModels(
    runtime: "antigravity" | "gemini-cli" | "claude-code" | "codebuddy" | "qoder",
    forceRefresh = false
  ): Promise<RunnerModelsResponse> {
    const qs = forceRefresh ? "?refresh=1" : "";
    return requestJSON(`/api/hoxi/runner/${runtime}/models${qs}`);
  },

  listPermissions(directory?: string): Promise<PermissionRequest[]> {
    return requestJSON(withDir("/api/hoxi/runner/permission", directory));
  },

  // `message` reaches the model as the deny reason, so rejecting can steer
  // the running agy turn without a follow-up prompt.
  replyPermission(requestID: string, reply: "once" | "always" | "reject", message?: string): Promise<void> {
    return requestJSON(`/api/hoxi/runner/permission/${encodeURIComponent(requestID)}/reply`, {
      method: "POST",
      body: JSON.stringify({ reply, message: message || undefined }),
    });
  },

  getAgyHookStatus(): Promise<AgyHookStatus> {
    return requestJSON("/api/hoxi/runner/antigravity/hook");
  },

  installAgyHook(): Promise<AgyHookStatus> {
    return requestJSON("/api/hoxi/runner/antigravity/hook", { method: "POST" });
  },

  uninstallAgyHook(): Promise<AgyHookStatus> {
    return requestJSON("/api/hoxi/runner/antigravity/hook", { method: "DELETE" });
  },
};

