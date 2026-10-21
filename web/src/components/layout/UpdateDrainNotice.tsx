// web/src/components/layout/UpdateDrainNotice.tsx
import { Show } from "solid-js";
import { AlertTriangle } from "lucide-solid";
import { isUpdateDraining, openDesktopUpdateModal } from "../../store/workspace";
import { t } from "../../i18n";

/**
 * Full-width strip shown once Sparkle accepted an install. Admission is closed
 * until relaunch, so any 503 from a new task has a visible cause here instead
 * of only living inside the overflow menu.
 */
export default function UpdateDrainNotice() {
  return (
    <Show when={isUpdateDraining()}>
      <button
        type="button"
        onClick={openDesktopUpdateModal}
        class="shrink-0 flex w-full items-center gap-2 border-b border-amber-500/25 bg-amber-500/10 px-4 py-1.5 text-left text-[11px] font-medium text-amber-600 transition-colors hover:bg-amber-500/15 cursor-pointer dark:text-amber-400"
        style={{ "--wails-draggable": "no-drag" } as any}
      >
        <AlertTriangle size={12} class="shrink-0" />
        <span class="truncate">{t("header.updateRestartRequired")}</span>
        <span class="truncate text-[11px] font-normal opacity-80">
          {t("header.updateDrainingNotice")}
        </span>
      </button>
    </Show>
  );
}
