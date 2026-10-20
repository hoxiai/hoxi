import { produce } from "solid-js/store";
import { setWorkspace, workspace } from "./workspace";
import type { MessageTurn } from "./workspace";
import { invalidateSessionDiffReads, loadSessionMessagesOnly } from "./workspace-sessions";

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
      lastUsed.delete(id);
    }
  }));
  return evicted;
}

// Queue-overflow recovery includes busy transcripts: waiting for the next idle
// event would leave missing text/parts on screen for the rest of a long run.
export async function resyncResidentTranscripts(): Promise<void> {
  const ids = new Set(Object.keys(workspace.messagesBySession));
  if (workspace.activeSessionID) ids.add(workspace.activeSessionID);
  const queue = [...ids];
  let next = 0;
  await Promise.all(Array.from({ length: Math.min(4, queue.length) }, async () => {
    while (next < queue.length) {
      const id = queue[next++];
      await loadSessionMessagesOnly(id).catch(() => {});
    }
  }));
}
