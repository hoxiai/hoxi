import { createEffect, createMemo, createSignal, For, on, onCleanup, Show } from "solid-js";
import {
  ArrowLeft,
  Check,
  Code2,
  Copy,
  ExternalLink,
  Eye,
  FileCode2,
  FolderOpen,
  FolderPlus,
  GitBranch,
  LoaderCircle,
  Search,
} from "lucide-solid";
import { hoxiAPI, type FileContentResponse } from "../../services/api";
import type { SnapshotFileDiff } from "../../services/sse-types";
import {
  activeDirectory,
  activeWorkspaceFolders,
  normalizeFolderPath,
  openDiffInInspector,
  openEditWorkspaceModal,
  setWorkspace,
  workspace,
  workspaceIdentityColor,
} from "../../store/workspace";
import { DiffChanges, FileIcon } from "../icons";
import { FolderIcon } from "../layout/left-nav-helpers";
import { computeChangedFileLines, resolveSessionDiffForFile } from "../timeline/file-diff-lines";
import MarkdownView from "../timeline/MarkdownView";
import { highlightCode, fileLanguage, type HighlightedCode } from "../timeline/syntax-highlight";
import { t } from "../../i18n";
import FileTreeNodes, { type FileTreeNode } from "./FileTreeNodes";
import {
  baseName,
  formatByteSize,
  isImagePreviewable,
  isMarkdownPath,
  isSvgPath,
} from "./file-tree-helpers";

export default function FileTreeView() {
  const [query, setQuery] = createSignal("");
  const [results, setResults] = createSignal<FileTreeNode[]>([]);
  const [selectedPath, setSelectedPath] = createSignal<string | null>(null);
  const [highlighted, setHighlighted] = createSignal<HighlightedCode>();
  const [searching, setSearching] = createSignal(false);
  const [previewPath, setPreviewPath] = createSignal<string | null>(null);
  const [previewDiff, setPreviewDiff] = createSignal<SnapshotFileDiff | null>(null);
  const [previewContent, setPreviewContent] = createSignal("");
  const [previewMeta, setPreviewMeta] = createSignal<FileContentResponse | null>(null);
  const [previewError, setPreviewError] = createSignal<string | null>(null);
  const [loadingPreview, setLoadingPreview] = createSignal(false);
  const [highlightedLine, setHighlightedLine] = createSignal<number | null>(null);
  const [viewMode, setViewMode] = createSignal<"preview" | "source">("source");
  const [copiedPath, setCopiedPath] = createSignal(false);
  const [copiedContent, setCopiedContent] = createSignal(false);

  let sourceContainer: HTMLDivElement | undefined;
  let lastLoadedDir = "";
  let listRequest = 0;
  let previewRequest = 0;
  onCleanup(() => {
    listRequest++;
    previewRequest++;
  });

  const folders = createMemo(() => activeWorkspaceFolders());
  const targetDirectory = createMemo(
    () => workspace.activeInspectorFolder || activeDirectory()
  );

  const loadInitialFiles = async (dir = targetDirectory(), subPath = "") => {
    const request = ++listRequest;
    setSearching(true);
    try {
      const nodes = await hoxiAPI.listFiles(
        subPath,
        dir,
        workspace.activeSessionID || undefined
      );
      if (request === listRequest) setResults(nodes ?? []);
    } catch {
      if (request === listRequest) setResults([]);
    } finally {
      if (request === listRequest) setSearching(false);
    }
  };

  const runSearch = async () => {
    const q = query().trim();
    if (!q) {
      await loadInitialFiles();
      return;
    }
    const request = ++listRequest;
    setSearching(true);
    try {
      const files = await hoxiAPI.findFiles(q, targetDirectory());
      if (request === listRequest) {
        setResults(
          Array.isArray(files)
            ? files.map((path) => ({
                path,
                name: path,
                type: path.endsWith("/") ? "directory" : "file",
              }))
            : []
        );
      }
    } catch (err) {
      console.error("find files failed:", err);
      if (request === listRequest) setResults([]);
    } finally {
      if (request === listRequest) setSearching(false);
    }
  };

  const openPreview = async (
    path: string,
    diffOverride?: SnapshotFileDiff | null,
    requestedLine?: number | null
  ) => {
    const request = ++previewRequest;
    const lineTarget = requestedLine !== undefined ? requestedLine : workspace.inspectedFileLine;
    setSelectedPath(path);
    setPreviewContent("");
    setPreviewMeta(null);
    setPreviewPath(path);
    setPreviewDiff(diffOverride === undefined ? workspace.inspectedFileDiff : diffOverride);
    setHighlightedLine(lineTarget);
    setViewMode(
      !lineTarget && (isMarkdownPath(path) || isSvgPath(path)) ? "preview" : "source"
    );
    setLoadingPreview(true);
    setPreviewError(null);
    try {
      const res = await hoxiAPI.readFileContent(
        path,
        targetDirectory(),
        workspace.activeSessionID || undefined
      );
      if (request !== previewRequest) return;
      if (res?.isDirectory) {
        setPreviewPath(null);
        await loadInitialFiles(targetDirectory(), res.resolvedPath || path);
        return;
      }
      setPreviewMeta(res ?? null);
      setPreviewContent(res?.content ?? "");
      if (
        !lineTarget &&
        (isMarkdownPath(res?.resolvedPath || path, res?.mimeType) ||
          isSvgPath(res?.resolvedPath || path, res?.mimeType))
      ) {
        setViewMode("preview");
      }
    } catch (err) {
      if (request !== previewRequest) return;
      setPreviewError(String(err));
      setPreviewContent("");
      setPreviewMeta(null);
    } finally {
      if (request === previewRequest) setLoadingPreview(false);
    }
  };

  const resolvedPreviewDiffPath = createMemo(() => {
    const p = previewPath();
    if (!p) return "";
    const base = normalizeFolderPath(activeDirectory());
    const target = normalizeFolderPath(targetDirectory());
    if (base && target && target !== base && target.startsWith(`${base}/`)) {
      const prefix = target.slice(base.length + 1);
      return `${prefix}/${p.replace(/^\/+/, "")}`;
    }
    return p;
  });

  const isBinaryPreview = createMemo(() => previewMeta()?.type === "binary");
  const isImageFile = createMemo(() =>
    Boolean(previewPath() && isImagePreviewable(previewPath()!, previewMeta()))
  );
  const canTogglePreview = createMemo(() => {
    const p = previewMeta()?.resolvedPath || previewPath() || "";
    const mime = previewMeta()?.mimeType;
    return !isBinaryPreview() && (isMarkdownPath(p, mime) || isSvgPath(p, mime));
  });

  const imageDataUrl = createMemo(() => {
    const p = previewPath() || "";
    const meta = previewMeta();
    const content = previewContent();
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
    isBinaryPreview() ? [] : previewContent().split("\n")
  );
  const activeDiff = createMemo(() => {
    const p = resolvedPreviewDiffPath();
    if (!p) return undefined;
    return resolveSessionDiffForFile(p, workspace.activeSessionID, previewDiff());
  });
  const changedLines = createMemo(() =>
    computeChangedFileLines(activeDiff(), sourceLines())
  );

  createEffect(
    on(
      () => {
        const d = activeDiff();
        return [
          previewPath(),
          d?.stamp ?? "",
          d?.additions ?? 0,
          d?.deletions ?? 0,
          d?.patch?.length ?? 0,
        ] as const;
      },
      ([path], prev) => {
        if (!path || !prev || prev[0] !== path || loadingPreview()) return;
        const request = ++previewRequest;
        void hoxiAPI
          .readFileContent(
            path,
            targetDirectory(),
            workspace.activeSessionID || undefined
          )
          .then((res) => {
            if (request === previewRequest && res?.content !== undefined) {
              setPreviewMeta(res);
              setPreviewContent(res.content);
            }
          })
          .catch(() => {
            /* ignore background refresh error */
          });
      },
      { defer: true }
    )
  );

  createEffect(() => {
    const code = previewContent();
    if (!code || isBinaryPreview()) {
      setHighlighted(undefined);
      return;
    }
    const language = fileLanguage(previewMeta()?.resolvedPath || previewPath() || "");
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
    const dir = targetDirectory();
    if (dir && dir !== lastLoadedDir) {
      const hadPrev = Boolean(lastLoadedDir);
      lastLoadedDir = dir;
      listRequest++;
      previewRequest++;
      setResults([]);
      setQuery("");
      setSelectedPath(null);
      if (hadPrev && !workspace.inspectedFilePath) {
        setPreviewPath(null);
      }
      void loadInitialFiles(dir);
    }
  });

  // React to file links clicked in chat, ToolCard, or ReviewComments
  createEffect(() => {
    const target = workspace.inspectedFilePath;
    if (target) {
      const diff = workspace.inspectedFileDiff;
      const line = workspace.inspectedFileLine;
      void openPreview(target, diff, line);
      setWorkspace("inspectedFilePath", null);
    }
  });

  createEffect(() => {
    const path = previewPath();
    if (!path || loadingPreview() || !sourceContainer || viewMode() !== "source") return;
    const changed = changedLines();
    const firstChanged = changed.size > 0 ? Math.min(...changed) : null;
    const line = workspace.inspectedFileLine || highlightedLine() || firstChanged;
    if (!line) return;
    requestAnimationFrame(() => {
      const target = sourceContainer?.querySelector<HTMLElement>(`[data-line="${line}"]`);
      target?.scrollIntoView({ block: "center", behavior: "smooth" });
    });
    if (workspace.inspectedFileLine) {
      setWorkspace("inspectedFileLine", null);
    }
  });

  const onKeyDown = (ev: KeyboardEvent) => {
    if (ev.key === "Enter") void runSearch();
  };

  const handleCopyPath = async () => {
    const target = previewMeta()?.resolvedPath || previewPath();
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
    const content = previewContent();
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
    const target = previewMeta()?.resolvedPath || previewPath();
    if (!target) return;
    void hoxiAPI
      .openExternalFile(target, {
        directory: targetDirectory(),
        sessionID: workspace.activeSessionID || undefined,
        action,
      })
      .catch((err) => {
        console.error(`Failed to ${action} file:`, err);
      });
  };

  return (
    <div class="flex flex-col h-full">
      <div class="shrink-0 px-3 py-2 border-b border-[var(--border-subtle)]">
        {/* Multi-Folder Workspace Switcher Bar */}
        <Show when={folders().length > 1}>
          <div class="mb-2 flex items-center gap-1 overflow-x-auto pb-0.5">
            <For each={folders()}>
              {(folder) => {
                const isSelected = () =>
                  normalizeFolderPath(targetDirectory()) ===
                  normalizeFolderPath(folder.path);
                return (
                  <button
                    type="button"
                    onClick={() =>
                      setWorkspace(
                        "activeInspectorFolder",
                        folder.isPrimary ? null : folder.path
                      )
                    }
                    title={folder.path}
                    class={`inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-mono transition-colors cursor-pointer shrink-0 ${
                      isSelected()
                        ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] border border-[var(--accent-border)] font-medium"
                        : "bg-[var(--bg-inset)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-transparent"
                    }`}
                  >
                    <FolderIcon
                      open={isSelected()}
                      color={workspaceIdentityColor(workspace.rootDir)}
                      class="w-3 h-3 shrink-0"
                    />
                    <span>{baseName(folder.path)}</span>
                  </button>
                );
              }}
            </For>
          </div>
        </Show>

        <div class="flex items-center gap-2 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2.5 py-1.5 focus-within:border-[var(--accent-border)]">
          <Search size={13} class="shrink-0 text-[var(--text-muted)]" />
          <input
            class="flex-1 bg-transparent text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
            placeholder={t("inspector.files.searchPlaceholder")}
            value={query()}
            onInput={(ev) => {
              setQuery(ev.currentTarget.value);
              if (!ev.currentTarget.value.trim()) void loadInitialFiles();
            }}
            onKeyDown={onKeyDown}
          />
          <Show when={searching()}>
            <LoaderCircle size={12} class="shrink-0 animate-spin text-[var(--text-muted)]" />
          </Show>
        </div>
        <div class="mt-1.5 flex items-center justify-between gap-1 text-[10px] text-[var(--text-faint)] font-mono">
          <div class="flex items-center gap-1 min-w-0 truncate">
            <FolderOpen size={10} class="shrink-0" />
            <span class="truncate" title={targetDirectory()}>
              {targetDirectory() || "—"}
            </span>
          </div>
          <button
            type="button"
            onClick={() => openEditWorkspaceModal(workspace.rootDir)}
            class="shrink-0 inline-flex items-center gap-0.5 rounded px-1 py-0.5 text-[10px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={t("leftNav.editWorkspace")}
          >
            <FolderPlus size={10} />
            <span>{t("leftNav.editWorkspace")}</span>
          </button>
        </div>
      </div>

      <div
        style={{ display: previewPath() ? "none" : undefined }}
        class="inspector-scroll flex-1 min-h-0 overflow-y-auto px-2 py-2 space-y-0.5"
      >
        <Show
          when={results().length > 0}
          fallback={
            <div class="px-2 py-8 text-center text-[11.5px] text-[var(--text-muted)]">
              {query().trim()
                ? t("inspector.files.noMatchingFiles")
                : t("inspector.files.noFilesInWorkspace")}
            </div>
          }
        >
          <FileTreeNodes
            nodes={results()}
            directory={targetDirectory()}
            selected={selectedPath()}
            onOpen={(path) => void openPreview(path, null, null)}
          />
        </Show>
      </div>

      <Show when={previewPath()}>
        <div class="flex-1 min-h-0 flex flex-col">
          {/* Preview Header Toolbar */}
          <div class="shrink-0 flex items-center gap-1.5 px-3 py-2 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]/60">
            <button
              type="button"
              class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer shrink-0"
              onClick={() => setPreviewPath(null)}
              title={t("inspector.files.back")}
            >
              <ArrowLeft size={12} />
              {t("inspector.files.back")}
            </button>
            <FileIcon
              node={{ path: previewMeta()?.resolvedPath || previewPath()!, type: "file" }}
              class="w-4 h-4 shrink-0"
            />
            <span
              class="flex-1 truncate font-mono text-[11px] font-medium text-[var(--text-primary)]"
              title={previewMeta()?.resolvedPath || previewPath()!}
            >
              {previewPath()}
            </span>

            <Show when={previewMeta()?.outsideWorkspace}>
              <span
                class="shrink-0 inline-flex items-center rounded bg-amber-500/15 px-1.5 py-0.5 text-[9.5px] font-mono font-medium text-amber-400 border border-amber-500/30"
                title={`${t("inspector.files.externalTooltip")}: ${previewMeta()?.resolvedPath || previewPath()}`}
              >
                {t("inspector.files.externalBadge")}
              </span>
            </Show>

            <Show when={activeDiff()?.repo}>
              <span
                class="shrink-0 inline-flex items-center gap-1 rounded bg-[var(--accent-surface)] px-1.5 py-0 text-[10px] font-mono text-[var(--accent-primary)] border border-[var(--accent-border)]"
                title={
                  activeDiff()!.branch
                    ? `${activeDiff()!.repo} (${activeDiff()!.branch})`
                    : activeDiff()!.repo
                }
              >
                <GitBranch size={10} />
                <span>
                  {activeDiff()!.repo}
                  {activeDiff()!.branch ? `:${activeDiff()!.branch}` : ""}
                </span>
              </span>
            </Show>

            {/* Markdown / SVG Preview vs Source Switcher */}
            <Show when={canTogglePreview()}>
              <div class="shrink-0 inline-flex items-center rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5">
                <button
                  type="button"
                  onClick={() => setViewMode("preview")}
                  class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] transition-colors cursor-pointer ${
                    viewMode() === "preview"
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
                  onClick={() => setViewMode("source")}
                  class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] transition-colors cursor-pointer ${
                    viewMode() === "source"
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
              <Show when={!isBinaryPreview() && previewContent()}>
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
                activeDiff() &&
                ((activeDiff()!.additions ?? 0) > 0 || (activeDiff()!.deletions ?? 0) > 0)
              }
            >
              <button
                type="button"
                onClick={() => openDiffInInspector(activeDiff() || resolvedPreviewDiffPath())}
                class="shrink-0 inline-flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--accent-primary)] hover:border-[var(--accent-border)] transition-colors cursor-pointer"
                title={t("toolCard.openInDiffInspector")}
              >
                <DiffChanges
                  changes={{
                    additions: activeDiff()!.additions ?? 0,
                    deletions: activeDiff()!.deletions ?? 0,
                  }}
                />
              </button>
            </Show>
          </div>

          {/* Preview Body */}
          <div ref={sourceContainer} class="flex-1 min-h-0 overflow-auto bg-[var(--bg-code)]">
            <Show when={loadingPreview()}>
              <div class="flex items-center justify-center py-8 text-[var(--text-muted)]">
                <LoaderCircle size={16} class="animate-spin" />
              </div>
            </Show>
            <Show when={previewError()}>
              <div class="px-4 py-5 space-y-3">
                <div class="font-mono text-[11.5px] text-[var(--danger-text)] break-all">
                  {previewError()}
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
            <Show when={!loadingPreview() && !previewError()}>
              {/* 1. Image Preview (Binary images or SVG in preview mode) */}
              <Show
                when={
                  isImageFile() &&
                  (isBinaryPreview() || viewMode() === "preview") &&
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
                          viewMode() === "preview" &&
                          isMarkdownPath(
                            previewMeta()?.resolvedPath || previewPath() || "",
                            previewMeta()?.mimeType
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
                                const isTarget = () => highlightedLine() === lineNo();
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
                          <MarkdownView content={previewContent()} />
                        </div>
                      </Show>
                    }
                  >
                    <div class="flex flex-col items-center justify-center py-12 px-4 text-center space-y-3">
                      <FileIcon
                        node={{ path: previewMeta()?.resolvedPath || previewPath()!, type: "file" }}
                        class="w-10 h-10 opacity-80"
                      />
                      <div class="text-[12.5px] font-medium text-[var(--text-primary)]">
                        {t("inspector.files.binaryFileTitle")}
                      </div>
                      <div class="text-[11px] font-mono text-[var(--text-muted)]">
                        {t("inspector.files.binaryFileDesc", {
                          size: formatByteSize(previewMeta()?.size),
                          mimeType: previewMeta()?.mimeType || "application/octet-stream",
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
                    alt={previewPath() || ""}
                    class="max-w-full max-h-[72vh] object-contain rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-2 shadow-sm"
                  />
                  <Show when={previewMeta()?.size}>
                    <div class="mt-2 text-[10.5px] font-mono text-[var(--text-muted)]">
                      {formatByteSize(previewMeta()?.size)}
                      {previewMeta()?.mimeType ? ` · ${previewMeta()?.mimeType}` : ""}
                    </div>
                  </Show>
                </div>
              </Show>
            </Show>
          </div>
        </div>
      </Show>
    </div>
  );
}
