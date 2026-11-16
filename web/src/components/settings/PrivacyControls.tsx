import { createSignal, onMount, Show } from "solid-js";
import { locale, t } from "../../i18n";
import {
  clearLocalPromptHistory,
  loadTelemetrySettings,
  openExternalURL,
  resolveOfficialWebURL,
  saveOfflineMode,
  saveTelemetryEnabled,
  telemetry,
} from "../../store/workspace";

const buttonClass = "rounded-lg border border-[var(--border-strong)] px-4 py-2 text-[13px] font-medium hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";
const linkClass = "text-[var(--accent-primary)] underline underline-offset-2";

function legalURL(document: "privacy" | "terms") {
  return resolveOfficialWebURL(`/legal/${document}-${locale() === "zh-CN" ? "zh" : "en"}.html`);
}

export function PrivacySettingsTab() {
  const [historyResult, setHistoryResult] = createSignal("");
  onMount(() => {
    void loadTelemetrySettings();
  });
  return (
    <div class="space-y-6 text-[var(--text-primary)]">
      <h3 class="text-[15px] font-semibold">{t("privacy.title")}</h3>
      <div class="space-y-3 text-[13px] leading-relaxed text-[var(--text-secondary)]">
        <p>{t("privacy.openSource")}</p>
        <p>{t("privacy.providers")}</p>
        <p>{t("privacy.connections")}</p>
      </div>
      <section class="space-y-3 border-t border-[var(--border-subtle)] pt-5">
        <div class="flex items-start justify-between gap-4">
          <div class="space-y-1">
            <h4 class="font-semibold">{t("privacy.offlineTitle")}</h4>
            <p class="text-[13px] text-[var(--text-secondary)]">{t("privacy.offlineDesc")}</p>
          </div>
          <button
            role="switch"
            aria-checked={telemetry.offline}
            disabled={telemetry.offlineEnvOverride || telemetry.saving}
            onClick={() => void saveOfflineMode(!telemetry.offline)}
            class={`${buttonClass} shrink-0 min-w-[72px]`}
          >
            {telemetry.offline ? t("privacy.telemetryOn") : t("privacy.telemetryOff")}
          </button>
        </div>
        <Show when={telemetry.offlineEnvOverride}>
          <p class="text-[13px] text-[var(--text-secondary)]">{t("privacy.offlineEnvLocked")}</p>
        </Show>
      </section>
      <section class="space-y-3 border-t border-[var(--border-subtle)] pt-5">
        <div class="flex items-start justify-between gap-4">
          <div class="space-y-1">
            <h4 class="font-semibold">{t("privacy.telemetryTitle")}</h4>
            <p class="text-[13px] text-[var(--text-secondary)]">{t("privacy.telemetryDesc")}</p>
          </div>
          <button
            role="switch"
            aria-checked={telemetry.enabled && !telemetry.offline}
            disabled={telemetry.envOverride || telemetry.offline || telemetry.saving}
            onClick={() => void saveTelemetryEnabled(!telemetry.enabled)}
            class={`${buttonClass} shrink-0 min-w-[72px]`}
          >
            {telemetry.enabled && !telemetry.offline ? t("privacy.telemetryOn") : t("privacy.telemetryOff")}
          </button>
        </div>
        <Show when={telemetry.offline && !telemetry.envOverride}>
          <p class="text-[13px] text-[var(--text-secondary)]">{t("privacy.telemetrySuppressedByOffline")}</p>
        </Show>
        <Show when={telemetry.envOverride}>
          <p class="text-[13px] text-[var(--text-secondary)]">{t("privacy.telemetryEnvLocked")}</p>
        </Show>
        <Show when={telemetry.error}>
          <p role="alert" class="text-[13px] text-red-500">{telemetry.error}</p>
        </Show>
      </section>
      <div class="flex flex-wrap gap-x-4 gap-y-2 text-[13px]">
        <a
          class={linkClass}
          href={legalURL("privacy")}
          target="_blank"
          rel="noopener noreferrer"
          onClick={(e) => {
            e.preventDefault();
            openExternalURL(legalURL("privacy"));
          }}
        >
          {t("privacy.policyLink")}
        </a>
        <a
          class={linkClass}
          href={legalURL("terms")}
          target="_blank"
          rel="noopener noreferrer"
          onClick={(e) => {
            e.preventDefault();
            openExternalURL(legalURL("terms"));
          }}
        >
          {t("privacy.termsLink")}
        </a>
      </div>
      <section class="space-y-3 border-t border-[var(--border-subtle)] pt-5">
        <h4 class="font-semibold">{t("privacy.localData")}</h4>
        <p class="text-[13px] text-[var(--text-secondary)]">{t("privacy.historyScope")}</p>
        <button class={buttonClass} onClick={() => setHistoryResult(clearLocalPromptHistory() ? t("privacy.historyCleared") : t("privacy.historyError"))}>{t("privacy.clearHistory")}</button>
        <p class="text-[13px]" role="status">{historyResult()}</p>
      </section>
    </div>
  );
}
