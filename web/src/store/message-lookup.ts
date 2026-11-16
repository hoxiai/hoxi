import { createSignal } from "solid-js";
import { $RAW } from "solid-js/store";
import type { MessageInfo, MessagePart } from "../services/sse-types";
import type { MessageTurn } from "./workspace-state";

interface IndexCache {
  length: number;
  ids: string[];
  refs: unknown[];
  hasGetter: boolean;
  hasOptimistic: boolean;
  byID: Map<string, number>;
}

const indexes = new WeakMap<object, IndexCache>();

function rawOf<T>(value: T): T {
  if (value && typeof value === "object") {
    return (value as { [$RAW]?: T })[$RAW] ?? value;
  }
  return value;
}

function hasIdGetter(ref: unknown): boolean {
  if (!ref || typeof ref !== "object") return false;
  return Boolean(Object.getOwnPropertyDescriptor(ref, "id")?.get);
}

function invalidateIndex(items: object): void {
  indexes.delete(items);
  indexes.delete(rawOf(items));
}

function getIndexCache(items: object): IndexCache | undefined {
  return indexes.get(rawOf(items)) ?? indexes.get(items);
}

function isCachePrefixValid<T>(
  rawItems: T[],
  cache: IndexCache,
  count: number,
  getID: (item: T) => string,
  getRef: (item: T) => unknown
): boolean {
  if (count <= 0 || rawItems.length < count) return false;
  if (cache.hasGetter) {
    for (let i = 0; i < count; i++) {
      if (getRef(rawItems[i]) !== cache.refs[i]) return false;
    }
    return (
      getID(rawItems[0]) === cache.ids[0] &&
      (count === 1 || getID(rawItems[count - 1]) === cache.ids[count - 1])
    );
  }
  for (let i = 0; i < count; i++) {
    const item = rawItems[i];
    if (getRef(item) !== cache.refs[i] || getID(item) !== cache.ids[i]) {
      return false;
    }
  }
  return true;
}

// Validate cached positions: reloads, optimistic promotion/removal and revert can replace/reorder arrays or mutate middle IDs in place.
function findIndex<T>(
  items: T[],
  id: string,
  getID: (item: T) => string,
  getRef: (item: T) => unknown,
  isOpt?: (id: string) => boolean
): number {
  const rawItems = rawOf(items);
  const len = rawItems.length;
  if (len === 0) {
    invalidateIndex(items);
    return -1;
  }
  let cache = getIndexCache(items);
  if (cache) {
    if (
      len > cache.length &&
      isCachePrefixValid(rawItems, cache, cache.length, getID, getRef)
    ) {
      for (let i = cache.length; i < len; i++) {
        const item = rawItems[i];
        const itemID = getID(item);
        const ref = getRef(item);
        cache.ids.push(itemID);
        cache.refs.push(ref);
        if (!cache.hasGetter && hasIdGetter(ref)) cache.hasGetter = true;
        cache.byID.set(itemID, i);
        if (isOpt?.(itemID)) cache.hasOptimistic = true;
      }
      cache.length = len;
    }
    if (cache.length === len) {
      const index = cache.byID.get(id);
      if (index !== undefined) {
        if (index < len && getID(rawItems[index]) === id) return index;
      } else if (isCachePrefixValid(rawItems, cache, len, getID, getRef)) {
        return -1;
      }
    }
  }
  const byID = new Map<string, number>();
  const ids = new Array<string>(len);
  const refs = new Array<unknown>(len);
  let hasGetter = false;
  let hasOptimistic = false;
  for (let i = 0; i < len; i++) {
    const item = rawItems[i];
    const itemID = getID(item);
    const ref = getRef(item);
    ids[i] = itemID;
    refs[i] = ref;
    if (!hasGetter && hasIdGetter(ref)) hasGetter = true;
    byID.set(itemID, i);
    if (isOpt?.(itemID)) hasOptimistic = true;
  }
  cache = {
    length: len,
    ids,
    refs,
    hasGetter,
    hasOptimistic,
    byID,
  };
  indexes.set(rawItems, cache);
  return byID.get(id) ?? -1;
}

export function findMessageIndex(turns: MessageTurn[], id: string): number {
  return findIndex(turns, id, (turn) => turn.info.id, (turn) => rawOf(turn.info), isOptimisticID);
}

export function findPartIndex(parts: MessagePart[], id: string): number {
  return findIndex(parts, id, (part) => part.id, (part) => rawOf(part), isOptimisticPartID);
}

function isOptimisticID(id: string): boolean {
  return id.startsWith("optimistic_");
}

// Optimistic parts are `prt_<ms>` / `prt_file_<ms>_<i>`; real OpenCode part IDs also start with `prt_`.
function isOptimisticPartID(id: string): boolean {
  return /^prt_(file_)?\d+(_\d+)?$/.test(id);
}

// Antigravity flushes step thinking on DONE after ACTIVE has already streamed
// `text_delta`; map `prt_<stem>_reason_<step>` to its same-step `text` part ID.
function pairedStepTextPartID(part: MessagePart): string | undefined {
  if (part.type !== "reasoning") return undefined;
  const match = /^(.*)_reason_(\d+)$/.exec(part.id);
  return match ? `${match[1]}_text_${match[2]}` : undefined;
}

export function orderSameStepReasoningPartsInPlace(parts: MessagePart[]): void {
  for (let i = 1; i < parts.length; i++) {
    const pairedTextID = pairedStepTextPartID(parts[i]);
    if (!pairedTextID) continue;
    const textIdx = findPartIndex(parts, pairedTextID);
    if (textIdx >= 0 && textIdx < i) {
      const [reasonPart] = parts.splice(i, 1);
      parts.splice(textIdx, 0, reasonPart);
      invalidateIndex(parts);
    }
  }
}

function parseRunnerMessageID(id: string): { time: number; seq: number; roleRank: number } | null {
  const match = /^msg_([ua])_(\d+)_(\d+)$/.exec(id);
  if (!match) return null;
  return {
    time: Number(match[2]),
    seq: Number(match[3]),
    roleRank: match[1] === "u" ? 0 : 1,
  };
}

export function compareMessageIDOrder(info: MessageInfo, boundaryID: string): number {
  if (info.id === boundaryID) return 0;
  if (isOptimisticID(info.id)) return 1;
  const a = parseRunnerMessageID(info.id);
  const b = parseRunnerMessageID(boundaryID);
  if (a && b) {
    if (a.time !== b.time) return a.time - b.time;
    if (a.seq !== b.seq) return a.seq - b.seq;
    return a.roleRank - b.roleRank;
  }
  if (b && info.time?.created && info.time.created !== b.time) {
    return info.time.created - b.time;
  }
  return info.id < boundaryID ? -1 : info.id > boundaryID ? 1 : 0;
}

// Runner user IDs (`msg_u_<ms>_<n>`) and OpenCode IDs (`msg_<hex>`) do not sort
// against each other as strings ("msg_u" > "msg_1"), so order by creation time.
export function compareTurnOrder(a: MessageInfo, b: MessageInfo): number {
  const ta = a.time?.created ?? 0;
  const tb = b.time?.created ?? 0;
  if (ta && tb && ta !== tb) return ta - tb;
  return compareMessageIDOrder(a, b.id);
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
    if (compareTurnOrder(prev.info, current.info) > 0) {
      list[pos] = prev;
      list[pos - 1] = current;
      pos--;
      continue;
    }
    break;
  }
  if (pos !== idx) invalidateIndex(list);
}

export function upsertTurnInfo(list: MessageTurn[], info: MessageInfo) {
  let idx = findMessageIndex(list, info.id);

  if (info.role === "user" && !isOptimisticID(info.id) && getIndexCache(list)?.hasOptimistic !== false) {
    const optIdx = list.findIndex((m) => isOptimisticID(m.info.id));
    if (optIdx >= 0 && idx < 0) {
      // Promote the optimistic turn in place: a new object would remount the bubble.
      const turn = list[optIdx];
      Object.assign(turn.info, info);
      for (const part of turn.parts) part.messageID = info.id;
      invalidateIndex(list);
      bubbleTurnIntoOrder(list, optIdx);
      return;
    }
    if (optIdx >= 0 && idx >= 0 && optIdx !== idx) {
      const turn = list[optIdx];
      Object.assign(turn.info, info);
      for (const part of turn.parts) part.messageID = info.id;
      for (const part of list[idx].parts) {
        upsertTurnPart([turn], part);
      }
      list.splice(idx, 1);
      invalidateIndex(list);
      const nextIdx = optIdx > idx ? optIdx - 1 : optIdx;
      bubbleTurnIntoOrder(list, nextIdx);
      return;
    }
  }

  if (idx >= 0) {
    const prevID = list[idx].info.id;
    Object.assign(list[idx].info, info);
    if (prevID !== info.id) invalidateIndex(list);
    bubbleTurnIntoOrder(list, idx);
  } else {
    list.push({ info, parts: [] });
    bubbleTurnIntoOrder(list, list.length - 1);
  }
}

export function upsertTurnPart(list: MessageTurn[], part: MessagePart) {
  let turnIdx = findMessageIndex(list, part.messageID);

  if (
    turnIdx < 0 &&
    (part.type === "text" || part.type === "file") &&
    getIndexCache(list)?.hasOptimistic !== false
  ) {
    const optIdx = list.findIndex(
      (m) =>
        isOptimisticID(m.info.id) &&
        m.parts.some((p) =>
          part.type === "text"
            ? p.type === "text" && Boolean(part.text) && p.text === part.text
            : p.type === "file" && (!part.filename || p.filename === part.filename)
        )
    );
    if (optIdx >= 0) {
      list[optIdx].info.id = part.messageID;
      for (const p of list[optIdx].parts) p.messageID = part.messageID;
      invalidateIndex(list);
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

  let pIdx = findPartIndex(turn.parts, part.id);
  if (pIdx < 0 && !isOptimisticPartID(part.id) && getIndexCache(turn.parts)?.hasOptimistic !== false) {
    pIdx = turn.parts.findIndex(
      (p) =>
        isOptimisticPartID(p.id) &&
        p.type === part.type &&
        (part.type !== "file" || !part.filename || (p.type === "file" && p.filename === part.filename))
    );
    if (pIdx < 0 && part.type === "file") {
      pIdx = turn.parts.findIndex((p) => isOptimisticPartID(p.id) && p.type === "file");
    }
  }
  normalizeMessagePartInPlace(part, pIdx >= 0 ? turn.parts[pIdx] : undefined);
  if (pIdx >= 0) {
    const prevID = turn.parts[pIdx].id;
    Object.assign(turn.parts[pIdx], part);
    if (prevID !== part.id) invalidateIndex(turn.parts);
  } else {
    const pairedTextID = pairedStepTextPartID(part);
    const textIdx = pairedTextID ? findPartIndex(turn.parts, pairedTextID) : -1;
    if (textIdx >= 0) {
      turn.parts.splice(textIdx, 0, part);
      invalidateIndex(turn.parts);
    } else {
      turn.parts.push(part);
    }
  }
}

function preserveTextPartSkillMeta(target: MessagePart, source?: MessagePart): void {
  if (target.type !== "text" || !source || source.type !== "text") return;
  const srcSkill = source.metadata?.hoxiSkill;
  if (srcSkill && !target.metadata?.hoxiSkill) {
    target.metadata = { ...(target.metadata || {}), hoxiSkill: srcSkill };
  }
}

const TOOL_EDIT_PAYLOAD_KEYS = [
  "Target",
  "Replacement",
  "Content",
  "content",
  "TargetContent",
  "targetContent",
  "ReplacementContent",
  "replacementContent",
  "ReplacementChunks",
  "CodeContent",
  "codeContent",
  "oldString",
  "newString",
  "old_string",
  "new_string",
  "patch",
  "diff",
  "patchText",
] as const;

function hasToolEditPayload(input?: Record<string, unknown>): boolean {
  if (!input || typeof input !== "object") return false;
  for (const key of TOOL_EDIT_PAYLOAD_KEYS) {
    const val = input[key];
    if (typeof val === "string" && val.trim().length > 0) return true;
    if (Array.isArray(val) && val.length > 0) return true;
  }
  return false;
}

function preserveToolPartEditInput(target: MessagePart, source?: MessagePart): void {
  if (target.type !== "tool" || !source || source.type !== "tool") return;
  const srcInput = source.state?.input as Record<string, unknown> | undefined;
  if (!hasToolEditPayload(srcInput)) return;
  const dstInput = (target.state?.input || {}) as Record<string, unknown>;
  if (!hasToolEditPayload(dstInput)) {
    target.state = {
      ...target.state,
      input: { ...srcInput, ...dstInput },
    };
    if ((target.tool === "edit" || !target.tool) && source.tool === "write") {
      target.tool = source.tool;
    }
  }
}

const MAX_REACTIVE_TOOL_OUTPUT_CHARS = 32 * 1024;
const MAX_FULL_TOOL_OUTPUT_LRU = 64;
export const MAX_FULL_TOOL_OUTPUT_BYTES = 4 * 1024 * 1024;

interface CachedToolOutput {
  text: string;
  sessionID?: string;
  bytes: number;
}

const fullToolOutputCache = new Map<string, CachedToolOutput>();
let fullToolOutputCacheBytes = 0;

function detachSlicedString(text: string): string {
  if (typeof structuredClone === "function") {
    return structuredClone(text);
  }
  return JSON.parse(JSON.stringify(text)) as string;
}

function deleteCachedToolOutput(partID: string): void {
  const existing = fullToolOutputCache.get(partID);
  if (!existing) return;
  fullToolOutputCache.delete(partID);
  fullToolOutputCacheBytes = Math.max(0, fullToolOutputCacheBytes - existing.bytes);
}

export function normalizeMessagePartInPlace(part: MessagePart, previous?: MessagePart): void {
  if (part.type === "text" || part.type === "reasoning") {
    // Some Runner snapshots omit empty text. Always expose a string to renderers,
    // preserving any tokens already received when an update omits the field.
    part.text ??= previous?.type === part.type ? previous.text ?? "" : "";
  }
  preserveTextPartSkillMeta(part, previous);
  preserveToolPartEditInput(part, previous);
  // Index before compaction: task outputs carry `session_id:` at their tail.
  indexSubagentLink(part);
  compactToolPartOutputInPlace(part);
}

export function isTaskLikeToolName(tool?: string): boolean {
  const t = (tool || "").toLowerCase();
  return t === "task" || t === "agent" || t.includes("subagent");
}

/** Child session ID carried by a task tool part (metadata, input or output). */
export function subagentChildIDFromPart(part: MessagePart): string | undefined {
  if (part.type !== "tool") return undefined;
  const meta = part.state.metadata as Record<string, unknown> | undefined;
  const input = part.state.input as Record<string, unknown> | undefined;
  const direct = meta?.sessionId || meta?.sessionID || input?.task_id;
  if (direct) return String(direct);
  const out = (part.state as { output?: string }).output;
  return typeof out === "string" ? out.match(/session_id:\s*([a-zA-Z0-9_-]+)/)?.[1] : undefined;
}

// child session -> parent session, filled as task parts enter the store. Event
// routing and several memos resolve parents per update, so a scan of every
// loaded transcript there grows with history x running tasks.
const subagentParentByChild = new Map<string, string>();
const subagentChildrenByParent = new Map<string, Set<string>>();
const [subagentLinkVersion, setSubagentLinkVersion] = createSignal(0);

function indexSubagentLink(part: MessagePart): void {
  if (part.type !== "tool" || !isTaskLikeToolName(part.tool)) return;
  const child = subagentChildIDFromPart(part);
  if (!child || child === part.sessionID || subagentParentByChild.get(child) === part.sessionID) return;
  const prevParent = subagentParentByChild.get(child);
  if (prevParent && prevParent !== part.sessionID) {
    subagentChildrenByParent.get(prevParent)?.delete(child);
  }
  subagentParentByChild.set(child, part.sessionID);
  let children = subagentChildrenByParent.get(part.sessionID);
  if (!children) {
    children = new Set<string>();
    subagentChildrenByParent.set(part.sessionID, children);
  }
  children.add(child);
  setSubagentLinkVersion((v) => v + 1);
}

/** Parent of a subagent session learned from task parts; reactive to new links. */
export function lookupSubagentParent(childID: string): string | null {
  subagentLinkVersion();
  return subagentParentByChild.get(childID) ?? null;
}

/** Child subagent IDs of a parent session learned from task parts; reactive to new links. */
export function lookupSubagentChildren(parentID: string): readonly string[] {
  subagentLinkVersion();
  const set = subagentChildrenByParent.get(parentID);
  return set && set.size > 0 ? Array.from(set) : [];
}

export function forgetSubagentLinks(sessionIDs: string[]): void {
  const ids = new Set(sessionIDs);
  let changed = false;
  for (const [child, parent] of subagentParentByChild) {
    if (ids.has(child) || ids.has(parent)) {
      subagentParentByChild.delete(child);
      subagentChildrenByParent.get(parent)?.delete(child);
      changed = true;
    }
  }
  for (const id of ids) {
    if (subagentChildrenByParent.delete(id)) changed = true;
  }
  if (changed) setSubagentLinkVersion((v) => v + 1);
}

function cacheFullToolOutput(partID: string, fullText: string, sessionID?: string) {
  if (!partID || !fullText) return;
  deleteCachedToolOutput(partID);
  if (fullText.length * 2 > MAX_FULL_TOOL_OUTPUT_BYTES) {
    const maxChars = Math.floor(MAX_FULL_TOOL_OUTPUT_BYTES / 2);
    fullText = detachSlicedString(fullText.slice(0, maxChars));
  }
  const entryBytes = fullText.length * 2;
  while (
    fullToolOutputCache.size >= MAX_FULL_TOOL_OUTPUT_LRU ||
    (fullToolOutputCache.size > 0 && fullToolOutputCacheBytes + entryBytes > MAX_FULL_TOOL_OUTPUT_BYTES)
  ) {
    const oldest = fullToolOutputCache.keys().next().value;
    if (oldest === undefined) break;
    deleteCachedToolOutput(oldest);
  }
  fullToolOutputCache.set(partID, { text: fullText, sessionID, bytes: entryBytes });
  fullToolOutputCacheBytes += entryBytes;
}

export function getFullToolOutput(partID: string): string | undefined {
  if (!partID) return undefined;
  const entry = fullToolOutputCache.get(partID);
  if (!entry) return undefined;
  fullToolOutputCache.delete(partID);
  fullToolOutputCache.set(partID, entry);
  return entry.text;
}

export function estimateSessionFullToolOutputBytes(sessionID: string): number {
  if (!sessionID) return 0;
  let total = 0;
  for (const entry of fullToolOutputCache.values()) {
    if (entry.sessionID === sessionID) total += entry.bytes;
  }
  return total;
}

export function forgetSessionToolOutputs(sessionIDs: Iterable<string>): void {
  const ids = new Set(sessionIDs);
  if (ids.size === 0) return;
  for (const [partID, entry] of fullToolOutputCache) {
    if (entry.sessionID && ids.has(entry.sessionID)) {
      deleteCachedToolOutput(partID);
    }
  }
}

export function compactToolPartOutputInPlace(part: MessagePart): void {
  if (part.type !== "tool" || part.state.status !== "completed") return;
  const out = part.state.output;
  if (typeof out === "string" && out.length > MAX_REACTIVE_TOOL_OUTPUT_CHARS) {
    cacheFullToolOutput(part.id, out, part.sessionID);
    part.state.output = detachSlicedString(out.slice(0, MAX_REACTIVE_TOOL_OUTPUT_CHARS));
    part.state.metadata = {
      ...(part.state.metadata || {}),
      truncatedChars: out.length - MAX_REACTIVE_TOOL_OUTPUT_CHARS,
      fullOutputLength: out.length,
    };
  }
  const metaOut = part.state.metadata?.output;
  if (typeof metaOut === "string" && metaOut.length > MAX_REACTIVE_TOOL_OUTPUT_CHARS) {
    cacheFullToolOutput(part.id, metaOut, part.sessionID);
    part.state.metadata = {
      ...part.state.metadata,
      output: detachSlicedString(metaOut.slice(0, MAX_REACTIVE_TOOL_OUTPUT_CHARS)),
      truncatedChars: metaOut.length - MAX_REACTIVE_TOOL_OUTPUT_CHARS,
      fullOutputLength: metaOut.length,
    };
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
    return (local.text ?? "").length > (remote.text ?? "").length;
  }
  if (local.type === "text" && remote.type === "text") {
    return (local.text ?? "").length > (remote.text ?? "").length;
  }
  return false;
}

function mergeTurnInto(local: MessageTurn, remote: { info: MessageInfo; parts: MessagePart[] }) {
  if (!(isTurnSettled(local.info) && !isTurnSettled(remote.info))) {
    const prevCompleted = local.info.time?.completed;
    Object.assign(local.info, remote.info);
    if (prevCompleted && local.info.time && !local.info.time.completed) {
      local.info.time = { ...local.info.time, completed: prevCompleted };
    }
  }
  const localSkillPart = local.parts.find(
    (p): p is Extract<MessagePart, { type: "text" }> =>
      p.type === "text" && Boolean(p.metadata?.hoxiSkill)
  );
  const remoteIDs = new Set(remote.parts.map((p) => p.id));
  const nextParts: MessagePart[] = [];
  for (const rp of remote.parts) {
    normalizeMessagePartInPlace(rp);
    if (localSkillPart && rp.type === "text") {
      preserveTextPartSkillMeta(rp, localSkillPart);
    }
    let idx = findPartIndex(local.parts, rp.id);
    if (idx < 0 && !isOptimisticPartID(rp.id)) {
      idx = local.parts.findIndex(
        (p) =>
          isOptimisticPartID(p.id) &&
          p.type === rp.type &&
          !nextParts.includes(p) &&
          (rp.type !== "file" || !rp.filename || (p.type === "file" && p.filename === rp.filename))
      );
    }
    const lp = idx >= 0 ? local.parts[idx] : undefined;
    if (!lp || lp.type !== rp.type) {
      nextParts.push(rp);
      continue;
    }
    preserveTextPartSkillMeta(rp, lp);
    preserveToolPartEditInput(rp, lp);
    preserveToolPartEditInput(lp, rp);
    const prevID = lp.id;
    if (!isLocalPartAhead(lp, rp)) Object.assign(lp, rp);
    if (prevID !== lp.id) invalidateIndex(local.parts);
    nextParts.push(lp);
  }
  // Parts streamed in after the snapshot was taken; optimistic placeholders yield to real parts.
  for (const lp of local.parts) {
    if (remoteIDs.has(lp.id)) continue;
    if (isOptimisticPartID(lp.id) && remote.parts.length > 0) continue;
    nextParts.push(lp);
  }
  orderSameStepReasoningPartsInPlace(nextParts);
  if (
    local.parts.length !== nextParts.length ||
    local.parts.some((p, i) => p !== nextParts[i])
  ) {
    local.parts.splice(0, local.parts.length, ...nextParts);
  }
  invalidateIndex(local.parts);
}

/**
 * Merges a REST message snapshot into the live store list in place (call inside `produce`).
 * Existing turn/part objects are updated rather than replaced so streamed progress that is
 * newer than the snapshot survives and rendered cards keep their identity.
 *
 * With `keepOlder` the snapshot is only the newest page of history: loaded
 * turns older than its first turn stay in front instead of being treated as
 * deleted. Returns how many such older turns were kept.
 */
export function mergeServerTurnsInto(
  list: MessageTurn[],
  remoteTurns: Array<{ info: MessageInfo; parts: MessagePart[] }>,
  /** Turn IDs already in the list when the snapshot was requested (e.g. a cached seed). */
  preRequestIDs?: ReadonlySet<string>,
  options?: { keepOlder?: boolean }
): number {
  const existing = list.slice();
  const priorRealUserIDs = new Set(
    existing
      .filter((t) => t.info.role === "user" && !isOptimisticID(t.info.id))
      .map((t) => t.info.id)
  );
  const hasNewRemoteUserTurn = remoteTurns.some(
    (t) => t.info.role === "user" && !priorRealUserIDs.has(t.info.id)
  );
  const lastRemote = remoteTurns[remoteTurns.length - 1]?.info;
  const firstRemote = options?.keepOlder ? remoteTurns[0]?.info : undefined;
  const older: MessageTurn[] = [];

  const remoteIDs = new Set<string>();
  const next: MessageTurn[] = [];
  let promoted: MessageTurn | undefined;
  for (const remote of remoteTurns) {
    for (const rp of remote.parts) normalizeMessagePartInPlace(rp);
    orderSameStepReasoningPartsInPlace(remote.parts);
    remoteIDs.add(remote.info.id);
    const idx = findMessageIndex(existing, remote.info.id);
    if (idx < 0) {
      const optimistic = remote.info.role === "user" && !promoted && !priorRealUserIDs.has(remote.info.id)
        ? existing.find((t) => isOptimisticID(t.info.id))
        : undefined;
      if (optimistic) {
        // Promote the optimistic bubble in place; a fresh object would remount it.
        promoted = optimistic;
        if (remote.parts.length > 0) {
          mergeTurnInto(optimistic, remote);
        } else {
          optimistic.info = remote.info;
          for (const part of optimistic.parts) part.messageID = remote.info.id;
        }
        next.push(optimistic);
        continue;
      }
      next.push({ info: remote.info, parts: remote.parts });
      continue;
    }
    mergeTurnInto(existing[idx], remote);
    next.push(existing[idx]);
  }

  for (const turn of existing) {
    if (turn === promoted || remoteIDs.has(turn.info.id)) continue;
    if (isOptimisticID(turn.info.id)) {
      if (!hasNewRemoteUserTurn) next.push(turn);
      continue;
    }
    // An empty snapshot has no cutoff: SSE may have filled the list while REST
    // was in flight. Keep those turns mounted instead of clearing the transcript,
    // but drop turns that predate the request: the server no longer has them.
    if (!lastRemote) {
      if (!preRequestIDs?.has(turn.info.id)) next.push(turn);
    } else if (firstRemote && compareTurnOrder(turn.info, firstRemote) < 0) {
      older.push(turn);
    } else if (compareTurnOrder(turn.info, lastRemote) > 0) {
      next.push(turn);
    }
  }
  if (older.length > 0) next.unshift(...older);

  if (
    list.length !== next.length ||
    list.some((t, i) => t !== next[i])
  ) {
    list.splice(0, list.length, ...next);
  }
  invalidateIndex(list);
  return older.length;
}
