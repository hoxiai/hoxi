// web/src/components/layout/UpdateBadge.tsx
import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { ArrowUpCircle, Download, ExternalLink, Sparkles, X } from "lucide-solid";
import { checkDesktopUpdate, desktopUpdater, refreshDesktopUpdateStatus, startDesktopUpdate } from "../../store/workspace";
import { t } from "../../i18n";

export default function UpdateBadge() {
  const [modalOpen, setModalOpen] = createSignal(false);

  onMount(() => {
    // 启动时后台静默自检一次版本
    void checkDesktopUpdate(false);
    void refreshDesktopUpdateStatus();
    // Native updater initialization can finish after the frontend mounts.
    const statusTimer = window.setInterval(() => void refreshDesktopUpdateStatus(), 5_000);

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && modalOpen()) {
        setModalOpen(false);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      window.clearInterval(statusTimer);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  const updateResult = () => desktopUpdater.result;
  const hasUpdate = () => Boolean(updateResult()?.needUpdate || desktopUpdater.draining);

  return (
    <Show when={hasUpdate()}>
      {/* 1. Header Capsule Tag (Kilo Style) */}
      <button
        type="button"
        style={{ "--wails-draggable": "no-drag" } as any}
        onClick={() => setModalOpen(true)}
        class="inline-flex items-center gap-1.5 rounded-full bg-[var(--accent-surface)] border border-[var(--accent-border)] hover:border-[var(--accent-primary)] px-2.5 py-0.5 text-[11px] font-semibold text-[var(--accent-text)] hover:shadow-xs transition-all cursor-pointer active:scale-[0.98]"
        title={desktopUpdater.draining ? t("header.updateRestartRequired") : t("header.updateAvailableTooltip", {
          version: `v${updateResult()?.latestVersion || ""}`,
        })}
      >
        <span class="relative flex h-1.5 w-1.5">
          <span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-[var(--accent-primary)] opacity-75" />
          <span class="relative inline-flex rounded-full h-1.5 w-1.5 bg-[var(--accent-primary)]" />
        </span>
        <Sparkles size={11} />
        <span>
          {desktopUpdater.draining ? t("header.updateRestartRequired") : t("header.updateAvailableBadge", {
            version: `v${updateResult()?.latestVersion || ""}`,
          })}
        </span>
      </button>

      {/* 2. Kilo-style Update Details Modal */}
      <Show when={modalOpen()}>
        <div
          class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-xs animate-in fade-in duration-150"
          onClick={() => setModalOpen(false)}
        >
          <div
            class="relative w-full max-w-md rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl overflow-hidden animate-in zoom-in-95 duration-150"
            onClick={(e) => e.stopPropagation()}
          >
            {/* Header banner */}
            <div class="p-5 border-b border-[var(--border-subtle)] bg-[var(--accent-surface)]">
              <div class="flex items-start justify-between">
                <div class="flex items-center gap-3">
                  <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-primary)] text-[var(--accent-foreground)] shadow-md">
                    <ArrowUpCircle size={20} />
                  </div>
                  <div>
                    <h3 class="text-[14px] font-bold text-[var(--text-primary)]">
                      {desktopUpdater.draining ? t("header.updateRestartRequired") : t("header.updateModalTitle")}
                    </h3>
                    <Show when={updateResult()}>
                      <div class="flex items-center gap-2 mt-1 font-mono text-[11px]">
                        <span class="text-[var(--text-muted)]">
                          {t("header.updateModalCurrentVer")}: v{updateResult()?.currentVersion}
                        </span>
                        <span class="text-[var(--text-faint)]">→</span>
                        <span class="font-bold text-[var(--accent-text)]">
                          {t("header.updateModalLatestVer")}: v{updateResult()?.latestVersion}
                        </span>
                      </div>
                    </Show>
                  </div>
                </div>
                <button
                  type="button"
                  aria-label={t("header.updateModalDismiss")}
                  onClick={() => setModalOpen(false)}
                  class="rounded-lg p-1.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                >
                  <X size={15} />
                </button>
              </div>
            </div>

            {/* Content Body: Release Notes */}
            <div class="p-5 space-y-3.5 max-h-[320px] overflow-y-auto">
              <Show when={desktopUpdater.nativeAvailable || desktopUpdater.draining}>
                <p class="text-[12px] leading-relaxed text-[var(--text-secondary)]">
                  {t("header.updateNativeExplanation")}
                </p>
              </Show>
              <Show when={desktopUpdater.draining}>
                <div role="status" class="rounded-xl border border-amber-500/30 bg-amber-500/10 p-2.5 text-[11px] text-amber-600 dark:text-amber-400">
                  {t("header.updateDrainingNotice")}
                </div>
              </Show>
              <Show when={desktopUpdater.error}>
                <p role="alert" class="text-[12px] text-red-500">
                  {t("header.updateError", { error: desktopUpdater.error || "" })}
                </p>
              </Show>
              <Show when={updateResult()?.mandatory}>
                <div class="rounded-xl border border-amber-500/30 bg-amber-500/10 p-2.5 text-[11px] text-amber-600 dark:text-amber-400">
                  {t("header.updateModalMandatoryNotice")}
                </div>
              </Show>

              <div>
                <div class="text-[11px] font-semibold text-[var(--text-secondary)] uppercase tracking-wider mb-1.5">
                  {updateResult()?.title || t("header.updateModalTitle")}
                </div>
                <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 p-3 text-[12px] leading-relaxed text-[var(--text-secondary)] font-sans whitespace-pre-wrap">
                  {updateResult()?.releaseNotes || t("header.updateReleaseNotesFallback")}
                </div>
              </div>
            </div>

            {/* Footer Actions: GitCode / GitHub Mirrors */}
            <div class="p-4 border-t border-[var(--border-subtle)] bg-[var(--bg-inset)]/40 flex flex-wrap items-center justify-between gap-2">
              <button
                type="button"
                onClick={() => setModalOpen(false)}
                class="px-3 py-1.5 text-[12px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
              >
                {t("header.updateModalDismiss")}
              </button>

              <div class="flex flex-wrap items-center justify-end gap-2">
                <Show when={desktopUpdater.nativeAvailable}>
                  <button
                    type="button"
                    disabled={desktopUpdater.isInstalling}
                    onClick={() => void startDesktopUpdate()}
                    class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] hover:opacity-90 px-3.5 py-1.5 text-[12px] font-semibold text-[var(--accent-foreground)] shadow-xs transition-opacity cursor-pointer disabled:opacity-50 disabled:cursor-wait"
                  >
                    <ArrowUpCircle size={13} />
                    {desktopUpdater.isInstalling ? t("header.updateOpening") : t("header.updateInApp")}
                  </button>
                </Show>
                <Show when={updateResult()?.mirrors.cn}>
                  <a
                    href={updateResult()?.mirrors.cn}
                    target="_blank"
                    rel="noreferrer"
                    class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] hover:bg-[var(--bg-hover)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] transition-colors cursor-pointer"
                  >
                    <Download size={13} />
                    <span>{t("header.updateModalDownloadGitCode")}</span>
                  </a>
                </Show>

                <Show when={updateResult()?.mirrors.global}>
                  <a
                    href={updateResult()?.mirrors.global}
                    target="_blank"
                    rel="noreferrer"
                    class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] hover:bg-[var(--bg-hover)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] transition-colors cursor-pointer"
                  >
                    <ExternalLink size={12} />
                    <span>{t("header.updateModalDownloadGitHub")}</span>
                  </a>
                </Show>

                <Show when={!updateResult()?.mirrors.cn && !updateResult()?.mirrors.global && updateResult()?.downloadUrl}>
                  <a
                    href={updateResult()?.downloadUrl}
                    target="_blank"
                    rel="noreferrer"
                    class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] hover:bg-[var(--bg-hover)] px-3.5 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] transition-colors cursor-pointer"
                  >
                    <Download size={13} />
                    <span>{t("header.updateModalDownloadDirect")}</span>
                  </a>
                </Show>
              </div>
            </div>
          </div>
        </div>
      </Show>
    </Show>
  );
}
