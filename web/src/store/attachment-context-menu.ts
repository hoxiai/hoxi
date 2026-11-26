// web/src/store/attachment-context-menu.ts
// Shared context menu state and clipboard/download/open actions for conversation
// attachments (images, files, audio, video) and the fullscreen image preview.
import { createSignal } from "solid-js";
import { hoxiAPI } from "../services/api";
import { writeDesktopClipboard } from "../services/desktop-events";
import { getGatewayToken, getLoopbackHTTPBase, isWailsDesktopWebView } from "../services/http";
import { isAnyPortalMode } from "./intake-portal";
import {
  openAttachmentInInspector,
  openExternalFile,
  openFileInInspector,
} from "./inspector";

export type AttachmentMenuKind = "image" | "file" | "audio" | "video";

export interface AttachmentMenuTarget {
  kind: AttachmentMenuKind;
  src: string;
  filename?: string;
  mime?: string;
  localPath?: string;
  downloadUrl?: string;
  onPreview?: () => void;
  onOpenOriginal?: () => void;
  originalLabel?: string;
}

export interface AttachmentContextMenuState {
  clientX: number;
  clientY: number;
  target: AttachmentMenuTarget;
}

export interface AttachmentActionFeedback {
  text: string;
  tone: "ok" | "error";
}

export interface ResolvedAttachmentActions {
  filename: string;
  downloadHref: string;
  canCopyImage: boolean;
  canDownload: boolean;
  canPreview: boolean;
  canOpenInInspector: boolean;
  openInInspectorLabel?: string;
  canOpenExternal: boolean;
  externalFilePath?: string;
  externalFileDir?: string;
  copyMeta?: { kind: "link" | "path" | "filename"; value: string };
}

const [menuState, setMenuState] = createSignal<AttachmentContextMenuState | null>(null);
const [feedbackState, setFeedbackState] = createSignal<AttachmentActionFeedback | null>(null);
let feedbackTimer: ReturnType<typeof setTimeout> | undefined;

export const attachmentContextMenu = menuState;
export const attachmentActionFeedback = feedbackState;

export function openAttachmentContextMenu(
  event: MouseEvent,
  target: AttachmentMenuTarget
): void {
  const src = (target.src || "").trim();
  const downloadUrl = (target.downloadUrl || "").trim();
  if (!src && !downloadUrl && !target.localPath) return;
  event.preventDefault();
  event.stopPropagation();
  setMenuState({
    clientX: event.clientX,
    clientY: event.clientY,
    target: {
      ...target,
      src,
      downloadUrl: downloadUrl || undefined,
    },
  });
}

export function closeAttachmentContextMenu(): void {
  setMenuState(null);
}

export function showAttachmentActionFeedback(
  text: string,
  tone: "ok" | "error" = "ok"
): void {
  const cleaned = text.trim();
  if (!cleaned) return;
  setFeedbackState({ text: cleaned, tone });
  if (feedbackTimer !== undefined) clearTimeout(feedbackTimer);
  feedbackTimer = setTimeout(() => {
    feedbackTimer = undefined;
    setFeedbackState(null);
  }, tone === "error" ? 3200 : 2000);
}

export function clearAttachmentActionFeedback(): void {
  if (feedbackTimer !== undefined) {
    clearTimeout(feedbackTimer);
    feedbackTimer = undefined;
  }
  setFeedbackState(null);
}

function isLocalLoopbackURL(url: string): boolean {
  const trimmed = url.trim();
  if (!/^https?:\/\//i.test(trimmed)) return false;
  try {
    const parsed = new URL(trimmed);
    const loopbackBase = getLoopbackHTTPBase();
    if (loopbackBase) {
      try {
        if (parsed.origin === new URL(loopbackBase).origin) return true;
      } catch {
        /* ignore invalid loopback base */
      }
    }
    const host = parsed.hostname.toLowerCase();
    return host === "127.0.0.1" || host === "localhost" || host === "::1" || host === "[::1]";
  } catch {
    return false;
  }
}

function isLocalGatewayURL(url: string): boolean {
  const trimmed = url.trim();
  return trimmed.startsWith("/") || isLocalLoopbackURL(trimmed);
}

function isRemoteHttpURL(url: string): boolean {
  const trimmed = url.trim();
  return /^https?:\/\//i.test(trimmed) && !isLocalLoopbackURL(trimmed);
}

function cleanGatewayAttachmentPath(url: string): string {
  const trimmed = url.trim();
  if (!trimmed) return "";
  try {
    const parsed = new URL(
      trimmed,
      typeof window !== "undefined" ? window.location.origin : "http://localhost"
    );
    return parsed.pathname;
  } catch {
    return stripQueryAndHash(trimmed);
  }
}

function parseStagedFileURL(url: string): { path: string; directory?: string } | null {
  const raw = url.trim();
  if (!raw.startsWith("/api/hoxi/file/content?") && !raw.includes("/api/hoxi/file/content?")) {
    return null;
  }
  try {
    const parsed = new URL(
      raw,
      typeof window !== "undefined" ? window.location.origin : "http://localhost"
    );
    if (parsed.pathname !== "/api/hoxi/file/content") return null;
    const stagedPath = (parsed.searchParams.get("path") || "").trim();
    if (!stagedPath) return null;
    const dir = (parsed.searchParams.get("directory") || "").trim() || undefined;
    return { path: stagedPath, directory: dir };
  } catch {
    return null;
  }
}

function isGatewayAttachmentURL(url: string): boolean {
  return /\/api\/hoxi\/attachments\/att_[A-Za-z0-9_-]+(?:\/|$|\?)/.test(url.trim());
}

function stripQueryAndHash(raw: string): string {
  return raw.split(/[?#]/, 1)[0] || "";
}

function basenameOfPath(raw: string): string {
  const cleaned = stripQueryAndHash(raw).replace(/\\/g, "/");
  const parts = cleaned.split("/").filter(Boolean);
  return parts.length > 0 ? parts[parts.length - 1] : "";
}

function defaultExtForTarget(kind: AttachmentMenuKind, mime?: string, src?: string): string {
  const m = (mime || "").toLowerCase();
  const dataMimeMatch = (src || "").trim().match(/^data:([^;,]+)/i);
  const effectiveMime = m || (dataMimeMatch ? dataMimeMatch[1].toLowerCase() : "");
  if (kind === "image") {
    if (effectiveMime.includes("jpeg") || effectiveMime.includes("jpg")) return ".jpg";
    if (effectiveMime.includes("webp")) return ".webp";
    if (effectiveMime.includes("gif")) return ".gif";
    if (effectiveMime.includes("svg")) return ".svg";
    if (effectiveMime.includes("avif")) return ".avif";
    return ".png";
  }
  if (kind === "audio") {
    if (effectiveMime.includes("wav")) return ".wav";
    if (effectiveMime.includes("ogg")) return ".ogg";
    if (effectiveMime.includes("m4a") || effectiveMime.includes("mp4")) return ".m4a";
    return ".mp3";
  }
  if (kind === "video") {
    if (effectiveMime.includes("webm")) return ".webm";
    if (effectiveMime.includes("quicktime")) return ".mov";
    return ".mp4";
  }
  return "";
}

function ensureMediaExtension(name: string, target: AttachmentMenuTarget): string {
  const cleaned = name.replace(/[<>:"/\\|?*\x00-\x1f]+/g, "_").trim();
  if (!cleaned) {
    return `${target.kind === "file" ? "attachment" : target.kind}${defaultExtForTarget(target.kind, target.mime, target.src)}`;
  }
  if (target.kind === "file") return cleaned;
  if (/\.[a-z0-9]{2,5}$/i.test(cleaned)) return cleaned;
  return `${cleaned}${defaultExtForTarget(target.kind, target.mime, target.downloadUrl || target.src)}`;
}

export function deriveAttachmentFilename(target: AttachmentMenuTarget): string {
  const explicit = (target.filename || "").trim();
  if (explicit) return ensureMediaExtension(basenameOfPath(explicit) || explicit, target);
  const local = (target.localPath || "").trim();
  if (local) {
    const base = basenameOfPath(local);
    if (base) return ensureMediaExtension(base, target);
  }
  const staged = parseStagedFileURL(target.downloadUrl || target.src);
  if (staged) {
    const base = basenameOfPath(staged.path);
    if (base) return ensureMediaExtension(base, target);
  }
  const url = (target.downloadUrl || target.src || "").trim();
  if (url && !url.startsWith("data:") && !url.startsWith("blob:")) {
    const base = basenameOfPath(url);
    if (base && base !== "content" && !/^att_[A-Za-z0-9_-]+$/.test(base)) {
      try {
        return ensureMediaExtension(decodeURIComponent(base), target);
      } catch {
        return ensureMediaExtension(base, target);
      }
    }
  }
  return ensureMediaExtension("", target);
}

export function resolveAttachmentMenuActions(
  target: AttachmentMenuTarget,
  opts?: { portalMode?: boolean }
): ResolvedAttachmentActions {
  const portalMode = opts?.portalMode ?? isAnyPortalMode();
  const src = (target.src || "").trim();
  const downloadHref = (target.downloadUrl || src).trim();
  const localPath = (target.localPath || "").trim();
  const staged = parseStagedFileURL(downloadHref) || parseStagedFileURL(src);
  const isGatewayAtt = isGatewayAttachmentURL(downloadHref) || isGatewayAttachmentURL(src);
  const remoteSrc = isRemoteHttpURL(src) ? src : "";
  const filename = deriveAttachmentFilename(target);

  let externalFilePath: string | undefined;
  let externalFileDir: string | undefined;
  if (localPath) {
    externalFilePath = localPath;
  } else if (staged) {
    externalFilePath = staged.path;
    externalFileDir = staged.directory;
  } else if (isGatewayAtt) {
    externalFilePath = cleanGatewayAttachmentPath(
      isGatewayAttachmentURL(src) ? src : downloadHref
    );
  }

  let copyMeta: ResolvedAttachmentActions["copyMeta"];
  if (localPath) {
    copyMeta = { kind: "path", value: localPath };
  } else if (staged) {
    copyMeta = { kind: "path", value: staged.path };
  } else if (remoteSrc) {
    copyMeta = { kind: "link", value: remoteSrc };
  } else if ((target.filename || "").trim()) {
    copyMeta = { kind: "filename", value: (target.filename || "").trim() };
  }

  const hasInspectorTarget =
    Boolean(target.onOpenOriginal) || Boolean(localPath) || Boolean(staged) || isGatewayAtt;

  return {
    filename,
    downloadHref,
    canCopyImage: target.kind === "image" && Boolean(src),
    canDownload: Boolean(downloadHref),
    canPreview: target.kind === "image" && Boolean(target.onPreview),
    canOpenInInspector: !portalMode && hasInspectorTarget,
    openInInspectorLabel: target.originalLabel,
    canOpenExternal: !portalMode && Boolean(externalFilePath),
    externalFilePath,
    externalFileDir,
    copyMeta,
  };
}

function buildFetchHeaders(url: string): Record<string, string> | undefined {
  if (!isLocalGatewayURL(url)) return undefined;
  const token = getGatewayToken();
  return token ? { "X-Hoxi-Token": token } : undefined;
}

function blobToDataURL(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === "string") {
        resolve(reader.result);
      } else {
        reject(new Error("Failed to encode image"));
      }
    };
    reader.onerror = () => reject(reader.error || new Error("Failed to read blob"));
    reader.readAsDataURL(blob);
  });
}

function renderImageURLToPngBlob(url: string, crossOrigin?: string): Promise<Blob> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    if (crossOrigin) img.crossOrigin = crossOrigin;
    img.onload = () => {
      try {
        const width = img.naturalWidth || img.width || 1024;
        const height = img.naturalHeight || img.height || 1024;
        const canvas = document.createElement("canvas");
        canvas.width = width;
        canvas.height = height;
        const ctx = canvas.getContext("2d");
        if (!ctx) {
          reject(new Error("Canvas unavailable"));
          return;
        }
        ctx.drawImage(img, 0, 0, width, height);
        canvas.toBlob(
          (blob) => {
            if (blob) resolve(blob);
            else reject(new Error("PNG conversion failed"));
          },
          "image/png"
        );
      } catch (err) {
        reject(err instanceof Error ? err : new Error("Canvas export blocked"));
      }
    };
    img.onerror = () => reject(new Error("Image decode failed"));
    img.src = url;
  });
}

async function blobToPngBlob(blob: Blob): Promise<Blob> {
  const mime = (blob.type || "").toLowerCase();
  if (mime === "image/png") return blob;
  const objectURL = URL.createObjectURL(blob);
  try {
    return await renderImageURLToPngBlob(objectURL);
  } finally {
    URL.revokeObjectURL(objectURL);
  }
}

async function loadImageSourceAsPngBlob(src: string): Promise<Blob> {
  const trimmed = src.trim();
  if (!trimmed) throw new Error("Empty image source");
  try {
    const resp = await fetch(trimmed, {
      headers: buildFetchHeaders(trimmed),
    });
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    const blob = await resp.blob();
    return await blobToPngBlob(blob);
  } catch (err) {
    if (isRemoteHttpURL(trimmed)) {
      return renderImageURLToPngBlob(trimmed, "anonymous");
    }
    throw err;
  }
}

async function writeBrowserClipboardPng(pngBlobPromise: Promise<Blob>): Promise<boolean> {
  if (
    typeof navigator === "undefined" ||
    !navigator.clipboard?.write ||
    typeof ClipboardItem === "undefined"
  ) {
    return false;
  }
  try {
    // Passing the Promise directly keeps WebKit/Chromium user-gesture activation alive.
    await navigator.clipboard.write([new ClipboardItem({ "image/png": pngBlobPromise })]);
    return true;
  } catch {
    try {
      const resolved = await pngBlobPromise;
      await navigator.clipboard.write([new ClipboardItem({ "image/png": resolved })]);
      return true;
    } catch {
      return false;
    }
  }
}

export async function copyImageSourceToClipboard(src: string): Promise<boolean> {
  const trimmed = src.trim();
  if (!trimmed) return false;

  const pngPromise = loadImageSourceAsPngBlob(trimmed);
  // Prevent unhandled rejection if clipboard APIs are unavailable before awaiting.
  pngPromise.catch(() => undefined);

  // Inside the Wails desktop WebView, write directly via the native desktop
  // pasteboard endpoint first to avoid WKWebView/WebView2 clipboard restrictions.
  if (isWailsDesktopWebView() && !isAnyPortalMode()) {
    try {
      const pngBlob = await pngPromise;
      const dataUrl = await blobToDataURL(pngBlob);
      await hoxiAPI.writeClipboardImage(dataUrl);
      return true;
    } catch {
      return writeBrowserClipboardPng(pngPromise);
    }
  }

  if (await writeBrowserClipboardPng(pngPromise)) {
    return true;
  }

  // Fallback for local browser sessions talking to the local Hoxi gateway.
  if (!isAnyPortalMode()) {
    try {
      const pngBlob = await pngPromise;
      const dataUrl = await blobToDataURL(pngBlob);
      await hoxiAPI.writeClipboardImage(dataUrl);
      return true;
    } catch {
      return false;
    }
  }

  return false;
}

function triggerAnchorDownload(href: string, filename: string, openBlank?: boolean): void {
  const link = document.createElement("a");
  link.href = href;
  link.download = filename;
  if (openBlank) {
    link.target = "_blank";
    link.rel = "noreferrer";
  }
  document.body.appendChild(link);
  link.click();
  link.remove();
}

export async function downloadAttachmentTarget(
  target: AttachmentMenuTarget
): Promise<boolean> {
  const resolved = resolveAttachmentMenuActions(target);
  const href = resolved.downloadHref;
  if (!href) return false;

  if (href.startsWith("data:") || href.startsWith("blob:")) {
    triggerAnchorDownload(href, resolved.filename);
    return true;
  }

  try {
    const resp = await fetch(href, {
      headers: buildFetchHeaders(href),
    });
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    const blob = await resp.blob();
    const objectURL = URL.createObjectURL(blob);
    triggerAnchorDownload(objectURL, resolved.filename);
    setTimeout(() => URL.revokeObjectURL(objectURL), 60_000);
    return true;
  } catch {
    if (isRemoteHttpURL(href)) {
      triggerAnchorDownload(href, resolved.filename, true);
      return true;
    }
    return false;
  }
}

export async function copyAttachmentTextValue(value: string): Promise<boolean> {
  const trimmed = value.trim();
  if (!trimmed) return false;
  try {
    await writeDesktopClipboard(trimmed);
    return true;
  } catch {
    return false;
  }
}

export function openAttachmentTargetInInspector(target: AttachmentMenuTarget): boolean {
  if (target.onOpenOriginal) {
    target.onOpenOriginal();
    return true;
  }
  const local = (target.localPath || "").trim();
  if (local) {
    openFileInInspector(local, null, null);
    return true;
  }
  return openAttachmentInInspector(target.downloadUrl || target.src, target.filename);
}

export async function openAttachmentTargetExternally(
  target: AttachmentMenuTarget,
  action: "open" | "reveal"
): Promise<boolean> {
  const resolved = resolveAttachmentMenuActions(target);
  if (!resolved.externalFilePath) return false;
  try {
    await openExternalFile(resolved.externalFilePath, {
      directory: resolved.externalFileDir,
      action,
    });
    return true;
  } catch {
    return false;
  }
}
