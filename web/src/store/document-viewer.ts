// web/src/store/document-viewer.ts
// Global on-demand document, HTML, and docked side-canvas preview state
// shared by chat, code blocks, inspector, and attachments.
import { createSignal } from "solid-js";
import { hoxiAPI, type FileContentResponse } from "../services/api";
import { setWorkspace, workspace } from "./workspace-state";

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

export interface CanvasPreviewEntry {
  id: string;
  kind: "html" | "svg";
  title: string;
  content: string;
  sessionID?: string;
  createdAt: number;
}

const MAX_CANVAS_HISTORY = 20;
let canvasSeq = 0;

const [viewer, setViewer] = createSignal<DocumentViewerPayload | null>(null);
const [canvasHistory, setCanvasHistory] = createSignal<CanvasPreviewEntry[]>([]);
const [activeCanvasID, setActiveCanvasID] = createSignal<string | null>(null);

export const documentViewer = viewer;
export const documentViewerOpen = () => viewer() !== null;

export const canvasPreviewHistory = canvasHistory;
export const activeCanvasPreviewID = activeCanvasID;
export const canvasPreview = (): CanvasPreviewEntry | null => {
  const list = canvasHistory();
  if (list.length === 0) return null;
  const curId = activeCanvasID();
  return list.find((item) => item.id === curId) ?? list[list.length - 1] ?? null;
};

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

export function openCanvasPreview(input: {
  kind: "html" | "svg";
  title?: string;
  content: string;
  sessionID?: string;
}): void {
  const content = input.content || "";
  if (!content.trim()) return;
  const title = (input.title || "").trim() || (input.kind === "svg" ? "SVG" : "HTML");
  const sid = input.sessionID ?? workspace.activeSessionID ?? undefined;

  const existing = canvasHistory().find(
    (item) => item.kind === input.kind && item.content === content && item.sessionID === sid
  );
  if (existing) {
    setActiveCanvasID(existing.id);
  } else {
    const entry: CanvasPreviewEntry = {
      id: `cvs_${Date.now()}_${++canvasSeq}`,
      kind: input.kind,
      title,
      content,
      ...(sid ? { sessionID: sid } : {}),
      createdAt: Date.now(),
    };
    setCanvasHistory((prev) => {
      const next = [...prev, entry];
      return next.length > MAX_CANVAS_HISTORY
        ? next.slice(next.length - MAX_CANVAS_HISTORY)
        : next;
    });
    setActiveCanvasID(entry.id);
  }

  setWorkspace({
    inspectorOpen: true,
    inspectorTab: "preview",
  });
}

export function selectCanvasPreview(id: string): void {
  if (canvasHistory().some((item) => item.id === id)) {
    setActiveCanvasID(id);
  }
}

export function clearCanvasPreview(): void {
  setCanvasHistory([]);
  setActiveCanvasID(null);
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

export function isSvgCodeBlockLang(lang: string): boolean {
  const clean = (lang || "").trim().toLowerCase();
  return clean === "svg" || clean === "xml+svg" || clean === "image/svg+xml";
}

export function isChartCodeBlockLang(lang: string): boolean {
  const clean = (lang || "").trim().toLowerCase();
  return clean === "chart" || clean === "hoxi-chart" || clean === "chartjs";
}
