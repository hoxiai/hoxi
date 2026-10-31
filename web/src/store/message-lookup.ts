import { createSignal } from "solid-js";
import type { MessageInfo, MessagePart } from "../services/sse-types";
import type { MessageTurn } from "./workspace-state";

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

// Runner user IDs (`msg_u_<ms>_<n>`) and OpenCode IDs (`msg_<hex>`) do not sort
// against each other as strings ("msg_u" > "msg_1"), so order by creation time.
function compareTurnOrder(a: MessageInfo, b: MessageInfo): number {
  const ta = a.time?.created ?? 0;
  const tb = b.time?.created ?? 0;
  if (ta && tb && ta !== tb) return ta - tb;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
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
}

export function upsertTurnInfo(list: MessageTurn[], info: MessageInfo) {
  let idx = findMessageIndex(list, info.id);

  if (info.role === "user" && !isOptimisticID(info.id)) {
    const optIdx = list.findIndex((m) => isOptimisticID(m.info.id));
    if (optIdx >= 0 && idx < 0) {
      // Promote the optimistic turn in place: a new object would remount the bubble.
      const turn = list[optIdx];
      Object.assign(turn.info, info);
      for (const part of turn.parts) part.messageID = info.id;
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
      const nextIdx = optIdx > idx ? optIdx - 1 : optIdx;
      bubbleTurnIntoOrder(list, nextIdx);
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

  if (turnIdx < 0 && (part.type === "text" || part.type === "file")) {
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
  if (pIdx < 0 && !isOptimisticPartID(part.id)) {
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
    Object.assign(turn.parts[pIdx], part);
  } else {
    turn.parts.push(part);
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
const fullToolOutputCache = new Map<string, string>();

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
const [subagentLinkVersion, setSubagentLinkVersion] = createSignal(0);

function indexSubagentLink(part: MessagePart): void {
  if (part.type !== "tool" || !isTaskLikeToolName(part.tool)) return;
  const child = subagentChildIDFromPart(part);
  if (!child || child === part.sessionID || subagentParentByChild.get(child) === part.sessionID) return;
  subagentParentByChild.set(child, part.sessionID);
  setSubagentLinkVersion((v) => v + 1);
}

/** Parent of a subagent session learned from task parts; reactive to new links. */
export function lookupSubagentParent(childID: string): string | null {
  subagentLinkVersion();
  return subagentParentByChild.get(childID) ?? null;
}

export function forgetSubagentLinks(sessionIDs: string[]): void {
  const ids = new Set(sessionIDs);
  let changed = false;
  for (const [child, parent] of subagentParentByChild) {
    if (ids.has(child) || ids.has(parent)) {
      subagentParentByChild.delete(child);
      changed = true;
    }
  }
  if (changed) setSubagentLinkVersion((v) => v + 1);
}

function cacheFullToolOutput(partID: string, fullText: string) {
  if (!partID || !fullText) return;
  if (fullToolOutputCache.has(partID)) {
    fullToolOutputCache.delete(partID);
  } else if (fullToolOutputCache.size >= MAX_FULL_TOOL_OUTPUT_LRU) {
    const oldest = fullToolOutputCache.keys().next().value;
    if (oldest !== undefined) fullToolOutputCache.delete(oldest);
  }
  fullToolOutputCache.set(partID, fullText);
}

export function getFullToolOutput(partID: string): string | undefined {
  if (!partID || !fullToolOutputCache.has(partID)) return undefined;
  const val = fullToolOutputCache.get(partID)!;
  fullToolOutputCache.delete(partID);
  fullToolOutputCache.set(partID, val);
  return val;
}

export function compactToolPartOutputInPlace(part: MessagePart): void {
  if (part.type !== "tool" || part.state.status !== "completed") return;
  const out = part.state.output;
  if (typeof out === "string" && out.length > MAX_REACTIVE_TOOL_OUTPUT_CHARS) {
    cacheFullToolOutput(part.id, out);
    part.state.output = out.slice(0, MAX_REACTIVE_TOOL_OUTPUT_CHARS);
    part.state.metadata = {
      ...(part.state.metadata || {}),
      truncatedChars: out.length - MAX_REACTIVE_TOOL_OUTPUT_CHARS,
      fullOutputLength: out.length,
    };
  }
  const metaOut = part.state.metadata?.output;
  if (typeof metaOut === "string" && metaOut.length > MAX_REACTIVE_TOOL_OUTPUT_CHARS) {
    cacheFullToolOutput(part.id, metaOut);
    part.state.metadata = {
      ...part.state.metadata,
      output: metaOut.slice(0, MAX_REACTIVE_TOOL_OUTPUT_CHARS),
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
    Object.assign(local.info, remote.info);
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
  return older.length;
}

