// web/src/components/layout/SavedFileToast.tsx
// Non-intrusive global notification shown after saving/exporting a file,
// displaying the saved path with one-click Reveal in Finder / Folder.
import { Show } from "solid-js";
import { Portal } from "solid-js/web";
import { AlertCircle, Check, FolderOpen, X } from "lucide-solid";
import { t } from "../../i18n";
import {
  dismissSavedFileToast,
  formatSavedDisplayPath,
  pauseSavedFileToastTimer,
  resumeSavedFileToastTimer,
  revealSavedFile,
  savedFileToast,
} from "../../store/workspace";

function isMacOS(): boolean {
  if (typeof navigator === "undefined") return false;
  const plat = (navigator.platform || navigator.userAgent || "").toLowerCase();
  return plat.includes("mac");
}

export default function SavedFileToast() {
  return (
    <Show when={savedFileToast()} keyed>
      {(toast) => {
        const res = toast.result;
        const isError = res.status === "error";
        const nativePath = res.status === "saved" && res.mode === "native" ? res.path : "";
        const label = () => {
          if (res.status === "error") {
            return t("share.saveFailed", { error: res.error });
          }
          if (res.mode === "native") {
            return t("share.savedToPath", { path: formatSavedDisplayPath(res.path) });
          }
          return t("share.savedBrowserDownload", { filename: res.filename });
        };

        return (
          <Portal>
            <div
              role="status"
              aria-live="polite"
              onMouseEnter={pauseSavedFileToastTimer}
              onMouseLeave={resumeSavedFileToastTimer}
              class={`fixed right-4 bottom-4 z-[130] flex max-w-[min(92vw,460px)] items-center gap-2 rounded-xl border px-3 py-2 text-[12px] shadow-2xl backdrop-blur-md ${
                isError
                  ? "border-[var(--danger-border)] bg-[var(--bg-elevated)]/95 text-[var(--danger-text)]"
                  : "border-[var(--ok-border)] bg-[var(--bg-elevated)]/95 text-[var(--text-primary)]"
              }`}
            >
              <span
                class={`shrink-0 ${
                  isError ? "text-[var(--danger-text)]" : "text-[var(--ok-text)]"
                }`}
              >
                {isError ? <AlertCircle size={14} /> : <Check size={14} />}
              </span>
              <span
                class="min-w-0 flex-1 truncate text-[12px]"
                title={nativePath || label()}
              >
                {toast.revealFailed ? t("share.revealFailed") : label()}
              </span>
              <Show when={nativePath}>
                <button
                  type="button"
                  onClick={() => void revealSavedFile(nativePath)}
                  class="inline-flex shrink-0 items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                >
                  <FolderOpen size={12} />
                  <span>{isMacOS() ? t("share.revealInFinder") : t("share.revealInFolder")}</span>
                </button>
              </Show>
              <button
                type="button"
                onClick={dismissSavedFileToast}
                title={t("common.close")}
                aria-label={t("common.close")}
                class="shrink-0 rounded-md p-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
              >
                <X size={13} />
              </button>
            </div>
          </Portal>
        );
      }}
    </Show>
  );
}
