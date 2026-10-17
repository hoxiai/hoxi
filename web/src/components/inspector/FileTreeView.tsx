import { createEffect, createSignal, For, onMount, Show } from "solid-js";
import { ArrowLeft, FolderOpen, LoaderCircle, Search } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import { activeDirectory, setWorkspace, workspace } from "../../store/workspace";
import { FileIcon } from "../icons";

export default function FileTreeView() {
  const [query, setQuery] = createSignal("");
  const [results, setResults] = createSignal<string[]>([]);
  const [searching, setSearching] = createSignal(false);
  const [previewPath, setPreviewPath] = createSignal<string | null>(null);
  const [previewContent, setPreviewContent] = createSignal("");
  const [previewError, setPreviewError] = createSignal<string | null>(null);
  const [loadingPreview, setLoadingPreview] = createSignal(false);

  const loadInitialFiles = async () => {
    setSearching(true);
    try {
      const nodes = await hoxiAPI.listFiles("", activeDirectory());
      const paths = (nodes ?? []).map((n) => n.path);
      setResults(paths);
    } catch {
      setResults([]);
    } finally {
      setSearching(false);
    }
  };

  const runSearch = async () => {
    const q = query().trim();
    if (!q) {
      await loadInitialFiles();
      return;
    }
    if (searching()) return;
    setSearching(true);
    try {
      const files = await hoxiAPI.findFiles(q, activeDirectory());
      setResults(Array.isArray(files) ? files : []);
    } catch (err) {
      console.error("find files failed:", err);
      setResults([]);
    } finally {
      setSearching(false);
    }
  };

  const openPreview = async (path: string) => {
    setPreviewPath(path);
    setLoadingPreview(true);
    setPreviewError(null);
    try {
      const res = await hoxiAPI.readFileContent(path, activeDirectory());
      setPreviewContent(res?.content ?? "");
    } catch (err) {
      setPreviewError(String(err));
      setPreviewContent("");
    } finally {
      setLoadingPreview(false);
    }
  };

  onMount(() => {
    void loadInitialFiles();
  });

  // React to file links clicked in chat, ToolCard, or ReviewComments
  createEffect(() => {
    const target = workspace.inspectedFilePath;
    if (target) {
      void openPreview(target);
      setWorkspace("inspectedFilePath", null);
    }
  });

  const onKeyDown = (ev: KeyboardEvent) => {
    if (ev.key === "Enter") void runSearch();
  };

  return (
    <div class="flex flex-col h-full">
      <div class="shrink-0 px-3 py-2 border-b border-[var(--border-subtle)]">
        <div class="flex items-center gap-2 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2.5 py-1.5 focus-within:border-[var(--accent-border)]">
          <Search size={13} class="shrink-0 text-[var(--text-muted)]" />
          <input
            class="flex-1 bg-transparent text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
            placeholder="Search workspace files (Enter)…"
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
        <div class="mt-1.5 flex items-center gap-1 text-[10px] text-[var(--text-faint)] font-mono truncate">
          <FolderOpen size={10} class="shrink-0" />
          <span class="truncate" title={activeDirectory()}>
            {activeDirectory() || "—"}
          </span>
        </div>
      </div>

      <Show
        when={previewPath()}
        fallback={
          <div class="flex-1 min-h-0 overflow-y-auto px-2 py-2 space-y-0.5">
            <Show
              when={results().length > 0}
              fallback={
                <div class="px-2 py-8 text-center text-[11.5px] text-[var(--text-muted)]">
                  {query().trim() ? "No matching files." : "No files found in workspace."}
                </div>
              }
            >
              <For each={results()}>
                {(path) => (
                  <button
                    type="button"
                    class="w-full flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-left hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                    onClick={() => void openPreview(path)}
                  >
                    <FileIcon
                      node={{
                        path,
                        type: path.endsWith("/") ? "directory" : "file",
                      }}
                      class="w-4 h-4 shrink-0"
                    />
                    <span
                      class="truncate font-mono text-[11.5px] text-[var(--text-primary)]"
                      title={path}
                    >
                      {path}
                    </span>
                  </button>
                )}
              </For>
            </Show>
          </div>
        }
      >
        <div class="flex-1 min-h-0 flex flex-col">
          <div class="shrink-0 flex items-center gap-2 px-3 py-2 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]/60">
            <button
              type="button"
              class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              onClick={() => setPreviewPath(null)}
              title="Back to file list"
            >
              <ArrowLeft size={12} />
              Back
            </button>
            <FileIcon node={{ path: previewPath()!, type: "file" }} class="w-4 h-4 shrink-0" />
            <span
              class="flex-1 truncate font-mono text-[11px] font-medium text-[var(--text-primary)]"
              title={previewPath()!}
            >
              {previewPath()}
            </span>
          </div>
          <div class="flex-1 min-h-0 overflow-auto bg-[var(--bg-code)]">
            <Show when={loadingPreview()}>
              <div class="flex items-center justify-center py-8 text-[var(--text-muted)]">
                <LoaderCircle size={16} class="animate-spin" />
              </div>
            </Show>
            <Show when={previewError()}>
              <div class="px-3 py-4 font-mono text-[11px] text-[var(--danger-text)]">
                {previewError()}
              </div>
            </Show>
            <Show when={!loadingPreview() && !previewError()}>
              <pre class="p-3 font-mono text-[11.5px] leading-relaxed text-[var(--text-primary)] whitespace-pre selectable-text">
                {previewContent()}
              </pre>
            </Show>
          </div>
        </div>
      </Show>
    </div>
  );
}
