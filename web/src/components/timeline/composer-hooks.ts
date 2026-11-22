// web/src/components/timeline/composer-hooks.ts
import { type Accessor, createSignal } from "solid-js";
import { t } from "../../i18n";
import {
  createPromptHistory,
  stagePromptFile,
  uploadPromptAttachment,
} from "../../store/workspace";
import { hoxiAPI, type PromptAttachment } from "../../services/api";
import {
  applyAutocompleteToken,
  applySlashCommandToken,
  createComposerAutocomplete,
  expandContextMentions,
  findAutocompleteToken,
  inspectActiveSlashCommand,
  inspectPromptContextMentions,
  removeLeadingSlashCommand,
  resolveContextMentionsForSend,
  resolveSentSlashCommand,
  type ActiveSlashCommandInfo,
  type AutocompleteItem,
  type AutocompleteToken,
  type ContextMentionChipInfo,
} from "./composer-autocomplete";
import {
  bumpDraftEpoch,
  composerDraftKey,
  createPasteCollapse,
  createSessionDraftSync,
  getDraftEpoch,
  sessionDrafts,
  setSessionDraft,
  type AttachmentKind,
  type CollapsedPasteItem,
  type ImageItem,
  type SessionComposerDraft,
} from "./composer-drafts";

export {
  applyAutocompleteToken,
  applySlashCommandToken,
  bumpDraftEpoch,
  composerDraftKey,
  createComposerAutocomplete,
  createPasteCollapse,
  createPromptHistory,
  createSessionDraftSync,
  expandContextMentions,
  findAutocompleteToken,
  getDraftEpoch,
  inspectActiveSlashCommand,
  inspectPromptContextMentions,
  removeLeadingSlashCommand,
  resolveContextMentionsForSend,
  resolveSentSlashCommand,
  type ActiveSlashCommandInfo,
  type AttachmentKind,
  type AutocompleteItem,
  type AutocompleteToken,
  type CollapsedPasteItem,
  type ContextMentionChipInfo,
  type ImageItem,
  type SessionComposerDraft,
};

export const MAX_ATTACHMENTS = 6;
export const MAX_ATTACHMENT_BYTES = 15 * 1024 * 1024;
export const MAX_IMAGE_DIMENSION_PX = 2048;

const DIRECT_IMAGE_MIMES = new Set([
  "image/png",
  "image/jpeg",
  "image/webp",
  "image/gif",
]);

const IMAGE_FILENAME_RE = /\.(png|jpe?g|webp|gif|bmp|tiff?|svg|heic|heif|avif)$/i;
const AUDIO_FILENAME_RE = /\.(mp3|wav|m4a|aac|ogg|oga|opus|flac)$/i;
const VIDEO_FILENAME_RE = /\.(mp4|mov|webm|mkv|avi|m4v)$/i;
const STAGED_MEDIA_OR_ARCHIVE_RE =
  /\.(mp4|mov|mkv|webm|avi|m4v|mp3|wav|m4a|aac|flac|ogg|opus|srt|vtt|ass|zip|tar|gz)$/i;

export function shouldStageFileToWorkspace(file: File): boolean {
  const mime = (file.type || "").toLowerCase();
  const name = file.name || "";
  if (mime.startsWith("video/") || mime.startsWith("audio/")) return true;
  if (STAGED_MEDIA_OR_ARCHIVE_RE.test(name)) return true;
  return file.size > MAX_ATTACHMENT_BYTES;
}
const SPREADSHEET_FILENAME_RE = /\.(xlsx|xls|ods|csv|tsv)$/i;
const DOCUMENT_FILENAME_RE = /\.(docx|doc|pptx|ppt|rtf)$/i;
const TEXT_FILENAME_RE =
  /\.(txt|md|markdown|json|jsonl|ya?ml|toml|xml|log|ini|conf|sh|bash|zsh|py|go|tsx?|jsx?|rs|java|c|cpp|h|hpp|cs|rb|php|swift|kt|dart|sql|html|css|scss|vue|svelte)$/i;

export function classifyAttachmentKind(filename: string, mime: string): AttachmentKind | null {
  const lowerMime = (mime || "").toLowerCase().trim().split(";")[0];
  const name = (filename || "").trim();
  if (lowerMime.startsWith("image/") || IMAGE_FILENAME_RE.test(name)) {
    return "image";
  }
  if (lowerMime === "application/pdf" || /\.pdf$/i.test(name)) {
    return "pdf";
  }
  if (lowerMime.startsWith("audio/") || AUDIO_FILENAME_RE.test(name)) {
    return "audio";
  }
  if (lowerMime.startsWith("video/") || VIDEO_FILENAME_RE.test(name)) {
    return "video";
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
  if (kind === "audio") {
    if (lower.startsWith("audio/")) return lower;
    if (ext === "mp3") return "audio/mpeg";
    if (ext === "wav") return "audio/wav";
    if (ext === "m4a") return "audio/mp4";
    if (ext === "aac") return "audio/aac";
    if (ext === "flac") return "audio/flac";
    if (ext === "ogg" || ext === "oga" || ext === "opus") return "audio/ogg";
    return "audio/webm";
  }
  if (kind === "video") {
    if (lower.startsWith("video/")) return lower;
    if (ext === "mov") return "video/quicktime";
    if (ext === "webm") return "video/webm";
    if (ext === "mkv") return "video/x-matroska";
    if (ext === "avi") return "video/x-msvideo";
    return "video/mp4";
  }
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
  if (mime === "image/gif" || mime === "image/svg+xml") {
    return Promise.resolve({ dataUrl, mime, filename });
  }
  const needsFormatConvert = !DIRECT_IMAGE_MIMES.has(mime);
  if (!needsFormatConvert && dataUrl.length < 1_600_000) {
    return Promise.resolve({ dataUrl, mime, filename });
  }
  if (typeof document === "undefined" || typeof Image === "undefined") {
    return Promise.resolve({
      dataUrl,
      mime: needsFormatConvert ? "image/png" : mime,
      filename,
    });
  }
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => {
      try {
        const rawW = img.naturalWidth || img.width || 1;
        const rawH = img.naturalHeight || img.height || 1;
        const longest = Math.max(rawW, rawH);
        if (!needsFormatConvert && longest <= MAX_IMAGE_DIMENSION_PX) {
          resolve({ dataUrl, mime, filename });
          return;
        }
        const scale = longest > MAX_IMAGE_DIMENSION_PX ? MAX_IMAGE_DIMENSION_PX / longest : 1;
        const targetW = Math.max(1, Math.round(rawW * scale));
        const targetH = Math.max(1, Math.round(rawH * scale));
        const canvas = document.createElement("canvas");
        canvas.width = targetW;
        canvas.height = targetH;
        const ctx = canvas.getContext("2d");
        if (ctx) {
          ctx.imageSmoothingEnabled = true;
          ctx.imageSmoothingQuality = "high";
          ctx.drawImage(img, 0, 0, targetW, targetH);
          const outMime =
            !needsFormatConvert && (mime === "image/jpeg" || mime === "image/webp")
              ? mime
              : "image/png";
          const outUrl =
            outMime === "image/png"
              ? canvas.toDataURL("image/png")
              : canvas.toDataURL(outMime, 0.88);
          const outName = needsFormatConvert
            ? filename.replace(/\.[^.]+$/, "") + ".png"
            : filename;
          resolve({ dataUrl: outUrl, mime: outMime, filename: outName });
          return;
        }
      } catch {
        /* fallback to original dataUrl */
      }
      resolve({ dataUrl, mime: needsFormatConvert ? "image/png" : mime, filename });
    };
    img.onerror = () =>
      resolve({ dataUrl, mime: needsFormatConvert ? "image/png" : mime, filename });
    img.src = dataUrl;
  });
}

/**
 * Supports Cmd+V clipboard screenshot/Finder image paste, drag-and-drop files, and file picker
 * with size/count limits, loading/error feedback, and session-switch stale-read guard.
 */
export function createImageAttachments(
  sessionID?: Accessor<string | null>,
  directory?: Accessor<string>,
  options?: {
    resolveStageDirectory?: () => Promise<string | null> | string | null;
  }
) {
  const [images, setImages] = createSignal<ImageItem[]>([]);
  const [dragging, setDragging] = createSignal(false);
  const [pendingCount, setPendingCount] = createSignal(0);
  const [notice, setNotice] = createSignal<string | null>(null);

  const currentKey = () => composerDraftKey(sessionID?.(), directory?.());

  const commitAttachmentForTarget = (
    targetKey: string,
    targetEpoch: number,
    item: ImageItem
  ): boolean => {
    if (targetEpoch !== getDraftEpoch(targetKey)) return false;
    if (currentKey() !== targetKey) {
      const existing = sessionDrafts.get(targetKey) ?? {
        text: "",
        images: [],
        pastes: [],
      };
      if (existing.images.length >= MAX_ATTACHMENTS) return false;
      setSessionDraft(targetKey, {
        ...existing,
        images: [...existing.images, item],
      });
      return true;
    }
    let committed = false;
    setImages((prev) => {
      if (prev.length >= MAX_ATTACHMENTS) {
        setNotice(t("timeline.attachmentLimitReached", { max: MAX_ATTACHMENTS }));
        return prev;
      }
      committed = true;
      return [...prev, item];
    });
    return committed;
  };

  const revokeBlobPreviewUrl = (url?: string) => {
    if (url && url.startsWith("blob:") && typeof URL !== "undefined" && typeof URL.revokeObjectURL === "function") {
      try {
        URL.revokeObjectURL(url);
      } catch {
        /* ignore */
      }
    }
  };

  const addDataUrlImage = (
    item: { filename: string; mime: string; dataUrl: string; size?: number },
    targetKey = currentKey(),
    targetEpoch = getDraftEpoch(targetKey)
  ) => {
    if (!item.dataUrl) return;
    const rawName = item.filename || "screenshot.png";
    const kind = classifyAttachmentKind(rawName, item.mime) ?? "image";
    const normMime = normalizeAttachmentMime(item.mime, rawName, kind);
    const id = `img_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`;

    if (typeof window !== "undefined" && item.dataUrl.startsWith("data:")) {
      setPendingCount((n) => n + 1);
      void uploadPromptAttachment({
        sessionID: targetKey.startsWith("__new__") ? undefined : targetKey,
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

  const stageFileToWorkspace = async (
    file: File,
    targetKey = currentKey(),
    targetEpoch = getDraftEpoch(targetKey)
  ) => {
    if (images().length + pendingCount() >= MAX_ATTACHMENTS) {
      setNotice(t("timeline.attachmentLimitReached", { max: MAX_ATTACHMENTS }));
      return;
    }
    setPendingCount((n) => n + 1);
    try {
      const targetDir = options?.resolveStageDirectory
        ? await options.resolveStageDirectory()
        : directory?.() || null;
      if (!targetDir) {
        setNotice("Workspace directory is not ready");
        return;
      }
      // If resolveStageDirectory switched the active workspace on a new draft,
      // re-anchor the target draft key so the staged item lands in the visible composer.
      if (!sessionID?.()) {
        targetKey = currentKey();
        targetEpoch = getDraftEpoch(targetKey);
      }
      const data = await stagePromptFile(targetDir, file);
      if (!data.ok || !data.path) {
        setNotice(data.error || `Failed to stage ${file.name}`);
        return;
      }
      const finalName = data.filename || file.name || "media.bin";
      const finalMime = data.mime || file.type || "application/octet-stream";
      const kind = classifyAttachmentKind(finalName, finalMime) ?? "document";
      const mediaUrl =
        data.url ||
        `/api/hoxi/file/content?path=${encodeURIComponent(data.path)}&directory=${encodeURIComponent(targetDir)}&raw=1`;
      let localPreviewUrl: string | undefined;
      if (
        (kind === "video" || kind === "image") &&
        typeof URL !== "undefined" &&
        typeof URL.createObjectURL === "function"
      ) {
        try {
          localPreviewUrl = URL.createObjectURL(file);
        } catch {
          localPreviewUrl = undefined;
        }
      }
      const committed = commitAttachmentForTarget(targetKey, targetEpoch, {
        id: `staged_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
        filename: finalName,
        mime: finalMime,
        dataUrl: mediaUrl,
        previewUrl: localPreviewUrl || mediaUrl,
        size: data.size ?? file.size,
        kind,
      });
      if (!committed && localPreviewUrl) {
        revokeBlobPreviewUrl(localPreviewUrl);
      }
    } catch (err) {
      setNotice(err instanceof Error ? err.message : String(err || "Stage failed"));
    } finally {
      setPendingCount((n) => Math.max(0, n - 1));
    }
  };

  const addFile = (file: File) => {
    setNotice(null);
    const rawName = file.name || "screenshot.png";
    if (shouldStageFileToWorkspace(file) && (options?.resolveStageDirectory || directory?.())) {
      const originKey = currentKey();
      const originEpoch = getDraftEpoch(originKey);
      void stageFileToWorkspace(file, originKey, originEpoch);
      return;
    }
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
    const originEpoch = getDraftEpoch(originKey);
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
    setImages((prev) => {
      const removed = prev.find((item) => item.id === id);
      if (removed) revokeBlobPreviewUrl(removed.previewUrl);
      return prev.filter((item) => item.id !== id);
    });
  };

  const clearImages = () => {
    for (const item of images()) {
      revokeBlobPreviewUrl(item.previewUrl);
    }
    setImages([]);
    setNotice(null);
  };

  const handleClipboardPaste = (
    ev: ClipboardEvent,
    onFallbackText?: (text: string) => void
  ): boolean => {
    const originKey = currentKey();
    const originEpoch = getDraftEpoch(originKey);
    const items = Array.from(ev.clipboardData?.items ?? []);
    const itemFiles: File[] = [];
    const isAcceptedClipboardFile = (f: File) =>
      classifyAttachmentKind(f.name || "", f.type || "") !== null ||
      shouldStageFileToWorkspace(f);
    for (const item of items) {
      if (item.kind === "file") {
        const f = item.getAsFile();
        if (f && isAcceptedClipboardFile(f)) {
          itemFiles.push(f);
        }
      }
    }
    if (itemFiles.length === 0 && ev.clipboardData?.files?.length) {
      for (const f of Array.from(ev.clipboardData.files)) {
        if (isAcceptedClipboardFile(f)) {
          itemFiles.push(f);
        }
      }
    }

    const plainText = ev.clipboardData?.getData("text/plain") ?? "";

    // macOS Finder Cmd+C on an image file puts the filename in text/plain (and Finder's file icon in image/*).
    // Prefer reading the actual file URL from NSPasteboard first, except when clipboard already carries
    // a staged media/large file that should stream directly to workspace inputs/.
    if (looksLikeCopiedImageFilenames(plainText) && !itemFiles.some(shouldStageFileToWorkspace)) {
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
