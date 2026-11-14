import { createSignal, untrack } from "solid-js";
import type { SessionInfo } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import { filterProjectSessions, indexSessions, setCachedProjectSessions } from "./session-index";
import { findWorkspaceProfileByDir, isInvalidProjectPath, normalizeFolderPath } from "./workspace-folders";
import { bootstrapProjectSessions, loadProjects } from "./workspace-projects";
import { isSessionDeleted } from "./session-query";
import { workspace } from "./workspace-state";

const BACKGROUND_LOAD_CONCURRENCY = 2;

/** Sidebar list state is scoped to its owner; pending reads never borrow another root's list. */
export function createProjectSessionLists() {
  const [sessions, setSessions] = createSignal<Record<string, SessionInfo[]>>({});
  const [loading, setLoading] = createSignal<Record<string, boolean>>({});
  const revisions = new Map<string, number>();
  const pending = new Map<string, Promise<void>>();
  const backgroundQueue: string[] = [];
  const backgroundQueued = new Set<string>();
  let backgroundActive = 0;
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
    setCachedProjectSessions(path, current);
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
          setCachedProjectSessions(path, owned);
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

  function pumpBackground(): void {
    while (!disposed && backgroundActive < BACKGROUND_LOAD_CONCURRENCY && backgroundQueue.length > 0) {
      const next = backgroundQueue.shift()!;
      backgroundQueued.delete(next);
      if (pending.has(next) || untrack(sessions)[next] !== undefined) continue;
      backgroundActive += 1;
      void load(next).finally(() => {
        backgroundActive -= 1;
        pumpBackground();
      });
    }
  }

  function enqueueBackground(paths: readonly string[]): void {
    if (disposed) return;
    const currentSessions = untrack(sessions);
    for (const raw of paths) {
      const path = normalizeFolderPath(raw);
      if (!path || isInvalidProjectPath(path)) continue;
      if (pending.has(path) || backgroundQueued.has(path) || currentSessions[path] !== undefined) continue;
      backgroundQueued.add(path);
      backgroundQueue.push(path);
    }
    pumpBackground();
  }

  async function loadWithConcurrency(paths: readonly string[], limit: number): Promise<void> {
    if (disposed || paths.length === 0) return;
    let nextIdx = 0;
    const workerCount = Math.min(paths.length, Math.max(1, limit));
    await Promise.all(
      Array.from({ length: workerCount }, async () => {
        while (!disposed) {
          const idx = nextIdx++;
          if (idx >= paths.length) return;
          await load(paths[idx]);
        }
      })
    );
  }

  async function refresh(paths: string[]): Promise<void> {
    await loadProjects();
    if (disposed) return;
    const unique = [
      ...new Set(paths.map(normalizeFolderPath).filter((p) => p && !isInvalidProjectPath(p))),
    ];
    const activeRoot = normalizeFolderPath(workspace.rootDir);
    if (activeRoot && unique.includes(activeRoot)) {
      await load(activeRoot);
      if (disposed) return;
    }
    const rest = unique.filter((p) => p !== activeRoot);
    await loadWithConcurrency(rest, BACKGROUND_LOAD_CONCURRENCY);
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

  return {
    sessions,
    loading,
    syncActive,
    load,
    enqueueBackground,
    refresh,
    updateSession,
    dispose: () => {
      disposed = true;
      backgroundQueue.length = 0;
      backgroundQueued.clear();
    },
  };
}
