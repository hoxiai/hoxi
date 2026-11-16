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

export function isPdfPath(p: string, mime?: string): boolean {
  if (mime === "application/pdf") return true;
  return /\.pdf$/i.test(p.trim());
}

export function isSpreadsheetPath(p: string, mime?: string): boolean {
  if (
    mime === "text/csv" ||
    mime === "text/tab-separated-values" ||
    mime === "application/vnd.ms-excel" ||
    mime === "application/vnd.oasis.opendocument.spreadsheet" ||
    mime === "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
  ) {
    return true;
  }
  return /\.(xlsx|xls|ods|csv|tsv)$/i.test(p.trim());
}

export function isOfficeDocumentPath(p: string, mime?: string): boolean {
  if (
    mime === "application/msword" ||
    mime === "application/rtf" ||
    mime === "application/vnd.ms-powerpoint" ||
    mime === "application/vnd.openxmlformats-officedocument.wordprocessingml.document" ||
    mime === "application/vnd.openxmlformats-officedocument.presentationml.presentation"
  ) {
    return true;
  }
  return /\.(docx|doc|pptx|ppt|rtf)$/i.test(p.trim());
}

export function isPreviewableDocumentPath(p: string, mime?: string): boolean {
  return (
    isMarkdownPath(p, mime) ||
    isSvgPath(p, mime) ||
    isPdfPath(p, mime) ||
    isSpreadsheetPath(p, mime) ||
    isOfficeDocumentPath(p, mime)
  );
}

export function excelColLabel(colIdx: number): string {
  let n = Math.max(0, colIdx) + 1;
  let out = "";
  while (n > 0) {
    n--;
    out = String.fromCharCode(65 + (n % 26)) + out;
    n = Math.floor(n / 26);
  }
  return out || "A";
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
