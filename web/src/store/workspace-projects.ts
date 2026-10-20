import { produce } from "solid-js/store";
import { hoxiAPI, type SaveWorkspaceInput, type WorkspaceFolder, type WorkspaceProfile } from "../services/api";
import { DISMISSED_PROJECTS_KEY, isSessionArchived, PROJECT_ORDER_KEY, RECENT_PROJECTS_KEY, setWorkspace, workspace } from "./workspace";
import { loadAgents, loadProviders } from "./workspace-providers";
import { isPlaceholderSessionTitle, preserveSessionTitle, sortSessionsByActivity } from "./session-title";
import { syncPendingPermissions } from "./approval-mode";
import { syncSessionStatuses } from "./session-status-sync";
import { isSessionBusy, isSessionDeleted, loadSessionMessagesOnly, reconcileSessionsInPlace, refreshWorktreeStatus, selectSession } from "./workspace-sessions";
import type { SessionInfo } from "../services/sse-types";

// Directly borrowed from Kilo Desktop: plugins/kilo-ui/src/contracts/workspaces/identity-color.ts
export const WORKSPACE_IDENTITY_COLORS = ["#5EA0F7", "#34D399", "#F87171", "#A78BFA", "#22D3EE", "#FBBF24"] as const;
export const DEFAULT_WORKSPACE_IDENTITY_COLOR = WORKSPACE_IDENTITY_COLORS[0];
const HEX_COLOR_RE = /^#[0-9A-Fa-f]{6}$/;

export function normalizeWorkspaceIdentityColor(value?: string | null): string {
  const trimmed = (value || "").trim().toUpperCase();
  if (HEX_COLOR_RE.test(trimmed)) return trimmed;
  return DEFAULT_WORKSPACE_IDENTITY_COLOR;
}

export function isDismissedProjectPath(pathOrID?: string | null): boolean {
  if (!pathOrID) return false;
  const clean = pathOrID.trim();
  const norm = normalizeFolderPath(clean);
  const list = workspace.dismissedProjects || [];
  return list.some((item) => {
    if (!item) return false;
    if (item === clean) return true;
    const itemNorm = normalizeFolderPath(item);
    return Boolean(itemNorm && norm && itemNorm === norm);
  });
}

export function recordDismissedProject(pathOrID?: string | null): void {
  if (!pathOrID) return;
  const clean = pathOrID.trim();
  const norm = normalizeFolderPath(clean);
  const next = [...(workspace.dismissedProjects || [])];
  if (clean && !next.includes(clean)) next.push(clean);
  if (norm && !next.includes(norm)) next.push(norm);
  setWorkspace("dismissedProjects", next);
  try { localStorage.setItem(DISMISSED_PROJECTS_KEY, JSON.stringify(next)); } catch { /* ignore */ }
}

export function unrecordDismissedProject(pathOrID?: string | null): void {
  if (!pathOrID) return;
  const clean = pathOrID.trim();
  const norm = normalizeFolderPath(clean);
  const next = (workspace.dismissedProjects || []).filter(
    (item) => item !== clean && normalizeFolderPath(item) !== norm
  );
  setWorkspace("dismissedProjects", next);
  try { localStorage.setItem(DISMISSED_PROJECTS_KEY, JSON.stringify(next)); } catch { /* ignore */ }
}

export function normalizeFolderPath(p?: string | null): string {
  const trimmed = (p || "").trim();
  if (!trimmed) return "";
  if (trimmed === "/") return "/";
  return trimmed.replace(/[\\/]+$/, "");
}

export function shortenPath(p: string): string {
  if (!p) return "";
  const winMatch = p.match(/^[A-Za-z]:[\\/]Users[\\/]([^\\/]+)([\\/].*)?$/);
  if (winMatch) {
    return "~" + (winMatch[2] || "");
  }
  const unixMatch = p.match(/^\/(?:Users|home)\/([^/]+)(\/.*)?$/);
  if (unixMatch) {
    return "~" + (unixMatch[2] || "");
  }
  return p;
}

export function isInvalidProjectPath(p?: string | null): boolean {
  const norm = normalizeFolderPath(p);
  if (!norm || norm === "." || norm === "/" || norm === "\\") return true;
  if (/^[A-Za-z]:$/.test(norm)) return true;
  if (norm.includes(".app/Contents")) return true;
  return false;
}

export function resolveDefaultProjectDir(preferredRoot?: string | null): string {
  const pref = normalizeFolderPath(preferredRoot);
  if (pref && !isInvalidProjectPath(pref) && !isDismissedProjectPath(pref)) {
    return pref;
  }
  for (const saved of workspace.savedWorkspaces) {
    if (isDismissedProjectPath(saved.id)) continue;
    const primary = normalizeFolderPath(workspacePrimaryFolder(saved));
    if (primary && !isInvalidProjectPath(primary) && !isDismissedProjectPath(primary)) {
      return primary;
    }
  }
  for (const recent of workspace.recentProjects) {
    const clean = normalizeFolderPath(recent);
    if (clean && !isInvalidProjectPath(clean) && !isDismissedProjectPath(clean)) {
      return clean;
    }
  }
  for (const proj of workspace.projects) {
    const wt = normalizeFolderPath(proj.worktree);
    if (wt && !isInvalidProjectPath(wt) && !isDismissedProjectPath(wt)) {
      return wt;
    }
  }
  return "";
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
  if (!workspace.rootDir || isInvalidProjectPath(workspace.rootDir)) return [];
  return [{ id: "primary", path: workspace.rootDir, isPrimary: true, position: 0 }];
}

export function workspaceFoldersForDir(dir: string): WorkspaceFolder[] {
  const profile = findWorkspaceProfileByDir(dir);
  if (profile && profile.folders.length > 0) {
    return profile.folders;
  }
  const cleaned = normalizeFolderPath(dir);
  if (!cleaned || isInvalidProjectPath(cleaned)) return [];
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

export async function bootstrapProjectSessions(directory: string, selectInitial = true): Promise<void> {
  const cleaned = normalizeFolderPath(directory);
  if (!cleaned || isInvalidProjectPath(cleaned)) {
    setWorkspace("bootstrappingSessions", false);
    return;
  }
  let rawSessions: SessionInfo[];
  try {
    rawSessions = await hoxiAPI.listSessions(cleaned);
  } catch {
    await new Promise((r) => setTimeout(r, 400));
    rawSessions = await hoxiAPI.listSessions(cleaned);
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
    bootstrappingSessions: hydratedSessions.length === 0 && !targetSessionID,
  });

  const selectPromise = targetSessionID ? selectSession(targetSessionID) : null;

  try {
    await Promise.all([
      bootstrapProjectSessions(cleaned, selectInitial),
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
