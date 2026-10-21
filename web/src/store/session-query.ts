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
import { indexedSession } from "./session-index";
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
  const indexed = indexedSession(sessionID);
  if (indexed?.managed !== undefined) return Boolean(indexed.managed);
  if (indexed?.runtime && indexed.runtime !== "opencode") return true;
  return isRunnerSessionID(sessionID);
}

// Directory lookups must not fall back to the active project for a session that
// belongs elsewhere: a wrong directory sends writes, diffs and approvals into
// the wrong workspace. Only a session we know nothing about uses the root.
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
  const indexed = indexedSession(sessionID);
  if (indexed?.directory) return indexed.directory;
  const parentID = session?.parentID ?? indexed?.parentID;
  if (parentID) {
    const parentIndexed = indexedSession(parentID);
    if (parentIndexed?.directory) return parentIndexed.directory;
    const parentSession = workspace.sessions.find((s) => s.id === parentID);
    if (parentSession?.directory) return parentSession.directory;
  }
  const known = session || indexed;
  if (!known) return workspace.rootDir || undefined;
  // A known session without a directory may still be rooted in the active
  // project (unadopted OpenCode sessions often omit it). A session attributed
  // to a different project, or seen only in the global busy snapshot while not
  // present in the active project's session list, must never inherit the active root.
  const projectID = session?.projectID || indexed?.projectID;
  const activeProjectID = (workspace.activeProjectID || "").trim();
  if (projectID && activeProjectID && projectID !== activeProjectID) return undefined;
  if (!session && indexed && !indexed.directory && !indexed.projectID) return undefined;
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
