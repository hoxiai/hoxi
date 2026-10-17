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
