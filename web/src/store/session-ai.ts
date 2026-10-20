import { setWorkspace, workspace, type MessageTurn, type SelectedModel } from "./workspace";

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

const pendingHistoryPatches = new Map<string, Partial<SessionAIConfig>>();

export function persistSessionAI(): void {
  try { localStorage.setItem(SESSION_AI_KEY, JSON.stringify(workspace.aiBySession)); }
  catch { /* storage unavailable */ }
}

export function sessionAIConfig(sessionID: string | null = workspace.activeSessionID): SessionAIConfig {
  return (sessionID && workspace.aiBySession[sessionID]) || workspace.defaultAI;
}

export function initializeSessionAI(sessionID: string, config = workspace.defaultAI): void {
  if (workspace.aiBySession[sessionID]) return;
  setWorkspace("aiBySession", sessionID, { ...config, model: config.model ? { ...config.model } : null });
  persistSessionAI();
}

export function restoreSessionAI(sessionID: string, turns: MessageTurn[]): void {
  const pending = pendingHistoryPatches.get(sessionID);
  if (workspace.aiBySession[sessionID] && !pending) return;
  const session = workspace.sessions.find((s) => s.id === sessionID);
  const latest = [...turns].reverse();
  const model = latest.find((turn) => turn.info.providerID && turn.info.modelID)?.info;
  const fallbackModel =
    session?.providerID && session?.modelID
      ? { providerID: session.providerID, modelID: session.modelID }
      : workspace.defaultAI.model;
  const agent = latest.find((turn) => turn.info.agent)?.info.agent;
  const config: SessionAIConfig = {
    ...workspace.defaultAI,
    model: model ? { providerID: model.providerID!, modelID: model.modelID! } : fallbackModel,
    agent: agent || workspace.defaultAI.agent,
    ...pending,
  };
  if (pending) {
    pendingHistoryPatches.delete(sessionID);
    setWorkspace("aiBySession", sessionID, config);
    persistSessionAI();
  } else initializeSessionAI(sessionID, config);
}

export function updateSessionAI(patch: Partial<SessionAIConfig>, sessionID = workspace.activeSessionID): void {
  if (sessionID) {
    if ((!workspace.aiBySession[sessionID] || pendingHistoryPatches.has(sessionID)) &&
      workspace.loadingMessagesBySession[sessionID]) {
      pendingHistoryPatches.set(sessionID, { ...pendingHistoryPatches.get(sessionID), ...patch });
    }
    initializeSessionAI(sessionID);
    setWorkspace("aiBySession", sessionID, patch);
    persistSessionAI();
  } else {
    setWorkspace("defaultAI", patch);
  }
}

export function parseModelString(val?: string | null): SelectedModel | null {
  if (!val || typeof val !== "string") return null;
  const slashIdx = val.indexOf("/");
  if (slashIdx <= 0 || slashIdx >= val.length - 1) return null;
  return {
    providerID: val.slice(0, slashIdx),
    modelID: val.slice(slashIdx + 1),
  };
}

export function setActiveAgent(agent: string, sessionID = workspace.activeSessionID): void {
  const currentConfig = sessionAIConfig(sessionID);
  const oldAgent = currentConfig.agent;
  const oldAgentDedicated = workspace.globalConfig?.agent?.[oldAgent]?.model;

  const patch: Partial<SessionAIConfig> = { agent };

  const targetAgentDedicated = workspace.globalConfig?.agent?.[agent]?.model;
  if (typeof targetAgentDedicated === "string" && targetAgentDedicated) {
    const parsed = parseModelString(targetAgentDedicated);
    if (parsed) patch.model = parsed;
  } else if (oldAgentDedicated && currentConfig.model) {
    const oldParsed = parseModelString(oldAgentDedicated);
    if (oldParsed && oldParsed.providerID === currentConfig.model.providerID && oldParsed.modelID === currentConfig.model.modelID) {
      patch.model = workspace.defaultAI.model;
    }
  }

  updateSessionAI(patch, sessionID);
  if (!sessionID) {
    try { localStorage.setItem(DEFAULT_AGENT_KEY, agent); } catch { /* storage unavailable */ }
  }
}
