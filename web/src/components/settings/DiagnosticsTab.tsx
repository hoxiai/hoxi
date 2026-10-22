import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { ChevronLeft, ChevronRight, Columns3, RefreshCw, X } from "lucide-solid";
import { locale, t } from "../../i18n";
import { formatCompactNumber, formatUsd } from "../../i18n/format";
import {
  estimateTokenCostUsd, loadRequestContent, loadRequestLog, requestLog, requestLogError,
  requestLogLoading, requestLogTotal, syncOfficialModelPricing,
} from "../../store/workspace";
import type { RequestLogContent, RequestLogContentPart, RequestLogEntry } from "../../services/api";

const control = "rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer";
const button = "inline-flex items-center justify-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 disabled:cursor-default cursor-pointer";
const headCell = "px-3 py-2 text-left font-medium";

type ColumnKey = "time" | "providerModel" | "status" | "firstToken" | "duration" | "tokens" | "cost" | "error";
type StatusFilter = "" | "ok" | "failed" | "pending";

const COLUMNS: ColumnKey[] = ["time", "providerModel", "status", "firstToken", "duration", "tokens", "cost", "error"];
const SLOW_OPTIONS = [0, 5000, 10000, 30000];
const PAGE_SIZES = [20, 50, 100];
// A call still running after this long is flagged as timed out.
const TIMEOUT_MS = 60_000;
// Background refresh cadence while the log is open.
const REFRESH_MS = 5000;

const COLUMN_WIDTH: Record<ColumnKey, string> = {
  time: "w-[86px]",
  providerModel: "",
  status: "w-[76px]",
  firstToken: "w-[72px]",
  duration: "w-[72px]",
  tokens: "w-[148px]",
  cost: "w-[84px]",
  error: "w-[160px]",
};

const COLUMN_LABEL: Record<ColumnKey, () => string> = {
  time: () => t("diagnostics.colTime"),
  providerModel: () => t("diagnostics.colProviderModel"),
  status: () => t("diagnostics.colStatus"),
  firstToken: () => t("diagnostics.colFirstToken"),
  duration: () => t("diagnostics.colDuration"),
  tokens: () => t("diagnostics.colTokens"),
  cost: () => t("diagnostics.colCost"),
  error: () => t("diagnostics.colError"),
};

// Prefer the runtime-reported cost; fall back to the shared catalog estimate so
// the row agrees with the usage history and the daily pulse.
function entryCost(entry: RequestLogEntry): number | null {
  if (entry.cost && entry.cost > 0) return entry.cost;
  return estimateTokenCostUsd(entry.modelID, {
    input: entry.inputTokens,
    output: entry.outputTokens,
    reasoning: entry.reasoningTokens,
    cacheRead: entry.cacheReadTokens,
    cacheWrite: entry.cacheWriteTokens,
  });
}

function formatDuration(ms: number): string {
  if (ms <= 0) return "—";
  if (ms < 1000) return `${ms}ms`;
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return t("diagnostics.durationSeconds", { seconds });
  return t("diagnostics.durationMinutes", { minutes: Math.floor(seconds / 60), seconds: seconds % 60 });
}

function formatWhen(ms: number): { date: string; time: string } {
  const value = new Date(ms);
  return {
    date: value.toLocaleDateString(locale()),
    time: value.toLocaleTimeString(locale(), { hour12: false }),
  };
}

function totalTokens(entry: RequestLogEntry): number {
  return entry.inputTokens + entry.outputTokens + entry.reasoningTokens + entry.cacheReadTokens + entry.cacheWriteTokens;
}

function isFailed(entry: RequestLogEntry): boolean {
  return entry.level === "error" || (!!entry.status && entry.status >= 400);
}

function isPending(entry: RequestLogEntry): boolean {
  return entry.level === "pending";
}

function badgeClass(entry: RequestLogEntry): string {
  if (isFailed(entry)) return "bg-[var(--danger-surface)] text-[var(--danger-text)]";
  if (isPending(entry)) return "bg-[var(--warn-surface)] text-[var(--warn-text)]";
  return "bg-emerald-500/10 text-emerald-600";
}

function badgeText(entry: RequestLogEntry): string {
  if (isFailed(entry)) return t("diagnostics.statusFailed");
  if (isPending(entry)) return t("diagnostics.statusPending");
  return t("diagnostics.statusSuccess");
}

function statusTitle(entry: RequestLogEntry): string {
  return entry.status ? `HTTP ${entry.status}` : badgeText(entry);
}

function rowTone(entry: RequestLogEntry): string {
  if (isFailed(entry)) return "bg-[var(--danger-surface)]/35";
  if (isPending(entry)) return "bg-[var(--warn-surface)]/25";
  return "";
}

function cellTone(entry: RequestLogEntry): string {
  if (isFailed(entry)) return "bg-[var(--danger-surface)]";
  if (isPending(entry)) return "bg-[var(--warn-surface)]";
  return "bg-[var(--bg-canvas)]";
}

function slowLabel(value: number): string {
  return value === 0 ? t("diagnostics.slowNone") : t("diagnostics.slowOver", { seconds: value / 1000 });
}

export default function DiagnosticsTab() {
  const [selected, setSelected] = createSignal<RequestLogEntry | null>(null);
  const [columnsOpen, setColumnsOpen] = createSignal(false);
  const [hidden, setHidden] = createSignal<ColumnKey[]>([]);
  const [status, setStatus] = createSignal<StatusFilter>("");
  const [provider, setProvider] = createSignal("");
  const [model, setModel] = createSignal("");
  const [slowMs, setSlowMs] = createSignal(0);
  const [query, setQuery] = createSignal("");
  const [page, setPage] = createSignal(1);
  const [pageSize, setPageSize] = createSignal(20);
  const [nowMs, setNowMs] = createSignal(Date.now());

  createEffect(() => {
    void syncOfficialModelPricing();
    const timer = setTimeout(() => void loadRequestLog(), 0);
    onCleanup(() => clearTimeout(timer));
  });

  // Only keep refreshing while something is running: tick the elapsed clock once
  // a second and re-poll for completion. When idle there is no polling at all,
  // so browsing pages never competes with a background query. Manual refresh
  // stays available.
  const hasPending = createMemo(() => requestLog.some(isPending));
  createEffect(() => {
    if (!hasPending()) return;
    setNowMs(Date.now());
    const tick = setInterval(() => setNowMs(Date.now()), 1000);
    const poll = setInterval(() => void loadRequestLog({ silent: true }), REFRESH_MS);
    onCleanup(() => {
      clearInterval(tick);
      clearInterval(poll);
    });
  });

  // Any filter change returns to the first page so the view never looks empty.
  createEffect(() => {
    status(); provider(); model(); slowMs(); query(); pageSize();
    setPage(1);
  });

  const providers = createMemo(() => unique(requestLog.map((entry) => entry.providerID)));
  const models = createMemo(() => unique(requestLog.map((entry) => entry.modelID)));

  const filtered = createMemo(() => requestLog.filter((entry) => {
    if (provider() && entry.providerID !== provider()) return false;
    if (model() && entry.modelID !== model()) return false;
    if (status() === "ok" && (isFailed(entry) || isPending(entry))) return false;
    if (status() === "failed" && !isFailed(entry)) return false;
    if (status() === "pending" && !isPending(entry)) return false;
    if (slowMs() > 0 && entry.durationMs < slowMs() && (entry.firstTokenMs || 0) < slowMs()) return false;
    const needle = query().trim().toLowerCase();
    if (needle && !`${entry.providerID} ${entry.modelID} ${entry.sessionID} ${entry.error || ""}`.toLowerCase().includes(needle)) return false;
    return true;
  }));

  // Price the whole filtered set exactly once and share the result with both the
  // total and each row, instead of re-pricing per row and per aggregate.
  const pricing = createMemo(() => {
    const byEntry = new Map<RequestLogEntry, number | null>();
    const unpriced = new Set<string>();
    let total = 0;
    for (const entry of filtered()) {
      const cost = entryCost(entry);
      byEntry.set(entry, cost);
      if (cost === null) unpriced.add(entry.modelID);
      else total += cost;
    }
    return { byEntry, total, unpriced: Array.from(unpriced).sort() };
  });

  const pageCount = createMemo(() => Math.max(1, Math.ceil(filtered().length / pageSize())));
  const rows = createMemo(() => {
    const start = (page() - 1) * pageSize();
    return filtered().slice(start, start + pageSize());
  });
  const hasFilters = createMemo(() => !!(status() || provider() || model() || slowMs() || query().trim()));

  const visible = (key: ColumnKey) => !hidden().includes(key);

  function clearFilters() {
    setStatus("");
    setProvider("");
    setModel("");
    setSlowMs(0);
    setQuery("");
  }

  return (
    <div class="space-y-3 select-text">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex min-w-0 items-baseline gap-2">
          <h3 class="text-[15px] font-semibold text-[var(--text-primary)]">{t("diagnostics.title")}</h3>
          <span class="rounded-full bg-[var(--bg-inset)] px-2 py-0.5 text-[11px] text-[var(--text-muted)]" title={t("diagnostics.listCount", { count: requestLogTotal() })}>
            {t("diagnostics.listCount", { count: filtered().length })}
          </span>
          <span class="text-[12px] text-[var(--text-muted)]">{t("diagnostics.localHint")}</span>
        </div>
        <div class="relative">
          <button type="button" class={button} aria-expanded={columnsOpen()} onClick={() => setColumnsOpen((open) => !open)}>
            <Columns3 size={13} />{t("diagnostics.columns")}
          </button>
          <Show when={columnsOpen()}>
            <div class="absolute right-0 z-10 mt-1 w-36 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-1.5 shadow-lg">
              <For each={COLUMNS}>
                {(key) => (
                  <label class="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]">
                    <input type="checkbox" checked={visible(key)} onChange={() => setHidden((current) => current.includes(key) ? current.filter((item) => item !== key) : [...current, key])} />
                    {COLUMN_LABEL[key]()}
                  </label>
                )}
              </For>
            </div>
          </Show>
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/35 p-2">
        <select aria-label={t("diagnostics.colStatus")} class={control} value={status()} onChange={(e) => setStatus(e.currentTarget.value as StatusFilter)}>
          <option value="">{t("diagnostics.statusAll")}</option>
          <option value="ok">{t("diagnostics.statusSuccess")}</option>
          <option value="failed">{t("diagnostics.statusFailed")}</option>
          <option value="pending">{t("diagnostics.statusPending")}</option>
        </select>
        <select aria-label={t("diagnostics.provider")} class={control} value={provider()} onChange={(e) => setProvider(e.currentTarget.value)}>
          <option value="">{t("diagnostics.allProviders")}</option>
          <For each={providers()}>{(id) => <option value={id}>{id}</option>}</For>
        </select>
        <select aria-label={t("diagnostics.colModel")} class={control} value={model()} onChange={(e) => setModel(e.currentTarget.value)}>
          <option value="">{t("diagnostics.allModels")}</option>
          <For each={models()}>{(id) => <option value={id}>{id}</option>}</For>
        </select>
        <select aria-label={t("diagnostics.slowLabel")} class={control} value={String(slowMs())} onChange={(e) => setSlowMs(Number(e.currentTarget.value))}>
          <For each={SLOW_OPTIONS}>{(value) => <option value={String(value)}>{slowLabel(value)}</option>}</For>
        </select>
        <input type="search" aria-label={t("diagnostics.search")} class={`${control} min-w-40 flex-1`} placeholder={t("diagnostics.searchPlaceholder")} value={query()} onInput={(e) => setQuery(e.currentTarget.value)} />
        <Show when={hasFilters()}>
          <button type="button" class="px-2 text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer" onClick={clearFilters}>
            {t("diagnostics.clearFilters")}
          </button>
        </Show>
        <button type="button" class={button} disabled={requestLogLoading()} title={t("diagnostics.refresh")} aria-label={t("diagnostics.refresh")} onClick={() => void loadRequestLog()}>
          <RefreshCw size={13} class={requestLogLoading() ? "animate-spin" : ""} />
        </button>
      </div>

      <Show when={requestLogError()}>
        <p role="alert" class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] p-3 text-[12px] text-[var(--danger-text)]">{t("diagnostics.loadFailed", { error: requestLogError() })}</p>
      </Show>
      <Show when={!requestLogLoading() && filtered().length === 0}>
        <p role="status" class="rounded-xl border border-dashed border-[var(--border-subtle)] p-6 text-center text-[12px] text-[var(--text-muted)]">{t("diagnostics.emptyFiltered")}</p>
      </Show>

      <Show when={rows().length > 0}>
        <div class="overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
          <table class="w-full table-fixed border-collapse text-[12px]">
            <thead>
              <tr class="border-b border-[var(--border-subtle)] text-[11px] text-[var(--text-muted)]">
                <Show when={visible("time")}><th class={`${headCell} ${COLUMN_WIDTH.time}`}>{t("diagnostics.colTime")}</th></Show>
                <Show when={visible("providerModel")}><th class={`${headCell} ${COLUMN_WIDTH.providerModel}`}>{t("diagnostics.colProviderModel")}</th></Show>
                <Show when={visible("status")}><th class={`${headCell} ${COLUMN_WIDTH.status}`}>{t("diagnostics.colStatus")}</th></Show>
                <Show when={visible("firstToken")}><th class={`${headCell} ${COLUMN_WIDTH.firstToken} text-right`}>{t("diagnostics.colFirstToken")}</th></Show>
                <Show when={visible("duration")}><th class={`${headCell} ${COLUMN_WIDTH.duration} text-right`}>{t("diagnostics.colDuration")}</th></Show>
                <Show when={visible("tokens")}><th class={`${headCell} ${COLUMN_WIDTH.tokens}`}>{t("diagnostics.colTokens")}</th></Show>
                <Show when={visible("cost")}><th class={`${headCell} ${COLUMN_WIDTH.cost} text-right`}>{t("diagnostics.colCost")}</th></Show>
                <Show when={visible("error")}><th class={`${headCell} ${COLUMN_WIDTH.error}`}>{t("diagnostics.colError")}</th></Show>
                <th class="sticky right-0 bg-[var(--bg-canvas)] px-3 py-2 text-right font-medium shadow-[-8px_0_8px_-8px_var(--border-strong)]">{t("diagnostics.colDetails")}</th>
              </tr>
            </thead>
            <tbody>
              <For each={rows()}>
                {(entry) => {
                  const when = () => formatWhen(entry.time);
                  const pending = () => isPending(entry);
                  const timedOut = () => pending() && nowMs() - entry.time >= TIMEOUT_MS;
                  return (
                    <tr class={`border-b border-[var(--border-subtle)]/70 last:border-0 ${rowTone(entry)}`}>
                      <Show when={visible("time")}>
                        <td class="whitespace-nowrap px-3 py-2 font-mono text-[11px] leading-4 text-[var(--text-secondary)]">
                          <div>{when().date}</div>
                          <div class="text-[var(--text-muted)]">{when().time}</div>
                        </td>
                      </Show>
                      <Show when={visible("providerModel")}>
                        <td class="px-3 py-2">
                          <div class="truncate text-[12px] text-[var(--text-primary)]" title={entry.account ? `${entry.providerID} · ${entry.account}` : entry.providerID}>
                            {entry.providerID}
                            <Show when={entry.account}><span class="ml-1.5 text-[11px] text-[var(--text-muted)]">{entry.account}</span></Show>
                          </div>
                          <div class="truncate font-mono text-[11px] text-[var(--text-secondary)]" title={entry.modelID}>{entry.modelID}</div>
                        </td>
                      </Show>
                      <Show when={visible("status")}>
                        <td class="px-3 py-2">
                          <span
                            class={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-medium ${timedOut() ? "bg-[var(--danger-surface)] text-[var(--danger-text)]" : badgeClass(entry)}`}
                            title={pending() ? formatDuration(nowMs() - entry.time) : statusTitle(entry)}
                          >
                            <Show when={pending()}>
                              <span class="h-1.5 w-1.5 animate-pulse rounded-full bg-current" aria-hidden="true" />
                            </Show>
                            {timedOut() ? t("diagnostics.statusTimeout") : badgeText(entry)}
                          </span>
                        </td>
                      </Show>
                      <Show when={visible("firstToken")}>
                        <td class="px-3 py-2 text-right font-mono text-[11px] text-[var(--text-secondary)]">{formatDuration(entry.firstTokenMs || 0)}</td>
                      </Show>
                      <Show when={visible("duration")}>
                        <td class="px-3 py-2 text-right font-mono text-[11px] text-[var(--text-secondary)]">
                          {formatDuration(pending() ? nowMs() - entry.time : entry.durationMs)}
                        </td>
                      </Show>
                      <Show when={visible("tokens")}>
                        <td class="px-3 py-2 font-mono text-[11px] leading-4 text-[var(--text-secondary)]">
                          <Show when={!pending() || totalTokens(entry) > 0} fallback={<span class="text-[var(--text-muted)]">{t("diagnostics.noError")}</span>}>
                            <button
                              type="button"
                              class="group block w-full text-left cursor-pointer"
                              title={t("diagnostics.contentClickHint")}
                              onClick={() => setSelected(entry)}
                            >
                              <div class="text-[var(--text-primary)] group-hover:text-[var(--accent-primary)] group-hover:underline" title={t("diagnostics.usageLine", { input: entry.inputTokens.toLocaleString(), output: entry.outputTokens.toLocaleString(), total: totalTokens(entry).toLocaleString() })}>
                                {t("diagnostics.tokensInOut", { input: formatCompactNumber(entry.inputTokens), output: formatCompactNumber(entry.outputTokens) })}
                              </div>
                            </button>
                          </Show>
                        </td>
                      </Show>
                      <Show when={visible("cost")}>
                        <td class="px-3 py-2 text-right font-mono text-[11px] text-[var(--text-secondary)]">
                          {(() => { const cost = pricing().byEntry.get(entry); return cost == null ? t("diagnostics.noError") : formatUsd(cost); })()}
                        </td>
                      </Show>
                      <Show when={visible("error")}>
                        <td class="truncate px-3 py-2 text-[11px] text-[var(--danger-text)]" title={entry.error || ""}>{entry.error || t("diagnostics.noError")}</td>
                      </Show>
                      <td class={`sticky right-0 px-3 py-2 text-right shadow-[-8px_0_8px_-8px_var(--border-strong)] ${cellTone(entry)}`}>
                        <button type="button" class={button} onClick={() => setSelected(entry)}>{t("diagnostics.details")}</button>
                      </td>
                    </tr>
                  );
                }}
              </For>
            </tbody>
          </table>
        </div>
      </Show>

      <Show when={rows().length > 0}>
        <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px] text-[var(--text-muted)]">
          <span title={t("diagnostics.costNote")}>{t("diagnostics.summaryCost", { cost: formatUsd(pricing().total) })}</span>
          <Show when={pricing().unpriced.length > 0}>
            <span class="text-[var(--warn-text)]" title={pricing().unpriced.join(", ")}>{t("diagnostics.costUnpriced", { models: pricing().unpriced.slice(0, 3).join(", ") })}</span>
          </Show>
          <span class="text-[var(--text-faint)]">{t("diagnostics.costNote")}</span>
        </div>
      </Show>

      <Show when={rows().length > 0}>
        <div class="flex flex-wrap items-center justify-between gap-2 text-[12px] text-[var(--text-muted)]">
          <span class="font-mono">{t("diagnostics.pageInfo", { page: page(), total: pageCount() })}</span>
          <div class="flex items-center gap-1.5">
            <select aria-label={t("diagnostics.pageSize", { size: pageSize() })} class={control} value={String(pageSize())} onChange={(e) => setPageSize(Number(e.currentTarget.value))}>
              <For each={PAGE_SIZES}>{(size) => <option value={String(size)}>{t("diagnostics.pageSize", { size })}</option>}</For>
            </select>
            <button type="button" class={button} disabled={page() <= 1} aria-label={t("diagnostics.pagePrev")} onClick={() => setPage((p) => Math.max(1, p - 1))}>
              <ChevronLeft size={13} />
            </button>
            <button type="button" class={button} disabled={page() >= pageCount()} aria-label={t("diagnostics.pageNext")} onClick={() => setPage((p) => Math.min(pageCount(), p + 1))}>
              <ChevronRight size={13} />
            </button>
          </div>
        </div>
      </Show>

      <Show when={selected()}>
        {(entry) => (
          <RequestDetail entry={entry()} onClose={() => setSelected(null)} />
        )}
      </Show>
    </div>
  );
}

function RequestDetail(props: { entry: RequestLogEntry; onClose: () => void }) {
  const when = () => formatWhen(props.entry.time);
  const [content, setContent] = createSignal<RequestLogContent | null>(null);
  const [contentFailed, setContentFailed] = createSignal(false);

  createEffect(() => {
    const id = props.entry.id;
    const sessionID = props.entry.sessionID;
    void props.entry.level;
    void props.entry.completed;
    setContent(null);
    setContentFailed(false);
    let cancelled = false;
    void loadRequestContent(id, sessionID)
      .then((res) => { if (!cancelled) setContent(res); })
      .catch(() => { if (!cancelled) setContentFailed(true); });
    onCleanup(() => { cancelled = true; });
  });

  return (
    <div class="fixed inset-0 z-[80] flex justify-end bg-black/30" onClick={props.onClose}>
    <section class="flex h-full w-[min(440px,92vw)] flex-col gap-3 overflow-y-auto border-l border-[var(--border-subtle)] bg-[var(--bg-panel)] p-4 shadow-2xl" onClick={(event) => event.stopPropagation()}>
      <div class="flex items-center justify-between gap-3">
        <h4 class="text-[13px] font-semibold text-[var(--text-primary)]">{t("diagnostics.detailTitle")}</h4>
        <button type="button" class={button} onClick={props.onClose}><X size={13} />{t("diagnostics.closeDetail")}</button>
      </div>
      <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-[12px]">
        <dt class="text-[var(--text-muted)]">{t("diagnostics.colTime")}</dt><dd class="font-mono text-[var(--text-secondary)]">{when().date} {when().time}</dd>
        <dt class="text-[var(--text-muted)]">{t("diagnostics.provider")}</dt><dd class="text-[var(--text-secondary)]">{props.entry.providerID}</dd>
        <dt class="text-[var(--text-muted)]">{t("diagnostics.colModel")}</dt><dd class="break-all font-mono text-[var(--text-secondary)]">{props.entry.modelID}</dd>
        <dt class="text-[var(--text-muted)]">{t("diagnostics.colStatus")}</dt><dd class="font-mono text-[var(--text-secondary)]">{statusTitle(props.entry)}</dd>
        <dt class="text-[var(--text-muted)]">{t("diagnostics.colFirstToken")}</dt><dd class="font-mono text-[var(--text-secondary)]">{formatDuration(props.entry.firstTokenMs || 0)}</dd>
        <dt class="text-[var(--text-muted)]">{t("diagnostics.colDuration")}</dt><dd class="font-mono text-[var(--text-secondary)]">{formatDuration(props.entry.durationMs)}</dd>
        <dt class="text-[var(--text-muted)]">{t("diagnostics.colTokens")}</dt>
        <dd class="font-mono text-[var(--text-secondary)]">
          {t("diagnostics.usageLine", { input: props.entry.inputTokens.toLocaleString(), output: props.entry.outputTokens.toLocaleString(), total: totalTokens(props.entry).toLocaleString() })}
          <div class="text-[var(--text-muted)]">{t("diagnostics.cacheLine", { cache: props.entry.cacheReadTokens.toLocaleString(), reasoning: props.entry.reasoningTokens.toLocaleString() })}</div>
        </dd>
        <dt class="text-[var(--text-muted)]">{t("diagnostics.colCost")}</dt>
        <dd class="font-mono text-[var(--text-secondary)]">
          {(() => { const cost = entryCost(props.entry); return cost === null ? t("diagnostics.noError") : formatUsd(cost); })()}
        </dd>
        <dt class="text-[var(--text-muted)]">{t("diagnostics.colClient")}</dt><dd class="text-[var(--text-secondary)]">{t("diagnostics.clientLocal")}</dd>
        <dt class="text-[var(--text-muted)]">{t("diagnostics.session")}</dt><dd class="break-all font-mono text-[var(--text-secondary)]">{props.entry.sessionID}</dd>
        <Show when={props.entry.error}>
          <dt class="text-[var(--text-muted)]">{t("diagnostics.colError")}</dt>
          <dd class="whitespace-pre-wrap break-words font-mono text-[var(--danger-text)]">{props.entry.error}</dd>
        </Show>
      </dl>
      <Show
        when={content()}
        fallback={
          <p class="rounded-lg border border-dashed border-[var(--border-subtle)] px-3 py-2 text-[11px] text-[var(--text-muted)]">
            {contentFailed() ? t("diagnostics.contentUnavailable") : t("diagnostics.contentLoading")}
          </p>
        }
      >
        {(loaded) => (
          <Show when={loaded().found} fallback={<p class="rounded-lg border border-dashed border-[var(--border-subtle)] px-3 py-2 text-[11px] text-[var(--text-muted)]">{t("diagnostics.contentUnavailable")}</p>}>
            <ContentSection title={t("diagnostics.contentInput")} parts={loaded().input} emptyText={t("diagnostics.contentInputEmpty")} />
            <ContentSection title={t("diagnostics.contentOutput")} parts={loaded().output} emptyText={t("diagnostics.contentOutputEmpty")} />
            <Show when={loaded().truncated}><p class="text-[10px] text-[var(--text-faint)]">{t("diagnostics.contentTruncated")}</p></Show>
            <p class="text-[10px] leading-relaxed text-[var(--text-faint)]">{t("diagnostics.contentScopeHint")}</p>
          </Show>
        )}
      </Show>
      <pre class="overflow-auto whitespace-pre-wrap break-all rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 font-mono text-[11px] text-[var(--text-secondary)]">{JSON.stringify(props.entry, null, 2)}</pre>
      <p class="text-[11px] text-[var(--text-faint)]">{t("diagnostics.detailsHint")}</p>
    </section>
    </div>
  );
}

function ContentSection(props: { title: string; parts: RequestLogContentPart[]; emptyText: string }) {
  return (
    <section class="space-y-1.5">
      <h5 class="text-[12px] font-semibold text-[var(--text-secondary)]">{props.title}</h5>
      <Show
        when={props.parts.length > 0}
        fallback={<p class="rounded-lg border border-dashed border-[var(--border-subtle)] px-3 py-2 text-[11px] text-[var(--text-muted)]">{props.emptyText}</p>}
      >
        <div class="space-y-1.5">
          <For each={props.parts}>
            {(part) => (
              <div class="overflow-hidden rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]">
                <Show when={part.kind === "reasoning"}>
                  <div class="border-b border-[var(--border-subtle)] px-2.5 py-1 text-[10px] font-medium text-[var(--text-faint)]">{t("diagnostics.contentReasoning")}</div>
                </Show>
                <pre class="max-h-64 overflow-auto whitespace-pre-wrap break-words px-2.5 py-2 font-mono text-[11px] text-[var(--text-primary)]">{part.text}</pre>
              </div>
            )}
          </For>
        </div>
      </Show>
    </section>
  );
}

function unique(values: string[]): string[] {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))].sort();
}
