import {
  workspace,
  isSessionBusy,
  resolveParentSessionID,
  collectChildSubagentSessionIDs,
} from "../../store/workspace";
import type { SessionInfo } from "../../services/sse-types";
import { matchesSessionQueryText } from "./session-search";

function resolveDirectParentID(
  session: SessionInfo,
  byID: ReadonlyMap<string, SessionInfo>,
  globalByID: ReadonlyMap<string, SessionInfo>,
): string | null {
  const explicit = (session.parentID || "").trim();
  if (explicit && (byID.has(explicit) || globalByID.has(explicit))) {
    return explicit;
  }
  const fromTurns = resolveParentSessionID(session.id);
  if (fromTurns && (byID.has(fromTurns) || globalByID.has(fromTurns))) {
    return fromTurns;
  }
  return explicit || fromTurns || null;
}

/**
 * Groups non-archived child sessions (subagents and convoy subtasks) under
 * their root parent session ID in chronological spawn order.
 */
export function groupSubagentsByRoot(
  rawSessions: readonly SessionInfo[],
): Map<string, SessionInfo[]> {
  const byID = new Map<string, SessionInfo>();
  for (const s of rawSessions) {
    if (s?.id) byID.set(s.id, s);
  }
  const globalByID = new Map<string, SessionInfo>();
  for (const s of workspace.sessions) {
    if (s?.id) globalByID.set(s.id, s);
  }

  const lookup = (id: string): SessionInfo | undefined =>
    byID.get(id) ?? globalByID.get(id);

  const resolveRootID = (session: SessionInfo): string | null => {
    const firstParent = resolveDirectParentID(session, byID, globalByID);
    if (!firstParent || firstParent === session.id) return null;
    let currentID = firstParent;
    const visited = new Set<string>([session.id, currentID]);
    for (let depth = 0; depth < 8; depth++) {
      const current = lookup(currentID);
      if (!current) break;
      const nextParent = resolveDirectParentID(current, byID, globalByID);
      if (!nextParent || visited.has(nextParent)) break;
      visited.add(nextParent);
      currentID = nextParent;
    }
    return currentID;
  };

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
    const rootID = resolveRootID(s);
    if (rootID) {
      addChild(rootID, s);
    }
  }

  // Also include child subagent sessions discovered via hydrated tool parts on root sessions.
  for (const s of rawSessions) {
    if (!s?.id || s.time?.archived || s.parentID) continue;
    for (const childID of collectChildSubagentSessionIDs(s.id)) {
      const child = lookup(childID);
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
  const q = (filterQuery ?? "").trim();
  for (const sub of subagents) {
    if (sub.id === activeID) return true;
    if (isSessionBusy(sub.id)) return true;
    if (pendingSessions.has(sub.id)) return true;
    if (q && matchesSessionQueryText(sub, q)) return true;
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
