// web/src/components/inspector/FileSourceCodeView.tsx
// Syntax-highlighted source view for FilePreviewPane with:
// - Incremental line windowing for large files
// - On-demand in-file search (Cmd/Ctrl+F when focused, Enter/Shift+Enter stepping)
// - Delegated line-number click / Shift+click range selection -> hoxi-context v1 quote chip
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { ChevronDown, ChevronUp, Quote, Search, X } from "lucide-solid";
import { t } from "../../i18n";
import { isTesterPortalMode } from "../../store/workspace";
import type { ContextEnvelope } from "../../store/context-protocol";
import type { HighlightedCode } from "../timeline/syntax-highlight";

const MAX_QUOTED_LINES = 80;

export function findMatchingLineIndices(lines: readonly string[], query: string): number[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const matches: number[] = [];
  for (let i = 0; i < lines.length; i++) {
    if (lines[i].toLowerCase().includes(q)) {
      matches.push(i + 1); // 1-based line number
    }
  }
  return matches;
}

export function formatLineRangeLabel(start: number, end: number): string {
  const lo = Math.min(start, end);
  const hi = Math.max(start, end);
  return lo === hi ? `${lo}` : `${lo}-${hi}`;
}

export default function FileSourceCodeView(props: {
  filePath: string;
  sourceLines: string[];
  visibleLines: string[];
  highlighted: HighlightedCode | undefined;
  changedLines: Set<number>;
  highlightedLine: number | null;
  searchOpen: boolean;
  onCloseSearch: () => void;
  onEnsureLineVisible: (line: number) => void;
  scrollContainer: () => HTMLDivElement | undefined;
}) {
  let searchInputRef: HTMLInputElement | undefined;
  let scrollRafId = 0;
  const [searchQuery, setSearchQuery] = createSignal("");
  const [activeMatchIdx, setActiveMatchIdx] = createSignal(0);
  const [selectedRange, setSelectedRange] = createSignal<{ anchor: number; end: number } | null>(null);

  onCleanup(() => {
    if (scrollRafId) cancelAnimationFrame(scrollRafId);
  });

  // Reset search & selection when switching files.
  createEffect(() => {
    void props.filePath;
    setSearchQuery("");
    setActiveMatchIdx(0);
    setSelectedRange(null);
  });

  createEffect(() => {
    if (props.searchOpen) {
      queueMicrotask(() => {
        searchInputRef?.focus();
        searchInputRef?.select();
      });
    }
  });

  const matchLines = createMemo(() =>
    props.searchOpen ? findMatchingLineIndices(props.sourceLines, searchQuery()) : []
  );
  const matchLineSet = createMemo(() => new Set(matchLines()));

  const safeMatchIdx = createMemo(() => {
    const len = matchLines().length;
    if (len === 0) return 0;
    return Math.min(Math.max(0, activeMatchIdx()), len - 1);
  });

  const activeMatchLine = createMemo(() => {
    const list = matchLines();
    if (list.length === 0) return null;
    return list[safeMatchIdx()] ?? null;
  });

  const scrollToLine = (line: number) => {
    props.onEnsureLineVisible(line);
    if (scrollRafId) cancelAnimationFrame(scrollRafId);
    scrollRafId = requestAnimationFrame(() => {
      scrollRafId = 0;
      const target = props.scrollContainer()?.querySelector<HTMLElement>(`[data-line="${line}"]`);
      target?.scrollIntoView({ block: "center", behavior: "smooth" });
    });
  };

  createEffect(() => {
    const line = activeMatchLine();
    if (line !== null) {
      scrollToLine(line);
    }
  });

  const stepMatch = (delta: 1 | -1) => {
    const list = matchLines();
    if (list.length === 0) return;
    setActiveMatchIdx((safeMatchIdx() + delta + list.length) % list.length);
  };

  // Capture-phase Escape closes file search or line selection before MessageStream's
  // global Escape handler can treat it as a request to stop an active agent turn.
  createEffect(() => {
    if (!selectedRange() && !props.searchOpen) return;
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key !== "Escape") return;
      if (props.searchOpen) {
        ev.preventDefault();
        ev.stopImmediatePropagation();
        props.onCloseSearch();
      } else if (selectedRange()) {
        ev.preventDefault();
        ev.stopImmediatePropagation();
        setSelectedRange(null);
      }
    };
    window.addEventListener("keydown", onKeyDown, { capture: true });
    onCleanup(() => window.removeEventListener("keydown", onKeyDown, { capture: true }));
  });

  const normalizedSelection = createMemo(() => {
    const r = selectedRange();
    if (!r) return null;
    return {
      start: Math.min(r.anchor, r.end),
      end: Math.max(r.anchor, r.end),
    };
  });

  // Single delegated click listener on <pre> for line-number gutter clicks (0 per-line handlers).
  const handlePreClick = (ev: MouseEvent) => {
    if (isTesterPortalMode()) return;
    const target = ev.target instanceof Element ? ev.target.closest<HTMLElement>("[data-line-gutter]") : null;
    if (!target) return;
    const lineNo = Number(target.dataset.lineGutter);
    if (!Number.isFinite(lineNo) || lineNo < 1) return;
    ev.preventDefault();
    if (ev.shiftKey && selectedRange()) {
      setSelectedRange({ anchor: selectedRange()!.anchor, end: lineNo });
    } else if (selectedRange()?.anchor === lineNo && selectedRange()?.end === lineNo) {
      setSelectedRange(null);
    } else {
      setSelectedRange({ anchor: lineNo, end: lineNo });
    }
  };

  const handleQuoteSelectedLines = () => {
    const sel = normalizedSelection();
    if (!sel) return;
    const clampedEnd = Math.min(props.sourceLines.length, sel.start + MAX_QUOTED_LINES - 1, sel.end);
    const snippet = props.sourceLines.slice(sel.start - 1, clampedEnd).join("\n");
    const rangeLabel = formatLineRangeLabel(sel.start, clampedEnd);
    const envelope: ContextEnvelope = {
      version: 1,
      items: [
        {
          kind: "quote",
          title: `${props.filePath}:L${rangeLabel}`,
          content: snippet,
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
    setSelectedRange(null);
  };

  return (
    <div class="relative min-h-full">
      {/* Sticky In-File Find Bar (mounted only when searchOpen is true) */}
      <Show when={props.searchOpen}>
        <div class="sticky top-0 z-20 flex items-center gap-1.5 border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/95 px-2.5 py-1.5 text-[11.5px] backdrop-blur-sm">
          <Search size={12} class="shrink-0 text-[var(--text-muted)]" />
          <input
            ref={searchInputRef}
            type="text"
            value={searchQuery()}
            onInput={(ev) => {
              setSearchQuery(ev.currentTarget.value);
              setActiveMatchIdx(0);
            }}
            onKeyDown={(ev) => {
              if (ev.key === "Enter") {
                ev.preventDefault();
                stepMatch(ev.shiftKey ? -1 : 1);
              } else if (ev.key === "Escape") {
                ev.preventDefault();
                props.onCloseSearch();
              }
            }}
            placeholder={t("inspector.files.searchInFilePlaceholder")}
            class="min-w-0 flex-1 bg-transparent text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
          />
          <Show when={searchQuery().trim().length > 0}>
            <span class="shrink-0 font-mono text-[10.5px] tabular-nums text-[var(--text-muted)]">
              {matchLines().length > 0
                ? `${safeMatchIdx() + 1}/${matchLines().length}`
                : t("inspector.files.searchNoMatches")}
            </span>
          </Show>
          <button
            type="button"
            disabled={matchLines().length === 0}
            onClick={() => stepMatch(-1)}
            title={t("inspector.files.searchPrevMatch")}
            class="rounded p-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-35 cursor-pointer"
          >
            <ChevronUp size={13} />
          </button>
          <button
            type="button"
            disabled={matchLines().length === 0}
            onClick={() => stepMatch(1)}
            title={t("inspector.files.searchNextMatch")}
            class="rounded p-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-35 cursor-pointer"
          >
            <ChevronDown size={13} />
          </button>
          <button
            type="button"
            onClick={() => props.onCloseSearch()}
            title={t("common.close")}
            class="rounded p-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <X size={13} />
          </button>
        </div>
      </Show>

      {/* Sticky Line-Range Quote Pill (zero-height overlay so it never shifts lines or overlaps find bar) */}
      <Show when={!isTesterPortalMode() && normalizedSelection()}>
        {(sel) => (
          <div
            class={`pointer-events-none sticky z-20 flex h-0 justify-end overflow-visible pr-3 ${
              props.searchOpen ? "top-11" : "top-2"
            }`}
          >
            <div class="pointer-events-auto inline-flex h-fit items-center gap-1 rounded-full border border-[var(--accent-border)] bg-[var(--bg-elevated)]/95 px-2.5 py-1 text-[11px] shadow-md backdrop-blur-sm">
              <button
                type="button"
                onClick={handleQuoteSelectedLines}
                class="inline-flex items-center gap-1.5 font-medium text-[var(--accent-primary)] hover:opacity-85 cursor-pointer"
              >
                <Quote size={11} />
                <span>
                  {t("inspector.files.quoteLineAction", {
                    range: formatLineRangeLabel(sel().start, sel().end),
                  })}
                </span>
              </button>
              <button
                type="button"
                onClick={() => setSelectedRange(null)}
                title={t("common.close")}
                class="ml-0.5 rounded-full p-0.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <X size={11} />
              </button>
            </div>
          </div>
        )}
      </Show>

      <pre
        onClick={handlePreClick}
        class="py-3 font-mono leading-relaxed text-[var(--text-primary)] whitespace-pre selectable-text"
        style={{ "font-size": "var(--app-code-text-size)" }}
      >
        <For each={props.visibleLines}>
          {(line, index) => {
            const lineNo = () => index() + 1;
            const isChanged = () => props.changedLines.has(lineNo());
            const isTarget = () => props.highlightedLine === lineNo();
            const isActiveMatch = () => activeMatchLine() === lineNo();
            const isMatch = () => matchLineSet().has(lineNo());
            const isSelected = () => {
              const sel = normalizedSelection();
              return Boolean(sel && lineNo() >= sel.start && lineNo() <= sel.end);
            };
            return (
              <div
                data-line={lineNo()}
                class={`flex min-h-[1.55em] px-3 ${
                  isSelected()
                    ? "bg-[var(--accent-surface)]/80 border-l-[3px] border-[var(--accent-primary)]"
                    : isActiveMatch()
                      ? "bg-amber-500/25 border-l-[3px] border-amber-400"
                      : isMatch()
                        ? "bg-amber-500/10 border-l-[3px] border-amber-400/50"
                        : isChanged()
                          ? "bg-emerald-500/18 border-l-[3px] border-emerald-400"
                          : isTarget()
                            ? "bg-[var(--accent-surface)] border-l-[3px] border-[var(--accent-primary)]"
                            : "border-l-[3px] border-transparent"
                }`}
              >
                <span
                  data-line-gutter={lineNo()}
                  title={isTesterPortalMode() ? undefined : t("inspector.files.quoteLineHint")}
                  class={`mr-2 w-10 shrink-0 select-none text-right ${
                    isTesterPortalMode()
                      ? ""
                      : "cursor-pointer hover:text-[var(--accent-primary)] transition-colors"
                  } ${
                    isSelected()
                      ? "text-[var(--accent-primary)] font-semibold"
                      : isChanged()
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
                    when={props.highlighted?.tokens[index()]}
                    fallback={line || " "}
                  >
                    {(tokens) => (
                      <For each={tokens()}>
                        {(token) => (
                          <span
                            style={{
                              color: token.color,
                              "font-style": (token.fontStyle ?? 0) & 1 ? "italic" : undefined,
                              "font-weight": (token.fontStyle ?? 0) & 2 ? "bold" : undefined,
                              "text-decoration": (token.fontStyle ?? 0) & 4 ? "underline" : undefined,
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
    </div>
  );
}
