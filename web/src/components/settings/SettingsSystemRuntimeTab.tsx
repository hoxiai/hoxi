// web/src/components/settings/SettingsSystemRuntimeTab.tsx
import { createSignal, onMount, Show } from "solid-js";
import { Activity, ArrowUpCircle, Cpu, Download, ExternalLink, KeyRound, RefreshCw, RotateCcw } from "lucide-solid";
import { hoxiAPI, type SystemRuntimeInfo } from "../../services/api";
import {
  checkDesktopUpdate,
  closeSettingsModal,
  desktopUpdater,
  loadAgents,
  loadProviders,
  openProviderModal,
  resetAllAppSettings,
  resetProviderTelemetry,
  workspace,
} from "../../store/workspace";
import { Spinner } from "../icons";
import { t } from "../../i18n";
import { SettingGroup, SettingRow } from "./SettingsModal";

export function SystemRuntimeSettingsTab() {
  const [sysInfo, setSysInfo] = createSignal<SystemRuntimeInfo | null>(null);
  const [reloading, setReloading] = createSignal(false);
  const [actionToast, setActionToast] = createSignal<string | null>(null);

  const fetchInfo = async () => {
    try {
      const info = await hoxiAPI.getSystemInfo();
      setSysInfo(info);
    } catch {
      /* ignore */
    }
  };

  onMount(() => {
    void fetchInfo();
  });

  const showToast = (msg: string) => {
    setActionToast(msg);
    setTimeout(() => setActionToast(null), 3000);
  };

  const onReloadEngine = async () => {
    if (reloading()) return;
    setReloading(true);
    try {
      await hoxiAPI.disposeGlobal();
      await Promise.allSettled([
        loadProviders(),
        loadAgents(workspace.rootDir || undefined),
        fetchInfo(),
      ]);
      showToast(t("settings.reloadEngineSuccess"));
    } catch {
      /* ignore */
    } finally {
      setReloading(false);
    }
  };

  const onClearTelemetry = async () => {
    await resetProviderTelemetry();
    showToast(t("settings.resetTelemetrySuccess"));
  };

  const onResetSettings = () => {
    if (!confirm(t("settings.resetSettingsConfirm"))) return;
    resetAllAppSettings();
    showToast(t("settings.resetSettingsSuccess"));
  };

  return (
    <div class="space-y-6">
      {/* 1. OpenCode Runtime Diagnostics */}
      <SettingGroup
        title={t("settings.sectionRuntimeTitle")}
        tip={t("settings.sectionRuntimeDesc")}
        action={
          <button
            type="button"
            onClick={() => void fetchInfo()}
            class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            title={t("common.refresh")}
          >
            <RefreshCw size={14} />
          </button>
        }
      >
        <SettingRow label={t("settings.runtimeStatusLabel")}>
          <span class="inline-flex items-center gap-2 text-[13.5px] font-medium text-[var(--text-primary)]">
            <span
              class={`h-2.5 w-2.5 rounded-full ${
                workspace.connected
                  ? "bg-[var(--ok)]"
                  : "bg-[var(--warn)] animate-pulse"
              }`}
            />
            {workspace.connected ? t("common.ready") : t("common.connecting")}
            <Show when={sysInfo()?.openCodeVersion}>
              <span class="ml-1 rounded-md bg-[var(--bg-panel)] px-2 py-0.5 font-mono text-[12px] text-[var(--text-muted)]">
                {sysInfo()?.openCodeVersion}
              </span>
            </Show>
          </span>
        </SettingRow>

        <SettingRow label={t("settings.runtimePortLabel")}>
          <span class="font-mono text-[13px] text-[var(--text-secondary)]">
            127.0.0.1:{sysInfo()?.openCodePort || "—"}
          </span>
        </SettingRow>

        <SettingRow label={t("settings.runtimeBinaryLabel")}>
          <span
            class="max-w-[320px] truncate font-mono text-[12.5px] text-[var(--text-secondary)] selectable-text"
            title={sysInfo()?.openCodeBinPath}
          >
            {sysInfo()?.openCodeBinPath || t("settings.runtimeNotDetected")}
          </span>
        </SettingRow>

        <SettingRow label={t("settings.runtimeConfigPathLabel")}>
          <span
            class="max-w-[320px] truncate font-mono text-[12.5px] text-[var(--text-secondary)] selectable-text"
            title={sysInfo()?.userConfigPath}
          >
            {sysInfo()?.userConfigPath || "~/.hoxi/models.json"}
          </span>
        </SettingRow>

        <SettingRow label={t("settings.runtimePlatformLabel")}>
          <span class="font-mono text-[12.5px] text-[var(--text-secondary)]">
            {sysInfo()
              ? `${sysInfo()!.os}/${sysInfo()!.arch} (${sysInfo()!.goVersion})`
              : "—"}
          </span>
        </SettingRow>
      </SettingGroup>

      {/* 2. Maintenance Actions */}
      <SettingGroup
        title={t("settings.sectionMaintenanceTitle")}
        tip={t("settings.sectionMaintenanceDesc")}
      >
        <div class="p-4 flex flex-wrap items-center gap-3">
          <button
            type="button"
            disabled={reloading()}
            onClick={() => void onReloadEngine()}
            class="inline-flex items-center gap-2 rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[13px] font-medium text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
          >
            <Cpu size={14} />
            {reloading()
              ? t("settings.reloadingEngineBtn")
              : t("settings.reloadEngineBtn")}
          </button>

          <button
            type="button"
            onClick={() => {
              closeSettingsModal();
              openProviderModal("providers");
            }}
            class="inline-flex items-center gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-4 py-2 text-[13px] font-medium text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <KeyRound size={14} class="text-[var(--accent-primary)]" />
            {t("settings.manageModelsAndKeysBtn")}
          </button>

          <button
            type="button"
            onClick={() => void onClearTelemetry()}
            class="inline-flex items-center gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-4 py-2 text-[13px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Activity size={14} />
            {t("settings.resetTelemetryBtn")}
          </button>

          <button
            type="button"
            onClick={onResetSettings}
            class="inline-flex items-center gap-2 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-4 py-2 text-[13px] font-medium text-[var(--danger-text)] hover:opacity-90 transition-opacity cursor-pointer"
          >
            <RotateCcw size={14} />
            {t("settings.resetSettingsBtn")}
          </button>
        </div>
      </SettingGroup>

      {/* 3. Desktop Version & Online Updates */}
      <SettingGroup
        title={t("settings.sectionUpdaterTitle")}
        tip={t("settings.sectionUpdaterDesc")}
        action={
          <button
            type="button"
            disabled={desktopUpdater.isChecking}
            onClick={() => void checkDesktopUpdate(true)}
            class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer disabled:opacity-50"
          >
            <Show when={desktopUpdater.isChecking} fallback={<RefreshCw size={12} />}>
              <Spinner class="h-3.5 w-3.5" />
            </Show>
            <span>
              {desktopUpdater.isChecking
                ? t("settings.updaterCheckingBtn")
                : t("settings.updaterCheckNowBtn")}
            </span>
          </button>
        }
      >
        <SettingRow label={t("settings.updaterCurrentVersion")}>
          <span class="inline-flex items-center gap-2 font-mono text-[13px] font-semibold text-[var(--text-primary)]">
            <ArrowUpCircle size={15} class="text-[var(--accent-primary)]" />
            {workspace.appVersion || sysInfo()?.appVersion
              ? `v${workspace.appVersion || sysInfo()?.appVersion}`
              : t("common.loading")}
          </span>
        </SettingRow>

        <Show when={desktopUpdater.result}>
          {(res) => (
            <div class="p-3.5 border-t border-[var(--border-subtle)]/70 bg-[var(--bg-inset)]/40 space-y-3">
              <Show
                when={res().needUpdate}
                fallback={
                  <div class="flex items-center gap-2 text-[12px] text-emerald-500 font-medium">
                    <span class="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                    <span>{t("settings.updaterAlreadyLatest")}</span>
                  </div>
                }
              >
                <div class="flex items-center justify-between">
                  <span class="text-[12.5px] font-semibold text-amber-500">
                    {t("settings.updaterNewVersionFound", {
                      version: `v${res().latestVersion}`,
                    })}
                  </span>
                  <span class="text-[11px] font-mono text-[var(--text-faint)]">
                    {res().checkedAt ? new Date(res().checkedAt).toLocaleDateString() : ""}
                  </span>
                </div>

                <Show when={res().releaseNotes}>
                  <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-3 text-[11.5px] leading-relaxed text-[var(--text-secondary)] max-h-[160px] overflow-y-auto whitespace-pre-wrap font-sans">
                    <div class="font-medium text-[var(--text-primary)] mb-1">
                      {res().title || t("settings.updaterReleaseNotesTitle")}
                    </div>
                    {res().releaseNotes}
                  </div>
                </Show>

                {/* Mirror download buttons: CN GitCode / Global GitHub */}
                <div class="flex flex-wrap items-center gap-2 pt-1">
                  <Show when={res().mirrors.cn}>
                    <a
                      href={res().mirrors.cn}
                      target="_blank"
                      rel="noreferrer"
                      class="inline-flex items-center gap-1.5 rounded-xl bg-gradient-to-r from-amber-500 to-orange-500 hover:from-amber-600 hover:to-orange-600 px-3 py-1.5 text-[11.5px] font-semibold text-white shadow-xs transition-all cursor-pointer"
                    >
                      <Download size={13} />
                      <span>{t("settings.updaterDownloadFromGitCodeBtn")}</span>
                    </a>
                  </Show>
                  <Show when={res().mirrors.global}>
                    <a
                      href={res().mirrors.global}
                      target="_blank"
                      rel="noreferrer"
                      class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-3 py-1.5 text-[11.5px] font-medium text-[var(--text-primary)] transition-colors cursor-pointer"
                    >
                      <ExternalLink size={12} />
                      <span>{t("settings.updaterDownloadFromGitHubBtn")}</span>
                    </a>
                  </Show>
                  <Show when={!res().mirrors.cn && !res().mirrors.global && res().downloadUrl}>
                    <a
                      href={res().downloadUrl}
                      target="_blank"
                      rel="noreferrer"
                      class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3 py-1.5 text-[11.5px] font-medium text-white transition-opacity hover:opacity-90 cursor-pointer"
                    >
                      <Download size={13} />
                      <span>{t("settings.updaterDownloadNowBtn")}</span>
                    </a>
                  </Show>
                </div>
              </Show>
            </div>
          )}
        </Show>

        <Show when={desktopUpdater.error}>
          <div class="p-3 border-t border-[var(--border-subtle)]/70 text-[11.5px] text-rose-500">
            {t("settings.updaterCheckFailed")} ({desktopUpdater.error})
          </div>
        </Show>
      </SettingGroup>

      <Show when={actionToast()}>
        <div class="rounded-xl border border-[var(--ok-border)] bg-[var(--ok-surface)] px-4 py-2.5 text-[13px] text-[var(--ok-text)]">
          {actionToast()}
        </div>
      </Show>
    </div>
  );
}
