// web/src/store/prompt-queue.ts
// Per-session queue of prompts typed while the agent is busy, plus dispatch
// bookkeeping. Pure state operations; sending is done by workspace-session-messages.
import type { SkillSelection } from "../services/api";
import { sessionAIConfig } from "./session-ai";
import { isSessionDeleted } from "./session-query";
import {
  setWorkspace,
  workspace,
  type ComposerDraftEdit,
  type QueuedPromptAttachment,
  type QueuedPromptItem,
  type SelectedModel,
} from "./workspace-state";

export interface EditingQueuedPromptState {
  item: QueuedPromptItem;
  index: number;
  mountedInComposer?: boolean;
  switchedFromAnotherQueuedItem?: boolean;
}

export interface LiveQueuedEditDraft {
  text: string;
  attachments: QueuedPromptAttachment[];
  skillSelection?: SkillSelection;
}

export interface StashedComposerDraft<TImage = any, TPaste = any> {
  text: string;
  images: TImage[];
  pastes: TPaste[];
  skillSelection?: SkillSelection | null;
}

const drainingBySession = new Set<string>();
const lastPromptDispatchAtBySession = new Map<string, number>();
const promptDispatchSeqBySession = new Map<string, number>();
const editingBySession = new Map<string, EditingQueuedPromptState>();
const stashedDraftBySession = new Map<string, StashedComposerDraft>();
let liveDraftGetter: ((sessionID: string) => LiveQueuedEditDraft | null) | null = null;
let activeCancelHandler: ((sessionID: string) => boolean) | null = null;

function cloneQueuedPromptItem(item: QueuedPromptItem): QueuedPromptItem {
  return {
    id: item.id,
    sessionID: item.sessionID,
    text: item.text,
    createdAt: item.createdAt,
    agent: item.agent,
    model: item.model ? { ...item.model } : undefined,
    effort: item.effort,
    attachments: (item.attachments ?? []).map((a) => ({ ...a })),
    ...(item.skillSelection ? { skillSelection: { ...item.skillSelection } } : {}),
    ...(item.system ? { system: item.system } : {}),
  };
}

export function registerQueuedEditDraftGetter(
  getter: ((sessionID: string) => LiveQueuedEditDraft | null) | null
) {
  liveDraftGetter = getter;
}

// Optimistic turns of prompts that were already on screen as queued bubbles.
// They take the bubble's place, so replaying the new-message fade-in would
// make text the reader was looking at blink out and back in.
const MAX_SHOWN_QUEUED_TURNS = 64;
const shownQueuedTurnIDs = new Set<string>();

export function markQueuedTurnShown(turnID: string) {
  if (shownQueuedTurnIDs.size >= MAX_SHOWN_QUEUED_TURNS) {
    const oldest = shownQueuedTurnIDs.values().next().value;
    if (oldest !== undefined) shownQueuedTurnIDs.delete(oldest);
  }
  shownQueuedTurnIDs.add(turnID);
}

export function wasQueuedTurnShown(turnID: string): boolean {
  return shownQueuedTurnIDs.has(turnID);
}

export function markSessionPromptDispatched(sessionID: string) {
  lastPromptDispatchAtBySession.set(sessionID, Date.now());
  promptDispatchSeqBySession.set(sessionID, (promptDispatchSeqBySession.get(sessionID) ?? 0) + 1);
}

export function lastPromptDispatchedAt(sessionID: string): number {
  return lastPromptDispatchAtBySession.get(sessionID) || 0;
}

export function promptDispatchSeq(sessionID: string): number {
  return promptDispatchSeqBySession.get(sessionID) || 0;
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
    system?: string;
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
    attachments: (options?.attachments ?? []).map((a) => ({ ...a })),
    ...(options?.skillSelection ? { skillSelection: { ...options.skillSelection } } : {}),
    ...(options?.system?.trim() ? { system: options.system.trim() } : {}),
  };
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  setWorkspace("queuedPromptsBySession", sessionID, [...current, item]);
  return item;
}

export function removeQueuedPrompt(sessionID: string, itemID: string) {
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  setWorkspace("queuedPromptsBySession", sessionID, current.filter((item) => item.id !== itemID));
}

export function hasEditingQueuedPrompt(sessionID?: string | null): boolean {
  return Boolean(sessionID && editingBySession.has(sessionID));
}

export function getEditingQueuedPrompt(
  sessionID?: string | null
): EditingQueuedPromptState | undefined {
  return sessionID ? editingBySession.get(sessionID) : undefined;
}

export function isQueuedPromptDrainBlocked(sessionID: string): boolean {
  const entry = editingBySession.get(sessionID);
  return Boolean(entry && entry.index <= 0);
}

export function takeEditingQueuedPrompt(
  sessionID: string
): EditingQueuedPromptState | undefined {
  const entry = editingBySession.get(sessionID);
  if (entry) editingBySession.delete(sessionID);
  return entry;
}

export function restoreEditingQueuedPrompt(
  sessionID: string,
  entry: EditingQueuedPromptState
) {
  if (!isSessionDeleted(sessionID)) {
    editingBySession.set(sessionID, entry);
  }
}

export function flushEditingQueuedPromptToQueue(
  sessionID: string,
  override?: LiveQueuedEditDraft | null
): QueuedPromptItem | null {
  const entry = editingBySession.get(sessionID);
  if (!entry) return null;
  editingBySession.delete(sessionID);
  if (workspace.composerDraftEditBySession[sessionID]?.id === entry.item.id) {
    setWorkspace("composerDraftEditBySession", sessionID, undefined);
  }
  const live = entry.mountedInComposer
    ? (override !== undefined ? override : liveDraftGetter?.(sessionID) ?? null)
    : null;
  const nextText = live ? live.text.trim() : "";
  const nextAttachments = live ? live.attachments : [];
  const nextSkill = live ? live.skillSelection : undefined;
  const hasLiveContent =
    Boolean(live) &&
    (nextText.length > 0 || nextAttachments.length > 0 || Boolean(nextSkill));
  const base = cloneQueuedPromptItem(entry.item);
  const restored: QueuedPromptItem = hasLiveContent
    ? {
        ...base,
        text: live!.text,
        attachments: nextAttachments.map((a) => ({ ...a })),
        ...(nextSkill ? { skillSelection: { ...nextSkill } } : {}),
      }
    : base;
  if (hasLiveContent && !nextSkill) {
    delete restored.skillSelection;
  }
  restoreQueuedPromptAt(sessionID, restored, entry.index);
  return restored;
}

export function cancelEditingQueuedPrompt(sessionID: string): QueuedPromptItem | null {
  const entry = editingBySession.get(sessionID);
  if (!entry) return null;
  editingBySession.delete(sessionID);
  if (workspace.composerDraftEditBySession[sessionID]?.id === entry.item.id) {
    setWorkspace("composerDraftEditBySession", sessionID, undefined);
  }
  const restored = cloneQueuedPromptItem(entry.item);
  restoreQueuedPromptAt(sessionID, restored, entry.index);
  return restored;
}

export function commitEditingQueuedPromptToQueue(
  sessionID: string,
  options: {
    text: string;
    agent?: string;
    model?: SelectedModel;
    effort?: QueuedPromptItem["effort"];
    attachments?: QueuedPromptAttachment[];
    skillSelection?: SkillSelection;
    system?: string;
  }
): QueuedPromptItem | null {
  const entry = editingBySession.get(sessionID);
  if (!entry) return null;
  editingBySession.delete(sessionID);
  const config = sessionAIConfig(sessionID);
  const model = options.model || entry.item.model || config.model;
  const sys = options.system?.trim() || entry.item.system?.trim();
  const updated: QueuedPromptItem = {
    id: entry.item.id,
    sessionID,
    text: options.text,
    createdAt: entry.item.createdAt,
    agent: options.agent || entry.item.agent || config.agent,
    model: model ? { ...model } : undefined,
    effort: options.effort ?? entry.item.effort ?? config.effort,
    attachments: (options.attachments ?? []).map((a) => ({ ...a })),
    ...(options.skillSelection ? { skillSelection: { ...options.skillSelection } } : {}),
    ...(sys ? { system: sys } : {}),
  };
  restoreQueuedPromptAt(sessionID, updated, entry.index);
  return updated;
}

export function editQueuedPrompt(sessionID: string, itemID: string) {
  const prevEditing = editingBySession.get(sessionID);
  const wasMountedInComposer = Boolean(prevEditing?.mountedInComposer);
  if (prevEditing) {
    flushEditingQueuedPromptToQueue(sessionID);
  }
  const current = workspace.queuedPromptsBySession[sessionID] ?? [];
  const targetIdx = current.findIndex((item) => item.id === itemID);
  if (targetIdx < 0) return;
  const target = cloneQueuedPromptItem(current[targetIdx]);
  editingBySession.set(sessionID, {
    item: target,
    index: targetIdx,
    mountedInComposer: false,
    switchedFromAnotherQueuedItem: wasMountedInComposer,
  });
  removeQueuedPrompt(sessionID, itemID);
  setWorkspace("composerDraftEditBySession", sessionID, {
    id: target.id,
    text: target.text,
    attachments: target.attachments.map((a) => ({ ...a })),
    skillSelection: target.skillSelection ? { ...target.skillSelection } : undefined,
    queuedEdit: true,
  });
}

export function beginComposerDraftEdit<TImage = any, TPaste = any>(
  sessionID: string,
  draft: ComposerDraftEdit,
  currentComposer: StashedComposerDraft<TImage, TPaste>
): { replaceComposer: boolean; priorDraftText: string } {
  consumeComposerDraftEdit(sessionID);
  if (draft.queuedEdit) {
    const editing = editingBySession.get(sessionID);
    const hasUnsentContent =
      currentComposer.text.trim().length > 0 ||
      currentComposer.images.length > 0 ||
      currentComposer.pastes.length > 0 ||
      Boolean(currentComposer.skillSelection);
    if (
      !editing?.switchedFromAnotherQueuedItem &&
      !stashedDraftBySession.has(sessionID) &&
      hasUnsentContent
    ) {
      stashedDraftBySession.set(sessionID, {
        text: currentComposer.text,
        images: [...currentComposer.images],
        pastes: [...currentComposer.pastes],
        skillSelection: currentComposer.skillSelection
          ? { ...currentComposer.skillSelection }
          : null,
      });
    }
    if (editing) {
      editing.mountedInComposer = true;
      editing.switchedFromAnotherQueuedItem = false;
    }
    return { replaceComposer: true, priorDraftText: "" };
  }
  if (editingBySession.has(sessionID)) {
    flushEditingQueuedPromptToQueue(sessionID);
    const stashed = popStashedComposerDraft<TImage, TPaste>(sessionID);
    return { replaceComposer: true, priorDraftText: stashed?.text ?? "" };
  }
  return { replaceComposer: false, priorDraftText: currentComposer.text };
}

export function popStashedComposerDraft<TImage = any, TPaste = any>(
  sessionID?: string | null
): StashedComposerDraft<TImage, TPaste> | undefined {
  if (!sessionID) return undefined;
  const stashed = stashedDraftBySession.get(sessionID);
  if (stashed) stashedDraftBySession.delete(sessionID);
  return stashed as StashedComposerDraft<TImage, TPaste> | undefined;
}

export function pushStashedComposerDraft<TImage = any, TPaste = any>(
  sessionID: string,
  draft: StashedComposerDraft<TImage, TPaste>
) {
  stashedDraftBySession.set(sessionID, draft);
}

export function cancelQueuedEditInComposer(sessionID?: string | null): boolean {
  if (!sessionID || !editingBySession.has(sessionID)) return false;
  if (activeCancelHandler) return activeCancelHandler(sessionID);
  cancelEditingQueuedPrompt(sessionID);
  return true;
}

export interface ComposerQueuedEditBridgeParams<TImage = any, TPaste = any> {
  sessionID: () => string | null;
  getText: () => string;
  setText: (text: string) => void;
  setCursorOffset: (offset: number) => void;
  getExpandedText: () => string;
  getPromptAttachments: () => QueuedPromptAttachment[];
  getImages: () => TImage[];
  replaceImages: (items: TImage[]) => void;
  restoreFromAttachments: (atts: QueuedPromptAttachment[]) => void;
  getPastes: () => TPaste[];
  replacePastes: (items: TPaste[]) => void;
  getSelectedSkill: () => SkillSelection | null;
  selectSkill: (
    sessionID: string | null,
    skill: SkillSelection | null,
    directory?: string
  ) => void;
  resetHistoryCursor: () => void;
  focusEnd: (len: number) => void;
  onDrain: (sessionID: string) => void;
}

export function bindComposerQueuedEditBridge<TImage = any, TPaste = any>(
  params: ComposerQueuedEditBridgeParams<TImage, TPaste>
) {
  const getter = (sid: string): LiveQueuedEditDraft | null =>
    sid === params.sessionID()
      ? {
          text: params.getExpandedText(),
          attachments: params.getPromptAttachments(),
          skillSelection: params.getSelectedSkill() ?? undefined,
        }
      : null;
  const applyInputs = (
    id: string | null,
    nextText: string,
    images: TImage[],
    pastes: TPaste[],
    skill: SkillSelection | null,
    directory?: string,
    focus = false
  ) => {
    params.resetHistoryCursor();
    params.setText(nextText);
    params.setCursorOffset(nextText.length);
    params.replacePastes(pastes);
    params.replaceImages(images);
    params.selectSkill(id, skill, directory);
    if (focus) params.focusEnd(nextText.length);
  };
  const cancelHandler = (sid: string): boolean => {
    if (!editingBySession.has(sid)) return false;
    cancelEditingQueuedPrompt(sid);
    const stashed = popStashedComposerDraft<TImage, TPaste>(sid);
    if (sid === params.sessionID()) {
      applyInputs(
        sid,
        stashed?.text ?? "",
        stashed?.images ?? [],
        stashed?.pastes ?? [],
        stashed?.skillSelection ?? null,
        undefined,
        true
      );
    }
    params.onDrain(sid);
    return true;
  };
  liveDraftGetter = getter;
  activeCancelHandler = cancelHandler;

  return {
    syncDraftEdit: () => {
      const id = params.sessionID();
      if (!id) return;
      const draft = workspace.composerDraftEditBySession[id];
      if (!draft) return;
      const { replaceComposer, priorDraftText } = beginComposerDraftEdit(id, draft, {
        text: params.getText(),
        images: params.getImages(),
        pastes: params.getPastes(),
        skillSelection: params.getSelectedSkill(),
      });
      params.resetHistoryCursor();
      const nextText =
        priorDraftText.trim() && draft.text
          ? `${priorDraftText}\n${draft.text}`
          : draft.text || priorDraftText;
      if (replaceComposer) {
        params.replacePastes([]);
        params.replaceImages([]);
        params.selectSkill(id, draft.skillSelection ?? null);
      } else if (draft.skillSelection) {
        params.selectSkill(id, draft.skillSelection);
      }
      params.setText(nextText);
      params.setCursorOffset(nextText.length);
      if (draft.attachments?.length) {
        params.restoreFromAttachments(draft.attachments);
      }
      params.focusEnd(nextText.length);
    },
    popStashedBeforeSend: (draftSessionID: string | null) =>
      draftSessionID && hasEditingQueuedPrompt(draftSessionID)
        ? popStashedComposerDraft<TImage, TPaste>(draftSessionID)
        : undefined,
    applyAfterClear: (
      draftSessionID: string | null,
      directory: string,
      isCurrent: boolean,
      stashed?: StashedComposerDraft<TImage, TPaste>
    ) => {
      if (isCurrent) {
        applyInputs(
          draftSessionID,
          stashed?.text ?? "",
          stashed?.images ?? [],
          stashed?.pastes ?? [],
          stashed?.skillSelection ?? null,
          directory,
          false
        );
      } else {
        params.selectSkill(draftSessionID, stashed?.skillSelection ?? null, directory);
      }
    },
    rollbackStashedOnError: (
      restoreID: string | null,
      isCurrent: boolean,
      stashed?: StashedComposerDraft<TImage, TPaste>
    ) => {
      if (!stashed || !restoreID) return false;
      pushStashedComposerDraft(restoreID, stashed);
      if (isCurrent) {
        params.setText("");
        params.replacePastes([]);
        params.replaceImages([]);
      }
      return true;
    },
    dispose: () => {
      if (liveDraftGetter === getter) liveDraftGetter = null;
      if (activeCancelHandler === cancelHandler) activeCancelHandler = null;
    },
  };
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
    promptDispatchSeqBySession.delete(id);
    editingBySession.delete(id);
    stashedDraftBySession.delete(id);
  }
}
