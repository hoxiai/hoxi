// web/src/components/settings/SettingsSystemRuntimeTab.tsx
import { createSignal, onMount, Show } from "solid-js";
import { Activity, ArrowUpCircle, CheckCircle2, ChevronRight, Download, ExternalLink, KeyRound, RefreshCw, RotateCcw, ShieldCheck } from "lucide-solid";
import type { SystemRuntimeInfo } from "../../services/api";
import {
  appSettings,
  checkDesktopUpdate,
  desktopUpdater,
  fetchSystemRuntimeInfo,
  installOpenCodeEngine,
  openExternalURL,
  openProviderModal,
  refreshDesktopUpdateStatus,
  resetAllAppSettings,
  resetProviderTelemetry,
  startDesktopUpdate,
  updateAppSetting,
  workspace,
} from "../../store/workspace";
import { Spinner } from "../icons";
import { t } from "../../i18n";
import { SettingGroup, SettingRow, ToggleSwitch } from "./setting-controls";

export function SystemRuntimeSettingsTab() {
  const [sysInfo, setSysInfo] = createSignal<SystemRuntimeInfo | null>(null);
  const [installingEngine, setInstallingEngine] = createSignal(false);
  const [actionToast, setActionToast] = createSignal<string | null>(null);

  const fetchInfo = async () => {
    try {
      setSysInfo(await fetchSystemRuntimeInfo());
    } catch {
      /* ignore */
    }
  };

  onMount(() => {
    void fetchInfo();
    void refreshDesktopUpdateStatus();
    if (!desktopUpdater.hasChecked) {
      void checkDesktopUpdate(false);
    }
  });

  const showToast = (msg: string) => {
    setActionToast(msg);
    setTimeout(() => setActionToast(null), 3000);
  };

  const onEngineAction = async () => {
    if (installingEngine()) return;
    const restart = engineReady();
    setInstallingEngine(true);
    try {
      await installOpenCodeEngine(restart);
      await fetchInfo();
      showToast(
        restart
          ? t("settings.engineRestartSuccess")
          : t("settings.engineInstallSuccess")
      );
    } catch (err) {
      showToast(
        t("settings.engineInstallFailed", {
          error: err instanceof Error ? err.message : String(err),
        })
      );
    } finally {
      setInstallingEngine(false);
    }
  };

  const engineReady = () =>
    sysInfo() ? Boolean(sysInfo()!.openCodeReady) : workspace.connected;

  const engineSourceLabel = () => {
    switch (sysInfo()?.openCodeSource) {
      case "bundled":
        return t("settings.engineSourceBundled");
      case "downloaded":
        return t("settings.engineSourceDownloaded");
      case "external":
        return t("settings.engineSourceExternal");
      default:
        return t("settings.runtimeNotDetected");
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

  const currentVer = () => workspace.appVersion || sysInfo()?.appVersion || "";
  const hasNewVersion = () => Boolean(desktopUpdater.result?.needUpdate);

  return (
    <div class="space-y-6">
      {/* 1. Desktop Version & Online Updates (Hero Card) */}
      <SettingGroup
        title={t("settings.sectionUpdaterTitle")}
        tip={t("settings.sectionUpdaterDesc")}
      >
        {/* Hero Header Row */}
        <div class="flex flex-wrap items-center justify-between gap-4 px-5 py-4">
          <div class="flex items-center gap-3.5 min-w-0">
            <div
              class={`flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl border transition-colors ${
                hasNewVersion()
                  ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)] shadow-2xs"
                  : desktopUpdater.result && !desktopUpdater.result.needUpdate
                    ? "border-emerald-500/25 bg-emerald-500/10 text-emerald-500"
                    : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-secondary)]"
              }`}
            >
              <Show
                when={!desktopUpdater.result || desktopUpdater.result.needUpdate}
                fallback={<CheckCircle2 size={20} />}
              >
                <ArrowUpCircle size={20} />
              </Show>
            </div>

            <div class="min-w-0 space-y-1">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-[15px] font-semibold text-[var(--text-primary)]">
                  Hoxi Desktop
                </span>
                <span class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 font-mono text-[12px] font-medium text-[var(--text-secondary)]">
                  {currentVer() ? `v${currentVer()}` : t("common.loading")}
                </span>
                <Show when={hasNewVersion()}>
                  <span class="text-[12px] text-[var(--text-faint)]">→</span>
                  <span class="inline-flex items-center gap-1 rounded-full border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-0.5 font-mono text-[11px] font-semibold text-[var(--accent-primary)]">
                    v{desktopUpdater.result?.latestVersion}
                  </span>
                </Show>
              </div>

              <div class="text-[12px] text-[var(--text-muted)] truncate">
                <Show
                  when={!desktopUpdater.error}
                  fallback={
                    <span class="text-rose-500">
                      {t("settings.updaterCheckFailed")} ({desktopUpdater.error})
                    </span>
                  }
                >
                  <Show
                    when={desktopUpdater.result}
                    fallback={<span>{t("settings.sectionUpdaterDesc")}</span>}
                  >
                    {(res) => (
                      <Show
                        when={res().needUpdate}
                        fallback={
                          <span class="inline-flex items-center gap-1.5 text-emerald-500 font-medium">
                            <span class="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                            {t("settings.updaterAlreadyLatest")}
                          </span>
                        }
                      >
                        <span class="text-[var(--text-secondary)]">
                          {desktopUpdater.nativeAvailable
                            ? t("header.updateNativeExplanation")
                            : t("settings.updaterNewVersionFound", {
                                version: `v${res().latestVersion}`,
                              })}
                        </span>
                      </Show>
                    )}
                  </Show>
                </Show>
              </div>
            </div>
          </div>

          {/* Right Action Cluster */}
          <div class="flex items-center gap-2 shrink-0">
            <Show when={hasNewVersion() && desktopUpdater.nativeAvailable}>
              <button
                type="button"
                disabled={desktopUpdater.isInstalling}
                onClick={() => void startDesktopUpdate()}
                class="hoxi-press inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] hover:opacity-90 px-4 py-2 text-[13px] font-semibold text-[var(--accent-foreground)] shadow-xs transition-opacity cursor-pointer disabled:opacity-50 disabled:cursor-wait"
              >
                <ArrowUpCircle size={14} />
                <span>
                  {desktopUpdater.isInstalling
                    ? t("header.updateOpening")
                    : t("header.updateInApp")}
                </span>
              </button>
            </Show>

            <button
              type="button"
              disabled={desktopUpdater.isChecking}
              onClick={() => void checkDesktopUpdate(true)}
              title={t("settings.updaterCheckNowBtn")}
              class={
                hasNewVersion() && desktopUpdater.nativeAvailable
                  ? "hoxi-press inline-flex items-center justify-center rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] p-2 text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer disabled:opacity-50"
                  : "hoxi-press inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] hover:border-[var(--border-strong)] px-3.5 py-2 text-[13px] font-medium text-[var(--text-primary)] shadow-2xs transition-colors cursor-pointer disabled:opacity-50"
              }
            >
              <Show when={desktopUpdater.isChecking} fallback={<RefreshCw size={13} />}>
                <Spinner class="h-3.5 w-3.5" />
              </Show>
              <Show when={!(hasNewVersion() && desktopUpdater.nativeAvailable)}>
                <span>
                  {desktopUpdater.isChecking
                    ? t("settings.updaterCheckingBtn")
                    : t("settings.updaterCheckNowBtn")}
                </span>
              </Show>
            </button>
          </div>
        </div>

        {/* Draining Notice */}
        <Show when={desktopUpdater.draining}>
          <div class="px-5 py-3 bg-amber-500/8 text-[12px] text-amber-600 dark:text-amber-400 flex items-center gap-2">
            <span class="h-2 w-2 rounded-full bg-amber-500 animate-pulse shrink-0" />
            <span>{t("header.updateDrainingNotice")}</span>
          </div>
        </Show>

        {/* Expandable Release Notes & Manual Installer Links when Update Available */}
        <Show when={desktopUpdater.result?.needUpdate && desktopUpdater.result}>
          {(res) => (
            <div class="px-5 py-4 bg-[var(--bg-panel)]/45 space-y-3">
              <Show when={res().releaseNotes}>
                <div class="rounded-xl border border-[var(--border-subtle)]/80 bg-[var(--bg-inset)]/55 px-4 py-3 space-y-1.5">
                  <div class="flex items-center justify-between gap-2">
                    <span class="text-[12px] font-semibold text-[var(--text-primary)]">
                      {res().title || t("settings.updaterReleaseNotesTitle")}
                    </span>
                    <Show when={res().checkedAt}>
                      <span class="font-mono text-[11px] text-[var(--text-faint)]">
                        {new Date(res().checkedAt).toLocaleDateString()}
                      </span>
                    </Show>
                  </div>
                  <div class="text-[12px] leading-relaxed text-[var(--text-secondary)] max-h-[160px] overflow-y-auto whitespace-pre-wrap font-sans">
                    {res().releaseNotes}
                  </div>
                </div>
              </Show>

              {/* Footer: SHA-256 verification badge + subtle manual download pills */}
              <div class="flex flex-wrap items-center justify-between gap-2 pt-0.5">
                <div class="flex items-center gap-1.5 text-[11px] font-mono text-[var(--text-faint)]">
                  <Show when={res().sha256}>
                    <ShieldCheck size={13} class="text-emerald-500 shrink-0" />
                    <span class=" selectable-text" title={res().sha256}>
                      SHA-256: {res().sha256?.slice(0, 12)}…
                    </span>
                  </Show>
                </div>

                <div class="flex flex-wrap items-center gap-2">
                  <Show when={res().mirrors.global}>
                    <a
                      href={res().mirrors.global}
                      target="_blank"
                      rel="noreferrer"
                      onClick={(e) => {
                        e.preventDefault();
                        if (res().mirrors.global) openExternalURL(res().mirrors.global!);
                      }}
                      class={
                        desktopUpdater.nativeAvailable
                          ? "inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                          : "inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] hover:opacity-90 px-3.5 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] transition-opacity cursor-pointer"
                      }
                    >
                      <ExternalLink size={12} />
                      <span>{t("settings.updaterDownloadFromGitHubBtn")}</span>
                    </a>
                  </Show>
                  <Show when={!res().mirrors.global && res().downloadUrl}>
                    <a
                      href={res().downloadUrl}
                      target="_blank"
                      rel="noreferrer"
                      onClick={(e) => {
                        e.preventDefault();
                        if (res().downloadUrl) openExternalURL(res().downloadUrl);
                      }}
                      class={
                        desktopUpdater.nativeAvailable
                          ? "inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                          : "inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] transition-opacity hover:opacity-90 cursor-pointer"
                      }
                    >
                      <Download size={12} />
                      <span>{t("settings.updaterDownloadNowBtn")}</span>
                    </a>
                  </Show>
                </div>
              </div>
            </div>
          )}
        </Show>
      </SettingGroup>

      {/* 2. OpenCode Runtime Diagnostics */}
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
          <span class="inline-flex items-center gap-2 text-[13px] font-medium text-[var(--text-primary)]">
            <span
              class={`h-2.5 w-2.5 rounded-full ${
                engineReady() ? "bg-[var(--ok)]" : "bg-[var(--warn)] animate-pulse"
              }`}
            />
            {engineReady()
              ? t("common.ready")
              : sysInfo()
                ? t("settings.engineNotReady")
                : t("common.connecting")}
            <Show when={sysInfo()?.openCodeVersion}>
              <span class="ml-1 rounded-md bg-[var(--bg-panel)] px-2 py-0.5 font-mono text-[12px] text-[var(--text-muted)]">
                {sysInfo()?.openCodeVersion}
              </span>
            </Show>
          </span>
        </SettingRow>

        <SettingRow label={t("settings.engineSourceLabel")}>
          <span class="text-[13px] text-[var(--text-secondary)]">
            {engineSourceLabel()}
          </span>
        </SettingRow>

        <SettingRow
          label={t("settings.keepAwakeLabel")}
          tip={t("settings.keepAwakeDesc")}
        >
          <Show when={sysInfo()?.status?.keepAwakeActive}>
            <span class="rounded-md bg-emerald-500/15 px-2 py-0.5 text-[11px] font-medium text-emerald-500">
              {t("settings.keepAwakeActiveBadge")}
            </span>
          </Show>
          <ToggleSwitch
            checked={appSettings.keepAwakeWhileRunning}
            onChange={(val) => {
              updateAppSetting("keepAwakeWhileRunning", val);
              setTimeout(() => void fetchInfo(), 150);
            }}
          />
        </SettingRow>

        <details class="group/rtpaths">
          <summary class="flex cursor-pointer list-none items-center gap-1.5 px-5 py-2.5 text-[12px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] [&::-webkit-details-marker]:hidden">
            <ChevronRight size={13} class="transition-transform group-open/rtpaths:rotate-90" />
            <span>{t("settings.runtimeAdvancedPaths")}</span>
          </summary>
          <div class="divide-y divide-[var(--border-subtle)]/55 border-t border-[var(--border-subtle)]/55">
            <SettingRow label={t("settings.runtimePortLabel")}>
              <span class="font-mono text-[13px] text-[var(--text-secondary)]">
                127.0.0.1:{sysInfo()?.openCodePort || "—"}
              </span>
            </SettingRow>

            <SettingRow label={t("settings.runtimeBinaryLabel")}>
              <span
                class="max-w-[320px] truncate font-mono text-[13px] text-[var(--text-secondary)] selectable-text"
                title={sysInfo()?.openCodeBinPath}
              >
                {sysInfo()?.openCodeBinPath || t("settings.runtimeNotDetected")}
              </span>
            </SettingRow>

            <SettingRow label={t("settings.runtimeConfigPathLabel")}>
              <span
                class="max-w-[320px] truncate font-mono text-[13px] text-[var(--text-secondary)] selectable-text"
                title={sysInfo()?.userConfigPath}
              >
                {sysInfo()?.userConfigPath || "~/.hoxi/models.json"}
              </span>
            </SettingRow>

            <SettingRow label={t("settings.runtimePlatformLabel")}>
              <span class="font-mono text-[13px] text-[var(--text-secondary)]">
                {sysInfo()
                  ? `${sysInfo()!.os}/${sysInfo()!.arch} (${sysInfo()!.goVersion})`
                  : "—"}
              </span>
            </SettingRow>
          </div>
        </details>
      </SettingGroup>

      {/* 3. Maintenance Actions */}
      <SettingGroup
        title={t("settings.sectionMaintenanceTitle")}
        tip={t("settings.sectionMaintenanceDesc")}
      >
        <div class="p-4 flex flex-wrap items-center gap-3">
          <button
            type="button"
            disabled={installingEngine()}
            onClick={() => void onEngineAction()}
            class="inline-flex items-center gap-2 rounded-xl bg-[var(--accent-primary)] px-4 py-2 text-[13px] font-medium text-[var(--accent-foreground)] hover:opacity-90 transition-opacity cursor-pointer disabled:opacity-50 disabled:cursor-wait"
          >
            <Show
              when={installingEngine()}
              fallback={
                engineReady() ? (
                  <RotateCcw size={14} />
                ) : (
                  <Download size={14} />
                )
              }
            >
              <Spinner class="h-3.5 w-3.5" />
            </Show>
            {installingEngine()
              ? engineReady()
                ? t("settings.engineRestartingBtn")
                : t("settings.engineInstallingBtn")
              : engineReady()
                ? t("settings.engineRestartBtn")
                : t("settings.engineInstallBtn")}
          </button>

          <button
            type="button"
            onClick={() => {
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

      <Show when={actionToast()}>
        <div class="rounded-xl border border-[var(--ok-border)] bg-[var(--ok-surface)] px-4 py-2.5 text-[13px] text-[var(--ok-text)]">
          {actionToast()}
        </div>
      </Show>
    </div>
  );
}
