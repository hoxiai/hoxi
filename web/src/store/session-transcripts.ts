import { produce } from "solid-js/store";
import type { MessageInfo } from "../services/sse-types";
import { normalizeMessagePartInPlace } from "./message-lookup";
import { setWorkspace, workspace } from "./workspace-state";
import type { MessageTurn } from "./workspace-state";
import { invalidateSessionDiffReads } from "./session-diffs";
import { forgetSessionHistory } from "./session-history";

export const MAX_RESIDENT_TRANSCRIPTS = 12;
export const MAX_RESIDENT_TRANSCRIPT_BYTES = 32 * 1024 * 1024;
export const hydratedSessionIDs = new Set<string>();
const lastUsed = new Map<string, number>();
const pendingReads = new Map<string, number>();
let accessSequence = 0;

export function touchSessionTranscript(id: string): void {
  lastUsed.set(id, ++accessSequence);
}

// Protect snapshots while they are being fetched so an eviction cannot be
// immediately undone by a late response to an older request.
export function beginSessionTranscriptRead(id: string): () => void {
  pendingReads.set(id, (pendingReads.get(id) ?? 0) + 1);
  touchSessionTranscript(id);
  return () => {
    const remaining = (pendingReads.get(id) ?? 1) - 1;
    if (remaining > 0) pendingReads.set(id, remaining);
    else pendingReads.delete(id);
  };
}

export function isReadingSessionTranscript(id: string): boolean {
  return pendingReads.has(id);
}

function valueBytes(value: unknown, depth = 0): number {
  if (depth > 16 || value == null) return 0;
  if (typeof value === "string") return value.length * 2;
  if (Array.isArray(value)) return value.reduce((total, item) => total + 16 + valueBytes(item, depth + 1), 0);
  if (typeof value === "object") {
    let total = 0;
    for (const [key, item] of Object.entries(value)) total += 32 + key.length * 2 + valueBytes(item, depth + 1);
    return total;
  }
  return 8;
}

export function estimateTranscriptBytes(turns: MessageTurn[]): number {
  let bytes = turns.length * 256;
  for (const turn of turns) {
    for (const part of turn.parts) {
      bytes += 256;
      if (part.type === "text" || part.type === "reasoning") bytes += (part.text?.length ?? 0) * 2;
      else if (part.type === "file") bytes += (part.url?.length ?? 0) * 2;
      else if (part.type === "tool") bytes += valueBytes(part.state);
    }
  }
  return bytes;
}

function protectedTranscripts(): Set<string> {
  const protectedIDs = new Set(pendingReads.keys());
  const parents = new Map(workspace.sessions.map((session) => [session.id, session.parentID]));
  let root = workspace.activeSessionID;
  const ancestors = new Set<string>();
  while (root && !ancestors.has(root)) {
    ancestors.add(root);
    protectedIDs.add(root);
    root = parents.get(root) ?? null;
  }
  // Inline subagent views and the active conversation keep their whole family.
  for (const id of Object.keys(workspace.messagesBySession)) {
    let current: string | undefined = id;
    const visited = new Set<string>();
    while (current && !visited.has(current)) {
      if (ancestors.has(current)) { protectedIDs.add(id); break; }
      visited.add(current);
      current = parents.get(current);
    }
    const status = workspace.statusBySession[id]?.type;
    if (status === "busy" || status === "retry" || workspace.loadingMessagesBySession[id] ||
        workspace.queuedPromptsBySession[id]?.length ||
        workspace.messagesBySession[id]?.some((turn) => turn.info.id.startsWith("optimistic_"))) protectedIDs.add(id);
  }
  for (const request of [...workspace.pendingPermissions, ...workspace.pendingQuestions]) protectedIDs.add(request.sessionID);
  return protectedIDs;
}

// Run periodically from App. Only durable idle bodies/diffs are released;
// session metadata, drafts, model choices and unread/approval state stay intact.
export function trimSessionTranscripts(
  maxSessions = MAX_RESIDENT_TRANSCRIPTS,
  maxBytes = MAX_RESIDENT_TRANSCRIPT_BYTES,
): string[] {
  const ids = Object.keys(workspace.messagesBySession).filter((id) => workspace.messagesBySession[id] !== undefined);
  const present = new Set(ids);
  for (const id of lastUsed.keys()) if (!present.has(id) && !pendingReads.has(id)) lastUsed.delete(id);
  const protectedIDs = protectedTranscripts();
  const entries = ids.map((id) => ({ id, bytes: estimateTranscriptBytes(workspace.messagesBySession[id]), used: lastUsed.get(id) ?? 0 }));
  let bytes = entries.reduce((total, entry) => total + entry.bytes, 0);
  let count = entries.length;
  const evicted: string[] = [];
  for (const entry of entries.sort((a, b) => a.used - b.used)) {
    if (count <= maxSessions && bytes <= maxBytes) break;
    if (protectedIDs.has(entry.id)) continue;
    evicted.push(entry.id);
    bytes -= entry.bytes;
    count--;
  }
  if (evicted.length) setWorkspace(produce((state) => {
    for (const id of evicted) {
      invalidateSessionDiffReads(id);
      delete state.messagesBySession[id];
      delete state.diffsBySession[id];
      delete state.worktreeDiffsBySession[id];
      hydratedSessionIDs.delete(id);
      forgetSessionHistory(id);
      lastUsed.delete(id);
    }
  }));
  return evicted;
}

// ---------------------------------------------------------------------------
// Stale-While-Revalidate Local Session Turns Snapshot Cache (Kilo pattern)
// ---------------------------------------------------------------------------

const SESSION_TURNS_CACHE_KEY = "hoxi.session-turns-cache:v1";
const MAX_CACHED_SESSIONS = 12;
const MAX_SNAPSHOT_TURNS_PER_SESSION = 20;
const MAX_SNAPSHOT_PART_CHARS = 16_384;
const MAX_SNAPSHOT_BUNDLE_CHARS = 600_000;

interface CachedSessionTurnsBundle {
  order: string[];
  entries: Record<string, MessageTurn[]>;
}

let cachedSessionTurnsBundle: CachedSessionTurnsBundle | null = null;
let saveSessionTurnsTimer: ReturnType<typeof setTimeout> | null = null;
let lastFlushedSessionTurnsJSON = "";

function loadSessionTurnsBundle(): CachedSessionTurnsBundle {
  if (cachedSessionTurnsBundle) return cachedSessionTurnsBundle;
  try {
    const raw = typeof localStorage !== "undefined" ? localStorage.getItem(SESSION_TURNS_CACHE_KEY) : null;
    if (raw) {
      const parsed = JSON.parse(raw);
      if (parsed && typeof parsed === "object" && Array.isArray(parsed.order) && typeof parsed.entries === "object") {
        cachedSessionTurnsBundle = parsed as CachedSessionTurnsBundle;
        lastFlushedSessionTurnsJSON = raw;
        return cachedSessionTurnsBundle;
      }
    }
  } catch {
    /* ignore storage errors */
  }
  cachedSessionTurnsBundle = { order: [], entries: {} };
  return cachedSessionTurnsBundle;
}

function serializeSessionTurnsBundle(bundle: CachedSessionTurnsBundle): string {
  let raw = JSON.stringify(bundle);
  while (raw.length > MAX_SNAPSHOT_BUNDLE_CHARS && bundle.order.length > 1) {
    const oldest = bundle.order.shift();
    if (oldest) delete bundle.entries[oldest];
    raw = JSON.stringify(bundle);
  }
  return raw;
}

function flushSessionTurnsBundleToStorage(): void {
  saveSessionTurnsTimer = null;
  let bundle = cachedSessionTurnsBundle;
  if (!bundle || typeof localStorage === "undefined") return;
  let raw = serializeSessionTurnsBundle(bundle);
  if (raw.length > MAX_SNAPSHOT_BUNDLE_CHARS) {
    bundle = { order: [], entries: {} };
    cachedSessionTurnsBundle = bundle;
    raw = JSON.stringify(bundle);
  }
  if (raw === lastFlushedSessionTurnsJSON) return;
  try {
    localStorage.setItem(SESSION_TURNS_CACHE_KEY, raw);
    lastFlushedSessionTurnsJSON = raw;
    return;
  } catch {
    /* fall through to quota eviction */
  }
  // If quota exceeded, evict oldest half and retry once
  try {
    const keep = bundle.order.slice(-Math.max(2, Math.floor(bundle.order.length / 2)));
    const trimmed: Record<string, MessageTurn[]> = {};
    for (const id of keep) {
      if (bundle.entries[id]) trimmed[id] = bundle.entries[id];
    }
    cachedSessionTurnsBundle = { order: keep, entries: trimmed };
    const trimmedRaw = JSON.stringify(cachedSessionTurnsBundle);
    localStorage.setItem(SESSION_TURNS_CACHE_KEY, trimmedRaw);
    lastFlushedSessionTurnsJSON = trimmedRaw;
  } catch {
    /* ignore storage errors */
  }
}

function saveSessionTurnsBundle(bundle: CachedSessionTurnsBundle): void {
  cachedSessionTurnsBundle = bundle;
  if (typeof localStorage === "undefined") return;
  if (saveSessionTurnsTimer !== null) clearTimeout(saveSessionTurnsTimer);
  saveSessionTurnsTimer = setTimeout(flushSessionTurnsBundleToStorage, 350);
}

function compactTurnForSnapshot(turn: MessageTurn): MessageTurn {
  const info = { ...turn.info } as MessageInfo & { summary?: unknown };
  delete info.summary;
  return {
    info,
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

