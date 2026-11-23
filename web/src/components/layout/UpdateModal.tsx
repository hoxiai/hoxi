// web/src/components/layout/UpdateModal.tsx
import { createSignal, onCleanup, onMount, Show, type Accessor } from "solid-js";
import { Presence, type PresenceState } from "../motion/Presence";
import { ArrowUpCircle, Check, Copy, Download, ExternalLink, X } from "lucide-solid";
import { Spinner } from "../icons";
import UpdateProgressBar from "./UpdateProgressBar";
import {
  closeDesktopUpdateModal,
  desktopUpdater,
  desktopUpdateActiveSourceLabel,
  desktopUpdateButtonLabel,
  desktopUpdateDetailLabel,
  desktopUpdateProgressPercent,
  desktopUpdateSpeedEtaLabel,
  desktopUpdateStatusLabel,
  getAcceleratedUpdateMirrorUrl,
  getGitHubUpdateMirrorUrl,
  isDesktopUpdateInstalling,
  isUpdateModalOpen,
  openExternalURL,
  startDesktopUpdate,
} from "../../store/workspace";
import { t } from "../../i18n";

function UpdateDialog(props: { state: Accessor<PresenceState> }) {
  const [copiedCmd, setCopiedCmd] = createSignal(false);
  onMount(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeDesktopUpdateModal();
    };
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => window.removeEventListener("keydown", handleKeyDown));
  });

  const updateResult = () => desktopUpdater.result;
  const acceleratedUrl = () => getAcceleratedUpdateMirrorUrl(updateResult());
  const githubUrl = () => getGitHubUpdateMirrorUrl(updateResult());
  const installing = isDesktopUpdateInstalling;
  const percent = () => desktopUpdateProgressPercent(desktopUpdater.progress);

  return (
    <div
      data-state={props.state()}
      class="hoxi-modal-backdrop fixed inset-0 z-50 flex items-center justify-center p-4"
      onClick={closeDesktopUpdateModal}
    >
      <div
        class="hoxi-modal-panel hoxi-elevation-4 relative w-full max-w-md rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div class="p-5 border-b border-[var(--border-subtle)] bg-[var(--accent-surface)]">
          <div class="flex items-start justify-between">
            <div class="flex items-center gap-3">
              <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-primary)] text-[var(--accent-foreground)] shadow-md">
                <Show when={installing()} fallback={<ArrowUpCircle size={20} />}>
                  <Spinner class="h-5 w-5" />
                </Show>
              </div>
              <div>
                <h3 class="text-[15px] font-bold text-[var(--text-primary)]">
                  {installing()
                    ? desktopUpdateStatusLabel()
                    : desktopUpdater.draining
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
              onClick={closeDesktopUpdateModal}
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
          <Show when={desktopUpdater.managedBy}>
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 p-3 space-y-2 text-[12px] text-[var(--text-secondary)]">
              <p class="leading-relaxed">
                {t("header.updateManagedByNotice", { manager: desktopUpdater.managedBy || "" })}
              </p>
              <Show when={desktopUpdater.upgradeCommand}>
                <div class="flex items-center justify-between gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5">
                  <code class="font-mono text-[11.5px] text-[var(--text-primary)] truncate selectable-text">
                    {desktopUpdater.upgradeCommand}
                  </code>
                  <button
                    type="button"
                    onClick={() => {
                      const cmd = desktopUpdater.upgradeCommand;
                      if (cmd && navigator.clipboard) {
                        void navigator.clipboard.writeText(cmd);
                        setCopiedCmd(true);
                        setTimeout(() => setCopiedCmd(false), 2000);
                      }
                    }}
                    class="inline-flex shrink-0 items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] px-2 py-0.5 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                  >
                    <Show when={copiedCmd()} fallback={<Copy size={11} />}>
                      <Check size={11} class="text-emerald-500" />
                    </Show>
                    <span>{t("header.updateCopyCommand")}</span>
                  </button>
                </div>
              </Show>
            </div>
          </Show>
          <Show when={desktopUpdater.draining}>
            <div role="status" class="rounded-xl border border-amber-500/30 bg-amber-500/10 p-2.5 text-[11px] text-amber-600 dark:text-amber-400">
              {t("header.updateDrainingNotice")}
            </div>
          </Show>
          <Show when={installing()}>
            <div role="status" class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 p-3 space-y-2 text-[12px] text-[var(--text-secondary)]">
              <div class="flex items-center justify-between gap-2">
                <span class="inline-flex items-center gap-2 font-medium text-[var(--text-primary)]">
                  <Spinner class="h-3.5 w-3.5 text-[var(--accent-primary)] shrink-0" />
                  <span>{desktopUpdateStatusLabel()}</span>
                  <Show when={desktopUpdateActiveSourceLabel()}>
                    <span class="rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--text-muted)]">
                      {desktopUpdateActiveSourceLabel()}
                    </span>
                  </Show>
                </span>
                <Show when={percent() !== undefined}>
                  <span class="font-mono font-semibold text-[var(--accent-text)]">{percent()}%</span>
                </Show>
              </div>
              <UpdateProgressBar percent={percent()} label={desktopUpdateStatusLabel()} />
              <div class="flex flex-wrap items-center justify-between gap-2 text-[11px] text-[var(--text-muted)]">
                <span
                  class={
                    desktopUpdater.progress?.phase === "downloading" &&
                    (desktopUpdater.progress?.downloadedBytes || 0) > 0
                      ? "font-mono"
                      : ""
                  }
                >
                  {desktopUpdateDetailLabel()}
                </span>
                <Show when={desktopUpdateSpeedEtaLabel()}>
                  <span class="font-mono">{desktopUpdateSpeedEtaLabel()}</span>
                </Show>
              </div>
              <Show when={desktopUpdater.downloadSlow}>
                <div class="rounded-lg border border-amber-500/30 bg-amber-500/10 p-2 text-[11px] leading-relaxed text-amber-600 dark:text-amber-400">
                  {t("header.updateSlowHint")}
                </div>
              </Show>
            </div>
          </Show>
          <Show when={desktopUpdater.error}>
            <div role="alert" class="space-y-2">
              <p class="text-[12px] text-red-500">
                {t("header.updateError", { error: desktopUpdater.error || "" })}
              </p>
              <Show when={desktopUpdater.nativeAvailable && !installing() && Boolean(updateResult()?.needUpdate)}>
                <div class="flex flex-wrap items-center gap-2">
                  <button
                    type="button"
                    onClick={() => void startDesktopUpdate("cdn", true)}
                    class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                  >
                    <Download size={11} />
                    <span>{t("header.updateRetryViaCdn")}</span>
                  </button>
                  <button
                    type="button"
                    onClick={() => void startDesktopUpdate("global", true)}
                    class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                  >
                    <ExternalLink size={11} />
                    <span>{t("header.updateRetryViaGitHub")}</span>
                  </button>
                </div>
              </Show>
            </div>
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
            onClick={closeDesktopUpdateModal}
            class="px-3 py-1.5 text-[12px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            {t("header.updateModalDismiss")}
          </button>

          <div class="flex flex-wrap items-center justify-end gap-2">
            <Show when={desktopUpdater.nativeAvailable}>
              <button
                type="button"
                disabled={installing()}
                onClick={() => void startDesktopUpdate()}
                class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] hover:opacity-90 px-3.5 py-1.5 text-[12px] font-semibold text-[var(--accent-foreground)] shadow-xs transition-opacity cursor-pointer disabled:opacity-50 disabled:cursor-wait"
              >
                <Show when={installing()} fallback={<ArrowUpCircle size={13} />}>
                  <Spinner class="h-3.5 w-3.5" />
                </Show>
                {installing() ? desktopUpdateButtonLabel() : t("header.updateInApp")}
              </button>
            </Show>
            <Show when={acceleratedUrl()}>
              <a
                href={acceleratedUrl()}
                target="_blank"
                rel="noreferrer"
                onClick={(e) => {
                  e.preventDefault();
                  const url = acceleratedUrl();
                  if (url) openExternalURL(url);
                }}
                class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] hover:bg-[var(--bg-hover)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-secondary)] transition-colors cursor-pointer"
              >
                <Download size={12} />
                <span>{t("header.updateModalDownloadCdn")}</span>
              </a>
            </Show>
            <Show when={githubUrl()}>
              <a
                href={githubUrl()}
                target="_blank"
                rel="noreferrer"
                onClick={(e) => {
                  e.preventDefault();
                  const url = githubUrl();
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
                !acceleratedUrl() &&
                !githubUrl() &&
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
  );
}

export default function UpdateModal() {
  return (
    <Presence when={isUpdateModalOpen()}>
      {(state) => <UpdateDialog state={state} />}
    </Presence>
  );
}
