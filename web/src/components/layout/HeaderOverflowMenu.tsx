import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  BarChart3,
  ChevronLeft,
  ChevronRight,
  MoreHorizontal,
  Palette,
  RefreshCw,
  Settings,
  Sparkles,
} from "lucide-solid";
import {
  desktopUpdater,
  hasDesktopUpdate,
  isDailyPulseSyncing,
  isUpdateDraining,
  openDesktopUpdateModal,
  openSettingsModal,
  refreshDailyDevPulse,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import ThemeStyleContent from "./ThemeStyleContent";
import DevPulseContent from "./DevPulseContent";

type Panel = "appearance" | "pulse" | null;

export default function HeaderOverflowMenu() {
  const [open, setOpen] = createSignal(false);
  const [panel, setPanel] = createSignal<Panel>(null);
  let triggerRef: HTMLButtonElement | undefined;
  let menuRef: HTMLDivElement | undefined;

  const close = () => {
    setOpen(false);
    setPanel(null);
  };

  const hasUpdate = () => hasDesktopUpdate() || isUpdateDraining();

  createEffect(() => {
    if (!open()) return;
    const handlePointerDown = (ev: MouseEvent) => {
      const target = ev.target as Node;
      if (
        menuRef && !menuRef.contains(target) &&
        triggerRef && !triggerRef.contains(target)
      ) {
        close();
      }
    };
    const handleKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        if (panel()) {
          setPanel(null);
        } else {
          close();
        }
      }
    };
    window.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      window.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  const rowClass =
    "w-full flex items-center gap-2.5 rounded-xl px-2.5 py-2 text-left text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer";

  return (
    <div class="relative inline-flex items-center">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => (open() ? close() : setOpen(true))}
        class={`hoxi-press relative inline-flex items-center justify-center w-7 h-7 rounded-lg transition-colors cursor-pointer ${
          open()
            ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
            : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
        }`}
        style={{ "--wails-draggable": "no-drag" } as any}
        title={t("header.moreTooltip")}
        aria-label={t("header.moreTooltip")}
        aria-haspopup="menu"
        aria-expanded={open()}
      >
        <MoreHorizontal size={16} />
        <Show when={hasUpdate()}>
          <span class="absolute right-1 top-1 h-1.5 w-1.5 rounded-full bg-[var(--accent-primary)]" />
        </Show>
      </button>

      <Presence when={open()}>
        {(state) => (
          <div
            ref={menuRef}
            data-state={state()}
            role="menu"
            style={{ "--hoxi-origin": "top right" }}
            class="hoxi-popover absolute right-0 top-full mt-2 z-50 w-[252px] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-xl"
          >
            <Show
              when={panel()}
              fallback={
                <div class="space-y-0.5">
                  <Show when={!workspace.connected}>
                    <div class="flex items-center gap-2 px-2.5 py-2 text-[11px] text-[var(--text-muted)]">
                      <span class="inline-block h-1.5 w-1.5 rounded-full bg-[var(--warn)] animate-pulse" />
                      <span>{t("common.connecting")}</span>
                    </div>
                  </Show>

                  <Show when={hasUpdate()}>
                    <button
                      type="button"
                      onClick={() => {
                        close();
                        openDesktopUpdateModal();
                      }}
                      class="w-full flex items-center gap-2.5 rounded-xl px-2.5 py-2 text-left text-[12px] font-medium text-[var(--accent-text)] bg-[var(--accent-surface)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                    >
                      <Sparkles size={14} class="shrink-0" />
                      <span class="flex-1 min-w-0 truncate">
                        {isUpdateDraining()
                          ? t("header.updateRestartRequired")
                          : t("header.updateAvailableBadge", {
                              version: `v${desktopUpdater.result?.latestVersion || ""}`,
                            })}
                      </span>
                    </button>
                  </Show>

                  <button type="button" onClick={() => setPanel("appearance")} class={rowClass}>
                    <Palette size={14} class="shrink-0" />
                    <span class="flex-1 min-w-0 truncate">{t("header.appearanceTooltip")}</span>
                    <ChevronRight size={13} class="shrink-0 text-[var(--text-muted)]" />
                  </button>

                  <button type="button" onClick={() => setPanel("pulse")} class={rowClass}>
                    <BarChart3 size={14} class="shrink-0 text-[var(--accent-primary)]" />
                    <span class="flex-1 min-w-0 truncate">{t("header.pulseTitle")}</span>
                    <ChevronRight size={13} class="shrink-0 text-[var(--text-muted)]" />
                  </button>

                  <div class="my-1 border-t border-[var(--border-subtle)]" />

                  <button
                    type="button"
                    onClick={() => {
                      close();
                      openSettingsModal("general");
                    }}
                    class={rowClass}
                  >
                    <Settings size={14} class="shrink-0" />
                    <span class="flex-1 min-w-0 truncate">{t("commandPalette.actionOpenSettings")}</span>
                  </button>
                </div>
              }
            >
              {(current) => (
                <div>
                  <div class="mb-1 flex items-center gap-1 px-1">
                    <button
                      type="button"
                      onClick={() => setPanel(null)}
                      class="inline-flex h-6 w-6 items-center justify-center rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                      aria-label={t("header.moreBack")}
                      title={t("header.moreBack")}
                    >
                      <ChevronLeft size={14} />
                    </button>
                    <span class="text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
                      {current() === "appearance" ? t("header.appearanceTitle") : t("header.pulseTitle")}
                    </span>
                    <Show when={current() === "pulse"}>
                      <button
                        type="button"
                        onClick={() => void refreshDailyDevPulse(true)}
                        class="ml-auto inline-flex h-6 w-6 items-center justify-center rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                        title={t("header.pulseRefreshTooltip")}
                        aria-label={t("header.pulseRefreshTooltip")}
                      >
                        <RefreshCw
                          size={12}
                          class={isDailyPulseSyncing() ? "animate-spin text-[var(--accent-primary)]" : ""}
                        />
                      </button>
                    </Show>
                  </div>

                  <div class="px-1.5 pb-1.5">
                    <Show
                      when={current() === "appearance"}
                      fallback={<DevPulseContent onNavigate={close} />}
                    >
                      <ThemeStyleContent />
                    </Show>
                  </div>
                </div>
              )}
            </Show>
          </div>
        )}
      </Presence>
    </div>
  );
}
