import { createEffect, createMemo, createSignal, Show } from "solid-js";
import { Brain, LoaderCircle, RefreshCw } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import { activeDirectory, workspace } from "../../store/workspace";
import { t } from "../../i18n";

const PROJECT_MEMORY_FILE = ".opencode/memory/project.md";

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

  const sessionMemoryFile = createMemo(() => {
    const id = workspace.activeSessionID;
    return id ? `.opencode/memory/sessions/${id}.md` : PROJECT_MEMORY_FILE;
  });

  const activeFile = createMemo(() =>
    scope() === "session" && workspace.activeSessionID
      ? sessionMemoryFile()
      : PROJECT_MEMORY_FILE
  );

  const load = async () => {
    setLoading(true);
    setError(null);
    const dir = activeDirectory();
    try {
      if (scope() === "session" && workspace.activeSessionID) {
        try {
          const res = await hoxiAPI.readFileContent(sessionMemoryFile(), dir);
          if (res?.content?.trim()) {
            setContent(res.content);
            return;
          }
        } catch {
          // Fallback to project memory if session memory file does not exist yet
        }
      }
      const res = await hoxiAPI.readFileContent(PROJECT_MEMORY_FILE, dir);
      setContent(res?.content ?? "");
    } catch (err) {
      console.error("read memory failed:", err);
      setError(t("inspector.memory.notFound"));
      setContent("");
    } finally {
      setLoading(false);
    }
  };

  createEffect(() => {
    void workspace.activeSessionID;
    void scope();
    void load();
  });

  const dims = createMemo(() => dimensions(content()));

  return (
    <div class="flex flex-col h-full">
      <div class="shrink-0 flex items-center justify-between gap-2 px-3 py-2 border-b border-[var(--border-subtle)]">
        <div class="flex items-center gap-1.5 min-w-0">
          <Brain size={13} class="text-[var(--accent-primary)] shrink-0" />
          <div class="inline-flex rounded-lg bg-[var(--bg-inset)] p-0.5 text-[12px]">
            <button
              type="button"
              onClick={() => setScope("session")}
              class={`rounded-md px-2 py-0.5 font-medium transition-colors cursor-pointer ${
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
              class={`rounded-md px-2 py-0.5 font-medium transition-colors cursor-pointer ${
                scope() === "project"
                  ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
            >
              {t("inspector.memory.projectMemoryTab")}
            </button>
          </div>
        </div>
        <button
          class="shrink-0 text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
          onClick={() => void load()}
          title={`${t("inspector.memory.reload")} (${activeFile()})`}
        >
          <RefreshCw size={13} class={loading() ? "animate-spin" : ""} />
        </button>
      </div>

      <div class="inspector-scroll flex-1 min-h-0 overflow-y-auto px-2.5 py-2.5 space-y-2">
        <Show when={dims().length > 0}>
          <div class="flex flex-wrap gap-1">
            {dims().map((d) => (
              <span class="rounded bg-[var(--bg-inset)] px-1.5 py-[1px] font-mono text-[9px] text-[var(--text-secondary)]">
                {d}
              </span>
            ))}
          </div>
        </Show>

        <Show when={error()}>
          <div class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-3 py-6 text-center text-[11px] text-[var(--text-muted)]">
            {error()}
          </div>
        </Show>

        <Show when={content() && !error()}>
          <pre class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-canvas)] p-2.5 font-mono leading-relaxed text-[var(--text-secondary)] whitespace-pre-wrap break-words selectable-text" style={{ "font-size": "var(--app-code-text-size)" }}>
            {content()}
          </pre>
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
