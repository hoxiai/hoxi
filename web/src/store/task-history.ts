// Current-task context for composer summaries, independent of transcript paging.
import { createStore, produce } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { compareTurnOrder, subagentChildIDFromPart } from "./message-lookup";
import { hasOlderSessionHistory, SESSION_HISTORY_PAGE_TURNS } from "./session-history";
import { isSessionDeleted, sessionDirectory } from "./session-query";
import { findTaskCycleStart } from "./task-progress";
import { workspace, type MessageTurn } from "./workspace-state";

interface TaskHistory {
  scope: string;
  turns: MessageTurn[];
}

const [history, setHistory] = createStore<Record<string, TaskHistory | undefined>>({});
const reads = new Map<string, { scope: string; promise: Promise<void> }>();

/** Changes on a new prompt, history replacement, revert or directory switch. */
export function currentTaskHistoryScope(sessionID: string): string {
  const turns = workspace.messagesBySession[sessionID] ?? [];
  const start = findTaskCycleStart(turns);
  const revert = workspace.sessions.find((s) => s.id === sessionID)?.revert;
  return JSON.stringify([
    sessionDirectory(sessionID), turns[0]?.info.id, start >= 0 ? turns[start].info.id : "",
    revert?.messageID, revert?.partID,
  ]);
}

export function forgetSessionTaskHistory(sessionID: string): void {
  reads.delete(sessionID);
  if (history[sessionID]) setHistory(produce((state) => { delete state[sessionID]; }));
}

/** Resident SSE data wins over older snapshots, so tools are never counted twice. */
export function collectCurrentTaskTurns(sessionID: string | null): MessageTurn[] {
  if (!sessionID) return [];
  const resident = workspace.messagesBySession[sessionID] ?? [];
  const saved = history[sessionID];
  let turns = resident;
  if (saved?.scope === currentTaskHistoryScope(sessionID)) {
    const loaded = new Set(resident.map((turn) => turn.info.id));
    turns = [...saved.turns.filter((turn) => !loaded.has(turn.info.id)), ...resident];
  }
  return turns.slice(Math.max(0, findTaskCycleStart(turns)));
}

// Historical context needs tool inputs/diffs and child links, not chat text or
// megabytes of command output. It never enters messagesBySession or localStorage.
function compactTaskTurns(turns: MessageTurn[]): MessageTurn[] {
  return turns.map((turn) => ({
    info: turn.info,
    parts: turn.parts.filter((part) => part.type === "tool" || part.type === "patch").map((part) => {
      if (part.type !== "tool") return part;
      const childID = subagentChildIDFromPart(part);
      return {
        ...part,
        state: {
          ...part.state,
          ...("output" in part.state ? { output: "" } : {}),
          metadata: {
            ...part.state.metadata, output: undefined, stdout: undefined,
            ...(childID ? { sessionID: childID } : {}),
          },
        },
      };
    }),
  })).filter((turn) => turn.info.role === "user" || turn.parts.length > 0);
}

/** Read only the missing prefix of this task; keep the chat's lazy paging intact. */
export function loadCurrentTaskHistory(sessionID: string): Promise<void> {
  const resident = workspace.messagesBySession[sessionID] ?? [];
  if (!resident.length || isSessionDeleted(sessionID) ||
      !hasOlderSessionHistory(sessionID) || findTaskCycleStart(resident) >= 0) {
    return Promise.resolve();
  }
  const scope = currentTaskHistoryScope(sessionID);
  const pending = reads.get(sessionID);
  if (pending?.scope === scope) return pending.promise;
  if (history[sessionID]?.scope === scope) return Promise.resolve();
  const directory = sessionDirectory(sessionID);
  const read = { scope, promise: Promise.resolve() };
  const isCurrent = () => reads.get(sessionID) === read &&
    !isSessionDeleted(sessionID) && currentTaskHistoryScope(sessionID) === scope;
  read.promise = Promise.resolve().then(async () => {
    let cursor = resident[0].info;
    const seen = new Set(resident.map((turn) => turn.info.id));
    let prefix: MessageTurn[] = [];
    while (isCurrent()) {
      const page = await hoxiAPI.getMessages(sessionID, directory, SESSION_HISTORY_PAGE_TURNS, cursor.id);
      if (!isCurrent()) return;
      // A backend ignoring `before` must not loop or mix a newer task into this one.
      const fresh = page.filter((turn) => !seen.has(turn.info.id) && compareTurnOrder(turn.info, cursor) < 0);
      if (!fresh.length) break;
      for (const turn of fresh) seen.add(turn.info.id);
      const start = findTaskCycleStart(fresh);
      prefix = [...compactTaskTurns(fresh.slice(Math.max(0, start))), ...prefix];
      if (start >= 0 || page.length < SESSION_HISTORY_PAGE_TURNS) break;
      cursor = fresh[0].info;
    }
    if (isCurrent()) setHistory(sessionID, { scope, turns: prefix });
  }).finally(() => {
    if (reads.get(sessionID) === read) reads.delete(sessionID);
  });
  reads.set(sessionID, read);
  return read.promise;
}
