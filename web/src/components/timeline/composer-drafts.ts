// web/src/components/timeline/composer-drafts.ts
import { type Accessor, createComputed, createSignal, onCleanup, type Setter, untrack } from "solid-js";
import type { PromptAttachment } from "../../services/api";
import {
  drainNextQueuedPrompt,
  flushEditingQueuedPromptToQueue,
  getComposerSkill,
  hasEditingQueuedPrompt,
  normalizeFolderPath,
  popStashedComposerDraft,
  selectComposerSkill,
  workspace,
} from "../../store/workspace";

export type AttachmentKind = "image" | "pdf" | "spreadsheet" | "document" | "text";

export interface ImageItem {
  id: string;
  filename: string;
  mime: string;
  dataUrl: string;
  previewUrl?: string;
  size?: number;
  kind?: AttachmentKind;
}

export interface CollapsedPasteItem {
  id: string;
  lines: number;
  chars: number;
  preview: string;
  content: string;
}

export interface SessionComposerDraft {
  text: string;
  images: ImageItem[];
  pastes: CollapsedPasteItem[];
  notice?: string | null;
}

const draftResetEpochByKey = new Map<string, number>();
export const getDraftEpoch = (key: string) => draftResetEpochByKey.get(key) ?? 0;
export const bumpDraftEpoch = (key: string) => {
  draftResetEpochByKey.set(key, getDraftEpoch(key) + 1);
};

export function composerDraftKey(
  sessionID?: string | null,
  directory?: string | null
): string {
  if (sessionID) return sessionID;
  const clean = normalizeFolderPath(directory ?? workspace.rootDir);
  return clean ? `__new__:${clean}` : "__new__";
}

const MAX_SESSION_DRAFTS = 64;
export const sessionDrafts = new Map<string, SessionComposerDraft>();

export function setSessionDraft(key: string, draft: SessionComposerDraft) {
  if (sessionDrafts.has(key)) sessionDrafts.delete(key);
  sessionDrafts.set(key, draft);
  while (sessionDrafts.size > MAX_SESSION_DRAFTS) {
    const oldest = sessionDrafts.keys().next().value;
    if (oldest === undefined) break;
    sessionDrafts.delete(oldest);
  }
}

/**
 * Ported from kilocode usePasteCollapse.ts:
 * Collapses large multi-line pastes (>= 10 lines or >= 600 chars) into compact chips
 * above the Composer so pasting stack traces or code doesn't flood the input box.
 */
export function createPasteCollapse() {
  const [pastes, setPastes] = createSignal<CollapsedPasteItem[]>([]);

  const handleTextPaste = (ev: ClipboardEvent): boolean => {
    const raw = ev.clipboardData?.getData("text/plain") ?? "";
    if (!raw) return false;
    const lineList = raw.split(/\r\n|\n|\r/);
    if (lineList.length < 10 && raw.length < 600) {
      return false;
    }
    ev.preventDefault();
    const firstNonEmpty =
      lineList.map((l) => l.trim()).find((l) => l.length > 0) || "Pasted text";
    setPastes((prev) => [
      ...prev,
      {
        id: `pst_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`,
        lines: lineList.length,
        chars: raw.length,
        preview: firstNonEmpty.slice(0, 48),
        content: raw,
      },
    ]);
    return true;
  };

  const removePaste = (id: string) => {
    setPastes((prev) => prev.filter((p) => p.id !== id));
  };

  const clearPastes = () => setPastes([]);
  const replacePastes = (items: CollapsedPasteItem[]) => setPastes(items);

  const expandIntoPrompt = (typedText: string): string => {
    const blocks = pastes().map((p) => p.content.trim());
    if (blocks.length === 0) return typedText.trim();
    const joinedBlocks = blocks.join("\n\n");
    return typedText.trim()
      ? `${joinedBlocks}\n\n${typedText.trim()}`
      : joinedBlocks;
  };

  return {
    pastes,
    handleTextPaste,
    removePaste,
    clearPastes,
    replacePastes,
    expandIntoPrompt,
  };
}

export interface DraftImageAttachmentsBridge {
  images: Accessor<ImageItem[]>;
  notice: Accessor<string | null>;
  setNotice: (val: string | null) => void;
  replaceImages: (items: ImageItem[]) => void;
  toPromptAttachments: () => PromptAttachment[];
}

export function createSessionDraftSync(params: {
  sessionID: Accessor<string | null>;
  directory?: Accessor<string>;
  enabled?: Accessor<boolean>;
  resetRequest?: Accessor<number>;
  text: Accessor<string>;
  setText: Setter<string>;
  imgAttach: DraftImageAttachmentsBridge;
  pasteCollapse: ReturnType<typeof createPasteCollapse>;
  onAfterRestore: () => void;
}) {
  let activeSessionID = params.sessionID();
  let activeDir = normalizeFolderPath(params.directory?.() ?? workspace.rootDir);
  let activeKey = composerDraftKey(activeSessionID, activeDir);
  let wasEnabled = params.enabled ? params.enabled() : true;
  let resetRequest = params.resetRequest?.();
  let disposed = false;
  const saveOutgoingDraft = () => {
    if (activeSessionID && hasEditingQueuedPrompt(activeSessionID)) {
      flushEditingQueuedPromptToQueue(activeSessionID, {
        text: params.pasteCollapse.expandIntoPrompt(params.text()),
        attachments: params.imgAttach.toPromptAttachments(),
        skillSelection: getComposerSkill(activeSessionID, activeDir) ?? undefined,
      });
      const stashed = popStashedComposerDraft<ImageItem, CollapsedPasteItem>(activeSessionID);
      selectComposerSkill(activeSessionID, stashed?.skillSelection ?? null, undefined, activeDir);
      if (stashed && (stashed.text.trim() || stashed.images.length || stashed.pastes.length)) {
        setSessionDraft(activeKey, { text: stashed.text, images: stashed.images, pastes: stashed.pastes, notice: null });
      } else {
        sessionDrafts.delete(activeKey);
      }
      void drainNextQueuedPrompt(activeSessionID);
      return;
    }
    const draft = {
      text: params.text(),
      images: params.imgAttach.images(),
      pastes: params.pasteCollapse.pastes(),
      notice: params.imgAttach.notice(),
    };
    if (draft.text.trim() || draft.images.length || draft.pastes.length || draft.notice) {
      setSessionDraft(activeKey, draft);
    } else {
      sessionDrafts.delete(activeKey);
    }
  };
  onCleanup(() => {
    if (wasEnabled) saveOutgoingDraft();
    disposed = true;
  });
  const isCurrentTarget = (sessionID: string | null, directory: string) =>
    !disposed && composerDraftKey(params.sessionID(), params.directory?.()) === composerDraftKey(sessionID, directory);

  const sync = () => {
    const nowEnabled = params.enabled ? params.enabled() : true;
    if (!nowEnabled) {
      if (wasEnabled) {
        untrack(() => saveOutgoingDraft());
        wasEnabled = false;
      }
      return;
    }
    const nextSessionID = params.sessionID();
    const nextDir = normalizeFolderPath(params.directory?.() ?? workspace.rootDir);
    const nextKey = composerDraftKey(nextSessionID, nextDir);
    const request = params.resetRequest?.();
    const reset = request !== resetRequest;
    resetRequest = request;
    const justEnabled = !wasEnabled;
    wasEnabled = true;
    if (nextKey === activeKey && !justEnabled) {
      if (reset) params.onAfterRestore();
      return;
    }

    untrack(() => {
      if (!justEnabled) {
        saveOutgoingDraft();
      }

      // If the user typed in the rootless "__new__" state before choosing their
      // first folder, carry that unsent draft into the newly selected workspace.
      if (
        !justEnabled &&
        !activeSessionID &&
        !activeDir &&
        !nextSessionID &&
        nextDir &&
        !sessionDrafts.has(nextKey) &&
        sessionDrafts.has(activeKey)
      ) {
        setSessionDraft(nextKey, sessionDrafts.get(activeKey)!);
        sessionDrafts.delete(activeKey);
      }

      activeKey = nextKey;
      activeSessionID = nextSessionID;
      activeDir = nextDir;
      const saved = sessionDrafts.get(nextKey);
      params.setText(saved?.text ?? "");
      params.imgAttach.replaceImages(saved?.images ?? []);
      params.pasteCollapse.replacePastes(saved?.pastes ?? []);
      params.imgAttach.setNotice(saved?.notice ?? null);
      params.onAfterRestore();
    });
  };

  const initial = sessionDrafts.get(activeKey);
  if (initial) {
    params.setText(initial.text);
    params.imgAttach.replaceImages(initial.images);
    params.pasteCollapse.replacePastes(initial.pastes);
    params.imgAttach.setNotice(initial.notice ?? null);
    params.onAfterRestore();
  }
  createComputed(sync);

  const clearCurrentDraft = (targetSessionID?: string | null, targetDir?: string | null) => {
    const key =
      targetSessionID !== undefined || targetDir !== undefined
        ? composerDraftKey(targetSessionID, targetDir)
        : composerDraftKey(params.sessionID(), params.directory?.() ?? workspace.rootDir);
    bumpDraftEpoch(key);
    sessionDrafts.delete(key);
  };

  const restoreFailedDraft = (
    draft: SessionComposerDraft,
    sessionID: string | null,
    directory: string,
    notice: string
  ) => {
    const key = composerDraftKey(sessionID, directory);
    const active = isCurrentTarget(sessionID, directory);
    const existing = active
      ? {
          text: params.text(),
          images: params.imgAttach.images(),
          pastes: params.pasteCollapse.pastes(),
        }
      : sessionDrafts.get(key);
    const mergeItems = <T extends { id: string }>(failed: T[], current: T[] = []) =>
      Array.from(new Map([...failed, ...current].map((item) => [item.id, item])).values());
    const restored: SessionComposerDraft = {
      text:
        existing?.text && existing.text !== draft.text
          ? [draft.text, existing.text].filter(Boolean).join("\n\n")
          : draft.text,
      images: mergeItems(draft.images, existing?.images),
      pastes: mergeItems(draft.pastes, existing?.pastes),
      notice,
    };
    setSessionDraft(key, restored);
    if (!active) return;
    params.setText(restored.text);
    params.imgAttach.replaceImages(restored.images);
    params.pasteCollapse.replacePastes(restored.pastes);
    params.imgAttach.setNotice(notice);
    params.onAfterRestore();
  };

  return { clearCurrentDraft, restoreFailedDraft, isCurrentTarget, sync };
}
