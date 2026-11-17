import { workspace } from "./workspace-state";
import {
  inferRunnerRuntimeFromSessionID,
  isRunnerSessionID,
} from "../services/api";
import { isSessionBusy, resolveSessionTargetRuntime } from "./session-query";

export function canRevertSession(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  if (isSessionBusy(sessionID)) return false;
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (session?.capabilities?.revert !== undefined) return session.capabilities.revert;
  return true;
}

export function canForkSession(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (session?.capabilities?.fork !== undefined) return session.capabilities.fork;
  return true;
}

export function canSteerSession(
  sessionID?: string | null,
  targetModel?: { providerID: string; modelID: string } | null
): boolean {
  if (!sessionID) return false;
  const session = workspace.sessions.find((s) => s.id === sessionID);
  const currentRuntime =
    session?.runtime ||
    inferRunnerRuntimeFromSessionID(sessionID) ||
    (isRunnerSessionID(sessionID) ? "" : "opencode");
  if (targetModel?.providerID) {
    const targetRuntime = resolveSessionTargetRuntime(sessionID, targetModel.providerID);
    if (currentRuntime && targetRuntime !== currentRuntime) return false;
    if (targetRuntime !== "opencode" && targetRuntime !== "native" && targetRuntime !== "antigravity") return false;
  }
  if (session?.capabilities?.steer !== undefined) return session.capabilities.steer;
  if (currentRuntime) return currentRuntime === "opencode" || currentRuntime === "native" || currentRuntime === "antigravity";
  return !isRunnerSessionID(sessionID);
}

/** OpenCode and Native Go Agent sessions can be compacted directly from Hoxi; external CLIs keep their own window. */
export function canCompactSession(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (session?.capabilities?.compact !== undefined) return session.capabilities.compact;
  const runtime = session?.runtime || inferRunnerRuntimeFromSessionID(sessionID);
  if (runtime) return runtime === "opencode" || runtime === "native";
  return true;
}

export function canApproveSession(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (session?.capabilities?.approval !== undefined) return session.capabilities.approval;
  if (session?.runtime === "codebuddy" || session?.runtime === "qoder") return false;
  return true;
}

export function canAttachToSession(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (session?.capabilities?.attachments !== undefined) return session.capabilities.attachments;
  if (session?.runtime === "codebuddy" || session?.runtime === "qoder") return false;
  return true;
}
