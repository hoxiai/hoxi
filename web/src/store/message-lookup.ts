import type { MessagePart } from "../services/sse-types";
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
