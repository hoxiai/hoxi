import { batch } from "solid-js";
import { produce } from "solid-js/store";
import { hoxiAPI, type SaveWorkspaceInput, type WorkspaceProfile } from "../services/api";
import {
  isValidStoredProjectPath, PROJECT_ORDER_KEY, RECENT_PROJECTS_KEY,
  SIDEBAR_PROJECT_MODE_KEY, setWorkspace, workspace, type SidebarProjectMode,
} from "./workspace-state";
import { loadAgents, loadProviders } from "./workspace-providers";
import { isPlaceholderSessionTitle, preserveSessionTitle, sortSessionsByActivity } from "./session-title";
import { syncPendingPermissions } from "./approval-mode";
import { syncSessionStatuses } from "./session-status-sync";
import { loadSessionMessagesOnly, reconcileSessionsInPlace, selectSession } from "./workspace-sessions";
import type { SessionInfo } from "../services/sse-types";
import { isSessionArchived, isSessionBusy, isSessionDeleted } from "./session-query";
import {
  findWorkspaceProfileByDir, findWorkspaceProfileById, isDefaultWorkspacePath,
  isDismissedProjectPath, isInvalidProjectPath, normalizeFolderPath, recordDismissedProject,
  resolveDefaultProjectDir, stripWorktreePath, unrecordDismissedProject,
  workspaceFoldersForDir, workspacePrimaryFolder,
} from "./workspace-folders";
import { defaultWorkspaceDir, setDefaultWorkspaceDir, setSurfaceMode, surfaceMode, type SurfaceMode } from "./layout";
import { refreshWorktreeStatus } from "./session-diffs";
import {
  deleteCachedProjectSessions, filterProjectSessions, getCachedProjectSessions, setCachedProjectSessions,
} from "./session-index";

let projectSessionLoadRevision = 0;
let appliedProjectSessionRevision = 0;
let projectResolutionRevision = 0;
const lastBootstrappedAtByDir = new Map<string, number>();

const REMEMBERED_CODE_SURFACE_KEY = "hoxi:remembered-code-surface";

function readRememberedCodeSurface(): { rootDir: string; sessionID: string | null } {
  try {
    const raw = localStorage.getItem(REMEMBERED_CODE_SURFACE_KEY);
    if (!raw) return { rootDir: "", sessionID: null };
    const parsed = JSON.parse(raw);
    const rootDir = normalizeFolderPath(parsed?.rootDir);
    if (rootDir && !isInvalidProjectPath(rootDir) && !isDefaultWorkspacePath(rootDir)) {
      return {
        rootDir,
        sessionID: typeof parsed?.sessionID === "string" ? parsed.sessionID : null,
      };
    }
  } catch {
    /* ignore */
  }
  return { rootDir: "", sessionID: null };
}

function writeRememberedCodeSurface(state: { rootDir: string; sessionID: string | null }): void {
  try {
    localStorage.setItem(REMEMBERED_CODE_SURFACE_KEY, JSON.stringify(state));
  } catch {
    /* ignore */
  }
}

let rememberedCodeSurface: { rootDir: string; sessionID: string | null; cachedSessions?: SessionInfo[] } =
  readRememberedCodeSurface();
let rememberedWorkSurface: { rootDir: string; sessionID: string | null; cachedSessions?: SessionInfo[] } = {
  rootDir: "",
  sessionID: null,
};

export function getInitialCodeProjectDir(): string {
  return normalizeFolderPath(rememberedCodeSurface.rootDir || resolveDefaultProjectDir(""));
}

export function setRememberedCodeSurface(rootDir: string, sessionID: string | null = null): void {
  const clean = normalizeFolderPath(rootDir);
  if (clean && !isInvalidProjectPath(clean) && !isDefaultWorkspacePath(clean)) {
    const sameDir = rememberedCodeSurface.rootDir === clean;
    const preservedSessionID = sessionID ?? (sameDir ? rememberedCodeSurface.sessionID : null);
    rememberedCodeSurface = {
      rootDir: clean,
      sessionID: preservedSessionID,
      cachedSessions: sameDir ? rememberedCodeSurface.cachedSessions : undefined,
    };
    writeRememberedCodeSurface({ rootDir: clean, sessionID: preservedSessionID });
  }
}

export async function activateSurfaceMode(nextMode: SurfaceMode): Promise<void> {
  const prevMode = surfaceMode();
  const currentRoot = normalizeFolderPath(workspace.rootDir);
  const currentSessionID = workspace.activeSessionID;

  if (
    prevMode === nextMode &&
    ((nextMode === "work" && currentRoot && isDefaultWorkspacePath(currentRoot)) ||
      (nextMode === "code" && currentRoot && !isDefaultWorkspacePath(currentRoot)))
  ) {
    return;
  }

  if (prevMode === "code" && currentRoot && !isDefaultWorkspacePath(currentRoot)) {
    rememberedCodeSurface = {
      rootDir: currentRoot,
      sessionID: currentSessionID,
      cachedSessions: workspace.sessions.slice(),
    };
    writeRememberedCodeSurface({ rootDir: currentRoot, sessionID: currentSessionID });
  } else if (prevMode === "work" && currentRoot && isDefaultWorkspacePath(currentRoot)) {
    const validWorkSessionID =
      currentSessionID && workspace.sessions.some((s) => s.id === currentSessionID)
        ? currentSessionID
        : null;
    rememberedWorkSurface = {
      rootDir: currentRoot,
      sessionID: validWorkSessionID,
      cachedSessions: workspace.sessions.slice(),
    };
  }

  if (nextMode === "work") {
    let workDir = normalizeFolderPath(defaultWorkspaceDir());
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
      let switchPromise: Promise<void> | undefined;
      batch(() => {
        setSurfaceMode("work");
        switchPromise = switchWorkspace(workDir, {
          targetSessionID: rememberedWorkSurface.sessionID ?? undefined,
          selectInitial: false,
          fastSurfaceSwitch: true,
          cachedSessions:
            rememberedWorkSurface.rootDir === workDir
              ? rememberedWorkSurface.cachedSessions
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
    const codeDir = normalizeFolderPath(
      rememberedCodeSurface.rootDir || resolveDefaultProjectDir("")
    );
    if (codeDir && !isDefaultWorkspacePath(codeDir)) {
      let switchPromise: Promise<void> | undefined;
      batch(() => {
        if (workspace.sidebarProjectMode === "attention" || workspace.sidebarProjectMode === "running") {
          setSidebarProjectMode("multi");
        }
        setSurfaceMode("code");
        switchPromise = switchWorkspace(codeDir, {
          targetSessionID: rememberedCodeSurface.sessionID ?? undefined,
          selectInitial: !rememberedCodeSurface.sessionID,
          fastSurfaceSwitch: true,
          cachedSessions:
            rememberedCodeSurface.rootDir === codeDir && rememberedCodeSurface.cachedSessions
              ? rememberedCodeSurface.cachedSessions
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
  } catch (err) {
    console.error("Failed to load saved workspaces:", err);
  }
}

export async function loadProjects(): Promise<void> {
  try {
    const remote = await hoxiAPI.listProjects();
    const list = (Array.isArray(remote) ? remote : []).filter(
      (p) => !isInvalidProjectPath(p.worktree) && !isDismissedProjectPath(p.worktree) && !isDefaultWorkspacePath(p.worktree)
    );
    setWorkspace("projects", list);
  } catch (err) {
    console.error("Failed to load projects:", err);
  }
}

export function recordRecentProject(directory: string) {
  const cleaned = normalizeFolderPath(directory);
  if (!cleaned || isInvalidProjectPath(cleaned) || isDefaultWorkspacePath(cleaned)) return;
  const recents = workspace.recentProjects.filter(
    (p) => normalizeFolderPath(p) !== cleaned && !isInvalidProjectPath(p) && !isDefaultWorkspacePath(p)
  );
  const updatedRecents = [cleaned, ...recents].slice(0, 20);
  setWorkspace("recentProjects", updatedRecents);
  try { localStorage.setItem(RECENT_PROJECTS_KEY, JSON.stringify(updatedRecents)); } catch { /* ignore */ }
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
  void refreshWorktreeStatus("__root__");
  void syncSessionStatuses(cleaned);
  void syncPendingPermissions(cleaned);
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

  recordRecentProject(cleaned);

  const knownProjectID = findWorkspaceProfileByDir(cleaned)?.id || null;
  const warmSurfaceSwitch =
    Boolean(opts.fastSurfaceSwitch) && hydratedSessions.length > 0;
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
    bootstrappingSessions: hydratedSessions.length === 0,
  });

  if (warmSurfaceSwitch) {
    if (targetSessionID) {
      if (workspace.messagesBySession[targetSessionID] !== undefined) {
        setWorkspace("unreadCompletedBySession", targetSessionID, false);
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


export async function openWorkspaceFolder(): Promise<void> {
  try {
    const res = await hoxiAPI.openDirectoryDialog();
    if (res?.path?.trim()) await switchWorkspace(res.path.trim());
  } catch (err) {
    console.error("open directory dialog error:", err);
  }
}

export function openCreateWorkspaceModal(initialFolderPath?: string) {
  setWorkspace({ workspaceModalMode: "create", editingWorkspaceID: null, editingWorkspaceDir: initialFolderPath ? normalizeFolderPath(initialFolderPath) : null });
}

export function openEditWorkspaceModal(dirOrWorkspaceID: string) {
  const byDir = findWorkspaceProfileById(dirOrWorkspaceID) ?? findWorkspaceProfileByDir(dirOrWorkspaceID);
  setWorkspace({ workspaceModalMode: "edit", editingWorkspaceID: byDir?.id ?? null, editingWorkspaceDir: byDir ? workspacePrimaryFolder(byDir) : normalizeFolderPath(dirOrWorkspaceID) });
  if (!byDir) void loadSavedWorkspaces();
}

export function openDeleteWorkspaceModal(dirOrWorkspaceID: string) {
  const byDir = findWorkspaceProfileById(dirOrWorkspaceID) ?? findWorkspaceProfileByDir(dirOrWorkspaceID);
  setWorkspace({ workspaceModalMode: "delete", editingWorkspaceID: byDir?.id ?? null, editingWorkspaceDir: byDir ? workspacePrimaryFolder(byDir) : normalizeFolderPath(dirOrWorkspaceID) });
}

export function openManageWorkspacesModal() {
  setWorkspace({ workspaceModalMode: "manage", editingWorkspaceID: null, editingWorkspaceDir: null });
}

export function closeWorkspaceModal() {
  setWorkspace({ workspaceModalMode: null, editingWorkspaceID: null, editingWorkspaceDir: null });
}

function isPathLikeID(value: string): boolean {
  return value.includes("/") || value.includes("\\") || value.startsWith("~");
}

export async function saveWorkspaceProfile(input: SaveWorkspaceInput, existingID?: string | null): Promise<WorkspaceProfile> {
  const cleanExistingID = existingID?.trim() || "";
  const previous = cleanExistingID ? findWorkspaceProfileById(cleanExistingID) ?? findWorkspaceProfileByDir(cleanExistingID) : undefined;
  const previousFolders = new Set<string>();
  if (previous) {
    for (const f of previous.folders) {
      const norm = normalizeFolderPath(f.path);
      if (norm) previousFolders.add(norm);
    }
  } else if (cleanExistingID) {
    if (isPathLikeID(cleanExistingID)) previousFolders.add(normalizeFolderPath(cleanExistingID));
    if (workspace.editingWorkspaceDir) previousFolders.add(normalizeFolderPath(workspace.editingWorkspaceDir));
  }
  const previousPrimary = normalizeFolderPath(
    workspacePrimaryFolder(previous) || (cleanExistingID ? workspace.editingWorkspaceDir || (isPathLikeID(cleanExistingID) ? cleanExistingID : "") : "")
  );
  const normalizedInput: SaveWorkspaceInput = previous
    ? {
        ...input,
        folders: input.folders.map((f, idx, all) => {
          if (f.id) return f;
          if (idx === 0 && all.length === 1 && previous.folders.length === 1) return { ...f, id: previous.folders[0].id };
          const byPath = previous.folders.find((pf) => normalizeFolderPath(pf.path) === normalizeFolderPath(f.path));
          return byPath?.id ? { ...f, id: byPath.id } : f;
        }),
      }
    : input;

  const saved = cleanExistingID
    ? await hoxiAPI.updateWorkspace(previous?.id || cleanExistingID, normalizedInput)
    : await hoxiAPI.createWorkspace(normalizedInput);

  const savedFolderSet = new Set((saved.folders || []).map((f) => normalizeFolderPath(f.path)).filter(Boolean));
  const savedPrimary = normalizeFolderPath(workspacePrimaryFolder(saved));
  const savedSecondarySet = new Set(Array.from(savedFolderSet).filter((p) => p !== savedPrimary));
  const aliasSet = new Set(saved.aliases || []);

  setWorkspace("savedWorkspaces", (prev) => {
    const next: WorkspaceProfile[] = [];
    let replaced = false;
    for (const w of prev) {
      if (aliasSet.has(w.id)) continue;
      if (w.id === saved.id || (previous && w.id === previous.id)) {
        if (!replaced) { next.push(saved); replaced = true; }
        continue;
      }
      if (w.hidden && w.folders?.some((f) => savedFolderSet.has(normalizeFolderPath(f.path)))) {
        const rem = w.folders.filter((f) => !savedFolderSet.has(normalizeFolderPath(f.path)));
        if (rem.length > 0) next.push({ ...w, folders: rem });
        continue;
      }
      next.push(w);
    }
    return replaced ? next : [saved, ...next];
  });

  const otherOwnedFolders = new Set<string>();
  const otherPrimaryFolders = new Set<string>();
  for (const w of workspace.savedWorkspaces) {
    if (w.id === saved.id || (previous && w.id === previous.id)) continue;
    if (!w.hidden) {
      const p = normalizeFolderPath(workspacePrimaryFolder(w));
      if (p) otherPrimaryFolders.add(p);
    }
    for (const f of w.folders || []) {
      const norm = normalizeFolderPath(f.path);
      if (norm) otherOwnedFolders.add(norm);
    }
  }

  const removedFolders = new Set<string>();
  for (const oldPath of previousFolders) {
    if (oldPath && !savedFolderSet.has(oldPath) && !otherOwnedFolders.has(oldPath)) removedFolders.add(oldPath);
  }
  for (const p of workspace.projects) {
    const wt = normalizeFolderPath(p.worktree);
    if (!wt || savedFolderSet.has(wt) || otherOwnedFolders.has(wt)) continue;
    if ((p.sandboxes || []).some((s) => savedFolderSet.has(stripWorktreePath(s)))) removedFolders.add(wt);
  }

  const hiddenSecondary = new Set(Array.from(savedSecondarySet).filter((p) => !otherPrimaryFolders.has(p)));
  for (const secPath of hiddenSecondary) deleteCachedProjectSessions(secPath);

  if (removedFolders.size > 0) {
    for (const oldPath of removedFolders) {
      recordDismissedProject(oldPath);
      deleteCachedProjectSessions(oldPath);
    }
    setWorkspace("projects", (prev) => prev.filter((p) => !removedFolders.has(normalizeFolderPath(p.worktree))));
  }
  if (removedFolders.size > 0 || hiddenSecondary.size > 0) {
    const nextRecents = workspace.recentProjects.filter((p) => {
      const norm = normalizeFolderPath(p);
      return !removedFolders.has(norm) && !hiddenSecondary.has(norm);
    });
    setWorkspace("recentProjects", nextRecents);
    try { localStorage.setItem(RECENT_PROJECTS_KEY, JSON.stringify(nextRecents)); } catch { /* ignore */ }
  }

  const currentOrder = workspace.projectOrder.slice();
  if (previousPrimary && savedPrimary && previousPrimary !== savedPrimary) {
    const prevIdx = currentOrder.findIndex((p) => normalizeFolderPath(p) === previousPrimary);
    if (prevIdx >= 0) currentOrder[prevIdx] = savedPrimary;
    if (rememberedCodeSurface.rootDir === previousPrimary || removedFolders.has(rememberedCodeSurface.rootDir)) {
      setRememberedCodeSurface(savedPrimary, null);
    }
  }
  if ((previousPrimary && savedPrimary && previousPrimary !== savedPrimary) || removedFolders.size > 0 || hiddenSecondary.size > 0) {
    setProjectOrder(
      currentOrder.filter((p) => {
        const norm = normalizeFolderPath(p);
        return norm === savedPrimary || (!removedFolders.has(norm) && !hiddenSecondary.has(norm));
      })
    );
  }

  if (savedPrimary) recordRecentProject(savedPrimary);
  if (saved?.id) {
    unrecordDismissedProject(saved.id);
    for (const f of saved.folders || []) unrecordDismissedProject(f.path);
  }

  const activeRoot = normalizeFolderPath(workspace.rootDir);
  if (
    savedPrimary &&
    activeRoot !== savedPrimary &&
    ((previousPrimary && activeRoot === previousPrimary) || removedFolders.has(activeRoot) || savedSecondarySet.has(activeRoot))
  ) {
    await switchWorkspace(savedPrimary, { preferredSessionID: workspace.activeSessionID ?? undefined });
  } else if (savedPrimary && activeRoot === savedPrimary && saved?.id) {
    setWorkspace("activeProjectID", saved.id);
    await bootstrapProjectSessions(savedPrimary, true, saved.id, workspace.activeSessionID);
  }
  return saved;
}

export async function quickAddFolderToWorkspace(dirOrWorkspaceID: string): Promise<void> {
  try {
    const picked = await hoxiAPI.openDirectoryDialog();
    const newPath = normalizeFolderPath(picked?.path);
    if (!newPath || isInvalidProjectPath(newPath)) return;
    const existing = findWorkspaceProfileById(dirOrWorkspaceID) ?? findWorkspaceProfileByDir(dirOrWorkspaceID);
    if (existing) {
      if (existing.folders.some((f) => normalizeFolderPath(f.path) === newPath)) return;
      const folders = [...existing.folders.map((f) => ({ path: f.path, isPrimary: f.isPrimary })), { path: newPath, isPrimary: false }];
      await saveWorkspaceProfile({ name: existing.name, color: existing.color, folders }, existing.id);
    } else {
      const primaryDir = normalizeFolderPath(dirOrWorkspaceID) || workspace.rootDir;
      const folders =
        primaryDir && !isInvalidProjectPath(primaryDir) && primaryDir !== newPath
          ? [{ path: primaryDir, isPrimary: true }, { path: newPath, isPrimary: false }]
          : [{ path: newPath, isPrimary: true }];
      await saveWorkspaceProfile({ folders });
    }
  } catch (err) {
    console.error("quickAddFolderToWorkspace error:", err);
  }
}

export async function removeWorkspaceProfile(dirOrWorkspaceID: string): Promise<void> {
  const normInput = normalizeFolderPath(dirOrWorkspaceID);
  const existing = findWorkspaceProfileById(dirOrWorkspaceID) ?? findWorkspaceProfileByDir(dirOrWorkspaceID);
  const removedKeys = new Set<string>();
  if (dirOrWorkspaceID) removedKeys.add(dirOrWorkspaceID);
  if (normInput) removedKeys.add(normInput);
  if (existing) {
    removedKeys.add(existing.id);
    for (const f of existing.folders) {
      const nf = normalizeFolderPath(f.path);
      if (nf) removedKeys.add(nf);
    }
  }
  for (const p of workspace.projects) {
    const wt = normalizeFolderPath(p.worktree);
    if (wt && (p.sandboxes || []).some((s) => removedKeys.has(stripWorktreePath(s)))) removedKeys.add(wt);
  }
  for (const k of removedKeys) recordDismissedProject(k);
  try {
    await hoxiAPI.deleteWorkspace(existing?.id || normInput || dirOrWorkspaceID);
  } catch (err) {
    console.warn("deleteWorkspace API warning:", err);
  }
  setWorkspace("savedWorkspaces", (prev) =>
    prev.filter((w) => !removedKeys.has(w.id) && !w.folders.some((f) => removedKeys.has(normalizeFolderPath(f.path))))
  );
  setWorkspace("projects", (prev) => prev.filter((p) => !removedKeys.has(normalizeFolderPath(p.worktree))));
  const nextRecents = workspace.recentProjects.filter((p) => !removedKeys.has(normalizeFolderPath(p)));
  setWorkspace("recentProjects", nextRecents);
  try { localStorage.setItem(RECENT_PROJECTS_KEY, JSON.stringify(nextRecents)); } catch { /* ignore */ }
  const nextOrder = workspace.projectOrder.filter((p) => !removedKeys.has(normalizeFolderPath(p)));
  setWorkspace("projectOrder", nextOrder);
  try { localStorage.setItem(PROJECT_ORDER_KEY, JSON.stringify(nextOrder)); } catch { /* ignore */ }
  if (removedKeys.has(normalizeFolderPath(workspace.rootDir))) {
    const remainingSaved = workspace.savedWorkspaces.filter(
      (w) => !removedKeys.has(w.id) && !w.folders.some((f) => removedKeys.has(normalizeFolderPath(f.path)))
    );
    const nextCandidate =
      workspacePrimaryFolder(remainingSaved[0]) ||
      nextRecents.find((p) => !removedKeys.has(normalizeFolderPath(p))) ||
      workspace.projects.find((p) => !removedKeys.has(normalizeFolderPath(p.worktree)))?.worktree;
    if (nextCandidate) await switchWorkspace(nextCandidate);
  }
}

export function setProjectOrder(order: string[]) {
  const unique = Array.from(new Set(order.filter(isValidStoredProjectPath)));
  setWorkspace("projectOrder", unique);
  try { localStorage.setItem(PROJECT_ORDER_KEY, JSON.stringify(unique)); } catch { /* ignore */ }
}

export function reorderProjects(
  currentPaths: string[],
  fromPath: string,
  toPath: string,
  position: "before" | "after"
) {
  if (!fromPath || !toPath || fromPath === toPath) return;
  const list = Array.from(new Set(currentPaths.filter(isValidStoredProjectPath)));
  const fromIdx = list.indexOf(fromPath);
  if (fromIdx < 0) return;
  list.splice(fromIdx, 1);
  const toIdx = list.indexOf(toPath);
  if (toIdx < 0) list.push(fromPath);
  else list.splice(position === "after" ? toIdx + 1 : toIdx, 0, fromPath);
  setProjectOrder(list);
}

export function setSidebarProjectMode(mode: SidebarProjectMode) {
  setWorkspace("sidebarProjectMode", mode);
  if (mode === "multi" || mode === "single" || mode === "recent") {
    try { localStorage.setItem(SIDEBAR_PROJECT_MODE_KEY, mode); } catch { /* ignore */ }
  }
}

export function healTransientSidebarProjectMode(): void {
  try {
    const savedMode = localStorage.getItem(SIDEBAR_PROJECT_MODE_KEY);
    if (savedMode === "attention" || savedMode === "running") {
      localStorage.setItem(SIDEBAR_PROJECT_MODE_KEY, "multi");
      if (workspace.sidebarProjectMode === "attention" || workspace.sidebarProjectMode === "running") {
        setWorkspace("sidebarProjectMode", "multi");
      }
    }
  } catch {
    /* ignore */
  }
}

healTransientSidebarProjectMode();
