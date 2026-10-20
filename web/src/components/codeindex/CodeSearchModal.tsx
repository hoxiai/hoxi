// web/src/components/codeindex/CodeSearchModal.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { History, LoaderCircle, RefreshCw, Search, X } from "lucide-solid";
import { t } from "../../i18n";
import {
  fetchCodeIndexStatus,
  refreshCodeIndex,
  searchCodeIndex,
  type CodeIndexResult,
  type CodeIndexStatus,
} from "../../services/code-index-api";
import {
  searchSessionRecall,
  type RecallResult,
} from "../../services/recall-api";

type SearchMode = "code" | "sessions";

export default function CodeSearchModal(props: {
  open: boolean;
  directory?: string | null;
  onClose: () => void;
  onSelectCode: (result: CodeIndexResult) => void;
  onSelectSession: (result: RecallResult) => void;
}) {
  const [mode, setMode] = createSignal<SearchMode>("code");
  const [query, setQuery] = createSignal("");
  const [codeResults, setCodeResults] = createSignal<CodeIndexResult[]>([]);
  const [recallResults, setRecallResults] = createSignal<RecallResult[]>([]);
  const [status, setStatus] = createSignal<CodeIndexStatus | null>(null);
  const [searching, setSearching] = createSignal(false);
  const [building, setBuilding] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  let inputRef: HTMLInputElement | undefined;
  let generation = 0;

  const directory = createMemo(() => props.directory?.trim() || "");
  const activeResults = createMemo(() =>
    mode() === "code" ? codeResults() : recallResults()
  );
  const placeholder = createMemo(() =>
    mode() === "code"
      ? t("timeline.codeSearchPlaceholder")
      : t("timeline.recallPlaceholder")
  );
  const count = createMemo(() => activeResults().length);

  const warm = async () => {
    const dir = directory();
    if (!dir) return;
    const current = ++generation;
    setBuilding(true);
    setError(null);
    try {
      const next = await fetchCodeIndexStatus(dir);
      if (current === generation) setStatus(next);
    } catch (err) {
      if (current === generation) setError(String(err));
    } finally {
      if (current === generation) setBuilding(false);
    }
  };

  createEffect(() => {
    if (!props.open) {
      setQuery("");
      setCodeResults([]);
      setRecallResults([]);
      setError(null);
      return;
    }
    void warm();
    queueMicrotask(() => inputRef?.focus());
  });

  createEffect(() => {
    if (!props.open) return;
    const dir = directory();
    const text = query().trim();
    const selected = mode();
    if (!dir) return;
    if (!text) {
      setCodeResults([]);
      setRecallResults([]);
      setSearching(false);
      return;
    }
    const current = ++generation;
    setSearching(true);
    const timer = setTimeout(() => {
      const run =
        selected === "code"
          ? searchCodeIndex(dir, text).then((response) => {
              if (current !== generation) return;
              setCodeResults(response.results);
              setStatus({
                directory: response.directory,
                ready: true,
                files: response.files,
                chunks: response.chunks,
                indexedAt: response.indexedAt,
                root: status()?.root || "",
              });
            })
          : searchSessionRecall(dir, text).then((response) => {
              if (current !== generation) return;
              setRecallResults(response.results);
            });
      run
        .then(() => {
          if (current === generation) setError(null);
        })
        .catch((err) => {
          if (current === generation) setError(String(err));
        })
        .finally(() => {
          if (current === generation) setSearching(false);
        });
    }, 250);
    onCleanup(() => clearTimeout(timer));
  });

  const rebuild = async () => {
    const dir = directory();
    if (!dir || building()) return;
    const current = ++generation;
    setBuilding(true);
    try {
      const next = await refreshCodeIndex(dir);
      if (current !== generation) return;
      setStatus(next);
      if (query().trim() && mode() === "code") {
        const response = await searchCodeIndex(dir, query().trim());
        if (current === generation) setCodeResults(response.results);
      }
    } catch (err) {
      if (current === generation) setError(String(err));
    } finally {
      if (current === generation) setBuilding(false);
    }
  };

  const choose = (index = 0) => {
    if (mode() === "code") {
      const result = codeResults()[index];
      if (!result) return;
      props.onSelectCode(result);
    } else {
      const result = recallResults()[index];
      if (!result) return;
      props.onSelectSession(result);
    }
    props.onClose();
  };

  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      event.preventDefault();
      props.onClose();
    } else if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      choose();
    }
  };

  return (
    <Show when={props.open}>
      <div
        class="fixed inset-0 z-[80] flex items-start justify-center bg-black/40 px-4 pt-[12vh] backdrop-blur-[2px]"
        onMouseDown={(event) => {
          if (event.target === event.currentTarget) props.onClose();
        }}
      >
        <div
          class="w-full max-w-2xl overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] shadow-2xl"
          onKeyDown={onKeyDown}
        >
          <div class="flex items-center gap-1 border-b border-[var(--border-subtle)] px-2 py-1.5">
            <button
              type="button"
              onClick={() => setMode("code")}
              class={`inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] font-medium cursor-pointer ${
                mode() === "code"
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)]"
              }`}
            >
              <Search size={13} />
              {t("timeline.codeSearchTabCode")}
            </button>
            <button
              type="button"
              onClick={() => setMode("sessions")}
              class={`inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] font-medium cursor-pointer ${
                mode() === "sessions"
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)]"
              }`}
            >
              <History size={13} />
              {t("timeline.codeSearchTabSessions")}
            </button>
            <button
              type="button"
              onClick={props.onClose}
              class="ml-auto rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <X size={14} />
            </button>
          </div>

          <div class="flex items-center gap-2 border-b border-[var(--border-subtle)] px-3 py-2">
            <Show
              when={!searching() && !building()}
              fallback={<LoaderCircle size={15} class="animate-spin shrink-0 text-[var(--text-muted)]" />}
            >
              <Search size={15} class="shrink-0 text-[var(--text-muted)]" />
            </Show>
            <input
              ref={inputRef}
              value={query()}
              onInput={(event) => setQuery(event.currentTarget.value)}
              placeholder={placeholder()}
              class="min-w-0 flex-1 bg-transparent text-[13px] text-[var(--text-primary)] outline-none placeholder:text-[var(--text-muted)]"
            />
            <Show when={mode() === "code"}>
              <button
                type="button"
                onClick={() => void rebuild()}
                disabled={building() || !directory()}
                title={t("timeline.codeSearchRefresh")}
                class="rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-40 cursor-pointer"
              >
                <RefreshCw size={14} class={building() ? "animate-spin" : ""} />
              </button>
            </Show>
          </div>

          <div class="flex items-center justify-between gap-2 px-3 py-1.5 text-[11px] text-[var(--text-muted)]">
            <span>
              <Show
                when={mode() === "sessions" || (!building() && status())}
                fallback={t("timeline.codeSearchIndexing")}
              >
                {mode() === "sessions"
                  ? t("timeline.recallResultsCount", { count: count() })
                  : `${t("timeline.codeSearchResultsCount", { count: count() })} · ${status()?.files ?? 0} files · ${status()?.chunks ?? 0} chunks`}
              </Show>
            </span>
            <span class="hidden sm:inline">
              {mode() === "code"
                ? t("timeline.codeSearchHint")
                : t("timeline.recallSelectHint")}
            </span>
          </div>

          <Show when={error()}>
            <p class="border-t border-[var(--border-subtle)] px-3 py-2 text-[11.5px] text-[var(--danger-text)] break-words">
              {t("timeline.codeSearchError", { error: error() || "" })}
            </p>
          </Show>

          <div class="max-h-[52vh] overflow-y-auto border-t border-[var(--border-subtle)]">
            <Show
              when={count() > 0}
              fallback={
                <p class="px-3 py-6 text-center text-[12px] text-[var(--text-muted)]">
                  {query().trim()
                    ? mode() === "code"
                      ? t("timeline.codeSearchEmpty")
                      : t("timeline.recallEmpty")
                    : mode() === "code"
                      ? t("timeline.codeSearchHint")
                      : t("timeline.recallSelectHint")}
                </p>
              }
            >
              <Show
                when={mode() === "code"}
                fallback={
                  <For each={recallResults()}>
                    {(result, index) => (
                      <button
                        type="button"
                        onClick={() => choose(index())}
                        class={`block w-full border-b border-[var(--border-subtle)] px-3 py-2 text-left transition-colors cursor-pointer hover:bg-[var(--bg-hover)] ${
                          index() === 0 ? "bg-[var(--bg-hover)]/60" : ""
                        }`}
                      >
                        <div class="flex items-center gap-2 text-[12px]">
                          <span class="truncate font-medium text-[var(--text-primary)]">{result.title || result.sessionID}</span>
                          <Show when={result.role}>
                            <span class="shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]">
                              {result.role}
                            </span>
                          </Show>
                          <span class="ml-auto shrink-0 text-[10px] text-[var(--text-faint)]">{result.score}</span>
                        </div>
                        <pre class="mt-1 line-clamp-2 overflow-hidden whitespace-pre-wrap break-words font-mono text-[11px] leading-snug text-[var(--text-secondary)]">
                          {result.snippet}
                        </pre>
                      </button>
                    )}
                  </For>
                }
              >
                <For each={codeResults()}>
                  {(result, index) => (
                    <button
                      type="button"
                      onClick={() => choose(index())}
                      class={`block w-full border-b border-[var(--border-subtle)] px-3 py-2 text-left transition-colors cursor-pointer hover:bg-[var(--bg-hover)] ${
                        index() === 0 ? "bg-[var(--bg-hover)]/60" : ""
                      }`}
                    >
                      <div class="flex items-center gap-2 text-[12px]">
                        <span class="truncate font-mono text-[var(--text-primary)]">{result.path}</span>
                        <span class="shrink-0 text-[10.5px] text-[var(--text-muted)]">
                          {result.startLine}-{result.endLine}
                        </span>
                        <Show when={result.symbol}>
                          <span class="shrink-0 rounded bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--accent-primary)]">
                            {result.symbol}
                          </span>
                        </Show>
                        <span class="ml-auto shrink-0 text-[10px] text-[var(--text-faint)]">{result.score}</span>
                      </div>
                      <pre class="mt-1 line-clamp-2 overflow-hidden whitespace-pre-wrap break-words font-mono text-[11px] leading-snug text-[var(--text-secondary)]">
                        {result.snippet}
                      </pre>
                    </button>
                  )}
                </For>
              </Show>
            </Show>
          </div>
        </div>
      </div>
    </Show>
  );
}
