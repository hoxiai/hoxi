import { createEffect, createSignal, For, onCleanup, Show } from "solid-js";
import { ChevronRight, Download, FolderOpen, RefreshCw } from "lucide-solid";
import { locale, t } from "../../i18n";
import {
  diagnostics, exportDiagnostics, openDiagnosticsDirectory, refreshDiagnostics,
  resetDiagnosticsFilters, setDiagnosticsFilter,
} from "../../store/workspace";
import type { DiagnosticKind, DiagnosticLevel } from "../../services/diagnostics";
import { SettingGroup } from "./setting-controls";

const control = "w-full min-w-0 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1.5 text-[12px] text-[var(--text-primary)]";
const button = "inline-flex items-center justify-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1.5 text-[12px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";

export default function DiagnosticsTab() {
  const [confirmExport, setConfirmExport] = createSignal(false);
  createEffect(() => {
    // Track a snapshot here so requests are debounced, not the input state.
    JSON.stringify(diagnostics.filters);
    const timer = setTimeout(() => void refreshDiagnostics(), 250);
    onCleanup(() => clearTimeout(timer));
  });

  async function download() {
    setConfirmExport(false);
    const blob = await exportDiagnostics();
    if (!blob) return;
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `hoxi-diagnostics-${new Date().toISOString().slice(0, 10)}.json`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
  }

  return (
    <div class="space-y-5 select-text">
      <div class="space-y-1">
        <h3 class="text-[15px] font-semibold text-[var(--text-primary)]">{t("diagnostics.title")}</h3>
        <p class="text-[12px] leading-relaxed text-[var(--text-muted)]">{t("diagnostics.subtitle")}</p>
      </div>
      <div class="space-y-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/35 p-2.5">
          <div class="flex flex-wrap items-center gap-2">
              <select aria-label={t("diagnostics.kind")} class={`${control} !w-auto max-w-full flex-1 sm:flex-none`} value={diagnostics.filters.kind} onChange={(e) => setDiagnosticsFilter("kind", e.currentTarget.value as DiagnosticKind | "")}>
                <option value="">{t("diagnostics.allKinds")}</option>
                <option value="system">{t("diagnostics.system")}</option>
                <option value="request">{t("diagnostics.request")}</option>
              </select>
              <select aria-label={t("diagnostics.level")} class={`${control} !w-auto max-w-full flex-1 sm:flex-none`} value={diagnostics.filters.level} onChange={(e) => setDiagnosticsFilter("level", e.currentTarget.value as DiagnosticLevel | "")}>
                <option value="">{t("diagnostics.allLevels")}</option>
                <option value="ERROR">{t("diagnostics.error")}</option>
                <option value="WARN">{t("diagnostics.warn")}</option>
                <option value="INFO">{t("diagnostics.info")}</option>
              </select>
            <input type="search" aria-label={t("diagnostics.search")} class={`${control} basis-full sm:basis-32 flex-1`} placeholder={t("diagnostics.searchPlaceholder")} value={diagnostics.filters.q} onInput={(e) => setDiagnosticsFilter("q", e.currentTarget.value)} />
            <button type="button" class={button} disabled={diagnostics.loading} title={t("diagnostics.refresh")} aria-label={t("diagnostics.refresh")} onClick={() => void refreshDiagnostics()}>
              <RefreshCw size={14} class={diagnostics.loading ? "animate-spin" : ""} />
            </button>
          </div>
          <div class="flex items-start gap-2">
          <details class="group/filters min-w-0 flex-1" open={!!(diagnostics.filters.sessionID || diagnostics.filters.providerID)}>
            <summary class="flex w-fit cursor-pointer list-none items-center gap-1 py-1 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] [&::-webkit-details-marker]:hidden">
              <ChevronRight size={12} class="transition-transform group-open/filters:rotate-90" />
              {t("diagnostics.advancedFilters")}
            </summary>
          <div class="mt-2 grid grid-cols-1 sm:grid-cols-2 gap-2">
            <label class="space-y-1 text-[12px] text-[var(--text-secondary)]">
              <span>{t("diagnostics.session")}</span>
              <input class={control} value={diagnostics.filters.sessionID} onInput={(e) => setDiagnosticsFilter("sessionID", e.currentTarget.value)} />
            </label>
            <label class="space-y-1 text-[12px] text-[var(--text-secondary)]">
              <span>{t("diagnostics.provider")}</span>
              <input class={control} value={diagnostics.filters.providerID} onInput={(e) => setDiagnosticsFilter("providerID", e.currentTarget.value)} />
            </label>
          </div>
          </details>
            <button type="button" class="shrink-0 py-1 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer" onClick={resetDiagnosticsFilters}>{t("diagnostics.reset")}</button>
          </div>
      </div>

      <Show when={diagnostics.error}>
        <p role="alert" class="rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] p-3 text-[12px] text-[var(--danger-text)] break-words">{t("diagnostics.loadFailed", { error: diagnostics.error })}</p>
      </Show>
      <div aria-busy={diagnostics.loading} class="space-y-2">
        <Show when={diagnostics.loading}><p role="status" class="text-[12px] text-[var(--text-muted)]">{t("diagnostics.loading")}</p></Show>
        <Show when={diagnostics.loaded && !diagnostics.loading && !diagnostics.error && diagnostics.entries.length === 0}>
          <p role="status" class="rounded-xl border border-dashed border-[var(--border-subtle)] p-6 text-center text-[12px] text-[var(--text-muted)]">{t("diagnostics.empty")}</p>
        </Show>
        <For each={diagnostics.entries}>
          {(entry) => (
            <details class="group/entry min-w-0 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/35 text-[11px]">
              <summary class="cursor-pointer list-none p-3 space-y-2 hover:bg-[var(--bg-hover)] rounded-xl [&::-webkit-details-marker]:hidden">
              <div class="flex flex-wrap items-center gap-2 text-[var(--text-muted)]">
                <span class={`rounded px-1.5 py-0.5 font-semibold ${entry.level === "ERROR" ? "bg-[var(--danger-surface)] text-[var(--danger-text)]" : entry.level === "WARN" ? "bg-[var(--warn-surface)] text-[var(--warn-text)]" : "bg-[var(--accent-surface)] text-[var(--accent-text)]"}`}>
                  {entry.level === "ERROR" ? t("diagnostics.error") : entry.level === "WARN" ? t("diagnostics.warn") : t("diagnostics.info")}
                </span>
                <span>{entry.kind === "system" ? t("diagnostics.system") : t("diagnostics.request")}</span>
                <time dateTime={entry.time}>{new Date(entry.time).toLocaleString(locale())}</time>
                <span class="font-mono break-all">{entry.source}</span>
                <span class="ml-auto inline-flex items-center gap-1 text-[var(--text-faint)]">
                  {t("diagnostics.details")}<ChevronRight size={13} class="transition-transform group-open/entry:rotate-90" />
                </span>
              </div>
              <p class="line-clamp-2 break-words font-mono text-[12px] text-[var(--text-primary)]">{entry.message}</p>
              </summary>
              <div class="space-y-3 border-t border-[var(--border-subtle)] p-3">
              <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-[var(--text-muted)]">
                <dt>{t("diagnostics.recordID")}</dt><dd class="break-all font-mono text-[var(--text-secondary)]">{entry.id}</dd>
                <dt>{t("diagnostics.timestamp")}</dt><dd class="break-all font-mono text-[var(--text-secondary)]">{entry.time}</dd>
                <dt>{t("diagnostics.message")}</dt><dd class="whitespace-pre-wrap break-words font-mono text-[var(--text-primary)]">{entry.message}</dd>
              </dl>
              <div class="flex flex-wrap gap-x-3 gap-y-1 font-mono text-[var(--text-muted)] break-all">
                <Show when={entry.sessionID}><span>{t("diagnostics.session")}: {entry.sessionID}</span></Show>
                <Show when={entry.providerID}><span>{t("diagnostics.provider")}: {entry.providerID}</span></Show>
                <Show when={entry.modelID}><span>{t("diagnostics.model", { model: entry.modelID! })}</span></Show>
                <Show when={entry.status !== undefined}><span>{t("diagnostics.status", { status: entry.status! })}</span></Show>
                <Show when={entry.exitCode !== undefined}><span>{t("diagnostics.exitCode", { code: entry.exitCode! })}</span></Show>
                <Show when={entry.durationMs !== undefined}><span>{t("diagnostics.duration", { duration: entry.durationMs! })}</span></Show>
              </div>
              <div class="space-y-1.5">
                <h4 class="font-medium text-[var(--text-secondary)]">{t("diagnostics.rawRecord")}</h4>
                <pre class="max-h-72 overflow-auto rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-3 font-mono text-[11px] whitespace-pre-wrap break-all text-[var(--text-secondary)]">{JSON.stringify(entry, null, 2)}</pre>
                <p class="text-[var(--text-faint)]">{t("diagnostics.detailsHint")}</p>
              </div>
              </div>
            </details>
          )}
        </For>
      </div>

      <SettingGroup title={t("diagnostics.directory")}>
        <div class="p-4 space-y-3">
          <p class="break-all font-mono text-[12px] text-[var(--text-secondary)]">{diagnostics.directory || t("diagnostics.directoryUnknown")}</p>
          <p class="text-[12px] leading-relaxed text-[var(--text-muted)]">{t("diagnostics.directoryHint")}</p>
          <p class="text-[12px] leading-relaxed text-[var(--text-muted)]">{t("diagnostics.privacy")}</p>
          <div class="flex flex-wrap gap-2">
            <button type="button" class={button} disabled={diagnostics.opening} onClick={() => void openDiagnosticsDirectory()}><FolderOpen size={13} />{t("diagnostics.openDirectory")}</button>
            <button type="button" class={button} disabled={diagnostics.exporting} onClick={() => setConfirmExport(true)}><Download size={13} />{t("diagnostics.export")}</button>
          </div>
          <Show when={confirmExport()}>
            <section aria-labelledby="diagnostics-export-title" class="rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)] p-3 space-y-2">
              <h4 id="diagnostics-export-title" class="text-[13px] font-semibold text-[var(--text-primary)]">{t("diagnostics.exportTitle")}</h4>
              <p class="text-[12px] leading-relaxed text-[var(--text-secondary)]">{t("diagnostics.exportNotice")}</p>
              <div class="flex flex-wrap gap-2">
                <button type="button" class={button} disabled={diagnostics.exporting} onClick={() => void download()}>{t("diagnostics.confirmExport")}</button>
                <button type="button" class={button} onClick={() => setConfirmExport(false)}>{t("diagnostics.cancel")}</button>
              </div>
            </section>
          </Show>
          <Show when={diagnostics.actionError}><p role="alert" class="text-[12px] text-[var(--danger-text)] break-words">{t("diagnostics.actionFailed", { error: diagnostics.actionError })}</p></Show>
        </div>
      </SettingGroup>
    </div>
  );
}
