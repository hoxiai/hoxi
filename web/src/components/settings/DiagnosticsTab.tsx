import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { Columns3, RefreshCw, X } from "lucide-solid";
import { locale, t } from "../../i18n";
import { formatCompactNumber } from "../../i18n/format";
import {
  loadRequestLog, requestLog, requestLogError, requestLogLoading, requestLogTotal,
} from "../../store/workspace";
import type { RequestLogEntry } from "../../services/api";

const control = "rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer";
const button = "inline-flex items-center justify-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";
const headCell = "px-3 py-2 text-left font-medium";

type ColumnKey = "time" | "provider" | "status" | "model" | "firstToken" | "duration" | "tokens" | "error";

const COLUMNS: ColumnKey[] = ["time", "provider", "status", "model", "firstToken", "duration", "tokens", "error"];

const COLUMN_WIDTH: Record<ColumnKey, string> = {
  time: "w-[86px]",
  provider: "w-[128px]",
  status: "w-[76px]",
  model: "",
  firstToken: "w-[72px]",
  duration: "w-[72px]",
  tokens: "w-[156px]",
  error: "w-[180px]",
};

const COLUMN_LABEL: Record<ColumnKey, () => string> = {
  time: () => t("diagnostics.colTime"),
  provider: () => t("diagnostics.colProvider"),
  status: () => t("diagnostics.colStatus"),
  model: () => t("diagnostics.colModel"),
  firstToken: () => t("diagnostics.colFirstToken"),
  duration: () => t("diagnostics.colDuration"),
  tokens: () => t("diagnostics.colTokens"),
  error: () => t("diagnostics.colError"),
};

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

export default function DiagnosticsTab() {
  const [selected, setSelected] = createSignal<RequestLogEntry | null>(null);
  const [columnsOpen, setColumnsOpen] = createSignal(false);
  const [hidden, setHidden] = createSignal<ColumnKey[]>([]);
  const [provider, setProvider] = createSignal("");
  const [model, setModel] = createSignal("");
  const [level, setLevel] = createSignal("");
  const [pendingOnly, setPendingOnly] = createSignal(false);

  createEffect(() => {
    const timer = setTimeout(() => void loadRequestLog(), 0);
    onCleanup(() => clearTimeout(timer));
  });

  const providers = createMemo(() => unique(requestLog().map((entry) => entry.providerID)));
  const models = createMemo(() => unique(requestLog().map((entry) => entry.modelID)));
  const rows = createMemo(() => requestLog().filter((entry) => {
    if (provider() && entry.providerID !== provider()) return false;
    if (model() && entry.modelID !== model()) return false;
    if (pendingOnly() && entry.level !== "pending") return false;
    if (level() === "error" && !isFailed(entry)) return false;
    if (level() === "info" && isFailed(entry)) return false;
    return true;
  }));
  const visible = (key: ColumnKey) => !hidden().includes(key);

  return (
    <div class="space-y-3 select-text">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex min-w-0 items-baseline gap-2">
          <h3 class="text-[15px] font-semibold text-[var(--text-primary)]">{t("diagnostics.title")}</h3>
          <span class="rounded-full bg-[var(--bg-inset)] px-2 py-0.5 text-[11px] text-[var(--text-muted)]">
            {t("diagnostics.listCount", { count: requestLogTotal() })}
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

      <div class="flex flex-wrap items-center gap-1.5">
        <select aria-label={t("diagnostics.level")} class={control} value={level()} onChange={(e) => setLevel(e.currentTarget.value)}>
          <option value="">{t("diagnostics.allLevels")}</option>
          <option value="ERROR">{t("diagnostics.error")}</option>
          <option value="INFO">{t("diagnostics.info")}</option>
        </select>
        <select aria-label={t("diagnostics.provider")} class={control} value={provider()} onChange={(e) => setProvider(e.currentTarget.value)}>
          <option value="">{t("diagnostics.allProviders")}</option>
          <For each={providers()}>{(id) => <option value={id}>{id}</option>}</For>
        </select>
        <select aria-label={t("diagnostics.colModel")} class={control} value={model()} onChange={(e) => setModel(e.currentTarget.value)}>
          <option value="">{t("diagnostics.allModels")}</option>
          <For each={models()}>{(id) => <option value={id}>{id}</option>}</For>
        </select>
        <button type="button" class={`${control} ${pendingOnly() ? "border-[var(--accent-primary)] text-[var(--accent-primary)]" : ""}`} aria-pressed={pendingOnly()} onClick={() => setPendingOnly((on) => !on)}>
          {t("diagnostics.inProgressOnly")}
        </button>
        <Show when={provider() || model() || pendingOnly() || level()}>
          <button type="button" class="px-2 text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer" onClick={() => { setProvider(""); setModel(""); setPendingOnly(false); setLevel(""); }}>
            {t("diagnostics.clearFilters")}
          </button>
        </Show>
        <button type="button" class={`${button} ml-auto`} disabled={requestLogLoading()} title={t("diagnostics.refresh")} aria-label={t("diagnostics.refresh")} onClick={() => void loadRequestLog()}>
          <RefreshCw size={13} class={requestLogLoading() ? "animate-spin" : ""} />
        </button>
      </div>

      <Show when={requestLogError()}>
        <p role="alert" class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] p-3 text-[12px] text-[var(--danger-text)]">{t("diagnostics.loadFailed", { error: requestLogError() })}</p>
      </Show>
      <Show when={!requestLogLoading() && rows().length === 0}>
        <p role="status" class="rounded-xl border border-dashed border-[var(--border-subtle)] p-6 text-center text-[12px] text-[var(--text-muted)]">{t("diagnostics.emptyFiltered")}</p>
      </Show>

      <Show when={rows().length > 0}>
        <div class="overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
          <table class="w-full table-fixed border-collapse text-[12px]">
            <thead>
              <tr class="border-b border-[var(--border-subtle)] text-[11px] text-[var(--text-muted)]">
                <Show when={visible("time")}><th class={`${headCell} ${COLUMN_WIDTH.time}`}>{t("diagnostics.colTime")}</th></Show>
                <Show when={visible("provider")}><th class={`${headCell} ${COLUMN_WIDTH.provider}`}>{t("diagnostics.colProvider")}</th></Show>
                <Show when={visible("status")}><th class={`${headCell} ${COLUMN_WIDTH.status}`}>{t("diagnostics.colStatus")}</th></Show>
                <Show when={visible("model")}><th class={`${headCell} ${COLUMN_WIDTH.model}`}>{t("diagnostics.colModel")}</th></Show>
                <Show when={visible("firstToken")}><th class={`${headCell} ${COLUMN_WIDTH.firstToken} text-right`}>{t("diagnostics.colFirstToken")}</th></Show>
                <Show when={visible("duration")}><th class={`${headCell} ${COLUMN_WIDTH.duration} text-right`}>{t("diagnostics.colDuration")}</th></Show>
                <Show when={visible("tokens")}><th class={`${headCell} ${COLUMN_WIDTH.tokens}`}>{t("diagnostics.colTokens")}</th></Show>
                <Show when={visible("error")}><th class={`${headCell} ${COLUMN_WIDTH.error}`}>{t("diagnostics.colError")}</th></Show>
                <th class="sticky right-0 bg-[var(--bg-canvas)] px-3 py-2 text-right font-medium shadow-[-8px_0_8px_-8px_var(--border-strong)]">{t("diagnostics.colDetails")}</th>
              </tr>
            </thead>
            <tbody>
              <For each={rows()}>
                {(entry) => {
                  const when = formatWhen(entry.time);
                  const failed = isFailed(entry);
                  return (
                    <tr class={`border-b border-[var(--border-subtle)]/70 last:border-0 ${failed ? "bg-[var(--danger-surface)]/35" : ""}`}>
                      <Show when={visible("time")}>
                        <td class="whitespace-nowrap px-3 py-2 font-mono text-[11px] leading-4 text-[var(--text-secondary)]">
                          <div>{when.date}</div>
                          <div class="text-[var(--text-muted)]">{when.time}</div>
                        </td>
                      </Show>
                      <Show when={visible("provider")}>
                        <td class="px-3 py-2">
                          <div class="truncate text-[var(--text-primary)]" title={entry.providerID}>{entry.providerID}</div>
                          <Show when={entry.account}><div class="truncate text-[11px] text-[var(--text-muted)]" title={entry.account}>{entry.account}</div></Show>
                        </td>
                      </Show>
                      <Show when={visible("status")}>
                        <td class="px-3 py-2">
                          <span class={`inline-block rounded-full px-2 py-0.5 text-[11px] font-medium ${badgeClass(entry)}`} title={statusTitle(entry)}>{badgeText(entry)}</span>
                        </td>
                      </Show>
                      <Show when={visible("model")}>
                        <td class="truncate px-3 py-2 font-mono text-[11px] text-[var(--text-secondary)]" title={entry.modelID}>{entry.modelID}</td>
                      </Show>
                      <Show when={visible("firstToken")}>
                        <td class="px-3 py-2 text-right font-mono text-[11px] text-[var(--text-secondary)]">{formatDuration(entry.firstTokenMs || 0)}</td>
                      </Show>
                      <Show when={visible("duration")}>
                        <td class="px-3 py-2 text-right font-mono text-[11px] text-[var(--text-secondary)]">{formatDuration(entry.durationMs)}</td>
                      </Show>
                      <Show when={visible("tokens")}>
                        <td class="px-3 py-2 font-mono text-[11px] leading-4 text-[var(--text-secondary)]">
                          <div class="text-[var(--text-primary)]" title={totalTokens(entry).toLocaleString()}>{formatCompactNumber(totalTokens(entry))}</div>
                          <div class="truncate text-[11px] text-[var(--text-muted)]" title={t("diagnostics.usageLine", { input: entry.inputTokens.toLocaleString(), output: entry.outputTokens.toLocaleString(), total: totalTokens(entry).toLocaleString() })}>
                            {t("diagnostics.tokensInOut", { input: formatCompactNumber(entry.inputTokens), output: formatCompactNumber(entry.outputTokens) })}
                          </div>
                        </td>
                      </Show>
                      <Show when={visible("error")}>
                        <td class="truncate px-3 py-2 text-[11px] text-[var(--danger-text)]" title={entry.error || ""}>{entry.error || t("diagnostics.noError")}</td>
                      </Show>
                      <td class={`sticky right-0 px-3 py-2 text-right shadow-[-8px_0_8px_-8px_var(--border-strong)] ${failed ? "bg-[var(--danger-surface)]" : "bg-[var(--bg-canvas)]"}`}>
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
        <dt class="text-[var(--text-muted)]">{t("diagnostics.colClient")}</dt><dd class="text-[var(--text-secondary)]">{t("diagnostics.clientLocal")}</dd>
        <dt class="text-[var(--text-muted)]">{t("diagnostics.session")}</dt><dd class="break-all font-mono text-[var(--text-secondary)]">{props.entry.sessionID}</dd>
        <Show when={props.entry.error}>
          <dt class="text-[var(--text-muted)]">{t("diagnostics.colError")}</dt>
          <dd class="whitespace-pre-wrap break-words font-mono text-[var(--danger-text)]">{props.entry.error}</dd>
        </Show>
      </dl>
      <pre class="overflow-auto whitespace-pre-wrap break-all rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-3 font-mono text-[11px] text-[var(--text-secondary)]">{JSON.stringify(props.entry, null, 2)}</pre>
      <p class="text-[11px] text-[var(--text-faint)]">{t("diagnostics.detailsHint")}</p>
    </section>
    </div>
  );
}

function unique(values: string[]): string[] {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))].sort();
}
