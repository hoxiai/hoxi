// web/src/store/question-answers.ts
import { createSignal } from "solid-js";
import type { QuestionInfo, QuestionRequest, ToolPart } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import { t } from "../i18n";
import {
  explicitSessionDirectory,
  isActionableApprovalSession,
  isDirectoryInScopes,
  permissionSyncScopes,
} from "./approval-mode";
import { sessionDirectory } from "./session-query";
import { resolveRootSessionID } from "./subagent-sessions";
import { normalizeFolderPath } from "./workspace-folders";
import { setWorkspace, workspace } from "./workspace-state";

/**
 * Records `question.asked` / `question.replied` / `question.rejected` payloads so the
 * timeline `question` tool card can render "what was asked + what the user picked"
 * even when the upstream tool output does not carry structured answers.
 */
export interface QuestionReplyRecord {
  requestID: string;
  sessionID: string;
  callID?: string;
  questions: QuestionInfo[];
  answers?: string[][];
  rejected?: boolean;
  updatedAt: number;
}

export interface QuestionOptionView {
  label: string;
  description: string;
  selected: boolean;
}

export interface QuestionItemView {
  question: string;
  header: string;
  multiple: boolean;
  options: QuestionOptionView[];
  customAnswers: string[];
  answers: string[];
  answered: boolean;
}

const repliesByRequestID = new Map<string, QuestionReplyRecord>();
const requestIDByCallID = new Map<string, string>();
const MAX_QUESTION_RECORDS = 200;
// The records live in plain Maps; this version lets cards re-read them when a
// question is asked, answered or skipped.
const [recordsVersion, setRecordsVersion] = createSignal(0);
const bumpRecords = () => setRecordsVersion((v) => v + 1);

function trimQuestionRecords(): void {
  while (repliesByRequestID.size > MAX_QUESTION_RECORDS) {
    const oldest = repliesByRequestID.keys().next().value;
    if (oldest === undefined) break;
    const record = repliesByRequestID.get(oldest);
    repliesByRequestID.delete(oldest);
    if (record?.callID) requestIDByCallID.delete(record.callID);
  }
}

function toolCallID(request: QuestionRequest): string | undefined {
  const tool = request.tool as { callID?: string; id?: string } | undefined;
  return tool?.callID ?? tool?.id;
}

export function rememberQuestionRequest(request: QuestionRequest): void {
  if (!request?.id) return;
  const callID = toolCallID(request);
  repliesByRequestID.set(request.id, {
    requestID: request.id,
    sessionID: request.sessionID,
    callID,
    questions: request.questions ?? [],
    updatedAt: Date.now(),
  });
  if (callID) requestIDByCallID.set(callID, request.id);
  trimQuestionRecords();
  bumpRecords();
}

export function rememberQuestionReply(
  requestID: string,
  sessionID: string,
  answers: string[][]
): void {
  if (!requestID) return;
  const existing = repliesByRequestID.get(requestID);
  repliesByRequestID.set(requestID, {
    requestID,
    sessionID: sessionID || existing?.sessionID || "",
    callID: existing?.callID,
    questions: existing?.questions ?? [],
    answers,
    rejected: false,
    updatedAt: Date.now(),
  });
  trimQuestionRecords();
  bumpRecords();
}

export function rememberQuestionReject(requestID: string, sessionID: string): void {
  if (!requestID) return;
  const existing = repliesByRequestID.get(requestID);
  repliesByRequestID.set(requestID, {
    requestID,
    sessionID: sessionID || existing?.sessionID || "",
    callID: existing?.callID,
    questions: existing?.questions ?? [],
    rejected: true,
    updatedAt: Date.now(),
  });
  trimQuestionRecords();
  bumpRecords();
}

function dropPendingQuestion(requestID: string): void {
  setWorkspace("pendingQuestions", (prev) => prev.filter((q) => q.id !== requestID));
}

function isQuestionGoneError(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err ?? "");
  return /\bHTTP\s+(400|404)\b/i.test(msg) || /not\s+found|already\s+answered/i.test(msg);
}

function resolveQuestionRequestDirectory(request: QuestionRequest): string | undefined {
  const explicit = typeof request.directory === "string" ? request.directory.trim() : "";
  return explicit || sessionDirectory(request.sessionID);
}

/** Sends the user's answers for a docked question and records them for the tool card. */
export async function replyQuestionRequest(request: QuestionRequest, answers: string[][]): Promise<void> {
  try {
    await hoxiAPI.replyQuestion(request.id, answers, resolveQuestionRequestDirectory(request));
  } catch (err) {
    if (isQuestionGoneError(err)) {
      dropPendingQuestion(request.id);
      return;
    }
    throw err;
  }
  rememberQuestionReply(request.id, request.sessionID, answers);
  dropPendingQuestion(request.id);
}

export async function rejectQuestionRequest(request: QuestionRequest): Promise<void> {
  try {
    await hoxiAPI.rejectQuestion(request.id, resolveQuestionRequestDirectory(request));
  } catch (err) {
    if (isQuestionGoneError(err)) {
      dropPendingQuestion(request.id);
      return;
    }
    throw err;
  }
  rememberQuestionReject(request.id, request.sessionID);
  dropPendingQuestion(request.id);
}

export function findPendingQuestionForPart(part: ToolPart): QuestionRequest | undefined {
  if (part.state.status !== "pending" && part.state.status !== "running") {
    return undefined;
  }
  const pending = workspace.pendingQuestions;
  if (!pending.length) return undefined;
  if (part.callID) {
    const byCall = pending.find((q) => toolCallID(q) === part.callID);
    if (byCall) return byCall;
  }
  return pending.find(
    (q) =>
      (!part.callID || !toolCallID(q)) &&
      (q.sessionID === part.sessionID || q.subagentSessionID === part.sessionID)
  );
}

function resolveQuestionDirectory(req: QuestionRequest): string | undefined {
  const cleanExplicit = normalizeFolderPath(
    typeof req.directory === "string" ? req.directory : undefined
  );
  if (cleanExplicit) return cleanExplicit;
  const rootID = resolveRootSessionID(req.sessionID) || req.sessionID;
  const knownSessionDir =
    explicitSessionDirectory(req.sessionID) ||
    explicitSessionDirectory(req.subagentSessionID) ||
    explicitSessionDirectory(rootID);
  if (knownSessionDir) return knownSessionDir;
  const sessionDir = normalizeFolderPath(sessionDirectory(req.sessionID));
  if (sessionDir) return sessionDir;
  const indexed =
    workspace.sessionIndex[req.sessionID] ||
    (req.subagentSessionID ? workspace.sessionIndex[req.subagentSessionID] : undefined) ||
    workspace.sessionIndex[rootID];
  if (indexed) {
    if (indexed.projectID && workspace.activeProjectID && indexed.projectID !== workspace.activeProjectID) {
      return undefined;
    }
    if (!indexed.projectID && !workspace.sessions.some((s) => s.id === req.sessionID || s.id === rootID)) {
      return undefined;
    }
  }
  return normalizeFolderPath(workspace.rootDir) || undefined;
}

export async function syncPendingQuestions(directory?: string): Promise<void> {
  const scopes = permissionSyncScopes(directory);
  try {
    const results = await Promise.allSettled(
      scopes.map((scope) => hoxiAPI.listQuestions(scope))
    );
    const merged = new Map<string, QuestionRequest>();
    const answeredScopes = new Set<string>();
    results.forEach((res, index) => {
      if (res.status !== "fulfilled" || !Array.isArray(res.value)) return;
      answeredScopes.add(scopes[index]);
      for (const req of res.value) {
        if (req?.id && isActionableApprovalSession(req.sessionID)) {
          merged.set(req.id, req);
        }
      }
    });
    if (answeredScopes.size === 0) return;
    const remoteIDs = new Set(merged.keys());
    for (const req of merged.values()) {
      rememberQuestionRequest(req);
    }
    setWorkspace("pendingQuestions", (prev) => {
      const kept = prev.filter(
        (q) =>
          isActionableApprovalSession(q?.sessionID) &&
          (remoteIDs.has(q.id) ||
            !isDirectoryInScopes(resolveQuestionDirectory(q), answeredScopes))
      );
      const existingIDs = new Set(kept.map((q) => q.id));
      const added: QuestionRequest[] = [];
      for (const req of merged.values()) {
        if (!existingIDs.has(req.id)) {
          added.push(req);
        }
      }
      if (added.length === 0 && kept.length === prev.length) return prev;
      return [...kept, ...added];
    });
  } catch {
    /* ignore if endpoint not ready */
  }
}

export function findQuestionReply(callID?: string): QuestionReplyRecord | undefined {
  recordsVersion();
  if (!callID) return undefined;
  const requestID = requestIDByCallID.get(callID);
  return requestID ? repliesByRequestID.get(requestID) : undefined;
}

export function clearQuestionRecords(): void {
  repliesByRequestID.clear();
  requestIDByCallID.clear();
  bumpRecords();
}

function normalizeAnswers(raw: unknown): string[][] | undefined {
  if (!Array.isArray(raw)) return undefined;
  return raw.map((item) => {
    if (Array.isArray(item)) return item.map((v) => String(v));
    if (item === undefined || item === null) return [];
    return [String(item)];
  });
}

function metadataAnswers(part: ToolPart): string[][] | undefined {
  const meta = part.state.status === "pending" ? undefined : part.state.metadata;
  return normalizeAnswers(meta?.answers);
}

function outputJSONAnswers(part: ToolPart): string[][] | undefined {
  if (part.state.status !== "completed") return undefined;
  const raw = part.state.output;
  if (typeof raw !== "string" || !raw.trim().startsWith("{")) return undefined;
  try {
    const parsed = JSON.parse(raw) as { answers?: unknown };
    return normalizeAnswers(parsed?.answers);
  } catch {
    return undefined;
  }
}

// Fallback for tools that only emit the model-facing text:
// `User has answered your questions: "q1"="a1, a2", "q2"="Unanswered"`
function outputTextAnswers(part: ToolPart, questions: QuestionInfo[]): string[][] | undefined {
  if (part.state.status !== "completed") return undefined;
  const raw = part.state.output;
  if (typeof raw !== "string" || !raw.includes('"="')) return undefined;
  const pairs: Array<{ question: string; answer: string }> = [];
  const regex = /"((?:[^"\\]|\\.)*)"\s*=\s*"((?:[^"\\]|\\.)*)"/g;
  let match: RegExpExecArray | null;
  while ((match = regex.exec(raw)) !== null) {
    pairs.push({
      question: match[1].replace(/\\"/g, '"').replace(/\\n/g, " "),
      answer: match[2].replace(/\\"/g, '"').trim(),
    });
  }
  if (pairs.length === 0) return undefined;
  return questions.map((q) => {
    const hit =
      pairs.find((p) => p.question === q.question) ??
      pairs.find((p) => q.question.includes(p.question) || p.question.includes(q.question));
    if (!hit || !hit.answer || hit.answer === "Unanswered") return [];
    return hit.answer.split(",").map((s) => s.trim()).filter(Boolean);
  });
}

function resolveAnswerList(
  part: ToolPart,
  questions: QuestionInfo[],
  reply?: QuestionReplyRecord
): string[][] {
  return (
    reply?.answers ??
    metadataAnswers(part) ??
    outputJSONAnswers(part) ??
    outputTextAnswers(part, questions) ??
    questions.map(() => [])
  );
}

function normalizeQuestion(raw: any): QuestionInfo {
  const options = Array.isArray(raw?.options)
    ? raw.options.map((opt: any) => ({
        label: String(opt?.label ?? ""),
        description: String(opt?.description ?? ""),
      }))
    : [];
  return {
    question: String(raw?.question ?? ""),
    header: String(raw?.header ?? ""),
    options,
    multiple: Boolean(raw?.multiple),
  };
}

export function resolveQuestionItems(
  part: ToolPart,
  reply?: QuestionReplyRecord
): QuestionItemView[] {
  const input = part.state.input as { questions?: unknown[] } | undefined;
  const rawQuestions = Array.isArray(input?.questions) ? input!.questions : [];
  const questions = rawQuestions.map(normalizeQuestion);
  const source = reply?.questions?.length ? reply.questions : questions;
  if (source.length === 0) return [];
  const answerList = resolveAnswerList(part, source, reply);
  return source.map((q, idx) => {
    const answers = answerList[idx] ?? [];
    const labels = new Set(q.options.map((opt) => opt.label));
    return {
      question: q.question,
      header: q.header,
      multiple: Boolean(q.multiple),
      options: q.options.map((opt) => ({ ...opt, selected: answers.includes(opt.label) })),
      customAnswers: answers.filter((a) => !labels.has(a)),
      answers,
      answered: answers.length > 0,
    };
  });
}

/** Collapsed-header summary: chosen answers once answered, otherwise the question itself. */
export function questionSummaryText(part: ToolPart, reply?: QuestionReplyRecord): string {
  const items = resolveQuestionItems(part, reply);
  if (items.length === 0) return "";
  const picked = items.flatMap((item) => item.answers);
  if (picked.length > 0) {
    return `${t("toolCard.questionSelectedSummary", { answers: picked.join(", ") })}`;
  }
  return items[0].question || items[0].header;
}
