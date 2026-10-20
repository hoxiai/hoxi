import { createEffect, createMemo, createSignal, For, on, onCleanup, Show } from "solid-js";
import { FolderOpen, FolderPlus, LoaderCircle, Search } from "lucide-solid";
import { hoxiAPI, type FileContentResponse } from "../../services/api";
import type { SnapshotFileDiff } from "../../services/sse-types";
import {
  activeDirectory,
  activeWorkspaceFolders,
  normalizeFolderPath,
  openEditWorkspaceModal,
  setWorkspace,
  workspace,
  workspaceIdentityColor,
} from "../../store/workspace";
import { FolderIcon } from "../layout/left-nav-helpers";
import { resolveSessionDiffForFile } from "../timeline/file-diff-lines";
import { t } from "../../i18n";
import FilePreviewPane from "./FilePreviewPane";
import FileTreeNodes, { type FileTreeNode } from "./FileTreeNodes";
import { baseName, isMarkdownPath, isSvgPath } from "./file-tree-helpers";

const MAX_CACHED_ROOT_DIRS = 24;
const rootFileNodesCache = new Map<string, FileTreeNode[]>();

function readCachedRootNodes(dir: string | undefined, subPath: string): FileTreeNode[] | undefined {
  const key = `${normalizeFolderPath(dir || "")}\x00${subPath}`;
  const hit = rootFileNodesCache.get(key);
  if (!hit) return undefined;
  rootFileNodesCache.delete(key);
  rootFileNodesCache.set(key, hit);
  return hit;
}

function writeCachedRootNodes(dir: string | undefined, subPath: string, nodes: FileTreeNode[]) {
  const key = `${normalizeFolderPath(dir || "")}\x00${subPath}`;
  if (rootFileNodesCache.has(key)) rootFileNodesCache.delete(key);
  while (rootFileNodesCache.size >= MAX_CACHED_ROOT_DIRS) {
    const oldest = rootFileNodesCache.keys().next().value;
    if (oldest === undefined) break;
    rootFileNodesCache.delete(oldest);
  }
  rootFileNodesCache.set(key, nodes);
}

export default function FileTreeView() {
  const [query, setQuery] = createSignal("");
  const [results, setResults] = createSignal<FileTreeNode[]>([]);
  const [selectedPath, setSelectedPath] = createSignal<string | null>(null);
  const [searching, setSearching] = createSignal(false);
  const [previewPath, setPreviewPath] = createSignal<string | null>(null);
  const [previewDiff, setPreviewDiff] = createSignal<SnapshotFileDiff | null>(null);
  const [previewContent, setPreviewContent] = createSignal("");
  const [previewMeta, setPreviewMeta] = createSignal<FileContentResponse | null>(null);
  const [previewError, setPreviewError] = createSignal<string | null>(null);
  const [loadingPreview, setLoadingPreview] = createSignal(false);
  const [highlightedLine, setHighlightedLine] = createSignal<number | null>(null);
  const [viewMode, setViewMode] = createSignal<"preview" | "source">("source");

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
    const cached = readCachedRootNodes(dir, subPath);
    if (cached) {
      setResults(cached);
    } else {
      setSearching(true);
    }
    try {
      const nodes = await hoxiAPI.listFiles(
        subPath,
        dir,
        workspace.activeSessionID || undefined
      );
      const list = nodes ?? [];
      writeCachedRootNodes(dir, subPath, list);
      if (request === listRequest) setResults(list);
    } catch {
      if (request === listRequest && !cached) setResults([]);
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

  const activeDiff = createMemo(() => {
    const p = resolvedPreviewDiffPath();
    if (!p) return undefined;
    return resolveSessionDiffForFile(p, workspace.activeSessionID, previewDiff());
  });

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
    const dir = targetDirectory();
    if (dir && dir !== lastLoadedDir) {
      const hadPrev = Boolean(lastLoadedDir);
      lastLoadedDir = dir;
      listRequest++;
      previewRequest++;
      setQuery("");
      setSelectedPath(null);
      if (hadPrev && !workspace.inspectedFilePath) {
        setPreviewPath(null);
      }
      const cached = readCachedRootNodes(dir, "");
      if (!cached) setResults([]);
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

  const onKeyDown = (ev: KeyboardEvent) => {
    if (ev.key === "Enter") void runSearch();
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
            class="flex-1 bg-transparent text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
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
        <div class="mt-1.5 flex items-center justify-between gap-1 text-[11px] text-[var(--text-faint)] font-mono">
          <div class="flex items-center gap-1 min-w-0 truncate">
            <FolderOpen size={10} class="shrink-0" />
            <span class="truncate" title={targetDirectory()}>
              {targetDirectory() || "—"}
            </span>
          </div>
          <button
            type="button"
            onClick={() => openEditWorkspaceModal(workspace.rootDir)}
            class="shrink-0 inline-flex items-center gap-0.5 rounded px-1 py-0.5 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={t("leftNav.editWorkspace")}
          >
            <FolderPlus size={10} />
            <span>{t("leftNav.editWorkspace")}</span>
          </button>
        </div>
      </div>

      <div
        class={`inspector-scroll flex-1 min-h-0 overflow-y-auto px-2 py-2 space-y-0.5 ${
          previewPath() ? "hidden" : "hoxi-view-enter"
        }`}
      >
        <Show
          when={results().length > 0}
          fallback={
            <div class="px-2 py-8 text-center text-[12px] text-[var(--text-muted)]">
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
        {(path) => (
          <FilePreviewPane
            previewPath={path()}
            resolvedDiffPath={resolvedPreviewDiffPath()}
            previewMeta={previewMeta()}
            previewContent={previewContent()}
            previewError={previewError()}
            loadingPreview={loadingPreview()}
            activeDiff={activeDiff()}
            highlightedLine={highlightedLine()}
            viewMode={viewMode()}
            onChangeViewMode={setViewMode}
            targetDirectory={targetDirectory()}
            onBack={() => setPreviewPath(null)}
          />
        )}
      </Show>
    </div>
  );
}
