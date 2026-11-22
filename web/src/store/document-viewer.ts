// web/src/store/document-viewer.ts
// Global on-demand document & HTML preview modal state shared by chat, code blocks and attachments.
import { createSignal } from "solid-js";
import { hoxiAPI, type FileContentResponse } from "../services/api";

export function readDocumentPreviewFile(
  path: string,
  directory?: string,
  sessionID?: string
): Promise<FileContentResponse> {
  return hoxiAPI.readFileContent(path, directory, sessionID);
}

export type DocumentViewerPayload =
  | {
      kind: "file";
      path: string;
      title?: string;
      directory?: string;
      sessionID?: string;
      mime?: string;
      downloadUrl?: string;
    }
  | {
      kind: "html";
      title: string;
      html: string;
    };

const [viewer, setViewer] = createSignal<DocumentViewerPayload | null>(null);

export const documentViewer = viewer;
export const documentViewerOpen = () => viewer() !== null;

export function openDocumentPreview(payload: DocumentViewerPayload): void {
  if (payload.kind === "file") {
    const path = (payload.path || "").trim();
    if (!path) return;
    setViewer({
      ...payload,
      path,
      title: (payload.title || "").trim() || path,
    });
    return;
  }
  const html = payload.html || "";
  if (!html.trim()) return;
  setViewer({
    kind: "html",
    title: (payload.title || "").trim() || "HTML",
    html,
  });
}

export function closeDocumentPreview(): void {
  setViewer(null);
}

export function isHtmlPreviewPath(path: string, mime?: string): boolean {
  const m = (mime || "").toLowerCase();
  if (m === "text/html" || m === "application/xhtml+xml") return true;
  return /\.html?$/i.test((path || "").trim());
}

export function isRichDocumentPreviewPath(path: string, mime?: string): boolean {
  const clean = (path || "").trim();
  const m = (mime || "").toLowerCase();
  if (
    m === "application/pdf" ||
    m === "text/markdown" ||
    m === "text/csv" ||
    m === "text/tab-separated-values" ||
    m === "text/html" ||
    m === "application/xhtml+xml" ||
    m === "application/msword" ||
    m === "application/rtf" ||
    m === "application/vnd.ms-excel" ||
    m === "application/vnd.ms-powerpoint" ||
    m === "application/vnd.oasis.opendocument.spreadsheet" ||
    m === "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" ||
    m === "application/vnd.openxmlformats-officedocument.wordprocessingml.document" ||
    m === "application/vnd.openxmlformats-officedocument.presentationml.presentation"
  ) {
    return true;
  }
  return /\.(?:pdf|xlsx|xls|ods|csv|tsv|docx|doc|pptx|ppt|rtf|md|mdx|markdown|html?)$/i.test(clean);
}

export function isMermaidCodeBlockLang(lang: string): boolean {
  const clean = (lang || "").trim().toLowerCase();
  return clean === "mermaid" || clean === "mmd";
}

export function isHtmlPreviewCodeBlockLang(lang: string): boolean {
  const clean = (lang || "").trim().toLowerCase();
  return clean === "html" || clean === "htm";
}
