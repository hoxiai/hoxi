import { produce } from "solid-js/store";
import { hoxiAPI, type SaveWorkspaceInput, type WorkspaceProfile } from "../services/api";
import {
  isValidStoredProjectPath,
  PROJECT_ORDER_KEY,
  RECENT_PROJECTS_KEY,
  SIDEBAR_PROJECT_MODE_KEY,
  setWorkspace,
  workspace,
} from "./workspace-state";
import { loadAgents, loadProviders } from "./workspace-providers";
import { isPlaceholderSessionTitle, preserveSessionTitle, sortSessionsByActivity } from "./session-title";
import { syncPendingPermissions } from "./approval-mode";
import { syncSessionStatuses } from "./session-status-sync";
import {
  loadSessionMessagesOnly,
  reconcileSessionsInPlace,
  selectSession,
} from "./workspace-sessions";
import type { SessionInfo } from "../services/sse-types";
import { isSessionArchived, isSessionBusy, isSessionDeleted } from "./session-query";
import {
  findWorkspaceProfileByDir,
  findWorkspaceProfileById,
  isDismissedProjectPath,
  isInvalidProjectPath,
  normalizeFolderPath,
  recordDismissedProject,
  unrecordDismissedProject,
  workspacePrimaryFolder,
} from "./workspace-folders";
import { refreshWorktreeStatus } from "./session-diffs";

export async function loadSavedWorkspaces(): Promise<void> {
  try {
    const res = await hoxiAPI.listWorkspaces();
    if (Array.isArray(res?.workspaces)) {
      setWorkspace("savedWorkspaces", res.workspaces);
    }
    if (Array.isArray(res?.dismissed)) {
      for (const d of res.dismissed) {
        recordDismissedProject(d);
      }
    }
  } catch (err) {
    console.error("Failed to load saved workspaces:", err);
  }
}

export async function loadProjects(): Promise<void> {
  try {
    const remote = await hoxiAPI.listProjects();
    const list = (Array.isArray(remote) ? remote : []).filter(
      (p) => !isInvalidProjectPath(p.worktree) && !isDismissedProjectPath(p.worktree)
    );
    setWorkspace("projects", list);
  } catch (err) {
    console.error("Failed to load projects:", err);
  }
}

export function recordRecentProject(directory: string) {
  const cleaned = normalizeFolderPath(directory);
  if (!cleaned || isInvalidProjectPath(cleaned)) return;
  const recents = workspace.recentProjects.filter(
    (p) => normalizeFolderPath(p) !== cleaned && !isInvalidProjectPath(p)
  );
  const updatedRecents = [cleaned, ...recents].slice(0, 20);
  setWorkspace("recentProjects", updatedRecents);
  try { localStorage.setItem(RECENT_PROJECTS_KEY, JSON.stringify(updatedRecents)); } catch { /* ignore */ }
}

export async function selectInitialProjectSession(
  sessions: SessionInfo[],
  expectedRootDir = normalizeFolderPath(workspace.rootDir)
): Promise<void> {
  const unarchived = sessions.filter((s) => !s.parentID && !isSessionArchived(s));
  if (unarchived.length === 0) {
    setWorkspace("bootstrappingSessions", false);
    return;
  }
  const sorted = sortSessionsByActivity(unarchived);
  const candidates = [
    ...sorted.filter((s) => !isPlaceholderSessionTitle(s.title)),
    ...sorted.filter((s) => isPlaceholderSessionTitle(s.title)),
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
        normalizeFolderPath(workspace.rootDir) !== expectedRootDir
      ) {
        break;
      }
      await loadSessionMessagesOnly(next.id).catch(() => {});
      if ((workspace.messagesBySession[next.id]?.length ?? 0) > 0 || isSessionBusy(next.id)) {
        if (
          workspace.activeSessionID === first.id &&
          normalizeFolderPath(workspace.rootDir) === expectedRootDir
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
  if (normalizeFolderPath(workspace.rootDir) !== cleaned) return null;
  setWorkspace("activeProjectID", projectID);
  return projectID;
}

export async function bootstrapProjectSessions(
  directory: string,
  selectInitial = true,
  projectID?: string | null
): Promise<void> {
  const cleaned = normalizeFolderPath(directory);
  if (!cleaned || isInvalidProjectPath(cleaned)) {
    setWorkspace("bootstrappingSessions", false);
    return;
  }
  let rawSessions: SessionInfo[];
  try {
    rawSessions = await hoxiAPI.listSessions(cleaned, projectID || undefined);
  } catch {
    await new Promise((r) => setTimeout(r, 400));
    rawSessions = await hoxiAPI.listSessions(cleaned, projectID || undefined);
  }
  if (normalizeFolderPath(workspace.rootDir) !== cleaned) return;
  const sessions = (Array.isArray(rawSessions) ? rawSessions : []).filter(
    (s) => !isSessionDeleted(s.id)
  );
  const titledSessions = sessions.map(preserveSessionTitle);
  reconcileSessionsInPlace(titledSessions);
  setWorkspace(
    produce((state) => {
      for (const s of titledSessions) {
        if (s.directory && !state.worktreeBySession[s.id]) {
          state.worktreeBySession[s.id] = {
            sessionID: s.id,
            isolated: s.sandboxMode ? s.sandboxMode === "worktree" : normalizeFolderPath(s.directory) !== cleaned,
            directory: s.directory,
            branch: s.branch,
            createdAt: new Date(s.time?.created || Date.now()).toISOString(),
          };
        }
      }
    })
  );
  void refreshWorktreeStatus("__root__");
  void syncSessionStatuses(cleaned);
  void syncPendingPermissions(cleaned);
  if (selectInitial) await selectInitialProjectSession(titledSessions, cleaned);
  else setWorkspace("bootstrappingSessions", false);
}

export interface SwitchWorkspaceOptions {
  selectInitial?: boolean;
  targetSessionID?: string;
  cachedSessions?: SessionInfo[];
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
  const targetSessionID = opts.targetSessionID ?? null;
  const hydratedSessions = Array.isArray(opts.cachedSessions)
    ? opts.cachedSessions.filter((s) => !isSessionDeleted(s.id)).map(preserveSessionTitle)
    : [];

  recordRecentProject(cleaned);

  setWorkspace(
    produce((state) => {
      const prevRoot = normalizeFolderPath(state.rootDir);
      for (const s of state.sessions) {
        if (s.directory && !state.worktreeBySession[s.id]) {
          state.worktreeBySession[s.id] = {
            sessionID: s.id,
            isolated: s.sandboxMode
              ? s.sandboxMode === "worktree"
              : Boolean(prevRoot && normalizeFolderPath(s.directory) !== prevRoot),
            directory: s.directory,
            branch: s.branch,
            createdAt: new Date(s.time?.created || Date.now()).toISOString(),
          };
        }
      }
    })
  );

  setWorkspace({
    rootDir: cleaned,
    sessions: hydratedSessions,
    activeInspectorFolder: null,
    inspectedFilePath: null,
    inspectedFileLine: null,
    inspectedFileDiff: null,
    inspectedDiffFile: null,
    activeSessionID: targetSessionID,
    activeProjectID: null,
    bootstrappingSessions: hydratedSessions.length === 0 && !targetSessionID,
  });

  const selectPromise = targetSessionID ? selectSession(targetSessionID) : null;
  const projectID = await syncActiveProjectID(cleaned);
  if (normalizeFolderPath(workspace.rootDir) !== cleaned) return;

  try {
    await Promise.all([
      bootstrapProjectSessions(cleaned, selectInitial, projectID),
      ...(selectPromise ? [selectPromise] : []),
    ]);
  } catch (err) {
    setWorkspace("bootstrappingSessions", false);
    console.error("switch workspace error:", err);
  }

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
  setWorkspace({
    workspaceModalMode: "create",
    editingWorkspaceID: null,
    editingWorkspaceDir: initialFolderPath ? normalizeFolderPath(initialFolderPath) : null,
  });
}

export function openEditWorkspaceModal(dirOrWorkspaceID: string) {
  const byDir = findWorkspaceProfileById(dirOrWorkspaceID) ?? findWorkspaceProfileByDir(dirOrWorkspaceID);
  setWorkspace({
    workspaceModalMode: "edit",
    editingWorkspaceID: byDir?.id ?? null,
    editingWorkspaceDir: byDir ? workspacePrimaryFolder(byDir) : normalizeFolderPath(dirOrWorkspaceID),
  });
}

export function openDeleteWorkspaceModal(dirOrWorkspaceID: string) {
  const byDir = findWorkspaceProfileById(dirOrWorkspaceID) ?? findWorkspaceProfileByDir(dirOrWorkspaceID);
  setWorkspace({
    workspaceModalMode: "delete",
    editingWorkspaceID: byDir?.id ?? null,
    editingWorkspaceDir: byDir ? workspacePrimaryFolder(byDir) : normalizeFolderPath(dirOrWorkspaceID),
  });
}

export function openManageWorkspacesModal() {
  setWorkspace({ workspaceModalMode: "manage", editingWorkspaceID: null, editingWorkspaceDir: null });
}

export function closeWorkspaceModal() {
  setWorkspace({ workspaceModalMode: null, editingWorkspaceID: null, editingWorkspaceDir: null });
}

export async function saveWorkspaceProfile(
  input: SaveWorkspaceInput,
  existingID?: string | null
): Promise<WorkspaceProfile> {
  const previous = existingID ? findWorkspaceProfileById(existingID) : undefined;
  const saved = existingID
    ? await hoxiAPI.updateWorkspace(existingID, input)
    : await hoxiAPI.createWorkspace(input);

  setWorkspace("savedWorkspaces", (prev) => {
    const idx = prev.findIndex((w) => w.id === saved.id);
    if (idx < 0) return [saved, ...prev];
    const next = prev.slice();
    next[idx] = saved;
    return next;
  });

  const primaryPath = workspacePrimaryFolder(saved);
  if (primaryPath) recordRecentProject(primaryPath);
  if (saved?.id) {
    unrecordDismissedProject(saved.id);
    for (const f of saved.folders || []) unrecordDismissedProject(f.path);
  }
  // The backend relinked sessions while saving. If the active root moved,
  // switch to the new path so transcripts, diffs and terminals follow it.
  const previousPrimary = normalizeFolderPath(workspacePrimaryFolder(previous));
  const savedPrimary = normalizeFolderPath(workspacePrimaryFolder(saved));
  if (
    previousPrimary && savedPrimary && previousPrimary !== savedPrimary &&
    normalizeFolderPath(workspace.rootDir) === previousPrimary
  ) {
    await switchWorkspace(savedPrimary, {
      selectInitial: false,
      targetSessionID: workspace.activeSessionID ?? undefined,
      cachedSessions: workspace.sessions.slice(),
    });
  }
  return saved;
}

export async function quickAddFolderToWorkspace(dirOrWorkspaceID: string): Promise<void> {
  try {
    const picked = await hoxiAPI.openDirectoryDialog();
    const newPath = normalizeFolderPath(picked?.path);
    if (!newPath || isInvalidProjectPath(newPath)) return;

    const existing =
      findWorkspaceProfileById(dirOrWorkspaceID) ?? findWorkspaceProfileByDir(dirOrWorkspaceID);

    if (existing) {
      if (existing.folders.some((f) => normalizeFolderPath(f.path) === newPath)) return;
      const folders = [
        ...existing.folders.map((f) => ({ path: f.path, isPrimary: f.isPrimary })),
        { path: newPath, isPrimary: false },
      ];
      await saveWorkspaceProfile({ name: existing.name, color: existing.color, folders }, existing.id);
    } else {
      const primaryDir = normalizeFolderPath(dirOrWorkspaceID) || workspace.rootDir;
      const folders =
        primaryDir && !isInvalidProjectPath(primaryDir) && primaryDir !== newPath
          ? [
              { path: primaryDir, isPrimary: true },
              { path: newPath, isPrimary: false },
            ]
          : [{ path: newPath, isPrimary: true }];
      await saveWorkspaceProfile({ folders });
    }
  } catch (err) {
    console.error("quickAddFolderToWorkspace error:", err);
  }
}

export async function removeWorkspaceProfile(dirOrWorkspaceID: string): Promise<void> {
  const normInput = normalizeFolderPath(dirOrWorkspaceID);
  const existing =
    findWorkspaceProfileById(dirOrWorkspaceID) ?? findWorkspaceProfileByDir(dirOrWorkspaceID);

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
  for (const k of removedKeys) recordDismissedProject(k);

  try {
    await hoxiAPI.deleteWorkspace(existing?.id || normInput || dirOrWorkspaceID);
  } catch (err) {
    console.warn("deleteWorkspace API warning:", err);
  }

  setWorkspace("savedWorkspaces", (prev) =>
    prev.filter(
      (w) => !removedKeys.has(w.id) && !w.folders.some((f) => removedKeys.has(normalizeFolderPath(f.path)))
    )
  );
  setWorkspace("projects", (prev) =>
    prev.filter((p) => !removedKeys.has(normalizeFolderPath(p.worktree)))
  );

  const nextRecents = workspace.recentProjects.filter((p) => !removedKeys.has(normalizeFolderPath(p)));
  setWorkspace("recentProjects", nextRecents);
  try {
    localStorage.setItem(RECENT_PROJECTS_KEY, JSON.stringify(nextRecents));
  } catch { /* ignore */ }

  const nextOrder = workspace.projectOrder.filter((p) => !removedKeys.has(normalizeFolderPath(p)));
  setWorkspace("projectOrder", nextOrder);
  try {
    localStorage.setItem(PROJECT_ORDER_KEY, JSON.stringify(nextOrder));
  } catch { /* ignore */ }

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

export function setSidebarProjectMode(mode: "single" | "multi" | "recent") {
  setWorkspace("sidebarProjectMode", mode);
  try { localStorage.setItem(SIDEBAR_PROJECT_MODE_KEY, mode); } catch { /* ignore */ }
}
