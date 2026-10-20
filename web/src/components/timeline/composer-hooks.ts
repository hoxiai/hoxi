// web/src/components/timeline/composer-hooks.ts
import { type Accessor, createEffect, createSignal, type Setter } from "solid-js";
import { t } from "../../i18n";
import { createPromptHistory, uploadPromptAttachment } from "../../store/workspace";
import { hoxiAPI, type PromptAttachment } from "../../services/api";
import {
  applyAutocompleteToken,
  createComposerAutocomplete,
  expandContextMentions,
  findAutocompleteToken,
  inspectPromptContextMentions,
  resolveContextMentionsForSend,
  type AutocompleteItem,
  type AutocompleteToken,
  type ContextMentionChipInfo,
} from "./composer-autocomplete";

export {
  applyAutocompleteToken,
  createComposerAutocomplete,
  createPromptHistory,
  expandContextMentions,
  findAutocompleteToken,
  inspectPromptContextMentions,
  resolveContextMentionsForSend,
  type AutocompleteItem,
  type AutocompleteToken,
  type ContextMentionChipInfo,
};

export const MAX_ATTACHMENTS = 6;
export const MAX_ATTACHMENT_BYTES = 15 * 1024 * 1024;

const DIRECT_IMAGE_MIMES = new Set([
  "image/png",
  "image/jpeg",
  "image/webp",
  "image/gif",
]);

const IMAGE_FILENAME_RE = /\.(png|jpe?g|webp|gif|bmp|tiff?|svg|heic|heif|avif)$/i;
const SPREADSHEET_FILENAME_RE = /\.(xlsx|xls|ods|csv|tsv)$/i;
const DOCUMENT_FILENAME_RE = /\.(docx|doc|pptx|ppt|rtf)$/i;
const TEXT_FILENAME_RE =
  /\.(txt|md|markdown|json|jsonl|ya?ml|toml|xml|log|ini|conf|sh|bash|zsh|py|go|tsx?|jsx?|rs|java|c|cpp|h|hpp|cs|rb|php|swift|kt|dart|sql|html|css|scss|vue|svelte)$/i;

export type AttachmentKind = "image" | "pdf" | "spreadsheet" | "document" | "text";

export function classifyAttachmentKind(filename: string, mime: string): AttachmentKind | null {
  const lowerMime = (mime || "").toLowerCase().trim().split(";")[0];
  const name = (filename || "").trim();
  if (lowerMime.startsWith("image/") || IMAGE_FILENAME_RE.test(name)) {
    return "image";
  }
  if (lowerMime === "application/pdf" || /\.pdf$/i.test(name)) {
    return "pdf";
  }
  if (
    lowerMime === "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" ||
    lowerMime === "application/vnd.ms-excel" ||
    lowerMime === "application/excel" ||
    lowerMime === "application/x-excel" ||
    lowerMime === "application/x-msexcel" ||
    lowerMime === "application/vnd.oasis.opendocument.spreadsheet" ||
    lowerMime === "text/csv" ||
    lowerMime === "application/csv" ||
    lowerMime === "text/tab-separated-values" ||
    SPREADSHEET_FILENAME_RE.test(name)
  ) {
    return "spreadsheet";
  }
  if (
    lowerMime === "application/vnd.openxmlformats-officedocument.wordprocessingml.document" ||
    lowerMime === "application/msword" ||
    lowerMime === "application/vnd.openxmlformats-officedocument.presentationml.presentation" ||
    lowerMime === "application/vnd.ms-powerpoint" ||
    lowerMime === "application/rtf" ||
    lowerMime === "text/rtf" ||
    DOCUMENT_FILENAME_RE.test(name)
  ) {
    return "document";
  }
  if (
    lowerMime.startsWith("text/") ||
    lowerMime === "application/json" ||
    lowerMime === "application/xml" ||
    lowerMime === "application/javascript" ||
    lowerMime === "application/typescript" ||
    lowerMime === "application/x-yaml" ||
    TEXT_FILENAME_RE.test(name)
  ) {
    return "text";
  }
  return null;
}

function normalizeAttachmentMime(mime: string, filename: string, kind: AttachmentKind): string {
  const lower = (mime || "").toLowerCase().trim().split(";")[0];
  const ext = (filename.split(".").pop() || "").toLowerCase();
  if (kind === "pdf") return "application/pdf";
  if (kind === "spreadsheet") {
    if (ext === "xlsx") {
      return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";
    }
    if (ext === "xls") return "application/vnd.ms-excel";
    if (ext === "ods") return "application/vnd.oasis.opendocument.spreadsheet";
    if (ext === "tsv") return "text/tab-separated-values";
    if (ext === "csv") return "text/csv";
    return lower || "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";
  }
  if (kind === "document") {
    if (ext === "docx") {
      return "application/vnd.openxmlformats-officedocument.wordprocessingml.document";
    }
    if (ext === "doc") return "application/msword";
    if (ext === "pptx") {
      return "application/vnd.openxmlformats-officedocument.presentationml.presentation";
    }
    if (ext === "ppt") return "application/vnd.ms-powerpoint";
    if (ext === "rtf") return "application/rtf";
    return lower || "application/vnd.openxmlformats-officedocument.wordprocessingml.document";
  }
  if (kind === "text") {
    if (lower.startsWith("text/") || lower === "application/json" || lower === "application/xml") {
      return lower;
    }
    if (ext === "md" || ext === "markdown") return "text/markdown";
    if (ext === "json" || ext === "jsonl") return "application/json";
    return "text/plain";
  }
  if (lower === "image/jpg") return "image/jpeg";
  if (lower.startsWith("image/")) return lower;
  if (ext === "jpg" || ext === "jpeg") return "image/jpeg";
  if (ext === "webp") return "image/webp";
  if (ext === "gif") return "image/gif";
  return "image/png";
}

function looksLikeCopiedImageFilenames(text: string): boolean {
  const trimmed = text.trim();
  if (!trimmed || trimmed.length > 1024) return false;
  const lines = trimmed
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean);
  if (lines.length === 0 || lines.length > 4) return false;
  return lines.every(
    (line) => !line.includes("://") && classifyAttachmentKind(line, "") !== null
  );
}

function convertDataUrlToPngIfNeeded(
  dataUrl: string,
  mime: string,
  filename: string
): Promise<{ dataUrl: string; mime: string; filename: string }> {
  if (DIRECT_IMAGE_MIMES.has(mime)) {
    return Promise.resolve({ dataUrl, mime, filename });
  }
  if (typeof document === "undefined" || typeof Image === "undefined") {
    return Promise.resolve({ dataUrl, mime: "image/png", filename });
  }
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => {
      try {
        const canvas = document.createElement("canvas");
        canvas.width = img.naturalWidth || img.width || 1;
        canvas.height = img.naturalHeight || img.height || 1;
        const ctx = canvas.getContext("2d");
        if (ctx) {
          ctx.drawImage(img, 0, 0);
          const pngUrl = canvas.toDataURL("image/png");
          const pngName = filename.replace(/\.[^.]+$/, "") + ".png";
          resolve({ dataUrl: pngUrl, mime: "image/png", filename: pngName });
          return;
        }
      } catch {
        /* fallback to original dataUrl */
      }
      resolve({ dataUrl, mime: "image/png", filename });
    };
    img.onerror = () => resolve({ dataUrl, mime: "image/png", filename });
    img.src = dataUrl;
  });
}

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

let draftResetEpoch = 0;

/**
 * Supports Cmd+V clipboard screenshot/Finder image paste, drag-and-drop files, and file picker
 * with size/count limits, loading/error feedback, and session-switch stale-read guard.
 */
export function createImageAttachments(sessionID?: Accessor<string | null>) {
  const [images, setImages] = createSignal<ImageItem[]>([]);
  const [dragging, setDragging] = createSignal(false);
  const [pendingCount, setPendingCount] = createSignal(0);
  const [notice, setNotice] = createSignal<string | null>(null);

  const currentKey = () => sessionID?.() || "__new__";

  const commitAttachmentForTarget = (
    targetKey: string,
    targetEpoch: number,
    item: ImageItem
  ) => {
    if (targetEpoch !== draftResetEpoch) return;
    if (currentKey() !== targetKey) {
      const existing = sessionDrafts.get(targetKey) ?? {
        text: "",
        images: [],
        pastes: [],
      };
      if (existing.images.length < MAX_ATTACHMENTS) {
        sessionDrafts.set(targetKey, {
          ...existing,
          images: [...existing.images, item],
        });
      }
      return;
    }
    setImages((prev) => {
      if (prev.length >= MAX_ATTACHMENTS) {
        setNotice(t("timeline.attachmentLimitReached", { max: MAX_ATTACHMENTS }));
        return prev;
      }
      return [...prev, item];
    });
  };

  const addDataUrlImage = (
    item: { filename: string; mime: string; dataUrl: string; size?: number },
    targetKey = currentKey(),
    targetEpoch = draftResetEpoch
  ) => {
    if (!item.dataUrl) return;
    const rawName = item.filename || "screenshot.png";
    const kind = classifyAttachmentKind(rawName, item.mime) ?? "image";
    const normMime = normalizeAttachmentMime(item.mime, rawName, kind);
    const id = `img_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`;

    if (typeof window !== "undefined" && item.dataUrl.startsWith("data:")) {
      setPendingCount((n) => n + 1);
      void uploadPromptAttachment({
        sessionID: targetKey === "__new__" ? undefined : targetKey,
        filename: rawName,
        mime: normMime,
        dataUrl: item.dataUrl,
      })
        .then((uploaded) => {
          commitAttachmentForTarget(targetKey, targetEpoch, {
            id,
            filename: uploaded.filename || rawName,
            mime: uploaded.mime || normMime,
            dataUrl: uploaded.url || item.dataUrl,
            previewUrl: kind === "image" ? item.dataUrl : undefined,
            size: uploaded.size ?? item.size,
            kind: uploaded.kind || kind,
          });
        })
        .catch(() => {
          commitAttachmentForTarget(targetKey, targetEpoch, {
            id,
            filename: rawName,
            mime: normMime,
            dataUrl: item.dataUrl,
            previewUrl: kind === "image" ? item.dataUrl : undefined,
            size: item.size,
            kind,
          });
        })
        .finally(() => {
          setPendingCount((n) => Math.max(0, n - 1));
        });
      return;
    }

    commitAttachmentForTarget(targetKey, targetEpoch, {
      id,
      filename: rawName,
      mime: normMime,
      dataUrl: item.dataUrl,
      previewUrl: kind === "image" ? item.dataUrl : undefined,
      size: item.size,
      kind,
    });
  };

  const addFile = (file: File) => {
    setNotice(null);
    const rawName = file.name || "screenshot.png";
    const kind = classifyAttachmentKind(rawName, file.type || "");
    if (!kind) {
      setNotice(t("timeline.attachmentUnsupportedType", { name: rawName }));
      return;
    }
    if (file.size > MAX_ATTACHMENT_BYTES) {
      setNotice(
        t("timeline.attachmentTooLarge", {
          name: rawName,
          maxMB: Math.round(MAX_ATTACHMENT_BYTES / (1024 * 1024)),
        })
      );
      return;
    }
    if (images().length + pendingCount() >= MAX_ATTACHMENTS) {
      setNotice(t("timeline.attachmentLimitReached", { max: MAX_ATTACHMENTS }));
      return;
    }

    const originKey = currentKey();
    const originEpoch = draftResetEpoch;
    const normMime = normalizeAttachmentMime(file.type, rawName, kind);
    setPendingCount((n) => n + 1);

    const reader = new FileReader();
    reader.onerror = () => {
      setPendingCount((n) => Math.max(0, n - 1));
      setNotice(t("timeline.attachmentReadFailed", { name: rawName }));
    };
    reader.onload = () => {
      setPendingCount((n) => Math.max(0, n - 1));
      if (typeof reader.result !== "string") {
        setNotice(t("timeline.attachmentReadFailed", { name: rawName }));
        return;
      }
      if (kind === "image") {
        void convertDataUrlToPngIfNeeded(reader.result, normMime, rawName).then((converted) => {
          addDataUrlImage(
            { ...converted, size: file.size },
            originKey,
            originEpoch
          );
        });
      } else {
        addDataUrlImage(
          { filename: rawName, mime: normMime, dataUrl: reader.result, size: file.size },
          originKey,
          originEpoch
        );
      }
    };
    reader.readAsDataURL(file);
  };

  const removeImage = (id: string) => {
    setImages((prev) => prev.filter((item) => item.id !== id));
  };

  const clearImages = () => {
    setImages([]);
    setNotice(null);
  };

  const handleClipboardPaste = (
    ev: ClipboardEvent,
    onFallbackText?: (text: string) => void
  ): boolean => {
    const originKey = currentKey();
    const originEpoch = draftResetEpoch;
    const items = Array.from(ev.clipboardData?.items ?? []);
    const itemFiles: File[] = [];
    for (const item of items) {
      if (item.kind === "file") {
        const f = item.getAsFile();
        if (f && classifyAttachmentKind(f.name || "", f.type || "") !== null) {
          itemFiles.push(f);
        }
      }
    }
    if (itemFiles.length === 0 && ev.clipboardData?.files?.length) {
      for (const f of Array.from(ev.clipboardData.files)) {
        if (classifyAttachmentKind(f.name || "", f.type || "") !== null) {
          itemFiles.push(f);
        }
      }
    }

    const plainText = ev.clipboardData?.getData("text/plain") ?? "";

    // macOS Finder Cmd+C on an image file puts the filename in text/plain (and Finder's file icon in image/*).
    // Prefer reading the actual file URL from NSPasteboard first.
    if (looksLikeCopiedImageFilenames(plainText)) {
      ev.preventDefault();
      setPendingCount((n) => n + 1);
      void hoxiAPI
        .readClipboardImages({ filesOnly: true })
        .then((nativeImages) => {
          if (nativeImages.length > 0) {
            for (const img of nativeImages) {
              addDataUrlImage(img, originKey, originEpoch);
            }
          } else if (itemFiles.length > 0) {
            for (const f of itemFiles) addFile(f);
          } else {
            onFallbackText?.(plainText);
          }
        })
        .catch(() => {
          if (itemFiles.length > 0) {
            for (const f of itemFiles) addFile(f);
          } else {
            onFallbackText?.(plainText);
          }
        })
        .finally(() => {
          setPendingCount((n) => Math.max(0, n - 1));
        });
      return true;
    }

    if (itemFiles.length > 0) {
      ev.preventDefault();
      for (const file of itemFiles) {
        addFile(file);
      }
      return true;
    }

    const html = ev.clipboardData?.getData("text/html") ?? "";
    if (!plainText.trim() && html) {
      const m = html.match(/src=["'](data:image\/([a-zA-Z0-9.+-]+);base64,[^"']+)["']/i);
      if (m?.[1]) {
        ev.preventDefault();
        const ext = (m[2] || "png").toLowerCase().replace("jpeg", "jpg");
        addDataUrlImage(
          {
            filename: `clipboard.${ext}`,
            mime: `image/${(m[2] || "png").toLowerCase()}`,
            dataUrl: m[1],
          },
          originKey,
          originEpoch
        );
        return true;
      }
    }

    if (!plainText) {
      ev.preventDefault();
      setPendingCount((n) => n + 1);
      void hoxiAPI
        .readClipboardImages()
        .then((nativeImages) => {
          for (const img of nativeImages) {
            addDataUrlImage(img, originKey, originEpoch);
          }
        })
        .catch(() => {})
        .finally(() => {
          setPendingCount((n) => Math.max(0, n - 1));
        });
      return true;
    }

    return false;
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
        kind: classifyAttachmentKind(a.filename || "", a.mime || "") ?? "image",
      })),
    ]);
  };

  const replaceImages = (items: ImageItem[]) => {
    setImages(items);
    setNotice(null);
  };

  return {
    images,
    dragging,
    pendingCount,
    notice,
    setNotice,
    clearNotice: () => setNotice(null),
    addFile,
    removeImage,
    clearImages,
    replaceImages,
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

interface SessionComposerDraft {
  text: string;
  images: ImageItem[];
  pastes: CollapsedPasteItem[];
}

const sessionDrafts = new Map<string, SessionComposerDraft>();

export function createSessionDraftSync(params: {
  sessionID: Accessor<string | null>;
  resetRequest?: Accessor<number>;
  text: Accessor<string>;
  setText: Setter<string>;
  imgAttach: ReturnType<typeof createImageAttachments>;
  pasteCollapse: ReturnType<typeof createPasteCollapse>;
  onAfterRestore: () => void;
}) {
  let activeKey = params.sessionID() || "__new__";
  let resetRequest = params.resetRequest?.();

  createEffect(() => {
    const nextKey = params.sessionID() || "__new__";
    const request = params.resetRequest?.();
    const reset = request !== resetRequest;
    resetRequest = request;
    if (nextKey === activeKey && !reset) return;

    const prevText = params.text();
    const prevImages = params.imgAttach.images();
    const prevPastes = params.pasteCollapse.pastes();
    if (prevText.trim() || prevImages.length > 0 || prevPastes.length > 0) {
      sessionDrafts.set(activeKey, {
        text: prevText,
        images: prevImages,
        pastes: prevPastes,
      });
    } else {
      sessionDrafts.delete(activeKey);
    }

    activeKey = nextKey;
    if (reset) {
      draftResetEpoch++;
      sessionDrafts.delete(nextKey);
    }
    const saved = sessionDrafts.get(nextKey);
    params.setText(saved?.text ?? "");
    params.imgAttach.replaceImages(saved?.images ?? []);
    params.pasteCollapse.replacePastes(saved?.pastes ?? []);
    params.onAfterRestore();
  });

  const clearCurrentDraft = () => {
    sessionDrafts.delete(params.sessionID() || "__new__");
  };

  return { clearCurrentDraft };
}
