import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { ArrowUpRight, Brain, Check, Code2, Edit3, Eye, LoaderCircle, Plus, RefreshCw, Save } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import {
  activeDirectory,
  promoteSessionMemoryToProject,
  resolveProjectMemoryDirectory,
  saveMemoryBankContent,
  workspace,
} from "../../store/workspace";
import { t, locale } from "../../i18n";
import MarkdownView, { slugifyHeading } from "../timeline/MarkdownView";

const PROJECT_MEMORY_FILE = ".hoxi/memory/project.md";
const LEGACY_PROJECT_MEMORY_FILE = ".opencode/memory/project.md";
const DEFAULT_PROJECT_MEMORY_TEMPLATE = `# Project Memory

- Keep this file concise: it is injected into every coding turn in this repository.
- Architecture boundaries & conventions:
`;

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
  const [draft, setDraft] = createSignal("");
  const [loading, setLoading] = createSignal(false);
  const [saving, setSaving] = createSignal(false);
  const [promoting, setPromoting] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [notice, setNotice] = createSignal<string | null>(null);
  const [viewMode, setViewMode] = createSignal<"preview" | "source" | "edit">("preview");
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
    const rawDir = activeDirectory();
    const projectDir = resolveProjectMemoryDirectory(rawDir);
    const dirCandidates = projectDir && projectDir !== rawDir ? [projectDir, rawDir] : [rawDir];
    const sid = workspace.activeSessionID;
    const candidates: string[] =
      scope() === "session"
        ? sid
          ? sessionMemoryFiles(sid)
          : []
        : [PROJECT_MEMORY_FILE, LEGACY_PROJECT_MEMORY_FILE];
    try {
      for (const dir of dirCandidates) {
        for (const file of candidates) {
          try {
            const res = await hoxiAPI.readFileContent(file, dir, undefined, locale());
            if (gen !== requestGen) return;
            if (res?.content?.trim()) {
              setContent(res.content);
              setDraft(res.content);
              if (viewMode() === "edit") setViewMode("preview");
              return;
            }
          } catch (err) {
            if (gen !== requestGen) return;
            if (dir === dirCandidates[dirCandidates.length - 1] && file === candidates[candidates.length - 1]) {
              console.error("read memory failed:", err);
            }
            // Try the next location: project root path, worktree path, legacy path.
          }
        }
      }
      setContent("");
      setDraft("");
      setError(
        scope() === "project" ? t("inspector.memory.notFound") : t("inspector.memory.empty")
      );
    } finally {
      if (gen === requestGen) {
        setLoading(false);
      }
    }
  };

  const handleStartEdit = (initialTemplate?: string) => {
    const base = initialTemplate ?? content();
    setDraft(base);
    setError(null);
    setNotice(null);
    setViewMode("edit");
  };

  const handleSave = async () => {
    if (saving()) return;
    setSaving(true);
    setError(null);
    setNotice(null);
    try {
      await saveMemoryBankContent({
        scope: scope(),
        content: draft(),
        directory: activeDirectory(),
        sessionID: workspace.activeSessionID,
      });
      setContent(draft());
      setViewMode("preview");
      setNotice(t("inspector.memory.savedNotice"));
      setTimeout(() => setNotice(null), 3000);
    } catch (err: any) {
      setError(err?.message || String(err));
    } finally {
      setSaving(false);
    }
  };

  const handlePromote = async () => {
    const sid = workspace.activeSessionID;
    if (!sid || promoting()) return;
    setPromoting(true);
    setError(null);
    setNotice(null);
    try {
      const res = await promoteSessionMemoryToProject({
        sessionID: sid,
        directory: activeDirectory(),
      });
      setNotice(t("inspector.memory.promotedNotice", { count: res.addedCount }));
      setTimeout(() => setNotice(null), 3500);
    } catch (err: any) {
      setError(err?.message || String(err));
    } finally {
      setPromoting(false);
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
      <div class="shrink-0 flex items-center justify-between gap-2 px-2.5 pt-1.5 pb-1">
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
          <Show when={scope() === "session" && content() && !error() && workspace.activeSessionID}>
            <button
              type="button"
              disabled={promoting()}
              onClick={() => void handlePromote()}
              title={t("inspector.memory.promoteToProjectTooltip")}
              class="hoxi-press inline-flex items-center gap-1 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)]/70 px-1.5 py-0.5 text-[11px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] disabled:opacity-45 cursor-pointer"
            >
              <Show
                when={!promoting()}
                fallback={<LoaderCircle size={11} class="animate-spin" />}
              >
                <ArrowUpRight size={11} />
              </Show>
              <span>{t("inspector.memory.promoteToProjectBtn")}</span>
            </button>
          </Show>
          <Show when={(content() && !error()) || viewMode() === "edit"}>
            <div class="inline-flex items-center rounded-md bg-[var(--bg-inset)] p-0.5">
              <button
                type="button"
                onClick={() => setViewMode("preview")}
                class={`hoxi-press inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] cursor-pointer ${
                  viewMode() === "preview"
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
                onClick={() => setViewMode("source")}
                class={`hoxi-press inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] cursor-pointer ${
                  viewMode() === "source"
                    ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
                title={t("inspector.files.sourceMode")}
              >
                <Code2 size={11} />
                <span>{t("inspector.files.sourceMode")}</span>
              </button>
              <button
                type="button"
                onClick={() => handleStartEdit()}
                class={`hoxi-press inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] cursor-pointer ${
                  viewMode() === "edit"
                    ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
                title={t("inspector.memory.editMode")}
              >
                <Edit3 size={11} />
                <span>{t("inspector.memory.editMode")}</span>
              </button>
            </div>
          </Show>
          <button
            class="hoxi-press shrink-0 rounded-md p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
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
        class={`inspector-scroll flex-1 min-h-0 overflow-y-auto px-3 py-2 space-y-2.5 ${memoryEnterClass()}`}
      >
        <Show when={notice()}>
          <div class="flex items-center gap-1.5 rounded-lg border border-[var(--ok)]/30 bg-[var(--ok-surface)] px-2.5 py-1.5 text-[11.5px] text-[var(--ok-text)]">
            <Check size={12} class="shrink-0" />
            <span>{notice()}</span>
          </div>
        </Show>

        <Show when={dims().length > 0 && viewMode() !== "edit"}>
          <div class="flex flex-wrap gap-1">
            <For each={dims()}>
              {(d) => (
                <button
                  type="button"
                  onClick={() => handleJumpDimension(d)}
                  class="hoxi-press rounded-md bg-[var(--bg-inset)] hover:bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-secondary)] hover:text-[var(--accent-primary)] transition-colors cursor-pointer"
                >
                  {d}
                </button>
              )}
            </For>
          </div>
        </Show>

        <Show when={error() && viewMode() !== "edit"}>
          <div class="rounded-xl bg-[var(--bg-inset)]/60 px-3 py-6 text-center text-[12px] text-[var(--text-muted)] space-y-2.5">
            <div>{error()}</div>
            <Show when={scope() === "project"}>
              <div>
                <button
                  type="button"
                  onClick={() => handleStartEdit(DEFAULT_PROJECT_MEMORY_TEMPLATE)}
                  class="hoxi-press inline-flex items-center gap-1 rounded-lg bg-[var(--accent-primary)] px-3 py-1.5 text-[11.5px] font-medium text-[var(--accent-foreground)] hover:opacity-90 cursor-pointer"
                >
                  <Plus size={12} />
                  <span>{t("inspector.memory.createProjectMemoryBtn")}</span>
                </button>
              </div>
            </Show>
          </div>
        </Show>

        <Show when={viewMode() === "edit"}>
          <div class="flex flex-col gap-2">
            <textarea
              value={draft()}
              onInput={(e) => setDraft(e.currentTarget.value)}
              placeholder={t("inspector.memory.editorPlaceholder")}
              rows={16}
              class="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/70 p-3 font-mono text-[12px] leading-relaxed text-[var(--text-primary)] outline-none focus:border-[var(--accent-border)] resize-y"
            />
            <div class="flex items-center justify-end gap-1.5">
              <button
                type="button"
                disabled={saving()}
                onClick={() => setViewMode("preview")}
                class="hoxi-press rounded-lg border border-[var(--border-subtle)] px-2.5 py-1 text-[11.5px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                {t("inspector.memory.cancelEdit")}
              </button>
              <button
                type="button"
                disabled={saving()}
                onClick={() => void handleSave()}
                class="hoxi-press inline-flex items-center gap-1 rounded-lg bg-[var(--accent-primary)] px-3 py-1 text-[11.5px] font-semibold text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-45 cursor-pointer"
              >
                <Show
                  when={!saving()}
                  fallback={<LoaderCircle size={12} class="animate-spin" />}
                >
                  <Save size={12} />
                </Show>
                <span>
                  {saving() ? t("inspector.memory.saving") : t("inspector.memory.save")}
                </span>
              </button>
            </div>
          </div>
        </Show>

        <Show when={content() && !error() && viewMode() !== "edit"}>
          <Show
            when={viewMode() === "preview"}
            fallback={
              <pre
                class="rounded-xl bg-[var(--bg-inset)]/60 p-3 font-mono leading-relaxed text-[var(--text-secondary)] whitespace-pre-wrap break-words selectable-text"
                style={{ "font-size": "var(--app-code-text-size)" }}
              >
                {content()}
              </pre>
            }
          >
            <div class="px-0.5 py-1">
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
