// web/src/components/timeline/SelectionQuoteBar.tsx
// Non-intrusive floating pill that appears when the user selects text inside
// the conversation transcript or Markdown reader, letting them attach the
// selection directly as a hoxi-context v1 quote chip in the Composer.
import { createEffect, createSignal, onCleanup, Show, type Accessor } from "solid-js";
import { Portal } from "solid-js/web";
import { Check, Copy, Quote } from "lucide-solid";
import { t } from "../../i18n";
import { isTesterPortalMode } from "../../store/workspace";
import type { ContextEnvelope } from "../../store/context-protocol";

interface SelectionState {
  text: string;
  x: number;
  y: number;
}

const MIN_SELECTION_CHARS = 2;
const MAX_QUOTED_CHARS = 3600;

function isInteractiveInputNode(node: Node | null): boolean {
  const el =
    node instanceof Element
      ? node
      : node?.parentElement ?? null;
  if (!el) return false;
  return Boolean(el.closest("input, textarea, [contenteditable='true']"));
}

export default function SelectionQuoteBar(props: {
  container: Accessor<HTMLElement | undefined>;
  sourceTitle?: string;
}) {
  const [selection, setSelection] = createSignal<SelectionState | null>(null);
  const [copied, setCopied] = createSignal(false);
  let barRef: HTMLDivElement | undefined;
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  onCleanup(() => {
    if (copyTimer) clearTimeout(copyTimer);
  });

  const readSelection = () => {
    const root = props.container();
    if (!root || typeof window === "undefined" || typeof window.getSelection !== "function") {
      setSelection(null);
      return;
    }
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed || sel.rangeCount === 0) {
      setSelection(null);
      return;
    }
    if (isInteractiveInputNode(sel.anchorNode) || isInteractiveInputNode(sel.focusNode)) {
      setSelection(null);
      return;
    }
    const range = sel.getRangeAt(0);
    if (!root.contains(range.commonAncestorContainer)) {
      setSelection(null);
      return;
    }
    const text = sel.toString().trim();
    if (text.length < MIN_SELECTION_CHARS) {
      setSelection(null);
      return;
    }
    const rect = range.getBoundingClientRect();
    if (!rect || (rect.width === 0 && rect.height === 0)) {
      setSelection(null);
      return;
    }
    const viewportW = window.innerWidth || 1024;
    const centerX = Math.min(viewportW - 110, Math.max(110, rect.left + rect.width / 2));
    const topY = rect.top > 56 ? rect.top - 10 : Math.min((window.innerHeight || 768) - 16, rect.bottom + 42);
    setSelection({ text: text.slice(0, MAX_QUOTED_CHARS), x: centerX, y: topY });
  };

  createEffect(() => {
    const root = props.container();
    if (!root || typeof document === "undefined") return;

    let rafId = 0;
    let pointerSelecting = false;
    const scheduleRead = () => {
      if (rafId) cancelAnimationFrame(rafId);
      rafId = requestAnimationFrame(() => {
        rafId = 0;
        readSelection();
      });
    };

    const onPointerDown = () => {
      pointerSelecting = true;
    };
    const onPointerEnd = () => {
      if (!pointerSelecting) return;
      pointerSelecting = false;
      scheduleRead();
    };
    const onSelectionChange = () => {
      if (pointerSelecting) return;
      scheduleRead();
    };
    const onMouseDown = (ev: MouseEvent) => {
      if (barRef && ev.target instanceof Node && barRef.contains(ev.target)) return;
      setSelection(null);
    };
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") setSelection(null);
    };
    const onScroll = () => setSelection(null);

    root.addEventListener("pointerdown", onPointerDown);
    root.addEventListener("pointerup", onPointerEnd);
    window.addEventListener("pointerup", onPointerEnd);
    window.addEventListener("pointercancel", onPointerEnd);
    document.addEventListener("selectionchange", onSelectionChange);
    document.addEventListener("mousedown", onMouseDown);
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("scroll", onScroll, true);

    onCleanup(() => {
      if (rafId) cancelAnimationFrame(rafId);
      root.removeEventListener("pointerdown", onPointerDown);
      root.removeEventListener("pointerup", onPointerEnd);
      window.removeEventListener("pointerup", onPointerEnd);
      window.removeEventListener("pointercancel", onPointerEnd);
      document.removeEventListener("selectionchange", onSelectionChange);
      document.removeEventListener("mousedown", onMouseDown);
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("scroll", onScroll, true);
    });
  });

  const handleQuote = (ev: MouseEvent) => {
    ev.preventDefault();
    ev.stopPropagation();
    const cur = selection();
    if (!cur) return;
    const title = props.sourceTitle
      ? t("timeline.quoteDocumentTitle", { file: props.sourceTitle })
      : t("timeline.quoteSelectionTitle");
    const envelope: ContextEnvelope = {
      version: 1,
      items: [
        {
          kind: "quote",
          title,
          content: cur.text,
        },
      ],
    };
    window.dispatchEvent(
      new CustomEvent("hoxi:composer-fill", {
        detail: {
          contexts: [envelope],
          appendContexts: true,
        },
      })
    );
    try {
      window.getSelection()?.removeAllRanges();
    } catch {
      /* ignore */
    }
    setSelection(null);
  };

  const handleCopy = async (ev: MouseEvent) => {
    ev.preventDefault();
    ev.stopPropagation();
    const cur = selection();
    if (!cur) return;
    try {
      await navigator.clipboard.writeText(cur.text);
      setCopied(true);
      if (copyTimer) clearTimeout(copyTimer);
      copyTimer = setTimeout(() => {
        setCopied(false);
        setSelection(null);
      }, 650);
    } catch {
      setSelection(null);
    }
  };

  return (
    <Show when={selection()}>
      {(sel) => (
        <Portal>
          <div
            ref={barRef}
            data-hoxi-selection-quote-bar="true"
            role="toolbar"
            style={{
              left: `${sel().x}px`,
              top: `${sel().y}px`,
            }}
            class="fixed z-[115] -translate-x-1/2 -translate-y-full inline-flex items-center gap-0.5 rounded-full border border-[var(--border-strong)] bg-[var(--bg-elevated)]/95 px-1.5 py-1 shadow-xl backdrop-blur-md select-none"
          >
            <Show when={!isTesterPortalMode()}>
              <button
                type="button"
                onMouseDown={(ev) => ev.preventDefault()}
                onClick={handleQuote}
                class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11.5px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] transition-colors cursor-pointer"
              >
                <Quote size={12} />
                <span>{t("timeline.quoteSelectionBtn")}</span>
              </button>
              <span class="h-3.5 w-px bg-[var(--border-subtle)]" />
            </Show>
            <button
              type="button"
              onMouseDown={(ev) => ev.preventDefault()}
              onClick={(ev) => void handleCopy(ev)}
              title={copied() ? t("common.copied") : t("common.copy")}
              class="inline-flex items-center gap-1 rounded-full px-2 py-1 text-[11.5px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <Show when={copied()} fallback={<Copy size={11.5} />}>
                <Check size={11.5} class="text-[var(--ok-text)]" />
              </Show>
              <span>{copied() ? t("common.copied") : t("common.copy")}</span>
            </button>
          </div>
        </Portal>
      )}
    </Show>
  );
}
