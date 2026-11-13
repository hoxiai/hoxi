// web/src/components/timeline/TranscriptSearchBar.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  on,
  onCleanup,
  onMount,
  Show,
} from "solid-js";
import { ChevronDown, ChevronUp, Search, SlidersHorizontal, X } from "lucide-solid";
import type { MessageTurn } from "../../store/workspace";
import { createTranscriptSearch } from "../../store/workspace";
import { clearTranscriptSearchHighlights, highlightTranscriptSearch } from "./transcript-search-highlights";
import { t } from "../../i18n";

/**
 * Ported from kilocode TranscriptSearch.tsx:
 * Floating in-session transcript search bar supporting Case-Sensitive (`Aa`),
 * Whole-Word (`ab`), and Regex (`.*`) matching with Enter / Shift+Enter navigation.
 */
export default function TranscriptSearchBar(props: {
  open: boolean;
  sessionID?: string | null;
  turns: MessageTurn[];
  onClose: () => void;
  onActiveTurnChange: (messageID: string | null, partID?: string) => void;
}) {
  const [query, setQuery] = createSignal("");
  const [matchCase, setMatchCase] = createSignal(false);
  const [wholeWord, setWholeWord] = createSignal(false);
  const [useRegex, setUseRegex] = createSignal(false);
  const [currentIndex, setCurrentIndex] = createSignal(0);
  const [highlightRevision, setHighlightRevision] = createSignal(0);
  const options = createMemo(() => ({ query: query(), matchCase: matchCase(), wholeWord: wholeWord(), useRegex: useRegex() }));
  const search = createTranscriptSearch({ enabled: () => props.open, sessionID: () => props.sessionID,
    turns: () => props.turns, options });
  const searchResult = createMemo(() => ({
    matchedTurnIDs: search.state().matches.map((match) => match.messageID),
    partIDs: Object.fromEntries(search.state().matches.map((match) => [match.messageID, match.partID])),
    invalidRegex: search.state().status === "invalid",
  }));

  let inputRef: HTMLInputElement | undefined;

  createEffect(() => {
    if (props.open) {
      queueMicrotask(() => inputRef?.focus());
    } else {
      props.onActiveTurnChange(null);
      clearTranscriptSearchHighlights();
    }
  });

  onCleanup(() => {
    clearTranscriptSearchHighlights();
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

  let navigation = 0;
  const notifyMatch = async (id: string | null) => {
    const version = ++navigation;
    const partID = id ? searchResult().partIDs[id] : undefined;
    if (!id) { props.onActiveTurnChange(null); return; }
    try {
      if (await search.reveal({ messageID: id, partID }) && version === navigation && props.open) {
        props.onActiveTurnChange(id, partID);
        setHighlightRevision((value) => value+1);
      }
    } catch { /* A failed history page must not navigate to a stale target. */ }
  };
  const activePartID = createMemo(() => {
    const id = activeTurnID();
    return id ? searchResult().partIDs?.[id] : undefined;
  });
  createEffect(on([activeTurnID, activePartID], ([id]) => { void notifyMatch(id); }));
  onCleanup(() => { navigation++; });

  createEffect(() => {
    highlightRevision();
    const rx = search.pattern();
    const tID = activeTurnID();
    const pID = activePartID();
    if (!props.open || !rx) {
      clearTranscriptSearchHighlights();
      return;
    }
    onCleanup(highlightTranscriptSearch(rx, tID, pID));
  });

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
          class="flex-1 min-w-0 bg-transparent text-[13px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
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
            class={`rounded px-1.5 py-0.5 font-mono text-[11px] font-semibold transition-colors cursor-pointer ${
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
            class={`rounded px-1.5 py-0.5 font-mono text-[11px] font-semibold transition-colors cursor-pointer ${
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
            class={`rounded px-1.5 py-0.5 font-mono text-[11px] font-semibold transition-colors cursor-pointer ${
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
              {search.state().status === "searching" ? t("timeline.searchSearching")
                : search.state().status === "timeout" ? t("timeline.searchTimeout")
                : search.state().status === "error" ? t("timeline.searchUnavailable")
                : searchResult().matchedTurnIDs.length > 0
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
