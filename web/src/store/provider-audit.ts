// web/src/store/provider-audit.ts
import { produce } from "solid-js/store";
import { locale, t } from "../i18n";
import {
  hoxiAPI,
  type CodeAuditResult,
  type WorkspaceReviewResult,
  type WorkspaceReviewScope,
} from "../services/api";
import { resolveInternalLLMModel } from "./provider-catalog";
import { AUTO_PROVIDER_ID, resolveAutoModelTarget } from "./auto-models";
import {
  emptyStats,
  ensureModelStats,
  pushIncident,
  pushSample,
  schedulePersistTelemetry,
} from "./provider-telemetry";
import { updateSessionAI } from "./session-ai";
import {
  OBSERVER_AUTO_AUDIT_KEY,
  OBSERVER_MODEL_KEY,
  setWorkspace,
  workspace,
  type SelectedModel,
} from "./workspace-state";
import { sessionDirectory } from "./session-query";
import { submitSessionPrompt } from "./session-goal";

const auditedFingerprintsBySession = new Map<string, string>();

export function setObserverModelRole(fullKey: string) {
  const trimmed = fullKey.trim();
  setWorkspace("observerModel", trimmed);
  try {
    if (trimmed) {
      localStorage.setItem(OBSERVER_MODEL_KEY, trimmed);
    } else {
      localStorage.removeItem(OBSERVER_MODEL_KEY);
    }
  } catch {
    /* ignore */
  }
}

export function setObserverAutoAudit(enabled: boolean) {
  setWorkspace("observerAutoAudit", enabled);
  try {
    localStorage.setItem(OBSERVER_AUTO_AUDIT_KEY, enabled ? "true" : "false");
  } catch {
    /* ignore */
  }
}

function parseProviderModelKey(fullKey: string): SelectedModel | null {
  const trimmed = fullKey.trim();
  if (!trimmed || !trimmed.includes("/")) return null;
  const idx = trimmed.indexOf("/");
  const providerID = trimmed.slice(0, idx).trim();
  const modelID = trimmed.slice(idx + 1).trim();
  if (!providerID || !modelID) return null;
  if (providerID === AUTO_PROVIDER_ID) {
    return resolveAutoModelTarget(modelID, { advance: true });
  }
  return { providerID, modelID };
}

export function resolveObserverModel(workerProviderID: string): {
  providerID: string;
  modelID: string;
  baseURL?: string;
  npm?: string;
} | null {
  const configured = resolveInternalLLMModel(
    parseProviderModelKey(workspace.observerModel),
    parseProviderModelKey(
      typeof workspace.globalConfig?.observer_model === "string"
        ? workspace.globalConfig.observer_model
        : ""
    ),
    parseProviderModelKey(
      typeof workspace.globalConfig?.small_model === "string"
        ? workspace.globalConfig.small_model
        : ""
    ),
    parseProviderModelKey(
      typeof workspace.globalConfig?.model === "string" ? workspace.globalConfig.model : ""
    ),
    workspace.selectedModel,
    workspace.defaultAI?.model
  );

  if (!configured) return null;

  const provInfo = workspace.providers.find((p) => p.id === configured.providerID);
  const cfgBaseURL =
    workspace.globalConfig?.provider?.[configured.providerID]?.options?.baseURL ||
    provInfo?.options?.baseURL;
  const cfgNPM =
    workspace.globalConfig?.provider?.[configured.providerID]?.npm || provInfo?.npm;

  return {
    providerID: configured.providerID,
    modelID: configured.modelID,
    baseURL: typeof cfgBaseURL === "string" ? cfgBaseURL : undefined,
    npm: typeof cfgNPM === "string" ? cfgNPM : undefined,
  };
}

function resolveSessionWorkerAndPrompt(sessionID: string): {
  workerProviderID: string;
  workerModelID: string;
  userPrompt: string;
} {
  const turns = workspace.messagesBySession[sessionID] ?? [];
  let workerProviderID = workspace.selectedModel?.providerID || "";
  let workerModelID = workspace.selectedModel?.modelID || "";
  let userPrompt = "";

  for (let i = turns.length - 1; i >= 0; i--) {
    const turn = turns[i];
    if (turn.info.role === "assistant" && turn.info.providerID && !workerProviderID) {
      workerProviderID = turn.info.providerID;
      workerModelID = turn.info.modelID || "";
    }
    if (turn.info.role === "assistant" && turn.info.providerID) {
      workerProviderID = turn.info.providerID;
      workerModelID = turn.info.modelID || workerModelID;
      break;
    }
  }

  for (let i = turns.length - 1; i >= 0; i--) {
    const turn = turns[i];
    if (turn.info.role === "user") {
      const texts = turn.parts
        .filter((p): p is Extract<typeof p, { type: "text" }> => p.type === "text")
        .map((p) => p.text.trim())
        .filter(Boolean);
      if (texts.length > 0) {
        userPrompt = texts.join("\n");
        break;
      }
    }
  }

  return { workerProviderID, workerModelID, userPrompt };
}

function collectSessionLspErrors(sessionID: string): {
  lspErrorCount: number;
  lspErrorsSummary?: string;
} {
  const turns = workspace.messagesBySession[sessionID] ?? [];
  const latestByFile = new Map<
    string,
    Array<{ range?: { start?: { line?: number } }; message?: string; severity?: number }>
  >();

  for (const turn of turns) {
    for (const part of turn.parts) {
      if (part.type !== "tool" || part.state.status !== "completed") continue;
      const diagMap = part.state.metadata?.diagnostics;
      if (!diagMap || typeof diagMap !== "object") continue;
      for (const [filePath, list] of Object.entries(diagMap)) {
        if (Array.isArray(list)) {
          latestByFile.set(filePath, list);
        }
      }
    }
  }

  let lspErrorCount = 0;
  const summaries: string[] = [];
  for (const [filePath, list] of latestByFile.entries()) {
    const shortFile = filePath.split("/").pop() || filePath;
    for (const d of list) {
      if (!d || typeof d.message !== "string") continue;
      if (d.severity !== undefined && d.severity !== 1) continue;
      lspErrorCount++;
      if (summaries.length < 3) {
        const line = (d.range?.start?.line ?? 0) + 1;
        summaries.push(`${shortFile}:${line} ${d.message.trim()}`);
      }
    }
  }

  return {
    lspErrorCount,
    lspErrorsSummary: summaries.length > 0 ? summaries.join("; ") : undefined,
  };
}

/**
 * Reviews the session's workspace changes (uncommitted or against a base branch).
 * The backend collects the diffs and REVIEW.md from the base revision, so the
 * code under review cannot change the rules applied to it. Results are keyed by
 * session, so a late response never shows up under another session.
 */
export async function runWorkspaceReview(
  sessionID: string,
  scope: WorkspaceReviewScope,
  baseRef = "",
): Promise<WorkspaceReviewResult | null> {
  const repoDir = sessionDirectory(sessionID);
  if (!sessionID || !repoDir) return null;
  const base = scope === "branch" ? baseRef.trim() : "";
  const store = (result: WorkspaceReviewResult) => {
    setWorkspace("reviewsBySession", sessionID, result);
    return result;
  };
  const failed = (error: string): WorkspaceReviewResult => ({
    ok: false,
    score: 0,
    pass: false,
    summary: "",
    error,
    observerProvider: "",
    observerModel: "",
    scope,
    baseRef: base || undefined,
    timestamp: Date.now(),
  });

  const { workerProviderID, workerModelID, userPrompt } = resolveSessionWorkerAndPrompt(sessionID);
  const observer = workerProviderID ? resolveObserverModel(workerProviderID) : null;
  if (!workerProviderID || !observer) {
    return store(failed(t("inspector.review.observerMissing")));
  }

  try {
    const lsp = collectSessionLspErrors(sessionID);
    const res = await hoxiAPI.auditProviderCode({
      sessionID,
      workerProviderID,
      workerModelID,
      observerProviderID: observer.providerID,
      observerModelID: observer.modelID,
      observerBaseURL: observer.baseURL,
      observerNPM: observer.npm,
      userPrompt: userPrompt || t("inspector.review.defaultRequest"),
      diffs: [],
      lspErrorCount: lsp.lspErrorCount,
      lspErrorsSummary: lsp.lspErrorsSummary,
      locale: locale(),
      reviewScope: scope,
      repoDir,
      baseRef: base || undefined,
    });
    return store({ ...res, scope, baseRef: base || undefined, timestamp: Date.now() });
  } catch (err: any) {
    return store(failed(err?.message || t("inspector.review.failed")));
  }
}

export async function runSessionCodeAudit(
  sessionID: string,
  force = false
): Promise<CodeAuditResult | null> {
  if (!sessionID) return null;
  if (!force && !workspace.observerAutoAudit) return null;
  if (workspace.auditingBySession[sessionID]) return null;

  // A failed or unfinished execution is not an assessable result-quality sample.
  const latestAssistant = (workspace.messagesBySession[sessionID] ?? [])
    .slice().reverse().find((turn) => turn.info.role === "assistant");
  if (!latestAssistant || latestAssistant.info.error || !latestAssistant.info.time?.completed) {
    return null;
  }

  const diffs = workspace.diffsBySession[sessionID] ?? [];
  if (diffs.length === 0) return null;

  const fingerprint = diffs
    .map((d) => `${d.file}:${d.additions}:${d.deletions}:${(d.after || "").length}`)
    .join("|");

  if (!force && auditedFingerprintsBySession.get(sessionID) === fingerprint) {
    return workspace.auditsBySession[sessionID] ?? null;
  }

  const { workerProviderID, workerModelID, userPrompt } =
    resolveSessionWorkerAndPrompt(sessionID);
  if (!workerProviderID) return null;

  const observer = resolveObserverModel(workerProviderID);
  if (!observer) return null;

  auditedFingerprintsBySession.set(sessionID, fingerprint);
  setWorkspace("auditingBySession", sessionID, true);

  try {
    const lsp = collectSessionLspErrors(sessionID);
    const res = await hoxiAPI.auditProviderCode({
      sessionID,
      workerProviderID,
      workerModelID,
      observerProviderID: observer.providerID,
      observerModelID: observer.modelID,
      observerBaseURL: observer.baseURL,
      observerNPM: observer.npm,
      userPrompt,
      diffs: diffs.slice(0, 8).map((d) => ({
        file: d.file,
        before: d.before,
        after: d.after,
        additions: d.additions,
        deletions: d.deletions,
      })),
      lspErrorCount: lsp.lspErrorCount,
      lspErrorsSummary: lsp.lspErrorsSummary,
      locale: locale(),
    });

    if (!res.ok || !Number.isFinite(res.score) || res.score < 0 || res.score > 100) return null;

    const enriched: CodeAuditResult = {
      ...res,
      timestamp: Date.now(),
      workerProviderID,
      workerModelID,
    };

    setWorkspace("auditsBySession", sessionID, enriched);

    setWorkspace(
      produce((state) => {
        const rec = (state.providerTelemetry[workerProviderID] ||=
          emptyStats(workerProviderID));
        rec.auditTotalCount = (rec.auditTotalCount ?? 0) + 1;
        const m = ensureModelStats(rec, workerModelID);
        if (m) {
          m.auditTotalCount = (m.auditTotalCount ?? 0) + 1;
        }
        if (res.pass) {
          rec.auditPassCount = (rec.auditPassCount ?? 0) + 1;
          if (m) m.auditPassCount = (m.auditPassCount ?? 0) + 1;
        } else {
          rec.hallucinationFlags += 1;
          if (m) m.hallucinationFlags += 1;
          rec.recentIncidents = pushIncident(
            rec.recentIncidents,
            "code_audit_fail",
            res.summary || `Audit score ${res.score}/100`,
            workerModelID
          );
        }
        rec.auditScores = pushSample(rec.auditScores, res.score, true);
        rec.lastAuditScore = res.score;
        rec.lastAuditPass = res.pass;
        rec.lastAuditSummary = res.summary;
        rec.updatedAt = Date.now();
        if (m) {
          m.auditScores = pushSample(m.auditScores, res.score, true);
          m.lastAuditScore = res.score;
          m.lastAuditPass = res.pass;
          m.lastAuditSummary = res.summary;
          m.updatedAt = rec.updatedAt;
        }
      })
    );
    schedulePersistTelemetry();
    return enriched;
  } catch {
    return null;
  } finally {
    setWorkspace("auditingBySession", sessionID, false);
  }
}

export async function applyAuditFeedbackToSession(
  sessionID: string,
  switchModel?: SelectedModel
) {
  const audit = workspace.auditsBySession[sessionID];
  if (!audit) return;

  if (switchModel) {
    updateSessionAI({ model: switchModel }, sessionID);
  }

  const issueLines =
    audit.issues && audit.issues.length > 0
      ? audit.issues.map((iss, idx) => `${idx + 1}. ${iss}`)
      : [`1. ${audit.summary}`];

  const promptText = [
    t("timeline.auditFeedbackPromptPrefix", { score: audit.score }),
    ...issueLines,
  ].join("\n");

  await submitSessionPrompt(promptText, sessionID);
}
