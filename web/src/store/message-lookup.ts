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

function isOptimisticPartID(id: string): boolean {
  return id.startsWith("prt_");
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

export function mergeServerTurns(
  existing: MessageTurn[] | undefined,
  remoteTurns: Array<{ info: MessageInfo; parts: MessagePart[] }>
): MessageTurn[] {
  const normalized = remoteTurns.map((t) => ({ info: t.info, parts: t.parts }));
  if (!existing || existing.length === 0) return normalized;

  const remoteIDs = new Set(normalized.map((t) => t.info.id));
  const priorRealUserIDs = new Set(
    existing
      .filter((t) => t.info.role === "user" && !isOptimisticID(t.info.id))
      .map((t) => t.info.id)
  );
  const hasNewRemoteUserTurn = normalized.some(
    (t) => t.info.role === "user" && !priorRealUserIDs.has(t.info.id)
  );

  const lastRemoteID =
    normalized.length > 0 ? normalized[normalized.length - 1].info.id : "";

  for (const turn of existing) {
    if (remoteIDs.has(turn.info.id)) continue;
    if (isOptimisticID(turn.info.id)) {
      if (!hasNewRemoteUserTurn) {
        normalized.push(turn);
      }
      continue;
    }
    if (lastRemoteID && turn.info.id > lastRemoteID) {
      normalized.push(turn);
    }
  }

  return normalized;
}

