// web/src/store/subagent-sessions.ts
// Subagent (task tool) session tree: child-ID resolution, titles, liveness and
// parent/root lookups. Read-mostly; loading children lives in workspace-sessions.
import { hoxiAPI } from "../services/api";
import type { ToolPart } from "../services/sse-types";
import { lookupSubagentParent, subagentChildIDFromPart } from "./message-lookup";
import { isSessionBusy } from "./session-query";
import { subagentDisplayTitle, type SubagentTitle } from "./session-title";
import { workspace } from "./workspace-state";

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

export function collectChildSubagentSessionIDs(parentSessionID: string): string[] {
  const childIDs = new Set<string>();
  for (const s of workspace.sessions) {
    if (s.parentID === parentSessionID) childIDs.add(s.id);
  }
  const turns = workspace.messagesBySession[parentSessionID] ?? [];
  for (const turn of turns) {
    for (const part of turn.parts) {
      if (part.type === "tool") {
        const lower = (part.tool || "").toLowerCase();
        if (lower === "task" || lower === "agent" || lower.includes("subagent")) {
          const cid = extractChildSubagentSessionId(part);
          if (cid) childIDs.add(cid);
        }
      }
    }
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

/**
 * Resolves the immediate parent session ID of `sessionID` (if it is a subagent session),
 * checking both `workspace.sessions` (`parentID`) and `task` tool calls in `messagesBySession`.
 */
export function resolveParentSessionID(sessionID: string | null | undefined): string | null {
  if (!sessionID) return null;
  const info = workspace.sessions.find((s) => s.id === sessionID);
  if (info?.parentID) return info.parentID;
  return lookupSubagentParent(sessionID);
}

/**
 * Walks up parent links to return the root/primary session ID.
 */
export function resolveRootSessionID(sessionID: string | null | undefined): string | null {
  if (!sessionID) return null;
  let curr: string = sessionID;
  const visited = new Set<string>([curr]);
  for (let depth = 0; depth < 8; depth++) {
    const parent = resolveParentSessionID(curr);
    if (!parent || visited.has(parent)) break;
    visited.add(parent);
    curr = parent;
  }
  return curr;
}
