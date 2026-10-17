// web/src/components/settings/SettingsSystemRuntimeTab.tsx
import { createSignal, onMount, Show } from "solid-js";
import { Activity, Cpu, KeyRound, RefreshCw, RotateCcw } from "lucide-solid";
import { hoxiAPI, type SystemRuntimeInfo } from "../../services/api";
import {
  closeSettingsModal,
  loadAgents,
  loadProviders,
  openProviderModal,
  resetAllAppSettings,
  resetProviderTelemetry,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";

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
      <section class="space-y-3">
        <div class="flex items-center justify-between">
          <div>
            <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
              {t("settings.sectionRuntimeTitle")}
            </h3>
            <p class="text-[11.5px] text-[var(--text-muted)]">
              {t("settings.sectionRuntimeDesc")}
            </p>
          </div>
          <button
            type="button"
            onClick={() => void fetchInfo()}
            class="rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            title={t("common.refresh")}
          >
            <RefreshCw size={13} />
          </button>
        </div>

        <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] divide-y divide-[var(--border-subtle)] text-[12px]">
          <div class="flex items-center justify-between px-4 py-2.5">
            <span class="text-[var(--text-muted)]">{t("settings.runtimeStatusLabel")}</span>
            <span class="inline-flex items-center gap-1.5 font-medium text-[var(--text-primary)]">
              <span
                class={`h-2 w-2 rounded-full ${
                  workspace.connected ? "bg-[var(--ok)]" : "bg-[var(--warn)] animate-pulse"
                }`}
              />
              {workspace.connected ? t("common.ready") : t("common.connecting")}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimeVersionLabel")}</span>
            <span class="font-mono text-[11.5px] text-[var(--text-primary)] truncate">
              {sysInfo()?.openCodeVersion || t("settings.runtimeNotDetected")}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimeBinaryLabel")}</span>
            <span class="font-mono text-[11px] text-[var(--text-secondary)] truncate selectable-text">
              {sysInfo()?.openCodeBinPath || t("settings.runtimeNotDetected")}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimePortLabel")}</span>
            <span class="font-mono text-[11.5px] text-[var(--text-primary)]">
              127.0.0.1:{sysInfo()?.openCodePort || "—"}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimeGatewayLabel")}</span>
            <span class="font-mono text-[11px] text-[var(--text-secondary)] truncate selectable-text">
              {sysInfo()?.wsBaseURL || workspace.wsBaseURL || "—"}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimeConfigPathLabel")}</span>
            <span class="font-mono text-[11px] text-[var(--text-secondary)] truncate selectable-text">
              {sysInfo()?.userConfigPath || "~/.hoxi/models.json"}
            </span>
          </div>

          <div class="flex items-center justify-between gap-4 px-4 py-2.5">
            <span class="text-[var(--text-muted)] shrink-0">{t("settings.runtimePlatformLabel")}</span>
            <span class="font-mono text-[11.5px] text-[var(--text-primary)]">
              {sysInfo() ? `${sysInfo()!.os}/${sysInfo()!.arch} (${sysInfo()!.goVersion})` : "—"}
            </span>
          </div>
        </div>
      </section>

      {/* Maintenance Actions */}
      <section class="border-t border-[var(--border-subtle)] pt-5 space-y-3">
        <div>
          <h3 class="text-[13.5px] font-semibold text-[var(--text-primary)]">
            {t("settings.sectionMaintenanceTitle")}
          </h3>
          <p class="text-[11.5px] text-[var(--text-muted)]">
            {t("settings.sectionMaintenanceDesc")}
          </p>
        </div>

        <div class="flex flex-wrap items-center gap-2.5">
          <button
            type="button"
            disabled={reloading()}
            onClick={() => void onReloadEngine()}
            class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3.5 py-2 text-[12px] font-medium text-white hover:opacity-90 disabled:opacity-50 transition-opacity cursor-pointer"
          >
            <Cpu size={13} />
            {reloading() ? t("settings.reloadingEngineBtn") : t("settings.reloadEngineBtn")}
          </button>

          <button
            type="button"
            onClick={() => {
              closeSettingsModal();
              openProviderModal("providers");
            }}
            class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] px-3.5 py-2 text-[12px] font-medium text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <KeyRound size={13} class="text-[var(--accent-primary)]" />
            {t("settings.manageModelsAndKeysBtn")}
          </button>

          <button
            type="button"
            onClick={() => void onClearTelemetry()}
            class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] px-3.5 py-2 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Activity size={13} />
            {t("settings.resetTelemetryBtn")}
          </button>

          <button
            type="button"
            onClick={onResetSettings}
            class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3.5 py-2 text-[12px] font-medium text-[var(--danger-text)] hover:opacity-90 transition-opacity cursor-pointer"
          >
            <RotateCcw size={13} />
            {t("settings.resetSettingsBtn")}
          </button>
        </div>

        <Show when={actionToast()}>
          <div class="rounded-xl border border-[var(--ok-border)] bg-[var(--ok-surface)] px-3.5 py-2 text-[12px] text-[var(--ok-text)]">
            {actionToast()}
          </div>
        </Show>
      </section>
    </div>
  );
}
