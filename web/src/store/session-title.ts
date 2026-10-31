// web/src/store/session-title.ts
import { dictionaries, locale, t } from "../i18n";
import { hoxiAPI } from "../services/api";
import type { SessionInfo } from "../services/sse-types";
import { AUTO_PROVIDER_ID, resolveAutoModelTarget } from "./auto-models";
import { resolveInternalLLMModel } from "./provider-catalog";
import { workspace, type SelectedModel } from "./workspace-state";

export const MANUAL_TITLES_KEY = "hoxi-session-manual-titles:v1";

export function loadManualSessionTitles(): Map<string, string> {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(MANUAL_TITLES_KEY) || "{}");
    if (!value || typeof value !== "object" || Array.isArray(value)) return new Map();
    return new Map(Object.entries(value).filter(
      (entry): entry is [string, string] => Boolean(entry[0])
        && typeof entry[1] === "string" && Boolean(entry[1].trim())
    ).map(([id, title]) => [id, title.trim()]));
  } catch {
    return new Map();
  }
}

export const manualSessionTitles = loadManualSessionTitles();

export function persistManualSessionTitles(): void {
  try {
    localStorage.setItem(MANUAL_TITLES_KEY, JSON.stringify(Object.fromEntries(manualSessionTitles)));
  } catch {
    // Keep in-memory protection when storage is unavailable.
  }
}

const customTitledSessions = new Map<string, string>();
const inFlightSummaries = new Set<string>();

export function sessionActivityTime(s: SessionInfo): number {
  const local = workspace.sessionFinishedAt[s.id] || 0;
  const updated = s.time?.updated || 0;
  const created = s.time?.created || 0;
  return Math.max(local, updated, created);
}

export function sortSessionsByActivity(list: SessionInfo[]): SessionInfo[] {
  return [...list].sort((a, b) => sessionActivityTime(b) - sessionActivityTime(a));
}

export function recordCustomSessionTitle(sessionID: string, title: string): void {
  const cleaned = title.trim();
  if (sessionID && cleaned) {
    customTitledSessions.set(sessionID, cleaned);
  }
}

export function getCustomSessionTitle(sessionID: string): string | undefined {
  return manualSessionTitles.get(sessionID) ?? customTitledSessions.get(sessionID);
}

export function hasManualSessionTitle(sessionID: string): boolean {
  return manualSessionTitles.has(sessionID);
}

export function recordManualSessionTitle(sessionID: string, title: string): void {
  manualSessionTitles.set(sessionID, title.trim());
  persistManualSessionTitles();
}

export function forgetSessionTitles(sessionIDs: string[]): void {
  for (const id of sessionIDs) {
    customTitledSessions.delete(id);
    manualSessionTitles.delete(id);
  }
  persistManualSessionTitles();
}

export function preserveSessionTitle(info: SessionInfo): SessionInfo {
  const title = getCustomSessionTitle(info.id);
  return title && title !== info.title ? { ...info, title } : info;
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
    "Claude Code Session",
    "Antigravity Session",
    "Gemini CLI Session",
    "CodeBuddy Session",
    "Qoder Session",
  ]);

  if (knownPlaceholders.has(trimmed)) return true;
  if (
    trimmed.startsWith("{") ||
    trimmed.startsWith("[{") ||
    trimmed.startsWith('["') ||
    trimmed.includes('"choices"')
  ) {
    return true;
  }
  const lower = trimmed.toLowerCase();
  return (
    lower.startsWith("new session -") ||
    lower.startsWith("child session -") ||
    lower === "new session"
  );
}

// OpenCode names child sessions "<description> (@<agent> subagent)".
const SUBAGENT_TITLE_SUFFIX = /\s*\(@[\w.-]+ subagent\)\s*$/i;

export function stripSubagentTitleSuffix(title?: string | null): string {
  return (title || "").replace(SUBAGENT_TITLE_SUFFIX, "").trim();
}

function normalizeTitleForCompare(title: string): string {
  return title.normalize("NFKC").toLowerCase().replace(/[\p{P}\p{S}\s]+/gu, "");
}

/**
 * True when a subagent's title only restates its parent session's title:
 * equal after normalization, contained in it, or containing it with little extra.
 */
export function isRedundantSubagentTitle(
  parentTitle?: string | null,
  childTitle?: string | null,
): boolean {
  if (isPlaceholderSessionTitle(parentTitle)) return false;
  const parent = normalizeTitleForCompare(parentTitle!);
  const child = normalizeTitleForCompare(stripSubagentTitleSuffix(childTitle));
  if (!parent || !child) return false;
  if (parent === child) return true;
  const parentLen = Array.from(parent).length;
  const childLen = Array.from(child).length;
  if (Math.min(parentLen, childLen) < 4) return false;
  if (parent.includes(child)) return true;
  return child.includes(parent) && childLen <= parentLen * 1.5;
}

// Letters and digits of space-delimited scripts; CJK characters can be split anywhere.
const isWordChar = (c?: string) =>
  Boolean(c) && /[\p{L}\p{N}]/u.test(c!) && !/[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]/u.test(c!);

/**
 * Drops the leading phrase a subagent title shares with its parent, e.g.
 * "检查自动确认实现链路" under "检查自动确认是否有效" → "实现链路". Returns null when
 * the shared part is under half of the shorter title or too little would remain.
 */
function trimSharedTitlePrefix(parentTitle: string, childTitle: string): string | null {
  const parent = normalizeTitleForCompare(parentTitle);
  const chars = Array.from(childTitle);
  let matched = "";
  let cut = 0;
  for (let i = 0; i < chars.length; i++) {
    const n = normalizeTitleForCompare(chars[i]);
    if (!n) continue;
    if (!parent.startsWith(matched + n)) break;
    matched += n;
    cut = i + 1;
  }
  while (cut > 0 && isWordChar(chars[cut - 1]) && isWordChar(chars[cut])) cut--;
  const shared = Array.from(normalizeTitleForCompare(chars.slice(0, cut).join(""))).length;
  const shorter = Math.min(Array.from(parent).length, Array.from(normalizeTitleForCompare(childTitle)).length);
  if (shared < 4 || shared * 2 < shorter) return null;
  const rest = chars.slice(cut).join("").replace(/^[\p{P}\p{S}\s]+/u, "").trim();
  return Array.from(normalizeTitleForCompare(rest)).length >= 2 ? rest : null;
}

export interface SubagentTitle {
  /**
   * What to render: the subagent's own title, its remainder after the phrase it
   * shares with the parent ("…rest"), or "Subtask N" when it only repeats the parent.
   */
  label: string;
  /** The original title, for tooltips. */
  full: string;
  redundant: boolean;
}

export function subagentDisplayTitle(
  parentTitle: string | null | undefined,
  childTitle: string | null | undefined,
  ordinal: () => number,
): SubagentTitle {
  const full = stripSubagentTitleSuffix(childTitle);
  const redundant = !full || isRedundantSubagentTitle(parentTitle, full);
  if (!redundant) {
    const rest = isPlaceholderSessionTitle(parentTitle) ? null : trimSharedTitlePrefix(parentTitle!, full);
    return { label: rest ? `…${rest}` : full, full, redundant };
  }
  const index = ordinal();
  const label = index > 0
    ? t("timeline.subagentOrdinalTitle", { index })
    : t("timeline.subagentBreadcrumbLabel");
  return { label, full: full || label, redundant };
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
  for (const sep of ["\u3002", "\uff1b", "\uff01", "\uff1f", ". ", "? ", "! "]) {
    const idx = clause.indexOf(sep);
    if (idx > 6) {
      clause = clause.slice(0, idx);
      break;
    }
  }
  clause = clause
    .replace(/^["'`\u201c\u201d\u2018\u2019\u300c\u300d\u3010\u3011]+|["'`\u201c\u201d\u2018\u2019\u300c\u300d\u3010\u3011\u3002\uff01!\uff1f?\uff0c,\uff1b;\uff1a:]+$/g, "")
    .trim();

  const maxChars = currentLocale.startsWith("zh") ? 18 : 42;
  if (!clause) {
    return dictionaries[currentLocale]?.leftNav.newThread ?? "New Thread";
  }
  const chars = Array.from(clause);
  return chars.length > maxChars ? chars.slice(0, maxChars).join("").trim() : clause;
}

function parseFullModelKey(raw?: unknown): SelectedModel | null {
  if (typeof raw !== "string") return null;
  const idx = raw.indexOf("/");
  if (idx <= 0 || idx >= raw.length - 1) return null;
  const providerID = raw.slice(0, idx).trim();
  const modelID = raw.slice(idx + 1).trim();
  return providerID && modelID ? { providerID, modelID } : null;
}

function resolveTitleModel(hint?: SelectedModel): SelectedModel | null {
  const smallCfg = workspace.globalConfig?.small_model;
  let small: SelectedModel | null = null;
  if (typeof smallCfg === "string" && smallCfg.includes("/")) {
    const idx = smallCfg.indexOf("/");
    const pid = smallCfg.slice(0, idx).trim();
    const mid = smallCfg.slice(idx + 1).trim();
    if (pid === AUTO_PROVIDER_ID && mid) {
      small = resolveAutoModelTarget(mid, { advance: true });
    } else if (pid && mid) {
      small = { providerID: pid, modelID: mid };
    }
  }
  const rawFallback = hint || workspace.selectedModel || null;
  const fallback = rawFallback?.providerID === AUTO_PROVIDER_ID
    ? resolveAutoModelTarget(rawFallback.modelID, { advance: false }) || rawFallback
    : rawFallback;
  return resolveInternalLLMModel(
    small,
    fallback,
    parseFullModelKey(workspace.globalConfig?.model),
    workspace.defaultAI?.model
  );
}

export async function summarizeAndRenameSession(
  sessionID: string,
  promptText: string,
  modelHint: SelectedModel | undefined,
  renameFn: (id: string, title: string) => Promise<void>
): Promise<void> {
  if (!sessionID || hasManualSessionTitle(sessionID) || inFlightSummaries.has(sessionID)) return;
  inFlightSummaries.add(sessionID);

  try {
    const currentLocale = locale();
    const currentSession = workspace.sessions.find((s) => s.id === sessionID);

    // 1. Immediately replace placeholder title with a clean local summary
    // so the user sees an instant title update while the LLM generates the polished localized title
    if (isPlaceholderSessionTitle(currentSession?.title)) {
      const immediateTitle = extractLocalFallbackTitle(promptText, currentLocale);
      if (immediateTitle && !isPlaceholderSessionTitle(immediateTitle)) {
        await renameFn(sessionID, immediateTitle);
      }
    }

    if (hasManualSessionTitle(sessionID)) return;
    // 2. Request LLM-summarized session title strictly in the current UI language (`locale()`)
    const targetModel = resolveTitleModel(modelHint);
    // No HTTP-capable model for internal calls: keep the local fallback title
    // instead of asking the backend to echo the raw prompt as a "summary".
    if (!targetModel) return;
    const provID = targetModel.providerID;
    const modID = targetModel.modelID;
    const runtimeProv = workspace.providers.find((p) => p.id === provID);
    const cfgProv = workspace.globalConfig?.provider?.[provID];

    const baseURL =
      cfgProv?.options?.baseURL || runtimeProv?.options?.baseURL || undefined;
    const npm = cfgProv?.npm || runtimeProv?.npm || undefined;
    const strippedPrompt = promptText.replace(/```[\s\S]*?```/g, " ").trim() || promptText.trim();
    const boundedPrompt =
      strippedPrompt.length > 600 ? strippedPrompt.slice(0, 600) : strippedPrompt;

    const res = await hoxiAPI.summarizeSessionTitle({
      providerID: provID,
      modelID: modID,
      baseURL,
      npm,
      prompt: boundedPrompt,
      locale: currentLocale,
    });

    const summarized = res?.title?.trim();
    if (!hasManualSessionTitle(sessionID) && summarized && !isPlaceholderSessionTitle(summarized)) {
      await renameFn(sessionID, summarized);
    }
  } catch (err) {
    console.warn("summarize session title failed:", err);
  } finally {
    inFlightSummaries.delete(sessionID);
  }
}
