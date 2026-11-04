// web/src/store/workspace-state.ts
// Workspace state root: the single reactive store plus the localStorage-hydrated
// initial values it is created from. Domain modules import state from here (never
// from the ./workspace barrel), so this file must stay free of runtime imports
// from other store modules.
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
  ProjectInfo,
  ProviderAuthMethod,
  ProviderInfo,
  ProviderTelemetryStats,
  SkillSelection,
  WorkspaceProfile,
  WorktreeMeta,
} from "../services/api";

export type ReasoningEffort = "" | "low" | "medium" | "high" | "xhigh" | "max";

export const VALID_REASONING_EFFORTS: readonly ReasoningEffort[] = [
  "",
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
];

export interface SessionAIConfig {
  model: SelectedModel | null;
  agent: string;
  effort: ReasoningEffort;
}

export const SESSION_AI_KEY = "hoxi-session-ai:v1";
export const DEFAULT_AGENT_KEY = "hoxi-default-agent:v1";

export function readStorageItem(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writeStorageItem(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {}
}

export function removeStorageItem(key: string): void {
  try {
    localStorage.removeItem(key);
  } catch {}
}

export function readStorageJSON<T = unknown>(key: string): T | null {
  const raw = readStorageItem(key);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

export function writeStorageJSON(key: string, value: unknown): void {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {}
}

export function readStorageRecord<T>(key: string): Record<string, T> {
  const parsed = readStorageJSON(key);
  if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
    return parsed as Record<string, T>;
  }
  return {};
}

export function readStorageArray<T>(key: string, guard: (item: unknown) => item is T): T[] {
  const parsed = readStorageJSON(key);
  return Array.isArray(parsed) ? parsed.filter(guard) : [];
}

export function initialDefaultAgent(): string {
  return readStorageItem(DEFAULT_AGENT_KEY) || "build";
}

export function initialSessionAI(): Record<string, SessionAIConfig> {
  const parsed = readStorageRecord<SessionAIConfig | null>(SESSION_AI_KEY);
  return Object.fromEntries(
    Object.entries(parsed).filter(([, c]) =>
      Boolean(
        c &&
          typeof c.agent === "string" &&
          c.agent.length > 0 &&
          VALID_REASONING_EFFORTS.includes(c.effort) &&
          (c.model === null ||
            (typeof c.model?.providerID === "string" && typeof c.model?.modelID === "string"))
      )
    )
  ) as Record<string, SessionAIConfig>;
}

export const DEFAULT_AGENTS: AgentInfo[] = [
  {
    name: "build",
    mode: "primary",
    description: "The default agent. Executes tools based on configured permissions.",
  },
  {
    name: "plan",
    mode: "primary",
    description: "Plan mode. Disallows all edit tools.",
  },
  {
    name: "ask",
    mode: "primary",
    description: "Read-only answers about the codebase. Never edits files or runs shell commands.",
  },
  {
    name: "debug",
    mode: "primary",
    description: "Troubleshoots failures: reproduces, traces the root cause, and applies a minimal verified fix.",
  },
];

export const PROVIDER_TELEMETRY_KEY = "hoxi-provider-telemetry:v2";

export function initialProviderTelemetry(): Record<string, ProviderTelemetryStats> {
  return readStorageRecord<ProviderTelemetryStats>(PROVIDER_TELEMETRY_KEY);
}

export interface QueuedPromptAttachment {
  type: "file";
  mime: string;
  filename?: string;
  url: string;
}

export interface QueuedPromptItem {
  id: string;
  sessionID: string;
  text: string;
  createdAt: number;
  agent?: string;
  model?: SelectedModel;
  effort?: ReasoningEffort;
  attachments: QueuedPromptAttachment[];
  skillSelection?: SkillSelection;
  /** Turn-scoped system instructions, kept out of the visible user message. */
  system?: string;
}

export interface ComposerDraftEdit {
  id: string;
  text: string;
  attachments: QueuedPromptAttachment[];
  skillSelection?: SkillSelection;
  queuedEdit?: boolean;
}

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

export type ProviderModalTab = "providers" | "official" | "custom" | "auto" | "models" | "agents" | "subagents" | "policy";
export type WorkspaceModalMode = "create" | "edit" | "delete" | "manage" | null;

// OFFICIAL_PROVIDER_ID is the dedicated provider for the signed-in Hoxi account
// Hoxi gateway channel. It stays separate from the third-party "hoxi-gateway" gateway
// provider so the two catalogs never overwrite or mix with each other.
export const OFFICIAL_PROVIDER_ID = "hoxi-official";

export interface SessionIndexEntry {
  directory?: string;
  projectID?: string;
  parentID?: string;
  surface?: SessionInfo["surface"];
  managed?: boolean;
  runtime?: SessionInfo["runtime"];
  updated: number;
}

export type SidebarProjectMode = "multi" | "single" | "recent" | "attention" | "running";

export interface HoxiProviderConfig {
  name?: string;
  npm?: string;
  options?: {
    baseURL?: string;
    apiKey?: string;
    headers?: Record<string, string>;
    [key: string]: any;
  };
  models?: Record<string, any>;
  [key: string]: any;
}

export interface OpenCodeGlobalConfig {
  provider?: Record<string, HoxiProviderConfig | undefined>;
  agent?: Record<string, any>;
  permission?: Record<string, any>;
  mcp?: Record<string, any>;
  [key: string]: any;
}

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
  // Cross-project metadata of every session seen by the stream or a project
  // list. It survives project switches so status/approval reconciliation never
  // mistakes a foreign session for one rooted in the active project.
  sessionIndex: Record<string, SessionIndexEntry>;
  archivedSessionIDs: string[];
  activeSessionID: string | null;
  activeProjectID: string | null;
  sessionSetupError: string | null;
  newThreadRequest: number;
  agents: AgentInfo[];
  activeMode: string;
  defaultAI: SessionAIConfig;
  aiBySession: Record<string, SessionAIConfig>;
  sessionSandboxMode: "local" | "worktree";
  sidebarProjectMode: SidebarProjectMode;
  inspectorTab: "diffs" | "memory" | "files" | "skills" | "terminal" | "browser" | "computer" | "tasks";
  inspectorOpen: boolean;
  inspectorMaximized: boolean;
  inspectedFilePath: string | null;
  inspectedFileLine: number | null;
  inspectedFileDiff: SnapshotFileDiff | null;
  inspectedDiffFile: string | null;
  inspectedBrowserURL: string | null;
  activeBrowserScopeID: string | null;
  activeInspectorFolder: string | null;
  workspaceModalMode: WorkspaceModalMode;
  editingWorkspaceID: string | null;
  editingWorkspaceDir: string | null;
  editingProviderID: string | null;
  arenaModalOpen: boolean;
  handoffModalOpen: boolean;
  theme: "light" | "dark";
  providers: ProviderInfo[];
  connectedProviders: string[];
  defaultModels: Record<string, string>;
  globalConfig: OpenCodeGlobalConfig;
  hoxiProviders: Record<string, HoxiProviderConfig | undefined>;
  openCodeImportedProviders: string[];
  providerAuthMethods: Record<string, ProviderAuthMethod[]>;
  selectedModel: SelectedModel | null;
  reasoningEffort: ReasoningEffort;
  favoriteModels: SelectedModel[];
  recentModels: SelectedModel[];
  messagesBySession: Record<string, MessageTurn[]>;
  loadingMessagesBySession: Record<string, boolean>;
  bootstrappingSessions: boolean;
  statusBySession: Record<string, SessionStatus>;
  stalledSinceBySession: Record<string, number>;
  diffsBySession: Record<string, SnapshotFileDiff[]>;
  worktreeDiffsBySession: Record<string, SnapshotFileDiff[]>;
  diffScope: "worktree" | "session";
  worktreeBySession: Record<string, WorktreeMeta>;
  reviewCommentsBySession: Record<string, ReviewCommentItem[]>;
  pendingPermissions: PermissionRequest[];
  pendingQuestions: QuestionRequest[];
  dismissedPlanFollowups: Record<string, boolean>;
  dismissedSandboxMergePrompts: Record<string, boolean>;
  providerTelemetry: Record<string, ProviderTelemetryStats>;
  interruptedBySession: Record<string, "paused" | "stopped" | undefined>;
  sessionFinishedAt: Record<string, number>;
  unreadCompletedBySession: Record<string, boolean>;
  queuedPromptsBySession: Record<
    string,
    QueuedPromptItem[]
  >;
  composerDraftEditBySession: Record<
    string,
    ComposerDraftEdit | undefined
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
  const parsed = readStorageJSON(storageKey);
  return Array.isArray(parsed) ? parsed.map((p) => String(p)) : [];
}

export function initialArchivedSessions(storageKey: string): string[] {
  return readStorageArray(storageKey, (id): id is string => typeof id === "string");
}

function initialTheme(): "light" | "dark" {
  const saved = readStorageItem(THEME_KEY);
  return saved === "light" || saved === "dark" ? saved : "light";
}

function isSelectedModelValue(m: unknown): m is SelectedModel {
  return Boolean(
    m &&
      typeof m === "object" &&
      typeof (m as SelectedModel).providerID === "string" &&
      typeof (m as SelectedModel).modelID === "string"
  );
}

function initialSelectedModel(): SelectedModel | null {
  const parsed = readStorageJSON(SELECTED_MODEL_KEY);
  return isSelectedModelValue(parsed)
    ? { providerID: parsed.providerID, modelID: parsed.modelID }
    : null;
}

function initialModelList(storageKey: string): SelectedModel[] {
  return readStorageArray(storageKey, isSelectedModelValue);
}

function initialEffort(): ReasoningEffort {
  const saved = readStorageItem(EFFORT_KEY);
  return saved !== null && (VALID_REASONING_EFFORTS as readonly string[]).includes(saved)
    ? (saved as ReasoningEffort)
    : "";
}

export function isValidStoredProjectPath(p: unknown): p is string {
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
  return readStorageArray(RECENT_PROJECTS_KEY, isValidStoredProjectPath);
}

function initialProjectOrder(): string[] {
  return readStorageArray(PROJECT_ORDER_KEY, isValidStoredProjectPath);
}

const PERSISTED_SIDEBAR_PROJECT_MODES: SidebarProjectMode[] = ["multi", "single", "recent"];

function initialSidebarProjectMode(): SidebarProjectMode {
  const saved = readStorageItem(SIDEBAR_PROJECT_MODE_KEY);
  if (PERSISTED_SIDEBAR_PROJECT_MODES.includes(saved as SidebarProjectMode)) {
    return saved as SidebarProjectMode;
  }
  if (saved === "attention" || saved === "running") {
    writeStorageItem(SIDEBAR_PROJECT_MODE_KEY, "multi");
  }
  return "multi";
}

function initialSessionSandboxMode(): "local" | "worktree" {
  removeStorageItem(SESSION_SANDBOX_MODE_KEY);
  const parsed = readStorageJSON<{ defaultSandboxMode?: string }>("hoxi-app-settings:v1");
  if (parsed?.defaultSandboxMode === "local" || parsed?.defaultSandboxMode === "worktree") {
    return parsed.defaultSandboxMode;
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
  sessionIndex: {},
  archivedSessionIDs: initialArchivedSessions(ARCHIVED_SESSIONS_KEY),
  activeSessionID: null,
  activeProjectID: null,
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
  activeBrowserScopeID: null,
  activeInspectorFolder: null,
  workspaceModalMode: null,
  editingWorkspaceID: null,
  editingWorkspaceDir: null,
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
  stalledSinceBySession: {},
  diffsBySession: {},
  worktreeDiffsBySession: {},
  diffScope: "worktree",
  worktreeBySession: {},
  reviewCommentsBySession: {},
  pendingPermissions: [],
  pendingQuestions: [],
  dismissedPlanFollowups: {},
  dismissedSandboxMergePrompts: {},
  providerTelemetry: initialProviderTelemetry(),
  interruptedBySession: {},
  sessionFinishedAt: {},
  unreadCompletedBySession: {},
  queuedPromptsBySession: {},
  composerDraftEditBySession: {},
});
