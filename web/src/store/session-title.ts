// web/src/store/session-title.ts
import { dictionaries, locale } from "../i18n";
import { hoxiAPI } from "../services/api";
import { workspace, type SelectedModel } from "./workspace";

const customTitledSessions = new Map<string, string>();
const inFlightSummaries = new Set<string>();

export function recordCustomSessionTitle(sessionID: string, title: string): void {
  const cleaned = title.trim();
  if (sessionID && cleaned) {
    customTitledSessions.set(sessionID, cleaned);
  }
}

export function getCustomSessionTitle(sessionID: string): string | undefined {
  return customTitledSessions.get(sessionID);
}

export function isPlaceholderSessionTitle(title?: string | null): boolean {
  if (!title) return true;
  const trimmed = title.trim();
  if (!trimmed) return true;

  const knownPlaceholders = new Set<string>([
    dictionaries["zh-CN"].leftNav.newThread,
    dictionaries["en-US"].leftNav.newThread,
    dictionaries["zh-CN"].timeline.imagePromptSessionTitle,
    dictionaries["en-US"].timeline.imagePromptSessionTitle,
    "New Thread",
    "New Session",
    "Untitled",
  ]);

  if (knownPlaceholders.has(trimmed)) return true;
  const lower = trimmed.toLowerCase();
  return (
    lower.startsWith("new session -") ||
    lower.startsWith("child session -") ||
    lower === "new session"
  );
}

export function extractLocalFallbackTitle(promptText: string, currentLocale = locale()): string {
  const withoutCode = promptText.replace(/```[\s\S]*?```/g, " ").trim();
  const source = withoutCode || promptText.trim();
  const firstLine =
    source
      .split("\n")
      .map((l) => l.trim())
      .find((l) => l.length > 0) || "";

  let clause = firstLine.replace(/^[#*\->\s]+/, "").trim();
  for (const sep of ["。", "；", "！", "？", ". ", "? ", "! "]) {
    const idx = clause.indexOf(sep);
    if (idx > 6) {
      clause = clause.slice(0, idx);
      break;
    }
  }
  clause = clause.replace(/^["'`“”‘’「」【】]+|["'`“”‘’「」【】。！!？?，,；;：:]+$/g, "").trim();

  const maxChars = currentLocale.startsWith("zh") ? 18 : 42;
  if (!clause) {
    return dictionaries[currentLocale]?.leftNav.newThread ?? "New Thread";
  }
  const chars = Array.from(clause);
  return chars.length > maxChars ? chars.slice(0, maxChars).join("").trim() : clause;
}

function resolveTitleModel(hint?: SelectedModel): SelectedModel | null {
  const smallCfg = workspace.globalConfig?.small_model;
  if (typeof smallCfg === "string" && smallCfg.includes("/")) {
    const idx = smallCfg.indexOf("/");
    const pid = smallCfg.slice(0, idx).trim();
    const mid = smallCfg.slice(idx + 1).trim();
    if (
      pid &&
      mid &&
      workspace.providers.some((p) => p.id === pid && p.models && p.models[mid])
    ) {
      return { providerID: pid, modelID: mid };
    }
  }
  return hint || workspace.selectedModel || null;
}

export async function summarizeAndRenameSession(
  sessionID: string,
  promptText: string,
  modelHint: SelectedModel | undefined,
  renameFn: (id: string, title: string) => Promise<void>
): Promise<void> {
  if (!sessionID || inFlightSummaries.has(sessionID)) return;
  inFlightSummaries.add(sessionID);

  try {
    const currentLocale = locale();
    const currentSession = workspace.sessions.find((s) => s.id === sessionID);

    // 1. Immediately replace placeholder title ("新建会话" / "New Thread") with a clean local summary
    // so the user sees an instant title update while the LLM generates the polished localized title
    if (isPlaceholderSessionTitle(currentSession?.title)) {
      const immediateTitle = extractLocalFallbackTitle(promptText, currentLocale);
      if (immediateTitle && !isPlaceholderSessionTitle(immediateTitle)) {
        await renameFn(sessionID, immediateTitle);
      }
    }

    // 2. Request LLM-summarized session title strictly in the current UI language (`locale()`)
    const targetModel = resolveTitleModel(modelHint);
    const provID = targetModel?.providerID || "";
    const modID = targetModel?.modelID || "";
    const runtimeProv = workspace.providers.find((p) => p.id === provID);
    const cfgProv = workspace.globalConfig?.provider?.[provID];

    const baseURL =
      cfgProv?.options?.baseURL || runtimeProv?.options?.baseURL || undefined;
    const npm = cfgProv?.npm || runtimeProv?.npm || undefined;

    const res = await hoxiAPI.summarizeSessionTitle({
      providerID: provID,
      modelID: modID,
      baseURL,
      npm,
      prompt: promptText,
      locale: currentLocale,
    });

    const summarized = res?.title?.trim();
    if (summarized && !isPlaceholderSessionTitle(summarized)) {
      await renameFn(sessionID, summarized);
    }
  } catch (err) {
    console.warn("summarize session title failed:", err);
  } finally {
    inFlightSummaries.delete(sessionID);
  }
}
