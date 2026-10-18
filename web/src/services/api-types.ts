// web/src/services/api-types.ts
export interface PromptAttachment {
  type: "file";
  mime: string;
  filename?: string;
  url: string;
}

export interface WorktreeMeta {
  sessionID: string;
  isolated: boolean;
  directory: string;
  branch?: string;
  baseCommit?: string;
  headCommit?: string;
  headSubject?: string;
  createdAt: string;
  dirty?: boolean;
  changedFiles?: number;
  uncommittedLines?: number;
  additions?: number;
  deletions?: number;
  aheadCount?: number;
  hasUpstream?: boolean;
}

export interface CommitAndPushResponse {
  ok: boolean;
  commit?: string;
  branch?: string;
  remote?: string;
  pushed?: boolean;
  message?: string;
}

export interface ProjectInfo {
  id: string;
  worktree: string;
  vcs?: string;
  icon?: { color?: string; url?: string };
  time: {
    created: number;
    updated: number;
  };
  sandboxes?: string[];
}

export interface WorkspaceFolderInput {
  path: string;
  isPrimary?: boolean;
}

export interface WorkspaceFolder {
  id: string;
  path: string;
  isPrimary: boolean;
  position: number;
}

export interface WorkspaceProfile {
  id: string;
  name: string;
  color: string;
  folders: WorkspaceFolder[];
  createdAt: string;
  updatedAt: string;
}

export interface SaveWorkspaceInput {
  name?: string;
  color?: string;
  folders: WorkspaceFolderInput[];
}

export interface WorktreeStatus {
  sessionID: string;
  isolated: boolean;
  directory: string;
  branch?: string;
  baseCommit?: string;
  headCommit?: string;
  headSubject?: string;
  dirty: boolean;
  changedFiles: number;
  uncommittedLines: number;
  additions: number;
  deletions: number;
  aheadCount?: number;
  hasUpstream?: boolean;
}

export interface GitBranchInfo {
  name: string;
  current: boolean;
  isSandbox: boolean;
  worktree?: string;
  sessionID?: string;
  headCommit?: string;
  subject?: string;
}

export interface BranchListResponse {
  currentBranch: string;
  branches: GitBranchInfo[];
  sandboxCount: number;
}

export interface BootstrapInfo {
  rootDir: string;
  wsBaseURL: string;
  status: {
    attention: number;
    running: number;
    queued: number;
    available: boolean;
  };
}

export interface SystemRuntimeInfo {
  openCodeBinPath: string;
  openCodeVersion: string;
  openCodePort: number;
  wsBaseURL: string;
  rootDir: string;
  userConfigPath: string;
  workspaceConfigPath: string;
  os: string;
  arch: string;
  goVersion: string;
  systemUsername?: string;
  gitUserName?: string;
  gitUserEmail?: string;
  status: {
    attention: number;
    running: number;
    queued: number;
    available: boolean;
  };
}

export interface ProviderModelInfo {
  id: string;
  name?: string;
  family?: string;
  reasoning?: boolean;
  attachment?: boolean;
  temperature?: boolean;
  tool_call?: boolean;
  release_date?: string;
  modalities?: {
    input?: string[];
    output?: string[];
  };
  capabilities?: {
    temperature?: boolean;
    reasoning?: boolean;
    attachment?: boolean;
    toolcall?: boolean;
    input?: {
      text?: boolean;
      audio?: boolean;
      image?: boolean;
      video?: boolean;
      pdf?: boolean;
    };
    output?: {
      text?: boolean;
      audio?: boolean;
      image?: boolean;
      video?: boolean;
      pdf?: boolean;
    };
  };
  cost?: {
    input?: number;
    output?: number;
    cache?: {
      read?: number;
      write?: number;
    };
  };
  limit?: {
    context?: number;
    input?: number;
    output?: number;
  };
  options?: Record<string, any>;
  variants?: Record<string, any>;
}

export interface ProviderInfo {
  id: string;
  name: string;
  source?: "env" | "config" | "custom" | "api" | "runner";
  env?: string[];
  key?: string;
  npm?: string;
  options?: Record<string, any>;
  models: Record<string, ProviderModelInfo>;
}

export interface ProviderAuthPrompt {
  type: "text" | "select";
  key: string;
  message: string;
  placeholder?: string;
  options?: Array<{ label: string; value: string; hint?: string }>;
  when?: { key: string; op: "eq" | "neq"; value: string };
}

export interface ProviderAuthMethod {
  type: "oauth" | "api";
  label: string;
  prompts?: ProviderAuthPrompt[];
}

export interface ProviderAuthAuthorization {
  url: string;
  method: "auto" | "code";
  instructions: string;
}

export interface ProviderListResponse {
  all: ProviderInfo[];
  default: Record<string, string>;
  connected: string[];
}

export interface SlashCommandInfo {
  name: string;
  description?: string;
  agent?: string;
}

export type CommandInfo = SlashCommandInfo;

export interface AgentInfo {
  name: string;
  description?: string;
  mode?: "primary" | "subagent" | "all";
  hidden?: boolean;
  native?: boolean;
  prompt?: string;
  temperature?: number;
}

export type ProviderIncidentType =
  | "timeout"
  | "api_error"
  | "retry"
  | "tool_error"
  | "hallucination_loop"
  | "user_revert"
  | "code_audit_fail";

export interface ProviderIncident {
  timestamp: number;
  type: ProviderIncidentType;
  modelID?: string;
  detail?: string;
}

export interface ModelTelemetryStats {
  modelID: string;
  totalTurns: number;
  successTurns: number;
  errorTurns: number;
  timeoutTurns: number;
  retryCount: number;
  toolCallsTotal: number;
  toolCallsFailed: number;
  hallucinationFlags: number;
  revertCount: number;
  cacheHitTurns: number;
  reasoningTurns?: number;
  auditTotalCount?: number;
  auditPassCount?: number;
  auditScores?: number[];
  lastAuditScore?: number;
  lastAuditPass?: boolean;
  lastAuditSummary?: string;
  ttftSamplesMs?: number[];
  tpsSamples?: number[];
  updatedAt: number;
}

export interface ProviderTelemetryStats {
  providerID: string;
  totalTurns: number;
  successTurns: number;
  errorTurns: number;
  timeoutTurns: number;
  retryCount: number;
  toolCallsTotal: number;
  toolCallsFailed: number;
  hallucinationFlags: number;
  revertCount: number;
  cacheHitTurns: number;
  reasoningTurns?: number;
  auditTotalCount?: number;
  auditPassCount?: number;
  auditScores?: number[];
  lastAuditScore?: number;
  lastAuditPass?: boolean;
  lastAuditSummary?: string;
  ttftSamplesMs?: number[];
  tpsSamples?: number[];
  lastProbeLatencyMs?: number;
  lastProbeOk?: boolean;
  models?: Record<string, ModelTelemetryStats>;
  recentIncidents?: ProviderIncident[];
  updatedAt: number;
}

export interface CodeAuditResult {
  ok: boolean;
  score: number;
  pass: boolean;
  summary: string;
  issues?: string[];
  observerProvider: string;
  observerModel: string;
  latencyMs?: number;
  error?: string;
  timestamp?: number;
  workerProviderID?: string;
  workerModelID?: string;
}

export interface CodeAuditRequest {
  sessionID?: string;
  workerProviderID: string;
  workerModelID: string;
  observerProviderID: string;
  observerModelID: string;
  observerBaseURL?: string;
  observerNPM?: string;
  userPrompt: string;
  diffs: Array<{
    file: string;
    before?: string;
    after?: string;
    additions: number;
    deletions: number;
  }>;
  lspErrorCount: number;
  lspErrorsSummary?: string;
  locale?: string;
}

export type ExternalToolID =
  | "grok-cli"
  | "claude-code"
  | "codex-cli"
  | "gemini-antigravity"
  | "codex";

export interface ExternalSessionSummary {
  id: string;
  source: ExternalToolID;
  sourceLabel: string;
  cwd: string;
  title: string;
  summary?: string;
  model?: string;
  gitBranch?: string;
  gitCommit?: string;
  createdAt: number;
  updatedAt: number;
  turnCount?: number;
  matchesCwd: boolean;
}

export interface NormalizedToolCall {
  callID: string;
  tool: string;
  title: string;
  input: Record<string, any>;
  output: string;
  status: string;
}

export interface NormalizedTurn {
  role: "user" | "assistant";
  timestamp: number;
  text: string;
  reasoning?: string;
  tools?: NormalizedToolCall[];
  modelID?: string;
}

export interface ExternalPlanStep {
  title: string;
  status: string;
}

export interface ExternalSessionDetail {
  summary: ExternalSessionSummary;
  turns: NormalizedTurn[];
  planSteps?: ExternalPlanStep[];
  changedFiles?: string[];
  lastTurnSummary?: string;
  lastRecap?: string;
}

export interface ExternalSourceStats {
  installed: boolean;
  sessionCount: number;
}

export interface HandoffSessionsResponse {
  sessions: ExternalSessionSummary[];
  sources: Record<ExternalToolID, ExternalSourceStats>;
}

export interface HandoffImportResponse {
  ok: boolean;
  session?: any;
  importedTurns: number;
  memoryPath?: string;
  targetDirectory?: string;
  resolvedProviderID?: string;
  resolvedModelID?: string;
  error?: string;
}

export interface LocalCLIStatusItem {
  id: ExternalToolID;
  name: string;
  installed: boolean;
  authenticated: boolean;
  connected: boolean;
  version?: string;
  defaultModel?: string;
  models?: string[];
  sessionCount: number;
  authMode?: string;
  providerID: string;
}

export interface LocalCLIStatusResponse {
  tools: LocalCLIStatusItem[];
}

export interface LocalCLIImportResponse {
  ok: boolean;
  toolID: string;
  providerID: string;
  models?: string[];
  error?: string;
}

export interface GitDailyPulseResponse {
  todayCommits: number;
  committedAdditions: number;
  committedDeletions: number;
  committedFiles: number;
  uncommittedAdditions: number;
  uncommittedDeletions: number;
  uncommittedFiles: number;
  branch?: string;
  headCommit?: string;
  headSubject?: string;
  dbTotalTokens?: number;
  dbInputTokens?: number;
  dbOutputTokens?: number;
  dbReasoningTokens?: number;
  dbCacheReadTokens?: number;
  dbCacheWriteTokens?: number;
  dbTotalCost?: number;
  dbTurnsCount?: number;
  dbSessionsCount?: number;
  dbToolCallsTotal?: number;
  dbToolCallsFailed?: number;
  dbLatestTurnTs?: number;
  dbModels?: Array<{
    providerID: string;
    modelID: string;
    turns: number;
    totalTokens: number;
    cost: number;
  }>;
}

export interface AuthUserProfile {
  id: number;
  email: string;
  nickname: string;
  username?: string;
  avatar?: string;
  avatarUrl?: string;
  role?: string;
  plan?: string;
}

export interface AuthStatusSnapshot {
  isLoggedIn: boolean;
  isAuthorizing: boolean;
  pendingState?: string;
  user?: AuthUserProfile;
  expiresAt?: string;
  serverBaseUrl: string;
  lastVerifiedAt?: string;
}

export interface StartAuthResponse {
  state: string;
  authUrl: string;
}

export interface UpdateMirrors {
  cn?: string;
  global?: string;
}

export interface UpdateCheckResult {
  currentVersion: string;
  latestVersion: string;
  versionCode: number;
  needUpdate: boolean;
  title: string;
  releaseNotes: string;
  downloadUrl: string;
  mirrors: UpdateMirrors;
  mandatory: boolean;
  sha256?: string;
  checkedAt: string;
}

export interface LeaderboardModelItem {
  rank: number;
  slug: string;
  name: string;
  vendor: string;
  apiName: string;
  price: {
    currency: string;
    input: number;
    output: number;
    cacheRead?: number;
    cacheWrite?: number;
  };
  contextWindow: number;
  scores: {
    overall?: number;
    coding?: number;
    reasoning?: number;
    chinese?: number;
    agentic?: number;
  };
  perf: {
    tps?: number;
    ttft?: number;
  };
  badges: string[];
  scenes: string[];
  summary: string;
  reasoning?: boolean;
  isFree?: boolean;
}

export interface ConnectableGatewayItem {
  id: string;
  name: string;
  domain: string;
  url: string;
  baseUrl: string;
  isSelfOperated: boolean;
  canOneClickConnect: boolean;
  badge?: string;
  badgeTone?: "positive" | "info" | "warning";
  savingsPercent: number;
  priceLabel: string;
  officialPriceCNY: number;
  latencyMs: number;
  uptime: string;
  models: string[];
  recommendedModels: string[];
  features: string[];
  caveat: string;
  authUrl?: string;
}

export interface LeaderboardResponse {
  code: number;
  message: string;
  data: {
    dimension: string;
    updatedAt: string;
    total: number;
    items: LeaderboardModelItem[];
  };
}

export interface ConnectableGatewaysResponse {
  code: number;
  message: string;
  data: {
    total: number;
    items: ConnectableGatewayItem[];
  };
}
