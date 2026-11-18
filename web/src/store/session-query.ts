// web/src/store/session-query.ts
// Read-only session lookups (directory, busy/archived/deleted flags, runtime).
// Leaf module (workspace-state + auto-models only) so every domain module can use
// it without creating import cycles.
import { createSignal } from "solid-js";
import {
  inferRunnerRuntimeFromSessionID,
  isRunnerSessionID,
  resolveRunnerRuntimeForProvider,
} from "../services/api";
import type { SessionInfo, SessionRuntime } from "../services/sse-types";
import { AUTO_PROVIDER_ID, peekAutoModelTarget } from "./auto-models";
import { indexedSession } from "./session-index";
import {
  AGENT_ENGINE_STORAGE_KEY,
  preferredAgentEngine,
  setPreferredAgentEngine,
  workspace,
  type AgentEngineMode,
} from "./workspace-state";

export { AGENT_ENGINE_STORAGE_KEY, preferredAgentEngine, setPreferredAgentEngine };
export type { AgentEngineMode };

// Bumped when the user sends (or queues) a prompt or selects an active-task
// session so the timeline re-follows the live tail.
const [timelineFollowRequest, setTimelineFollowRequest] =
  createSignal<{ sessionID: string; seq: number } | null>(null);
export { timelineFollowRequest };

export function requestTimelineFollow(sessionID: string): void {
  setTimelineFollowRequest((prev) => ({ sessionID, seq: (prev?.seq ?? 0) + 1 }));
}

export function isSessionArchived(session: SessionInfo): boolean {
  if (session.time?.archived && session.time.archived > 0) return true;
  return workspace.archivedSessionIDs.includes(session.id);
}

const deletedSessionIDs = new Set<string>();
const MAX_DISMISSED_SHARED_BRANCH_NOTICES = 128;
const dismissedSharedBranchKeys = new Set<string>();
const [sharedBranchNoticeRev, setSharedBranchNoticeRev] = createSignal(0);

function formatSharedBranchNoticeKey(sessionID: string | null | undefined, peerIDs: string[]): string {
  const scope = (sessionID || "__draft__").trim() || "__draft__";
  const sorted = [...peerIDs].map((id) => id.trim()).filter(Boolean).sort();
  return `${scope}\0${sorted.join(",")}`;
}

export function isSharedBranchNoticeDismissed(
  sessionID: string | null | undefined,
  peerIDs: string[]
): boolean {
  sharedBranchNoticeRev();
  if (peerIDs.length === 0) return false;
  return dismissedSharedBranchKeys.has(formatSharedBranchNoticeKey(sessionID, peerIDs));
}

export function dismissSharedBranchNotice(
  sessionID: string | null | undefined,
  peerIDs: string[]
): void {
  if (peerIDs.length === 0) return;
  const key = formatSharedBranchNoticeKey(sessionID, peerIDs);
  if (dismissedSharedBranchKeys.has(key)) return;
  while (dismissedSharedBranchKeys.size >= MAX_DISMISSED_SHARED_BRANCH_NOTICES) {
    const oldest = dismissedSharedBranchKeys.values().next().value;
    if (oldest === undefined) break;
    dismissedSharedBranchKeys.delete(oldest);
  }
  dismissedSharedBranchKeys.add(key);
  setSharedBranchNoticeRev((v) => v + 1);
}

export function isSessionDeleted(id: string): boolean {
  return deletedSessionIDs.has(id);
}

export function markSessionDeleted(id: string): void {
  deletedSessionIDs.add(id);
  const prefix = `${id}\0`;
  let removed = false;
  for (const key of Array.from(dismissedSharedBranchKeys)) {
    if (key.startsWith(prefix)) {
      dismissedSharedBranchKeys.delete(key);
      removed = true;
    }
  }
  if (removed) setSharedBranchNoticeRev((v) => v + 1);
}

export function activeSession(): SessionInfo | undefined {
  return workspace.sessions.find((s) => s.id === workspace.activeSessionID);
}

export function isManagedSession(sessionID?: string | null): boolean {
  if (!sessionID) return false;
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (session?.managed !== undefined) return Boolean(session.managed);
  if (session?.runtime && session.runtime !== "opencode") return true;
  const indexed = indexedSession(sessionID);
  if (indexed?.managed !== undefined) return Boolean(indexed.managed);
  if (indexed?.runtime && indexed.runtime !== "opencode") return true;
  return isRunnerSessionID(sessionID);
}

// Directory lookups must not fall back to the active project for a session that
// belongs elsewhere: a wrong directory sends writes, diffs and approvals into
// the wrong workspace. Only a session we know nothing about uses the root.
export function sessionDirectory(sessionID: string | null): string | undefined {
  if (!sessionID) return undefined;
  const meta = workspace.worktreeBySession[sessionID];
  const session = workspace.sessions.find((s) => s.id === sessionID);
  if (meta?.directory || session?.directory) {
    return meta?.directory || session?.directory;
  }
  if (session?.parentID) {
    const parentMeta = workspace.worktreeBySession[session.parentID];
    const parentSession = workspace.sessions.find((s) => s.id === session.parentID);
    if (parentMeta?.directory || parentSession?.directory) {
      return parentMeta?.directory || parentSession?.directory;
    }
  }
  const indexed = indexedSession(sessionID);
  if (indexed?.directory) return indexed.directory;
  const parentID = session?.parentID ?? indexed?.parentID;
  if (parentID) {
    const parentIndexed = indexedSession(parentID);
    if (parentIndexed?.directory) return parentIndexed.directory;
    const parentSession = workspace.sessions.find((s) => s.id === parentID);
    if (parentSession?.directory) return parentSession.directory;
  }
  const known = session || indexed;
  if (!known) return workspace.rootDir || undefined;
  // A known session without a directory may still be rooted in the active
  // project (unadopted OpenCode sessions often omit it). A session attributed
  // to a different project, or seen only in the global busy snapshot while not
  // present in the active project's session list, must never inherit the active root.
  const projectID = session?.projectID || indexed?.projectID;
  const activeProjectID = (workspace.activeProjectID || "").trim();
  if (projectID && activeProjectID && projectID !== activeProjectID) return undefined;
  if (!session && indexed && !indexed.directory && !indexed.projectID) return undefined;
  return workspace.rootDir || undefined;
}

export function activeDirectory(): string | undefined {
  if (!workspace.activeSessionID) return workspace.rootDir || undefined;
  return sessionDirectory(workspace.activeSessionID);
}

export function isSessionBusy(sessionID: string | null): boolean {
  if (!sessionID) return false;
  const st = workspace.statusBySession[sessionID]?.type;
  return st === "busy" || st === "retry";
}

export function isSessionTaskActive(sessionID: string | null): boolean {
  if (!sessionID) return false;
  if (isSessionBusy(sessionID)) return true;
  return (
    workspace.pendingPermissions.some(
      (p) =>
        p.sessionID === sessionID ||
        p.subagentSessionID === sessionID ||
        p.metadata?.subagentSessionID === sessionID
    ) ||
    workspace.pendingQuestions.some(
      (q) => q.sessionID === sessionID || q.subagentSessionID === sessionID
    )
  );
}

export function sessionsWithPendingPermission(): Set<string> {
  const set = new Set<string>();
  for (const p of workspace.pendingPermissions) {
    set.add(p.sessionID);
    const subID = p.subagentSessionID || p.metadata?.subagentSessionID;
    if (typeof subID === "string" && subID) set.add(subID);
  }
  for (const q of workspace.pendingQuestions) {
    set.add(q.sessionID);
    if (typeof q.subagentSessionID === "string" && q.subagentSessionID) {
      set.add(q.subagentSessionID);
    }
  }
  return set;
}

/**
 * True when sending would fail because no model is chosen: OpenCode and Go Native
 * need a configured model, while local CLI runtimes fall back to their own default.
 */
export function sessionNeedsModelSelection(sessionID?: string | null): boolean {
  const config = (sessionID && workspace.aiBySession[sessionID]) || workspace.defaultAI;
  if (config.model) return false;
  const agentModel = workspace.globalConfig?.agent?.[config.agent]?.model;
  if (typeof agentModel === "string" && agentModel) return false;
  const rt = sessionRuntimeForID(sessionID);
  return rt === "opencode" || rt === "native";
}

export function resolveSessionTargetRuntime(
  sessionID?: string | null,
  providerID?: string | null
): SessionRuntime {
  const hasConfigured = Boolean(workspace.hoxiProviders?.[providerID || ""]);
  const found = sessionID ? workspace.sessions.find((s) => s.id === sessionID) : undefined;
  const stickyEngine: AgentEngineMode =
    found?.runtime === "native"
      ? "native"
      : found?.runtime === "opencode"
        ? "opencode"
        : preferredAgentEngine();
  return resolveRunnerRuntimeForProvider(providerID, hasConfigured, stickyEngine);
}

export function sessionRuntimeForID(sessionID?: string | null): SessionRuntime {
  if (!sessionID) {
    const rawModel = workspace.defaultAI.model;
    const resolved =
      rawModel?.providerID === AUTO_PROVIDER_ID
        ? peekAutoModelTarget(rawModel.modelID)
        : rawModel;
    const draftProv = resolved?.providerID;
    return resolveRunnerRuntimeForProvider(
      draftProv,
      Boolean(workspace.hoxiProviders?.[draftProv || ""]),
      preferredAgentEngine()
    );
  }
  const found = workspace.sessions.find((s) => s.id === sessionID);
  if (found?.runtime) return found.runtime;
  return inferRunnerRuntimeFromSessionID(sessionID) || preferredAgentEngine();
}
