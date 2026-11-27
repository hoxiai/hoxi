// Only request identity and scope are durable here: never persist prompt or
// attachment bodies. Unconfirmed effects are inspected, never replayed on load.
import { createSignal } from "solid-js";
import { hoxiAPI } from "../services/api";
import { createClientRequestID, recoverPromptRequest, RequestOutcomeUnknownError } from "../services/request-ledger";
import { t } from "../i18n";
import { sessionGoal } from "./session-goal-state";

const KEY = "hoxi.pending-prompts:v1";
const MAX_PENDING = 128;
const active = new Map<string, Promise<void>>();
const lastErrorBySession = new Map<string, string>();
const [pendingVersion, setPendingVersion] = createSignal(0);

export interface PendingPromptRecord {
  sessionID: string;
  directory: string;
  clientRequestId: string;
  lastError?: string;
}

interface PendingPrompt {
  sessionID: string;
  directory: string;
  clientRequestId: string;
}

function bumpPendingVersion(): void {
  setPendingVersion((v) => v + 1);
}

function rememberLastError(sessionID: string, message: string): void {
  const trimmed = message.trim();
  if (!sessionID || !trimmed) return;
  if (lastErrorBySession.size >= MAX_PENDING && !lastErrorBySession.has(sessionID)) {
    const oldest = lastErrorBySession.keys().next().value;
    if (oldest) lastErrorBySession.delete(oldest);
  }
  lastErrorBySession.set(sessionID, trimmed);
}

function readPending(): PendingPrompt[] {
  let value: unknown;
  try {
    const raw = localStorage.getItem(KEY);
    value = raw ? JSON.parse(raw) : [];
  } catch {
    throw new Error(t("timeline.pendingRequestsUnreadable"));
  }
  if (!Array.isArray(value) || value.length > MAX_PENDING || value.some((row: unknown) =>
    !row || typeof row !== "object" ||
    !("sessionID" in row) || typeof row.sessionID !== "string" ||
    !("directory" in row) || typeof row.directory !== "string" ||
    !("clientRequestId" in row) || typeof row.clientRequestId !== "string" || !row.clientRequestId
  )) throw new Error(t("timeline.pendingRequestsUnreadable"));
  return value;
}

function writePending(rows: PendingPrompt[]): void {
  localStorage.setItem(KEY, JSON.stringify(rows));
  bumpPendingVersion();
}

function forgetPending(id: string, sessionID?: string): void {
  const current = readPending();
  const next = current.filter((row) => row.clientRequestId !== id);
  if (sessionID && !next.some((row) => row.sessionID === sessionID)) {
    lastErrorBySession.delete(sessionID);
  }
  writePending(next);
}

// Reactive inspection for the composer recovery banner: returns an unresolved
// persisted prompt record when no request is actively in-flight for sessionID.
export function getPendingPrompt(sessionID: string | null | undefined): PendingPromptRecord | null {
  pendingVersion();
  const sid = (sessionID ?? "").trim();
  if (!sid || active.has(sid)) return null;
  try {
    const row = readPending().find((item) => item.sessionID === sid);
    if (!row) return null;
    const lastError = lastErrorBySession.get(sid);
    return lastError ? { ...row, lastError } : { ...row };
  } catch {
    return null;
  }
}

export function isPendingPromptStorageCorrupt(): boolean {
  pendingVersion();
  try {
    readPending();
    return false;
  } catch {
    return true;
  }
}

// Called before any new send, not just the Retry button. A changed model or
// prompt must not bypass an unresolved request from this session.
export function hasPendingPrompt(sessionID: string): boolean {
  pendingVersion();
  return active.has(sessionID) || readPending().some((row) => row.sessionID === sessionID);
}

export async function recoverPendingPrompt(sessionID: string): Promise<boolean> {
  // A business-error SSE can arrive before the submission response. Await
  // that response before deciding whether a semantic retry is safe.
  const inFlight = active.get(sessionID);
  if (inFlight) await inFlight;
  const pending = readPending().find((row) => row.sessionID === sessionID);
  if (!pending) {
    if (lastErrorBySession.delete(sessionID)) bumpPendingVersion();
    return false;
  }
  try {
    const outcome = await recoverPromptRequest(pending);
    forgetPending(pending.clientRequestId, sessionID);
    return outcome === "completed";
  } catch (err) {
    if (err instanceof RequestOutcomeUnknownError) {
      rememberLastError(sessionID, err.message);
      bumpPendingVersion();
    }
    throw err;
  }
}

// Explicitly re-checks a stuck pending prompt against the host request ledger
// without ever dispatching a new POST.
export async function recheckPendingPrompt(
  sessionID: string
): Promise<"completed" | "still_unknown" | "none"> {
  const sid = sessionID.trim();
  if (!sid) return "none";
  try {
    const completed = await recoverPendingPrompt(sid);
    return completed ? "completed" : "none";
  } catch (err) {
    if (err instanceof RequestOutcomeUnknownError) {
      return "still_unknown";
    }
    throw err;
  }
}

// Manual human escape hatch for lingering outcome_unknown / not_admitted locks.
// Requires exact clientRequestId match, refuses while a POST is in flight, and
// NEVER replays the previous request.
export function dismissPendingPrompt(sessionID: string, clientRequestId: string): boolean {
  const sid = sessionID.trim();
  const reqID = clientRequestId.trim();
  if (!sid || !reqID || active.has(sid)) return false;
  const rows = readPending();
  const next = rows.filter(
    (row) => !(row.sessionID === sid && row.clientRequestId === reqID)
  );
  if (next.length === rows.length) return false;
  if (!next.some((row) => row.sessionID === sid)) {
    lastErrorBySession.delete(sid);
  }
  writePending(next);
  return true;
}

// Explicit human reset when localStorage pending-request state is corrupt and
// blocks all sessions from sending. Never called automatically.
export function resetCorruptPendingPrompts(): void {
  lastErrorBySession.clear();
  writePending([]);
}

export async function sendRecoverablePrompt(
  sessionID: string,
  text: string,
  options: Parameters<typeof hoxiAPI.sendPromptAsync>[2],
  isCurrent: () => boolean = () => true
): Promise<"sent" | "recovered"> {
  if (await recoverPendingPrompt(sessionID)) return "recovered";
  if (!isCurrent()) throw new Error("Request was cancelled before dispatch");
  const rows = readPending();
  if (rows.length >= MAX_PENDING) throw new Error(t("timeline.pendingRequestsFull"));
  const pending: PendingPrompt = {
    sessionID,
    directory: options?.directory || "",
    clientRequestId: options?.clientRequestId?.trim() || createClientRequestID("prompt"),
  };
  // This synchronous durable reservation also prevents concurrent sends from
  // issuing a second command while the first command's response is pending.
  const existing = rows.find((row) => row.sessionID === sessionID);
  if (existing) {
    throw new RequestOutcomeUnknownError(t("timeline.requestAwaitingConfirmation"), existing.clientRequestId);
  }
  let release: () => void = () => {};
  const flight = new Promise<void>((resolve) => { release = resolve; });
  active.set(sessionID, flight);
  try {
    writePending([...rows, pending]);
    await hoxiAPI.sendPromptAsync(sessionID, text, { ...options, goalActive: sessionGoal(sessionID)?.status === "active", clientRequestId: pending.clientRequestId });
  } catch (err) {
    if (err instanceof RequestOutcomeUnknownError) {
      rememberLastError(sessionID, err.message);
    } else {
      try { forgetPending(pending.clientRequestId, sessionID); } catch { /* retain identity on storage failure */ }
    }
    throw err;
  } finally {
    active.delete(sessionID);
    release();
    bumpPendingVersion();
  }
  // Acceptance is already confirmed. Cleanup failure must not report that
  // the command failed and invite a duplicate execution.
  try { forgetPending(pending.clientRequestId, sessionID); } catch { /* recover the retained receipt next time */ }
  return "sent";
}

export function forgetSessionRequests(sessionIDs: string[]): void {
  const removed = new Set(sessionIDs);
  for (const id of removed) lastErrorBySession.delete(id);
  try {
    writePending(readPending().filter((row) => !removed.has(row.sessionID)));
  } catch { /* leave recovery evidence intact; never prevent unrelated state cleanup */ }
}
