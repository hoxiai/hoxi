// web/src/store/approval-mode.ts
import { t } from "../i18n";
import { hoxiAPI } from "../services/api";
import type { PermissionRequest } from "../services/sse-types";
import { appSettings, triggerDesktopNotification, updateAppSetting } from "./settings";
import { setWorkspace, workspace } from "./workspace";
import { sessionDirectory } from "./workspace-sessions";

export type ApprovalMode = "auto" | "smart" | "manual";

const DESTRUCTIVE_RE =
  /\b(rm|sudo|chmod|chown|mkfs|dd|diskutil|killall|reboot|shutdown)\b|[>|]\s*\/etc\/|git\s+push\b[^\n;&|]*(--force\b|-f\b|--force-with-lease\b)|git\s+reset\b[^\n;&|]*--hard\b|git\s+clean\b[^\n;&|]*\s-[a-z]*f|\bfind\b[^\n;&|]*\s-(delete|exec|ok)\b|\b(curl|wget)\b[^\n|]*\|\s*(sh|bash|zsh)\b/i;

const SAFE_COMMANDS = [
  /^(ls|pwd|cat|head|tail|wc|find|grep|rg|fd)\b/,
  /^git\s+(status|diff|log|show|branch|rev-parse|worktree)\b/,
  /^go\s+(test|vet|build|list|env|version)\b/,
  /^npm\s+(test|run\s+build|run\s+lint|run\s+check)\b/,
  /^pnpm\s+(test|build|lint|check)\b/,
  /^cargo\s+(check|test)\b/,
  /^(pytest|tsc|npx\s+tsc)\b/,
  /^node\s+-v\b/,
];

const READ_PERMISSIONS = new Set(["read", "glob", "grep", "list", "lsp", "todoread"]);
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

const SHELL_SPLIT_RE = /\s*(?:&&|\|\||;|\|(?!\|)|(?<!>)&(?!>)|\n|\$\(|`)\s*/;

function pushText(out: string[], value: unknown) {
  if (typeof value === "string") {
    const text = value.trim();
    if (text) out.push(text);
    return;
  }
  if (Array.isArray(value)) {
    for (const item of value) pushText(out, item);
  }
}

export function extractPermissionCommands(req: Partial<PermissionRequest> & Record<string, any>): string[] {
  const out: string[] = [];
  pushText(out, req?.patterns);
  pushText(out, req?.pattern);
  const meta = (req?.metadata || {}) as Record<string, unknown>;
  pushText(out, meta.command);
  pushText(out, meta.cmd);
  pushText(out, meta.commands);
  if (out.length === 0) pushText(out, req?.title);
  return out;
}

function splitCommandChain(command: string): string[] {
  return String(command || "")
    .split(SHELL_SPLIT_RE)
    .map((segment) => segment.replace(/\)\s*$/, "").trim())
    .filter(Boolean);
}

function isSafeSegment(segment: string): boolean {
  if (/\s-(exec|delete|ok|fprint)\b/.test(segment)) return false;
  if (/[>|]\s*\/etc\//.test(segment)) return false;
  if (/`|\$\(/.test(segment)) return false;
  const withoutHarmlessRedirect = segment
    .replace(/\s+\d*>>?\s*\/dev\/null\b/g, "")
    .replace(/\s+2>&1\b/g, "")
    .trim();
  if (/[<>]/.test(withoutHarmlessRedirect)) return false;
  return SAFE_COMMANDS.some((re) => re.test(withoutHarmlessRedirect));
}

/**
 * Safety Gate (Rule 6): Destructive shell commands and doom_loop must NEVER auto-pass
 * regardless of the user's selected ApprovalMode.
 */
export function isDestructivePermissionRequest(
  req: Partial<PermissionRequest> & Record<string, any>
): boolean {
  const perm = String(req?.permission || req?.type || "").toLowerCase();
  if (perm === "doom_loop") return true;
  const commands = extractPermissionCommands(req);
  const blob = [perm, ...commands].join("\n");
  return DESTRUCTIVE_RE.test(blob);
}

export function isSafeCommandPermissionRequest(
  req: Partial<PermissionRequest> & Record<string, any>
): boolean {
  if (isDestructivePermissionRequest(req)) return false;
  const commands = extractPermissionCommands(req);
  if (commands.length === 0) return false;
  const segments = commands.flatMap(splitCommandChain);
  return segments.length > 0 && segments.every(isSafeSegment);
}

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
