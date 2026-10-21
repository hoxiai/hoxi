// web/src/store/approval-mode.ts
import { createSignal } from "solid-js";
import { t } from "../i18n";
import {
  hoxiAPI,
  isRunnerPermissionID,
  runnerAPI,
  type ApprovalAuditEntry,
  type ApprovalGrant,
} from "../services/api";
import type { AgyHookStatus } from "../services/runner-api";
import type { PermissionRequest } from "../services/sse-types";
export type { AgyHookStatus, ApprovalAuditEntry, ApprovalGrant };
import {
  appSettings,
  triggerDesktopNotification,
  updateAppSetting,
  type ApprovalMode,
} from "./settings";
import { setWorkspace, workspace } from "./workspace-state";
import { sessionDirectory } from "./session-query";
import { normalizeFolderPath } from "./workspace-folders";
import { fetchGlobalConfig, updateGlobalPermissions } from "./global-config";
import {
  commandCandidates as extractPermissionCommands,
  isDestructivePermissionRequest,
  isSafeCommandPermissionRequest,
  READ_PERMISSIONS,
} from "../../../.opencode/lib/permission-policy.js";

export { extractPermissionCommands, isDestructivePermissionRequest, isRunnerPermissionID, isSafeCommandPermissionRequest };

const [approvalGrants, setApprovalGrants] = createSignal<ApprovalGrant[]>([]);
const [approvalAuditEntries, setApprovalAuditEntries] = createSignal<ApprovalAuditEntry[]>([]);
const [approvalRecordsLoading, setApprovalRecordsLoading] = createSignal(false);

export { approvalAuditEntries, approvalGrants, approvalRecordsLoading };


const SMART_AUTO_PERMISSIONS = new Set([
  "read",
  "glob",
  "grep",
  "list",
  "lsp",
  "todoread",
  "todowrite",
  "edit",
  "write",
  "patch",
  "multiedit",
  "apply_patch",
  "webfetch",
  "task",
  "skill",
]);

export function shouldAutoApprovePermission(
  req: Partial<PermissionRequest> & Record<string, any>,
  mode: ApprovalMode
): boolean {
  // Rule 6 hard guarantee: destructive operations always require manual confirmation
  if (isDestructivePermissionRequest(req)) return false;

  const perm = String(req?.permission || req?.type || "").toLowerCase();
  if (READ_PERMISSIONS.has(perm)) return true;

  if (mode === "manual") {
    return false;
  }

  if (mode === "smart") {
    if (SMART_AUTO_PERMISSIONS.has(perm)) return true;
    if (perm === "bash" || perm === "shell") {
      return isSafeCommandPermissionRequest(req);
    }
    return false;
  }

  // mode === "auto": auto-approve all non-destructive tool requests
  return true;
}

const inFlightAutoApprovals = new Set<string>();

// Requests already answered (here, by another client, or gone upstream). The
// 4s watchdog `listPermissions` snapshot can predate the reply; re-docking it
// flashed the approval card in and out and fired a stray Attention.
const RESOLVED_PERMISSION_LIMIT = 256;
const resolvedPermissionIDs = new Set<string>();

export function markPermissionResolved(id: string): void {
  if (!id) return;
  resolvedPermissionIDs.delete(id);
  resolvedPermissionIDs.add(id);
  if (resolvedPermissionIDs.size > RESOLVED_PERMISSION_LIMIT) {
    const oldest = resolvedPermissionIDs.values().next().value;
    if (oldest) resolvedPermissionIDs.delete(oldest);
  }
  setWorkspace("pendingPermissions", (prev) =>
    prev.some((p) => p.id === id) ? prev.filter((p) => p.id !== id) : prev
  );
}

/** Upstream no longer holds the request: it was already answered elsewhere. */
export function isPermissionGoneError(err: unknown): boolean {
  const msg = String(err);
  return /\bHTTP 404\b/.test(msg) || msg.includes("PermissionNotFound");
}

function addPendingPermissionToStore(req: PermissionRequest) {
  if (resolvedPermissionIDs.has(req.id)) return;
  let added = false;
  setWorkspace("pendingPermissions", (prev) => {
    if (prev.some((p) => p.id === req.id)) return prev;
    added = true;
    return [...prev, req];
  });
  if (added) {
    triggerDesktopNotification("Hoxi", t("approval.title"), { sessionID: req.sessionID, kind: "attention" });
  }
}

export function handleIncomingPermissionRequest(req: PermissionRequest): void {
  if (!req || !req.id || resolvedPermissionIDs.has(req.id)) return;
  const mode = appSettings.approvalMode || "auto";

  if (!shouldAutoApprovePermission(req, mode)) {
    addPendingPermissionToStore(req);
    return;
  }

  if (inFlightAutoApprovals.has(req.id)) return;
  inFlightAutoApprovals.add(req.id);

  const dir = sessionDirectory(req.sessionID);
  void hoxiAPI
    .replyPermission(req.id, "once", dir)
    .then(() => markPermissionResolved(req.id))
    .catch((err) => {
      if (isPermissionGoneError(err)) {
        markPermissionResolved(req.id);
        return;
      }
      console.warn("auto-approve permission fallback to manual dock:", err);
      addPendingPermissionToStore(req);
    })
    .finally(() => {
      inFlightAutoApprovals.delete(req.id);
    });
}

export async function syncApprovalModeToOpenCodeConfig(mode: ApprovalMode): Promise<void> {
  try {
    const cfg = await fetchGlobalConfig().catch(() => ({} as Record<string, any>));
    const existingPerm =
      cfg?.permission && typeof cfg.permission === "object" ? cfg.permission : {};
    const desiredEdit = mode === "manual" ? "ask" : "allow";
    const desiredWebfetch = mode === "manual" ? "ask" : "allow";
    if (
      existingPerm.edit === desiredEdit &&
      existingPerm.webfetch === desiredWebfetch &&
      existingPerm.bash === "ask"
    ) {
      return;
    }
    await updateGlobalPermissions({
      edit: desiredEdit,
      webfetch: desiredWebfetch,
      // Keep bash at "ask" in OpenCode so hoxi-permissions.js & our Rule 6 safety gate always inspect commands
      ...(typeof existingPerm.bash === "object" ? {} : { bash: "ask" }),
    });
  } catch {
    /* ignore background sync errors */
  }
}

export function setApprovalMode(mode: ApprovalMode): void {
  updateAppSetting("approvalMode", mode);
  void syncApprovalModeToOpenCodeConfig(mode);

  // Immediately evaluate any currently docked permissions under the new mode
  const pending = [...workspace.pendingPermissions];
  for (const req of pending) {
    if (shouldAutoApprovePermission(req, mode)) {
      setWorkspace("pendingPermissions", (prev) => prev.filter((p) => p.id !== req.id));
      handleIncomingPermissionRequest(req);
    }
  }
}

export async function syncPendingPermissions(directory?: string): Promise<void> {
  const scope = normalizeFolderPath(directory || workspace.rootDir);
  try {
    const list = await hoxiAPI.listPermissions(scope || undefined);
    if (!Array.isArray(list)) return;
    const remoteIDs = new Set(list.map((r) => r.id));
    if (workspace.pendingPermissions.length > 0) {
      // A snapshot only speaks for the directory it was taken in. Requests from
      // other projects must survive; otherwise switching projects would drop
      // approvals that are still waiting on the user.
      const inScope = (sessionID: string) => {
        if (!scope) return true;
        const dir = normalizeFolderPath(sessionDirectory(sessionID));
        if (dir) return dir === scope;
        return normalizeFolderPath(workspace.rootDir) === scope;
      };
      setWorkspace("pendingPermissions", (prev) =>
        prev.filter((p) => remoteIDs.has(p.id) || !inScope(p.sessionID))
      );
    }
    for (const req of list) {
      handleIncomingPermissionRequest(req);
    }
  } catch {
    /* ignore if endpoint not ready */
  }
}

export type PermissionReplyMode = "once" | "always" | "reject";

/**
 * Answers a docked permission request from the approval card. Selected rule
 * patterns are merged into `permission.<tool>` first (OpenCode only; runner
 * rules live in the Go gate). A reject note travels with the reply as the
 * rejection reason across both Runner and OpenCode without triggering a new turn.
 */
export async function replyPermissionRequest(
  req: PermissionRequest,
  mode: PermissionReplyMode,
  options: { rules?: Record<string, "allow" | "deny">; note?: string } = {}
): Promise<void> {
  const dir = sessionDirectory(req.sessionID);
  const runner = isRunnerPermissionID(req.id);
  const rules = Object.entries(options.rules ?? {});
  if (!runner && rules.length > 0) {
    try {
      await updateGlobalPermissions({ [req.permission || "bash"]: Object.fromEntries(rules) });
    } catch (err) {
      console.warn("failed to persist custom permission rules:", err);
    }
  }
  const note = (options.note ?? "").trim();
  try {
    await hoxiAPI.replyPermission(req.id, mode, dir, mode === "reject" && note ? note : undefined);
  } catch (err) {
    if (isPermissionGoneError(err)) {
      markPermissionResolved(req.id);
      return;
    }
    throw err;
  }
  markPermissionResolved(req.id);
  if (mode === "always") {
    void loadApprovalGrants();
  }
  void loadApprovalAudit();
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
