// web/src/components/layout/UpdateBadge.tsx
import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { ArrowUpCircle, Download, ExternalLink, Sparkles, X } from "lucide-solid";
import { checkDesktopUpdate, desktopUpdater } from "../../store/workspace";
import { t } from "../../i18n";

export default function UpdateBadge() {
  const [modalOpen, setModalOpen] = createSignal(false);

  onMount(() => {
    // 启动时后台静默自检一次版本
    void checkDesktopUpdate(false);

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && modalOpen()) {
        setModalOpen(false);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  const updateResult = () => desktopUpdater.result;
  const hasUpdate = () => Boolean(updateResult()?.needUpdate);

  return (
    <Show when={hasUpdate()}>
      {/* 1. Header Capsule Tag (Kilo Style) */}
      <button
        type="button"
        style={{ "--wails-draggable": "no-drag" } as any}
        onClick={() => setModalOpen(true)}
        class="inline-flex items-center gap-1.5 rounded-full bg-gradient-to-r from-amber-500/15 via-orange-500/15 to-amber-500/15 border border-amber-500/35 hover:border-amber-500/60 px-2.5 py-0.5 text-[11px] font-semibold text-amber-500 hover:text-amber-400 hover:shadow-xs transition-all cursor-pointer active:scale-[0.98]"
        title={t("header.updateAvailableTooltip", {
          version: `v${updateResult()?.latestVersion || ""}`,
        })}
      >
        <span class="relative flex h-1.5 w-1.5">
          <span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-amber-400 opacity-75" />
          <span class="relative inline-flex rounded-full h-1.5 w-1.5 bg-amber-500" />
        </span>
        <Sparkles size={11} class="text-amber-500" />
        <span>
          {t("header.updateAvailableBadge", {
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
            <div class="p-5 border-b border-[var(--border-subtle)] bg-gradient-to-r from-amber-500/10 via-orange-500/5 to-transparent">
              <div class="flex items-start justify-between">
                <div class="flex items-center gap-3">
                  <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br from-amber-500 to-orange-500 text-white shadow-md shadow-amber-500/20">
                    <ArrowUpCircle size={20} />
                  </div>
                  <div>
                    <h3 class="text-[14px] font-bold text-[var(--text-primary)]">
                      {t("header.updateModalTitle")}
                    </h3>
                    <div class="flex items-center gap-2 mt-1 font-mono text-[11px]">
                      <span class="text-[var(--text-muted)]">
                        {t("header.updateModalCurrentVer")}: v{updateResult()?.currentVersion}
                      </span>
                      <span class="text-[var(--text-faint)]">→</span>
                      <span class="font-bold text-amber-500">
                        {t("header.updateModalLatestVer")}: v{updateResult()?.latestVersion}
                      </span>
                    </div>
                  </div>
                </div>
                <button
                  type="button"
                  onClick={() => setModalOpen(false)}
                  class="rounded-lg p-1.5 text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                >
                  <X size={15} />
                </button>
              </div>
            </div>

            {/* Content Body: Release Notes */}
            <div class="p-5 space-y-3.5 max-h-[320px] overflow-y-auto">
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
                  {updateResult()?.releaseNotes || "Performance optimizations and stability fixes."}
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

              <div class="flex items-center gap-2">
                <Show when={updateResult()?.mirrors.cn}>
                  <a
                    href={updateResult()?.mirrors.cn}
                    target="_blank"
                    rel="noreferrer"
                    class="inline-flex items-center gap-1.5 rounded-xl bg-gradient-to-r from-amber-500 to-orange-500 hover:from-amber-600 hover:to-orange-600 px-3.5 py-1.5 text-[12px] font-semibold text-white shadow-xs transition-all cursor-pointer"
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
                    class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] hover:bg-[var(--bg-hover)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-primary)] transition-colors cursor-pointer"
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
                    class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3.5 py-1.5 text-[12px] font-semibold text-white transition-opacity hover:opacity-95 cursor-pointer"
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
