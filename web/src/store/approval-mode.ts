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
  // Sibling workspace folders and SDK caches are routine edits; credential and
  // execution-controlling targets are filtered by isDestructivePermissionRequest.
  "external_directory",
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

// Approvals waiting on a session in another project are invisible to a query
// scoped to the active project, so the refresh also covers the directories of
// sessions that are still running.
function permissionSyncScopes(primary?: string): string[] {
  const scopes = new Set<string>();
  const add = (value?: string | null) => {
    const clean = normalizeFolderPath(value);
    if (!clean) return;
    scopes.add(clean);
    const wtIdx = clean.indexOf("/.hoxi/worktrees/");
    if (wtIdx > 0) scopes.add(clean.slice(0, wtIdx));
  };
  add(primary);
  add(workspace.rootDir);
  for (const [sid, status] of Object.entries(workspace.statusBySession)) {
    if (status?.type === "busy" || status?.type === "retry") add(sessionDirectory(sid));
  }
  return Array.from(scopes);
}

function resolvePermissionDirectory(req: PermissionRequest): string | undefined {
  const sessionDir = normalizeFolderPath(sessionDirectory(req.sessionID));
  if (sessionDir) return sessionDir;
  const metaDir =
    typeof req.metadata?.directory === "string"
      ? req.metadata.directory
      : typeof req.metadata?.cwd === "string"
        ? req.metadata.cwd
        : undefined;
  const cleanMeta = normalizeFolderPath(metaDir);
  if (cleanMeta) return cleanMeta;
  const indexed = workspace.sessionIndex[req.sessionID];
  if (indexed) {
    if (indexed.projectID && workspace.activeProjectID && indexed.projectID !== workspace.activeProjectID) {
      return undefined;
    }
    if (!indexed.projectID && !workspace.sessions.some((s) => s.id === req.sessionID)) {
      return undefined;
    }
  }
  return normalizeFolderPath(workspace.rootDir) || undefined;
}

function isDirectoryInScopes(dir: string | undefined, scopes: Set<string>): boolean {
  if (!dir) return false;
  if (scopes.has(dir)) return true;
  const wtIdx = dir.indexOf("/.hoxi/worktrees/");
  if (wtIdx > 0 && scopes.has(dir.slice(0, wtIdx))) return true;
  return false;
}

export async function syncPendingPermissions(directory?: string): Promise<void> {
  const scopes = permissionSyncScopes(directory);
  try {
    const results = await Promise.allSettled(
      scopes.map((scope) => hoxiAPI.listPermissions(scope))
    );
    const merged = new Map<string, PermissionRequest>();
    const answeredScopes = new Set<string>();
    results.forEach((res, index) => {
      if (res.status !== "fulfilled" || !Array.isArray(res.value)) return;
      answeredScopes.add(scopes[index]);
      for (const req of res.value) if (req?.id) merged.set(req.id, req);
    });
    if (answeredScopes.size === 0) return;
    const remoteIDs = new Set(merged.keys());
    if (workspace.pendingPermissions.length > 0) {
      // A snapshot only speaks for the directories it was taken in. Requests
      // from projects we did not query must survive; otherwise switching
      // projects would drop approvals that are still waiting on the user.
      setWorkspace("pendingPermissions", (prev) =>
        prev.filter((p) => remoteIDs.has(p.id) || !isDirectoryInScopes(resolvePermissionDirectory(p), answeredScopes))
      );
    }
    for (const req of merged.values()) {
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
