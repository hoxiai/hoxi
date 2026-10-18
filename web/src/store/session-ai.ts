import { setWorkspace, workspace, type MessageTurn } from "./workspace";
import { DEFAULT_AGENT_KEY, SESSION_AI_KEY, type SessionAIConfig } from "./session-ai-storage";

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
  const latest = [...turns].reverse();
  const model = latest.find((turn) => turn.info.providerID && turn.info.modelID)?.info;
  const agent = latest.find((turn) => turn.info.agent)?.info.agent;
  const config: SessionAIConfig = {
    ...workspace.defaultAI,
    model: model ? { providerID: model.providerID!, modelID: model.modelID! } : workspace.defaultAI.model,
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

export function setActiveAgent(agent: string, sessionID = workspace.activeSessionID): void {
  updateSessionAI({ agent }, sessionID);
  if (!sessionID) {
    try { localStorage.setItem(DEFAULT_AGENT_KEY, agent); } catch { /* storage unavailable */ }
  }
}
