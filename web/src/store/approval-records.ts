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

const [approvalGrants, setApprovalGrants] = createSignal<ApprovalGrant[]>([]);
const [approvalAuditEntries, setApprovalAuditEntries] = createSignal<ApprovalAuditEntry[]>([]);
const [approvalRecordsLoading, setApprovalRecordsLoading] = createSignal(false);

export { approvalAuditEntries, approvalGrants, approvalRecordsLoading };

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
