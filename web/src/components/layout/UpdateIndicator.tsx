// web/src/components/layout/UpdateIndicator.tsx
import { Show } from "solid-js";
import { ArrowUpCircle } from "lucide-solid";
import {
  desktopUpdateButtonLabel,
  desktopUpdater,
  hasDesktopUpdate,
  isDesktopUpdateInstalling,
  isUpdateDraining,
  openDesktopUpdateModal,
} from "../../store/workspace";
import { Spinner } from "../icons";
import { t } from "../../i18n";

/**
 * Header-level update affordance. Renders nothing in the steady state, so it
 * adds no width until a release is actually available. Once Sparkle accepts the
 * install, the full-width drain strip takes over and this yields to it.
 */
export default function UpdateIndicator() {
  const latestVersion = () => `v${desktopUpdater.result?.latestVersion || ""}`;

  const title = () =>
    isDesktopUpdateInstalling()
      ? desktopUpdateButtonLabel()
      : t("header.updateAvailableTooltip", { version: latestVersion() });

  return (
    <Show when={hasDesktopUpdate() && !isUpdateDraining()}>
      <button
        type="button"
        onClick={openDesktopUpdateModal}
        class="hoxi-press inline-flex h-7 shrink-0 items-center gap-1.5 rounded-lg bg-[var(--accent-surface)] px-2 text-[11px] font-medium text-[var(--accent-text)] transition-colors hover:bg-[var(--bg-hover)] cursor-pointer"
        style={{ "--wails-draggable": "no-drag" } as any}
        title={title()}
        aria-label={title()}
      >
        <Show when={isDesktopUpdateInstalling()} fallback={<ArrowUpCircle size={13} class="shrink-0" />}>
          <Spinner class="h-3 w-3 shrink-0" />
        </Show>
        <span class="max-w-[140px] truncate">
          {isDesktopUpdateInstalling()
            ? desktopUpdateButtonLabel()
            : t("header.updateAvailableBadge", { version: latestVersion() })}
        </span>
      </button>
    </Show>
  );
}
