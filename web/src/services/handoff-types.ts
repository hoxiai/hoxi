// web/src/services/handoff-types.ts
export type ExternalToolID =
  | "grok-cli"
  | "claude-code"
  | "codex-cli"
  | "gemini-antigravity"
  | "codebuddy"
  | "trae"
  | "qoder"
  | "opencode-cli"
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

export interface HandoffPreviewFidelity {
  retainedTurns: number;
  retainedToolCalls: number;
  retainedFiles?: string[];
  unsupportedFeatures?: string[];
  targetDirectory: string;
  targetRuntime: string;
}

export interface ExternalSessionDetail {
  summary: ExternalSessionSummary;
  turns: NormalizedTurn[];
  planSteps?: ExternalPlanStep[];
  changedFiles?: string[];
  lastTurnSummary?: string;
  lastRecap?: string;
  fidelity?: HandoffPreviewFidelity;
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
