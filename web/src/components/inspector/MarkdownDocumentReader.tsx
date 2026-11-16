import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { ListTree, X } from "lucide-solid";
import { t } from "../../i18n";
import { workspace } from "../../store/workspace";
import MarkdownView, {
  extractMarkdownHeadings,
  type MarkdownHeadingItem,
} from "../timeline/MarkdownView";

export interface MarkdownDocumentReaderProps {
  content: string;
  baseFilePath?: string;
  headings?: MarkdownHeadingItem[];
  outlineOpen: boolean;
  onToggleOutline: (open: boolean) => void;
  scrollContainer?: () => HTMLElement | undefined;
}

export function resolveActiveHeadingSlug(
  headings: ReadonlyArray<{ slug: string; offsetTop: number }>,
  atBottom: boolean,
  pinnedSlug?: string | null,
  thresholdPx = 72
): string {
  if (headings.length === 0) return "";
  if (pinnedSlug) {
    const pinned = headings.find((h) => h.slug === pinnedSlug);
    if (pinned && (pinned.offsetTop <= thresholdPx || atBottom)) {
      return pinned.slug;
    }
  }
  if (atBottom) {
    return headings[headings.length - 1].slug;
  }
  let current = headings[0].slug;
  for (const h of headings) {
    if (h.offsetTop <= thresholdPx) {
      current = h.slug;
    } else {
      break;
    }
  }
  return current;
}

export default function MarkdownDocumentReader(props: MarkdownDocumentReaderProps) {
  let rootRef: HTMLDivElement | undefined;
  let jumpRafId = 0;
  let pinnedSlug: string | null = null;
  const headings = createMemo<MarkdownHeadingItem[]>(
    () => props.headings ?? extractMarkdownHeadings(props.content)
  );
  const [activeSlug, setActiveSlug] = createSignal<string>("");

  onCleanup(() => {
    if (jumpRafId && typeof cancelAnimationFrame === "function") {
      cancelAnimationFrame(jumpRafId);
      jumpRafId = 0;
    }
  });

  createEffect(() => {
    const list = headings();
    if (list.length === 0) {
      pinnedSlug = null;
      setActiveSlug("");
      return;
    }
    if (!activeSlug() || !list.some((h) => h.slug === activeSlug())) {
      setActiveSlug(list[0].slug);
    }
  });

  createEffect(() => {
    const list = headings();
    const container =
      props.scrollContainer?.() ??
      (rootRef?.closest(".overflow-auto, .overflow-y-auto") as HTMLElement | null) ??
      undefined;
    if (!container || list.length < 2) return;

    const updateActiveHeading = () => {
      if (!rootRef) return;
      const nodes = Array.from(rootRef.querySelectorAll<HTMLElement>("[data-heading-slug]"));
      if (nodes.length === 0) return;
      const containerTop = container.getBoundingClientRect().top;
      const atBottom =
        container.scrollHeight > container.clientHeight &&
        Math.ceil(container.scrollTop + container.clientHeight) >= container.scrollHeight - 4;
      const measured = nodes.map((node) => ({
        slug: node.dataset.headingSlug || "",
        offsetTop: node.getBoundingClientRect().top - containerTop,
      }));
      if (pinnedSlug && !atBottom) {
        const pinned = measured.find((h) => h.slug === pinnedSlug);
        if (!pinned || pinned.offsetTop <= 72) {
          pinnedSlug = null;
        }
      }
      const next = resolveActiveHeadingSlug(measured, atBottom, pinnedSlug);
      if (next) setActiveSlug(next);
    };

    updateActiveHeading();
    container.addEventListener("scroll", updateActiveHeading, { passive: true });
    onCleanup(() => container.removeEventListener("scroll", updateActiveHeading));
  });

  const minLevel = createMemo(() => {
    const list = headings();
    if (list.length === 0) return 1;
    return Math.min(...list.map((h) => h.level));
  });

  const handleJumpToHeading = (slug: string) => {
    pinnedSlug = slug;
    setActiveSlug(slug);
    const shouldCloseFloatingOutline = !workspace.inspectorMaximized && props.outlineOpen;
    if (shouldCloseFloatingOutline) {
      props.onToggleOutline(false);
    }
    const scrollToTarget = () => {
      jumpRafId = 0;
      const target = rootRef?.querySelector<HTMLElement>(`[data-heading-slug="${CSS.escape(slug)}"]`);
      target?.scrollIntoView({ behavior: "smooth", block: "start" });
    };
    if (shouldCloseFloatingOutline && typeof requestAnimationFrame === "function") {
      if (jumpRafId && typeof cancelAnimationFrame === "function") {
        cancelAnimationFrame(jumpRafId);
      }
      jumpRafId = requestAnimationFrame(scrollToTarget);
    } else {
      scrollToTarget();
    }
  };

  return (
    <div
      ref={rootRef}
      data-markdown-reader-root="true"
      class="relative min-h-full bg-[var(--bg-panel)]"
    >
      <div
        class={`flex min-h-full items-start ${
          workspace.inspectorMaximized ? "max-w-6xl mx-auto gap-6 px-8 py-6" : "p-4"
        }`}
      >
        {/* Document Content Column */}
        <article
          class={`min-w-0 flex-1 ${
            workspace.inspectorMaximized ? "max-w-3xl mx-auto" : ""
          }`}
        >
          <MarkdownView
            content={props.content}
            density="document"
            baseFilePath={props.baseFilePath}
          />
        </article>

        {/* Pinned Side TOC when maximized & outlineOpen */}
        <Show when={workspace.inspectorMaximized && props.outlineOpen && headings().length >= 2}>
          <aside class="sticky top-4 w-60 shrink-0 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/80 p-3 max-h-[calc(100vh-8rem)] overflow-y-auto">
            <div class="mb-2 flex items-center justify-between gap-2 pb-1.5 border-b border-[var(--border-subtle)]">
              <div class="flex items-center gap-1.5 text-[11px] font-semibold text-[var(--text-primary)]">
                <ListTree size={12} class="text-[var(--accent-primary)]" />
                <span>{t("inspector.files.outlineTitle")}</span>
              </div>
              <button
                type="button"
                onClick={() => props.onToggleOutline(false)}
                class="rounded p-0.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                <X size={12} />
              </button>
            </div>
            <nav class="space-y-0.5">
              <For each={headings()}>
                {(item) => {
                  const isActive = () => activeSlug() === item.slug;
                  const indentPx = () => Math.max(0, item.level - minLevel()) * 10;
                  return (
                    <button
                      type="button"
                      onClick={() => handleJumpToHeading(item.slug)}
                      style={{ "padding-left": `${indentPx() + 8}px` }}
                      class={`w-full text-left rounded-md pr-2 py-1 text-[11px] leading-snug truncate transition-colors cursor-pointer ${
                        isActive()
                          ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                          : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                      }`}
                      title={item.text}
                    >
                      {item.text}
                    </button>
                  );
                }}
              </For>
            </nav>
          </aside>
        </Show>
      </div>

      {/* Floating Outline Drawer for standard width inspector */}
      <Show when={!workspace.inspectorMaximized && props.outlineOpen && headings().length >= 2}>
        <div class="sticky bottom-3 ml-auto mr-3 w-64 max-w-[calc(100%-1.5rem)] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)]/95 backdrop-blur-md shadow-lg p-2.5 z-20">
          <div class="mb-1.5 flex items-center justify-between gap-2 pb-1 border-b border-[var(--border-subtle)]">
            <div class="flex items-center gap-1.5 text-[11px] font-semibold text-[var(--text-primary)]">
              <ListTree size={12} class="text-[var(--accent-primary)]" />
              <span>{t("inspector.files.outlineTitle")}</span>
            </div>
            <button
              type="button"
              onClick={() => props.onToggleOutline(false)}
              class="rounded p-0.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
            >
              <X size={12} />
            </button>
          </div>
          <nav class="max-h-56 overflow-y-auto space-y-0.5 pr-0.5">
            <For each={headings()}>
              {(item) => {
                const isActive = () => activeSlug() === item.slug;
                const indentPx = () => Math.max(0, item.level - minLevel()) * 10;
                return (
                  <button
                    type="button"
                    onClick={() => handleJumpToHeading(item.slug)}
                    style={{ "padding-left": `${indentPx() + 8}px` }}
                    class={`w-full text-left rounded-md pr-2 py-1 text-[11px] leading-snug truncate transition-colors cursor-pointer ${
                      isActive()
                        ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                        : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                    }`}
                    title={item.text}
                  >
                    {item.text}
                  </button>
                );
              }}
            </For>
          </nav>
        </div>
      </Show>
    </div>
  );
}
