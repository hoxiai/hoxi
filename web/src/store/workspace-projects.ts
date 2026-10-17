import {
  hoxiAPI,
  type SaveWorkspaceInput,
  type WorkspaceFolder,
  type WorkspaceProfile,
} from "../services/api";
import {
  isSessionArchived,
  PROJECT_ORDER_KEY,
  RECENT_PROJECTS_KEY,
  setWorkspace,
  workspace,
} from "./workspace";
import { loadAgents, loadProviders } from "./workspace-providers";
import {
  isSessionDeleted,
  refreshWorktreeStatus,
  selectSession,
} from "./workspace-sessions";

// Directly borrowed from Kilo Desktop: plugins/kilo-ui/src/contracts/workspaces/identity-color.ts
export const WORKSPACE_IDENTITY_COLORS = [
  "#5EA0F7",
  "#34D399",
  "#F87171",
  "#A78BFA",
  "#22D3EE",
  "#FBBF24",
] as const;

export const DEFAULT_WORKSPACE_IDENTITY_COLOR = WORKSPACE_IDENTITY_COLORS[0];

const HEX_COLOR_RE = /^#[0-9A-Fa-f]{6}$/;

export function normalizeWorkspaceIdentityColor(value?: string | null): string {
  const trimmed = (value || "").trim().toUpperCase();
  if (HEX_COLOR_RE.test(trimmed)) return trimmed;
  return DEFAULT_WORKSPACE_IDENTITY_COLOR;
}

export function normalizeFolderPath(p?: string | null): string {
  const trimmed = (p || "").trim();
  if (!trimmed) return "";
  if (trimmed === "/") return "/";
  return trimmed.replace(/[\\/]+$/, "");
}

export function findWorkspaceProfileById(id?: string | null): WorkspaceProfile | undefined {
  if (!id) return undefined;
  return workspace.savedWorkspaces.find((w) => w.id === id);
}

export function findWorkspaceProfileByDir(dir?: string | null): WorkspaceProfile | undefined {
  const norm = normalizeFolderPath(dir);
  if (!norm) return undefined;
  const primaryMatch = workspace.savedWorkspaces.find((w) =>
    w.folders.some((f) => f.isPrimary && normalizeFolderPath(f.path) === norm)
  );
  if (primaryMatch) return primaryMatch;
  return workspace.savedWorkspaces.find((w) =>
    w.folders.some((f) => normalizeFolderPath(f.path) === norm)
  );
}

export function activeWorkspaceProfile(): WorkspaceProfile | undefined {
  return findWorkspaceProfileByDir(workspace.rootDir);
}

export function workspacePrimaryFolder(profile?: WorkspaceProfile | null): string {
  if (!profile || profile.folders.length === 0) return "";
  const primary = profile.folders.find((f) => f.isPrimary) ?? profile.folders[0];
  return primary?.path || "";
}

export function activeWorkspaceFolders(): WorkspaceFolder[] {
  const profile = activeWorkspaceProfile();
  if (profile && profile.folders.length > 0) {
    return profile.folders;
  }
  if (!workspace.rootDir) return [];
  return [
    {
      id: "primary",
      path: workspace.rootDir,
      isPrimary: true,
      position: 0,
    },
  ];
}

export function workspaceFoldersForDir(dir: string): WorkspaceFolder[] {
  const profile = findWorkspaceProfileByDir(dir);
  if (profile && profile.folders.length > 0) {
    return profile.folders;
  }
  const cleaned = normalizeFolderPath(dir);
  if (!cleaned) return [];
  return [{ id: "primary", path: cleaned, isPrimary: true, position: 0 }];
}

export function workspaceDisplayName(dir: string, fallbackName?: string): string {
  const profile = findWorkspaceProfileByDir(dir);
  if (profile?.name) return profile.name;
  if (fallbackName) return fallbackName;
  const parts = normalizeFolderPath(dir).split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || dir;
}

export function workspaceIdentityColor(dir: string, fallbackColor?: string): string | undefined {
  const profile = findWorkspaceProfileByDir(dir);
  if (profile?.color) return profile.color;
  return fallbackColor;
}

// Directly borrowed from Kilo Desktop: multi-folder workspace system prompt builder (lwe)
export function buildWorkspaceFoldersSystemPrompt(
  folders: Array<{ path: string; isPrimary: boolean }> = activeWorkspaceFolders()
): string | undefined {
  const primary = folders.find((f) => f.isPrimary) ?? folders[0];
  if (!primary || folders.length <= 1) return undefined;
  const additional = folders.filter(
    (f) => normalizeFolderPath(f.path) !== normalizeFolderPath(primary.path)
  );
  if (additional.length === 0) return undefined;
  return [
    "Workspace context for this session:",
    `- Primary folder (default working directory): ${JSON.stringify(primary.path)}`,
    ...additional.map((f) => `- Additional workspace folder: ${JSON.stringify(f.path)}`),
    "All listed workspace folders are in scope and may be read from and written to.",
    "Use the primary folder as the default working directory, but consider every listed folder when locating or changing relevant files.",
  ].join("\n");
}

export async function loadSavedWorkspaces(): Promise<void> {
  try {
    const res = await hoxiAPI.listWorkspaces();
    if (Array.isArray(res?.workspaces)) {
      setWorkspace("savedWorkspaces", res.workspaces);
    }
  } catch (err) {
    console.error("Failed to load saved workspaces:", err);
  }
}

export async function loadProjects(): Promise<void> {
  try {
    const remote = await hoxiAPI.listProjects();
    const list = Array.isArray(remote) ? remote : [];
    setWorkspace("projects", list);
  } catch (err) {
    console.error("Failed to load projects:", err);
  }
}

export function recordRecentProject(directory: string) {
  const cleaned = normalizeFolderPath(directory);
  if (!cleaned) return;
  const recents = workspace.recentProjects.filter(
    (p) => normalizeFolderPath(p) !== cleaned
  );
  const updatedRecents = [cleaned, ...recents].slice(0, 20);
  setWorkspace("recentProjects", updatedRecents);
  try {
    localStorage.setItem(RECENT_PROJECTS_KEY, JSON.stringify(updatedRecents));
  } catch {
    /* ignore */
  }
}

export async function switchWorkspace(directory: string): Promise<void> {
  const cleaned = normalizeFolderPath(directory);
  if (!cleaned) return;

  recordRecentProject(cleaned);

  setWorkspace({
    rootDir: cleaned,
    activeInspectorFolder: null,
    activeSessionID: null,
    messagesBySession: {},
    diffsBySession: {},
    worktreeBySession: {},
    reviewCommentsBySession: {},
    pendingPermissions: [],
  });

  try {
    const rawSessions = await hoxiAPI.listSessions(cleaned);
    const sessions = rawSessions.filter((s) => !isSessionDeleted(s.id));
    setWorkspace("sessions", sessions);

    void refreshWorktreeStatus("__root__");
    await Promise.allSettled(sessions.map((s) => refreshWorktreeStatus(s.id)));

    const unarchived = sessions.filter((s) => !isSessionArchived(s));
    if (unarchived.length > 0) {
      const mostRecent = [...unarchived].sort((a, b) => b.time.created - a.time.created)[0];
      await selectSession(mostRecent.id);
    }
  } catch (err) {
    console.error("switch workspace error:", err);
  }

  void loadProviders();
  void loadProjects();
  void loadSavedWorkspaces();
  void loadAgents(cleaned);
}

export async function openWorkspaceFolder(): Promise<void> {
  try {
    const res = await hoxiAPI.openDirectoryDialog();
    if (res?.path && res.path.trim()) {
      await switchWorkspace(res.path.trim());
    }
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
  const byId = findWorkspaceProfileById(dirOrWorkspaceID);
  const byDir = byId ?? findWorkspaceProfileByDir(dirOrWorkspaceID);
  setWorkspace({
    workspaceModalMode: "edit",
    editingWorkspaceID: byDir?.id ?? null,
    editingWorkspaceDir: byDir ? workspacePrimaryFolder(byDir) : normalizeFolderPath(dirOrWorkspaceID),
  });
}

export function openDeleteWorkspaceModal(dirOrWorkspaceID: string) {
  const byId = findWorkspaceProfileById(dirOrWorkspaceID);
  const byDir = byId ?? findWorkspaceProfileByDir(dirOrWorkspaceID);
  setWorkspace({
    workspaceModalMode: "delete",
    editingWorkspaceID: byDir?.id ?? null,
    editingWorkspaceDir: byDir ? workspacePrimaryFolder(byDir) : normalizeFolderPath(dirOrWorkspaceID),
  });
}

export function openManageWorkspacesModal() {
  setWorkspace({
    workspaceModalMode: "manage",
    editingWorkspaceID: null,
    editingWorkspaceDir: null,
  });
}

export function closeWorkspaceModal() {
  setWorkspace({
    workspaceModalMode: null,
    editingWorkspaceID: null,
    editingWorkspaceDir: null,
  });
}

export async function saveWorkspaceProfile(
  input: SaveWorkspaceInput,
  existingID?: string | null
): Promise<WorkspaceProfile> {
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
  if (primaryPath) {
    recordRecentProject(primaryPath);
  }
  return saved;
}

export async function quickAddFolderToWorkspace(dirOrWorkspaceID: string): Promise<void> {
  try {
    const picked = await hoxiAPI.openDirectoryDialog();
    const newPath = normalizeFolderPath(picked?.path);
    if (!newPath) return;

    const existing =
      findWorkspaceProfileById(dirOrWorkspaceID) ??
      findWorkspaceProfileByDir(dirOrWorkspaceID);

    if (existing) {
      const alreadyIn = existing.folders.some(
        (f) => normalizeFolderPath(f.path) === newPath
      );
      if (alreadyIn) return;
      const folders = [
        ...existing.folders.map((f) => ({ path: f.path, isPrimary: f.isPrimary })),
        { path: newPath, isPrimary: false },
      ];
      await saveWorkspaceProfile(
        {
          name: existing.name,
          color: existing.color,
          folders,
        },
        existing.id
      );
    } else {
      const primaryDir = normalizeFolderPath(dirOrWorkspaceID) || workspace.rootDir;
      const folders =
        primaryDir && primaryDir !== newPath
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
  const existing =
    findWorkspaceProfileById(dirOrWorkspaceID) ??
    findWorkspaceProfileByDir(dirOrWorkspaceID);

  const removedDirs = new Set<string>();
  const normInput = normalizeFolderPath(dirOrWorkspaceID);
  if (normInput) removedDirs.add(normInput);
  if (existing) {
    for (const f of existing.folders) {
      removedDirs.add(normalizeFolderPath(f.path));
    }
    try {
      await hoxiAPI.deleteWorkspace(existing.id);
    } catch (err) {
      console.warn("deleteWorkspace API warning:", err);
    }
    setWorkspace("savedWorkspaces", (prev) => prev.filter((w) => w.id !== existing.id));
  }

  const nextRecents = workspace.recentProjects.filter(
    (p) => !removedDirs.has(normalizeFolderPath(p))
  );
  setWorkspace("recentProjects", nextRecents);
  try {
    localStorage.setItem(RECENT_PROJECTS_KEY, JSON.stringify(nextRecents));
  } catch {
    /* ignore */
  }

  const nextOrder = workspace.projectOrder.filter(
    (p) => !removedDirs.has(normalizeFolderPath(p))
  );
  setWorkspace("projectOrder", nextOrder);
  try {
    localStorage.setItem(PROJECT_ORDER_KEY, JSON.stringify(nextOrder));
  } catch {
    /* ignore */
  }

  if (removedDirs.has(normalizeFolderPath(workspace.rootDir))) {
    const nextCandidate =
      workspacePrimaryFolder(workspace.savedWorkspaces[0]) ||
      nextRecents[0] ||
      workspace.projects.find((p) => !removedDirs.has(normalizeFolderPath(p.worktree)))?.worktree;
    if (nextCandidate) {
      await switchWorkspace(nextCandidate);
    }
  }
}
