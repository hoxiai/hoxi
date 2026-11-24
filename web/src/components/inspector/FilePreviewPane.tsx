import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import {
  ArrowLeft,
  Check,
  ChevronLeft,
  ChevronRight,
  Code2,
  Copy,
  ExternalLink,
  Eye,
  FileCode2,
  FolderOpen,
  GitBranch,
  ListTree,
  LoaderCircle,
  Search,
} from "lucide-solid";
import type { FileContentResponse, SpreadsheetSheetPreview } from "../../services/api";
import type { SnapshotFileDiff } from "../../services/sse-types";
import {
  isLoopbackMediaURL,
  openDiffInInspector,
  openExternalFile,
  openImagePreview,
  resolveMediaPlaybackURL,
  setWorkspace,
  updateInspectedSpreadsheetCells,
  workspace,
} from "../../store/workspace";
import { DiffChanges, FileIcon } from "../icons";
import { computeChangedFileLines } from "../timeline/file-diff-lines";
import { extractMarkdownHeadings } from "../timeline/MarkdownView";
import { highlightCode, fileLanguage, type HighlightedCode } from "../timeline/syntax-highlight";
import { t } from "../../i18n";
import {
  formatByteSize,
  isAudioPreviewable,
  isHtmlPath,
  isImagePreviewable,
  isJsonPath,
  isMarkdownPath,
  isOfficeDocumentPath,
  isPdfPath,
  isSpreadsheetPath,
  isSvgPath,
  isVideoPreviewable,
} from "./file-tree-helpers";
import MarkdownDocumentReader from "./MarkdownDocumentReader";
import { PdfPreviewFrame, SpreadsheetPreviewView } from "./SpreadsheetPreviewView";
import SandboxedHtmlPreview from "../preview/SandboxedHtmlPreview";
import FileSourceCodeView from "./FileSourceCodeView";
import JsonTreeView, { tryParseStructuredJson } from "../timeline/JsonTreeView";

export interface FilePreviewPaneProps {
  previewPath: string;
  resolvedDiffPath: string;
  previewMeta: FileContentResponse | null;
  previewContent: string;
  previewError: string | null;
  loadingPreview: boolean;
  activeDiff: SnapshotFileDiff | undefined;
  highlightedLine: number | null;
  viewMode: "preview" | "source";
  onChangeViewMode: (mode: "preview" | "source") => void;
  targetDirectory: string | undefined;
  canGoBack?: boolean;
  canGoForward?: boolean;
  onNavigateBack?: () => void;
  onNavigateForward?: () => void;
  onBack: () => void;
}

export default function FilePreviewPane(props: FilePreviewPaneProps) {
  const [highlighted, setHighlighted] = createSignal<HighlightedCode>();
  const [copiedPath, setCopiedPath] = createSignal(false);
  const [copiedContent, setCopiedContent] = createSignal(false);
  const [outlineOpen, setOutlineOpen] = createSignal(false);
  const [searchOpen, setSearchOpen] = createSignal(false);
  const [editedSheets, setEditedSheets] = createSignal<SpreadsheetSheetPreview[] | null>(null);
  const [mediaError, setMediaError] = createSignal(false);
  let rootContainer: HTMLDivElement | undefined;
  let sourceContainer: HTMLDivElement | undefined;
  let drillPhase = false;
  const drillEnterClass = createMemo(() => {
    void props.previewPath;
    drillPhase = !drillPhase;
    return drillPhase ? "hoxi-drill-enter-a" : "hoxi-drill-enter-b";
  });

  createEffect(() => {
    void props.previewPath;
    void props.previewMeta;
    setEditedSheets(null);
    setMediaError(false);
    setSearchOpen(false);
  });

  const isBinaryPreview = createMemo(() => props.previewMeta?.type === "binary");
  const isImageFile = createMemo(() =>
    Boolean(props.previewPath && isImagePreviewable(props.previewPath, props.previewMeta))
  );
  const isVideoFile = createMemo(() => {
    const p = props.previewMeta?.resolvedPath || props.previewPath || "";
    return Boolean(p && isVideoPreviewable(p, props.previewMeta?.mimeType));
  });
  const isAudioFile = createMemo(() => {
    const p = props.previewMeta?.resolvedPath || props.previewPath || "";
    return Boolean(p && !isVideoFile() && isAudioPreviewable(p, props.previewMeta?.mimeType));
  });
  const mediaStreamUrl = createMemo(() => {
    if (!isVideoFile() && !isAudioFile()) return "";
    const p = props.previewMeta?.resolvedPath || props.previewPath || "";
    if (!p) return "";
    const params = new URLSearchParams({ path: p, raw: "1" });
    if (props.targetDirectory) params.set("directory", props.targetDirectory);
    if (workspace.activeSessionID) params.set("sessionID", workspace.activeSessionID);
    return resolveMediaPlaybackURL(`/api/hoxi/file/content?${params.toString()}`);
  });
  const isPdfFile = createMemo(() => {
    const p = props.previewMeta?.resolvedPath || props.previewPath || "";
    return isPdfPath(p, props.previewMeta?.mimeType);
  });
  const isSpreadsheetFile = createMemo(() => {
    const p = props.previewMeta?.resolvedPath || props.previewPath || "";
    return (
      Boolean(props.previewMeta?.sheets?.length) ||
      isSpreadsheetPath(p, props.previewMeta?.mimeType)
    );
  });
  const isOfficeDocFile = createMemo(() => {
    const p = props.previewMeta?.resolvedPath || props.previewPath || "";
    return isOfficeDocumentPath(p, props.previewMeta?.mimeType);
  });
  const isMarkdownFile = createMemo(() => {
    const p = props.previewMeta?.resolvedPath || props.previewPath || "";
    return !isBinaryPreview() && isMarkdownPath(p, props.previewMeta?.mimeType);
  });
  const isHtmlFile = createMemo(() => {
    const p = props.previewMeta?.resolvedPath || props.previewPath || "";
    return !isBinaryPreview() && isHtmlPath(p, props.previewMeta?.mimeType);
  });
  const isJsonFile = createMemo(() => {
    const p = props.previewMeta?.resolvedPath || props.previewPath || "";
    return !isBinaryPreview() && isJsonPath(p, props.previewMeta?.mimeType);
  });
  const effectiveSourceText = createMemo(() => {
    if (!isBinaryPreview()) return props.previewContent;
    return props.previewMeta?.extractedText || "";
  });
  const parsedJsonData = createMemo(() =>
    isJsonFile() ? tryParseStructuredJson(effectiveSourceText()) : null
  );
  const canTogglePreview = createMemo(() => {
    const p = props.previewMeta?.resolvedPath || props.previewPath || "";
    const mime = props.previewMeta?.mimeType;
    if (!isBinaryPreview() && (isMarkdownPath(p, mime) || isHtmlPath(p, mime) || isSvgPath(p, mime))) {
      return true;
    }
    if (isJsonFile() && Boolean(parsedJsonData())) {
      return true;
    }
    if (Boolean(props.previewMeta?.sheets?.length) && Boolean(effectiveSourceText())) {
      return true;
    }
    if (isPdfFile() && Boolean(props.previewContent) && Boolean(effectiveSourceText())) {
      return true;
    }
    return false;
  });
  const markdownHeadings = createMemo(() =>
    isMarkdownFile() && props.previewContent
      ? extractMarkdownHeadings(props.previewContent)
      : []
  );

  const imageDataUrl = createMemo(() => {
    const p = props.previewPath || "";
    const meta = props.previewMeta;
    const content = props.previewContent;
    if (!content) return "";
    const mime = meta?.mimeType || (isSvgPath(p) ? "image/svg+xml" : "image/png");
    if (meta?.type === "binary" || meta?.encoding === "base64") {
      return `data:${mime};base64,${content}`;
    }
    if (isSvgPath(p, mime)) {
      return `data:image/svg+xml;utf8,${encodeURIComponent(content)}`;
    }
    return "";
  });

  const sourceLines = createMemo(() => {
    const text = effectiveSourceText();
    return text ? text.split("\n") : [];
  });
  const INITIAL_SOURCE_LINE_LIMIT = 600;
  const SOURCE_LINE_BATCH = 600;
  const [renderedLineLimit, setRenderedLineLimit] = createSignal(INITIAL_SOURCE_LINE_LIMIT);
  const visibleSourceLines = createMemo(() => {
    const lines = sourceLines();
    const limit = renderedLineLimit();
    return lines.length > limit ? lines.slice(0, limit) : lines;
  });
  const changedLines = createMemo(() =>
    computeChangedFileLines(props.activeDiff, sourceLines())
  );

  createEffect(() => {
    void props.previewPath;
    setRenderedLineLimit(INITIAL_SOURCE_LINE_LIMIT);
  });

  const handleSourceScroll = (ev: Event) => {
    const el = ev.currentTarget as HTMLElement | null;
    if (!el) return;
    const total = sourceLines().length;
    if (renderedLineLimit() >= total) return;
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 480) {
      setRenderedLineLimit((prev) => Math.min(total, prev + SOURCE_LINE_BATCH));
    }
  };

  createEffect(() => {
    const code = effectiveSourceText();
    if (!code || isBinaryPreview()) {
      setHighlighted(undefined);
      return;
    }
    const language = fileLanguage(props.previewMeta?.resolvedPath || props.previewPath || "");
    const theme = workspace.theme === "dark" ? "dark" : "light";
    let cancelled = false;
    setHighlighted(undefined);
    void highlightCode(code, language, theme).then((result) => {
      if (!cancelled) setHighlighted(result);
    });
    onCleanup(() => {
      cancelled = true;
    });
  });

  createEffect(() => {
    const path = props.previewPath;
    if (!path || props.loadingPreview || !sourceContainer || props.viewMode !== "source") return;
    const changed = changedLines();
    const firstChanged = changed.size > 0 ? Math.min(...changed) : null;
    const line = workspace.inspectedFileLine || props.highlightedLine || firstChanged;
    if (!line) return;
    if (line > renderedLineLimit()) {
      setRenderedLineLimit(Math.min(sourceLines().length, line + 200));
    }
    requestAnimationFrame(() => {
      const target = sourceContainer?.querySelector<HTMLElement>(`[data-line="${line}"]`);
      target?.scrollIntoView({ block: "center", behavior: "smooth" });
    });
    if (workspace.inspectedFileLine) {
      setWorkspace("inspectedFileLine", null);
    }
  });

  const handleCopyPath = async () => {
    const target = props.previewMeta?.resolvedPath || props.previewPath;
    if (!target) return;
    try {
      await navigator.clipboard.writeText(target);
      setCopiedPath(true);
      setTimeout(() => setCopiedPath(false), 1500);
    } catch {
      /* ignore */
    }
  };

  const handleCopyContent = async () => {
    const content = effectiveSourceText();
    if (!content) return;
    try {
      await navigator.clipboard.writeText(content);
      setCopiedContent(true);
      setTimeout(() => setCopiedContent(false), 1500);
    } catch {
      /* ignore */
    }
  };

  const handleOpenExternal = (action: "open" | "reveal") => {
    const target = props.previewMeta?.resolvedPath || props.previewPath;
    if (!target) return;
    void openExternalFile(target, {
      directory: props.targetDirectory,
      sessionID: workspace.activeSessionID || undefined,
      action,
    }).catch((err) => {
      console.error(`Failed to ${action} file:`, err);
    });
  };

  const renderExternalOpenButtons = () => (
    <div class="flex items-center gap-2 pt-1">
      <button
        type="button"
        onClick={() => handleOpenExternal("reveal")}
        class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
      >
        <FolderOpen size={13} />
        <span>{t("inspector.files.revealInFinder")}</span>
      </button>
      <button
        type="button"
        onClick={() => handleOpenExternal("open")}
        class="inline-flex items-center gap-1.5 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-1.5 text-[12px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
      >
        <ExternalLink size={13} />
        <span>{t("inspector.files.openInDefaultApp")}</span>
      </button>
    </div>
  );

  const showSourceView = createMemo(
    () =>
      !props.loadingPreview &&
      !props.previewError &&
      Boolean(effectiveSourceText()) &&
      !(isImageFile() && (isBinaryPreview() || props.viewMode === "preview") && imageDataUrl()) &&
      !(props.viewMode === "preview" && isSpreadsheetFile() && (props.previewMeta?.sheets?.length ?? 0) > 0) &&
      !(props.viewMode === "preview" && isPdfFile() && Boolean(props.previewContent)) &&
      !((isVideoFile() || isAudioFile()) && Boolean(mediaStreamUrl())) &&
      !(props.viewMode === "preview" && isHtmlFile()) &&
      !(props.viewMode === "preview" && Boolean(parsedJsonData())) &&
      !(props.viewMode === "preview" && (isMarkdownFile() || isOfficeDocFile()))
  );

  // Scoped Cmd/Ctrl+F inside FilePreviewPane opens in-file search without hijacking chat search.
  const handleRootKeyDown = (ev: KeyboardEvent) => {
    if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === "f" && showSourceView()) {
      ev.preventDefault();
      ev.stopPropagation();
      setSearchOpen(true);
    }
  };

  return (
    <div
      ref={rootContainer}
      tabIndex={-1}
      onKeyDown={handleRootKeyDown}
      class={`flex-1 min-h-0 flex flex-col outline-none ${drillEnterClass()}`}
    >
      {/* Preview Header Toolbar */}
      <div class="shrink-0 flex items-center gap-1.5 px-2.5 py-1.5">
        <button
          type="button"
          class="hoxi-press inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer shrink-0"
          onClick={() => props.onBack()}
          title={t("inspector.files.backToTree")}
        >
          <ArrowLeft size={12} />
          {t("inspector.files.back")}
        </button>

        <Show when={props.canGoBack || props.canGoForward}>
          <div class="shrink-0 inline-flex items-center rounded-md bg-[var(--bg-inset)] p-0.5">
            <button
              type="button"
              disabled={!props.canGoBack}
              onClick={() => props.onNavigateBack?.()}
              title={t("inspector.files.historyBack")}
              class="hoxi-press rounded p-0.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-35 cursor-pointer"
            >
              <ChevronLeft size={12} />
            </button>
            <button
              type="button"
              disabled={!props.canGoForward}
              onClick={() => props.onNavigateForward?.()}
              title={t("inspector.files.historyForward")}
              class="hoxi-press rounded p-0.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-35 cursor-pointer"
            >
              <ChevronRight size={12} />
            </button>
          </div>
        </Show>

        <FileIcon
          node={{ path: props.previewMeta?.resolvedPath || props.previewPath, type: "file" }}
          class="w-4 h-4 shrink-0"
        />
        <span
          class="flex-1 truncate font-mono text-[11px] font-medium text-[var(--text-primary)]"
          title={props.previewMeta?.resolvedPath || props.previewPath}
        >
          {props.previewPath}
        </span>

        <Show when={props.previewMeta?.outsideWorkspace}>
          <span
            class="shrink-0 inline-flex items-center rounded bg-amber-500/15 px-1.5 py-0.5 text-[11px] font-mono font-medium text-amber-400"
            title={`${t("inspector.files.externalTooltip")}: ${props.previewMeta?.resolvedPath || props.previewPath}`}
          >
            {t("inspector.files.externalBadge")}
          </span>
        </Show>

        <Show when={props.activeDiff?.repo}>
          <span
            class="shrink-0 inline-flex items-center gap-1 rounded-md bg-[var(--accent-surface)] px-1.5 py-0.5 text-[11px] font-mono text-[var(--accent-primary)]"
            title={
              props.activeDiff!.branch
                ? `${props.activeDiff!.repo} (${props.activeDiff!.branch})`
                : props.activeDiff!.repo
            }
          >
            <GitBranch size={10} />
            <span>
              {props.activeDiff!.repo}
              {props.activeDiff!.branch ? `:${props.activeDiff!.branch}` : ""}
            </span>
          </span>
        </Show>

        {/* Markdown / SVG Preview vs Source Switcher */}
        <Show when={canTogglePreview()}>
          <div class="shrink-0 inline-flex items-center rounded-md bg-[var(--bg-inset)] p-0.5">
            <button
              type="button"
              onClick={() => props.onChangeViewMode("preview")}
              class={`hoxi-press inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] cursor-pointer ${
                props.viewMode === "preview"
                  ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
              title={t("inspector.files.previewMode")}
            >
              <Eye size={11} />
              <span>{t("inspector.files.previewMode")}</span>
            </button>
            <button
              type="button"
              onClick={() => props.onChangeViewMode("source")}
              class={`hoxi-press inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] cursor-pointer ${
                props.viewMode === "source"
                  ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
              title={t("inspector.files.sourceMode")}
            >
              <Code2 size={11} />
              <span>{t("inspector.files.sourceMode")}</span>
            </button>
          </div>
        </Show>

        {/* Markdown Outline (TOC) Toggle */}
        <Show when={props.viewMode === "preview" && markdownHeadings().length >= 2}>
          <button
            type="button"
            onClick={() => setOutlineOpen((v) => !v)}
            class={`hoxi-press shrink-0 inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] cursor-pointer ${
              outlineOpen()
                ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                : "bg-[var(--bg-inset)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
            }`}
            title={t("inspector.files.outlineTitle")}
          >
            <ListTree size={11} />
            <span>
              {t("inspector.files.outlineCount", { count: markdownHeadings().length })}
            </span>
          </button>
        </Show>

        {/* Quick Action Icons: Search in File, Copy Path, Copy Content, Reveal in Finder, Open in Default App */}
        <div class="shrink-0 inline-flex items-center gap-0.5">
          <Show when={showSourceView()}>
            <button
              type="button"
              onClick={() => setSearchOpen((v) => !v)}
              class={`inline-flex items-center justify-center rounded p-1 transition-colors cursor-pointer ${
                searchOpen()
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
              }`}
              title={t("inspector.files.searchInFile")}
            >
              <Search size={12} />
            </button>
          </Show>
          <button
            type="button"
            onClick={handleCopyPath}
            class="inline-flex items-center justify-center rounded p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={copiedPath() ? t("inspector.files.copiedPath") : t("inspector.files.copyPath")}
          >
            <Show when={copiedPath()} fallback={<FileCode2 size={12} />}>
              <Check size={12} class="text-[var(--ok-text)]" />
            </Show>
          </button>
          <Show when={Boolean(effectiveSourceText())}>
            <button
              type="button"
              onClick={handleCopyContent}
              class="inline-flex items-center justify-center rounded p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              title={copiedContent() ? t("inspector.files.copiedContent") : t("inspector.files.copyContent")}
            >
              <Show when={copiedContent()} fallback={<Copy size={12} />}>
                <Check size={12} class="text-[var(--ok-text)]" />
              </Show>
            </button>
          </Show>
          <button
            type="button"
            onClick={() => handleOpenExternal("reveal")}
            class="inline-flex items-center justify-center rounded p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={t("inspector.files.revealInFinder")}
          >
            <FolderOpen size={12} />
          </button>
          <button
            type="button"
            onClick={() => handleOpenExternal("open")}
            class="inline-flex items-center justify-center rounded p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={t("inspector.files.openInDefaultApp")}
          >
            <ExternalLink size={12} />
          </button>
        </div>

        <Show
          when={
            props.activeDiff &&
            ((props.activeDiff!.additions ?? 0) > 0 || (props.activeDiff!.deletions ?? 0) > 0)
          }
        >
          <button
            type="button"
            onClick={() => openDiffInInspector(props.activeDiff || props.resolvedDiffPath)}
            class="shrink-0 inline-flex items-center gap-1.5 rounded-md bg-[var(--bg-inset)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--accent-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={t("toolCard.openInDiffInspector")}
          >
            <DiffChanges
              changes={{
                additions: props.activeDiff!.additions ?? 0,
                deletions: props.activeDiff!.deletions ?? 0,
              }}
            />
          </button>
        </Show>
      </div>

      {/* Preview Body */}
      <div
        ref={sourceContainer}
        onScroll={handleSourceScroll}
        class="flex-1 min-h-0 overflow-auto bg-[var(--bg-code)]"
      >
        <Show when={props.loadingPreview}>
          <div class="flex items-center justify-center py-8 text-[var(--text-muted)]">
            <LoaderCircle size={16} class="animate-spin" />
          </div>
        </Show>
        <Show when={props.previewError}>
          <div class="px-4 py-5 space-y-3">
            <div class="font-mono text-[12px] text-[var(--danger-text)] break-all">
              {props.previewError}
            </div>
            <div class="flex items-center gap-2">
              <button
                type="button"
                onClick={() => handleOpenExternal("reveal")}
                class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <FolderOpen size={12} />
                <span>{t("inspector.files.revealInFinder")}</span>
              </button>
              <button
                type="button"
                onClick={() => handleOpenExternal("open")}
                class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <ExternalLink size={12} />
                <span>{t("inspector.files.openInDefaultApp")}</span>
              </button>
            </div>
          </div>
        </Show>
        <Show when={!props.loadingPreview && !props.previewError}>
          {/* 1. Image Preview (Binary images or SVG in preview mode) */}
          <Show
            when={
              isImageFile() &&
              (isBinaryPreview() || props.viewMode === "preview") &&
              imageDataUrl()
            }
            fallback={
              /* 2. Spreadsheet Interactive Table Preview (.xlsx / .xls / .ods / .csv / .tsv) */
              <Show
                when={
                  props.viewMode === "preview" &&
                  isSpreadsheetFile() &&
                  (props.previewMeta?.sheets?.length ?? 0) > 0
                }
                fallback={
                  /* 3. Native PDF Preview */
                  <Show
                    when={props.viewMode === "preview" && isPdfFile() && Boolean(props.previewContent)}
                    fallback={
                      /* 3.5. Streaming Video / Audio Preview */
                      <Show
                        when={(isVideoFile() || isAudioFile()) && Boolean(mediaStreamUrl())}
                        fallback={
                          /* 4. Non-previewable Binary File Card (when no extractedText or in binary fallback) */
                          <Show
                            when={isBinaryPreview() && !effectiveSourceText()}
                        fallback={
                          /* 5. Sandboxed HTML or Markdown / Extracted Office Document Reader */
                          <Show
                            when={props.viewMode === "preview" && isHtmlFile() && Boolean(effectiveSourceText())}
                            fallback={
                          <Show
                            when={
                              props.viewMode === "preview" &&
                              (isMarkdownFile() || (isOfficeDocFile() && Boolean(effectiveSourceText())))
                            }
                            fallback={
                              /* 6. Structured JSON Tree (preview mode) or Highlighted Source View */
                              <Show
                                when={props.viewMode === "preview" && parsedJsonData()}
                                fallback={
                                  <FileSourceCodeView
                                    filePath={props.previewPath}
                                    sourceLines={sourceLines()}
                                    visibleLines={visibleSourceLines()}
                                    highlighted={highlighted()}
                                    changedLines={changedLines()}
                                    highlightedLine={props.highlightedLine}
                                    searchOpen={searchOpen()}
                                    onCloseSearch={() => setSearchOpen(false)}
                                    onEnsureLineVisible={(line) => {
                                      if (line > renderedLineLimit()) {
                                        setRenderedLineLimit(Math.min(sourceLines().length, line + 200));
                                      }
                                    }}
                                    scrollContainer={() => sourceContainer}
                                  />
                                }
                              >
                                {(jsonObj) => (
                                  <div class="p-3">
                                    <JsonTreeView data={jsonObj()} rawText={effectiveSourceText()} fullHeight={true} />
                                  </div>
                                )}
                              </Show>
                            }
                          >
                            <MarkdownDocumentReader
                              content={effectiveSourceText()}
                              baseFilePath={props.previewPath}
                              headings={markdownHeadings()}
                              outlineOpen={outlineOpen()}
                              onToggleOutline={setOutlineOpen}
                              scrollContainer={() => sourceContainer}
                            />
                          </Show>
                            }
                          >
                            <SandboxedHtmlPreview
                              html={effectiveSourceText()}
                              title={props.previewPath}
                              compactToolbar={true}
                            />
                          </Show>
                        }
                      >
                        <div class="flex flex-col items-center justify-center py-12 px-4 text-center space-y-3">
                          <FileIcon
                            node={{ path: props.previewMeta?.resolvedPath || props.previewPath, type: "file" }}
                            class="w-10 h-10 opacity-80"
                          />
                          <div class="text-[13px] font-medium text-[var(--text-primary)]">
                            {t("inspector.files.binaryFileTitle")}
                          </div>
                          <div class="text-[11px] font-mono text-[var(--text-muted)]">
                            {t("inspector.files.binaryFileDesc", {
                              size: formatByteSize(props.previewMeta?.size),
                              mimeType: props.previewMeta?.mimeType || "application/octet-stream",
                            })}
                          </div>
                          {renderExternalOpenButtons()}
                        </div>
                      </Show>
                        }
                      >
                        <div class="flex flex-col items-center justify-center p-6 min-h-full bg-[var(--bg-inset)] space-y-3">
                          <Show
                            when={!mediaError()}
                            fallback={
                              <div class="text-[12px] text-[var(--text-secondary)] text-center max-w-sm">
                                {t("timeline.videoPlaybackFailed")}
                              </div>
                            }
                          >
                            <Show
                              when={isVideoFile()}
                              fallback={
                                <audio
                                  controls
                                  preload="metadata"
                                  crossOrigin={isLoopbackMediaURL(mediaStreamUrl()) ? "anonymous" : undefined}
                                  src={mediaStreamUrl()}
                                  onError={() => setMediaError(true)}
                                  class="w-full max-w-md"
                                />
                              }
                            >
                              <video
                                controls
                                preload="metadata"
                                playsinline
                                crossOrigin={isLoopbackMediaURL(mediaStreamUrl()) ? "anonymous" : undefined}
                                src={mediaStreamUrl()}
                                onError={() => setMediaError(true)}
                                class="max-w-full max-h-[70vh] rounded-lg border border-[var(--border-subtle)] bg-black object-contain shadow-sm"
                              />
                            </Show>
                          </Show>
                          {renderExternalOpenButtons()}
                        </div>
                      </Show>
                    }
                  >
                    <PdfPreviewFrame
                      base64Content={props.previewContent}
                      filePath={props.previewPath}
                    />
                  </Show>
                }
              >
                <SpreadsheetPreviewView
                  sheets={editedSheets() ?? props.previewMeta?.sheets ?? []}
                  filePath={props.previewPath}
                  canEdit={
                    !props.previewMeta?.outsideWorkspace &&
                    /\.(?:xlsx|csv|tsv)$/i.test(props.previewMeta?.resolvedPath || props.previewPath || "")
                  }
                  onSaveCell={async (sheetName, cellRef, value) => {
                    const target = props.previewMeta?.resolvedPath || props.previewPath;
                    if (!target) return;
                    const res = await updateInspectedSpreadsheetCells({
                      path: target,
                      directory: props.targetDirectory,
                      sessionID: workspace.activeSessionID || undefined,
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
            <div class="flex flex-col items-center justify-center p-6 min-h-full bg-[var(--bg-inset)]">
              <button
                type="button"
                onClick={() =>
                  openImagePreview({
                    src: imageDataUrl(),
                    alt: props.previewPath || "",
                  })
                }
                title={t("timeline.previewImageTooltip")}
                class="cursor-zoom-in rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-2 shadow-sm transition-colors hover:border-[var(--accent-border)]"
              >
                <img
                  src={imageDataUrl()}
                  alt={props.previewPath || ""}
                  class="max-w-full max-h-[72vh] object-contain"
                />
              </button>
              <Show when={props.previewMeta?.size}>
                <div class="mt-2 text-[11px] font-mono text-[var(--text-muted)]">
                  {formatByteSize(props.previewMeta?.size)}
                  {props.previewMeta?.mimeType ? ` · ${props.previewMeta?.mimeType}` : ""}
                </div>
              </Show>
            </div>
          </Show>
        </Show>
      </div>
    </div>
  );
}
