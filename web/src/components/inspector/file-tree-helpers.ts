import { normalizeFolderPath } from "../../store/workspace";
import type { FileContentResponse } from "../../services/api";

export function baseName(p: string): string {
  const parts = normalizeFolderPath(p).split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || p;
}

export function isMarkdownPath(p: string, mime?: string): boolean {
  if (mime === "text/markdown") return true;
  return /\.(md|mdx|markdown)$/i.test(p.trim());
}

export function isSvgPath(p: string, mime?: string): boolean {
  if (mime?.startsWith("image/svg+xml")) return true;
  return /\.svg$/i.test(p.trim());
}

export function isImagePreviewable(p: string, meta?: FileContentResponse | null): boolean {
  const mime = meta?.mimeType || "";
  if (mime.startsWith("image/")) return true;
  return /\.(png|jpe?g|gif|webp|ico|bmp|avif|svg)$/i.test(p.trim());
}

export function formatByteSize(bytes?: number): string {
  if (typeof bytes !== "number" || Number.isNaN(bytes) || bytes < 0) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}
