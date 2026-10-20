// web/src/store/git-commit.ts
import { createSignal } from "solid-js";
import { locale, t } from "../i18n";
import { hoxiAPI } from "../services/api";
import type { MessagePart, SnapshotFileDiff } from "../services/sse-types";
import { isModelFree } from "./provider-catalog";
import { AUTO_PROVIDER_ID, resolveAutoModelTarget } from "./auto-models";
import {
  canSteerSession,
  sessionDirectory,
  submitSessionPrompt,
  workspace,
  type SelectedModel,
} from "./workspace";

export const COMMIT_MODEL_STORAGE_KEY = "hoxi-commit-model:v1";

function loadInitialCommitModelSpec(): string {
  try {
    const saved = localStorage.getItem(COMMIT_MODEL_STORAGE_KEY);
    if (typeof saved === "string") return saved.trim();
  } catch {
    /* ignore */
  }
  return "";
}

const [commitModelSpecSignal, setCommitModelSpecSignal] = createSignal<string>(
  loadInitialCommitModelSpec()
);

/**
 * Commit message generation model specification:
 * - `""` (default): Follow `small_model` or active provider's fast/working model
 * - `"none"`: Never use LLM; synthesize English commit message from changed file paths (0ms)
 * - `"<providerID>/<modelID>"`: Use a specific designated model
 */
export function commitModelSpec(): string {
  return commitModelSpecSignal();
}

export function setCommitModelSpec(spec: string): void {
  const cleaned = spec.trim();
  setCommitModelSpecSignal(cleaned);
  try {
    if (cleaned) {
      localStorage.setItem(COMMIT_MODEL_STORAGE_KEY, cleaned);
    } else {
      localStorage.removeItem(COMMIT_MODEL_STORAGE_KEY);
    }
  } catch {
    /* ignore */
  }
}

const [aiCommitFallbackNotice, setAiCommitFallbackNotice] = createSignal(false);

export function hasAICommitFallbackNotice(): boolean {
  return aiCommitFallbackNotice();
}

export function dismissAICommitFallback(): void {
  setAiCommitFallbackNotice(false);
}

function parseProviderModelKey(raw?: unknown): SelectedModel | null {
  if (typeof raw !== "string" || !raw.includes("/")) return null;
  const idx = raw.indexOf("/");
  const providerID = raw.slice(0, idx).trim();
  const modelID = raw.slice(idx + 1).trim();
  if (!providerID || !modelID) return null;
  if (providerID === AUTO_PROVIDER_ID) {
    return resolveAutoModelTarget(modelID, { advance: true });
  }
  const exists = workspace.providers.some(
    (p) => p.id === providerID && p.models && p.models[modelID]
  );
  return exists ? { providerID, modelID } : null;
}

export function isMalformedLLMText(text?: string | null): boolean {
  if (!text) return true;
  const t = text.trim();
  if (!t) return true;
  return (
    t.startsWith("{") ||
    t.startsWith("[") ||
    t.includes('"choices"') ||
    t.includes('"finish_reason"') ||
    t.includes('"logprobs"')
  );
}

export function resolveEffectiveCommitModel(): SelectedModel | null {
  const spec = commitModelSpecSignal();
  if (spec === "none") return null;

  if (spec) {
    const explicit = parseProviderModelKey(spec);
    if (explicit) return explicit;
  }

  // 1. Prefer configured global small_model
  const small = parseProviderModelKey(workspace.globalConfig?.small_model);
  if (small) return small;

  const cheapKeywords = ["flash", "mini", "haiku", "lite", "nano", "small", "fast"];

  // 2. Check the user's currently active provider first (guaranteed to be authenticated & working)
  if (workspace.selectedModel?.providerID) {
    const activeProv = workspace.providers.find(
      (p) => p.id === workspace.selectedModel?.providerID
    );
    if (activeProv?.models) {
      for (const [mID, mInfo] of Object.entries(activeProv.models)) {
        const isReasoning = Boolean(mInfo?.reasoning || mInfo?.capabilities?.reasoning);
        if (!isReasoning && cheapKeywords.some((kw) => mID.toLowerCase().includes(kw))) {
          return { providerID: activeProv.id, modelID: mID };
        }
      }
      return workspace.selectedModel;
    }
  }

  // 3. Otherwise scan connected non-opencode providers, then opencode
  const connSet = new Set(workspace.connectedProviders);
  const cfgProviders = workspace.globalConfig?.provider ?? {};
  const orderedProviders = [...workspace.providers].sort((a, b) =>
    a.id === "opencode" ? 1 : b.id === "opencode" ? -1 : 0
  );

  for (const allowReasoning of [false, true]) {
    for (const p of orderedProviders) {
      const isConn = connSet.has(p.id) || Boolean(cfgProviders[p.id]);
      if (!isConn || !p.models) continue;
      for (const [mID, mInfo] of Object.entries(p.models)) {
        const isReasoning = Boolean(mInfo?.reasoning || mInfo?.capabilities?.reasoning);
        if (!allowReasoning && isReasoning) continue;
        if (
          cheapKeywords.some((kw) => mID.toLowerCase().includes(kw)) ||
          isModelFree(p.id, mID, mInfo)
        ) {
          return { providerID: p.id, modelID: mID };
        }
      }
    }
  }

  return workspace.selectedModel || null;
}

/**
 * Pure English structural fallback commit message derived strictly from modified file paths
 * and statuses (never copies raw user chat prompts or Chinese session titles).
 */
export function synthesizeLocalCommitMessage(
  _sessionID: string | null | undefined,
  diffs: SnapshotFileDiff[]
): string {
  const files = diffs.map((d) => d.file).filter(Boolean);
  if (files.length === 0) {
    return "chore(workspace): update project files";
  }

  const topDirs = Array.from(
    new Set(
      files.map((f) => {
        const parts = f.split("/").filter(Boolean);
        return parts.length > 1
          ? parts[parts.length - 2]
          : parts[0]?.replace(/\.[^.]+$/, "") || "core";
      })
    )
  ).slice(0, 2);
  const scope = topDirs.join(",") || "workspace";

  const allTests = files.every((f) => /(test|spec)\./i.test(f) || f.includes("__tests__"));
  const allDocs = files.every((f) => /\.(md|txt|rst)$/i.test(f));
  const anyAdded = diffs.some((d) => d.status === "added");
  const allDeleted = diffs.length > 0 && diffs.every((d) => d.status === "deleted");

  let prefix = "feat";
  if (allTests) prefix = "test";
  else if (allDocs) prefix = "docs";
  else if (allDeleted) prefix = "chore";

  const verb = allDeleted ? "remove" : anyAdded && diffs.length <= 3 ? "add and update" : "update";
  const baseNames = Array.from(
    new Set(
      files.map((f) => {
        const name = f.split("/").pop() || f;
        return name.replace(/\.(tsx|ts|jsx|js|go|py|rs|md|json|css)$/i, "");
      })
    )
  );

  const listed = baseNames.slice(0, 3).join(", ");
  const suffix = baseNames.length > 3 ? ` and ${baseNames.length - 3} more files` : "";
  return `${prefix}(${scope}): ${verb} ${listed}${suffix}`;
}

function buildSessionConversationDigest(sessionID?: string | null): string {
  if (!sessionID) return "";
  const turns = workspace.messagesBySession[sessionID] ?? [];
  if (turns.length === 0) return "";

  const recentTurns = turns.slice(-6);
  const entries: string[] = [];

  for (const turn of recentTurns) {
    const role = turn.info.role === "user" ? "User" : "Assistant";
    const textParts: string[] = [];
    for (const p of turn.parts) {
      if (p.type === "text" && p.text?.trim()) {
        textParts.push(p.text.trim().slice(0, 320));
      }
    }
    if (textParts.length > 0) {
      entries.push(`[${role}]: ${textParts.join(" ").replace(/\s+/g, " ").slice(0, 360)}`);
    }
  }

  return entries.slice(-4).join("\n");
}

function extractLastUserPrompt(sessionID?: string | null): string {
  if (!sessionID) return "";
  const turns = workspace.messagesBySession[sessionID] ?? [];
  for (let i = turns.length - 1; i >= 0; i--) {
    if (turns[i].info.role === "user") {
      const textPart = turns[i].parts.find(
        (p): p is Extract<MessagePart, { type: "text" }> => p.type === "text"
      );
      if (textPart?.text) return textPart.text.trim();
    }
  }
  return "";
}

function buildCompactDiffSummary(diffs: SnapshotFileDiff[]): string {
  const lines: string[] = [];
  for (const d of diffs.slice(0, 20)) {
    lines.push(
      `${d.status || "modified"} ${d.file} (+${d.additions || 0} -${d.deletions || 0})`
    );
  }
  let patchExcerpt = "";
  for (const d of diffs.slice(0, 6)) {
    if (!d.patch) continue;
    const snippet = d.patch
      .split("\n")
      .filter((l) => (l.startsWith("+") || l.startsWith("-")) && !l.startsWith("+++") && !l.startsWith("---"))
      .slice(0, 14)
      .join("\n");
    if (snippet) {
      patchExcerpt += `\n[${d.file}]\n${snippet}\n`;
      if (patchExcerpt.length > 1200) break;
    }
  }
  return `${lines.join("\n")}${patchExcerpt ? `\n${patchExcerpt.slice(0, 1200)}` : ""}`;
}

export async function generateSmartCommitMessage(
  sessionID?: string | null,
  providedDiffs?: SnapshotFileDiff[]
): Promise<string> {
  const activeID = sessionID ?? workspace.activeSessionID;
  const key = activeID || "__root__";
  let diffs = providedDiffs ?? workspace.diffsBySession[key] ?? [];
  if (providedDiffs === undefined && diffs.length === 0) {
    const dirArg = key === "__root__" ? workspace.rootDir || undefined : sessionDirectory(key);
    diffs = await hoxiAPI.getWorktreeDiff(key, dirArg).catch(() => [] as SnapshotFileDiff[]);
  }

  const fallback = synthesizeLocalCommitMessage(activeID, diffs);
  const targetModel = resolveEffectiveCommitModel();
  if (!targetModel) {
    return fallback;
  }

  const provID = targetModel.providerID;
  const modID = targetModel.modelID;
  const runtimeProv = workspace.providers.find((p) => p.id === provID);
  const cfgProv = workspace.globalConfig?.provider?.[provID];
  const baseURL =
    cfgProv?.options?.baseURL ||
    runtimeProv?.options?.baseURL ||
    undefined;
  const npm = cfgProv?.npm || runtimeProv?.npm || undefined;

  try {
    const res = await hoxiAPI.generateCommitMessage({
      providerID: provID,
      modelID: modID,
      baseURL,
      npm,
      sessionDigest: buildSessionConversationDigest(activeID),
      lastPrompt: extractLastUserPrompt(activeID),
      diffSummary: buildCompactDiffSummary(diffs),
      locale: locale(),
    });
    const candidate = res?.message?.trim() || "";
    if (res?.ok && candidate && !isMalformedLLMText(candidate)) {
      setAiCommitFallbackNotice(false);
      return candidate;
    }
    setAiCommitFallbackNotice(true);
  } catch (err) {
    console.warn("LLM commit message synthesis failed, using local fallback:", err);
    setAiCommitFallbackNotice(true);
  }
  return fallback;
}

export async function executeTaskCommitAndPush(
  sessionID?: string | null,
  options?: { push?: boolean; customMessage?: string }
): Promise<string> {
  const activeID = sessionID ?? workspace.activeSessionID;
  if (!activeID || activeID === "__root__") {
    throw new Error(t("timeline.gitSessionRequired"));
  }
  const directory = sessionDirectory(activeID);
  if (!directory) throw new Error(t("timeline.gitSessionRequired"));
  const customMessage = options?.customMessage?.trim();
  const cheapModel = canSteerSession(activeID)
    ? resolveEffectiveCommitModel() || undefined
    : undefined;
  const prompt = [
    t("timeline.gitAgentCommitPrompt", { directory: JSON.stringify(directory) }),
    t(options?.push === false ? "timeline.gitAgentCommitOnlyPrompt" : "timeline.gitAgentCommitPushPrompt"),
    customMessage ? t("timeline.gitAgentCommitMessagePrompt", { message: JSON.stringify(customMessage) }) : "",
  ].filter(Boolean).join("\n\n");
  // Dispatch is not completion; the agent reports actual Git results in the timeline.
  // Override only this prompt; the session's saved model remains unchanged.
  return submitSessionPrompt(prompt, activeID, { agent: "build", model: cheapModel });
}
