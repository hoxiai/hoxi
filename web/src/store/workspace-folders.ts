// web/src/store/workspace-folders.ts
// Workspace profile / folder path queries and dismissed-project bookkeeping.
// Leaf module: depends only on workspace-state.
import { createSignal } from "solid-js";
import type { WorkspaceFolder, WorkspaceProfile } from "../services/api";
import type { SessionInfo } from "../services/sse-types";
import { defaultWorkspaceDir } from "./layout";
import {
  DISMISSED_PROJECTS_KEY,
  PROJECT_ORDER_KEY,
  RECENT_PROJECTS_KEY,
  setWorkspace,
  workspace,
} from "./workspace-state";

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
  const stripped = trimmed.replace(/[\\/]+$/, "");
  if (/^[A-Za-z]:(?:$|[\\/])/.test(stripped)) {
    return `${stripped[0].toUpperCase()}:${stripped.slice(2).replace(/\\+/g, "/")}`;
  }
  return stripped;
}

export function stripWorktreePath(p?: string | null): string {
  const norm = normalizeFolderPath(p);
  if (!norm) return "";
  return norm.replace(/[\\/]\.hoxi[\\/]worktrees[\\/][^\\/]+$/, "");
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

const HOME_ROOT_RE = /^(?:\/(?:Users|home)\/[^/]+|[A-Za-z]:\/Users\/[^/]+)$/i;
const APP_INSTALL_OR_SYSTEM_PATH_RE =
  /(?:\/AppData\/Local\/Programs\/Hoxi(?:$|\/)|\/Program Files(?: \(x86\))?\/Hoxi(?:$|\/)|^[A-Za-z]:\/Windows(?:$|\/))/i;

export function isInvalidProjectPath(p?: string | null): boolean {
  const norm = normalizeFolderPath(p);
  if (!norm || norm === "." || norm === "/" || norm === "\\") return true;
  if (/^[A-Za-z]:$/.test(norm)) return true;
  if (norm.includes(".app/Contents")) return true;
  if (HOME_ROOT_RE.test(norm)) return true;
  if (APP_INSTALL_OR_SYSTEM_PATH_RE.test(norm)) return true;
  return false;
}

const DEFAULT_WORKSPACE_PATH_RE = /(?:^|[\\/])\.hoxi[\\/]workspaces[\\/]default(?:$|[\\/])/i;
const WORK_STORAGE_PATH_RE = /(?:^|[\\/])\.hoxi[\\/]workspaces(?:$|[\\/])/i;
export const WORK_WORKSPACE_DIRS_KEY = "hoxi:work-workspace-dirs";
const REMEMBERED_CODE_SURFACE_KEY = "hoxi:remembered-code-surface";
const REMEMBERED_WORK_SURFACE_KEY = "hoxi:remembered-work-surface";

function initialWorkWorkspaceDirs(): string[] {
  try {
    const raw = localStorage.getItem(WORK_WORKSPACE_DIRS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      return Array.from(new Set(parsed.map((p) => stripWorktreePath(String(p))).filter(Boolean)));
    }
  } catch { /* ignore */ }
  return [];
}

const [workWorkspaceDirs, setWorkWorkspaceDirs] = createSignal<string[]>(initialWorkWorkspaceDirs());

export function recordWorkWorkspaceDir(pathOrDir?: string | null): void {
  const norm = stripWorktreePath(pathOrDir);
  if (!norm || isInvalidProjectPath(norm) || isDefaultWorkspacePath(norm)) return;
  const current = workWorkspaceDirs();
  if (current.includes(norm)) return;
  const next = [...current, norm];
  setWorkWorkspaceDirs(next);
  try { localStorage.setItem(WORK_WORKSPACE_DIRS_KEY, JSON.stringify(next)); } catch { /* ignore */ }
}

export function unrecordWorkWorkspaceDir(pathOrDir?: string | null): void {
  const norm = stripWorktreePath(pathOrDir);
  if (!norm) return;
  const current = workWorkspaceDirs();
  if (!current.includes(norm)) return;
  const next = current.filter((item) => item !== norm);
  setWorkWorkspaceDirs(next);
  try { localStorage.setItem(WORK_WORKSPACE_DIRS_KEY, JSON.stringify(next)); } catch { /* ignore */ }
}

export function isDefaultWorkspacePath(p?: string | null): boolean {
  const norm = stripWorktreePath(p);
  if (!norm) return false;
  if (DEFAULT_WORKSPACE_PATH_RE.test(norm)) return true;
  const def = normalizeFolderPath(defaultWorkspaceDir());
  return Boolean(def && norm === def);
}

export function isWorkSurfacePath(p?: string | null): boolean {
  const norm = stripWorktreePath(p);
  if (!norm) return false;
  if (isDefaultWorkspacePath(norm) || WORK_STORAGE_PATH_RE.test(norm)) return true;
  const visible = workspace.savedWorkspaces.filter((w) => !w.hidden && !isDismissedProjectPath(w.id));
  const matchIn = (list: WorkspaceProfile[]) =>
    list.find((w) => w.folders.some((f) => f.isPrimary && normalizeFolderPath(f.path) === norm)) ??
    list.find((w) => w.folders.some((f) => normalizeFolderPath(f.path) === norm));
  const visOwner = matchIn(visible);
  if (visOwner?.surface === "work") return true;
  if (visOwner?.surface === "code") return false;
  const anyOwner = matchIn(workspace.savedWorkspaces);
  if (anyOwner?.surface === "work") return true;
  if (anyOwner?.surface === "code") return false;
  if (workWorkspaceDirs().includes(norm)) return true;
  let hasWorkSession = false;
  for (const entry of Object.values(workspace.sessionIndex)) {
    if (!entry?.directory || stripWorktreePath(entry.directory) !== norm) continue;
    if (entry.surface === "code") return false;
    if (entry.surface === "work") hasWorkSession = true;
  }
  return hasWorkSession;
}

export interface RememberedSurfaceState {
  rootDir: string;
  sessionID: string | null;
  cachedSessions?: SessionInfo[];
}

function readRememberedSurface(storageKey: string, expectWork: boolean): RememberedSurfaceState {
  try {
    const raw = localStorage.getItem(storageKey);
    if (!raw) return { rootDir: "", sessionID: null };
    const parsed = JSON.parse(raw);
    const rootDir = normalizeFolderPath(parsed?.rootDir);
    if (
      rootDir &&
      !isInvalidProjectPath(rootDir) &&
      (expectWork ? true : !isWorkSurfacePath(rootDir))
    ) {
      return {
        rootDir,
        sessionID: typeof parsed?.sessionID === "string" ? parsed.sessionID : null,
      };
    }
  } catch { /* ignore */ }
  return { rootDir: "", sessionID: null };
}

function writeRememberedSurface(storageKey: string, state: { rootDir: string; sessionID: string | null }): void {
  try { localStorage.setItem(storageKey, JSON.stringify(state)); } catch { /* ignore */ }
}

let rememberedCodeSurface: RememberedSurfaceState = readRememberedSurface(REMEMBERED_CODE_SURFACE_KEY, false);
let rememberedWorkSurface: RememberedSurfaceState = readRememberedSurface(REMEMBERED_WORK_SURFACE_KEY, true);

export function getRememberedCodeSurface(): RememberedSurfaceState {
  if (rememberedCodeSurface.rootDir && isWorkSurfacePath(rememberedCodeSurface.rootDir)) {
    rememberedCodeSurface = { rootDir: "", sessionID: null };
    writeRememberedSurface(REMEMBERED_CODE_SURFACE_KEY, { rootDir: "", sessionID: null });
  }
  return rememberedCodeSurface;
}

export function setRememberedCodeSurface(
  rootDir: string,
  sessionID: string | null = null,
  cachedSessions?: SessionInfo[]
): void {
  const clean = normalizeFolderPath(rootDir);
  if (clean && !isInvalidProjectPath(clean) && !isWorkSurfacePath(clean)) {
    const sameDir = rememberedCodeSurface.rootDir === clean;
    const preservedSessionID = sessionID ?? (sameDir ? rememberedCodeSurface.sessionID : null);
    rememberedCodeSurface = {
      rootDir: clean,
      sessionID: preservedSessionID,
      cachedSessions: cachedSessions ?? (sameDir ? rememberedCodeSurface.cachedSessions : undefined),
    };
    writeRememberedSurface(REMEMBERED_CODE_SURFACE_KEY, { rootDir: clean, sessionID: preservedSessionID });
  }
}

export function getRememberedWorkSurface(): RememberedSurfaceState {
  return rememberedWorkSurface;
}

export function setRememberedWorkSurface(
  rootDir: string,
  sessionID: string | null = null,
  cachedSessions?: SessionInfo[]
): void {
  const clean = normalizeFolderPath(rootDir);
  if (clean && !isInvalidProjectPath(clean)) {
    recordWorkWorkspaceDir(clean);
    const sameDir = rememberedWorkSurface.rootDir === clean;
    const preservedSessionID = sessionID ?? (sameDir ? rememberedWorkSurface.sessionID : null);
    rememberedWorkSurface = {
      rootDir: clean,
      sessionID: preservedSessionID,
      cachedSessions: cachedSessions ?? (sameDir ? rememberedWorkSurface.cachedSessions : undefined),
    };
    writeRememberedSurface(REMEMBERED_WORK_SURFACE_KEY, { rootDir: clean, sessionID: preservedSessionID });
  }
}

export function getActiveWorkWorkspaceDir(): string {
  const rem = normalizeFolderPath(rememberedWorkSurface.rootDir);
  if (rem && !isInvalidProjectPath(rem) && !isDismissedProjectPath(rem) && isWorkSurfacePath(rem)) {
    return rem;
  }
  const firstSaved = visibleSavedWorkspaces("work")[0];
  const savedPrimary = normalizeFolderPath(workspacePrimaryFolder(firstSaved));
  if (savedPrimary && !isInvalidProjectPath(savedPrimary) && !isDismissedProjectPath(savedPrimary)) {
    return savedPrimary;
  }
  return normalizeFolderPath(defaultWorkspaceDir());
}

export function getInitialCodeProjectDir(): string {
  return normalizeFolderPath(getRememberedCodeSurface().rootDir || resolveDefaultProjectDir(""));
}

export function pruneWorkPathsFromCodeState(): void {
  for (const ws of workspace.savedWorkspaces) {
    if (ws.surface === "work") {
      for (const f of ws.folders || []) recordWorkWorkspaceDir(f.path);
    }
  }
  const filteredRecents = workspace.recentProjects.filter((p) => !isWorkSurfacePath(p));
  if (filteredRecents.length !== workspace.recentProjects.length) {
    setWorkspace("recentProjects", filteredRecents);
    try { localStorage.setItem(RECENT_PROJECTS_KEY, JSON.stringify(filteredRecents)); } catch { /* ignore */ }
  }
  const filteredOrder = workspace.projectOrder.filter((p) => !isWorkSurfacePath(p));
  if (filteredOrder.length !== workspace.projectOrder.length) {
    setWorkspace("projectOrder", filteredOrder);
    try { localStorage.setItem(PROJECT_ORDER_KEY, JSON.stringify(filteredOrder)); } catch { /* ignore */ }
  }
  if (workspace.projects.some((p) => isWorkSurfacePath(p.worktree))) {
    setWorkspace("projects", (prev) => prev.filter((p) => !isWorkSurfacePath(p.worktree)));
  }
  getRememberedCodeSurface();
}

/** Saved workspaces the user can see, scoped by surface ("code" by default). */
export function visibleSavedWorkspaces(surface: "code" | "work" = "code"): WorkspaceProfile[] {
  return workspace.savedWorkspaces.filter((saved) => {
    if (saved.hidden || isDismissedProjectPath(saved.id)) return false;
    const primary = normalizeFolderPath(workspacePrimaryFolder(saved));
    const isWork = saved.surface === "work" || (saved.surface !== "code" && isWorkSurfacePath(primary));
    return surface === "work" ? isWork : !isWork;
  });
}

export function resolveDefaultProjectDir(preferredRoot?: string | null): string {
  const pref = normalizeFolderPath(preferredRoot);
  if (pref && !isInvalidProjectPath(pref) && !isDismissedProjectPath(pref) && !isWorkSurfacePath(pref)) {
    return pref;
  }
  for (const saved of visibleSavedWorkspaces("code")) {
    const primary = normalizeFolderPath(workspacePrimaryFolder(saved));
    if (primary && !isInvalidProjectPath(primary) && !isDismissedProjectPath(primary) && !isWorkSurfacePath(primary)) {
      return primary;
    }
  }
  for (const recent of workspace.recentProjects) {
    const clean = normalizeFolderPath(recent);
    if (clean && !isInvalidProjectPath(clean) && !isDismissedProjectPath(clean) && !isWorkSurfacePath(clean)) {
      return clean;
    }
  }
  for (const proj of workspace.projects) {
    const wt = normalizeFolderPath(proj.worktree);
    if (wt && !isInvalidProjectPath(wt) && !isDismissedProjectPath(wt) && !isWorkSurfacePath(wt)) {
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
  const norm = stripWorktreePath(dir);
  if (!norm) return undefined;
  const visible = workspace.savedWorkspaces.filter((w) => !w.hidden);
  const matchIn = (list: WorkspaceProfile[]) =>
    list.find((w) => w.folders.some((f) => f.isPrimary && normalizeFolderPath(f.path) === norm)) ??
    list.find((w) => w.folders.some((f) => normalizeFolderPath(f.path) === norm));
  return matchIn(visible) ?? matchIn(workspace.savedWorkspaces);
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
  const cleaned = normalizeFolderPath(dir);
  const profile = findWorkspaceProfileByDir(cleaned);
  if (profile && profile.folders.length > 0) {
    if (cleaned && stripWorktreePath(cleaned) !== cleaned) {
      return profile.folders.map((f) =>
        f.isPrimary ? { ...f, path: cleaned } : f
      );
    }
    return profile.folders;
  }
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
  if (!primary?.path || isInvalidProjectPath(primary.path)) return undefined;
  const additional = folders.filter(
    (f) => normalizeFolderPath(f.path) !== normalizeFolderPath(primary.path)
  );
  if (additional.length === 0) {
    return [
      "Workspace context for this session:",
      `- Primary folder (default working directory): ${JSON.stringify(primary.path)}`,
    ].join("\n");
  }
  return [
    "Workspace context for this session:",
    `- Primary folder (default working directory): ${JSON.stringify(primary.path)}`,
    ...additional.map((f) => `- Additional workspace folder: ${JSON.stringify(f.path)}`),
    "All listed workspace folders are in scope and may be read from and written to.",
    "Use the primary folder as the default working directory, but consider every listed folder when locating or changing relevant files.",
  ].join("\n");
}

export function secondaryWorkspaceDirs(primaryDir?: string): string[] {
  const baseNorm = normalizeFolderPath(primaryDir || workspace.rootDir || "");
  if (!baseNorm) return [];
  const folders = workspaceFoldersForDir(baseNorm);
  if (folders.length <= 1) return [];
  const out: string[] = [];
  const seen = new Set<string>([baseNorm]);
  for (const f of folders) {
    const norm = normalizeFolderPath(f.path);
    if (!norm || seen.has(norm)) continue;
    seen.add(norm);
    out.push(norm);
  }
  return out;
}
