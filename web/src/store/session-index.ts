// web/src/store/session-index.ts
// Cross-project session metadata. `workspace.sessions` only holds the active
// project, but status and approval reconciliation must still know where a
// session lives; this index survives project switches and transcript eviction.
import type { SessionInfo } from "../services/sse-types";
import { setWorkspace, workspace, type SessionIndexEntry } from "./workspace-state";
import { findWorkspaceProfileByDir, normalizeFolderPath, stripWorktreePath } from "./workspace-folders";

function entryFrom(info: SessionInfo, previous?: SessionIndexEntry): SessionIndexEntry | null {
  const directory = (info.directory || "").trim() || previous?.directory;
  const projectID = (info.projectID || "").trim() || previous?.projectID;
  const parentID = info.parentID ?? previous?.parentID;
  const managed = info.managed ?? previous?.managed;
  const runtime = info.runtime ?? previous?.runtime;
  const updated = info.time?.updated || info.time?.created || previous?.updated || 0;
  if (!directory && !projectID && !parentID && managed === undefined && !runtime) return null;
  return { directory: directory || undefined, projectID: projectID || undefined, parentID, managed, runtime, updated };
}

/** Records metadata from a session list or stream event; never drops known fields. */
export function indexSession(info: SessionInfo | undefined | null): void {
  if (!info?.id) return;
  const entry = entryFrom(info, workspace.sessionIndex[info.id]);
  if (!entry) return;
  const prev = workspace.sessionIndex[info.id];
  if (prev &&
    prev.directory === entry.directory &&
    prev.projectID === entry.projectID &&
    prev.parentID === entry.parentID &&
    prev.managed === entry.managed &&
    prev.runtime === entry.runtime &&
    prev.updated === entry.updated) {
    return;
  }
  setWorkspace("sessionIndex", info.id, entry);
}

export function indexSessions(list: SessionInfo[] | undefined | null): void {
  if (!Array.isArray(list)) return;
  for (const info of list) indexSession(info);
}

export function indexedSession(sessionID: string | null | undefined): SessionIndexEntry | undefined {
  return sessionID ? workspace.sessionIndex[sessionID] : undefined;
}

/** Stable attribution wins; legacy sessions fall back to exact roots or Hoxi worktrees. */
export function sessionBelongsToProject(info: SessionInfo, directory: string, projectID?: string | null): boolean {
  const root = normalizeFolderPath(directory);
  if (!root) return false;
  const expectedID = projectID || findWorkspaceProfileByDir(root)?.id;
  const indexed = indexedSession(info.id);
  const ownerID = info.projectID || indexed?.projectID;
  if (ownerID && expectedID) return ownerID === expectedID;
  const sessionDir = normalizeFolderPath(info.directory || indexed?.directory ||
    workspace.worktreeBySession[info.id]?.directory);
  const profile = findWorkspaceProfileByDir(root);
  const roots = profile?.folders.map((f) => normalizeFolderPath(f.path)) || [root];
  if (sessionDir) return roots.includes(stripWorktreePath(sessionDir));
  const parentID = info.parentID || indexed?.parentID;
  if (parentID && parentID !== info.id) {
    const parent = workspace.sessions.find((s) => s.id === parentID) || indexedSession(parentID);
    if (parent?.projectID && expectedID) return parent.projectID === expectedID;
    if (parent?.directory) return roots.includes(stripWorktreePath(parent.directory));
  }
  // A directory-less legacy item is still accepted from its scoped list.
  return true;
}

export function filterProjectSessions(list: SessionInfo[], directory: string, projectID?: string | null): SessionInfo[] {
  const seen = new Set<string>();
  return list.filter((info) => {
    if (!info?.id || seen.has(info.id) || !sessionBelongsToProject(info, directory, projectID)) return false;
    seen.add(info.id);
    return true;
  });
}
