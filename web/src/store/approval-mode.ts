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


const READ_TOOL_NAMES = new Set([
  ...READ_PERMISSIONS, "ls", "notebookread", "notebook_read", "read_file", "view_file",
  "list_dir", "find_by_name", "grep_search", "read_resource", "list_resources",
  "read_browser_page", "browser_get_dom", "browser_get_network_request",
  "browser_list_network_requests", "list_browser_pages", "capture_browser_console_logs",
  "capture_browser_screenshot", "list_permissions", "list_plugin_accounts",
  "search_marketplace", "command_status",
]);

const EDIT_TOOL_NAMES = new Set([
  "edit", "write", "patch", "multiedit", "apply_patch", "notebookedit", "notebook_edit",
  "write_to_file", "write_file", "create_file", "edit_file", "replace_file_content",
  "multi_replace_file_content", "sed_file", "insert_edit_into_file", "str_replace_editor",
  "str_replace_based_edit_tool",
]);

const WEBFETCH_TOOL_NAMES = new Set([
  "webfetch", "websearch", "codesearch", "web_fetch", "web_search", "fetch_url",
  "read_url_content", "search_web", "execute_url",
]);

const BASH_TOOL_NAMES = new Set([
  "bash", "shell", "run_command", "execute_command", "run_terminal_cmd", "sh", "zsh", "cmd",
]);

const SMART_AUTO_PERMISSIONS = new Set([
  ...READ_TOOL_NAMES, ...EDIT_TOOL_NAMES, ...WEBFETCH_TOOL_NAMES,
  "todowrite", "task", "skill", "external_directory",
]);

function classifyToolToken(raw: unknown): "read" | "edit" | "webfetch" | "bash" | null {
  const name = String(raw || "").trim().toLowerCase();
  if (!name) return null;
  if (READ_TOOL_NAMES.has(name)) return "read";
  if (EDIT_TOOL_NAMES.has(name)) return "edit";
  if (WEBFETCH_TOOL_NAMES.has(name)) return "webfetch";
  if (BASH_TOOL_NAMES.has(name)) return "bash";
  return null;
}

export function resolvePermissionCategory(
  req: Partial<PermissionRequest> & Record<string, any>
): "read" | "edit" | "webfetch" | "bash" | "other" {
  const direct = classifyToolToken(req?.permission || req?.type);
  if (direct) return direct;
  const subTool =
    classifyToolToken(req?.metadata?.tool) ||
    classifyToolToken((req?.tool as any)?.name) ||
    classifyToolToken(req?.patterns?.[0]) ||
    classifyToolToken(req?.pattern);
  if (subTool) return subTool;
  return "other";
}

function isRuleObject(val: unknown): val is Record<string, any> {
  return Boolean(
    val &&
      typeof val === "object" &&
      !Array.isArray(val) &&
      Object.keys(val as Record<string, unknown>).length > 0
  );
}

function hasConfiguredPermissionEntry(val: unknown): boolean {
  if (val === "allow" || val === "ask" || val === "deny") return true;
  return isRuleObject(val);
}

function parseMetadataArgs(rawArgs: unknown): Record<string, any> {
  if (rawArgs && typeof rawArgs === "object" && !Array.isArray(rawArgs)) {
    return rawArgs as Record<string, any>;
  }
  if (typeof rawArgs === "string" && rawArgs.trim().startsWith("{")) {
    try {
      const parsed = JSON.parse(rawArgs);
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
        return parsed;
      }
    } catch {
      /* ignore invalid json */
    }
  }
  return {};
}

function extractCategoryTargets(
  req: Partial<PermissionRequest> & Record<string, any>,
  category: "edit" | "webfetch" | "bash"
): string[] {
  if (category === "bash") {
    return extractPermissionCommands(req);
  }
  const out: string[] = [];
  const perm = String(req?.permission || req?.type || "").toLowerCase();
  const toolNameLower = String(req?.metadata?.tool || (req?.tool as any)?.name || "").trim().toLowerCase();
  const pushCandidate = (v: unknown) => {
    if (typeof v !== "string") return;
    const trimmed = v.trim();
    if (!trimmed) return;
    if (perm === "tool" && (trimmed.toLowerCase() === toolNameLower || classifyToolToken(trimmed) !== null)) {
      return;
    }
    out.push(trimmed);
  };

  if (Array.isArray(req?.patterns)) {
    for (const p of req.patterns) pushCandidate(p);
  }
  pushCandidate(req?.pattern);
  const meta = req?.metadata || {};
  const args = parseMetadataArgs(meta.args);

  if (category === "edit") {
    for (const k of [
      "filepath", "filePath", "path", "target", "Path", "AbsolutePath",
      "TargetFile", "target_file", "file_path", "File", "file",
      "NotebookPath", "notebook_path",
    ]) {
      pushCandidate(meta[k]);
      pushCandidate(args[k]);
    }
    if (Array.isArray(meta.files)) {
      for (const f of meta.files) {
        pushCandidate(f?.filePath || f?.filepath || f?.file || f?.path);
      }
    }
  } else if (category === "webfetch") {
    for (const k of ["url", "Url", "URL", "uri", "URI", "domain", "Domain", "query", "Query"]) {
      pushCandidate(meta[k]);
      pushCandidate(args[k]);
    }
  }
  return Array.from(new Set(out));
}

function globPatternToRegExp(pattern: string): RegExp {
  const escaped = pattern
    .replace(/[.+^${}()|[\]\\]/g, "\\$&")
    .replace(/\*\*/g, "\0")
    .replace(/\*/g, "[^/]*")
    .replace(/\0/g, ".*")
    .replace(/\?/g, ".");
  return new RegExp(`^${escaped}$`, "i");
}

function matchesRulePattern(
  pattern: string,
  target: string,
  category: "edit" | "webfetch" | "bash"
): boolean {
  const p = pattern.trim();
  const t = target.trim();
  if (!p || !t) return false;
  if (p === "*" || p === "**" || p.toLowerCase() === t.toLowerCase()) return true;
  if (globPatternToRegExp(p).test(t)) return true;
  // Also allow '*' to match across '/' for URL / command globs like "https://docs.example.com/*"
  const looseRegex = new RegExp(
    `^${p.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*+/g, ".*").replace(/\?/g, ".")}$`,
    "i"
  );
  if (looseRegex.test(t)) return true;

  if (category === "webfetch") {
    try {
      const parsed = new URL(t.includes("://") ? t : `https://${t}`);
      const host = parsed.hostname.toLowerCase();
      const patLower = p.toLowerCase();
      if (host === patLower) return true;
      if (patLower.startsWith("*.") && (host === patLower.slice(2) || host.endsWith(patLower.slice(1)))) {
        return true;
      }
      if (looseRegex.test(host) || looseRegex.test(parsed.origin)) return true;
    } catch {
      /* ignore non-URL query strings */
    }
  }
  return false;
}

function evaluatePermissionRuleObject(
  req: Partial<PermissionRequest> & Record<string, any>,
  category: "edit" | "webfetch" | "bash",
  ruleObj: Record<string, any>
): "allow" | "ask" | "deny" {
  const entries = Object.entries(ruleObj).filter(
    ([, v]) => v === "allow" || v === "ask" || v === "deny"
  ) as Array<[string, "allow" | "ask" | "deny"]>;
  if (entries.length === 0) return "ask";

  const targets = extractCategoryTargets(req, category);
  if (targets.length === 0) {
    const wildcard = ruleObj["*"] ?? ruleObj["**"];
    return wildcard === "deny" ? "deny" : "ask";
  }

  let allAllowed = true;
  for (const target of targets) {
    const specificMatches = entries.filter(
      ([pat]) => pat.trim() !== "*" && pat.trim() !== "**" && matchesRulePattern(pat, target, category)
    );
    if (specificMatches.some(([, dec]) => dec === "deny")) return "deny";
    if (specificMatches.some(([, dec]) => dec === "ask")) return "ask";
    if (specificMatches.some(([, dec]) => dec === "allow")) continue;

    const wildcard = ruleObj["*"] ?? ruleObj["**"];
    if (wildcard === "deny") return "deny";
    if (wildcard === "allow") continue;
    allAllowed = false;
  }
  return allAllowed ? "allow" : "ask";
}

export interface EffectivePermissionSummary {
  mode: ApprovalMode;
  edit: "allow" | "ask";
  bash: "allow" | "smart" | "ask";
  webfetch: "allow" | "ask";
  hasCustomToolOverrides: boolean;
}

export function resolveEffectivePermissionSummary(
  mode: ApprovalMode = appSettings.approvalMode || "auto",
  permissionConfig: Record<string, any> | undefined = workspace.globalConfig?.permission
): EffectivePermissionSummary {
  const presetEdit: "allow" | "ask" = mode === "manual" ? "ask" : "allow";
  const presetWebfetch: "allow" | "ask" = mode === "manual" ? "ask" : "allow";
  const presetBash: "allow" | "smart" | "ask" =
    mode === "manual" ? "ask" : mode === "smart" ? "smart" : "allow";

  const editCfg = permissionConfig?.edit;
  const edit: "allow" | "ask" =
    editCfg === "ask" || editCfg === "deny" || isRuleObject(editCfg)
      ? "ask"
      : editCfg === "allow"
        ? "allow"
        : presetEdit;

  const webCfg = permissionConfig?.webfetch;
  const webfetch: "allow" | "ask" =
    webCfg === "ask" || webCfg === "deny" || isRuleObject(webCfg)
      ? "ask"
      : webCfg === "allow"
        ? "allow"
        : presetWebfetch;

  const bashOverride = appSettings.bashApprovalRule;
  const bash: "allow" | "smart" | "ask" =
    permissionConfig?.bash === "deny"
      ? "ask"
      : bashOverride === "allow" || bashOverride === "smart" || bashOverride === "ask"
        ? bashOverride
        : presetBash;

  const hasCustomToolOverrides =
    edit !== presetEdit ||
    webfetch !== presetWebfetch ||
    bash !== presetBash ||
    isRuleObject(editCfg) ||
    isRuleObject(webCfg) ||
    editCfg === "deny" ||
    webCfg === "deny";

  return {
    mode,
    edit,
    bash,
    webfetch,
    hasCustomToolOverrides,
  };
}

export function shouldAutoApprovePermission(
  req: Partial<PermissionRequest> & Record<string, any>,
  mode: ApprovalMode = appSettings.approvalMode || "auto",
  permissionOverride?: Record<string, any>
): boolean {
  // Rule 6 hard guarantee: destructive operations always require manual confirmation
  if (isDestructivePermissionRequest(req)) return false;

  const perm = String(req?.permission || req?.type || "").toLowerCase();
  const category = resolvePermissionCategory(req);

  if (READ_PERMISSIONS.has(perm)) return true;
  if (category === "read" && req?.metadata?.externalDirectory !== true) {
    return true;
  }

  const isCurrentModeContext =
    permissionOverride !== undefined || mode === (appSettings.approvalMode || "auto");
  const permCfg =
    permissionOverride ??
    (workspace.globalConfig?.permission &&
    typeof workspace.globalConfig.permission === "object" &&
    !Array.isArray(workspace.globalConfig.permission)
      ? workspace.globalConfig.permission
      : undefined);

  if (category === "edit") {
    const editRule = permCfg?.edit;
    if (editRule === "ask" || editRule === "deny") return false;
    if (isRuleObject(editRule)) {
      return evaluatePermissionRuleObject(req, "edit", editRule) === "allow";
    }
    if (editRule === "allow" && isCurrentModeContext) {
      return true;
    }
    return mode !== "manual";
  }

  if (category === "webfetch") {
    const webRule = permCfg?.webfetch;
    if (webRule === "ask" || webRule === "deny") return false;
    if (isRuleObject(webRule)) {
      return evaluatePermissionRuleObject(req, "webfetch", webRule) === "allow";
    }
    if (webRule === "allow" && isCurrentModeContext) {
      return true;
    }
    return mode !== "manual";
  }

  if (category === "bash") {
    const bashCfg = permCfg?.bash;
    if (bashCfg === "deny") return false;
    if (isRuleObject(bashCfg)) {
      const objDecision = evaluatePermissionRuleObject(req, "bash", bashCfg);
      if (objDecision === "deny" || objDecision === "ask") return false;
    }
    const effectiveBashRule: "allow" | "smart" | "ask" =
      permissionOverride?.bash === "allow" ||
      permissionOverride?.bash === "smart" ||
      permissionOverride?.bash === "ask"
        ? permissionOverride.bash
        : isCurrentModeContext &&
            (appSettings.bashApprovalRule === "allow" ||
              appSettings.bashApprovalRule === "smart" ||
              appSettings.bashApprovalRule === "ask")
          ? appSettings.bashApprovalRule
          : mode === "manual"
            ? "ask"
            : mode === "smart"
              ? "smart"
              : "allow";

    if (effectiveBashRule === "ask") return false;
    if (effectiveBashRule === "smart") return isSafeCommandPermissionRequest(req);
    return true;
  }

  if (mode === "manual") {
    return false;
  }

  if (mode === "smart") {
    if (category === "read" && req?.metadata?.externalDirectory === true) {
      return true;
    }
    if (SMART_AUTO_PERMISSIONS.has(perm)) return true;
    if (perm === "tool") {
      const toolName = String(req?.metadata?.tool || req?.patterns?.[0] || "")
        .trim()
        .toLowerCase();
      if (READ_PERMISSIONS.has(toolName) || SMART_AUTO_PERMISSIONS.has(toolName)) {
        return true;
      }
    }
    return false;
  }

  // mode === "auto": auto-approve all non-destructive tool requests (unless overridden above)
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

  const dir = resolvePermissionDirectory(req);
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

export async function syncApprovalModeToOpenCodeConfig(
  mode: ApprovalMode,
  options?: { preserveCustomRules?: boolean }
): Promise<void> {
  try {
    const cfg = await fetchGlobalConfig();
    if (!cfg || typeof cfg !== "object" || Array.isArray(cfg)) return;
    const existingPerm =
      cfg.permission && typeof cfg.permission === "object" && !Array.isArray(cfg.permission)
        ? cfg.permission
        : {};
    const preserve = Boolean(options?.preserveCustomRules);
    const defaultRule = mode === "manual" ? "ask" : "allow";
    const desiredEdit =
      preserve && hasConfiguredPermissionEntry(existingPerm.edit)
        ? existingPerm.edit
        : defaultRule;
    const desiredWebfetch =
      preserve && hasConfiguredPermissionEntry(existingPerm.webfetch)
        ? existingPerm.webfetch
        : defaultRule;
    const desiredBash =
      isRuleObject(existingPerm.bash) || (preserve && existingPerm.bash === "deny")
        ? existingPerm.bash
        : "ask";
    if (
      existingPerm.edit === desiredEdit &&
      existingPerm.webfetch === desiredWebfetch &&
      existingPerm.bash === desiredBash
    ) {
      return;
    }
    await updateGlobalPermissions({
      edit: desiredEdit,
      webfetch: desiredWebfetch,
      // Keep bash at "ask" in OpenCode so hoxi-permissions.js & our Rule 6 safety gate always inspect commands
      bash: desiredBash,
    });
  } catch {
    /* ignore background sync errors without overwriting existing rules */
  }
}

function reevaluatePendingPermissions(
  mode: ApprovalMode,
  permissionOverride?: Record<string, any>
): void {
  const pending = [...workspace.pendingPermissions];
  for (const req of pending) {
    if (shouldAutoApprovePermission(req, mode, permissionOverride)) {
      setWorkspace("pendingPermissions", (prev) => prev.filter((p) => p.id !== req.id));
      handleIncomingPermissionRequest(req);
    }
  }
}

export function setApprovalMode(mode: ApprovalMode): void {
  updateAppSetting("approvalMode", mode);
  updateAppSetting("bashApprovalRule", "");
  const presetPerm =
    mode === "manual"
      ? { edit: "ask", webfetch: "ask" }
      : { edit: "allow", webfetch: "allow" };
  if (workspace.globalConfig?.permission && typeof workspace.globalConfig.permission === "object") {
    setWorkspace("globalConfig", "permission", (prev: Record<string, any>) => ({
      ...(prev || {}),
      ...presetPerm,
    }));
  }
  void syncApprovalModeToOpenCodeConfig(mode);
  reevaluatePendingPermissions(mode, presetPerm);
}

export async function updateToolPermissionRule(
  tool: "edit" | "webfetch" | "bash",
  value: "allow" | "smart" | "ask"
): Promise<void> {
  const existingPerm =
    workspace.globalConfig?.permission &&
    typeof workspace.globalConfig.permission === "object" &&
    !Array.isArray(workspace.globalConfig.permission)
      ? workspace.globalConfig.permission
      : {};
  const bashPatch =
    isRuleObject(existingPerm.bash) || existingPerm.bash === "deny"
      ? {}
      : { bash: "ask" };

  if (tool === "bash") {
    const nextBashRule: "allow" | "smart" | "ask" =
      value === "ask" ? "ask" : value === "smart" ? "smart" : "allow";
    await updateGlobalPermissions({
      ...bashPatch,
    });
    const currentMode = appSettings.approvalMode || "auto";
    const presetBash =
      currentMode === "manual" ? "ask" : currentMode === "smart" ? "smart" : "allow";
    updateAppSetting("bashApprovalRule", nextBashRule === presetBash ? "" : nextBashRule);
    reevaluatePendingPermissions(currentMode);
    return;
  }

  const ruleMode: "allow" | "ask" = value === "ask" ? "ask" : "allow";
  await updateGlobalPermissions({
    [tool]: ruleMode,
    ...bashPatch,
  });
  const currentMode = appSettings.approvalMode || "auto";
  reevaluatePendingPermissions(currentMode);
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
  const metaDir =
    typeof req.metadata?.directory === "string"
      ? req.metadata.directory
      : typeof req.metadata?.cwd === "string"
        ? req.metadata.cwd
        : undefined;
  const cleanMeta = normalizeFolderPath(metaDir);
  if (cleanMeta) return cleanMeta;
  const sessionDir = normalizeFolderPath(sessionDirectory(req.sessionID));
  if (sessionDir) return sessionDir;
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
  const dir = resolvePermissionDirectory(req);
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
