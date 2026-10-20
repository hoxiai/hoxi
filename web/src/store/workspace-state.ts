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
  CodeAuditResult,
  ProjectInfo,
  ProviderAuthMethod,
  ProviderInfo,
  ProviderTelemetryStats,
  SkillSelection,
  WorkspaceProfile,
  WorkspaceReviewResult,
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

export function initialDefaultAgent(): string {
  try { return localStorage.getItem(DEFAULT_AGENT_KEY) || "build"; }
  catch { return "build"; }
}

export function initialSessionAI(): Record<string, SessionAIConfig> {
  try {
    const parsed = JSON.parse(localStorage.getItem(SESSION_AI_KEY) || "{}");
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).filter(([, value]) => {
      const c = value as SessionAIConfig | null;
      return c && typeof c.agent === "string" && c.agent.length > 0 &&
        VALID_REASONING_EFFORTS.includes(c.effort) &&
        (c.model === null || (typeof c.model?.providerID === "string" &&
          typeof c.model?.modelID === "string"));
    })) as Record<string, SessionAIConfig>;
  } catch {
    return {};
  }
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
  try {
    const raw = localStorage.getItem(PROVIDER_TELEMETRY_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed;
    }
  } catch {
    /* ignore */
  }
  return {};
}

export const OBSERVER_MODEL_KEY = "hoxi-observer-model:v1";
export const OBSERVER_AUTO_AUDIT_KEY = "hoxi-observer-auto-audit:v1";

export function initialObserverModel(): string {
  try {
    return localStorage.getItem(OBSERVER_MODEL_KEY) || "";
  } catch {
    return "";
  }
}

export function initialObserverAutoAudit(): boolean {
  try {
    const raw = localStorage.getItem(OBSERVER_AUTO_AUDIT_KEY);
    if (raw === "false") return false;
    return true;
  } catch {
    return true;
  }
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
}

export interface ComposerDraftEdit {
  id: string;
  text: string;
  attachments: QueuedPromptAttachment[];
  skillSelection?: SkillSelection;
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

export type ProviderModalTab = "providers" | "custom" | "auto" | "models" | "agents" | "policy";
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
  inspectorTab: "diffs" | "memory" | "files" | "skills" | "terminal" | "browser";
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
  providerTelemetry: Record<string, ProviderTelemetryStats>;
  observerModel: string;
  observerAutoAudit: boolean;
  auditsBySession: Record<string, CodeAuditResult>;
  auditingBySession: Record<string, boolean>;
  reviewsBySession: Record<string, WorkspaceReviewResult>;
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

function initialEffort(): ReasoningEffort {
  try {
    const saved = localStorage.getItem(EFFORT_KEY);
    if (saved !== null && (VALID_REASONING_EFFORTS as readonly string[]).includes(saved)) {
      return saved as ReasoningEffort;
    }
  } catch {
    /* ignore */
  }
  return "";
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
  stalledSinceBySession: {},
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
  reviewsBySession: {},
  interruptedBySession: {},
  sessionFinishedAt: {},
  unreadCompletedBySession: {},
  queuedPromptsBySession: {},
  composerDraftEditBySession: {},
});
