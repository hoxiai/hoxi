import { produce } from "solid-js/store";
import {
  DEFAULT_AGENT_KEY,
  OFFICIAL_PROVIDER_ID,
  SESSION_AI_KEY,
  setWorkspace,
  workspace,
  writeStorageItem,
  writeStorageJSON,
  type MessageTurn,
  type SelectedModel,
  type SessionAIConfig,
} from "./workspace-state";


const pendingHistoryPatches = new Map<string, Partial<SessionAIConfig>>();

export function persistSessionAI(): void {
  writeStorageJSON(SESSION_AI_KEY, workspace.aiBySession);
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
    writeStorageItem(DEFAULT_AGENT_KEY, agent);
  }
}

export function forgetPendingSessionAI(sessionID: string): void {
  if (!sessionID) return;
  pendingHistoryPatches.delete(sessionID);
}

const OPENAI_FAST_MODEL_RE = /(?:^|[/-])(?:gpt-(?:4o|5|6|7)|o[34](?:$|[.-])|codex)/i;

function isOfficialFastHost(rawURL?: string | null): boolean {
  const trimmed = (rawURL || "").trim();
  if (!trimmed) return true;
  try {
    const u = new URL(trimmed);
    const host = u.hostname.toLowerCase();
    const path = u.pathname.toLowerCase().replace(/\/+$/, "");
    if (
      host === "api.anthropic.com" ||
      host.endsWith(".anthropic.com") ||
      host === "api.openai.com" ||
      host.endsWith(".openai.com")
    ) {
      return true;
    }
    return path === "/backend-api/codex" || path === "/backend-api/codex/responses";
  } catch {
    return false;
  }
}

export function inferModelFastModeCapability(
  providerID?: string | null,
  modelID?: string | null,
  mInfo?: Record<string, any> | null,
  cfgModel?: Record<string, any> | null,
  customBaseURL?: string | null
): boolean {
  const explicit = mInfo?.fastMode ?? cfgModel?.fastMode;
  if (typeof explicit === "boolean") return explicit;

  const p = (providerID || "").trim().toLowerCase();
  const m = (modelID || "").trim().toLowerCase();
  if (!p || !m) return false;
  if (p === "hoxi-auto") return true;

  if (
    m.includes("haiku") ||
    m.includes("sonnet") ||
    m.includes("flash") ||
    m.includes("mini") ||
    m.includes("nano") ||
    m.includes("lite") ||
    m.includes("oss-") ||
    m.includes("image") ||
    m.includes("video") ||
    m.includes("audio") ||
    m.includes("realtime") ||
    m.includes("tts") ||
    m.includes("whisper") ||
    m.includes("embed")
  ) {
    return false;
  }

  if (p !== OFFICIAL_PROVIDER_ID && p !== "claude-code" && p !== "codex" && !isOfficialFastHost(customBaseURL)) {
    return false;
  }

  if (p === "anthropic" || p === "claude-code" || (p === OFFICIAL_PROVIDER_ID && m.includes("claude"))) {
    return m.includes("opus") || m.includes("fable") || m.includes("mythos");
  }

  if (p === "openai" || p === "codex" || p === OFFICIAL_PROVIDER_ID) {
    return OPENAI_FAST_MODEL_RE.test(m);
  }

  return false;
}

export function resolveEffectiveFastMode(
  model: SelectedModel | null | undefined,
  requested?: boolean
): boolean {
  if (!requested || !model?.providerID || !model?.modelID) return false;
  const prov = workspace.providers.find((p) => p.id === model.providerID);
  const mInfo = prov?.models?.[model.modelID] as Record<string, any> | undefined;
  const hoxiProv = workspace.hoxiProviders?.[model.providerID];
  const globalProv = workspace.globalConfig?.provider?.[model.providerID];
  const cfgModel = (hoxiProv?.models?.[model.modelID] ?? globalProv?.models?.[model.modelID]) as
    | Record<string, any>
    | undefined;
  const customBaseURL = hoxiProv?.options?.baseURL ?? globalProv?.options?.baseURL ?? null;
  return inferModelFastModeCapability(model.providerID, model.modelID, mInfo, cfgModel, customBaseURL);
}

export function setFastMode(enabled: boolean, sessionID = workspace.activeSessionID): void {
  const nextVal = enabled ? true : undefined;
  if (sessionID) {
    if (
      (!workspace.aiBySession[sessionID] || pendingHistoryPatches.has(sessionID)) &&
      workspace.loadingMessagesBySession[sessionID]
    ) {
      const prev = { ...pendingHistoryPatches.get(sessionID) };
      if (enabled) prev.fastMode = true;
      else delete prev.fastMode;
      pendingHistoryPatches.set(sessionID, prev);
    }
    initializeSessionAI(sessionID);
    setWorkspace(
      produce((state) => {
        const entry = state.aiBySession[sessionID];
        if (!entry) return;
        if (nextVal) entry.fastMode = true;
        else delete entry.fastMode;
      })
    );
    persistSessionAI();
  } else {
    setWorkspace(
      produce((state) => {
        if (nextVal) state.defaultAI.fastMode = true;
        else delete state.defaultAI.fastMode;
      })
    );
  }
}

export function toggleFastMode(sessionID = workspace.activeSessionID): boolean {
  const next = !Boolean(sessionAIConfig(sessionID).fastMode);
  setFastMode(next, sessionID);
  return next;
}
