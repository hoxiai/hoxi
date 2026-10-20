// web/src/components/layout/UpdateBadge.tsx
import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import { ArrowUpCircle, Download, ExternalLink, X } from "lucide-solid";
import {
  checkDesktopUpdate,
  desktopUpdater,
  openExternalURL,
  refreshDesktopUpdateStatus,
  startDesktopUpdate,
} from "../../store/workspace";
import { t } from "../../i18n";

const [updateModalOpen, setUpdateModalOpen] = createSignal(false);

export function openDesktopUpdateModal(): void {
  setUpdateModalOpen(true);
}

export default function UpdateBadge() {
  onMount(() => {
    void checkDesktopUpdate(false);
    void refreshDesktopUpdateStatus();
    const statusTimer = window.setInterval(() => void refreshDesktopUpdateStatus(), 5_000);

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && updateModalOpen()) {
        setUpdateModalOpen(false);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      window.clearInterval(statusTimer);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  const updateResult = () => desktopUpdater.result;

  return (
    <Presence when={updateModalOpen()}>
      {(state) => (
        <div
          data-state={state()}
          class="hoxi-modal-backdrop fixed inset-0 z-50 flex items-center justify-center p-4"
          onClick={() => setUpdateModalOpen(false)}
        >
          <div
            class="hoxi-modal-panel hoxi-elevation-4 relative w-full max-w-md rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] overflow-hidden"
            onClick={(e) => e.stopPropagation()}
          >
            <div class="p-5 border-b border-[var(--border-subtle)] bg-[var(--accent-surface)]">
              <div class="flex items-start justify-between">
                <div class="flex items-center gap-3">
                  <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-primary)] text-[var(--accent-foreground)] shadow-md">
                    <ArrowUpCircle size={20} />
                  </div>
                  <div>
                    <h3 class="text-[15px] font-bold text-[var(--text-primary)]">
                      {desktopUpdater.draining
                        ? t("header.updateRestartRequired")
                        : t("header.updateModalTitle")}
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
                  onClick={() => setUpdateModalOpen(false)}
                  class="rounded-lg p-1.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                >
                  <X size={15} />
                </button>
              </div>
            </div>

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

            <div class="p-4 border-t border-[var(--border-subtle)] bg-[var(--bg-inset)]/40 flex flex-wrap items-center justify-between gap-2">
              <button
                type="button"
                onClick={() => setUpdateModalOpen(false)}
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
                    onClick={(e) => {
                      e.preventDefault();
                      const url = updateResult()?.mirrors.cn;
                      if (url) openExternalURL(url);
                    }}
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
                    onClick={(e) => {
                      e.preventDefault();
                      const url = updateResult()?.mirrors.global;
                      if (url) openExternalURL(url);
                    }}
                    class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] hover:bg-[var(--bg-hover)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] transition-colors cursor-pointer"
                  >
                    <ExternalLink size={12} />
                    <span>{t("header.updateModalDownloadGitHub")}</span>
                  </a>
                </Show>
                <Show
                  when={
                    !updateResult()?.mirrors.cn &&
                    !updateResult()?.mirrors.global &&
                    updateResult()?.downloadUrl
                  }
                >
                  <a
                    href={updateResult()?.downloadUrl}
                    target="_blank"
                    rel="noreferrer"
                    onClick={(e) => {
                      e.preventDefault();
                      const url = updateResult()?.downloadUrl;
                      if (url) openExternalURL(url);
                    }}
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
      )}
    </Presence>
  );
}
