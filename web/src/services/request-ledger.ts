// web/src/services/request-ledger.ts
import type {
  CommitAndPushResponse,
  RequestInspectKind,
  RequestInspectResponse,
} from "./api-types";
import type { SnapshotFileDiff } from "./sse-types";
import { requestJSON, withDir } from "./http";

const HTTP_STATUS_BODY_RE = /^HTTP (\d{3}):\s*([\s\S]*)$/;

export function createClientRequestID(prefix = "req"): string {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`;
}

function parseCodedHTTPError(err: unknown): {
  httpStatus: number;
  code?: string;
  message?: string;
} | null {
  const raw = err instanceof Error ? err.message.trim() : typeof err === "string" ? err.trim() : "";
  const match = HTTP_STATUS_BODY_RE.exec(raw);
  if (!match) return null;
  const httpStatus = Number(match[1]);
  const body = match[2].trim();
  if (!body) return { httpStatus };
  try {
    const parsed = JSON.parse(body) as Record<string, unknown>;
    return {
      httpStatus,
      code: typeof parsed?.code === "string" ? parsed.code : undefined,
      message: typeof parsed?.error === "string" ? parsed.error : undefined,
    };
  } catch {
    return { httpStatus };
  }
}

export function shouldInspectAfterFailure(err: unknown): boolean {
  const coded = parseCodedHTTPError(err);
  if (!coded) {
    // No HTTP status means fetch() failed at transport level (disconnect, reset, timeout).
    return true;
  }
  if (coded.httpStatus === 502 || coded.httpStatus === 503 || coded.httpStatus === 504) {
    return true;
  }
  return (
    coded.httpStatus === 409 &&
    (coded.code === "outcome_unknown" || coded.code === "request_in_progress")
  );
}

export function inspectHoxiRequest<T = unknown>(input: {
  clientRequestId: string;
  kind: RequestInspectKind;
  scope: string;
  directory?: string;
  digest?: string;
  turnResult?: boolean;
}): Promise<RequestInspectResponse<T>> {
  const params = new URLSearchParams({
    kind: input.kind,
    scope: input.scope,
  });
  if (input.directory) params.set("directory", input.directory);
  if (input.digest) params.set("digest", input.digest);
  if (input.turnResult) params.set("turnResult", "1");
  return requestJSON<RequestInspectResponse<T>>(
    `/api/hoxi/requests/${encodeURIComponent(input.clientRequestId)}?${params.toString()}`
  );
}

async function probeInspectWithReconnectRetry<T = unknown>(input: {
  clientRequestId: string;
  kind: RequestInspectKind;
  scope: string;
  directory?: string;
}): Promise<RequestInspectResponse<T> | null> {
  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      const insp = await inspectHoxiRequest<T>(input);
      if (insp.status === "admitted" && attempt === 0) {
        await new Promise((r) => setTimeout(r, 250));
        continue;
      }
      return insp;
    } catch {
      if (attempt === 0) {
        await new Promise((r) => setTimeout(r, 250));
      }
    }
  }
  return null;
}

export class RequestOutcomeUnknownError extends Error {
  readonly code = "outcome_unknown";
  readonly clientRequestId: string;
  readonly evidence?: Record<string, unknown>;

  constructor(message: string, clientRequestId: string, evidence?: Record<string, unknown>) {
    super(message);
    this.name = "RequestOutcomeUnknownError";
    this.clientRequestId = clientRequestId;
    this.evidence = evidence;
  }
}

function buildOutcomeUnknownError(insp: RequestInspectResponse): RequestOutcomeUnknownError {
  const ev = insp.evidence;
  const headAdvanced = ev?.headAdvanced === true;
  const treeClean = ev?.workingTreeClean === true;
  let detail =
    insp.errorMessage?.trim() ||
    "Request was interrupted before completion; outcome requires confirmation.";
  if (insp.kind === "worktree_commit" && headAdvanced && treeClean) {
    detail += " (Host Git HEAD already advanced and working tree is clean; refresh status to confirm.)";
  }
  return new RequestOutcomeUnknownError(detail, insp.clientRequestId, ev);
}

export async function recoverPromptRequest(input: {
  clientRequestId: string;
  sessionID: string;
  directory?: string;
}): Promise<"completed"> {
  const insp = await probeInspectWithReconnectRetry({
    clientRequestId: input.clientRequestId,
    kind: "prompt",
    scope: input.sessionID,
    directory: input.directory,
  });
  if (insp && (insp.kind !== "prompt" || insp.scope !== input.sessionID || insp.clientRequestId !== input.clientRequestId)) {
    throw new RequestOutcomeUnknownError("Request outcome did not match the original request", input.clientRequestId);
  }
  if (insp?.status === "completed") return "completed";
  // Absence is not a rejection: this read can overtake a delayed POST, or its
  // receipt can have expired. Never turn an ambiguous send into a fresh ID.
  if (insp) throw buildOutcomeUnknownError(insp);
  throw new RequestOutcomeUnknownError(
    "Could not confirm whether the request was admitted; inspect again before retrying.",
    input.clientRequestId
  );
}

export async function commitWorktreeWithRecovery(
  sessionID: string,
  message: string,
  opts?: {
    push?: boolean;
    directory?: string;
    sessionDiffs?: SnapshotFileDiff[];
    clientRequestId?: string;
  }
): Promise<CommitAndPushResponse> {
  const clientRequestId = opts?.clientRequestId?.trim() || createClientRequestID("commit");
  const normalizedSid = sessionID.trim();
  const scope =
    normalizedSid && normalizedSid !== "__root__"
      ? normalizedSid
      : opts?.directory?.trim()
        ? `dir:${opts.directory.trim()}`
        : "__root__";

  try {
    return await requestJSON<CommitAndPushResponse>("/api/hoxi/worktree/commit", {
      method: "POST",
      headers: { "X-Idempotency-Key": clientRequestId },
      body: JSON.stringify({
        sessionID,
        message,
        clientRequestId,
        ...(opts?.push ? { push: true } : {}),
        ...(opts?.directory ? { directory: opts.directory } : {}),
        ...(opts?.sessionDiffs !== undefined ? { sessionDiffs: opts.sessionDiffs } : {}),
      }),
    });
  } catch (err) {
    if (!shouldInspectAfterFailure(err)) throw err;
    const insp = await probeInspectWithReconnectRetry<CommitAndPushResponse>({
      clientRequestId,
      kind: "worktree_commit",
      scope,
      directory: opts?.directory,
    });
    if (insp?.status === "completed" && insp.result) {
      return {
        ...insp.result,
        clientRequestId,
        deduplicated: true,
      };
    }
    if (insp?.status === "outcome_unknown") {
      throw buildOutcomeUnknownError(insp);
    }
    throw err;
  }
}

export async function replyPermissionWithRecovery(
  requestID: string,
  reply: "once" | "always" | "reject",
  directory?: string,
  message?: string,
  grantClass?: boolean,
  explicitClientRequestId?: string
): Promise<void> {
  const clientRequestId = explicitClientRequestId?.trim() || createClientRequestID("perm");
  try {
    await requestJSON<void>(withDir(`/api/hoxi/permission/${encodeURIComponent(requestID)}/reply`, directory), {
      method: "POST",
      headers: { "X-Idempotency-Key": clientRequestId },
      body: JSON.stringify({
        reply,
        ...(message ? { message } : {}),
        ...(grantClass ? { grantClass: true } : {}),
        clientRequestId,
      }),
    });
  } catch (err) {
    if (!shouldInspectAfterFailure(err)) throw err;
    const insp = await probeInspectWithReconnectRetry({
      clientRequestId,
      kind: "permission_reply",
      scope: requestID,
      directory,
    });
    if (insp?.status === "completed") {
      return;
    }
    if (insp?.status === "outcome_unknown") {
      throw buildOutcomeUnknownError(insp);
    }
    throw err;
  }
}
