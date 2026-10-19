// web/src/store/workspace.ts
import { createStore } from "solid-js/store";
import { initialDefaultAgent, initialSessionAI, type SessionAIConfig } from "./session-ai";
import type {
  MessageInfo,
  MessagePart,
  PermissionRequest,
  QuestionRequest,
  SessionInfo,
  SessionStatus,
  SnapshotFileDiff,
} from "../services/sse-types";
import type {
  AgentInfo,
  CodeAuditResult,
  ProjectInfo,
  ProviderAuthMethod,
  ProviderInfo,
  ProviderTelemetryStats,
  WorkspaceProfile,
  WorktreeMeta,
} from "../services/api";
import { DEFAULT_AGENTS } from "./workspace-providers";
import { initialProviderTelemetry } from "./provider-telemetry";
import { initialObserverAutoAudit, initialObserverModel } from "./provider-audit";

export interface MessageTurn {
  info: MessageInfo;
  parts: MessagePart[];
}

export interface SelectedModel {
  providerID: string;
  modelID: string;
}

export interface ReviewCommentItem {
  id: string;
  file: string;
  line?: number;
  body: string;
  snippet?: string;
}

export type ProviderModalTab = "providers" | "custom" | "models" | "policy";
export type WorkspaceModalMode = "create" | "edit" | "delete" | "manage" | null;

export interface WorkspaceState {
  appVersion: string;
  connected: boolean;
  rootDir: string;
  wsBaseURL: string;
  projects: ProjectInfo[];
  savedWorkspaces: WorkspaceProfile[];
  dismissedProjects: string[];
  recentProjects: string[];
  projectOrder: string[];
  sessions: SessionInfo[];
  archivedSessionIDs: string[];
  activeSessionID: string | null;
  sessionSetupError: string | null;
  newThreadRequest: number;
  agents: AgentInfo[];
  activeMode: string;
  defaultAI: SessionAIConfig;
  aiBySession: Record<string, SessionAIConfig>;
  sessionSandboxMode: "local" | "worktree";
  sidebarProjectMode: "single" | "multi" | "recent";
  inspectorTab: "diffs" | "memory" | "files" | "terminal" | "browser";
  inspectorOpen: boolean;
  inspectorMaximized: boolean;
  inspectedFilePath: string | null;
  inspectedFileLine: number | null;
  inspectedFileDiff: SnapshotFileDiff | null;
  inspectedDiffFile: string | null;
  inspectedBrowserURL: string | null;
  activeInspectorFolder: string | null;
  workspaceModalMode: WorkspaceModalMode;
  editingWorkspaceID: string | null;
  editingWorkspaceDir: string | null;
  providerModalOpen: boolean;
  providerModalTab: ProviderModalTab;
  editingProviderID: string | null;
  arenaModalOpen: boolean;
  handoffModalOpen: boolean;
  theme: "light" | "dark";
  providers: ProviderInfo[];
  connectedProviders: string[];
  defaultModels: Record<string, string>;
  globalConfig: Record<string, any>;
  hoxiProviders: Record<string, any>;
  openCodeImportedProviders: string[];
  providerAuthMethods: Record<string, ProviderAuthMethod[]>;
  selectedModel: SelectedModel | null;
  reasoningEffort: "" | "low" | "medium" | "high";
  favoriteModels: SelectedModel[];
  recentModels: SelectedModel[];
  messagesBySession: Record<string, MessageTurn[]>;
  loadingMessagesBySession: Record<string, boolean>;
  bootstrappingSessions: boolean;
  statusBySession: Record<string, SessionStatus>;
  diffsBySession: Record<string, SnapshotFileDiff[]>;
  worktreeDiffsBySession: Record<string, SnapshotFileDiff[]>;
  diffScope: "worktree" | "session";
  worktreeBySession: Record<string, WorktreeMeta>;
  reviewCommentsBySession: Record<string, ReviewCommentItem[]>;
  pendingPermissions: PermissionRequest[];
  pendingQuestions: QuestionRequest[];
  dismissedPlanFollowups: Record<string, boolean>;
  providerTelemetry: Record<string, ProviderTelemetryStats>;
  observerModel: string;
  observerAutoAudit: boolean;
  auditsBySession: Record<string, CodeAuditResult>;
  auditingBySession: Record<string, boolean>;
  interruptedBySession: Record<string, "paused" | "stopped" | undefined>;
  sessionFinishedAt: Record<string, number>;
  unreadCompletedBySession: Record<string, boolean>;
  queuedPromptsBySession: Record<
    string,
    import("./workspace-queue-subagents").QueuedPromptItem[]
  >;
  composerDraftEditBySession: Record<
    string,
    import("./workspace-queue-subagents").ComposerDraftEdit | undefined
  >;
}

export const THEME_KEY = "hoxi-theme";
export const SELECTED_MODEL_KEY = "hoxi-selected-model:v1";
export const FAVORITE_MODELS_KEY = "hoxi-model-favorites:v1";
export const RECENT_MODELS_KEY = "hoxi-recent-models:v1";
export const EFFORT_KEY = "hoxi-reasoning-effort:v1";
export const RECENT_PROJECTS_KEY = "hoxi-recent-projects:v1";
export const PROJECT_ORDER_KEY = "hoxi-project-order:v1";
export const SIDEBAR_PROJECT_MODE_KEY = "hoxi-sidebar-project-mode:v1";
export const SESSION_SANDBOX_MODE_KEY = "hoxi-session-sandbox-mode:v1";
export const ARCHIVED_SESSIONS_KEY = "hoxi-archived-sessions:v1";
export const DISMISSED_PROJECTS_KEY = "hoxi-dismissed-projects:v1";

export function initialDismissedProjects(storageKey: string): string[] {
  try {
    const raw = localStorage.getItem(storageKey);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed.map((p) => String(p));
  } catch {
    /* ignore */
  }
  return [];
}

export function initialArchivedSessions(storageKey: string): string[] {
  try {
    const raw = localStorage.getItem(storageKey);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed.filter((id) => typeof id === "string");
  } catch {
    /* ignore */
  }
  return [];
}

function initialTheme(): "light" | "dark" {
  try {
    const saved = localStorage.getItem(THEME_KEY);
    if (saved === "light" || saved === "dark") return saved;
  } catch {
    /* storage unavailable */
  }
  return "light";
}

function initialSelectedModel(): SelectedModel | null {
  try {
    const raw = localStorage.getItem(SELECTED_MODEL_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed.providerID === "string" && typeof parsed.modelID === "string") {
      return { providerID: parsed.providerID, modelID: parsed.modelID };
    }
  } catch {
    /* ignore */
  }
  return null;
}

function initialModelList(storageKey: string): SelectedModel[] {
  try {
    const raw = localStorage.getItem(storageKey);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      return parsed.filter(
        (m) => m && typeof m.providerID === "string" && typeof m.modelID === "string"
      );
    }
  } catch {
    /* ignore */
  }
  return [];
}

function initialEffort(): "" | "low" | "medium" | "high" {
  try {
    const saved = localStorage.getItem(EFFORT_KEY);
    if (saved === "low" || saved === "medium" || saved === "high" || saved === "") return saved;
  } catch {
    /* ignore */
  }
  return "";
}

function isValidStoredProjectPath(p: unknown): p is string {
  if (typeof p !== "string") return false;
  const clean = p.trim().replace(/[\\/]+$/, "");
  return Boolean(
    clean &&
      clean !== "." &&
      clean !== "/" &&
      clean !== "\\" &&
      !/^[A-Za-z]:$/.test(clean) &&
      !clean.includes(".app/Contents")
  );
}

function initialRecentProjects(): string[] {
  try {
    const raw = localStorage.getItem(RECENT_PROJECTS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed.filter(isValidStoredProjectPath);
  } catch {
    /* ignore */
  }
  return [];
}

function initialProjectOrder(): string[] {
  try {
    const raw = localStorage.getItem(PROJECT_ORDER_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed.filter(isValidStoredProjectPath);
  } catch {
    /* ignore */
  }
  return [];
}

function initialSidebarProjectMode(): "single" | "multi" | "recent" {
  try {
    const saved = localStorage.getItem(SIDEBAR_PROJECT_MODE_KEY);
    if (saved === "single" || saved === "multi" || saved === "recent") return saved;
  } catch {
    /* ignore */
  }
  return "multi";
}

function initialSessionSandboxMode(): "local" | "worktree" {
  try {
    localStorage.removeItem(SESSION_SANDBOX_MODE_KEY);
    const raw = localStorage.getItem("hoxi-app-settings:v1");
    if (raw) {
      const parsed = JSON.parse(raw);
      if (parsed?.defaultSandboxMode === "local" || parsed?.defaultSandboxMode === "worktree") {
        return parsed.defaultSandboxMode;
      }
    }
  } catch {
    /* ignore */
  }
  return "local";
}

export const [workspace, setWorkspace] = createStore<WorkspaceState>({
  appVersion: "",
  connected: false,
  rootDir: "",
  wsBaseURL: "",
  projects: [],
  savedWorkspaces: [],
  dismissedProjects: initialDismissedProjects(DISMISSED_PROJECTS_KEY),
  recentProjects: initialRecentProjects(),
  projectOrder: initialProjectOrder(),
  sessions: [],
  archivedSessionIDs: initialArchivedSessions(ARCHIVED_SESSIONS_KEY),
  activeSessionID: null,
  sessionSetupError: null,
  newThreadRequest: 0,
  agents: DEFAULT_AGENTS,
  defaultAI: { model: initialSelectedModel(), agent: initialDefaultAgent(), effort: initialEffort() },
  aiBySession: initialSessionAI(),
  get activeMode() {
    return (this.activeSessionID && this.aiBySession[this.activeSessionID] || this.defaultAI).agent;
  },
  sessionSandboxMode: initialSessionSandboxMode(),
  sidebarProjectMode: initialSidebarProjectMode(),
  inspectorTab: "diffs",
  inspectorOpen: false,
  inspectorMaximized: false,
  inspectedFilePath: null,
  inspectedFileLine: null,
  inspectedFileDiff: null,
  inspectedDiffFile: null,
  inspectedBrowserURL: null,
  activeInspectorFolder: null,
  workspaceModalMode: null,
  editingWorkspaceID: null,
  editingWorkspaceDir: null,
  providerModalOpen: false,
  providerModalTab: "providers",
  editingProviderID: null,
  arenaModalOpen: false,
  handoffModalOpen: false,
  theme: initialTheme(),
  providers: [],
  connectedProviders: [],
  defaultModels: {},
  globalConfig: {},
  hoxiProviders: {},
  openCodeImportedProviders: [],
  providerAuthMethods: {},
  get selectedModel() {
    return (this.activeSessionID && this.aiBySession[this.activeSessionID] || this.defaultAI).model;
  },
  get reasoningEffort() {
    return (this.activeSessionID && this.aiBySession[this.activeSessionID] || this.defaultAI).effort;
  },
  favoriteModels: initialModelList(FAVORITE_MODELS_KEY),
  recentModels: initialModelList(RECENT_MODELS_KEY),
  messagesBySession: {},
  loadingMessagesBySession: {},
  bootstrappingSessions: true,
  statusBySession: {},
  diffsBySession: {},
  worktreeDiffsBySession: {},
  diffScope: "worktree",
  worktreeBySession: {},
  reviewCommentsBySession: {},
  pendingPermissions: [],
  pendingQuestions: [],
  dismissedPlanFollowups: {},
  providerTelemetry: initialProviderTelemetry(),
  observerModel: initialObserverModel(),
  observerAutoAudit: initialObserverAutoAudit(),
  auditsBySession: {},
  auditingBySession: {},
  interruptedBySession: {},
  sessionFinishedAt: {},
  unreadCompletedBySession: {},
  queuedPromptsBySession: {},
  composerDraftEditBySession: {},
});

export function isSessionArchived(session: SessionInfo): boolean {
  if (session.time?.archived && session.time.archived > 0) return true;
  return workspace.archivedSessionIDs.includes(session.id);
}

export function setSessionSandboxMode(mode: "local" | "worktree") {
  setWorkspace("sessionSandboxMode", mode);
  try { localStorage.setItem(SESSION_SANDBOX_MODE_KEY, mode); } catch { /* ignore */ }
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

export function openProviderModal(
  tab: ProviderModalTab = "providers",
  editProviderID: string | null = null
) {
  setWorkspace({ providerModalTab: tab, editingProviderID: editProviderID, providerModalOpen: true });
}

export function openHandoffModal() { setWorkspace("handoffModalOpen", true); }
export function closeHandoffModal() { setWorkspace("handoffModalOpen", false); }

export function setSidebarProjectMode(mode: "single" | "multi" | "recent") {
  setWorkspace("sidebarProjectMode", mode);
  try { localStorage.setItem(SIDEBAR_PROJECT_MODE_KEY, mode); } catch { /* ignore */ }
}

function firstChangedLine(patch?: string): number | null {
  if (!patch) return null;
  const hunk = patch.match(/^@@\s+-\d+(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@/m);
  if (!hunk) return null;
  let line = Number(hunk[1]);
  for (const entry of patch.slice(patch.indexOf(hunk[0]) + hunk[0].length).split("\n")) {
    if (entry.startsWith("@@")) break;
    if (entry.startsWith("+")) return line;
    if (!entry.startsWith("-")) line++;
  }
  return line;
}

function parseFileReference(raw: string): { path: string; line: number | null } {
  let str = raw.trim();
  if (/^file:\/\//i.test(str)) {
    str = str.replace(/^file:\/\//i, "");
    try {
      str = decodeURIComponent(str);
    } catch {
      /* ignore */
    }
  }
  let parsedLine: number | null = null;
  const hashMatch = str.match(/#L(\d+)(?:[-–—]L?\d+)?$/i);
  if (hashMatch) {
    parsedLine = Number(hashMatch[1]) || null;
    str = str.slice(0, hashMatch.index);
  } else {
    const colonMatch = str.match(/:(\d+)(?:[:\-–—]\d+)?$/);
    if (colonMatch) {
      parsedLine = Number(colonMatch[1]) || null;
      str = str.slice(0, colonMatch.index);
    }
  }
  str = str.replace(/^\.\//, "").trim();
  const activeSession = workspace.activeSessionID
    ? workspace.sessions.find((s) => s.id === workspace.activeSessionID)
    : undefined;
  const activeWorktreeDir = workspace.activeSessionID
    ? workspace.worktreeBySession[workspace.activeSessionID]?.directory
    : undefined;
  const candidateRoots = (
    workspace.activeInspectorFolder
      ? [workspace.activeInspectorFolder]
      : [activeWorktreeDir, activeSession?.directory, workspace.rootDir]
  ).filter((d): d is string => Boolean(d));
  for (const root of candidateRoots) {
    const normRoot = root.replace(/\/+$/, "");
    if (normRoot && str.startsWith(`${normRoot}/`)) {
      str = str.slice(normRoot.length + 1);
      break;
    }
  }
  return { path: str, line: parsedLine };
}

export function openFileInInspector(filePath: string, line?: number | null, diff?: SnapshotFileDiff | null) {
  const ref = parseFileReference(filePath);
  if (!ref.path) return;
  const targetLine = line && line > 0 ? line : ref.line;
  setWorkspace({
    inspectedFileDiff: diff ?? null,
    inspectedFilePath: ref.path,
    inspectedFileLine: targetLine && targetLine > 0 ? targetLine : null,
    inspectorTab: "files",
    inspectorOpen: true,
  });
}

export function openChangedFileInInspector(diff: SnapshotFileDiff | string) {
  const file = typeof diff === "string" ? diff : diff.file;
  const ref = parseFileReference(file);
  const key = workspace.activeSessionID || "__root__";
  const pool = [
    ...(workspace.activeSessionID ? workspace.diffsBySession[workspace.activeSessionID] ?? [] : []),
    ...(workspace.worktreeDiffsBySession[key] ?? []),
    ...(workspace.worktreeDiffsBySession["__root__"] ?? []),
  ];
  const resolvedDiff = typeof diff === "string"
    ? (pool.find((item) => item.file === ref.path) ??
        pool.find((item) => item.file.endsWith(`/${ref.path}`) || ref.path.endsWith(`/${item.file}`)) ??
        null)
    : diff;
  openFileInInspector(
    file,
    ref.line ?? firstChangedLine(resolvedDiff?.patch),
    resolvedDiff
  );
}

export function openDiffInInspector(file?: string | SnapshotFileDiff) {
  const filePath = typeof file === "string" ? file : file?.file;
  const cleaned = filePath ? parseFileReference(filePath).path : "";
  setWorkspace({
    inspectedFileDiff: typeof file === "object" ? file : null,
    inspectedDiffFile: cleaned || null,
    inspectorTab: "diffs",
    inspectorOpen: true,
  });
}

export function openURLInInspector(url: string) {
  const trimmed = url.trim();
  if (!trimmed || !/^https?:\/\//i.test(trimmed)) return;
  setWorkspace({
    inspectedBrowserURL: trimmed,
    inspectorTab: "browser",
    inspectorOpen: true,
  });
}

export function addReviewComment(sessionID: string, comment: Omit<ReviewCommentItem, "id">) {
  const item: ReviewCommentItem = {
    ...comment,
    id: `rc_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
  };
  setWorkspace("reviewCommentsBySession", sessionID, [
    ...(workspace.reviewCommentsBySession[sessionID] ?? []),
    item,
  ]);
}

export function removeReviewComment(sessionID: string, commentID: string) {
  setWorkspace(
    "reviewCommentsBySession",
    sessionID,
    (workspace.reviewCommentsBySession[sessionID] ?? []).filter((c) => c.id !== commentID)
  );
}

export function clearReviewComments(sessionID: string) {
  setWorkspace("reviewCommentsBySession", sessionID, []);
}

export function applyThemeClass(theme: "light" | "dark") {
  document.documentElement.classList.toggle("dark", theme === "dark");
}

export function setTheme(theme: "light" | "dark") {
  setWorkspace("theme", theme);
  applyThemeClass(theme);
  try {
    localStorage.setItem(THEME_KEY, theme);
    const rawSettings = localStorage.getItem("hoxi-app-settings:v1");
    if (rawSettings) {
      const parsed = JSON.parse(rawSettings);
      if (parsed && typeof parsed === "object") {
        parsed.themeMode = theme;
        localStorage.setItem("hoxi-app-settings:v1", JSON.stringify(parsed));
      }
    }
  } catch { /* storage unavailable */ }
}

export function toggleTheme() {
  setTheme(workspace.theme === "dark" ? "light" : "dark");
}

export * from "./workspace-providers";
export * from "./provider-catalog";
export * from "./session-ai";
export * from "./workspace-projects";
export * from "./workspace-sessions";
export * from "./workspace-session-messages";
export * from "./workspace-queue-subagents";
export * from "./session-sandbox";
export * from "./session-status-sync";
export * from "./session-title";
export * from "./git-commit";
export * from "./approval-mode";
export * from "./provider-telemetry";
export * from "./provider-health";
export * from "./provider-audit";
export * from "./settings";
export * from "./daily-pulse";
export * from "./auth-state";
export * from "./updater-state";
export * from "./leaderboard-state";
export * from "./privacy";
