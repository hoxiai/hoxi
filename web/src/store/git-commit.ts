// web/src/store/git-commit.ts
import { createSignal } from "solid-js";
import { locale } from "../i18n";
import { hoxiAPI, type CommitAndPushResponse } from "../services/api";
import type { MessagePart, SnapshotFileDiff } from "../services/sse-types";
import { isModelFree } from "../components/models/model-selector-utils";
import {
  loadSessionMessages,
  refreshWorktreeStatus,
  setWorkspace,
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
 * - `""` (default): Follow `small_model` (or auto-pick cheap/fast model, fallback to local rule)
 * - `"none"`: Never use LLM; synthesize commit message purely from session title & file paths (0ms)
 * - `"<providerID>/<modelID>"`: Use a specific designated cheap model
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

function parseProviderModelKey(raw?: unknown): SelectedModel | null {
  if (typeof raw !== "string" || !raw.includes("/")) return null;
  const idx = raw.indexOf("/");
  const providerID = raw.slice(0, idx).trim();
  const modelID = raw.slice(idx + 1).trim();
  if (!providerID || !modelID) return null;
  const exists = workspace.providers.some(
    (p) => p.id === providerID && p.models && p.models[modelID]
  );
  return exists ? { providerID, modelID } : null;
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

  // 2. Look for a connected free or lightweight/flash/mini/haiku model
  const connSet = new Set(workspace.connectedProviders);
  const cfgProviders = workspace.globalConfig?.provider ?? {};
  const cheapKeywords = ["flash", "mini", "haiku", "lite", "nano", "small", "fast"];

  for (const p of workspace.providers) {
    const isConn = connSet.has(p.id) || Boolean(cfgProviders[p.id]) || p.id === "opencode";
    if (!isConn || !p.models) continue;
    for (const [mID, mInfo] of Object.entries(p.models)) {
      if (isModelFree(p.id, mID, mInfo)) {
        return { providerID: p.id, modelID: mID };
      }
    }
  }

  for (const p of workspace.providers) {
    const isConn = connSet.has(p.id) || Boolean(cfgProviders[p.id]) || p.id === "opencode";
    if (!isConn || !p.models) continue;
    for (const mID of Object.keys(p.models)) {
      const lower = mID.toLowerCase();
      if (cheapKeywords.some((kw) => lower.includes(kw))) {
        return { providerID: p.id, modelID: mID };
      }
    }
  }

  return workspace.selectedModel || null;
}

export function synthesizeLocalCommitMessage(
  sessionID: string | null | undefined,
  diffs: SnapshotFileDiff[]
): string {
  const files = diffs.map((d) => d.file);
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

  const session = workspace.sessions.find((s) => s.id === sessionID);
  const turns = sessionID ? workspace.messagesBySession[sessionID] ?? [] : [];
  let rawIntent = session?.title || "";
  if (!rawIntent || rawIntent === "New Session" || rawIntent.startsWith("Session")) {
    for (let i = turns.length - 1; i >= 0; i--) {
      if (turns[i].info.role === "user") {
        const textPart = turns[i].parts.find(
          (p): p is Extract<MessagePart, { type: "text" }> => p.type === "text"
        );
        if (textPart?.text) {
          rawIntent = textPart.text.split("\n")[0].trim();
          break;
        }
      }
    }
  }

  const lower = rawIntent.toLowerCase();
  let prefix = "feat";
  if (/\b(fix|bug|error|issue|crash|repair)\b|\u4fee\u590d|\u62a5\u9519|\u95ee\u9898|\u5d29\u6e83/.test(lower)) prefix = "fix";
  else if (/\b(refactor|clean|split|modular)\b|\u91cd\u6784|\u62c6\u5206|\u6e05\u7406/.test(lower)) prefix = "refactor";
  else if (/\b(doc|readme|agents\.md)\b|\u6587\u6863/.test(lower)) prefix = "docs";
  else if (/\b(test|spec)\b|\u6d4b\u8bd5/.test(lower)) prefix = "test";

  const cleanSummary = rawIntent
    ? rawIntent
        .replace(/^(please\s+|can you\s+|help me\s+)/i, "")
        .replace(/[.!?\u3002\uff01\uff1f]+$/, "")
        .slice(0, 64)
        .trim()
    : `update ${files
        .slice(0, 2)
        .map((f) => f.split("/").pop())
        .join(", ")}${files.length > 2 ? ` and ${files.length - 2} more` : ""}`;

  return `${prefix}(${scope}): ${cleanSummary}`;
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
      if (patchExcerpt.length > 1000) break;
    }
  }
  return `${lines.join("\n")}${patchExcerpt ? `\n${patchExcerpt.slice(0, 1000)}` : ""}`;
}

export async function generateSmartCommitMessage(
  sessionID?: string | null,
  providedDiffs?: SnapshotFileDiff[]
): Promise<string> {
  const key = sessionID || workspace.activeSessionID || "__root__";
  let diffs = providedDiffs ?? workspace.diffsBySession[key] ?? [];
  if (diffs.length === 0) {
    const dirArg = key === "__root__" ? workspace.rootDir || undefined : undefined;
    diffs = await hoxiAPI.getWorktreeDiff(key, dirArg).catch(() => [] as SnapshotFileDiff[]);
  }

  const fallback = synthesizeLocalCommitMessage(sessionID ?? workspace.activeSessionID, diffs);
  const targetModel = resolveEffectiveCommitModel();
  if (!targetModel) {
    return fallback;
  }

  const provID = targetModel.providerID;
  const modID = targetModel.modelID;
  const runtimeProv = workspace.providers.find((p) => p.id === provID);
  const cfgProv = workspace.globalConfig?.provider?.[provID];
  const baseURL = cfgProv?.options?.baseURL || runtimeProv?.options?.baseURL || undefined;
  const npm = cfgProv?.npm || runtimeProv?.npm || undefined;
  const session = workspace.sessions.find((s) => s.id === (sessionID ?? workspace.activeSessionID));

  try {
    const res = await hoxiAPI.generateCommitMessage({
      providerID: provID,
      modelID: modID,
      baseURL,
      npm,
      sessionTitle: session?.title || "",
      lastPrompt: extractLastUserPrompt(sessionID ?? workspace.activeSessionID),
      diffSummary: buildCompactDiffSummary(diffs),
      locale: locale(),
    });
    if (res?.ok && res.message?.trim()) {
      return res.message.trim();
    }
  } catch (err) {
    console.warn("LLM commit message synthesis failed, using local fallback:", err);
  }
  return fallback;
}

export async function executeTaskCommitAndPush(
  sessionID?: string | null,
  options?: { push?: boolean; customMessage?: string }
): Promise<CommitAndPushResponse> {
  const activeID = sessionID ?? workspace.activeSessionID;
  const key = activeID || "__root__";
  const shouldPush = options?.push ?? true;
  const dirArg = workspace.rootDir || undefined;

  const st = await hoxiAPI.getWorktreeStatus(key, key === "__root__" ? dirArg : undefined);
  let res: CommitAndPushResponse;

  if (st.dirty) {
    const diffs = await hoxiAPI
      .getWorktreeDiff(key, key === "__root__" ? dirArg : undefined)
      .catch(() => workspace.diffsBySession[key] ?? []);
    const msg =
      options?.customMessage?.trim() ||
      (await generateSmartCommitMessage(activeID, diffs));
    res = await hoxiAPI.commitWorktree(key, msg, {
      push: shouldPush,
      directory: dirArg,
    });
  } else if (shouldPush) {
    res = await hoxiAPI.pushWorktree(key, dirArg);
  } else {
    throw new Error("Nothing to commit, working tree clean");
  }

  setWorkspace("inspectedDiffFile", null);
  await refreshWorktreeStatus(activeID);
  await refreshWorktreeStatus("__root__");
  if (activeID) {
    await loadSessionMessages(activeID).catch(() => {});
  }
  return res;
}
