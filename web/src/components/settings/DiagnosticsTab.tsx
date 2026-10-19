import { createEffect, createSignal, For, onCleanup, Show } from "solid-js";
import { Download, FolderOpen, RefreshCw } from "lucide-solid";
import { locale, t } from "../../i18n";
import {
  diagnostics, exportDiagnostics, openDiagnosticsDirectory, refreshDiagnostics,
  resetDiagnosticsFilters, setDiagnosticsFilter,
} from "../../store/workspace";
import type { DiagnosticKind, DiagnosticLevel } from "../../services/diagnostics";
import { SettingGroup } from "./SettingsModal";

const control = "w-full min-w-0 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2 text-[12px] text-[var(--text-primary)]";
const button = "inline-flex items-center justify-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2 text-[12px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";

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
      <SettingGroup>
        <div class="space-y-3 p-4">
          <div class="grid grid-cols-2 gap-3">
            <label class="space-y-1 text-[12px] text-[var(--text-secondary)]">
              <span>{t("diagnostics.kind")}</span>
              <select class={control} value={diagnostics.filters.kind} onChange={(e) => setDiagnosticsFilter("kind", e.currentTarget.value as DiagnosticKind | "")}>
                <option value="">{t("diagnostics.allKinds")}</option>
                <option value="system">{t("diagnostics.system")}</option>
                <option value="request">{t("diagnostics.request")}</option>
              </select>
            </label>
            <label class="space-y-1 text-[12px] text-[var(--text-secondary)]">
              <span>{t("diagnostics.level")}</span>
              <select class={control} value={diagnostics.filters.level} onChange={(e) => setDiagnosticsFilter("level", e.currentTarget.value as DiagnosticLevel | "")}>
                <option value="">{t("diagnostics.allLevels")}</option>
                <option value="ERROR">{t("diagnostics.error")}</option>
                <option value="WARN">{t("diagnostics.warn")}</option>
                <option value="INFO">{t("diagnostics.info")}</option>
              </select>
            </label>
          </div>
          <label class="block space-y-1 text-[12px] text-[var(--text-secondary)]">
            <span>{t("diagnostics.search")}</span>
            <input type="search" class={control} placeholder={t("diagnostics.searchPlaceholder")} value={diagnostics.filters.q} onInput={(e) => setDiagnosticsFilter("q", e.currentTarget.value)} />
          </label>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <label class="space-y-1 text-[12px] text-[var(--text-secondary)]">
              <span>{t("diagnostics.session")}</span>
              <input class={control} value={diagnostics.filters.sessionID} onInput={(e) => setDiagnosticsFilter("sessionID", e.currentTarget.value)} />
            </label>
            <label class="space-y-1 text-[12px] text-[var(--text-secondary)]">
              <span>{t("diagnostics.provider")}</span>
              <input class={control} value={diagnostics.filters.providerID} onInput={(e) => setDiagnosticsFilter("providerID", e.currentTarget.value)} />
            </label>
          </div>
          <div class="flex flex-wrap justify-end gap-2">
            <button type="button" class={button} onClick={resetDiagnosticsFilters}>{t("diagnostics.reset")}</button>
            <button type="button" class={button} disabled={diagnostics.loading} onClick={() => void refreshDiagnostics()}>
              <RefreshCw size={13} class={diagnostics.loading ? "animate-spin" : ""} />{t("diagnostics.refresh")}
            </button>
          </div>
        </div>
      </SettingGroup>

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
            <article class="min-w-0 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/35 p-3 space-y-2 text-[11px]">
              <div class="flex flex-wrap items-center gap-2 text-[var(--text-muted)]">
                <span class={`rounded px-1.5 py-0.5 font-semibold ${entry.level === "ERROR" ? "bg-[var(--danger-surface)] text-[var(--danger-text)]" : entry.level === "WARN" ? "bg-[var(--warn-surface)] text-[var(--warn-text)]" : "bg-[var(--accent-surface)] text-[var(--accent-text)]"}`}>
                  {entry.level === "ERROR" ? t("diagnostics.error") : entry.level === "WARN" ? t("diagnostics.warn") : t("diagnostics.info")}
                </span>
                <span>{entry.kind === "system" ? t("diagnostics.system") : t("diagnostics.request")}</span>
                <time dateTime={entry.time}>{new Date(entry.time).toLocaleString(locale())}</time>
                <span class="font-mono break-all">{entry.source}</span>
              </div>
              <p class="whitespace-pre-wrap break-words font-mono text-[12px] text-[var(--text-primary)]">{entry.message}</p>
              <div class="flex flex-wrap gap-x-3 gap-y-1 font-mono text-[var(--text-muted)] break-all">
                <Show when={entry.sessionID}><span>{t("diagnostics.session")}: {entry.sessionID}</span></Show>
                <Show when={entry.providerID}><span>{t("diagnostics.provider")}: {entry.providerID}</span></Show>
                <Show when={entry.modelID}><span>{t("diagnostics.model", { model: entry.modelID! })}</span></Show>
                <Show when={entry.status !== undefined}><span>{t("diagnostics.status", { status: entry.status! })}</span></Show>
                <Show when={entry.exitCode !== undefined}><span>{t("diagnostics.exitCode", { code: entry.exitCode! })}</span></Show>
                <Show when={entry.durationMs !== undefined}><span>{t("diagnostics.duration", { duration: entry.durationMs! })}</span></Show>
              </div>
            </article>
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
