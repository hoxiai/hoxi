// web/src/store/workspace-folders.ts
// Workspace profile / folder path queries and dismissed-project bookkeeping.
// Leaf module: depends only on workspace-state.
import { createSignal } from "solid-js";
import { unwrap } from "solid-js/store";
import type { WorkspaceFolder, WorkspaceProfile } from "../services/api";
import type { SessionInfo } from "../services/sse-types";
import { defaultWorkspaceDir } from "./layout";
import {
  DISMISSED_PROJECTS_KEY,
  PROJECT_ORDER_KEY,
  RECENT_PROJECTS_KEY,
  setWorkspace,
  workspace,
  type SessionIndexEntry,
} from "./workspace-state";

// Directly borrowed from Kilo Desktop: plugins/kilo-ui/src/contracts/workspaces/identity-color.ts
export const WORKSPACE_IDENTITY_COLORS = ["#5EA0F7", "#34D399", "#F87171", "#A78BFA", "#22D3EE", "#FBBF24"] as const;
export const DEFAULT_WORKSPACE_IDENTITY_COLOR = WORKSPACE_IDENTITY_COLORS[0];
const HEX_COLOR_RE = /^#[0-9A-Fa-f]{6}$/;

const MAX_PATH_CACHE = 4096;
const normalizedPathCache = new Map<string, string>();
const strippedPathCache = new Map<string, string>();

export const [sessionSurfaceRevision, setSessionSurfaceRevision] = createSignal(0);

export function bumpSessionSurfaceRevision(): void {
  setSessionSurfaceRevision((v) => v + 1);
}

export function normalizeWorkspaceIdentityColor(value?: string | null): string {
  const trimmed = (value || "").trim().toUpperCase();
  if (HEX_COLOR_RE.test(trimmed)) return trimmed;
  return DEFAULT_WORKSPACE_IDENTITY_COLOR;
}

export function isDismissedProjectPath(pathOrID?: string | null): boolean {
  if (!pathOrID) return false;
  const clean = pathOrID.trim();
  if (!clean) return false;
  const norm = normalizeFolderPath(clean);
  const dismissedSet = getFolderLookup().dismissedSet;
  return dismissedSet.has(clean) || Boolean(norm && dismissedSet.has(norm));
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
  if (!p) return "";
  const cached = normalizedPathCache.get(p);
  if (cached !== undefined) return cached;
  const trimmed = p.trim();
  let result = "";
  if (trimmed === "/") {
    result = "/";
  } else if (trimmed) {
    const stripped = trimmed.replace(/[\\/]+$/, "");
    if (/^[A-Za-z]:(?:$|[\\/])/.test(stripped)) {
      result = `${stripped[0].toUpperCase()}:${stripped.slice(2).replace(/\\+/g, "/")}`;
    } else {
      result = stripped;
    }
  }
  if (normalizedPathCache.size >= MAX_PATH_CACHE) normalizedPathCache.clear();
  normalizedPathCache.set(p, result);
  return result;
}

export function stripWorktreePath(p?: string | null): string {
  if (!p) return "";
  const cached = strippedPathCache.get(p);
  if (cached !== undefined) return cached;
  const norm = normalizeFolderPath(p);
  const result = norm ? norm.replace(/[\\/]\.hoxi[\\/]worktrees[\\/][^\\/]+$/, "") : "";
  if (strippedPathCache.size >= MAX_PATH_CACHE) strippedPathCache.clear();
  strippedPathCache.set(p, result);
  return result;
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

interface FolderLookup {
  dismissedSet: Set<string>;
  profileById: Map<string, WorkspaceProfile>;
  visibleProfileByDir: Map<string, WorkspaceProfile>;
  nonHiddenProfileByDir: Map<string, WorkspaceProfile>;
  anyProfileByDir: Map<string, WorkspaceProfile>;
  workDirsSet: Set<string>;
  sessionSurfaceByDir: Map<string, "work" | "code">;
  workSurfaceByDir: Map<string, boolean>;
}

let cachedLookup: FolderLookup | null = null;
let cachedDefDir = "";
let cachedWorkDirs: string[] | null = null;
let cachedSessRev = -1;
let cachedRawSessionIndex: Record<string, SessionIndexEntry> | null = null;
let cachedFirstSessionKey: string | undefined;
let cachedFirstSessionEntry: SessionIndexEntry | undefined;
let cachedSessionSurfaceByDir = new Map<string, "work" | "code">();
let cachedSavedItems: WorkspaceProfile[] = [];
let cachedSavedFolders: Array<WorkspaceFolder[] | undefined> = [];
let cachedSavedSurfaces: Array<WorkspaceProfile["surface"]> = [];
let cachedSavedHidden: Array<boolean | undefined> = [];
let cachedSavedNames: Array<string | undefined> = [];
let cachedSavedColors: Array<string | undefined> = [];
let cachedDismissedItems: string[] = [];

function buildProfileDirMap(list: WorkspaceProfile[]): Map<string, WorkspaceProfile> {
  const map = new Map<string, WorkspaceProfile>();
  for (const w of list) {
    for (const f of w.folders || []) {
      if (!f.isPrimary) continue;
      const fp = normalizeFolderPath(f.path);
      if (fp && !map.has(fp)) map.set(fp, w);
    }
  }
  for (const w of list) {
    for (const f of w.folders || []) {
      const fp = normalizeFolderPath(f.path);
      if (fp && !map.has(fp)) map.set(fp, w);
    }
  }
  return map;
}

function getFolderLookup(): FolderLookup {
  const defDir = normalizeFolderPath(defaultWorkspaceDir());
  const wDirs = workWorkspaceDirs();
  const sessRev = sessionSurfaceRevision();
  void workspace.savedWorkspaces.length;
  void workspace.dismissedProjects.length;

  const rawSaved = unwrap(workspace.savedWorkspaces) || [];
  const rawDismissed = unwrap(workspace.dismissedProjects) || [];
  const rawSessionIndex = unwrap(workspace.sessionIndex) || {};

  let firstSessionKey: string | undefined;
  for (const k in rawSessionIndex) {
    firstSessionKey = k;
    break;
  }
  const firstSessionEntry =
    firstSessionKey !== undefined ? rawSessionIndex[firstSessionKey] : undefined;
  const sessionIndexSame =
    sessRev === cachedSessRev &&
    rawSessionIndex === cachedRawSessionIndex &&
    firstSessionKey === cachedFirstSessionKey &&
    firstSessionEntry === cachedFirstSessionEntry;

  if (!sessionIndexSame) {
    const nextSessionSurface = new Map<string, "work" | "code">();
    for (const id in rawSessionIndex) {
      const entry = rawSessionIndex[id];
      if (!entry?.directory || (entry.surface !== "work" && entry.surface !== "code")) continue;
      const dir = stripWorktreePath(entry.directory);
      if (!dir) continue;
      if (entry.surface === "code") {
        nextSessionSurface.set(dir, "code");
      } else if (!nextSessionSurface.has(dir)) {
        nextSessionSurface.set(dir, "work");
      }
    }
    cachedSessionSurfaceByDir = nextSessionSurface;
    cachedSessRev = sessRev;
    cachedRawSessionIndex = rawSessionIndex;
    cachedFirstSessionKey = firstSessionKey;
    cachedFirstSessionEntry = firstSessionEntry;
  }

  let savedSame =
    cachedLookup !== null &&
    defDir === cachedDefDir &&
    wDirs === cachedWorkDirs &&
    sessionIndexSame &&
    rawSaved.length === cachedSavedItems.length &&
    rawDismissed.length === cachedDismissedItems.length;

  if (savedSame) {
    for (let i = 0; i < rawSaved.length; i++) {
      const w = rawSaved[i];
      if (
        w !== cachedSavedItems[i] ||
        w?.folders !== cachedSavedFolders[i] ||
        w?.surface !== cachedSavedSurfaces[i] ||
        w?.hidden !== cachedSavedHidden[i] ||
        w?.name !== cachedSavedNames[i] ||
        w?.color !== cachedSavedColors[i]
      ) {
        savedSame = false;
        break;
      }
    }
  }
  if (savedSame) {
    for (let i = 0; i < rawDismissed.length; i++) {
      if (rawDismissed[i] !== cachedDismissedItems[i]) {
        savedSame = false;
        break;
      }
    }
  }
  if (savedSame && cachedLookup) {
    return cachedLookup;
  }

  const dismissedSet = new Set<string>();
  for (const item of rawDismissed) {
    if (!item) continue;
    const clean = item.trim();
    if (clean) dismissedSet.add(clean);
    const norm = normalizeFolderPath(clean);
    if (norm) dismissedSet.add(norm);
  }

  const isDismissedId = (id?: string | null): boolean => {
    if (!id) return false;
    const clean = id.trim();
    if (!clean) return false;
    const norm = normalizeFolderPath(clean);
    return dismissedSet.has(clean) || Boolean(norm && dismissedSet.has(norm));
  };

  const proxySaved = workspace.savedWorkspaces;
  const allList: WorkspaceProfile[] = [];
  const nonHiddenList: WorkspaceProfile[] = [];
  const visibleList: WorkspaceProfile[] = [];
  const profileById = new Map<string, WorkspaceProfile>();

  for (let i = 0; i < rawSaved.length; i++) {
    const w = proxySaved[i] ?? rawSaved[i];
    if (!w) continue;
    allList.push(w);
    if (w.id && !profileById.has(w.id)) profileById.set(w.id, w);
    if (!w.hidden) {
      nonHiddenList.push(w);
      if (!isDismissedId(w.id)) {
        visibleList.push(w);
      }
    }
  }

  cachedDefDir = defDir;
  cachedWorkDirs = wDirs;
  cachedSavedItems = rawSaved.slice();
  cachedSavedFolders = rawSaved.map((w) => w?.folders);
  cachedSavedSurfaces = rawSaved.map((w) => w?.surface);
  cachedSavedHidden = rawSaved.map((w) => w?.hidden);
  cachedSavedNames = rawSaved.map((w) => w?.name);
  cachedSavedColors = rawSaved.map((w) => w?.color);
  cachedDismissedItems = rawDismissed.slice();

  cachedLookup = {
    dismissedSet,
    profileById,
    visibleProfileByDir: buildProfileDirMap(visibleList),
    nonHiddenProfileByDir: buildProfileDirMap(nonHiddenList),
    anyProfileByDir: buildProfileDirMap(allList),
    workDirsSet: new Set(wDirs),
    sessionSurfaceByDir: cachedSessionSurfaceByDir,
    workSurfaceByDir: new Map<string, boolean>(),
  };
  return cachedLookup;
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
  const lookup = getFolderLookup();
  const cached = lookup.workSurfaceByDir.get(norm);
  if (cached !== undefined) return cached;

  let result = false;
  if (DEFAULT_WORKSPACE_PATH_RE.test(norm) || (cachedDefDir && norm === cachedDefDir) || WORK_STORAGE_PATH_RE.test(norm)) {
    result = true;
  } else {
    const visOwner = lookup.visibleProfileByDir.get(norm);
    if (visOwner?.surface === "work") {
      result = true;
    } else if (visOwner?.surface === "code") {
      result = false;
    } else {
      const anyOwner = lookup.anyProfileByDir.get(norm);
      if (anyOwner?.surface === "work") {
        result = true;
      } else if (anyOwner?.surface === "code") {
        result = false;
      } else if (lookup.workDirsSet.has(norm)) {
        result = true;
      } else {
        result = lookup.sessionSurfaceByDir.get(norm) === "work";
      }
    }
  }
  lookup.workSurfaceByDir.set(norm, result);
  return result;
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
  return getFolderLookup().profileById.get(id);
}

export function findWorkspaceProfileByDir(dir?: string | null): WorkspaceProfile | undefined {
  const norm = stripWorktreePath(dir);
  if (!norm) return undefined;
  const lookup = getFolderLookup();
  return lookup.nonHiddenProfileByDir.get(norm) ?? lookup.anyProfileByDir.get(norm);
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
