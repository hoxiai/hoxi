// web/src/store/workspace.ts
import { createStore } from "solid-js/store";
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

export interface WorkspaceState {
  connected: boolean;
  rootDir: string;
  wsBaseURL: string;
  projects: ProjectInfo[];
  recentProjects: string[];
  sessions: SessionInfo[];
  activeSessionID: string | null;
  agents: AgentInfo[];
  activeMode: string;
  sidebarProjectMode: "single" | "multi";
  inspectorTab: "diffs" | "memory" | "files" | "terminal";
  inspectorOpen: boolean;
  inspectedFilePath: string | null;
  providerModalOpen: boolean;
  providerModalTab: ProviderModalTab;
  editingProviderID: string | null;
  arenaModalOpen: boolean;
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
  statusBySession: Record<string, SessionStatus>;
  diffsBySession: Record<string, SnapshotFileDiff[]>;
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
}

export const THEME_KEY = "hoxi-theme";
export const SELECTED_MODEL_KEY = "hoxi-selected-model:v1";
export const FAVORITE_MODELS_KEY = "hoxi-model-favorites:v1";
export const RECENT_MODELS_KEY = "hoxi-recent-models:v1";
export const EFFORT_KEY = "hoxi-reasoning-effort:v1";
export const RECENT_PROJECTS_KEY = "hoxi-recent-projects:v1";
export const SIDEBAR_PROJECT_MODE_KEY = "hoxi-sidebar-project-mode:v1";

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

function initialRecentProjects(): string[] {
  try {
    const raw = localStorage.getItem(RECENT_PROJECTS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed.filter((p) => typeof p === "string");
  } catch {
    /* ignore */
  }
  return [];
}

function initialSidebarProjectMode(): "single" | "multi" {
  try {
    const saved = localStorage.getItem(SIDEBAR_PROJECT_MODE_KEY);
    if (saved === "single" || saved === "multi") return saved;
  } catch {
    /* ignore */
  }
  return "multi";
}

export const [workspace, setWorkspace] = createStore<WorkspaceState>({
  connected: false,
  rootDir: "",
  wsBaseURL: "",
  projects: [],
  recentProjects: initialRecentProjects(),
  sessions: [],
  activeSessionID: null,
  agents: DEFAULT_AGENTS,
  activeMode: "build",
  sidebarProjectMode: initialSidebarProjectMode(),
  inspectorTab: "diffs",
  inspectorOpen: false,
  inspectedFilePath: null,
  providerModalOpen: false,
  providerModalTab: "providers",
  editingProviderID: null,
  arenaModalOpen: false,
  theme: initialTheme(),
  providers: [],
  connectedProviders: [],
  defaultModels: {},
  globalConfig: {},
  hoxiProviders: {},
  openCodeImportedProviders: [],
  providerAuthMethods: {},
  selectedModel: initialSelectedModel(),
  reasoningEffort: initialEffort(),
  favoriteModels: initialModelList(FAVORITE_MODELS_KEY),
  recentModels: initialModelList(RECENT_MODELS_KEY),
  messagesBySession: {},
  statusBySession: {},
  diffsBySession: {},
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
});

export function openProviderModal(
  tab: ProviderModalTab = "providers",
  editProviderID: string | null = null
) {
  setWorkspace("providerModalTab", tab);
  setWorkspace("editingProviderID", editProviderID);
  setWorkspace("providerModalOpen", true);
}

export function setSidebarProjectMode(mode: "single" | "multi") {
  setWorkspace("sidebarProjectMode", mode);
  try {
    localStorage.setItem(SIDEBAR_PROJECT_MODE_KEY, mode);
  } catch {
    /* ignore */
  }
}

export function openFileInInspector(filePath: string) {
  const cleaned = filePath.replace(/^\.\//, "").replace(/:\d+(?::\d+)?$/, "").trim();
  if (!cleaned) return;
  setWorkspace("inspectedFilePath", cleaned);
  setWorkspace("inspectorTab", "files");
  setWorkspace("inspectorOpen", true);
}

export function openDiffInInspector(_filePath?: string) {
  setWorkspace("inspectorTab", "diffs");
  setWorkspace("inspectorOpen", true);
}

export function addReviewComment(
  sessionID: string,
  comment: Omit<ReviewCommentItem, "id">
) {
  const item: ReviewCommentItem = {
    ...comment,
    id: `rc_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
  };
  const current = workspace.reviewCommentsBySession[sessionID] ?? [];
  setWorkspace("reviewCommentsBySession", sessionID, [...current, item]);
}

export function removeReviewComment(sessionID: string, commentID: string) {
  const current = workspace.reviewCommentsBySession[sessionID] ?? [];
  setWorkspace(
    "reviewCommentsBySession",
    sessionID,
    current.filter((c) => c.id !== commentID)
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
  } catch {
    /* storage unavailable */
  }
}

export function toggleTheme() {
  setTheme(workspace.theme === "dark" ? "light" : "dark");
}

export * from "./workspace-providers";
export * from "./workspace-sessions";
export * from "./provider-telemetry";
export * from "./provider-audit";

