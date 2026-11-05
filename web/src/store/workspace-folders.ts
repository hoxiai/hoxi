// web/src/store/workspace-folders.ts
// Workspace profile / folder path queries and dismissed-project bookkeeping.
// Leaf module: depends only on workspace-state.
import { createSignal, getListener } from "solid-js";
import { unwrap } from "solid-js/store";
import type { WorkspaceFolder, WorkspaceProfile } from "../services/api";
import type { SessionInfo } from "../services/sse-types";
import { defaultWorkspaceDir } from "./layout";
import {
  DISMISSED_PROJECTS_KEY,
  PROJECT_ORDER_KEY,
  readStorageJSON,
  RECENT_PROJECTS_KEY,
  setWorkspace,
  workspace,
  writeStorageJSON,
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
  writeStorageJSON(DISMISSED_PROJECTS_KEY, next);
}

export function unrecordDismissedProject(pathOrID?: string | null): void {
  if (!pathOrID) return;
  const clean = pathOrID.trim();
  const norm = normalizeFolderPath(clean);
  const next = (workspace.dismissedProjects || []).filter(
    (item) => item !== clean && normalizeFolderPath(item) !== norm
  );
  setWorkspace("dismissedProjects", next);
  writeStorageJSON(DISMISSED_PROJECTS_KEY, next);
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
  const parsed = readStorageJSON<unknown>(WORK_WORKSPACE_DIRS_KEY);
  if (Array.isArray(parsed)) {
    return Array.from(new Set(parsed.map((p) => stripWorktreePath(String(p))).filter(Boolean)));
  }
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
  writeStorageJSON(WORK_WORKSPACE_DIRS_KEY, next);
}

export function unrecordWorkWorkspaceDir(pathOrDir?: string | null): void {
  const norm = stripWorktreePath(pathOrDir);
  if (!norm) return;
  const current = workWorkspaceDirs();
  if (!current.includes(norm)) return;
  const next = current.filter((item) => item !== norm);
  setWorkWorkspaceDirs(next);
  writeStorageJSON(WORK_WORKSPACE_DIRS_KEY, next);
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

interface SavedProfileSnap {
  ref: WorkspaceProfile;
  id: string | undefined;
  name: string | undefined;
  color: string | undefined;
  surface: WorkspaceProfile["surface"];
  hidden: boolean | undefined;
  foldersRef: WorkspaceFolder[] | undefined;
  folders: Array<{ path: string; isPrimary: boolean | undefined; missing: boolean | undefined }>;
}

interface SessionEntrySnap {
  ref: SessionIndexEntry;
  directory: string | undefined;
  surface: SessionIndexEntry["surface"];
}

let cachedLookup: FolderLookup | null = null;
let cachedDefDir = "";
let cachedWorkDirs: string[] | null = null;
let cachedSessRev = -1;
let cachedRawSessionIndex: Record<string, SessionIndexEntry> | null = null;
let cachedSessionEntries = new Map<string, SessionEntrySnap>();
let cachedSessionSurfaceByDir = new Map<string, "work" | "code">();
let cachedSavedSnaps: SavedProfileSnap[] = [];
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

function snapshotFolders(
  folders: WorkspaceFolder[] | undefined
): Array<{ path: string; isPrimary: boolean | undefined; missing: boolean | undefined }> {
  return (folders || []).map((f) => ({ path: f.path, isPrimary: f.isPrimary, missing: f.missing }));
}

function foldersMatchSnapshot(
  folders: WorkspaceFolder[] | undefined,
  snap: Array<{ path: string; isPrimary: boolean | undefined; missing: boolean | undefined }> | undefined
): boolean {
  const list = folders || [];
  if (!snap || list.length !== snap.length) return false;
  for (let i = 0; i < list.length; i++) {
    if (
      list[i]?.path !== snap[i].path ||
      list[i]?.isPrimary !== snap[i].isPrimary ||
      list[i]?.missing !== snap[i].missing
    ) {
      return false;
    }
  }
  return true;
}

// The lookup is derived from every field it indexes by. Reading only `.length`
// would leave a caller that runs on a cache hit unaware of in-place edits that
// keep the array size (rename, recolor, hide, change folders), so subscribe to
// each of those fields through the store proxy whenever a computation is listening.
function trackFolderLookupSources(): void {
  if (!getListener()) return;
  const saved = workspace.savedWorkspaces;
  for (let i = 0; i < saved.length; i++) {
    const w = saved[i];
    if (!w) continue;
    void w.id;
    void w.name;
    void w.color;
    void w.surface;
    void w.hidden;
    const folders = w.folders;
    if (!folders) continue;
    for (let j = 0; j < folders.length; j++) {
      void folders[j]?.path;
      void folders[j]?.isPrimary;
      void folders[j]?.missing;
    }
  }
  const dismissed = workspace.dismissedProjects;
  for (let i = 0; i < dismissed.length; i++) void dismissed[i];
}

function sessionIndexMatchesCache(
  rawSessionIndex: Record<string, SessionIndexEntry>,
  sessRev: number
): boolean {
  if (sessRev !== cachedSessRev || rawSessionIndex !== cachedRawSessionIndex) {
    return false;
  }
  let count = 0;
  for (const id in rawSessionIndex) {
    count++;
    const entry = rawSessionIndex[id];
    const snap = cachedSessionEntries.get(id);
    if (
      !snap ||
      snap.ref !== entry ||
      snap.directory !== entry?.directory ||
      snap.surface !== entry?.surface
    ) {
      return false;
    }
  }
  return count === cachedSessionEntries.size;
}

function savedProfileMatchesSnap(w: WorkspaceProfile | undefined, snap: SavedProfileSnap | undefined): boolean {
  return Boolean(
    w &&
      snap &&
      w === snap.ref &&
      w.id === snap.id &&
      w.folders === snap.foldersRef &&
      foldersMatchSnapshot(w.folders, snap.folders) &&
      w.surface === snap.surface &&
      w.hidden === snap.hidden &&
      w.name === snap.name &&
      w.color === snap.color
  );
}

function getFolderLookup(): FolderLookup {
  const defDir = normalizeFolderPath(defaultWorkspaceDir());
  const wDirs = workWorkspaceDirs();
  const sessRev = sessionSurfaceRevision();
  trackFolderLookupSources();

  const rawSaved = unwrap(workspace.savedWorkspaces) || [];
  const rawDismissed = unwrap(workspace.dismissedProjects) || [];
  const rawSessionIndex = unwrap(workspace.sessionIndex) || {};

  const sessionIndexSame = sessionIndexMatchesCache(rawSessionIndex, sessRev);

  if (!sessionIndexSame) {
    const nextSessionSurface = new Map<string, "work" | "code">();
    const nextSessionEntries = new Map<string, SessionEntrySnap>();
    for (const id in rawSessionIndex) {
      const entry = rawSessionIndex[id];
      if (!entry) continue;
      nextSessionEntries.set(id, {
        ref: entry,
        directory: entry.directory,
        surface: entry.surface,
      });
      if (!entry.directory || (entry.surface !== "work" && entry.surface !== "code")) continue;
      const dir = stripWorktreePath(entry.directory);
      if (!dir) continue;
      if (entry.surface === "code") {
        nextSessionSurface.set(dir, "code");
      } else if (!nextSessionSurface.has(dir)) {
        nextSessionSurface.set(dir, "work");
      }
    }
    cachedSessionSurfaceByDir = nextSessionSurface;
    cachedSessionEntries = nextSessionEntries;
    cachedSessRev = sessRev;
    cachedRawSessionIndex = rawSessionIndex;
  }

  let savedSame =
    cachedLookup !== null &&
    defDir === cachedDefDir &&
    wDirs === cachedWorkDirs &&
    sessionIndexSame &&
    rawSaved.length === cachedSavedSnaps.length &&
    rawDismissed.length === cachedDismissedItems.length;

  if (savedSame) {
    for (let i = 0; i < rawSaved.length; i++) {
      if (!savedProfileMatchesSnap(rawSaved[i], cachedSavedSnaps[i])) {
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
  cachedSavedSnaps = rawSaved.map((w) => ({
    ref: w,
    id: w?.id,
    name: w?.name,
    color: w?.color,
    surface: w?.surface,
    hidden: w?.hidden,
    foldersRef: w?.folders,
    folders: snapshotFolders(w?.folders),
  }));
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
  const parsed = readStorageJSON<Record<string, unknown>>(storageKey);
  const rootDir = normalizeFolderPath(typeof parsed?.rootDir === "string" ? parsed.rootDir : "");
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
  return { rootDir: "", sessionID: null };
}

function writeRememberedSurface(storageKey: string, state: { rootDir: string; sessionID: string | null }): void {
  writeStorageJSON(storageKey, state);
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
    writeStorageJSON(RECENT_PROJECTS_KEY, filteredRecents);
  }
  const filteredOrder = workspace.projectOrder.filter((p) => !isWorkSurfacePath(p));
  if (filteredOrder.length !== workspace.projectOrder.length) {
    setWorkspace("projectOrder", filteredOrder);
    writeStorageJSON(PROJECT_ORDER_KEY, filteredOrder);
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
