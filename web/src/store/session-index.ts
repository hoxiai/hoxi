// web/src/store/session-index.ts
// Cross-project session metadata. `workspace.sessions` only holds the active
// project, but status and approval reconciliation must still know where a
// session lives; this index survives project switches and transcript eviction.
import type { SessionInfo } from "../services/sse-types";
import { setWorkspace, workspace, type SessionIndexEntry } from "./workspace-state";
import {
  bumpSessionSurfaceRevision,
  findWorkspaceProfileByDir,
  findWorkspaceProfileById,
  isWorkSurfacePath,
  normalizeFolderPath,
  recordWorkWorkspaceDir,
  stripWorktreePath,
} from "./workspace-folders";

function entryFrom(info: SessionInfo, previous?: SessionIndexEntry): SessionIndexEntry | null {
  const directory = (info.directory || "").trim() || previous?.directory;
  const projectID = (info.projectID || "").trim() || previous?.projectID;
  const parentID = info.parentID ?? previous?.parentID;
  const surface = info.surface ?? previous?.surface;
  const managed = info.managed ?? previous?.managed;
  const runtime = info.runtime ?? previous?.runtime;
  const updated = info.time?.updated || info.time?.created || previous?.updated || 0;
  if (!directory && !projectID && !parentID && !surface && managed === undefined && !runtime) return null;
  return { directory: directory || undefined, projectID: projectID || undefined, parentID, surface, managed, runtime, updated };
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
    prev.surface === entry.surface &&
    prev.managed === entry.managed &&
    prev.runtime === entry.runtime &&
    prev.updated === entry.updated) {
    return;
  }
  const surfaceChanged =
    prev?.surface !== entry.surface ||
    Boolean(
      (entry.surface || prev?.surface) &&
        stripWorktreePath(prev?.directory) !== stripWorktreePath(entry.directory)
    );
  setWorkspace("sessionIndex", info.id, entry);
  if (surfaceChanged) {
    bumpSessionSurfaceRevision();
  }
  if (entry.surface === "work" && entry.directory) {
    const owner = findWorkspaceProfileByDir(entry.directory);
    if (owner?.surface !== "code") {
      recordWorkWorkspaceDir(entry.directory);
    }
  }
}

export function indexSessions(list: SessionInfo[] | undefined | null): void {
  if (!Array.isArray(list)) return;
  for (const info of list) indexSession(info);
}

export function indexedSession(sessionID: string | null | undefined): SessionIndexEntry | undefined {
  return sessionID ? workspace.sessionIndex[sessionID] : undefined;
}

export function resolveSessionSurface(
  info: { id?: string; surface?: SessionInfo["surface"]; directory?: string } | null | undefined
): "code" | "work" {
  if (info?.surface === "work" || info?.surface === "code") return info.surface;
  const indexed = info?.id ? indexedSession(info.id) : undefined;
  if (indexed?.surface === "work" || indexed?.surface === "code") return indexed.surface;
  const dir =
    info?.directory ||
    indexed?.directory ||
    (info?.id ? workspace.worktreeBySession[info.id]?.directory : "");
  return isWorkSurfacePath(dir) ? "work" : "code";
}

interface ProjectOwnershipContext {
  root: string;
  rootIsWork: boolean;
  expectedSurface: "code" | "work";
  expectedID: string | undefined;
  roots: string[];
}

function buildProjectOwnershipContext(
  directory: string,
  projectID?: string | null
): ProjectOwnershipContext | null {
  const root = normalizeFolderPath(directory);
  if (!root) return null;
  const rootIsWork = isWorkSurfacePath(root);
  const profile = findWorkspaceProfileByDir(root);
  const expectedID = projectID || profile?.id;
  const roots = profile?.folders.map((f) => normalizeFolderPath(f.path)) || [root];
  return {
    root,
    rootIsWork,
    expectedSurface: rootIsWork ? "work" : "code",
    expectedID,
    roots,
  };
}

function sessionBelongsToProjectWithContext(
  info: SessionInfo,
  ctx: ProjectOwnershipContext,
  allowRemovedProjectDirectoryFallback: boolean
): boolean {
  const indexed = indexedSession(info.id);
  const explicitSurface = info.surface ?? indexed?.surface;
  const sessionDir = normalizeFolderPath(
    info.directory || indexed?.directory || workspace.worktreeBySession[info.id]?.directory
  );
  const strippedSessionDir = sessionDir ? stripWorktreePath(sessionDir) : "";
  const sessionDirIsWork = sessionDir
    ? strippedSessionDir === ctx.root
      ? ctx.rootIsWork
      : isWorkSurfacePath(strippedSessionDir)
    : false;
  const sessionSurface: "code" | "work" =
    explicitSurface === "work" || explicitSurface === "code"
      ? explicitSurface
      : sessionDirIsWork
        ? "work"
        : "code";
  if (sessionSurface !== ctx.expectedSurface) {
    return false;
  }
  if (sessionDir && sessionDirIsWork !== ctx.rootIsWork) {
    return false;
  }
  const ownerID = info.projectID || indexed?.projectID;
  // Before boot resolves the project there is no expected ID to compare
  // against; ownership then falls back to the directory checks below.
  if (ownerID && ctx.expectedID) {
    if (ownerID === ctx.expectedID) return true;
    const ownerIsLive = Boolean(findWorkspaceProfileById(ownerID));
    // A backend-scoped session list can contain an old owner ID only after
    // ListProject has confirmed that ID is no longer live. Global/background
    // events do not carry that guarantee and must stay strictly attributed.
    if (!allowRemovedProjectDirectoryFallback || ownerIsLive) return false;
  }
  if (sessionDir) return ctx.roots.includes(strippedSessionDir);
  const parentID = info.parentID || indexed?.parentID;
  if (parentID && parentID !== info.id) {
    const parent = workspace.sessions.find((s) => s.id === parentID) || indexedSession(parentID);
    if (parent?.projectID && ctx.expectedID) return parent.projectID === ctx.expectedID;
    if (parent?.directory) return ctx.roots.includes(stripWorktreePath(parent.directory));
  }
  // A directory-less legacy item is still accepted from its scoped list.
  return !ownerID;
}

/** Stable attribution wins; legacy sessions fall back to exact roots or Hoxi worktrees. */
export function sessionBelongsToProject(
  info: SessionInfo,
  directory: string,
  projectID?: string | null,
  allowRemovedProjectDirectoryFallback = false
): boolean {
  const ctx = buildProjectOwnershipContext(directory, projectID);
  if (!ctx) return false;
  return sessionBelongsToProjectWithContext(info, ctx, allowRemovedProjectDirectoryFallback);
}

export function filterProjectSessions(list: SessionInfo[], directory: string, projectID?: string | null): SessionInfo[] {
  const ctx = buildProjectOwnershipContext(directory, projectID);
  if (!ctx) return [];
  const seen = new Set<string>();
  return list.filter((info) => {
    if (!info?.id || seen.has(info.id) ||
      !sessionBelongsToProjectWithContext(info, ctx, true)) return false;
    seen.add(info.id);
    return true;
  });
}

const cachedProjectSessionsByDir = new Map<string, SessionInfo[]>();

export function getCachedProjectSessions(directory: string): SessionInfo[] | undefined {
  const key = normalizeFolderPath(directory);
  return key ? cachedProjectSessionsByDir.get(key) : undefined;
}

export function setCachedProjectSessions(directory: string, list: SessionInfo[]): void {
  const key = normalizeFolderPath(directory);
  if (!key) return;
  cachedProjectSessionsByDir.set(key, list);
}

export function deleteCachedProjectSessions(directory: string): void {
  const key = normalizeFolderPath(directory);
  if (!key) return;
  cachedProjectSessionsByDir.delete(key);
}
