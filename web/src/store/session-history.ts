// web/src/store/session-history.ts
// Paged transcript history. Long conversations only load their newest turns;
// older turns are fetched on demand (scrolling to the top, "load earlier",
// transcript search, export and commit scoping). Without paging a long
// session shipped and parsed its whole transcript (several MB) on every open
// and refresh, and every full-list memo re-scanned thousands of turns.
import { createStore, produce } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { MessageInfo, MessagePart } from "../services/sse-types";
import { mergeServerTurnsInto, normalizeMessagePartInPlace } from "./message-lookup";
import { sessionDirectory } from "./session-query";
import { setWorkspace, workspace } from "./workspace-state";

/** Newest turns fetched when a conversation opens or refreshes. */
export const SESSION_HISTORY_PAGE_TURNS = 200;

type RemoteTurn = { info: MessageInfo; parts: MessagePart[] };

// true = the server still has turns older than the loaded ones.
const [olderHistory, setOlderHistory] = createStore<Record<string, boolean>>({});
const olderReads = new Map<string, Promise<number>>();
const fullReads = new Map<string, Promise<void>>();

export function hasOlderSessionHistory(sessionID: string | null | undefined): boolean {
  return Boolean(sessionID && olderHistory[sessionID]);
}

export function forgetSessionHistory(sessionID: string): void {
  if (sessionID in olderHistory) setOlderHistory(produce((state) => { delete state[sessionID]; }));
}

/**
 * Records what a newest-turns snapshot proved about older history. A full
 * page means older turns may remain on the server, unless older pages were
 * already loaded and kept, in which case the previous answer still holds.
 */
export function noteSessionHistoryTail(
  sessionID: string,
  received: number,
  limit: number | "all",
  keptOlder: number
): void {
  if (limit === "all" || received < limit) {
    forgetSessionHistory(sessionID);
    return;
  }
  if (keptOlder > 0 && sessionID in olderHistory) return;
  setOlderHistory(sessionID, true);
}

function prependOlderTurns(sessionID: string, older: RemoteTurn[]): number {
  let added = 0;
  setWorkspace(produce((state) => {
    const list = state.messagesBySession[sessionID];
    if (!list) return;
    const known = new Set(list.map((turn) => turn.info.id));
    const fresh = older.filter((turn) => !known.has(turn.info.id));
    for (const turn of fresh) for (const part of turn.parts) normalizeMessagePartInPlace(part);
    added = fresh.length;
    if (added > 0) list.unshift(...fresh);
  }));
  return added;
}

/**
 * Prepends the page of turns just before the oldest loaded one and returns how
 * many were added. Backends that ignore the `before` cursor resend turns we
 * already have, which ends paging instead of looping.
 */
export function loadOlderSessionHistory(
  sessionID: string,
  limit = SESSION_HISTORY_PAGE_TURNS,
  /** Wraps the synchronous prepend, e.g. to keep the reader's scroll position. */
  around: (apply: () => number) => number = (apply) => apply()
): Promise<number> {
  if (!hasOlderSessionHistory(sessionID)) return Promise.resolve(0);
  const pending = olderReads.get(sessionID);
  if (pending) return pending;
  const read = (async () => {
    const oldest = workspace.messagesBySession[sessionID]?.[0]?.info.id;
    if (!oldest) {
      forgetSessionHistory(sessionID);
      return 0;
    }
    const older = await hoxiAPI.getMessages(sessionID, sessionDirectory(sessionID), limit, oldest);
    if (workspace.messagesBySession[sessionID]?.[0]?.info.id !== oldest) return 0;
    const added = around(() => prependOlderTurns(sessionID, older));
    if (added === 0 || older.length < limit) forgetSessionHistory(sessionID);
    return added;
  })().finally(() => olderReads.delete(sessionID));
  olderReads.set(sessionID, read);
  return read;
}

/** Loads the whole transcript once for features that must see every turn. */
export function ensureFullSessionHistory(sessionID: string): Promise<void> {
  if (!hasOlderSessionHistory(sessionID)) return Promise.resolve();
  const pending = fullReads.get(sessionID);
  if (pending) return pending;
  const read = (async () => {
    const turns = await hoxiAPI.getMessages(sessionID, sessionDirectory(sessionID), "all");
    if (workspace.messagesBySession[sessionID] === undefined) return;
    setWorkspace(produce((state) => {
      const list = state.messagesBySession[sessionID];
      if (list) mergeServerTurnsInto(list, turns);
    }));
    forgetSessionHistory(sessionID);
  })().finally(() => fullReads.delete(sessionID));
  fullReads.set(sessionID, read);
  return read;
}
