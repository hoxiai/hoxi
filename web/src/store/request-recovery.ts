// Only request identity and scope are durable here: never persist prompt or
// attachment bodies. Unconfirmed effects are inspected, never replayed on load.
import { hoxiAPI } from "../services/api";
import { createClientRequestID, recoverPromptRequest, RequestOutcomeUnknownError } from "../services/request-ledger";
import { t } from "../i18n";

const KEY = "hoxi.pending-prompts:v1";
const MAX_PENDING = 128;
const active = new Map<string, Promise<void>>();
interface PendingPrompt {
  sessionID: string;
  directory: string;
  clientRequestId: string;
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
}

function forgetPending(id: string): void {
  writePending(readPending().filter((row) => row.clientRequestId !== id));
}

// Called before any new send, not just the Retry button. A changed model or
// prompt must not bypass an unresolved request from this session.
export function hasPendingPrompt(sessionID: string): boolean {
  return active.has(sessionID) || readPending().some((row) => row.sessionID === sessionID);
}

export async function recoverPendingPrompt(sessionID: string): Promise<boolean> {
  // A business-error SSE can arrive before the submission response. Await
  // that response before deciding whether a semantic retry is safe.
  const inFlight = active.get(sessionID);
  if (inFlight) await inFlight;
  const pending = readPending().find((row) => row.sessionID === sessionID);
  if (!pending) return false;
  const outcome = await recoverPromptRequest(pending);
  forgetPending(pending.clientRequestId);
  return outcome === "completed";
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
  writePending([...rows, pending]);
  let release: () => void = () => {};
  active.set(sessionID, new Promise<void>((resolve) => { release = resolve; }));
  try {
    await hoxiAPI.sendPromptAsync(sessionID, text, { ...options, clientRequestId: pending.clientRequestId });
  } catch (err) {
    if (!(err instanceof RequestOutcomeUnknownError)) {
      try { forgetPending(pending.clientRequestId); } catch { /* retain identity on storage failure */ }
    }
    throw err;
  } finally {
    active.delete(sessionID);
    release();
  }
  // Acceptance is already confirmed. Cleanup failure must not report that
  // the command failed and invite a duplicate execution.
  try { forgetPending(pending.clientRequestId); } catch { /* recover the retained receipt next time */ }
  return "sent";
}

export function forgetSessionRequests(sessionIDs: string[]): void {
  const removed = new Set(sessionIDs);
  try {
    writePending(readPending().filter((row) => !removed.has(row.sessionID)));
  } catch { /* leave recovery evidence intact; never prevent unrelated state cleanup */ }
}
