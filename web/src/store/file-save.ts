// web/src/store/file-save.ts
// Unified desktop native Save dialog export with browser-download fallback,
// remembered save directory, Reveal-in-Finder/Folder action, and global toast feedback.
import { createSignal } from "solid-js";
import { hoxiAPI } from "../services/api";
import { errorMessageOf, isWailsDesktopWebView } from "../services/http";
import { openExternalFile } from "./inspector";
import { isAnyPortalMode } from "./intake-portal";

export type FileSaveResult =
  | { status: "saved"; mode: "native"; path: string; directory: string }
  | { status: "saved"; mode: "browser"; filename: string }
  | { status: "canceled" }
  | { status: "error"; error: string };

export interface SavedFileToastState {
  id: number;
  result: Exclude<FileSaveResult, { status: "canceled" }>;
  revealFailed?: boolean;
}

const LAST_SAVE_DIR_STORAGE_KEY = "hoxi.file-save-last-dir:v1";
// Match maxSaveFileBytes in internal/gateway/file_save.go (50 MiB).
const MAX_NATIVE_SAVE_BYTES = 50 * 1024 * 1024;
const TOAST_SAVED_MS = 9000;
const TOAST_ERROR_MS = 6500;
const TOAST_RESUME_MS = 4000;

const [toastState, setToastState] = createSignal<SavedFileToastState | null>(null);
let toastSeq = 0;
let toastTimer: ReturnType<typeof setTimeout> | undefined;
let nativeSaveInFlight = false;

export const savedFileToast = toastState;

function clearToastTimer(): void {
  if (toastTimer !== undefined) {
    clearTimeout(toastTimer);
    toastTimer = undefined;
  }
}

function scheduleToastDismiss(id: number, delayMs: number): void {
  clearToastTimer();
  toastTimer = setTimeout(() => {
    setToastState((cur) => (cur && cur.id === id ? null : cur));
    toastTimer = undefined;
  }, delayMs);
}

export function showSavedFileToast(
  result: Exclude<FileSaveResult, { status: "canceled" }>
): void {
  const id = ++toastSeq;
  setToastState({ id, result });
  scheduleToastDismiss(id, result.status === "error" ? TOAST_ERROR_MS : TOAST_SAVED_MS);
}

export function dismissSavedFileToast(): void {
  clearToastTimer();
  setToastState(null);
}

export function pauseSavedFileToastTimer(): void {
  clearToastTimer();
}

export function resumeSavedFileToastTimer(): void {
  const cur = toastState();
  if (!cur) return;
  scheduleToastDismiss(cur.id, TOAST_RESUME_MS);
}

function loadLastFileSaveDir(): string | undefined {
  try {
    const raw =
      typeof localStorage !== "undefined" ? localStorage.getItem(LAST_SAVE_DIR_STORAGE_KEY) : null;
    const trimmed = (raw || "").trim();
    return trimmed || undefined;
  } catch {
    return undefined;
  }
}

function rememberLastFileSaveDir(dir: string | undefined): void {
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

async function blobToDataURL(blob: Blob): Promise<string> {
  if (typeof FileReader !== "undefined") {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => {
        if (typeof reader.result === "string") resolve(reader.result);
        else reject(new Error("Failed to encode file"));
      };
      reader.onerror = () => reject(reader.error ?? new Error("Failed to encode file"));
      reader.readAsDataURL(blob);
    });
  }
  const bytes = new Uint8Array(await blob.arrayBuffer());
  const mime = (blob.type || "application/octet-stream").split(";")[0].trim() || "application/octet-stream";
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return `data:${mime};base64,${btoa(binary)}`;
}

function triggerBrowserBlobDownload(blob: Blob, filename: string): void {
  if (typeof document === "undefined" || typeof URL === "undefined") {
    throw new Error("Browser download unavailable");
  }
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

function isUnavailableNativeSaveError(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err || "");
  if (/selected (?:destination|directory) is protected|destination path is protected/i.test(msg)) {
    return false;
  }
  if (/^HTTP (?:401|403|404|405):/.test(msg)) return true;
  if (/^HTTP 400:.*unsupported (?:export file type|file extension)/i.test(msg)) return true;
  if (!isWailsDesktopWebView() && /Failed to fetch|NetworkError|Load failed/i.test(msg)) {
    return true;
  }
  return false;
}

export async function saveBlobWithFeedback(
  blob: Blob,
  filename: string,
  options?: {
    dialogTitle?: string;
    showToast?: boolean;
  }
): Promise<FileSaveResult> {
  const cleanFilename = (filename || "").trim() || "download";
  const shouldToast = options?.showToast !== false;

  if (
    !isAnyPortalMode() &&
    typeof hoxiAPI.saveFile === "function" &&
    blob.size > 0 &&
    blob.size <= MAX_NATIVE_SAVE_BYTES
  ) {
    if (nativeSaveInFlight) {
      return { status: "canceled" };
    }
    nativeSaveInFlight = true;
    try {
      const dataUrl = await blobToDataURL(blob);
      const res = await hoxiAPI.saveFile({
        title: options?.dialogTitle,
        defaultDir: loadLastFileSaveDir(),
        filename: cleanFilename,
        dataUrl,
      });
      if (res?.supported) {
        if (res.canceled) {
          return { status: "canceled" };
        }
        if (res.saved && res.path) {
          const directory = (res.directory || "").trim();
          rememberLastFileSaveDir(directory);
          const result: FileSaveResult = {
            status: "saved",
            mode: "native",
            path: res.path,
            directory,
          };
          if (shouldToast) showSavedFileToast(result);
          return result;
        }
        const errResult: FileSaveResult = {
          status: "error",
          error: res.error || "Could not save file",
        };
        if (shouldToast) showSavedFileToast(errResult);
        return errResult;
      }
    } catch (err) {
      if (!isUnavailableNativeSaveError(err)) {
        const errResult: FileSaveResult = {
          status: "error",
          error: errorMessageOf(err, "Could not save file"),
        };
        if (shouldToast) showSavedFileToast(errResult);
        return errResult;
      }
    } finally {
      nativeSaveInFlight = false;
    }
  }

  try {
    triggerBrowserBlobDownload(blob, cleanFilename);
    const result: FileSaveResult = {
      status: "saved",
      mode: "browser",
      filename: cleanFilename,
    };
    if (shouldToast) showSavedFileToast(result);
    return result;
  } catch (err) {
    const errResult: FileSaveResult = {
      status: "error",
      error: errorMessageOf(err, "Could not download file"),
    };
    if (shouldToast) showSavedFileToast(errResult);
    return errResult;
  }
}

export async function revealSavedFile(targetPath: string): Promise<boolean> {
  const cleaned = targetPath.trim();
  if (!cleaned) return false;
  try {
    await openExternalFile(cleaned, { action: "reveal" });
    return true;
  } catch {
    const cur = toastState();
    if (cur && cur.result.status === "saved" && cur.result.mode === "native" && cur.result.path === cleaned) {
      setToastState({ ...cur, revealFailed: true });
      scheduleToastDismiss(cur.id, TOAST_RESUME_MS);
    }
    return false;
  }
}
