import type { SelectedModel } from "./workspace";

export interface SessionAIConfig {
  model: SelectedModel | null;
  agent: string;
  effort: "" | "low" | "medium" | "high";
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
        ["", "low", "medium", "high"].includes(c.effort) &&
        (c.model === null || (typeof c.model?.providerID === "string" &&
          typeof c.model?.modelID === "string"));
    })) as Record<string, SessionAIConfig>;
  } catch {
    return {};
  }
}
