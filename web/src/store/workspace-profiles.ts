// web/src/store/workspace-profiles.ts
import { produce } from "solid-js/store";
import { hoxiAPI, type SaveWorkspaceInput, type WorkspaceProfile } from "../services/api";
import {
  isValidStoredProjectPath,
  PROJECT_ORDER_KEY,
  RECENT_PROJECTS_KEY,
  setWorkspace,
  workspace,
  writeStorageJSON,
} from "./workspace-state";
import {
  findWorkspaceProfileByDir,
  findWorkspaceProfileById,
  getRememberedCodeSurface,
  isInvalidProjectPath,
  isWorkSurfacePath,
  normalizeFolderPath,
  pruneWorkPathsFromCodeState,
  recordDismissedProject,
  recordWorkWorkspaceDir,
  setRememberedCodeSurface,
  setRememberedWorkSurface,
  stripWorktreePath,
  unrecordDismissedProject,
  unrecordWorkWorkspaceDir,
  visibleSavedWorkspaces,
  workspacePrimaryFolder,
} from "./workspace-folders";
import { defaultWorkspaceDir, surfaceMode } from "./layout";
import { deleteCachedProjectSessions } from "./session-index";
import {
  bootstrapProjectSessions,
  loadSavedWorkspaces,
  recordRecentProject,
  switchWorkspace,
} from "./workspace-projects";

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
  void loadSavedWorkspaces();
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
  void loadSavedWorkspaces();
}

export function closeWorkspaceModal() {
  setWorkspace({ workspaceModalMode: null, editingWorkspaceID: null, editingWorkspaceDir: null });
}

function isPathLikeID(value: string): boolean {
  return value.includes("/") || value.includes("\\") || value.startsWith("~");
}

export async function saveWorkspaceProfile(
  input: SaveWorkspaceInput,
  existingID?: string | null
): Promise<WorkspaceProfile> {
  const cleanExistingID = existingID?.trim() || "";
  const previous = cleanExistingID
    ? findWorkspaceProfileById(cleanExistingID) ?? findWorkspaceProfileByDir(cleanExistingID)
    : undefined;
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
    workspacePrimaryFolder(previous) ||
      (cleanExistingID
        ? workspace.editingWorkspaceDir || (isPathLikeID(cleanExistingID) ? cleanExistingID : "")
        : "")
  );
  const targetSurface: "code" | "work" =
    input.surface || previous?.surface || (surfaceMode() === "work" ? "work" : "code");
  const normalizedInput: SaveWorkspaceInput = previous
    ? {
        ...input,
        surface: targetSurface,
        folders: input.folders.map((f, idx, all) => {
          if (f.id) return f;
          if (idx === 0 && all.length === 1 && previous.folders.length === 1) {
            return { ...f, id: previous.folders[0].id };
          }
          const byPath = previous.folders.find(
            (pf) => normalizeFolderPath(pf.path) === normalizeFolderPath(f.path)
          );
          return byPath?.id ? { ...f, id: byPath.id } : f;
        }),
      }
    : { ...input, surface: targetSurface };

  const saved = cleanExistingID
    ? await hoxiAPI.updateWorkspace(previous?.id || cleanExistingID, normalizedInput)
    : await hoxiAPI.createWorkspace(normalizedInput);
  if (!saved.surface) saved.surface = targetSurface;

  const savedFolderSet = new Set(
    (saved.folders || []).map((f) => normalizeFolderPath(f.path)).filter(Boolean)
  );
  const savedPrimary = normalizeFolderPath(workspacePrimaryFolder(saved));
  const savedSecondarySet = new Set(Array.from(savedFolderSet).filter((p) => p !== savedPrimary));
  const aliasSet = new Set(saved.aliases || []);

  setWorkspace("savedWorkspaces", (prev) => {
    const next: WorkspaceProfile[] = [];
    let replaced = false;
    for (const w of prev) {
      if (aliasSet.has(w.id)) continue;
      if (w.id === saved.id || (previous && w.id === previous.id)) {
        if (!replaced) {
          next.push(saved);
          replaced = true;
        }
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
    if (oldPath && !savedFolderSet.has(oldPath) && !otherOwnedFolders.has(oldPath)) {
      removedFolders.add(oldPath);
    }
  }
  for (const p of workspace.projects) {
    const wt = normalizeFolderPath(p.worktree);
    if (!wt || savedFolderSet.has(wt) || otherOwnedFolders.has(wt)) continue;
    if ((p.sandboxes || []).some((s) => savedFolderSet.has(stripWorktreePath(s)))) {
      removedFolders.add(wt);
    }
  }

  const hiddenSecondary = new Set(
    Array.from(savedSecondarySet).filter((p) => !otherPrimaryFolders.has(p))
  );
  for (const secPath of hiddenSecondary) deleteCachedProjectSessions(secPath);

  if (removedFolders.size > 0) {
    for (const oldPath of removedFolders) {
      recordDismissedProject(oldPath);
      deleteCachedProjectSessions(oldPath);
    }
    setWorkspace("projects", (prev) =>
      prev.filter((p) => !removedFolders.has(normalizeFolderPath(p.worktree)))
    );
  }
  if (removedFolders.size > 0 || hiddenSecondary.size > 0) {
    const nextRecents = workspace.recentProjects.filter((p) => {
      const norm = normalizeFolderPath(p);
      return !removedFolders.has(norm) && !hiddenSecondary.has(norm);
    });
    setWorkspace("recentProjects", nextRecents);
    writeStorageJSON(RECENT_PROJECTS_KEY, nextRecents);
  }

  if (targetSurface === "work") {
    for (const f of saved.folders || []) recordWorkWorkspaceDir(f.path);
    pruneWorkPathsFromCodeState();
    if (savedPrimary) setRememberedWorkSurface(savedPrimary, workspace.activeSessionID);
  } else {
    for (const f of saved.folders || []) unrecordWorkWorkspaceDir(f.path);
    const currentOrder = workspace.projectOrder.slice();
    const remCode = getRememberedCodeSurface();
    if (previousPrimary && savedPrimary && previousPrimary !== savedPrimary) {
      const prevIdx = currentOrder.findIndex((p) => normalizeFolderPath(p) === previousPrimary);
      if (prevIdx >= 0) currentOrder[prevIdx] = savedPrimary;
      if (remCode.rootDir === previousPrimary || removedFolders.has(remCode.rootDir)) {
        setRememberedCodeSurface(savedPrimary, null);
      }
    }
    if (
      (previousPrimary && savedPrimary && previousPrimary !== savedPrimary) ||
      removedFolders.size > 0 ||
      hiddenSecondary.size > 0
    ) {
      setProjectOrder(
        currentOrder.filter((p) => {
          const norm = normalizeFolderPath(p);
          return norm === savedPrimary || (!removedFolders.has(norm) && !hiddenSecondary.has(norm));
        })
      );
    }
    if (savedPrimary) recordRecentProject(savedPrimary);
  }
  if (saved?.id) {
    unrecordDismissedProject(saved.id);
    for (const f of saved.folders || []) unrecordDismissedProject(f.path);
  }

  const relocatedByOldRoot = new Map<string, string>();
  if (previous) {
    const prevByID = new Map(
      previous.folders
        .filter((f) => Boolean(f.id))
        .map((f) => [f.id, normalizeFolderPath(f.path)] as const)
    );
    for (const sf of saved.folders || []) {
      const oldPath = sf.id ? prevByID.get(sf.id) : undefined;
      const newPath = normalizeFolderPath(sf.path);
      if (oldPath && newPath && oldPath !== newPath && !savedFolderSet.has(oldPath)) {
        relocatedByOldRoot.set(oldPath, newPath);
      }
    }
  } else if (
    previousPrimary &&
    savedPrimary &&
    previousPrimary !== savedPrimary &&
    (saved.folders?.length ?? 0) === 1
  ) {
    relocatedByOldRoot.set(previousPrimary, savedPrimary);
  }
  if (relocatedByOldRoot.size > 0) {
    const ownedProjectIDs = new Set<string>([saved.id, ...(previous ? [previous.id] : []), ...aliasSet]);
    const rewriteDir = (rawDir: string | undefined): string | undefined => {
      if (!rawDir) return rawDir;
      const norm = normalizeFolderPath(rawDir);
      const root = stripWorktreePath(norm);
      const newRoot = root ? relocatedByOldRoot.get(root) : undefined;
      if (!newRoot) return rawDir;
      return norm === root ? newRoot : `${newRoot}${norm.slice(root.length)}`;
    };
    setWorkspace(
      produce((state) => {
        for (const s of state.sessions) {
          if (s.projectID && !ownedProjectIDs.has(s.projectID)) continue;
          const nextDir = rewriteDir(s.directory);
          if (nextDir && nextDir !== s.directory) s.directory = nextDir;
        }
        for (const [sid, entry] of Object.entries(state.sessionIndex)) {
          if (!entry || (entry.projectID && !ownedProjectIDs.has(entry.projectID))) continue;
          const nextDir = rewriteDir(entry.directory);
          if (nextDir && nextDir !== entry.directory) {
            state.sessionIndex[sid] = { ...entry, directory: nextDir };
          }
        }
        for (const [sid, wt] of Object.entries(state.worktreeBySession)) {
          if (!wt?.directory) continue;
          const idxEntry = state.sessionIndex[sid];
          if (idxEntry?.projectID && !ownedProjectIDs.has(idxEntry.projectID)) continue;
          const nextDir = rewriteDir(wt.directory);
          if (nextDir && nextDir !== wt.directory) {
            state.worktreeBySession[sid] = { ...wt, directory: nextDir };
          }
        }
      })
    );
  }

  const activeRoot = normalizeFolderPath(workspace.rootDir);
  if (
    savedPrimary &&
    activeRoot !== savedPrimary &&
    ((previousPrimary && activeRoot === previousPrimary) ||
      removedFolders.has(activeRoot) ||
      (savedSecondarySet.has(activeRoot) && !otherPrimaryFolders.has(activeRoot)))
  ) {
    await switchWorkspace(savedPrimary, {
      preferredSessionID: workspace.activeSessionID ?? undefined,
    });
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
  const sharedFolders = new Set<string>();
  for (const w of workspace.savedWorkspaces) {
    if (w.hidden || (existing && w.id === existing.id)) continue;
    for (const f of w.folders || []) {
      const nf = normalizeFolderPath(f.path);
      if (nf) sharedFolders.add(nf);
    }
  }
  const removedPrimary = normalizeFolderPath(workspacePrimaryFolder(existing)) || normInput;
  const removedKeys = new Set<string>();
  const addRemovedKey = (key: string) => {
    if (key && !sharedFolders.has(key)) removedKeys.add(key);
  };
  addRemovedKey(dirOrWorkspaceID);
  addRemovedKey(normInput);
  if (existing) {
    removedKeys.add(existing.id);
    for (const f of existing.folders) addRemovedKey(normalizeFolderPath(f.path));
  }
  for (const p of workspace.projects) {
    const wt = normalizeFolderPath(p.worktree);
    if (wt && (p.sandboxes || []).some((s) => removedKeys.has(stripWorktreePath(s)))) {
      removedKeys.add(wt);
    }
  }
  for (const k of removedKeys) {
    recordDismissedProject(k);
    unrecordWorkWorkspaceDir(k);
  }
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
  writeStorageJSON(RECENT_PROJECTS_KEY, nextRecents);
  const nextOrder = workspace.projectOrder.filter((p) => !removedKeys.has(normalizeFolderPath(p)));
  setWorkspace("projectOrder", nextOrder);
  writeStorageJSON(PROJECT_ORDER_KEY, nextOrder);
  const activeRoot = normalizeFolderPath(workspace.rootDir);
  if (removedKeys.has(activeRoot) || (removedPrimary && activeRoot === removedPrimary)) {
    if (surfaceMode() === "work") {
      const nextWork =
        workspacePrimaryFolder(visibleSavedWorkspaces("work")[0]) ||
        normalizeFolderPath(defaultWorkspaceDir());
      if (nextWork) await switchWorkspace(nextWork, { selectInitial: false });
    } else {
      const remainingSaved = visibleSavedWorkspaces("code").filter(
        (w) => !removedKeys.has(w.id) && !w.folders.some((f) => removedKeys.has(normalizeFolderPath(f.path)))
      );
      const nextCandidate =
        workspacePrimaryFolder(remainingSaved[0]) ||
        nextRecents.find(
          (p) =>
            !removedKeys.has(normalizeFolderPath(p)) &&
            normalizeFolderPath(p) !== removedPrimary &&
            !isWorkSurfacePath(p)
        ) ||
        workspace.projects.find(
          (p) => !removedKeys.has(normalizeFolderPath(p.worktree)) && !isWorkSurfacePath(p.worktree)
        )?.worktree;
      if (nextCandidate) await switchWorkspace(nextCandidate);
    }
  }
}

export function setProjectOrder(order: string[]) {
  const unique = Array.from(new Set(order.filter(isValidStoredProjectPath)));
  setWorkspace("projectOrder", unique);
  writeStorageJSON(PROJECT_ORDER_KEY, unique);
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
