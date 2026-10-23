import { createSignal, untrack } from "solid-js";
import type { SessionInfo } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import { filterProjectSessions, indexSessions } from "./session-index";
import { findWorkspaceProfileByDir, isInvalidProjectPath, normalizeFolderPath } from "./workspace-folders";
import { bootstrapProjectSessions, loadProjects } from "./workspace-projects";
import { isSessionDeleted } from "./session-query";
import { workspace } from "./workspace-state";

/** Sidebar list state is scoped to its owner; pending reads never borrow another root's list. */
export function createProjectSessionLists() {
  const [sessions, setSessions] = createSignal<Record<string, SessionInfo[]>>({});
  const [loading, setLoading] = createSignal<Record<string, boolean>>({});
  const revisions = new Map<string, number>();
  const pending = new Map<string, Promise<void>>();
  let disposed = false;
  const revisionFor = (path: string) => revisions.get(path) || 0;
  const advance = (path: string) => revisions.set(path, revisionFor(path) + 1);
  const filtered = (list: SessionInfo[], path: string, projectID?: string) =>
    filterProjectSessions(list, path, projectID).filter((s) => !isSessionDeleted(s.id));

  function syncActive(directory = workspace.rootDir): void {
    const path = normalizeFolderPath(directory);
    if (disposed || !path || isInvalidProjectPath(path) || workspace.bootstrappingSessions ||
      path !== normalizeFolderPath(workspace.rootDir)) return;
    const current = filtered(workspace.sessions, path, workspace.activeProjectID || undefined);
    const previous = untrack(sessions)[path];
    if (previous && previous.length === current.length && previous.every((s, i) => s === current[i])) return;
    advance(path);
    setSessions((prev) => ({ ...prev, [path]: current }));
  }

  function load(directory: string): Promise<void> {
    const path = normalizeFolderPath(directory);
    if (disposed || !path || isInvalidProjectPath(path)) return Promise.resolve();
    const existing = pending.get(path);
    if (existing) return existing;
    const revision = revisionFor(path);
    const projectID = findWorkspaceProfileByDir(path)?.id;
    const isCurrent = () => !disposed && revisionFor(path) === revision &&
      findWorkspaceProfileByDir(path)?.id === projectID;
    setLoading((prev) => ({ ...prev, [path]: true }));
    // Defer the read so even a synchronous failure runs after pending is registered.
    const request = Promise.resolve().then(async () => {
      try {
        if (path === normalizeFolderPath(workspace.rootDir)) {
          await bootstrapProjectSessions(path, false);
          // bootstrap can discard its response after a switch. Never copy the
          // new active project's sessions into the old project's cache.
          if (isCurrent()) syncActive(path);
        } else {
          const list = await hoxiAPI.listSessions(path, projectID);
          if (!isCurrent()) return;
          const owned = filtered(Array.isArray(list) ? list : [], path, projectID);
          indexSessions(owned.map((s) => s.directory ? s : { ...s, directory: path }));
          setSessions((prev) => ({ ...prev, [path]: owned }));
        }
      } catch (err) {
        console.error(`Failed to load sessions for ${path}:`, err);
        if (isCurrent()) setSessions((prev) => prev[path] !== undefined ? prev : { ...prev, [path]: [] });
      } finally {
        pending.delete(path);
        if (!disposed) setLoading((prev) => ({ ...prev, [path]: false }));
      }
    });
    pending.set(path, request);
    return request;
  }

  async function refresh(paths: string[]): Promise<void> {
    await loadProjects();
    if (disposed) return;
    await Promise.all([...new Set(paths.map(normalizeFolderPath))].map(load));
  }

  function updateSession(directory: string, sessionID: string, update: (info: SessionInfo) => SessionInfo | null): void {
    const path = normalizeFolderPath(directory);
    if (sessions()[path]) advance(path);
    setSessions((prev) => {
      const list = prev[path];
      if (!list) return prev;
      const next: SessionInfo[] = [];
      for (const info of list) {
        const item = info.id === sessionID ? update(info) : info;
        if (item) next.push(item);
      }
      return { ...prev, [path]: next };
    });
  }

  return { sessions, loading, syncActive, load, refresh, updateSession,
    dispose: () => { disposed = true; } };
}
