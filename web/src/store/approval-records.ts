// web/src/store/approval-records.ts
import { createSignal } from "solid-js";
import { t } from "../i18n";
import {
  hoxiAPI,
  runnerAPI,
  type ApprovalAuditEntry,
  type ApprovalGrant,
} from "../services/api";
import type { AgyHookStatus } from "../services/runner-api";
import type { PermissionRequest } from "../services/sse-types";
import {
  readStorageItem,
  removeStorageItem,
  workspace,
  writeStorageItem,
} from "./workspace-state";
import { getInternalLLMProviderModelGroups, isInternalLLMModel } from "./provider-catalog";

const [approvalGrants, setApprovalGrants] = createSignal<ApprovalGrant[]>([]);
const [approvalAuditEntries, setApprovalAuditEntries] = createSignal<ApprovalAuditEntry[]>([]);
const [approvalRecordsLoading, setApprovalRecordsLoading] = createSignal(false);

export { approvalAuditEntries, approvalGrants, approvalRecordsLoading };

const AI_REVIEW_DISABLED_KEY = "hoxi.aiReviewUserDisabled";
const FAST_REVIEW_KEYWORDS = ["flash", "mini", "haiku", "lite", "nano", "small", "fast"];
const REASONING_MODEL_ID_RE = /(?:^|[-_/.])(?:r1|qwq|reasoner|thinking|o1|o3|o4)(?:$|[-_/.])/i;
let aiReviewUserDisabledMem = false;

export function isAIReviewUserDisabled(): boolean {
  if (aiReviewUserDisabledMem) return true;
  return readStorageItem(AI_REVIEW_DISABLED_KEY) === "1";
}

export function setAIReviewUserDisabled(disabled: boolean): void {
  aiReviewUserDisabledMem = disabled;
  if (disabled) {
    writeStorageItem(AI_REVIEW_DISABLED_KEY, "1");
  } else {
    removeStorageItem(AI_REVIEW_DISABLED_KEY);
  }
}

function isLikelyReasoningModel(modelID: string, mInfo?: any): boolean {
  return Boolean(
    mInfo?.reasoning || mInfo?.capabilities?.reasoning || REASONING_MODEL_ID_RE.test(modelID)
  );
}

function parseInternalModelSpec(raw?: unknown): { providerID: string; modelID: string } | null {
  if (typeof raw !== "string") return null;
  const trimmed = raw.trim();
  const idx = trimmed.indexOf("/");
  if (idx <= 0 || idx >= trimmed.length - 1) return null;
  const candidate = {
    providerID: trimmed.slice(0, idx).trim(),
    modelID: trimmed.slice(idx + 1).trim(),
  };
  return isInternalLLMModel(candidate) ? candidate : null;
}

function pickFastestProviderModel(providerID: string, fallbackModelID: string): string {
  const prov = workspace.providers.find((p) => p.id === providerID);
  if (!prov?.models) return `${providerID}/${fallbackModelID}`;
  const entries = Object.entries(prov.models);
  for (const [mID, mInfo] of entries) {
    if (
      !isLikelyReasoningModel(mID, mInfo) &&
      FAST_REVIEW_KEYWORDS.some((kw) => mID.toLowerCase().includes(kw))
    ) {
      return `${providerID}/${mID}`;
    }
  }
  if (isLikelyReasoningModel(fallbackModelID, prov.models[fallbackModelID])) {
    const nonReasoning = entries.find(([mID, mInfo]) => !isLikelyReasoningModel(mID, mInfo));
    if (nonReasoning) return `${providerID}/${nonReasoning[0]}`;
  }
  return `${providerID}/${fallbackModelID}`;
}

export function resolveDefaultAIReviewModelFor(currentAIReviewModel?: string): string {
  const connSet = new Set(workspace.connectedProviders);
  const cfgProviders = workspace.globalConfig?.provider ?? {};
  const isUserConnected = (providerID: string) =>
    (connSet.has(providerID) ||
      Boolean(cfgProviders[providerID]) ||
      Boolean(workspace.hoxiProviders?.[providerID])) &&
    (providerID !== "opencode" ||
      Boolean(cfgProviders.opencode) ||
      Boolean(workspace.hoxiProviders?.opencode));

  const existing = parseInternalModelSpec(currentAIReviewModel);
  if (existing) {
    if (workspace.providers.length === 0) return `${existing.providerID}/${existing.modelID}`;
    if (isUserConnected(existing.providerID)) {
      const prov = workspace.providers.find((p) => p.id === existing.providerID);
      if (isLikelyReasoningModel(existing.modelID, prov?.models?.[existing.modelID])) {
        return pickFastestProviderModel(existing.providerID, existing.modelID);
      }
      return `${existing.providerID}/${existing.modelID}`;
    }
  }

  const small = parseInternalModelSpec(workspace.globalConfig?.small_model);
  if (small && (workspace.providers.length === 0 || isUserConnected(small.providerID))) {
    return pickFastestProviderModel(small.providerID, small.modelID);
  }

  for (const cand of [
    workspace.selectedModel,
    parseInternalModelSpec(workspace.globalConfig?.model),
    workspace.defaultAI?.model,
  ]) {
    if (cand && isInternalLLMModel(cand) && isUserConnected(cand.providerID)) {
      return pickFastestProviderModel(cand.providerID, cand.modelID);
    }
  }

  for (const group of getInternalLLMProviderModelGroups()) {
    if (!isUserConnected(group.providerID) || group.models.length === 0) continue;
    const provModels = workspace.providers.find((p) => p.id === group.providerID)?.models;
    const fastNonReasoning = group.models.find(
      (m) =>
        !isLikelyReasoningModel(m.id, provModels?.[m.id]) &&
        FAST_REVIEW_KEYWORDS.some((kw) => m.id.toLowerCase().includes(kw))
    );
    const anyNonReasoning = group.models.find(
      (m) => !isLikelyReasoningModel(m.id, provModels?.[m.id])
    );
    const fast = group.models.find((m) =>
      FAST_REVIEW_KEYWORDS.some((kw) => m.id.toLowerCase().includes(kw))
    );
    return (fastNonReasoning || anyNonReasoning || fast || group.models[0]).fullKey;
  }

  return "";
}

/**
 * Classes of recoverable work Go offers for a session grant on this request
 * ("本会话内此类都允许"); empty when the request can only be allowed once.
 */
export function grantableClasses(req: Partial<PermissionRequest> & Record<string, any>): string[] {
  const raw = req?.metadata?.hoxiGrantClasses;
  return Array.isArray(raw) ? raw.filter((c): c is string => typeof c === "string") : [];
}

export function grantClassLabel(cls: string): string {
  switch (cls) {
    case "temp-delete":
      return t("approval.grantClassTempDelete");
    case "dir-delete":
      return t("approval.grantClassDirDelete");
    case "git-discard":
      return t("approval.grantClassGitDiscard");
    default:
      return cls;
  }
}

/**
 * What AI review concluded for a request that reached the user (Go sets
 * metadata.hoxiAIReview): the model asked for confirmation, or gave no usable
 * verdict and why. Null when AI review was not involved.
 */
export type AIReviewNote = {
  status: "ask" | "unavailable";
  failure?: string;
  detail?: string;
  reason?: string;
  timeoutSeconds?: number;
};

export function aiReviewNote(req: Partial<PermissionRequest> & Record<string, any>): AIReviewNote | null {
  const raw = req?.metadata?.hoxiAIReview;
  if (!raw || typeof raw !== "object") return null;
  const status = raw.status;
  if (status !== "ask" && status !== "unavailable") return null;
  const text = (v: unknown) => (typeof v === "string" && v.trim() ? v.trim() : undefined);
  return {
    status,
    failure: text(raw.failure),
    detail: text(raw.detail),
    reason: text(raw.reason),
    timeoutSeconds: typeof raw.timeoutSeconds === "number" ? raw.timeoutSeconds : undefined,
  };
}

/** True when the reviewer model itself is the likely problem, so the card offers to change it. */
export function aiReviewNoteSuggestsModelChange(note: AIReviewNote): boolean {
  return note.status === "unavailable" && note.failure !== "error";
}

export function aiReviewNoteText(note: AIReviewNote): string {
  if (note.status === "ask") {
    return note.reason
      ? t("approval.aiReviewNoteAsk", { reason: note.reason })
      : t("approval.aiReviewNoteAskNoReason");
  }
  switch (note.failure) {
    case "empty":
      return t("approval.aiReviewNoteEmpty");
    case "timeout":
      return t("approval.aiReviewNoteTimeout", { seconds: note.timeoutSeconds ?? 25 });
    case "invalid":
      return t("approval.aiReviewNoteInvalid");
    case "unready":
      return t("approval.aiReviewNoteUnready");
    default:
      return note.detail
        ? t("approval.aiReviewNoteError", { detail: note.detail })
        : t("approval.aiReviewNoteErrorNoDetail");
  }
}

export async function loadApprovalGrants(sessionID?: string): Promise<ApprovalGrant[]> {
  try {
    const list = await hoxiAPI.listApprovalGrants(sessionID ? { sessionID } : undefined);
    setApprovalGrants(list);
    return list;
  } catch {
    return approvalGrants();
  }
}

export async function revokeApprovalGrant(id: string): Promise<void> {
  if (!id) return;
  const res = await hoxiAPI.revokeApprovalGrants({ id });
  setApprovalGrants(res.grants);
}

export async function revokeAllApprovalGrants(sessionID?: string): Promise<void> {
  const res = await hoxiAPI.revokeApprovalGrants(sessionID ? { sessionID } : { all: true });
  setApprovalGrants(res.grants);
}

export async function loadApprovalAudit(limit = 200): Promise<ApprovalAuditEntry[]> {
  setApprovalRecordsLoading(true);
  try {
    const list = await hoxiAPI.listApprovalAudit({ limit });
    setApprovalAuditEntries(list);
    return list;
  } catch {
    return approvalAuditEntries();
  } finally {
    setApprovalRecordsLoading(false);
  }
}

export interface DeferredEntry {
  id: string;
  sessionID: string;
  category?: string;
  command: string;
  reason?: string;
  createdAt: number;
  resolved?: boolean;
}

const [deferredEntries, setDeferredEntries] = createSignal<DeferredEntry[]>([]);
export { deferredEntries };

export async function loadDeferred(): Promise<void> {
  try {
    setDeferredEntries(await hoxiAPI.listDeferred());
  } catch {
    /* desktop-only */
  }
}

export async function resolveDeferred(id: string): Promise<void> {
  await hoxiAPI.resolveDeferred(id);
  await loadDeferred();
}

export async function clearApprovalAudit(): Promise<void> {
  await hoxiAPI.clearApprovalAudit();
  setApprovalAuditEntries([]);
}

export function fetchAgyHookStatus(): Promise<AgyHookStatus> {
  return runnerAPI.getAgyHookStatus();
}

export function installAgyHook(): Promise<AgyHookStatus> {
  return runnerAPI.installAgyHook();
}

export function uninstallAgyHook(): Promise<AgyHookStatus> {
  return runnerAPI.uninstallAgyHook();
}
