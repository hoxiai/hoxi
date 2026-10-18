import type { MessageInfo, MessagePart } from "../services/sse-types";
import type { MessageTurn } from "./workspace";

const indexes = new WeakMap<object, { length: number; byID: Map<string, number> }>();

// Validate cached positions: reloads, optimistic removal and revert can replace/reorder arrays.
function findIndex<T>(items: T[], id: string, getID: (item: T) => string): number {
  let cache = indexes.get(items);
  const index = cache?.byID.get(id);
  if (cache?.length === items.length && index !== undefined && getID(items[index]) === id) {
    return index;
  }
  cache = { length: items.length, byID: new Map() };
  for (let i = 0; i < items.length; i++) cache.byID.set(getID(items[i]), i);
  indexes.set(items, cache);
  return cache.byID.get(id) ?? -1;
}

export function findMessageIndex(turns: MessageTurn[], id: string): number {
  return findIndex(turns, id, (turn) => turn.info.id);
}

export function findPartIndex(parts: MessagePart[], id: string): number {
  return findIndex(parts, id, (part) => part.id);
}

function isOptimisticID(id: string): boolean {
  return id.startsWith("optimistic_");
}

// Optimistic parts are `prt_<ms>` / `prt_file_<ms>_<i>`; real OpenCode part IDs also start with `prt_`.
function isOptimisticPartID(id: string): boolean {
  return /^prt_(file_)?\d+(_\d+)?$/.test(id);
}

function bubbleTurnIntoOrder(list: MessageTurn[], idx: number) {
  const current = list[idx];
  if (!current || isOptimisticID(current.info.id)) return;
  let pos = idx;
  while (pos > 0) {
    const prev = list[pos - 1];
    if (isOptimisticID(prev.info.id)) {
      if (current.info.role === "user") {
        list[pos] = prev;
        list[pos - 1] = current;
        pos--;
        continue;
      }
      break;
    }
    if (prev.info.id > current.info.id) {
      list[pos] = prev;
      list[pos - 1] = current;
      pos--;
      continue;
    }
    break;
  }
}

export function upsertTurnInfo(list: MessageTurn[], info: MessageInfo) {
  let idx = findMessageIndex(list, info.id);

  if (info.role === "user" && !isOptimisticID(info.id)) {
    const optIdx = list.findIndex((m) => isOptimisticID(m.info.id));
    if (optIdx >= 0 && idx < 0) {
      const fallbackParts = list[optIdx].parts.map((p) => ({
        ...p,
        messageID: info.id,
      }));
      list[optIdx] = { info, parts: fallbackParts };
      bubbleTurnIntoOrder(list, optIdx);
      return;
    }
    if (optIdx >= 0 && idx >= 0 && optIdx !== idx) {
      const realParts = list[idx].parts;
      const fallbackParts = list[optIdx].parts.map((p) => ({
        ...p,
        messageID: info.id,
      }));
      const mergedParts = realParts.length > 0 ? realParts : fallbackParts;
      const targetIdx = Math.min(optIdx, idx);
      const removeIdx = Math.max(optIdx, idx);
      list[targetIdx] = { info, parts: mergedParts };
      list.splice(removeIdx, 1);
      bubbleTurnIntoOrder(list, targetIdx);
      return;
    }
  }

  if (idx >= 0) {
    Object.assign(list[idx].info, info);
    bubbleTurnIntoOrder(list, idx);
  } else {
    list.push({ info, parts: [] });
    bubbleTurnIntoOrder(list, list.length - 1);
  }
}

export function upsertTurnPart(list: MessageTurn[], part: MessagePart) {
  let turnIdx = findMessageIndex(list, part.messageID);

  if (turnIdx < 0 && part.type === "text" && part.text) {
    const optIdx = list.findIndex(
      (m) =>
        isOptimisticID(m.info.id) &&
        m.parts.some((p) => p.type === "text" && p.text === part.text)
    );
    if (optIdx >= 0) {
      list[optIdx].info.id = part.messageID;
      turnIdx = optIdx;
      bubbleTurnIntoOrder(list, turnIdx);
    }
  }

  let turn = turnIdx >= 0 ? list[turnIdx] : undefined;
  if (!turn) {
    turn = {
      info: {
        id: part.messageID,
        sessionID: part.sessionID,
        role: "assistant",
        time: { created: Date.now() },
      },
      parts: [],
    };
    list.push(turn);
    bubbleTurnIntoOrder(list, list.length - 1);
  }

  if (!isOptimisticPartID(part.id)) {
    for (let i = turn.parts.length - 1; i >= 0; i--) {
      if (isOptimisticPartID(turn.parts[i].id) && turn.parts[i].type === part.type) {
        turn.parts.splice(i, 1);
      }
    }
  }

  const pIdx = findPartIndex(turn.parts, part.id);
  if (pIdx >= 0) {
    Object.assign(turn.parts[pIdx], part);
  } else {
    turn.parts.push(part);
  }
}

const toolStatusRank: Record<string, number> = {
  pending: 0,
  running: 1,
  completed: 2,
  error: 2,
};

function isTurnSettled(info: MessageInfo): boolean {
  return Boolean(info.time?.completed || info.error);
}

// A REST snapshot is taken before its response lands, so SSE may already have advanced the
// same part further. Parts only move forward (status rank, text growth, reasoning end).
function isLocalPartAhead(local: MessagePart, remote: MessagePart): boolean {
  if (local.type === "tool" && remote.type === "tool") {
    return (toolStatusRank[local.state.status] ?? 0) > (toolStatusRank[remote.state.status] ?? 0);
  }
  if (local.type === "reasoning" && remote.type === "reasoning") {
    const localEnded = Boolean(local.time?.end);
    const remoteEnded = Boolean(remote.time?.end);
    if (localEnded !== remoteEnded) return localEnded;
    return local.text.length > remote.text.length;
  }
  if (local.type === "text" && remote.type === "text") {
    return local.text.length > remote.text.length;
  }
  return false;
}

function mergeTurnInto(local: MessageTurn, remote: { info: MessageInfo; parts: MessagePart[] }) {
  if (!(isTurnSettled(local.info) && !isTurnSettled(remote.info))) {
    Object.assign(local.info, remote.info);
  }
  const remoteIDs = new Set(remote.parts.map((p) => p.id));
  const nextParts: MessagePart[] = [];
  for (const rp of remote.parts) {
    const idx = findPartIndex(local.parts, rp.id);
    const lp = idx >= 0 ? local.parts[idx] : undefined;
    if (!lp || lp.type !== rp.type) {
      nextParts.push(rp);
      continue;
    }
    if (!isLocalPartAhead(lp, rp)) Object.assign(lp, rp);
    nextParts.push(lp);
  }
  // Parts streamed in after the snapshot was taken; optimistic placeholders yield to real parts.
  for (const lp of local.parts) {
    if (remoteIDs.has(lp.id)) continue;
    if (isOptimisticPartID(lp.id) && remote.parts.length > 0) continue;
    nextParts.push(lp);
  }
  if (
    local.parts.length !== nextParts.length ||
    local.parts.some((p, i) => p !== nextParts[i])
  ) {
    local.parts.splice(0, local.parts.length, ...nextParts);
  }
}

/**
 * Merges a REST message snapshot into the live store list in place (call inside `produce`).
 * Existing turn/part objects are updated rather than replaced so streamed progress that is
 * newer than the snapshot survives and rendered cards keep their identity.
 */
export function mergeServerTurnsInto(
  list: MessageTurn[],
  remoteTurns: Array<{ info: MessageInfo; parts: MessagePart[] }>
) {
  const existing = list.slice();
  const priorRealUserIDs = new Set(
    existing
      .filter((t) => t.info.role === "user" && !isOptimisticID(t.info.id))
      .map((t) => t.info.id)
  );
  const hasNewRemoteUserTurn = remoteTurns.some(
    (t) => t.info.role === "user" && !priorRealUserIDs.has(t.info.id)
  );
  const lastRemoteID =
    remoteTurns.length > 0 ? remoteTurns[remoteTurns.length - 1].info.id : "";

  const remoteIDs = new Set<string>();
  const next: MessageTurn[] = [];
  for (const remote of remoteTurns) {
    remoteIDs.add(remote.info.id);
    const idx = findMessageIndex(existing, remote.info.id);
    if (idx < 0) {
      next.push({ info: remote.info, parts: remote.parts });
      continue;
    }
    mergeTurnInto(existing[idx], remote);
    next.push(existing[idx]);
  }

  for (const turn of existing) {
    if (remoteIDs.has(turn.info.id)) continue;
    if (isOptimisticID(turn.info.id)) {
      if (!hasNewRemoteUserTurn) next.push(turn);
      continue;
    }
    if (lastRemoteID && turn.info.id > lastRemoteID) next.push(turn);
  }

  if (
    list.length !== next.length ||
    list.some((t, i) => t !== next[i])
  ) {
    list.splice(0, list.length, ...next);
  }
}
