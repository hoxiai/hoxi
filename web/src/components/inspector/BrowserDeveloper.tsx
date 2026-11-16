import { For, Show } from "solid-js";
import { CircleHelp } from "lucide-solid";
import type { BrowserPreviewScale, BrowserViewportMode, createBrowserDeveloper } from "../../store/workspace";
import { t } from "../../i18n";

export default function BrowserDeveloper(props: {
  developer: ReturnType<typeof createBrowserDeveloper>;
  chrome: boolean;
  window: boolean;
  resetZoom: () => void;
}) {
  const button = "rounded border border-[var(--border-subtle)] px-2 py-0.5 text-[11px] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer";
  return <div class="shrink-0 border-b border-[var(--border-subtle)]">
    <button class="w-full px-2 py-1 text-left text-[11px] hover:bg-[var(--bg-hover)] cursor-pointer" aria-expanded={props.developer.open()} onClick={() => props.developer.setOpen(value => !value)}>
      {t("inspector.browser.developerTools")} {props.developer.open() ? "▾" : "▸"}
    </button>
    <Show when={props.developer.open()}><div class="space-y-2 px-2 pb-2 text-[11px]">
      <div class="flex flex-wrap items-center gap-2">
        <label title={props.chrome ? t("inspector.browser.chromeViewportHint") : props.window ? t("inspector.browser.windowViewportHint") : undefined}>{t("inspector.browser.viewportPreset")} <select value={props.chrome || props.developer.viewportMode() !== "actual" ? props.developer.viewportMode() : "fit"} disabled={props.window}
          class="rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-1 py-0.5 disabled:opacity-50" onChange={event => props.developer.setViewportMode(event.currentTarget.value as BrowserViewportMode)}>
          <option value="fit">{t("inspector.browser.viewportFit")}</option>
          <option value="page">{t("inspector.browser.viewportPage")}</option>
          <option value="desktop">{t("inspector.browser.viewportDesktop")}</option>
          <option value="mobile">{t("inspector.browser.viewportMobile")}</option>
          <Show when={props.chrome}><option value="actual">{t("inspector.browser.viewportActual")}</option></Show>
        </select></label>
        <label>{t("inspector.browser.previewScale")} <select value={props.developer.previewScale()}
          class="rounded border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-1 py-0.5" onChange={event => props.developer.setPreviewScale(event.currentTarget.value as BrowserPreviewScale)}>
          <option value="fit">{t("inspector.browser.viewportFit")}</option><option value="0.5">50%</option><option value="1">100%</option><option value="1.5">150%</option>
        </select></label>
        <button class={button} onClick={props.resetZoom}>{t("inspector.browser.resetZoom")}</button>
        <button class={button} disabled={props.developer.capturing()} onClick={() => void props.developer.attachScreenshot()}>{t(props.developer.capturing() ? "inspector.browser.screenshotAttaching" : "inspector.browser.attachScreenshot")}</button>
      </div>
      <Show when={props.developer.notice()}><p role="status">{props.developer.notice()}</p></Show>
      <div class="flex items-center justify-between">
        <span class="inline-flex items-center gap-1" title={t("inspector.browser.diagnosticsHint")}>
          {t("inspector.browser.diagnostics")}
          <CircleHelp size={11} class="text-[var(--text-muted)]" />
        </span>
        <button class={button} onClick={() => void props.developer.clearDiagnostics()}>{t("inspector.browser.clearDiagnostics")}</button>
      </div>
      <Show when={props.developer.error()}><p role="alert" class="break-words text-red-500">{props.developer.error()}</p></Show>
      <div class="max-h-40 space-y-1 overflow-y-auto selectable-text" aria-label={t("inspector.browser.diagnostics")}>
        <Show when={props.developer.diagnostics().length} fallback={<p class="text-[var(--text-muted)]">{t("inspector.browser.diagnosticsEmpty")}</p>}>
          <For each={props.developer.diagnostics()}>{item => <div class="rounded bg-[var(--bg-inset)] px-2 py-1 break-words">
            <span class="text-[var(--text-muted)]">{item.kind === "console" ? t("inspector.browser.consoleError") : t("inspector.browser.failedRequest")} · </span>{item.message}
            <Show when={item.url}><p class="text-[var(--text-muted)] break-all">{item.url}</p></Show>
          </div>}</For>
        </Show>
      </div>
    </div></Show>
  </div>;
}
