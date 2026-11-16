import { createEffect, For, onCleanup, Show } from "solid-js";
import { ChevronRight, RefreshCw } from "lucide-solid";
import { locale, t } from "../../i18n";
import { diagnostics, refreshDiagnostics, setDiagnosticsFilter } from "../../store/workspace";
import type { DiagnosticLevel } from "../../services/diagnostics";

const control = "rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer";
const button = "inline-flex items-center justify-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";

export default function ErrorLogTab() {
  createEffect(() => {
    JSON.stringify(diagnostics.filters);
    const timer = setTimeout(() => void refreshDiagnostics(), 250);
    onCleanup(() => clearTimeout(timer));
  });

  const rows = () => diagnostics.entries.filter((entry) => entry.level === "ERROR" || entry.level === "WARN");

  return (
    <div class="space-y-3 select-text">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="min-w-0 space-y-1">
          <h3 class="text-[15px] font-semibold text-[var(--text-primary)]">{t("diagnostics.errorLogTitle")}</h3>
          <p class="text-[12px] text-[var(--text-muted)]">{t("diagnostics.errorLogSubtitle")}</p>
        </div>
        <button type="button" class={button} disabled={diagnostics.loading} aria-label={t("diagnostics.refresh")} onClick={() => void refreshDiagnostics()}>
          <RefreshCw size={13} class={diagnostics.loading ? "animate-spin" : ""} />
        </button>
      </div>

      <div class="flex flex-wrap items-center gap-1.5">
        <select aria-label={t("diagnostics.level")} class={control} value={diagnostics.filters.level} onChange={(e) => setDiagnosticsFilter("level", e.currentTarget.value as DiagnosticLevel | "")}>
          <option value="">{t("diagnostics.allLevels")}</option>
          <option value="ERROR">{t("diagnostics.error")}</option>
          <option value="WARN">{t("diagnostics.warn")}</option>
        </select>
        <input type="search" aria-label={t("diagnostics.search")} class={`${control} min-w-40 flex-1`} placeholder={t("diagnostics.searchPlaceholder")} value={diagnostics.filters.q} onInput={(e) => setDiagnosticsFilter("q", e.currentTarget.value)} />
      </div>

      <Show when={diagnostics.error}>
        <p role="alert" class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] p-3 text-[12px] text-[var(--danger-text)]">{t("diagnostics.loadFailed", { error: diagnostics.error })}</p>
      </Show>
      <Show when={diagnostics.loaded && !diagnostics.loading && rows().length === 0}>
        <p role="status" class="rounded-xl border border-dashed border-[var(--border-subtle)] p-6 text-center text-[12px] text-[var(--text-muted)]">{t("diagnostics.errorLogEmpty")}</p>
      </Show>

      <div class="space-y-2">
        <For each={rows()}>
          {(entry) => (
            <details class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/35 text-[12px]">
              <summary class="flex cursor-pointer list-none flex-wrap items-center gap-2 p-3 [&::-webkit-details-marker]:hidden">
                <span class={`rounded px-1.5 py-0.5 text-[11px] font-semibold ${entry.level === "ERROR" ? "bg-[var(--danger-surface)] text-[var(--danger-text)]" : "bg-[var(--warn-surface)] text-[var(--warn-text)]"}`}>
                  {entry.level === "ERROR" ? t("diagnostics.error") : t("diagnostics.warn")}
                </span>
                <time class="font-mono text-[11px] text-[var(--text-muted)]" dateTime={entry.time}>{new Date(entry.time).toLocaleString(locale())}</time>
                <span class="font-mono text-[11px] text-[var(--text-muted)]">{entry.source}</span>
                <span class="min-w-0 flex-1 truncate text-[var(--text-primary)]">{entry.message}</span>
                <ChevronRight size={13} class="text-[var(--text-faint)]" />
              </summary>
              <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 border-t border-[var(--border-subtle)] p-3 text-[12px]">
                <dt class="text-[var(--text-muted)]">{t("diagnostics.message")}</dt>
                <dd class="whitespace-pre-wrap break-words font-mono text-[var(--text-primary)]">{entry.message}</dd>
                <Show when={entry.providerID}><dt class="text-[var(--text-muted)]">{t("diagnostics.provider")}</dt><dd class="text-[var(--text-secondary)]">{entry.providerID}</dd></Show>
                <Show when={entry.modelID}><dt class="text-[var(--text-muted)]">{t("diagnostics.colModel")}</dt><dd class="font-mono text-[var(--text-secondary)]">{entry.modelID}</dd></Show>
                <Show when={entry.sessionID}><dt class="text-[var(--text-muted)]">{t("diagnostics.session")}</dt><dd class="break-all font-mono text-[var(--text-secondary)]">{entry.sessionID}</dd></Show>
                <Show when={entry.status !== undefined}><dt class="text-[var(--text-muted)]">{t("diagnostics.colStatus")}</dt><dd class="font-mono text-[var(--text-secondary)]">{entry.status}</dd></Show>
              </dl>
            </details>
          )}
        </For>
      </div>
    </div>
  );
}
