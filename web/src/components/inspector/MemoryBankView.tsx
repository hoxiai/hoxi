import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { Brain, Code2, Eye, LoaderCircle, RefreshCw } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import { activeDirectory, workspace } from "../../store/workspace";
import { t } from "../../i18n";
import MarkdownView, { slugifyHeading } from "../timeline/MarkdownView";

const PROJECT_MEMORY_FILE = ".hoxi/memory/project.md";
const LEGACY_PROJECT_MEMORY_FILE = ".opencode/memory/project.md";

/** Memory moved from .opencode/memory to .hoxi/memory; keep reading both. */
function sessionMemoryFiles(id: string): string[] {
  return [`.hoxi/memory/sessions/${id}.md`, `.opencode/memory/sessions/${id}.md`];
}

/** Parse the 8-dimension headings out of the memory markdown for a compact index. */
function dimensions(text: string): string[] {
  return text
    .split("\n")
    .filter((l) => /^##\s+/.test(l.trim()))
    .map((l) => l.replace(/^##\s+/, "").trim());
}

export default function MemoryBankView() {
  const [scope, setScope] = createSignal<"session" | "project">("session");
  const [content, setContent] = createSignal("");
  const [loading, setLoading] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [viewMode, setViewMode] = createSignal<"preview" | "source">("preview");
  let scrollRef: HTMLDivElement | undefined;

  const activeFile = createMemo(() =>
    scope() === "session" && workspace.activeSessionID
      ? sessionMemoryFiles(workspace.activeSessionID)[0]
      : PROJECT_MEMORY_FILE
  );

  let requestGen = 0;

  const load = async () => {
    const gen = ++requestGen;
    setLoading(true);
    setError(null);
    const dir = activeDirectory();
    const sid = workspace.activeSessionID;
    const candidates: string[] =
      scope() === "session"
        ? sid
          ? sessionMemoryFiles(sid)
          : []
        : [PROJECT_MEMORY_FILE, LEGACY_PROJECT_MEMORY_FILE];
    try {
      for (const file of candidates) {
        try {
          const res = await hoxiAPI.readFileContent(file, dir);
          if (gen !== requestGen) return;
          if (res?.content?.trim()) {
            setContent(res.content);
            return;
          }
        } catch (err) {
          if (gen !== requestGen) return;
          if (file === candidates[candidates.length - 1]) {
            console.error("read memory failed:", err);
          }
          // Try the next location: current path, legacy path, then project memory.
        }
      }
      setContent("");
      setError(t("inspector.memory.notFound"));
    } finally {
      if (gen === requestGen) {
        setLoading(false);
      }
    }
  };

  createEffect(() => {
    if (!workspace.inspectorOpen || workspace.inspectorTab !== "memory") return;
    const sid = workspace.activeSessionID;
    void (sid ? workspace.sessionFinishedAt[sid] : 0);
    void activeDirectory();
    void scope();
    void load();
  });

  const dims = createMemo(() => dimensions(content()));
  let scopePhase = false;
  const memoryEnterClass = createMemo(() => {
    void scope();
    void workspace.activeSessionID;
    scopePhase = !scopePhase;
    return scopePhase ? "hoxi-view-enter-a" : "hoxi-view-enter-b";
  });

  const handleJumpDimension = (dim: string) => {
    const slug = slugifyHeading(dim);
    if (viewMode() !== "preview") {
      setViewMode("preview");
    }
    requestAnimationFrame(() => {
      const target = scrollRef?.querySelector<HTMLElement>(
        `[data-heading-slug="${CSS.escape(slug)}"]`
      );
      target?.scrollIntoView({ behavior: "smooth", block: "start" });
    });
  };

  return (
    <div class="flex flex-col h-full">
      <div class="shrink-0 flex items-center justify-between gap-2 px-3 py-2 border-b border-[var(--border-subtle)]">
        <div class="flex items-center gap-1.5 min-w-0">
          <Brain size={13} class="text-[var(--accent-primary)] shrink-0" />
          <div class="inline-flex rounded-lg bg-[var(--bg-inset)] p-0.5 text-[12px]">
            <button
              type="button"
              onClick={() => setScope("session")}
              class={`hoxi-press rounded-md px-2 py-0.5 font-medium cursor-pointer ${
                scope() === "session"
                  ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
            >
              {t("inspector.memory.sessionMemoryTab")}
            </button>
            <button
              type="button"
              onClick={() => setScope("project")}
              class={`hoxi-press rounded-md px-2 py-0.5 font-medium cursor-pointer ${
                scope() === "project"
                  ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
            >
              {t("inspector.memory.projectMemoryTab")}
            </button>
          </div>
        </div>
        <div class="flex items-center gap-1.5 shrink-0">
          <Show when={content() && !error()}>
            <div class="inline-flex items-center rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5">
              <button
                type="button"
                onClick={() => setViewMode("preview")}
                class={`hoxi-press inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] cursor-pointer ${
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
                class={`hoxi-press inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] cursor-pointer ${
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
          <button
            class="hoxi-press shrink-0 text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
            onClick={() => void load()}
            title={`${t("inspector.memory.reload")} (${activeFile()})`}
          >
            <RefreshCw size={13} class={loading() ? "animate-spin" : ""} />
          </button>
        </div>
      </div>

      <div
        ref={scrollRef}
        data-markdown-reader-root="true"
        class={`inspector-scroll flex-1 min-h-0 overflow-y-auto px-2.5 py-2.5 space-y-2 ${memoryEnterClass()}`}
      >
        <Show when={dims().length > 0}>
          <div class="flex flex-wrap gap-1">
            <For each={dims()}>
              {(d) => (
                <button
                  type="button"
                  onClick={() => handleJumpDimension(d)}
                  class="hoxi-press rounded bg-[var(--bg-inset)] hover:bg-[var(--accent-surface)] px-1.5 py-[1px] font-mono text-[11px] text-[var(--text-secondary)] hover:text-[var(--accent-primary)] transition-colors cursor-pointer"
                >
                  {d}
                </button>
              )}
            </For>
          </div>
        </Show>

        <Show when={error()}>
          <div class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-6 text-center text-[11px] text-[var(--text-muted)]">
            {error()}
          </div>
        </Show>

        <Show when={content() && !error()}>
          <Show
            when={viewMode() === "preview"}
            fallback={
              <pre
                class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-canvas)] p-2.5 font-mono leading-relaxed text-[var(--text-secondary)] whitespace-pre-wrap break-words selectable-text"
                style={{ "font-size": "var(--app-code-text-size)" }}
              >
                {content()}
              </pre>
            }
          >
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-3.5">
              <MarkdownView
                content={content()}
                density="document"
                baseFilePath={activeFile()}
              />
            </div>
          </Show>
        </Show>

        <Show when={loading() && !content()}>
          <div class="flex items-center justify-center py-8 text-[var(--text-muted)]">
            <LoaderCircle size={16} class="animate-spin" />
          </div>
        </Show>
      </div>
    </div>
  );
}
