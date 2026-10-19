import { createSignal } from "solid-js";
import type { MessageInfo, MessagePart, SnapshotFileDiff } from "../services/sse-types";
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
      turn.info = info;
      for (const part of turn.parts) part.messageID = info.id;
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
  normalizeMessagePartInPlace(part, pIdx >= 0 ? turn.parts[pIdx] : undefined);
  if (pIdx >= 0) {
    Object.assign(turn.parts[pIdx], part);
  } else {
    turn.parts.push(part);
  }
}

const MAX_REACTIVE_TOOL_OUTPUT_CHARS = 32 * 1024;
const MAX_FULL_TOOL_OUTPUT_LRU = 64;
const fullToolOutputCache = new Map<string, string>();

function normalizeMessagePartInPlace(part: MessagePart, previous?: MessagePart): void {
  if (part.type === "text" || part.type === "reasoning") {
    // Some Runner snapshots omit empty text. Always expose a string to renderers,
    // preserving any tokens already received when an update omits the field.
    part.text ??= previous?.type === part.type ? previous.text ?? "" : "";
  }
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
  const remoteIDs = new Set(remote.parts.map((p) => p.id));
  const nextParts: MessagePart[] = [];
  for (const rp of remote.parts) {
    normalizeMessagePartInPlace(rp);
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
  remoteTurns: Array<{ info: MessageInfo; parts: MessagePart[] }>,
  /** Turn IDs already in the list when the snapshot was requested (e.g. a cached seed). */
  preRequestIDs?: ReadonlySet<string>
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
  const lastRemote = remoteTurns[remoteTurns.length - 1]?.info;

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
        optimistic.info = remote.info;
        if (remote.parts.length > 0) optimistic.parts = remote.parts;
        else for (const part of optimistic.parts) part.messageID = remote.info.id;
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
    } else if (compareTurnOrder(turn.info, lastRemote) > 0) {
      next.push(turn);
    }
  }

  if (
    list.length !== next.length ||
    list.some((t, i) => t !== next[i])
  ) {
    list.splice(0, list.length, ...next);
  }
}

const SESSION_EDITING_TOOLS = new Set([
  "edit",
  "write",
  "patch",
  "multiedit",
  "apply_patch",
  "str_replace_based_edit_tool",
  "create_file",
  "write_to_file",
  "replace_file_content",
  "multi_replace_file_content",
  "file_edit",
  "notebookedit",
]);

function normalizeSessionRelPath(raw: string, rootDir: string): string {
  let trimmed = (raw || "").trim().replace(/\\/g, "/");
  if (!trimmed) return "";
  const root = (rootDir || "").trim().replace(/\\/g, "/").replace(/\/+$/, "");
  if (root && (trimmed === root || trimmed.startsWith(`${root}/`))) {
    trimmed = trimmed.slice(root.length).replace(/^\/+/, "");
  } else {
    trimmed = trimmed.replace(/^\.\/+/, "");
  }
  return trimmed.replace(/^\.hoxi\/worktrees\/[^/]+\/+/, "");
}

export function filterLocalSessionDiffsByTurns(
  sessionID: string,
  diffs: SnapshotFileDiff[],
  turnsBySession: Record<string, MessageTurn[]>,
  rootDir: string
): SnapshotFileDiff[] {
  if (!diffs.length) return diffs;
  const turns = turnsBySession[sessionID];
  if (!turns || turns.length === 0) return diffs;

  const touched = new Set<string>();
  const addPath = (raw?: unknown) => {
    if (typeof raw !== "string") return;
    const norm = normalizeSessionRelPath(raw, rootDir);
    if (norm) touched.add(norm);
  };

  const visited = new Set<string>();
  const scanSession = (sid: string) => {
    if (!sid || visited.has(sid)) return;
    visited.add(sid);
    for (const turn of turnsBySession[sid] ?? []) {
      for (const p of turn.parts) {
        if (p.type !== "tool") continue;
        const toolName = (p.tool || "").toLowerCase();
        if (toolName === "task" || toolName === "agent" || toolName.includes("subagent")) {
          const meta = p.state.status !== "pending" ? (p.state.metadata as Record<string, any> | undefined) : undefined;
          const inp = (p.state.input || {}) as Record<string, any>;
          const out = (p.state as { output?: string }).output;
          const outMatch = typeof out === "string" ? out.match(/session_id:\s*([a-zA-Z0-9_-]+)/) : null;
          const childID = meta?.sessionId || meta?.sessionID || inp.task_id || outMatch?.[1];
          if (typeof childID === "string") scanSession(childID);
          continue;
        }
        if (p.state.status !== "completed" || !SESSION_EDITING_TOOLS.has(toolName)) continue;
        const inp = (p.state.input || {}) as Record<string, any>;
        addPath(
          inp.filePath ||
            inp.file_path ||
            inp.path ||
            inp.file ||
            inp.TargetFile ||
            inp.target_file ||
            inp.targetFile ||
            inp.filename ||
            inp.notebook_path
        );
        if (typeof inp.patchText === "string") {
          for (const line of inp.patchText.split(/\r?\n/)) {
            const m = line.match(/^\*\*\*\s+(?:Update|Add|Delete)\s+File:\s*(.+)$/);
            if (m) addPath(m[1]);
          }
        }
        const meta = (p.state.metadata || {}) as Record<string, any>;
        addPath(meta.filediff?.file || meta.filediff?.path);
        if (Array.isArray(meta.files)) {
          for (const item of meta.files) {
            if (typeof item === "string") addPath(item);
            else if (item && typeof item === "object") {
              addPath(item.relativePath || item.filePath || item.file || item.path);
            }
          }
        }
      }
    }
  };

  scanSession(sessionID);
  if (touched.size === 0) return [];

  return diffs.filter((d) => {
    const norm = normalizeSessionRelPath(d.file, rootDir);
    if (!norm) return false;
    if (touched.has(norm)) return true;
    for (const f of touched) {
      if (norm.endsWith(`/${f}`) || f.endsWith(`/${norm}`)) return true;
    }
    return false;
  });
}


// ---------------------------------------------------------------------------
// Stale-While-Revalidate Local Session Turns Snapshot Cache (Kilo pattern)
// ---------------------------------------------------------------------------

const SESSION_TURNS_CACHE_KEY = "hoxi.session-turns-cache:v1";
const MAX_CACHED_SESSIONS = 12;
const MAX_SNAPSHOT_TURNS_PER_SESSION = 20;
const MAX_SNAPSHOT_PART_CHARS = 2048;

interface CachedSessionTurnsBundle {
  order: string[];
  entries: Record<string, MessageTurn[]>;
}

let cachedSessionTurnsBundle: CachedSessionTurnsBundle | null = null;

function loadSessionTurnsBundle(): CachedSessionTurnsBundle {
  if (cachedSessionTurnsBundle) return cachedSessionTurnsBundle;
  try {
    const raw = typeof localStorage !== "undefined" ? localStorage.getItem(SESSION_TURNS_CACHE_KEY) : null;
    if (raw) {
      const parsed = JSON.parse(raw);
      if (parsed && typeof parsed === "object" && Array.isArray(parsed.order) && typeof parsed.entries === "object") {
        cachedSessionTurnsBundle = parsed as CachedSessionTurnsBundle;
        return cachedSessionTurnsBundle;
      }
    }
  } catch {
    /* ignore storage errors */
  }
  cachedSessionTurnsBundle = { order: [], entries: {} };
  return cachedSessionTurnsBundle;
}

function saveSessionTurnsBundle(bundle: CachedSessionTurnsBundle): void {
  cachedSessionTurnsBundle = bundle;
  if (typeof localStorage === "undefined") return;
  try {
    localStorage.setItem(SESSION_TURNS_CACHE_KEY, JSON.stringify(bundle));
  } catch {
    // If quota exceeded, evict oldest half and retry once
    try {
      const keep = bundle.order.slice(-Math.max(2, Math.floor(bundle.order.length / 2)));
      const trimmed: Record<string, MessageTurn[]> = {};
      for (const id of keep) {
        if (bundle.entries[id]) trimmed[id] = bundle.entries[id];
      }
      cachedSessionTurnsBundle = { order: keep, entries: trimmed };
      localStorage.setItem(SESSION_TURNS_CACHE_KEY, JSON.stringify(cachedSessionTurnsBundle));
    } catch {
      /* ignore storage errors */
    }
  }
}

function compactTurnForSnapshot(turn: MessageTurn): MessageTurn {
  return {
    info: { ...turn.info },
    parts: turn.parts
      .filter((p) => p.type !== "file" || !p.url?.startsWith("data:"))
      .map((p) => {
        if (p.type === "text" || p.type === "reasoning") {
          const text = p.text && p.text.length > MAX_SNAPSHOT_PART_CHARS
            ? p.text.slice(0, MAX_SNAPSHOT_PART_CHARS)
            : p.text;
          return { ...p, text };
        }
        if (p.type === "tool") {
          if (p.state.status === "completed") {
            const out = p.state.output && p.state.output.length > MAX_SNAPSHOT_PART_CHARS
              ? p.state.output.slice(0, MAX_SNAPSHOT_PART_CHARS)
              : p.state.output;
            return {
              ...p,
              state: {
                ...p.state,
                output: out,
                metadata: p.state.metadata
                  ? { ...p.state.metadata, output: undefined, stdout: undefined }
                  : {},
              },
            };
          }
        }
        return p;
      }),
  };
}

export function persistCachedSessionTurns(sessionID: string, turns: MessageTurn[]): void {
  if (!sessionID || !Array.isArray(turns)) return;
  // An empty merged snapshot means the server has no history; drop stale cache seeds.
  if (turns.length === 0) return evictCachedSessionTurns([sessionID]);
  const tail = turns.slice(-MAX_SNAPSHOT_TURNS_PER_SESSION).map(compactTurnForSnapshot);
  const bundle = loadSessionTurnsBundle();
  bundle.order = bundle.order.filter((id) => id !== sessionID);
  bundle.order.push(sessionID);
  bundle.entries[sessionID] = tail;
  while (bundle.order.length > MAX_CACHED_SESSIONS) {
    const oldest = bundle.order.shift();
    if (oldest) delete bundle.entries[oldest];
  }
  saveSessionTurnsBundle(bundle);
}

export function readCachedSessionTurns(sessionID: string): MessageTurn[] | undefined {
  if (!sessionID) return undefined;
  const bundle = loadSessionTurnsBundle();
  const turns = bundle.entries[sessionID];
  if (!Array.isArray(turns) || turns.length === 0) return undefined;
  for (const turn of turns) {
    for (const part of turn.parts) normalizeMessagePartInPlace(part);
  }
  return turns;
}

export function evictCachedSessionTurns(sessionIDs: string[]): void {
  if (!sessionIDs.length) return;
  const bundle = loadSessionTurnsBundle();
  let changed = false;
  const removeSet = new Set(sessionIDs);
  for (const id of sessionIDs) {
    if (bundle.entries[id]) {
      delete bundle.entries[id];
      changed = true;
    }
  }
  if (changed) {
    bundle.order = bundle.order.filter((id) => !removeSet.has(id));
    saveSessionTurnsBundle(bundle);
  }
}

