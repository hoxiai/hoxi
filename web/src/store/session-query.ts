// web/src/store/session-query.ts
// Read-only session lookups (directory, busy/archived/deleted flags, runtime).
// Leaf module (workspace-state + auto-models only) so every domain module can use
// it without creating import cycles.
import {
  inferRunnerRuntimeFromSessionID,
  isRunnerSessionID,
  resolveRunnerRuntimeForProvider,
} from "../services/api";
import type { SessionInfo, SessionRuntime } from "../services/sse-types";
import { AUTO_PROVIDER_ID, peekAutoModelTarget } from "./auto-models";
import { workspace } from "./workspace-state";

export function isSessionArchived(session: SessionInfo): boolean {
  if (session.time?.archived && session.time.archived > 0) return true;
  return workspace.archivedSessionIDs.includes(session.id);
}

const deletedSessionIDs = new Set<string>();

export function isSessionDeleted(id: string): boolean {
  return deletedSessionIDs.has(id);
}

export function markSessionDeleted(id: string): void {
  deletedSessionIDs.add(id);
}

export function activeSession(): SessionInfo | undefined {
  return workspace.sessions.find((s) => s.id === workspace.activeSessionID);
}

export function isManagedSession(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (session?.managed !== undefined) return Boolean(session.managed);
  if (session?.runtime && session.runtime !== "opencode") return true;
  return isRunnerSessionID(sessionID);
}

export function sessionDirectory(sessionID: string | null): string | undefined {
  if (!sessionID) return undefined;
  const meta = workspace.worktreeBySession[sessionID];
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (meta?.directory || session?.directory) {
    return meta?.directory || session?.directory;
  }
  if (session?.parentID) {
    const parentMeta = workspace.worktreeBySession[session.parentID];
    const parentSession = workspace.sessions.find((s) => s.id === session.parentID);
    if (parentMeta?.directory || parentSession?.directory) {
      return parentMeta?.directory || parentSession?.directory;
    }
  }
  return workspace.rootDir || undefined;
}

export function activeDirectory(): string | undefined {
  return sessionDirectory(workspace.activeSessionID);
}

export function isSessionBusy(sessionID: string | null): boolean {
  if (!sessionID) return false;
  const st = workspace.statusBySession[sessionID]?.type;
  return st === "busy" || st === "retry";
}

export function sessionsWithPendingPermission(): Set<string> {
  const set = new Set<string>();
  for (const p of workspace.pendingPermissions) set.add(p.sessionID);
  for (const q of workspace.pendingQuestions) set.add(q.sessionID);
  return set;
}

export function sessionRuntimeForID(sessionID?: string | null): SessionRuntime {
  if (!sessionID) {
    const rawModel = workspace.defaultAI.model;
    const resolved =
      rawModel?.providerID === AUTO_PROVIDER_ID
        ? peekAutoModelTarget(rawModel.modelID)
        : rawModel;
    const draftProv = resolved?.providerID;
    return resolveRunnerRuntimeForProvider(draftProv, Boolean(workspace.hoxiProviders?.[draftProv || ""]));
  }
  const found = workspace.sessions.find((s) => s.id === sessionID);
  if (found?.runtime) return found.runtime;
  return inferRunnerRuntimeFromSessionID(sessionID) || "opencode";
}
