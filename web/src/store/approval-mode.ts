// web/src/store/approval-mode.ts
import { t } from "../i18n";
import { hoxiAPI } from "../services/api";
import type { PermissionRequest } from "../services/sse-types";
import { appSettings, triggerDesktopNotification, updateAppSetting } from "./settings";
import { setWorkspace, workspace } from "./workspace";
import { sessionDirectory } from "./workspace-sessions";
import {
  commandCandidates as extractPermissionCommands,
  isDestructivePermissionRequest,
  isSafeCommandPermissionRequest,
  READ_PERMISSIONS,
} from "../../../.opencode/lib/permission-policy.js";

export { extractPermissionCommands, isDestructivePermissionRequest, isSafeCommandPermissionRequest };

export type ApprovalMode = "auto" | "smart" | "manual";

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

function addPendingPermissionToStore(req: PermissionRequest) {
  let added = false;
  setWorkspace("pendingPermissions", (prev) => {
    if (prev.some((p) => p.id === req.id)) return prev;
    added = true;
    return [...prev, req];
  });
  if (added) {
    triggerDesktopNotification("Hoxi", t("approval.title"));
  }
}

export function handleIncomingPermissionRequest(req: PermissionRequest): void {
  if (!req || !req.id) return;
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
    .then(() => {
      setWorkspace("pendingPermissions", (prev) => prev.filter((p) => p.id !== req.id));
    })
    .catch((err) => {
      console.warn("auto-approve permission fallback to manual dock:", err);
      addPendingPermissionToStore(req);
    })
    .finally(() => {
      inFlightAutoApprovals.delete(req.id);
    });
}

export async function syncApprovalModeToOpenCodeConfig(mode: ApprovalMode): Promise<void> {
  try {
    const cfg = await hoxiAPI.getGlobalConfig().catch(() => ({} as Record<string, any>));
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
    await hoxiAPI.updateGlobalConfig({
      ...cfg,
      permission: {
        ...existingPerm,
        edit: desiredEdit,
        webfetch: desiredWebfetch,
        // Keep bash at "ask" in OpenCode so hoxi-permissions.js & our Rule 6 safety gate always inspect commands
        bash: typeof existingPerm.bash === "object" ? existingPerm.bash : "ask",
      },
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
  try {
    const list = await hoxiAPI.listPermissions(directory || workspace.rootDir || undefined);
    if (!Array.isArray(list)) return;
    for (const req of list) {
      handleIncomingPermissionRequest(req);
    }
  } catch {
    /* ignore if endpoint not ready */
  }
}
