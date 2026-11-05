// web/src/store/subagent-sessions.ts
// Subagent (task tool) session tree: child-ID resolution, titles, liveness,
// parent/root lookups, code-session partitioning and drawer expansion state.
import { createSignal } from "solid-js";
import { hoxiAPI } from "../services/api";
import type { SessionInfo, ToolPart } from "../services/sse-types";
import { lookupSubagentParent, subagentChildIDFromPart } from "./message-lookup";
import { isSessionBusy } from "./session-query";
import { indexedSession, resolveSessionSurface } from "./session-index";
import { subagentDisplayTitle, type SubagentTitle } from "./session-title";
import { workspace, type MessageTurn } from "./workspace-state";

export function extractChildSubagentSessionId(tp: ToolPart): string | undefined {
  const direct = subagentChildIDFromPart(tp);
  if (direct) return direct;
  const input = tp.state.input as Record<string, any> | undefined;
  let desc = typeof input?.description === "string" ? input.description.trim() : "";
  if (!desc) {
    const raw = (tp.state as { raw?: string }).raw;
    if (typeof raw === "string") {
      const m = raw.match(/"description"\s*:\s*"((?:\\.|[^"\\])*)"/);
      if (m?.[1]) desc = m[1].trim();
    }
  }
  if (tp.sessionID && desc) {
    const matched = workspace.sessions.find(
      (s) =>
        s.parentID === tp.sessionID &&
        typeof s.title === "string" &&
        (s.title === desc || s.title.startsWith(`${desc} (`) || s.title.includes(desc))
    );
    if (matched?.id) return matched.id;
  }
  return undefined;
}

/** 1-based spawn order of `childID` among its parent's subagents, or 0 when unknown. */
export function subagentOrdinal(parentID?: string | null, childID?: string | null): number {
  if (!parentID || !childID) return 0;
  const seen = new Set<string>();
  for (const turn of workspace.messagesBySession[parentID] ?? []) {
    for (const part of turn.parts ?? []) {
      if (part.type !== "tool" || (part.tool || "").toLowerCase() !== "task") continue;
      const id = extractChildSubagentSessionId(part as ToolPart);
      if (!id || seen.has(id)) continue;
      seen.add(id);
      if (id === childID) return seen.size;
    }
  }
  const siblings = workspace.sessions
    .filter((s) => s.parentID === parentID)
    .sort((a, b) => (a.time?.created || 0) - (b.time?.created || 0));
  return siblings.findIndex((s) => s.id === childID) + 1;
}

/** Display title for a subagent, falling back to "Subtask N" when it repeats its parent's title. */
export function resolveSubagentTitle(
  parentID: string | null | undefined,
  childID: string | null | undefined,
  rawTitle?: string | null,
): SubagentTitle {
  const parentTitle = parentID ? workspace.sessions.find((s) => s.id === parentID)?.title : undefined;
  const fallback = rawTitle || workspace.sessions.find((s) => s.id === childID)?.title;
  return subagentDisplayTitle(parentTitle, fallback, () => subagentOrdinal(parentID, childID));
}

function isChildSubagentTurnCompletedCleanly(childSessionID?: string): boolean {
  if (!childSessionID || workspace.interruptedBySession[childSessionID]) return false;
  const turns = workspace.messagesBySession[childSessionID];
  if (!turns || turns.length === 0) return false;
  const lastTurn = turns[turns.length - 1];
  if (lastTurn.info.role !== "assistant") return false;
  if (!lastTurn.info.time?.completed || Boolean(lastTurn.info.error)) return false;
  return !(lastTurn.parts ?? []).some(
    (p) => p.type === "tool" && (p.state.status === "running" || p.state.status === "pending")
  );
}

export function isSubagentToolAlive(
  tp: ToolPart,
  parentSessionID?: string | null
): boolean {
  const childSessionID = extractChildSubagentSessionId(tp);
  if (childSessionID && isSessionBusy(childSessionID)) return true;

  const rawStatus = tp.state.status;
  if (rawStatus !== "running" && rawStatus !== "pending") return false;

  if (childSessionID) {
    if (workspace.interruptedBySession[childSessionID]) return false;
    if (workspace.statusBySession[childSessionID]?.type === "idle") return false;
    if (isChildSubagentTurnCompletedCleanly(childSessionID)) return false;
  }

  const parentID = parentSessionID ?? tp.sessionID;
  return parentID ? isSessionBusy(parentID) : false;
}

export function isSubagentToolInterrupted(
  tp: ToolPart,
  parentSessionID?: string | null
): boolean {
  if (isSubagentToolAlive(tp, parentSessionID)) return false;
  const childSessionID = extractChildSubagentSessionId(tp);
  if (childSessionID && workspace.interruptedBySession[childSessionID]) return true;

  const rawStatus = tp.state.status;
  if (rawStatus === "running" || rawStatus === "pending") {
    if (childSessionID && isChildSubagentTurnCompletedCleanly(childSessionID)) {
      return false;
    }
    return true;
  }
  return rawStatus === "error" && Boolean(childSessionID);
}

export function childSubagentIDsFromTurns(
  turns: readonly MessageTurn[] | undefined,
): string[] {
  if (!turns || turns.length === 0) return [];
  const ids: string[] = [];
  const seen = new Set<string>();
  for (const turn of turns) {
    for (const part of turn.parts ?? []) {
      if (part.type === "tool") {
        const lower = (part.tool || "").toLowerCase();
        if (lower === "task" || lower === "agent" || lower.includes("subagent")) {
          const cid = extractChildSubagentSessionId(part);
          if (cid && !seen.has(cid)) {
            seen.add(cid);
            ids.push(cid);
          }
        }
      }
    }
  }
  return ids;
}

export function collectChildSubagentSessionIDs(parentSessionID: string): string[] {
  const childIDs = new Set<string>();
  for (const s of workspace.sessions) {
    if (s.parentID === parentSessionID) childIDs.add(s.id);
  }
  for (const cid of childSubagentIDsFromTurns(workspace.messagesBySession[parentSessionID])) {
    childIDs.add(cid);
  }
  return Array.from(childIDs);
}

export async function deleteChildSubagentSessionsBackend(
  childIDs: string[],
  targetDir?: string
): Promise<void> {
  await Promise.all(
    childIDs.map(async (cid) => {
      try {
        await hoxiAPI.abortSession(cid, targetDir);
      } catch {
        /* ignore */
      }
      try {
        await hoxiAPI.deleteSession(cid, targetDir);
      } catch {
        /* ignore */
      }
    })
  );
}

function isKnownParentSession(
  parentID: string | null | undefined,
  sessionID: string,
  byID?: ReadonlyMap<string, SessionInfo>,
): string | null {
  const clean = (parentID || "").trim();
  if (!clean || clean === sessionID) return null;
  if (byID) return byID.has(clean) ? clean : null;
  if (workspace.sessions.some((s) => s.id === clean)) return clean;
  if (indexedSession(clean)) return clean;
  if (workspace.messagesBySession[clean]) return clean;
  return null;
}

/**
 * Resolves the immediate parent session ID of `sessionID` (if it is a subagent session),
 * checking optional `byID`, `workspace.sessions`, `sessionIndex`, and `task` tool calls.
 * An orphaned reference whose parent session no longer exists returns `null`.
 */
export function resolveParentSessionID(
  sessionID: string | null | undefined,
  byID?: ReadonlyMap<string, SessionInfo>,
): string | null {
  if (!sessionID) return null;
  if (byID?.has(sessionID)) {
    const localParent = isKnownParentSession(byID.get(sessionID)?.parentID, sessionID, byID);
    if (localParent) return localParent;
    const indexedParent = isKnownParentSession(indexedSession(sessionID)?.parentID, sessionID, byID);
    if (indexedParent) return indexedParent;
    return isKnownParentSession(lookupSubagentParent(sessionID), sessionID, byID);
  }
  const info = workspace.sessions.find((s) => s.id === sessionID);
  const directParent = isKnownParentSession(info?.parentID, sessionID, byID);
  if (directParent) return directParent;
  const indexedParent = isKnownParentSession(indexedSession(sessionID)?.parentID, sessionID, byID);
  if (indexedParent) return indexedParent;
  return isKnownParentSession(lookupSubagentParent(sessionID), sessionID, byID);
}

/**
 * Walks up parent links to return the root/primary session ID.
 */
export function resolveRootSessionID(
  sessionID: string | null | undefined,
  byID?: ReadonlyMap<string, SessionInfo>,
): string | null {
  if (!sessionID) return null;
  let curr: string = sessionID;
  const visited = new Set<string>([curr]);
  for (let depth = 0; depth < 8; depth++) {
    const parent = resolveParentSessionID(curr, byID);
    if (!parent || visited.has(parent)) break;
    visited.add(parent);
    curr = parent;
  }
  return curr;
}

/**
 * Groups non-archived child sessions (subagents and convoy subtasks) under
 * their root parent session ID in chronological spawn order.
 */
export function groupSubagentsByRoot(
  rawSessions: readonly SessionInfo[],
): Map<string, SessionInfo[]> {
  const combinedByID = new Map<string, SessionInfo>();
  for (const s of workspace.sessions) {
    if (s?.id) combinedByID.set(s.id, s);
  }
  for (const s of rawSessions) {
    if (s?.id) combinedByID.set(s.id, s);
  }

  const grouped = new Map<string, Map<string, SessionInfo>>();
  const addChild = (rootID: string, child: SessionInfo) => {
    if (!rootID || !child?.id || child.id === rootID || child.time?.archived) return;
    let bucket = grouped.get(rootID);
    if (!bucket) {
      bucket = new Map<string, SessionInfo>();
      grouped.set(rootID, bucket);
    }
    if (!bucket.has(child.id)) {
      bucket.set(child.id, child);
    }
  };

  for (const s of rawSessions) {
    if (!s?.id || s.time?.archived) continue;
    const rootID = resolveRootSessionID(s.id, combinedByID);
    if (rootID && rootID !== s.id) {
      addChild(rootID, s);
    }
  }

  for (const s of rawSessions) {
    if (!s?.id || s.time?.archived || resolveParentSessionID(s.id, combinedByID)) continue;
    for (const childID of childSubagentIDsFromTurns(workspace.messagesBySession[s.id])) {
      const child = combinedByID.get(childID);
      if (child && !child.time?.archived) {
        addChild(s.id, child);
      }
    }
  }

  const result = new Map<string, SessionInfo[]>();
  for (const [rootID, bucket] of grouped.entries()) {
    const list = Array.from(bucket.values()).sort((a, b) => {
      const ta = a.time?.created || a.time?.updated || 0;
      const tb = b.time?.created || b.time?.updated || 0;
      return ta !== tb ? ta - tb : a.id.localeCompare(b.id);
    });
    if (list.length > 0) {
      result.set(rootID, list);
    }
  }
  return result;
}

export interface CodeSessionPartition {
  codeSessions: SessionInfo[];
  roots: SessionInfo[];
  subagentsByRoot: Map<string, SessionInfo[]>;
}

/**
 * Filters a project's session list down to the code surface and partitions it
 * into top-level root sessions and subagent groups keyed by root session ID.
 */
export function partitionCodeSessions(
  rawSessions: readonly SessionInfo[],
): CodeSessionPartition {
  const codeSessions = rawSessions.filter((s) => resolveSessionSurface(s) === "code");
  const byID = new Map<string, SessionInfo>();
  for (const s of codeSessions) {
    if (s?.id) byID.set(s.id, s);
  }
  const roots = codeSessions.filter((s) => !resolveParentSessionID(s.id, byID));
  const subagentsByRoot = groupSubagentsByRoot(codeSessions);
  return { codeSessions, roots, subagentsByRoot };
}

function matchesSubagentQuery(session: SessionInfo, queryLower: string): boolean {
  return (
    !queryLower ||
    (session.title || "").toLowerCase().includes(queryLower) ||
    (session.slug || "").toLowerCase().includes(queryLower)
  );
}

/**
 * Returns true when the subagent drawer under a parent session should
 * automatically expand (running child, pending approval, active selection, or search match).
 */
export function shouldAutoExpandSubagents(
  subagents: readonly SessionInfo[] | undefined,
  pendingSessions: ReadonlySet<string>,
  filterQuery?: string,
): boolean {
  if (!subagents || subagents.length === 0) return false;
  const activeID = workspace.activeSessionID;
  const q = (filterQuery ?? "").trim().toLowerCase();
  for (const sub of subagents) {
    if (sub.id === activeID) return true;
    if (isSessionBusy(sub.id)) return true;
    if (pendingSessions.has(sub.id)) return true;
    if (q && matchesSubagentQuery(sub, q)) return true;
  }
  return false;
}

export function countRunningSubagents(
  subagents: readonly SessionInfo[] | undefined,
): number {
  if (!subagents || subagents.length === 0) return 0;
  let count = 0;
  for (const sub of subagents) {
    if (isSessionBusy(sub.id)) count++;
  }
  return count;
}

interface SubagentDrawerOverride {
  manual: boolean;
  autoSnapshot: boolean;
}

const MAX_SUBAGENT_DRAWER_OVERRIDES = 512;

const [subagentDrawerOverrides, setSubagentDrawerOverrides] = createSignal<
  Record<string, SubagentDrawerOverride>
>({});

export function isSubagentDrawerExpanded(
  sessionID: string,
  autoExpand: boolean,
): boolean {
  const entry = subagentDrawerOverrides()[sessionID];
  if (!entry) return true;
  if (!entry.manual) {
    if (!entry.autoSnapshot && autoExpand) {
      entry.manual = true;
      entry.autoSnapshot = true;
      return true;
    }
    if (entry.autoSnapshot && !autoExpand) {
      entry.autoSnapshot = false;
    }
    return false;
  }
  entry.autoSnapshot = autoExpand;
  return true;
}

export function setSubagentDrawerExpanded(
  sessionID: string,
  expanded: boolean,
  autoExpand: boolean,
): void {
  if (!sessionID) return;
  setSubagentDrawerOverrides((prev) => {
    const next: Record<string, SubagentDrawerOverride> = { ...prev };
    delete next[sessionID];
    const keys = Object.keys(next);
    if (keys.length >= MAX_SUBAGENT_DRAWER_OVERRIDES) {
      delete next[keys[0]!];
    }
    next[sessionID] = { manual: expanded, autoSnapshot: autoExpand };
    return next;
  });
}

export function clearSubagentDrawerOverride(sessionID: string): void {
  if (!sessionID) return;
  setSubagentDrawerOverrides((prev) => {
    if (!(sessionID in prev)) return prev;
    const next = { ...prev };
    delete next[sessionID];
    return next;
  });
}
