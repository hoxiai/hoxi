// web/src/store/share-image.ts
// Global state, remembered preferences, native save-dialog export, clipboard copy,
// and file-manager reveal helpers for the local Session Share Image modal.
import { createSignal } from "solid-js";
import { hoxiAPI } from "../services/api";
import { errorMessageOf, isWailsDesktopWebView } from "../services/http";
import type { SessionInfo } from "../services/sse-types";
import { openExternalFile } from "./inspector";
import { isAnyPortalMode } from "./intake-portal";

export type ShareImageThemeOption = "light" | "dark" | "system";
export type ShareImageWidthOption = "standard" | "wide";
export type ShareImageLayoutOption = "single" | "paged";

export interface ShareImagePreferences {
  theme: ShareImageThemeOption;
  width: ShareImageWidthOption;
  layout: ShareImageLayoutOption;
  includeTools: boolean;
  includeThinking: boolean;
  includeImages: boolean;
  hideLocalPaths: boolean;
  redactSecrets: boolean;
  showBrandFooter: boolean;
}

export interface ShareImageModalRequest {
  sessionID: string;
  /** When opened from an assistant turn footer, pre-select only that Q&A exchange. */
  focusMessageID?: string;
  /** Optional snapshot of session metadata (covers cross-project sidebar items). */
  session?: SessionInfo;
}

export type ShareSaveResult =
  | { status: "saved"; mode: "native"; paths: string[]; directory: string }
  | { status: "saved"; mode: "browser"; filenames: string[] }
  | { status: "partial"; savedCount: number; total: number; error?: string }
  | { status: "canceled" }
  | { status: "error"; error: string };

const PREFS_STORAGE_KEY = "hoxi.share-image-prefs:v1";
const LAST_SAVE_DIR_STORAGE_KEY = "hoxi.share-image-last-dir:v1";
// Match the bounded desktop /api/hoxi/file/save-images request in gateway/file_save.go.
const NATIVE_SAVE_BATCH_COUNT = 32;
const NATIVE_SAVE_BATCH_BYTES = 80 * 1024 * 1024;

export const DEFAULT_SHARE_IMAGE_PREFS: ShareImagePreferences = {
  theme: "light",
  width: "standard",
  layout: "single",
  includeTools: true,
  includeThinking: false,
  includeImages: true,
  hideLocalPaths: true,
  redactSecrets: true,
  showBrandFooter: true,
};

function loadPreferences(): ShareImagePreferences {
  try {
    const raw = typeof localStorage !== "undefined" ? localStorage.getItem(PREFS_STORAGE_KEY) : null;
    if (!raw) return { ...DEFAULT_SHARE_IMAGE_PREFS };
    const parsed = JSON.parse(raw) as Partial<ShareImagePreferences> | null;
    if (!parsed || typeof parsed !== "object") return { ...DEFAULT_SHARE_IMAGE_PREFS };
    return {
      theme: parsed.theme === "dark" || parsed.theme === "system" ? parsed.theme : "light",
      width: parsed.width === "wide" ? "wide" : "standard",
      layout: parsed.layout === "paged" ? "paged" : "single",
      includeTools: typeof parsed.includeTools === "boolean" ? parsed.includeTools : true,
      includeThinking: typeof parsed.includeThinking === "boolean" ? parsed.includeThinking : false,
      includeImages: typeof parsed.includeImages === "boolean" ? parsed.includeImages : true,
      hideLocalPaths: typeof parsed.hideLocalPaths === "boolean" ? parsed.hideLocalPaths : true,
      redactSecrets: typeof parsed.redactSecrets === "boolean" ? parsed.redactSecrets : true,
      showBrandFooter: typeof parsed.showBrandFooter === "boolean" ? parsed.showBrandFooter : true,
    };
  } catch {
    return { ...DEFAULT_SHARE_IMAGE_PREFS };
  }
}

function loadLastSaveDir(): string | undefined {
  try {
    const raw = typeof localStorage !== "undefined" ? localStorage.getItem(LAST_SAVE_DIR_STORAGE_KEY) : null;
    const trimmed = (raw || "").trim();
    return trimmed || undefined;
  } catch {
    return undefined;
  }
}

function rememberLastSaveDir(dir: string | undefined): void {
  const trimmed = (dir || "").trim();
  if (!trimmed) return;
  try {
    if (typeof localStorage !== "undefined") {
      localStorage.setItem(LAST_SAVE_DIR_STORAGE_KEY, trimmed);
    }
  } catch {
    /* ignore storage errors */
  }
}

const [modalRequest, setModalRequest] = createSignal<ShareImageModalRequest | null>(null);
const [prefs, setPrefs] = createSignal<ShareImagePreferences>(loadPreferences());

export const shareImageModal = modalRequest;
export const shareImageModalOpen = () => modalRequest() !== null;
export const shareImagePrefs = prefs;

export function openShareImageModal(request: ShareImageModalRequest): void {
  const sessionID = (request.sessionID || "").trim();
  if (!sessionID) return;
  const focusMessageID = request.focusMessageID?.trim() || undefined;
  setModalRequest({ sessionID, focusMessageID, session: request.session });
}

export function closeShareImageModal(): void {
  setModalRequest(null);
}

export function updateShareImagePrefs(patch: Partial<ShareImagePreferences>): void {
  setPrefs((current) => {
    const next: ShareImagePreferences = { ...current, ...patch };
    try {
      if (typeof localStorage !== "undefined") {
        localStorage.setItem(PREFS_STORAGE_KEY, JSON.stringify(next));
      }
    } catch {
      /* ignore storage errors */
    }
    return next;
  });
}

export function sanitizeShareImageFilename(title: string): string {
  const cleaned = title
    .trim()
    .replace(/[\\/:*?"<>|]+/g, "-")
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^[.-]+|-+$/g, "");
  return cleaned.slice(0, 72) || "hoxi-session";
}

function blobToDataURL(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === "string") resolve(reader.result);
      else reject(new Error("Failed to encode image"));
    };
    reader.onerror = () => reject(reader.error ?? new Error("Failed to encode image"));
    reader.readAsDataURL(blob);
  });
}

function triggerBrowserDownloads(items: Array<{ filename: string; blob: Blob }>): void {
  items.forEach((item, idx) => {
    const trigger = () => {
      const url = URL.createObjectURL(item.blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = item.filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 60_000);
    };
    if (idx === 0) trigger();
    else setTimeout(trigger, idx * 150);
  });
}

function isUnavailableNativeSaveError(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err || "");
  if (/selected (?:destination|directory) is protected|destination path is protected/i.test(msg)) {
    return false;
  }
  if (/^HTTP (?:401|403|404|405):/.test(msg)) return true;
  if (!isWailsDesktopWebView() && /Failed to fetch|NetworkError|Load failed/i.test(msg)) return true;
  return false;
}

function nativeSaveBatches<T extends { blob: Blob }>(items: T[]): T[][] {
  const batches: T[][] = [];
  let batch: T[] = [];
  let bytes = 0;
  for (const item of items) {
    if (batch.length >= NATIVE_SAVE_BATCH_COUNT || (batch.length && bytes + item.blob.size > NATIVE_SAVE_BATCH_BYTES)) {
      batches.push(batch);
      batch = [];
      bytes = 0;
    }
    batch.push(item);
    bytes += item.blob.size;
  }
  if (batch.length) batches.push(batch);
  return batches;
}

export async function saveShareImagePages(
  pages: ReadonlyArray<{ blob: Blob }>,
  baseName: string,
  options?: {
    dialogTitle?: string;
    batchDialogTitle?: (firstPage: number, lastPage: number) => string;
    isCurrent?: () => boolean;
  }
): Promise<ShareSaveResult> {
  if (pages.length === 0) return { status: "canceled" };
  const stem = sanitizeShareImageFilename(baseName);
  const items = pages.map((page, idx) => ({
    filename: pages.length > 1 ? `${stem}-${idx + 1}.png` : `${stem}.png`,
    blob: page.blob,
  }));

  if (!isAnyPortalMode() && typeof hoxiAPI.saveImages === "function") {
    const batches = nativeSaveBatches(items);
    const paths: string[] = [];
    const directories = new Set<string>();
    const interrupted = (error?: string): ShareSaveResult => {
      if (paths.length) return { status: "partial", savedCount: paths.length, total: items.length, error };
      return error ? { status: "error", error } : { status: "canceled" };
    };
    let defaultDir = loadLastSaveDir();
    try {
      for (const batch of batches) {
        if (options?.isCurrent?.() === false) return interrupted();
        const images = await Promise.all(batch.map(async ({ filename, blob }) => ({
          filename, dataUrl: await blobToDataURL(blob),
        })));
        if (options?.isCurrent?.() === false) return interrupted();
        const title = batches.length > 1
          ? options?.batchDialogTitle?.(paths.length + 1, paths.length + batch.length) ?? options?.dialogTitle
          : options?.dialogTitle;
        const res = await hoxiAPI.saveImages({ title, defaultDir, images });
        if (!res?.supported && !paths.length) break;
        if (res?.canceled) return interrupted();
        if (!res?.supported || !res.saved || !Array.isArray(res.paths) || res.paths.length !== batch.length) {
          return interrupted(res?.error || "Could not save PNG");
        }
        paths.push(...res.paths);
        const directory = (res.directory || "").trim();
        directories.add(directory);
        if (directory) defaultDir = directory;
        rememberLastSaveDir(directory);
      }
      if (paths.length) {
        const [firstDirectory] = directories;
        return { status: "saved", mode: "native", paths, directory: directories.size === 1 ? firstDirectory : "" };
      }
    } catch (err) {
      if (paths.length || !isUnavailableNativeSaveError(err)) {
        return interrupted(errorMessageOf(err, "Could not save PNG"));
      }
    }
  }

  if (options?.isCurrent?.() === false) return { status: "canceled" };
  triggerBrowserDownloads(items);
  return {
    status: "saved",
    mode: "browser",
    filenames: items.map((item) => item.filename),
  };
}

export async function copyShareImageBlob(blob: Blob): Promise<boolean> {
  const writeBrowser = async (): Promise<boolean> => {
    if (
      typeof navigator === "undefined" ||
      !navigator.clipboard?.write ||
      typeof ClipboardItem === "undefined"
    ) {
      return false;
    }
    try {
      await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })]);
      return true;
    } catch {
      return false;
    }
  };

  if (isWailsDesktopWebView() && !isAnyPortalMode() && typeof hoxiAPI.writeClipboardImage === "function") {
    try {
      const dataUrl = await blobToDataURL(blob);
      await hoxiAPI.writeClipboardImage(dataUrl);
      return true;
    } catch {
      return writeBrowser();
    }
  }

  if (await writeBrowser()) return true;

  if (!isAnyPortalMode() && typeof hoxiAPI.writeClipboardImage === "function") {
    try {
      const dataUrl = await blobToDataURL(blob);
      await hoxiAPI.writeClipboardImage(dataUrl);
      return true;
    } catch {
      return false;
    }
  }
  return false;
}

export async function revealSavedShareImage(targetPath: string): Promise<boolean> {
  const cleaned = targetPath.trim();
  if (!cleaned) return false;
  try {
    await openExternalFile(cleaned, { action: "reveal" });
    return true;
  } catch {
    return false;
  }
}

export function formatSavedDisplayPath(fullPath: string): string {
  const cleaned = fullPath.trim();
  if (!cleaned) return "";
  return cleaned
    .replace(/^\/Users\/[^/]+(?=\/|$)/, "~")
    .replace(/^\/home\/[^/]+(?=\/|$)/, "~")
    .replace(/^[A-Za-z]:\\Users\\[^\\]+(?=\\|$)/, "~");
}
