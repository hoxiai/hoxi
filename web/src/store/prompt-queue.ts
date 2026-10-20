// web/src/store/prompt-queue.ts
// Per-session queue of prompts typed while the agent is busy, plus dispatch
// bookkeeping. Pure state operations; sending is done by workspace-session-messages.
import type { SkillSelection } from "../services/api";
import { sessionAIConfig } from "./session-ai";
import { isSessionDeleted } from "./session-query";
import {
  setWorkspace,
  workspace,
  type QueuedPromptAttachment,
  type QueuedPromptItem,
  type SelectedModel,
} from "./workspace-state";

const drainingBySession = new Set<string>();
const lastPromptDispatchAtBySession = new Map<string, number>();

export function markSessionPromptDispatched(sessionID: string) {
  lastPromptDispatchAtBySession.set(sessionID, Date.now());
}

export function lastPromptDispatchedAt(sessionID: string): number {
  return lastPromptDispatchAtBySession.get(sessionID) || 0;
}

export function enqueueSessionPrompt(
  sessionID: string,
  text: string,
  options?: {
    agent?: string;
    model?: SelectedModel;
    effort?: QueuedPromptItem["effort"];
    attachments?: QueuedPromptAttachment[];
    skillSelection?: SkillSelection;
  }
): QueuedPromptItem {
  const config = sessionAIConfig(sessionID);
  const model = options?.model || config.model;
  const item: QueuedPromptItem = {
    id: `q_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
    sessionID,
    text,
    createdAt: Date.now(),
    agent: options?.agent || config.agent,
    model: model ? { ...model } : undefined,
    effort: options?.effort ?? config.effort,
    attachments: options?.attachments ?? [],
    ...(options?.skillSelection ? { skillSelection: { ...options.skillSelection } } : {}),
  };
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  setWorkspace("queuedPromptsBySession", sessionID, [...current, item]);
  return item;
}

export function removeQueuedPrompt(sessionID: string, itemID: string) {
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  setWorkspace("queuedPromptsBySession", sessionID, current.filter((item) => item.id !== itemID));
}

export function editQueuedPrompt(sessionID: string, itemID: string) {
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  const target = current.find((item) => item.id === itemID);
  if (!target) return;
  removeQueuedPrompt(sessionID, itemID);
  setWorkspace("composerDraftEditBySession", sessionID, {
    id: target.id,
    text: target.text,
    attachments: target.attachments,
    skillSelection: target.skillSelection,
  });
}

export function consumeComposerDraftEdit(sessionID: string) {
  setWorkspace("composerDraftEditBySession", sessionID, undefined);
}

export function restoreQueuedPromptAt(sessionID: string, item: QueuedPromptItem, index: number) {
  if (isSessionDeleted(sessionID)) return;
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  if (current.some((existing) => existing.id === item.id)) return;
  const next = current.slice();
  const clamped = Math.max(0, Math.min(index, next.length));
  next.splice(clamped, 0, item);
  setWorkspace("queuedPromptsBySession", sessionID, next);
}

/** Marks a queue drain as in flight; returns false when one is already running. */
export function beginQueueDrain(sessionID: string): boolean {
  if (drainingBySession.has(sessionID)) return false;
  drainingBySession.add(sessionID);
  return true;
}

export function endQueueDrain(sessionID: string): void {
  drainingBySession.delete(sessionID);
}

export function forgetPromptQueueState(sessionIDs: string[]): void {
  for (const id of sessionIDs) {
    drainingBySession.delete(id);
    lastPromptDispatchAtBySession.delete(id);
  }
}
