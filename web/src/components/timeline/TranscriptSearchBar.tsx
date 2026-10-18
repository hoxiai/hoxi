// web/src/components/timeline/TranscriptSearchBar.tsx
import { createDeferred, createEffect, createMemo, createSignal, mapArray, on, onMount, Show } from "solid-js";
import { ChevronDown, ChevronUp, Search, SlidersHorizontal, X } from "lucide-solid";
import type { MessageTurn } from "../../store/workspace";
import { t } from "../../i18n";

function turnSearchableText(turn: MessageTurn): string {
  const chunks: string[] = [];
  for (const part of turn.parts) {
    if (part.type === "text" || part.type === "reasoning") {
      if (part.text) chunks.push(part.text);
    } else if (part.type === "tool") {
      chunks.push(part.tool || "");
      if (part.state.input) chunks.push(JSON.stringify(part.state.input));
      if (part.state.status === "completed" && part.state.output) {
        chunks.push(part.state.output);
      }
      if (part.state.status === "error" && part.state.error) {
        chunks.push(part.state.error);
      }
    }
  }
  return chunks.join("\n");
}

export function firstMatchingPartID(turn: MessageTurn, regex: RegExp): string | undefined {
  for (const part of turn.parts) {
    regex.lastIndex = 0;
    if (regex.test(turnSearchableText({ info: turn.info, parts: [part] }))) return part.id;
  }
  return undefined;
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * Ported from kilocode TranscriptSearch.tsx:
 * Floating in-session transcript search bar supporting Case-Sensitive (`Aa`),
 * Whole-Word (`ab`), and Regex (`.*`) matching with Enter / Shift+Enter navigation.
 */
export default function TranscriptSearchBar(props: {
  open: boolean;
  turns: MessageTurn[];
  onClose: () => void;
  onActiveTurnChange: (messageID: string | null, partID?: string) => void;
}) {
  const [query, setQuery] = createSignal("");
  const [matchCase, setMatchCase] = createSignal(false);
  const [wholeWord, setWholeWord] = createSignal(false);
  const [useRegex, setUseRegex] = createSignal(false);
  const [currentIndex, setCurrentIndex] = createSignal(0);
  const deferredQuery = createDeferred(query, { timeoutMs: 150 });
  const searchableTurns = mapArray(
    () => props.open && query().trim() ? props.turns : [],
    (turn) => ({ id: () => turn.info.id, turn, text: createMemo(() => turnSearchableText(turn)) })
  );

  let inputRef: HTMLInputElement | undefined;

  createEffect(() => {
    if (props.open) {
      queueMicrotask(() => inputRef?.focus());
    } else {
      props.onActiveTurnChange(null);
    }
  });

  const searchResult = createMemo<{
    matchedTurnIDs: string[];
    invalidRegex: boolean;
    partIDs?: Record<string, string | undefined>;
  }>(() => {
    if (!props.open) return { matchedTurnIDs: [], invalidRegex: false };
    const q = deferredQuery().trim();
    if (!q) return { matchedTurnIDs: [], invalidRegex: false };

    let pattern = useRegex() ? q : escapeRegExp(q);
    if (wholeWord()) {
      pattern = `\\b(?:${pattern})\\b`;
    }
    const flags = matchCase() ? "g" : "gi";

    let regex: RegExp;
    try {
      regex = new RegExp(pattern, flags);
    } catch {
      return { matchedTurnIDs: [], invalidRegex: true };
    }

    const matched: string[] = [];
    const partIDs: Record<string, string | undefined> = {};
    for (const turn of searchableTurns()) {
      const text = turn.text();
      regex.lastIndex = 0;
      if (regex.test(text)) {
        matched.push(turn.id());
        partIDs[turn.id()] = firstMatchingPartID(turn.turn, regex);
      }
    }
    return { matchedTurnIDs: matched, invalidRegex: false, partIDs };
  });

  const activeTurnID = createMemo(() => {
    const ids = searchResult().matchedTurnIDs;
    return ids[Math.min(currentIndex(), ids.length - 1)] ?? null;
  });

  createEffect(() => {
    const ids = searchResult().matchedTurnIDs;
    if (ids.length === 0) {
      setCurrentIndex(0);
      return;
    }
    const clamped = Math.min(currentIndex(), ids.length - 1);
    if (clamped !== currentIndex()) setCurrentIndex(clamped);
  });

  const notifyMatch = (id: string | null) => props.onActiveTurnChange(id, id ? searchResult().partIDs?.[id] : undefined);
  const activePartID = createMemo(() => {
    const id = activeTurnID();
    return id ? searchResult().partIDs?.[id] : undefined;
  });
  createEffect(on([activeTurnID, activePartID], ([id, partID]) => props.onActiveTurnChange(id, partID)));

  const goNext = () => {
    const ids = searchResult().matchedTurnIDs;
    if (ids.length === 0) return;
    const next = (currentIndex() + 1) % ids.length;
    if (next === currentIndex()) notifyMatch(ids[next] ?? null);
    setCurrentIndex(next);
  };

  const goPrev = () => {
    const ids = searchResult().matchedTurnIDs;
    if (ids.length === 0) return;
    const prev = (currentIndex() - 1 + ids.length) % ids.length;
    if (prev === currentIndex()) notifyMatch(ids[prev] ?? null);
    setCurrentIndex(prev);
  };

  onMount(() => {
    inputRef?.focus();
  });

  return (
    <Show when={props.open}>
      <div class="transcript-search-panel flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-2.5 py-2 shadow-xl"
        role="search" onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.preventDefault();
            event.stopPropagation();
            props.onClose();
          }
        }}>
        <Search size={13} class="text-[var(--text-muted)] shrink-0" />
        <input
          ref={inputRef}
          type="text"
          value={query()}
          onInput={(ev) => {
            setQuery(ev.currentTarget.value);
            setCurrentIndex(0);
          }}
          onKeyDown={(ev) => {
            if (ev.key === "Enter") {
              ev.preventDefault();
              if (ev.shiftKey) goPrev();
              else goNext();
            } else if (ev.key === "Escape") {
              ev.preventDefault();
              ev.stopPropagation();
              props.onClose();
            }
          }}
          placeholder={t("timeline.searchPlaceholder")}
          aria-label={t("timeline.searchPlaceholder")}
          class="flex-1 min-w-0 bg-transparent text-[12.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
        />

        {/* Match Options: Aa / ab / .* */}
        <details class="relative shrink-0">
          <summary class="flex list-none cursor-pointer rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] [&::-webkit-details-marker]:hidden"
            title={t("timeline.searchOptions")} aria-label={t("timeline.searchOptions")}>
            <SlidersHorizontal size={13} />
          </summary>
        <div class="transcript-search-options flex items-center gap-0.5 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-lg">
          <button
            type="button"
            onClick={() => setMatchCase((v) => !v)}
            class={`rounded px-1.5 py-0.5 font-mono text-[10.5px] font-semibold transition-colors cursor-pointer ${
              matchCase()
                ? "bg-[var(--accent-surface)] text-[var(--accent-text)] border border-[var(--accent-border)]"
                : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)]"
            }`}
            title={t("timeline.matchCaseTooltip")}
          >
            Aa
          </button>
          <button
            type="button"
            onClick={() => setWholeWord((v) => !v)}
            class={`rounded px-1.5 py-0.5 font-mono text-[10.5px] font-semibold transition-colors cursor-pointer ${
              wholeWord()
                ? "bg-[var(--accent-surface)] text-[var(--accent-text)] border border-[var(--accent-border)]"
                : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)]"
            }`}
            title={t("timeline.wholeWordTooltip")}
          >
            ab
          </button>
          <button
            type="button"
            onClick={() => setUseRegex((v) => !v)}
            class={`rounded px-1.5 py-0.5 font-mono text-[10.5px] font-semibold transition-colors cursor-pointer ${
              useRegex()
                ? "bg-[var(--accent-surface)] text-[var(--accent-text)] border border-[var(--accent-border)]"
                : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)]"
            }`}
            title={t("timeline.regexTooltip")}
          >
            .*
          </button>
        </div>
        </details>

        {/* Match Counter */}
        <Show
          when={!searchResult().invalidRegex}
          fallback={
            <span class="shrink-0 font-mono text-[11px] text-[var(--danger-text)]">
              {t("timeline.invalidRegex")}
            </span>
          }
        >
          <Show when={query().trim().length > 0}>
            <span class="shrink-0 font-mono text-[11px] text-[var(--text-muted)] tabular-nums">
              {searchResult().matchedTurnIDs.length > 0
                ? `${currentIndex() + 1} / ${searchResult().matchedTurnIDs.length}`
                : t("timeline.noResults")}
            </span>
          </Show>
        </Show>

        {/* Prev / Next / Close */}
        <div class="flex items-center gap-0.5 shrink-0 border-l border-[var(--border-subtle)] pl-1.5">
          <button
            type="button"
            disabled={searchResult().matchedTurnIDs.length === 0}
            onClick={goPrev}
            class="rounded p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-30 cursor-pointer"
            title={t("timeline.prevMatchTooltip")}
          >
            <ChevronUp size={13} />
          </button>
          <button
            type="button"
            disabled={searchResult().matchedTurnIDs.length === 0}
            onClick={goNext}
            class="rounded p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-30 cursor-pointer"
            title={t("timeline.nextMatchTooltip")}
          >
            <ChevronDown size={13} />
          </button>
          <button
            type="button"
            onClick={props.onClose}
            class="rounded p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
            title={t("common.close")}
          >
            <X size={13} />
          </button>
        </div>
      </div>
    </Show>
  );
}
