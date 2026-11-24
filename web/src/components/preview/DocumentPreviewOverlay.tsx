// web/src/components/preview/DocumentPreviewOverlay.tsx
import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import { Portal } from "solid-js/web";
import {
  Check,
  Code2,
  Copy,
  ExternalLink,
  Eye,
  FolderOpen,
  ListTree,
  LoaderCircle,
  PanelRightOpen,
  X,
} from "lucide-solid";
import { Presence } from "../motion/Presence";
import {
  activeDirectory,
  closeDocumentPreview,
  documentViewer,
  errorMessageOf,
  isHtmlPreviewPath,
  isTesterPortalMode,
  openCanvasPreview,
  openExternalFile,
  openFileInInspector,
  readDocumentPreviewFile,
  updateInspectedSpreadsheetCells,
  workspace,
} from "../../store/workspace";
import type { FileContentResponse, SpreadsheetSheetPreview } from "../../services/api";
import { FileIcon } from "../icons";
import { t } from "../../i18n";
import {
  formatByteSize,
  isMarkdownPath,
  isOfficeDocumentPath,
  isPdfPath,
  isSpreadsheetPath,
} from "../inspector/file-tree-helpers";
import { extractMarkdownHeadings } from "../timeline/MarkdownView";
import MarkdownDocumentReader from "../inspector/MarkdownDocumentReader";
import { PdfPreviewFrame, SpreadsheetPreviewView } from "../inspector/SpreadsheetPreviewView";
import SandboxedHtmlPreview from "./SandboxedHtmlPreview";

export default function DocumentPreviewOverlay() {
  const [loading, setLoading] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [meta, setMeta] = createSignal<FileContentResponse | null>(null);
  const [editedSheets, setEditedSheets] = createSignal<SpreadsheetSheetPreview[] | null>(null);
  const [viewMode, setViewMode] = createSignal<"preview" | "source">("preview");
  const [outlineOpen, setOutlineOpen] = createSignal(false);
  const [copiedSource, setCopiedSource] = createSignal(false);
  let bodyScrollRef: HTMLDivElement | undefined;
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  onCleanup(() => {
    if (copyTimer) clearTimeout(copyTimer);
  });

  createEffect(() => {
    const payload = documentViewer();
    setViewMode("preview");
    setOutlineOpen(false);
    setEditedSheets(null);
    setError(null);
    setMeta(null);

    if (!payload || payload.kind !== "file") {
      setLoading(false);
      return;
    }

    const dir = payload.directory || activeDirectory() || workspace.rootDir || undefined;
    const sid = payload.sessionID || workspace.activeSessionID || undefined;
    let cancelled = false;
    onCleanup(() => {
      cancelled = true;
    });

    setLoading(true);
    void readDocumentPreviewFile(payload.path, dir, sid)
      .then((res) => {
        if (cancelled) return;
        setMeta(res);
        setLoading(false);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(errorMessageOf(err, t("timeline.documentLoadFailed")));
        setLoading(false);
      });
  });

  const handleKeyDown = (ev: KeyboardEvent) => {
    if (!documentViewer()) return;
    if (ev.key === "Escape") {
      ev.preventDefault();
      ev.stopImmediatePropagation();
      closeDocumentPreview();
    }
  };
  window.addEventListener("keydown", handleKeyDown, { capture: true });
  onCleanup(() => window.removeEventListener("keydown", handleKeyDown, { capture: true }));

  const resolvedPath = createMemo(() => {
    const p = documentViewer();
    if (!p || p.kind !== "file") return "";
    return meta()?.resolvedPath || p.path;
  });

  const isBinary = createMemo(() => meta()?.type === "binary");
  const isPdf = createMemo(() => {
    const p = documentViewer();
    if (!p || p.kind !== "file") return false;
    return isPdfPath(resolvedPath(), meta()?.mimeType || p.mime);
  });
  const isSpreadsheet = createMemo(() => {
    const p = documentViewer();
    if (!p || p.kind !== "file") return false;
    return (
      Boolean(meta()?.sheets?.length) ||
      isSpreadsheetPath(resolvedPath(), meta()?.mimeType || p.mime)
    );
  });
  const isHtml = createMemo(() => {
    const p = documentViewer();
    if (!p) return false;
    if (p.kind === "html") return true;
    return !isBinary() && isHtmlPreviewPath(resolvedPath(), meta()?.mimeType || p.mime);
  });
  const isMarkdown = createMemo(() => {
    const p = documentViewer();
    if (!p || p.kind !== "file") return false;
    return !isBinary() && isMarkdownPath(resolvedPath(), meta()?.mimeType || p.mime);
  });
  const isOfficeDoc = createMemo(() => {
    const p = documentViewer();
    if (!p || p.kind !== "file") return false;
    return isOfficeDocumentPath(resolvedPath(), meta()?.mimeType || p.mime);
  });

  const sourceText = createMemo(() => {
    const p = documentViewer();
    if (!p) return "";
    if (p.kind === "html") return p.html;
    const m = meta();
    if (!m) return "";
    return m.type === "binary" ? m.extractedText || "" : m.content || "";
  });

  const canToggleSource = createMemo(() => {
    if (isHtml() && Boolean(sourceText())) return true;
    if (isMarkdown() && Boolean(sourceText())) return true;
    return false;
  });

  const markdownHeadings = createMemo(() =>
    isMarkdown() && sourceText() ? extractMarkdownHeadings(sourceText()) : []
  );

  const handleDockToInspector = () => {
    const p = documentViewer();
    if (!p) return;
    if (p.kind === "html") {
      closeDocumentPreview();
      openCanvasPreview({ kind: "html", title: p.title, content: p.html });
      return;
    }
    closeDocumentPreview();
    openFileInInspector(p.path);
  };

  const handleOpenExternal = (action: "open" | "reveal") => {
    const p = documentViewer();
    if (!p || p.kind !== "file") return;
    const target = meta()?.resolvedPath || p.path;
    void openExternalFile(target, {
      directory: p.directory || activeDirectory() || workspace.rootDir || undefined,
      sessionID: p.sessionID || workspace.activeSessionID || undefined,
      action,
    }).catch((err) => {
      console.error(`Failed to ${action} file:`, err);
    });
  };

  const handleCopySource = async () => {
    const text = sourceText();
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopiedSource(true);
      if (copyTimer) clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopiedSource(false), 1500);
    } catch {
      /* ignore */
    }
  };

  return (
    <Presence when={documentViewer() !== null}>
      {(state) => {
        const payload = () => documentViewer();
        return (
          <Portal>
            <div
              data-state={state()}
              class="fixed inset-0 z-[120] flex items-center justify-center bg-black/55 p-4 sm:p-6 backdrop-blur-[2px]"
              onClick={(ev) => {
                if (ev.target === ev.currentTarget) closeDocumentPreview();
              }}
            >
              <div class="flex h-[86vh] w-[min(1120px,94vw)] flex-col overflow-hidden rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] shadow-2xl">
                {/* Header */}
                <div class="flex shrink-0 items-center justify-between gap-3 border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/80 px-4 py-2.5">
                  <div class="flex min-w-0 items-center gap-2.5">
                    <FileIcon
                      node={{
                        path:
                          payload()?.kind === "file"
                            ? resolvedPath()
                            : "preview.html",
                        type: "file",
                      }}
                      class="h-4 w-4 shrink-0"
                    />
                    <div class="min-w-0">
                      <div class="truncate font-mono text-[12.5px] font-medium text-[var(--text-primary)]">
                        {payload()?.title || ""}
                      </div>
                      <Show when={meta()?.size}>
                        <div class="font-mono text-[10.5px] text-[var(--text-muted)]">
                          {formatByteSize(meta()?.size)}
                          {meta()?.mimeType ? ` · ${meta()?.mimeType}` : ""}
                        </div>
                      </Show>
                    </div>
                  </div>

                  <div class="flex shrink-0 items-center gap-1.5">
                    <Show when={canToggleSource()}>
                      <div class="inline-flex items-center rounded-lg bg-[var(--bg-inset)] p-0.5">
                        <button
                          type="button"
                          onClick={() => setViewMode("preview")}
                          class={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] transition-colors cursor-pointer ${
                            viewMode() === "preview"
                              ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                              : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                          }`}
                        >
                          <Eye size={12} />
                          <span>{t("inspector.files.previewMode")}</span>
                        </button>
                        <button
                          type="button"
                          onClick={() => setViewMode("source")}
                          class={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] transition-colors cursor-pointer ${
                            viewMode() === "source"
                              ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                              : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                          }`}
                        >
                          <Code2 size={12} />
                          <span>{t("inspector.files.sourceMode")}</span>
                        </button>
                      </div>
                    </Show>

                    <Show when={viewMode() === "preview" && markdownHeadings().length >= 2}>
                      <button
                        type="button"
                        onClick={() => setOutlineOpen((v) => !v)}
                        class={`inline-flex items-center gap-1 rounded-lg px-2 py-1 text-[11px] cursor-pointer ${
                          outlineOpen()
                            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                            : "bg-[var(--bg-inset)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                        }`}
                      >
                        <ListTree size={12} />
                        <span>{t("inspector.files.outlineCount", { count: markdownHeadings().length })}</span>
                      </button>
                    </Show>

                    <Show when={Boolean(sourceText())}>
                      <button
                        type="button"
                        onClick={() => void handleCopySource()}
                        title={copiedSource() ? t("common.copied") : t("common.copy")}
                        class="inline-flex items-center justify-center rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                      >
                        <Show when={copiedSource()} fallback={<Copy size={14} />}>
                          <Check size={14} class="text-[var(--ok-text)]" />
                        </Show>
                      </button>
                    </Show>

                    <Show when={payload()?.kind === "html" && !isTesterPortalMode()}>
                      <button
                        type="button"
                        onClick={handleDockToInspector}
                        title={t("timeline.dockHtmlPreview")}
                        class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:border-[var(--accent-border)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                      >
                        <PanelRightOpen size={13} />
                        <span class="hidden sm:inline">{t("timeline.dockToInspector")}</span>
                      </button>
                    </Show>

                    <Show when={payload()?.kind === "file" && !isTesterPortalMode()}>
                      <button
                        type="button"
                        onClick={() => handleOpenExternal("reveal")}
                        title={t("inspector.files.revealInFinder")}
                        class="inline-flex items-center justify-center rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                      >
                        <FolderOpen size={14} />
                      </button>
                      <button
                        type="button"
                        onClick={() => handleOpenExternal("open")}
                        title={t("inspector.files.openInDefaultApp")}
                        class="inline-flex items-center justify-center rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                      >
                        <ExternalLink size={14} />
                      </button>
                      <button
                        type="button"
                        onClick={handleDockToInspector}
                        title={t("timeline.dockToInspector")}
                        class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:border-[var(--accent-border)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                      >
                        <PanelRightOpen size={13} />
                        <span class="hidden sm:inline">{t("timeline.dockToInspector")}</span>
                      </button>
                    </Show>

                    <button
                      type="button"
                      onClick={() => closeDocumentPreview()}
                      aria-label={t("common.close")}
                      title={t("common.close")}
                      class="ml-0.5 inline-flex items-center justify-center rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                    >
                      <X size={16} />
                    </button>
                  </div>
                </div>

                {/* Body */}
                <div ref={bodyScrollRef} class="flex-1 min-h-0 overflow-auto bg-[var(--bg-code)]">
                  <Show when={loading()}>
                    <div class="flex h-full items-center justify-center text-[var(--text-muted)]">
                      <LoaderCircle size={20} class="animate-spin" />
                    </div>
                  </Show>

                  <Show when={!loading() && error()}>
                    <div class="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
                      <div class="max-w-lg font-mono text-[12px] text-[var(--danger-text)] break-all">
                        {error()}
                      </div>
                      <Show when={payload()?.kind === "file" && !isTesterPortalMode()}>
                        <button
                          type="button"
                          onClick={handleDockToInspector}
                          class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                        >
                          <PanelRightOpen size={13} />
                          <span>{t("timeline.dockToInspector")}</span>
                        </button>
                      </Show>
                    </div>
                  </Show>

                  <Show when={!loading() && !error()}>
                    {/* 1. Sandboxed HTML Preview (iframe srcdoc) */}
                    <Show
                      when={isHtml() && viewMode() === "preview" && Boolean(sourceText())}
                      fallback={
                        /* 2. Native PDF Preview (Blob URL iframe) */
                        <Show
                          when={isPdf() && Boolean(meta()?.content)}
                          fallback={
                            /* 3. Interactive Spreadsheet Preview (.xlsx / .csv / .tsv) */
                            <Show
                              when={isSpreadsheet() && (meta()?.sheets?.length ?? 0) > 0}
                              fallback={
                                /* 4. Markdown or Office Document Reader */
                                <Show
                                  when={
                                    viewMode() === "preview" &&
                                    (isMarkdown() || (isOfficeDoc() && Boolean(sourceText())))
                                  }
                                  fallback={
                                    /* 5. Source Code / Plain Text Fallback */
                                    <Show
                                      when={Boolean(sourceText())}
                                      fallback={
                                        <div class="flex h-full flex-col items-center justify-center gap-3 p-6 text-center text-[13px] text-[var(--text-muted)]">
                                          <div>{t("inspector.files.binaryFileTitle")}</div>
                                          <Show when={payload()?.kind === "file" && !isTesterPortalMode()}>
                                            <button
                                              type="button"
                                              onClick={() => handleOpenExternal("open")}
                                              class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-1.5 text-[12px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
                                            >
                                              <ExternalLink size={13} />
                                              <span>{t("inspector.files.openInDefaultApp")}</span>
                                            </button>
                                          </Show>
                                        </div>
                                      }
                                    >
                                      <pre
                                        class="p-4 font-mono text-[12.5px] leading-relaxed text-[var(--text-primary)] whitespace-pre-wrap break-words selectable-text"
                                      >
                                        {sourceText()}
                                      </pre>
                                    </Show>
                                  }
                                >
                                  <MarkdownDocumentReader
                                    content={sourceText()}
                                    baseFilePath={resolvedPath()}
                                    headings={markdownHeadings()}
                                    outlineOpen={outlineOpen()}
                                    onToggleOutline={setOutlineOpen}
                                    scrollContainer={() => bodyScrollRef}
                                  />
                                </Show>
                              }
                            >
                              <SpreadsheetPreviewView
                                sheets={editedSheets() ?? meta()?.sheets ?? []}
                                filePath={resolvedPath()}
                                canEdit={
                                  !isTesterPortalMode() &&
                                  !meta()?.outsideWorkspace &&
                                  /\.(?:xlsx|csv|tsv)$/i.test(resolvedPath())
                                }
                                onSaveCell={async (sheetName, cellRef, value) => {
                                  const p = documentViewer();
                                  if (!p || p.kind !== "file") return;
                                  const res = await updateInspectedSpreadsheetCells({
                                    path: resolvedPath(),
                                    directory:
                                      p.directory || activeDirectory() || workspace.rootDir || undefined,
                                    sessionID: p.sessionID || workspace.activeSessionID || undefined,
                                    sheet: sheetName,
                                    edits: { [cellRef]: value },
                                  });
                                  if (res?.sheets?.length) {
                                    setEditedSheets(res.sheets);
                                  }
                                }}
                              />
                            </Show>
                          }
                        >
                          <PdfPreviewFrame
                            base64Content={meta()?.content || ""}
                            filePath={resolvedPath()}
                          />
                        </Show>
                      }
                    >
                      <SandboxedHtmlPreview
                        html={sourceText()}
                        title={payload()?.title || "HTML"}
                      />
                    </Show>
                  </Show>
                </div>
              </div>
            </div>
          </Portal>
        );
      }}
    </Presence>
  );
}
