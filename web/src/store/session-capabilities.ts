// web/src/store/session-capabilities.ts
import { workspace } from "./workspace";
import { isRunnerSessionID } from "../services/api";

export function canRevertSession(sessionID?: string | null): boolean {
  if (!sessionID) return false;
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

export function canSteerSession(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (session?.capabilities?.steer !== undefined) return session.capabilities.steer;
  if (session?.runtime) return session.runtime === "opencode";
  return !isRunnerSessionID(sessionID);
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
