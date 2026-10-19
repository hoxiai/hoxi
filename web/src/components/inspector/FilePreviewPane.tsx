import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  ArrowLeft,
  Check,
  Code2,
  Copy,
  ExternalLink,
  Eye,
  FileCode2,
  FolderOpen,
  GitBranch,
  LoaderCircle,
} from "lucide-solid";
import { hoxiAPI, type FileContentResponse } from "../../services/api";
import type { SnapshotFileDiff } from "../../services/sse-types";
import { openDiffInInspector, setWorkspace, workspace } from "../../store/workspace";
import { DiffChanges, FileIcon } from "../icons";
import { computeChangedFileLines } from "../timeline/file-diff-lines";
import MarkdownView from "../timeline/MarkdownView";
import { highlightCode, fileLanguage, type HighlightedCode } from "../timeline/syntax-highlight";
import { t } from "../../i18n";
import {
  formatByteSize,
  isImagePreviewable,
  isMarkdownPath,
  isSvgPath,
} from "./file-tree-helpers";

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
  onBack: () => void;
}

export default function FilePreviewPane(props: FilePreviewPaneProps) {
  const [highlighted, setHighlighted] = createSignal<HighlightedCode>();
  const [copiedPath, setCopiedPath] = createSignal(false);
  const [copiedContent, setCopiedContent] = createSignal(false);
  let sourceContainer: HTMLDivElement | undefined;
  let drillPhase = false;
  const drillEnterClass = createMemo(() => {
    void props.previewPath;
    drillPhase = !drillPhase;
    return drillPhase ? "hoxi-drill-enter-a" : "hoxi-drill-enter-b";
  });

  const isBinaryPreview = createMemo(() => props.previewMeta?.type === "binary");
  const isImageFile = createMemo(() =>
    Boolean(props.previewPath && isImagePreviewable(props.previewPath, props.previewMeta))
  );
  const canTogglePreview = createMemo(() => {
    const p = props.previewMeta?.resolvedPath || props.previewPath || "";
    const mime = props.previewMeta?.mimeType;
    return !isBinaryPreview() && (isMarkdownPath(p, mime) || isSvgPath(p, mime));
  });

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

  const sourceLines = createMemo(() =>
    isBinaryPreview() ? [] : props.previewContent.split("\n")
  );
  const changedLines = createMemo(() =>
    computeChangedFileLines(props.activeDiff, sourceLines())
  );

  createEffect(() => {
    const code = props.previewContent;
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
    const content = props.previewContent;
    if (!content || isBinaryPreview()) return;
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
    void hoxiAPI
      .openExternalFile(target, {
        directory: props.targetDirectory,
        sessionID: workspace.activeSessionID || undefined,
        action,
      })
      .catch((err) => {
        console.error(`Failed to ${action} file:`, err);
      });
  };

  return (
    <div class={`flex-1 min-h-0 flex flex-col ${drillEnterClass()}`}>
      {/* Preview Header Toolbar */}
      <div class="shrink-0 flex items-center gap-1.5 px-3 py-2 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]/60">
        <button
          type="button"
          class="hoxi-press inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer shrink-0"
          onClick={() => props.onBack()}
          title={t("inspector.files.back")}
        >
          <ArrowLeft size={12} />
          {t("inspector.files.back")}
        </button>
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
            class="shrink-0 inline-flex items-center rounded bg-amber-500/15 px-1.5 py-0.5 text-[9.5px] font-mono font-medium text-amber-400 border border-amber-500/30"
            title={`${t("inspector.files.externalTooltip")}: ${props.previewMeta?.resolvedPath || props.previewPath}`}
          >
            {t("inspector.files.externalBadge")}
          </span>
        </Show>

        <Show when={props.activeDiff?.repo}>
          <span
            class="shrink-0 inline-flex items-center gap-1 rounded bg-[var(--accent-surface)] px-1.5 py-0 text-[10px] font-mono text-[var(--accent-primary)] border border-[var(--accent-border)]"
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
          <div class="shrink-0 inline-flex items-center rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5">
            <button
              type="button"
              onClick={() => props.onChangeViewMode("preview")}
              class={`hoxi-press inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] cursor-pointer ${
                props.viewMode === "preview"
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
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
              class={`hoxi-press inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] cursor-pointer ${
                props.viewMode === "source"
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
              title={t("inspector.files.sourceMode")}
            >
              <Code2 size={11} />
              <span>{t("inspector.files.sourceMode")}</span>
            </button>
          </div>
        </Show>

        {/* Quick Action Icons: Copy Path, Copy Content, Reveal in Finder, Open in Default App */}
        <div class="shrink-0 inline-flex items-center gap-0.5">
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
          <Show when={!isBinaryPreview() && props.previewContent}>
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
            class="shrink-0 inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--accent-primary)] hover:border-[var(--accent-border)] transition-colors cursor-pointer"
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
      <div ref={sourceContainer} class="flex-1 min-h-0 overflow-auto bg-[var(--bg-code)]">
        <Show when={props.loadingPreview}>
          <div class="flex items-center justify-center py-8 text-[var(--text-muted)]">
            <LoaderCircle size={16} class="animate-spin" />
          </div>
        </Show>
        <Show when={props.previewError}>
          <div class="px-4 py-5 space-y-3">
            <div class="font-mono text-[11.5px] text-[var(--danger-text)] break-all">
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
              /* 2. Non-image Binary File Card */
              <Show
                when={isBinaryPreview()}
                fallback={
                  /* 3. Markdown Rendered Document Preview */
                  <Show
                    when={
                      props.viewMode === "preview" &&
                      isMarkdownPath(
                        props.previewMeta?.resolvedPath || props.previewPath || "",
                        props.previewMeta?.mimeType
                      )
                    }
                    fallback={
                      /* 4. Highlighted Code / Text Source View */
                      <pre
                        class="py-3 font-mono leading-relaxed text-[var(--text-primary)] whitespace-pre selectable-text"
                        style={{ "font-size": "var(--app-code-text-size)" }}
                      >
                        <For each={sourceLines()}>
                          {(line, index) => {
                            const lineNo = () => index() + 1;
                            const isChanged = () => changedLines().has(lineNo());
                            const isTarget = () => props.highlightedLine === lineNo();
                            return (
                              <div
                                data-line={lineNo()}
                                class={`flex min-h-[1.55em] px-3 ${
                                  isChanged()
                                    ? "bg-emerald-500/18 border-l-[3px] border-emerald-400"
                                    : isTarget()
                                      ? "bg-[var(--accent-surface)] border-l-[3px] border-[var(--accent-primary)]"
                                      : "border-l-[3px] border-transparent"
                                }`}
                              >
                                <span
                                  class={`mr-2 w-10 shrink-0 select-none text-right ${
                                    isChanged()
                                      ? "text-[var(--ok-text)] font-semibold"
                                      : "text-[var(--text-faint)]"
                                  }`}
                                >
                                  {lineNo()}
                                </span>
                                <span
                                  class={`mr-2 w-3 shrink-0 select-none text-center font-bold ${
                                    isChanged() ? "text-[var(--ok-text)]" : "text-transparent"
                                  }`}
                                >
                                  +
                                </span>
                                <span class={isChanged() ? "text-[var(--text-primary)]" : ""}>
                                  <Show
                                    when={highlighted()?.tokens[index()]}
                                    fallback={line || " "}
                                  >
                                    {(tokens) => (
                                      <For each={tokens()}>
                                        {(token) => (
                                          <span
                                            style={{
                                              color: token.color,
                                              "font-style":
                                                (token.fontStyle ?? 0) & 1
                                                  ? "italic"
                                                  : undefined,
                                              "font-weight":
                                                (token.fontStyle ?? 0) & 2
                                                  ? "bold"
                                                  : undefined,
                                              "text-decoration":
                                                (token.fontStyle ?? 0) & 4
                                                  ? "underline"
                                                  : undefined,
                                            }}
                                          >
                                            {token.content}
                                          </span>
                                        )}
                                      </For>
                                    )}
                                  </Show>
                                </span>
                              </div>
                            );
                          }}
                        </For>
                      </pre>
                    }
                  >
                    <div class="p-4 bg-[var(--bg-panel)] min-h-full">
                      <MarkdownView content={props.previewContent} />
                    </div>
                  </Show>
                }
              >
                <div class="flex flex-col items-center justify-center py-12 px-4 text-center space-y-3">
                  <FileIcon
                    node={{ path: props.previewMeta?.resolvedPath || props.previewPath, type: "file" }}
                    class="w-10 h-10 opacity-80"
                  />
                  <div class="text-[12.5px] font-medium text-[var(--text-primary)]">
                    {t("inspector.files.binaryFileTitle")}
                  </div>
                  <div class="text-[11px] font-mono text-[var(--text-muted)]">
                    {t("inspector.files.binaryFileDesc", {
                      size: formatByteSize(props.previewMeta?.size),
                      mimeType: props.previewMeta?.mimeType || "application/octet-stream",
                    })}
                  </div>
                  <div class="flex items-center gap-2 pt-1">
                    <button
                      type="button"
                      onClick={() => handleOpenExternal("reveal")}
                      class="inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1.5 text-[11.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                    >
                      <FolderOpen size={13} />
                      <span>{t("inspector.files.revealInFinder")}</span>
                    </button>
                    <button
                      type="button"
                      onClick={() => handleOpenExternal("open")}
                      class="inline-flex items-center gap-1.5 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-1.5 text-[11.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
                    >
                      <ExternalLink size={13} />
                      <span>{t("inspector.files.openInDefaultApp")}</span>
                    </button>
                  </div>
                </div>
              </Show>
            }
          >
            <div class="flex flex-col items-center justify-center p-6 min-h-full bg-[var(--bg-inset)]">
              <img
                src={imageDataUrl()}
                alt={props.previewPath || ""}
                class="max-w-full max-h-[72vh] object-contain rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-2 shadow-sm"
              />
              <Show when={props.previewMeta?.size}>
                <div class="mt-2 text-[10.5px] font-mono text-[var(--text-muted)]">
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
