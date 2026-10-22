// web/src/store/approval-mode.ts
import { createSignal } from "solid-js";
import { t } from "../i18n";
import {
  hoxiAPI,
  isRunnerPermissionID,
  runnerAPI,
  type ApprovalAuditEntry,
  type ApprovalGrant,
  type ApprovalPolicy,
  type ApprovalPolicyState,
} from "../services/api";
import type { AgyHookStatus } from "../services/runner-api";
import type { PermissionRequest } from "../services/sse-types";
export type {
  AgyHookStatus,
  ApprovalAuditEntry,
  ApprovalGrant,
  ApprovalPolicy,
  ApprovalPolicyState,
};
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
import { isDestructivePermissionRequest } from "../../../.opencode/lib/permission-policy.js";

export { isDestructivePermissionRequest, isRunnerPermissionID };

const [approvalPolicy, setApprovalPolicySignal] = createSignal<ApprovalPolicy>({ mode: "auto" });
const [approvalGrants, setApprovalGrants] = createSignal<ApprovalGrant[]>([]);
const [approvalAuditEntries, setApprovalAuditEntries] = createSignal<ApprovalAuditEntry[]>([]);
const [approvalRecordsLoading, setApprovalRecordsLoading] = createSignal(false);

export { approvalAuditEntries, approvalGrants, approvalPolicy, approvalRecordsLoading };

function isRuleObject(val: unknown): val is Record<string, any> {
  return Boolean(
    val &&
      typeof val === "object" &&
      !Array.isArray(val) &&
      Object.keys(val as Record<string, unknown>).length > 0
  );
}

function presetRule(
  mode: ApprovalMode,
  tool: "edit" | "webfetch" | "bash"
): "allow" | "smart" | "ask" {
  if (mode === "manual") return "ask";
  if (mode === "smart" && tool === "bash") return "smart";
  return "allow";
}

function applyApprovalPolicy(p: ApprovalPolicy) {
  setApprovalPolicySignal(p);
  updateAppSetting("approvalMode", p.mode); // appSettings.approvalMode serves as the UI cache
  if (appSettings.bashApprovalRule) updateAppSetting("bashApprovalRule", "");
}

// One-time migration: when ~/.hoxi/approval-policy.json does not exist yet,
// build the policy from legacy localStorage settings and OpenCode config strings.
function legacyApprovalPolicy(): ApprovalPolicy {
  const mode = appSettings.approvalMode || "auto";
  const perm = workspace.globalConfig?.permission ?? {};
  const preset = mode === "manual" ? "ask" : "allow";
  const pick = (v: unknown) => ((v === "allow" || v === "ask") && v !== preset ? v : "");
  const presetBash = mode === "manual" ? "ask" : mode === "smart" ? "smart" : "allow";
  const bash =
    appSettings.bashApprovalRule && appSettings.bashApprovalRule !== presetBash
      ? appSettings.bashApprovalRule
      : "";
  return { mode, edit: pick(perm.edit), webfetch: pick(perm.webfetch), bash };
}

let approvalPolicyGen = 0;

export async function loadApprovalPolicy(): Promise<void> {
  const gen = ++approvalPolicyGen;
  let state = await hoxiAPI.fetchApprovalPolicy();
  if (gen !== approvalPolicyGen) return;
  if (!state.exists) {
    try {
      await fetchGlobalConfig();
    } catch {
      /* use what is cached */
    }
    if (gen !== approvalPolicyGen) return;
    state = await hoxiAPI.updateApprovalPolicy(legacyApprovalPolicy());
    if (gen !== approvalPolicyGen) return;
  }
  applyApprovalPolicy(state.policy);
}

export async function setApprovalMode(mode: ApprovalMode): Promise<void> {
  const gen = ++approvalPolicyGen;
  // Switching approval modes clears per-tool overrides.
  const state = await hoxiAPI.updateApprovalPolicy({ mode });
  if (gen !== approvalPolicyGen) return;
  applyApprovalPolicy(state.policy);
}

export async function updateToolPermissionRule(
  tool: "edit" | "webfetch" | "bash",
  value: "allow" | "smart" | "ask"
): Promise<void> {
  const gen = ++approvalPolicyGen;
  const cur = approvalPolicy();
  const v = tool === "bash" ? value : value === "ask" ? "ask" : "allow";
  const preset = presetRule(cur.mode, tool);
  const state = await hoxiAPI.updateApprovalPolicy({ ...cur, [tool]: v === preset ? "" : v });
  if (gen !== approvalPolicyGen) return;
  applyApprovalPolicy(state.policy);
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
  permissionConfig: Record<string, any> | undefined = workspace.globalConfig?.permission,
  policy: ApprovalPolicy = approvalPolicy()
): EffectivePermissionSummary {
  const presetEdit: "allow" | "ask" = mode === "manual" ? "ask" : "allow";
  const presetWebfetch: "allow" | "ask" = mode === "manual" ? "ask" : "allow";
  const presetBash: "allow" | "smart" | "ask" =
    mode === "manual" ? "ask" : mode === "smart" ? "smart" : "allow";

  const activePolicy = policy.mode === mode ? policy : undefined;
  const editCfg = permissionConfig?.edit;
  const webCfg = permissionConfig?.webfetch;
  const bashCfg = permissionConfig?.bash;

  const edit: "allow" | "ask" =
    editCfg === "deny"
      ? "ask"
      : activePolicy?.edit === "allow" || activePolicy?.edit === "ask"
        ? activePolicy.edit
        : presetEdit;

  const webfetch: "allow" | "ask" =
    webCfg === "deny"
      ? "ask"
      : activePolicy?.webfetch === "allow" || activePolicy?.webfetch === "ask"
        ? activePolicy.webfetch
        : presetWebfetch;

  const bash: "allow" | "smart" | "ask" =
    bashCfg === "deny"
      ? "ask"
      : activePolicy?.bash === "allow" ||
          activePolicy?.bash === "smart" ||
          activePolicy?.bash === "ask"
        ? activePolicy.bash
        : presetBash;

  const hasCustomToolOverrides =
    edit !== presetEdit ||
    webfetch !== presetWebfetch ||
    bash !== presetBash ||
    Boolean(activePolicy?.edit || activePolicy?.webfetch || activePolicy?.bash) ||
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
    triggerDesktopNotification("Hoxi", t("approval.title"), {
      sessionID: req.sessionID,
      kind: "attention",
    });
  }
}

export function handleIncomingPermissionRequest(req: PermissionRequest): void {
  if (!req?.id || resolvedPermissionIDs.has(req.id)) return;
  addPendingPermissionToStore(req); // Dock only; auto-approval happens in the Go gate
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
  options: { rules?: Record<string, "allow" | "deny">; note?: string; grantClass?: boolean } = {}
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
    await hoxiAPI.replyPermission(
      req.id,
      mode,
      dir,
      mode === "reject" && note ? note : undefined,
      options.grantClass && mode === "once"
    );
  } catch (err) {
    if (isPermissionGoneError(err)) {
      markPermissionResolved(req.id);
      return;
    }
    throw err;
  }
  markPermissionResolved(req.id);
  if (mode === "always" || options.grantClass) {
    void loadApprovalGrants();
  }
  void loadApprovalAudit();
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
