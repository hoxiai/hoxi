import { batch } from "solid-js";
import { produce } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import {
  readStorageItem,
  RECENT_PROJECTS_KEY,
  SIDEBAR_PROJECT_MODE_KEY,
  setWorkspace,
  workspace,
  writeStorageItem,
  writeStorageJSON,
  type SidebarProjectMode,
} from "./workspace-state";
import { loadAgents, loadProviders } from "./workspace-providers";
import { isPlaceholderSessionTitle, preserveSessionTitle, sortSessionsByActivity } from "./session-title";
import { syncPendingPermissions } from "./approval-mode";
import { syncPendingQuestions } from "./question-answers";
import { syncSessionStatuses } from "./session-status-sync";
import { loadSessionMessagesOnly, reconcileSessionsInPlace, selectSession } from "./workspace-sessions";
import { hydratedSessionIDs } from "./session-transcripts";
import type { SessionInfo } from "../services/sse-types";
import {
  isSessionArchived, isSessionBusy, isSessionDeleted, isSessionTaskActive, requestTimelineFollow,
} from "./session-query";
import {
  findWorkspaceProfileByDir, findWorkspaceProfileById, getActiveWorkWorkspaceDir,
  getRememberedCodeSurface, getRememberedWorkSurface, isDismissedProjectPath,
  isInvalidProjectPath, isWorkSurfacePath, normalizeFolderPath, pruneWorkPathsFromCodeState,
  recordDismissedProject, resolveDefaultProjectDir,
  setRememberedCodeSurface, setRememberedWorkSurface, stripWorktreePath,
  workspaceFoldersForDir,
} from "./workspace-folders";
import { setDefaultWorkspaceDir, setSurfaceMode, surfaceMode, type SurfaceMode } from "./layout";
import { refreshWorktreeStatus } from "./session-diffs";
import {
  filterProjectSessions, getCachedProjectSessions, setCachedProjectSessions,
} from "./session-index";

let projectSessionLoadRevision = 0;
let appliedProjectSessionRevision = 0;
let projectResolutionRevision = 0;
const lastBootstrappedAtByDir = new Map<string, number>();

export async function activateSurfaceMode(nextMode: SurfaceMode): Promise<void> {
  const prevMode = surfaceMode();
  const currentRoot = normalizeFolderPath(workspace.rootDir);
  const currentSessionID = workspace.activeSessionID;

  if (
    prevMode === nextMode &&
    ((nextMode === "work" && currentRoot && isWorkSurfacePath(currentRoot)) ||
      (nextMode === "code" && currentRoot && !isWorkSurfacePath(currentRoot)))
  ) {
    return;
  }

  if (prevMode === "code" && currentRoot && !isWorkSurfacePath(currentRoot)) {
    setRememberedCodeSurface(currentRoot, currentSessionID, workspace.sessions.slice());
  } else if (prevMode === "work" && currentRoot && isWorkSurfacePath(currentRoot)) {
    const validWorkSessionID =
      currentSessionID && workspace.sessions.some((s) => s.id === currentSessionID)
        ? currentSessionID
        : null;
    setRememberedWorkSurface(currentRoot, validWorkSessionID, workspace.sessions.slice());
  }

  if (nextMode === "work") {
    let workDir = getActiveWorkWorkspaceDir();
    if (!workDir) {
      try {
        const boot = await hoxiAPI.getBootstrap();
        if (boot?.defaultWorkspaceDir) {
          workDir = normalizeFolderPath(boot.defaultWorkspaceDir);
          setDefaultWorkspaceDir(workDir);
        }
      } catch {
        /* ignore */
      }
    }
    if (workDir) {
      const remWork = getRememberedWorkSurface();
      let switchPromise: Promise<void> | undefined;
      batch(() => {
        setSurfaceMode("work");
        switchPromise = switchWorkspace(workDir, {
          targetSessionID: remWork.rootDir === workDir ? remWork.sessionID ?? undefined : undefined,
          selectInitial: false,
          fastSurfaceSwitch: true,
          cachedSessions:
            remWork.rootDir === workDir && remWork.cachedSessions
              ? remWork.cachedSessions
              : getCachedProjectSessions(workDir),
        });
      });
      await switchPromise;
    } else {
      batch(() => {
        setSurfaceMode("work");
        setWorkspace({ rootDir: "", sessions: [], activeSessionID: null, activeProjectID: null });
      });
    }
  } else {
    if (workspace.savedWorkspaces.length === 0 && workspace.projects.length === 0) {
      void Promise.all([loadSavedWorkspaces(), loadProjects()]);
    }
    const remCode = getRememberedCodeSurface();
    const codeDir = normalizeFolderPath(remCode.rootDir || resolveDefaultProjectDir(""));
    if (codeDir && !isWorkSurfacePath(codeDir)) {
      let switchPromise: Promise<void> | undefined;
      batch(() => {
        if (workspace.sidebarProjectMode === "attention" || workspace.sidebarProjectMode === "running") {
          setSidebarProjectMode("multi");
        }
        setSurfaceMode("code");
        switchPromise = switchWorkspace(codeDir, {
          targetSessionID: remCode.sessionID ?? undefined,
          selectInitial: !remCode.sessionID,
          fastSurfaceSwitch: true,
          cachedSessions:
            remCode.rootDir === codeDir && remCode.cachedSessions
              ? remCode.cachedSessions
              : getCachedProjectSessions(codeDir),
        });
      });
      await switchPromise;
    } else {
      batch(() => {
        setSurfaceMode("code");
        setWorkspace({ rootDir: "", sessions: [], activeSessionID: null, activeProjectID: null });
      });
    }
  }
}

export async function loadSavedWorkspaces(): Promise<void> {
  try {
    const res = await hoxiAPI.listWorkspaces();
    if (Array.isArray(res?.workspaces)) setWorkspace("savedWorkspaces", res.workspaces);
    if (Array.isArray(res?.dismissed)) {
      for (const d of res.dismissed) recordDismissedProject(d);
    }
    pruneWorkPathsFromCodeState();
  } catch (err) {
    console.error("Failed to load saved workspaces:", err);
  }
}

export async function loadProjects(): Promise<void> {
  try {
    const remote = await hoxiAPI.listProjects();
    const list = (Array.isArray(remote) ? remote : []).filter(
      (p) => !isInvalidProjectPath(p.worktree) && !isDismissedProjectPath(p.worktree) && !isWorkSurfacePath(p.worktree)
    );
    setWorkspace("projects", list);
  } catch (err) {
    console.error("Failed to load projects:", err);
  }
}

export function recordRecentProject(directory: string) {
  const cleaned = normalizeFolderPath(directory);
  if (!cleaned || isInvalidProjectPath(cleaned) || isWorkSurfacePath(cleaned) || surfaceMode() === "work") return;
  const recents = workspace.recentProjects.filter(
    (p) => normalizeFolderPath(p) !== cleaned && !isInvalidProjectPath(p) && !isWorkSurfacePath(p)
  );
  const updatedRecents = [cleaned, ...recents].slice(0, 20);
  setWorkspace("recentProjects", updatedRecents);
  writeStorageJSON(RECENT_PROJECTS_KEY, updatedRecents);
}

function isSessionInWorkspaceFolders(s: SessionInfo, rootDir: string): boolean {
  const dir = stripWorktreePath(s.directory || workspace.worktreeBySession[s.id]?.directory);
  if (!dir) return true;
  const cleanedRoot = normalizeFolderPath(rootDir);
  if (cleanedRoot && dir === cleanedRoot) return true;
  return workspaceFoldersForDir(cleanedRoot).some((f) => normalizeFolderPath(f.path) === dir);
}

export async function selectInitialProjectSession(
  sessions: SessionInfo[],
  expectedRootDir = normalizeFolderPath(workspace.rootDir),
  expectedNewThreadReq = workspace.newThreadRequest,
  preferredSessionID?: string | null
): Promise<void> {
  if (
    normalizeFolderPath(workspace.rootDir) !== expectedRootDir ||
    workspace.newThreadRequest !== expectedNewThreadReq ||
    workspace.activeSessionID !== null
  ) {
    setWorkspace("bootstrappingSessions", false);
    return;
  }
  const unarchived = sessions.filter(
    (s) => !s.parentID && !isSessionArchived(s) && isSessionInWorkspaceFolders(s, expectedRootDir)
  );
  if (unarchived.length === 0) {
    setWorkspace("bootstrappingSessions", false);
    return;
  }
  const preferred = preferredSessionID ? unarchived.find((s) => s.id === preferredSessionID) : undefined;
  const sorted = sortSessionsByActivity(unarchived);
  const candidates = [
    ...(preferred ? [preferred] : []),
    ...sorted.filter((s) => s.id !== preferred?.id && !isPlaceholderSessionTitle(s.title)),
    ...sorted.filter((s) => s.id !== preferred?.id && isPlaceholderSessionTitle(s.title)),
  ];
  const first = candidates[0];
  const p = selectSession(first.id);
  setWorkspace("bootstrappingSessions", false);
  await p;

  if (
    (workspace.messagesBySession[first.id]?.length ?? 0) === 0 &&
    !isSessionBusy(first.id) &&
    candidates.length > 1
  ) {
    for (const next of candidates.slice(1, 6)) {
      if (
        workspace.activeSessionID !== first.id ||
        normalizeFolderPath(workspace.rootDir) !== expectedRootDir ||
        workspace.newThreadRequest !== expectedNewThreadReq
      ) {
        break;
      }
      await loadSessionMessagesOnly(next.id).catch(() => {});
      if ((workspace.messagesBySession[next.id]?.length ?? 0) > 0 || isSessionBusy(next.id)) {
        if (
          workspace.activeSessionID === first.id &&
          normalizeFolderPath(workspace.rootDir) === expectedRootDir &&
          workspace.newThreadRequest === expectedNewThreadReq
        ) {
          await selectSession(next.id);
        }
        return;
      }
    }
  }
}

// Resolves the stable project ID for the active root. The backend registers a
// hidden project on first use, so sessions keep their attribution even when
// the folder was never saved as a workspace.
export async function syncActiveProjectID(directory: string): Promise<string | null> {
  const revision = ++projectResolutionRevision;
  const cleaned = normalizeFolderPath(directory);
  if (!cleaned || isInvalidProjectPath(cleaned)) {
    setWorkspace("activeProjectID", null);
    return null;
  }
  const known = findWorkspaceProfileByDir(cleaned);
  if (known?.id) {
    setWorkspace("activeProjectID", known.id);
    return known.id;
  }
  let projectID: string | null = null;
  try {
    projectID = (await hoxiAPI.resolveProject(cleaned)).projectID || null;
  } catch (err) {
    console.warn("resolve project failed:", err);
  }
  if (revision !== projectResolutionRevision || normalizeFolderPath(workspace.rootDir) !== cleaned) return null;
  setWorkspace("activeProjectID", projectID);
  if (projectID && !findWorkspaceProfileById(projectID)) {
    void loadSavedWorkspaces();
  }
  return projectID;
}

export async function bootstrapProjectSessions(
  directory: string,
  selectInitial = true,
  projectID?: string | null,
  preferredSessionID?: string | null
): Promise<void> {
  const revision = ++projectSessionLoadRevision;
  const cleaned = normalizeFolderPath(directory);
  if (!cleaned || isInvalidProjectPath(cleaned)) {
    setWorkspace("bootstrappingSessions", false);
    return;
  }
  const initialNewThreadReq = workspace.newThreadRequest;
  const effectiveProjectID =
    projectID !== undefined
      ? projectID
      : normalizeFolderPath(workspace.rootDir) === cleaned
        ? workspace.activeProjectID
        : undefined;
  let rawSessions: SessionInfo[];
  try {
    rawSessions = await hoxiAPI.listSessions(cleaned, effectiveProjectID || undefined);
  } catch {
    await new Promise((r) => setTimeout(r, 400));
    rawSessions = await hoxiAPI.listSessions(cleaned, effectiveProjectID || undefined);
  }
  if (normalizeFolderPath(workspace.rootDir) !== cleaned) return;
  const sessions = filterProjectSessions(Array.isArray(rawSessions) ? rawSessions : [], cleaned, effectiveProjectID)
    .filter((s) => !isSessionDeleted(s.id));
  const titledSessions = sessions.map(preserveSessionTitle);
  // An older read that lands after a newer one must not roll the list back,
  // but it still owns its own selection and loading-flag duties below: a
  // reconnect refresh racing the startup load must not skip initial selection.
  const isLatest = revision > appliedProjectSessionRevision;
  if (isLatest) {
    appliedProjectSessionRevision = revision;
    reconcileSessionsInPlace(titledSessions);
    setCachedProjectSessions(cleaned, workspace.sessions.slice());
    lastBootstrappedAtByDir.set(cleaned, Date.now());
    setWorkspace(
      produce((state) => {
        for (const s of titledSessions) {
          if (!s.directory) continue;
          const existing = state.worktreeBySession[s.id];
          const rootChanged =
            existing && stripWorktreePath(existing.directory) !== stripWorktreePath(s.directory);
          if (!existing || rootChanged) {
            state.worktreeBySession[s.id] = {
              sessionID: s.id,
              isolated: s.sandboxMode ? s.sandboxMode === "worktree" : normalizeFolderPath(s.directory) !== cleaned,
              directory: s.directory,
              branch: s.branch ?? existing?.branch,
              baseBranch: existing?.baseBranch,
              createdAt: existing?.createdAt || new Date(s.time?.created || Date.now()).toISOString(),
            };
          }
        }
      })
    );
  }
  if (surfaceMode() !== "work" && !isWorkSurfacePath(cleaned)) {
    void refreshWorktreeStatus("__root__");
  }
  void syncSessionStatuses(cleaned);
  void syncPendingPermissions(cleaned);
  void syncPendingQuestions(cleaned);
  if (selectInitial && workspace.newThreadRequest === initialNewThreadReq && workspace.activeSessionID) {
    const active = titledSessions.find((s) => s.id === workspace.activeSessionID);
    if (active && !isSessionInWorkspaceFolders(active, cleaned)) {
      setWorkspace("activeSessionID", null);
    }
  }
  if (
    selectInitial &&
    workspace.newThreadRequest === initialNewThreadReq &&
    !workspace.activeSessionID
  ) {
    await selectInitialProjectSession(titledSessions, cleaned, initialNewThreadReq, preferredSessionID);
  } else if (isLatest) {
    // A newer read already landed and cleared it, or is still loading.
    setWorkspace("bootstrappingSessions", false);
  }
}

export interface SwitchWorkspaceOptions {
  selectInitial?: boolean;
  targetSessionID?: string;
  preferredSessionID?: string;
  cachedSessions?: SessionInfo[];
  fastSurfaceSwitch?: boolean;
}

export async function switchWorkspace(
  directory: string,
  optionsOrSelectInitial: boolean | SwitchWorkspaceOptions = true
): Promise<void> {
  const cleaned = normalizeFolderPath(directory);
  if (!cleaned || isInvalidProjectPath(cleaned)) return;

  const opts: SwitchWorkspaceOptions =
    typeof optionsOrSelectInitial === "boolean"
      ? { selectInitial: optionsOrSelectInitial }
      : optionsOrSelectInitial;
  const selectInitial = opts.selectInitial ?? !opts.targetSessionID;
  const rawCached = opts.cachedSessions ?? getCachedProjectSessions(cleaned);
  const hydratedSessions = Array.isArray(rawCached)
    ? filterProjectSessions(rawCached, cleaned).filter((s) => !isSessionDeleted(s.id)).map(preserveSessionTitle)
    : [];

  let targetSessionID = opts.targetSessionID ?? null;
  if (!targetSessionID && selectInitial && hydratedSessions.length > 0) {
    const unarchived = hydratedSessions.filter(
      (s) => !s.parentID && !isSessionArchived(s) && isSessionInWorkspaceFolders(s, cleaned)
    );
    if (unarchived.length > 0) {
      const preferred = opts.preferredSessionID
        ? unarchived.find((s) => s.id === opts.preferredSessionID)
        : undefined;
      const sorted = sortSessionsByActivity(unarchived);
      const first = preferred || sorted.find((s) => !isPlaceholderSessionTitle(s.title)) || sorted[0];
      targetSessionID = first?.id ?? null;
    }
  }

  if (surfaceMode() === "work" || isWorkSurfacePath(cleaned)) {
    setRememberedWorkSurface(cleaned, targetSessionID);
  } else {
    recordRecentProject(cleaned);
    setRememberedCodeSurface(cleaned, targetSessionID);
  }

  const knownProjectID = findWorkspaceProfileByDir(cleaned)?.id || null;
  const hasCachedSessions = rawCached !== undefined;
  const warmSurfaceSwitch =
    Boolean(opts.fastSurfaceSwitch) && hasCachedSessions;
  const recentlyBootstrapped =
    warmSurfaceSwitch &&
    Date.now() - (lastBootstrappedAtByDir.get(cleaned) || 0) < 30_000;

  setWorkspace({
    rootDir: cleaned,
    sessions: hydratedSessions,
    activeInspectorFolder: null,
    inspectedFilePath: null,
    inspectedFileLine: null,
    inspectedFileDiff: null,
    inspectedDiffFile: null,
    activeSessionID: targetSessionID,
    activeProjectID: knownProjectID,
    bootstrappingSessions: !warmSurfaceSwitch && hydratedSessions.length === 0,
  });

  if (warmSurfaceSwitch) {
    if (targetSessionID) {
      if (workspace.messagesBySession[targetSessionID] !== undefined && hydratedSessionIDs.has(targetSessionID)) {
        setWorkspace("unreadCompletedBySession", targetSessionID, false);
        if (isSessionTaskActive(targetSessionID)) requestTimelineFollow(targetSessionID);
      } else {
        void selectSession(targetSessionID);
      }
    }
    if (!recentlyBootstrapped) {
      setTimeout(() => {
        if (normalizeFolderPath(workspace.rootDir) !== cleaned) return;
        void syncActiveProjectID(cleaned);
        void bootstrapProjectSessions(cleaned, selectInitial, knownProjectID, opts.preferredSessionID);
      }, 120);
    }
    return;
  }

  const selectPromise = targetSessionID ? selectSession(targetSessionID) : null;
  // Project resolution and the session list run in parallel: the backend
  // resolves the project from the directory when no ID is passed yet.
  const isCurrent = () => normalizeFolderPath(workspace.rootDir) === cleaned;
  try {
    await Promise.all([
      syncActiveProjectID(cleaned),
      bootstrapProjectSessions(cleaned, selectInitial, knownProjectID, opts.preferredSessionID),
      ...(selectPromise ? [selectPromise] : []),
    ]);
  } catch (err) {
    if (isCurrent()) setWorkspace("bootstrappingSessions", false);
    console.error("switch workspace error:", err);
  }
  if (!isCurrent()) return;

  if (workspace.providers.length === 0) void loadProviders();
  if (workspace.projects.length === 0) void loadProjects();
  if (workspace.savedWorkspaces.length === 0) void loadSavedWorkspaces();
  void loadAgents(cleaned);
}

export function setSidebarProjectMode(mode: SidebarProjectMode) {
  setWorkspace("sidebarProjectMode", mode);
  if (mode === "multi" || mode === "single" || mode === "recent") {
    writeStorageItem(SIDEBAR_PROJECT_MODE_KEY, mode);
  }
}

export function healTransientSidebarProjectMode(): void {
  const savedMode = readStorageItem(SIDEBAR_PROJECT_MODE_KEY);
  if (savedMode === "attention" || savedMode === "running") {
    writeStorageItem(SIDEBAR_PROJECT_MODE_KEY, "multi");
    if (workspace.sidebarProjectMode === "attention" || workspace.sidebarProjectMode === "running") {
      setWorkspace("sidebarProjectMode", "multi");
    }
  }
}

healTransientSidebarProjectMode();
