import { createMemo, createSignal, onMount, Show } from "solid-js";
import { Brain, LoaderCircle, RefreshCw } from "lucide-solid";
import { hoxiAPI } from "../../services/api";
import { activeDirectory } from "../../store/workspace";
import { t } from "../../i18n";

const MEMORY_FILE = ".opencode/memory/project.md";

/** Parse the 8-dimension headings out of the memory markdown for a compact index. */
function dimensions(text: string): string[] {
  return text
    .split("\n")
    .filter((l) => /^##\s+/.test(l.trim()))
    .map((l) => l.replace(/^##\s+/, "").trim());
}

export default function MemoryBankView() {
  const [content, setContent] = createSignal("");
  const [loading, setLoading] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await hoxiAPI.readFileContent(MEMORY_FILE, activeDirectory());
      setContent(res?.content ?? "");
    } catch (err) {
      console.error("read memory failed:", err);
      setError(t("inspector.memory.notFound"));
      setContent("");
    } finally {
      setLoading(false);
    }
  };

  onMount(() => void load());

  const dims = createMemo(() => dimensions(content()));

  return (
    <div class="flex flex-col h-full">
      <div class="shrink-0 flex items-center justify-between px-3 py-2 border-b border-[var(--border-subtle)]">
        <div class="flex items-center gap-1.5 text-[11px] text-[var(--text-secondary)] min-w-0">
          <Brain size={13} class="text-[var(--accent-primary)] shrink-0" />
          <span class="font-mono truncate" title={`${activeDirectory()}/${MEMORY_FILE}`}>
            {MEMORY_FILE}
          </span>
        </div>
        <button
          class="shrink-0 text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
          onClick={() => void load()}
          title={t("inspector.memory.reload")}
        >
          <RefreshCw size={13} class={loading() ? "animate-spin" : ""} />
        </button>
      </div>

      <div class="flex-1 min-h-0 overflow-y-auto px-2.5 py-2.5 space-y-2">
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
          <pre class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-canvas)] p-2.5 font-mono text-[10px] leading-relaxed text-[var(--text-secondary)] whitespace-pre-wrap break-words selectable-text">
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
