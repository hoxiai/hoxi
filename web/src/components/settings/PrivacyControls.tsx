import { createSignal, onMount, Show } from "solid-js";
import { ExternalLink, Globe, History, Shield, Trash2, WifiOff } from "lucide-solid";
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
import { HelpTip, SettingGroup, SettingRow, ToggleSwitch } from "./setting-controls";

function legalURL(document: "privacy" | "terms") {
  return resolveOfficialWebURL(`/legal/${document}-${locale() === "zh-CN" ? "zh" : "en"}.html`);
}

export function PrivacySettingsTab() {
  const [historyResult, setHistoryResult] = createSignal("");
  onMount(() => {
    void loadTelemetrySettings();
  });

  const overviewTip = () =>
    `${t("privacy.openSource")}\n\n${t("privacy.providers")}\n\n${t("privacy.connections")}`;

  return (
    <div class="space-y-6 text-[var(--text-primary)]">
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-4 py-3">
        <div class="flex items-center gap-3 min-w-0">
          <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
            <Globe size={16} />
          </div>
          <div class="flex items-center gap-2 min-w-0">
            <h3 class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
              {t("privacy.title")}
            </h3>
            <HelpTip text={overviewTip()} />
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-2 shrink-0 text-[12px]">
          <a
            class="inline-flex items-center gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1.5 font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors"
            href={legalURL("privacy")}
            target="_blank"
            rel="noopener noreferrer"
            onClick={(e) => {
              e.preventDefault();
              openExternalURL(legalURL("privacy"));
            }}
          >
            <span>{t("privacy.policyLink")}</span>
            <ExternalLink size={12} />
          </a>
          <a
            class="inline-flex items-center gap-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-1.5 font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors"
            href={legalURL("terms")}
            target="_blank"
            rel="noopener noreferrer"
            onClick={(e) => {
              e.preventDefault();
              openExternalURL(legalURL("terms"));
            }}
          >
            <span>{t("privacy.termsLink")}</span>
            <ExternalLink size={12} />
          </a>
        </div>
      </div>

      <SettingGroup>
        <SettingRow
          icon={WifiOff}
          label={t("privacy.offlineTitle")}
          tip={t("privacy.offlineDesc")}
        >
          <Show when={telemetry.offlineEnvOverride}>
            <span class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] text-[var(--text-muted)]">
              {t("privacy.offlineEnvLocked")}
            </span>
          </Show>
          <ToggleSwitch
            checked={telemetry.offline}
            disabled={telemetry.offlineEnvOverride || telemetry.saving}
            onChange={(next) => void saveOfflineMode(next)}
          />
        </SettingRow>

        <SettingRow
          icon={Shield}
          label={t("privacy.telemetryTitle")}
          tip={t("privacy.telemetryDesc")}
        >
          <Show when={telemetry.offline && !telemetry.envOverride}>
            <span class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] text-[var(--text-muted)]">
              {t("privacy.telemetrySuppressedByOffline")}
            </span>
          </Show>
          <Show when={telemetry.envOverride}>
            <span class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] text-[var(--text-muted)]">
              {t("privacy.telemetryEnvLocked")}
            </span>
          </Show>
          <ToggleSwitch
            checked={telemetry.enabled && !telemetry.offline}
            disabled={telemetry.envOverride || telemetry.offline || telemetry.saving}
            onChange={(next) => void saveTelemetryEnabled(next)}
          />
        </SettingRow>

        <SettingRow
          icon={History}
          label={t("privacy.localData")}
          tip={t("privacy.historyScope")}
        >
          <Show when={historyResult()}>
            <span role="status" class="text-[12px] text-[var(--text-secondary)]">
              {historyResult()}
            </span>
          </Show>
          <button
            type="button"
            class="hoxi-press inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-3.5 py-1.5 text-[12.5px] font-medium text-[var(--text-primary)] transition-colors cursor-pointer"
            onClick={() =>
              setHistoryResult(
                clearLocalPromptHistory()
                  ? t("privacy.historyCleared")
                  : t("privacy.historyError")
              )
            }
          >
            <Trash2 size={13} />
            <span>{t("privacy.clearHistory")}</span>
          </button>
        </SettingRow>
      </SettingGroup>

      <Show when={telemetry.error}>
        <p role="alert" class="px-1 text-[12.5px] text-rose-500">
          {telemetry.error}
        </p>
      </Show>
    </div>
  );
}
