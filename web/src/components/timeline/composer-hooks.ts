// web/src/components/timeline/composer-hooks.ts
import { type Accessor, createSignal } from "solid-js";
import type { PromptAttachment } from "../../services/api";
import {
  createComposerAutocomplete,
  expandContextMentions,
  type AutocompleteItem,
} from "./composer-autocomplete";

export { createComposerAutocomplete, expandContextMentions, type AutocompleteItem };

const ACCEPTED_IMAGE_MIMES = new Set([
  "image/png",
  "image/jpeg",
  "image/webp",
  "image/gif",
]);

export interface ImageItem {
  id: string;
  filename: string;
  mime: string;
  dataUrl: string;
}

export interface CollapsedPasteItem {
  id: string;
  lines: number;
  chars: number;
  preview: string;
  content: string;
}

/**
 * Ported from kilocode useImageAttachments.ts:
 * Supports Cmd+V clipboard screenshot paste, drag-and-drop image files, and file picker.
 */
export function createImageAttachments() {
  const [images, setImages] = createSignal<ImageItem[]>([]);
  const [dragging, setDragging] = createSignal(false);

  const addFile = (file: File) => {
    if (!ACCEPTED_IMAGE_MIMES.has(file.type)) return;
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result !== "string") return;
      setImages((prev) => [
        ...prev,
        {
          id: `img_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
          filename: file.name || "screenshot.png",
          mime: file.type,
          dataUrl: reader.result as string,
        },
      ]);
    };
    reader.readAsDataURL(file);
  };

  const removeImage = (id: string) => {
    setImages((prev) => prev.filter((item) => item.id !== id));
  };

  const clearImages = () => setImages([]);

  const handleClipboardPaste = (ev: ClipboardEvent): boolean => {
    const items = Array.from(ev.clipboardData?.items ?? []);
    const imageItems = items.filter(
      (item) => item.kind === "file" && ACCEPTED_IMAGE_MIMES.has(item.type)
    );
    if (imageItems.length === 0) return false;
    ev.preventDefault();
    for (const item of imageItems) {
      const file = item.getAsFile();
      if (file) addFile(file);
    }
    return true;
  };

  const handleDragOver = (ev: DragEvent) => {
    if (!ev.dataTransfer?.types.includes("Files")) return;
    ev.preventDefault();
    setDragging(true);
  };

  const handleDragLeave = () => {
    setDragging(false);
  };

  const handleDrop = (ev: DragEvent) => {
    setDragging(false);
    if (!ev.dataTransfer?.files?.length) return;
    ev.preventDefault();
    for (const file of Array.from(ev.dataTransfer.files)) {
      addFile(file);
    }
  };

  const toPromptAttachments = (): PromptAttachment[] =>
    images().map((img) => ({
      type: "file",
      mime: img.mime,
      filename: img.filename,
      url: img.dataUrl,
    }));

  const restoreFromAttachments = (atts: PromptAttachment[]) => {
    if (!atts || atts.length === 0) return;
    setImages((prev) => [
      ...prev,
      ...atts.map((a, idx) => ({
        id: `img_restored_${Date.now()}_${idx}`,
        filename: a.filename || "image.png",
        mime: a.mime,
        dataUrl: a.url,
      })),
    ]);
  };

  return {
    images,
    dragging,
    addFile,
    removeImage,
    clearImages,
    handleClipboardPaste,
    handleDragOver,
    handleDragLeave,
    handleDrop,
    toPromptAttachments,
    restoreFromAttachments,
  };
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
    expandIntoPrompt,
  };
}

const HISTORY_STORAGE_KEY = "hoxi-prompt-history:v1";
const MAX_HISTORY = 50;

function loadStoredHistory(): string[] {
  try {
    const raw = localStorage.getItem(HISTORY_STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed.filter((s) => typeof s === "string");
  } catch {
    /* ignore */
  }
  return [];
}

/**
 * Ported from kilocode usePromptHistory.ts:
 * Shell-style Up/Down arrow history navigation in Composer.
 */
export function createPromptHistory(sessionID: Accessor<string | null>) {
  const [entries, setEntries] = createSignal<string[]>(loadStoredHistory());
  const [cursorIdx, setCursorIdx] = createSignal(-1);
  let savedDraft = "";
  let lastSession: string | null = null;

  const syncSession = () => {
    const cur = sessionID();
    if (cur !== lastSession) {
      lastSession = cur;
      setCursorIdx(-1);
      savedDraft = "";
    }
  };

  const recordPrompt = (text: string) => {
    const cleaned = text.trim();
    if (!cleaned || cleaned.length > 4000) return;
    setCursorIdx(-1);
    savedDraft = "";
    setEntries((prev) => {
      const deduped = [cleaned, ...prev.filter((item) => item !== cleaned)].slice(
        0,
        MAX_HISTORY
      );
      try {
        localStorage.setItem(HISTORY_STORAGE_KEY, JSON.stringify(deduped));
      } catch {
        /* ignore */
      }
      return deduped;
    });
  };

  const navigateHistory = (
    direction: "up" | "down",
    currentText: string,
    selectionStart: number
  ): string | null => {
    syncSession();
    const list = entries();
    if (list.length === 0) return null;

    const browsing = cursorIdx() >= 0;
    if (direction === "up") {
      const onFirstLine = !currentText.slice(0, selectionStart).includes("\n");
      if (!browsing && currentText.trim().length > 0 && !onFirstLine) return null;

      if (cursorIdx() === -1) {
        savedDraft = currentText;
        setCursorIdx(0);
        return list[0] ?? null;
      }
      const next = cursorIdx() + 1;
      if (next >= list.length) return null;
      setCursorIdx(next);
      return list[next] ?? null;
    }

    if (!browsing) return null;
    const onLastLine = !currentText.slice(selectionStart).includes("\n");
    if (!onLastLine) return null;

    if (cursorIdx() > 0) {
      const next = cursorIdx() - 1;
      setCursorIdx(next);
      return list[next] ?? null;
    }
    setCursorIdx(-1);
    return savedDraft;
  };

  return {
    recordPrompt,
    navigateHistory,
    resetHistoryCursor: () => setCursorIdx(-1),
  };
}
