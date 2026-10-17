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
  createdAt: string;
  dirty?: boolean;
  changedFiles?: number;
  uncommittedLines?: number;
  additions?: number;
  deletions?: number;
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

export interface WorktreeStatus {
  sessionID: string;
  isolated: boolean;
  directory: string;
  branch?: string;
  baseCommit?: string;
  dirty: boolean;
  changedFiles: number;
  uncommittedLines: number;
  additions: number;
  deletions: number;
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
  source?: "env" | "config" | "custom" | "api";
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


