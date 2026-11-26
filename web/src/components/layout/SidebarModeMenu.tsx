// web/src/components/layout/SidebarModeMenu.tsx
import { createEffect, createSignal, For, onCleanup, Show } from "solid-js";
import { Activity, ArrowRightLeft, Bell, Check, Folder, Globe, Layers } from "lucide-solid";
import { Presence } from "../motion/Presence";
import { t } from "../../i18n";
import {
  setSidebarProjectMode,
  workspace,
  type SidebarProjectMode,
} from "../../store/workspace";
import { surfaceExtension } from "./surface-extension";

export default function SidebarModeMenu(props: {
  pendingApprovalCount: () => number;
}) {
  const [open, setOpen] = createSignal(false);
  let btnRef: HTMLButtonElement | undefined;
  let menuRef: HTMLDivElement | undefined;

  createEffect(() => {
    if (!open()) return;
    const handlePointerDown = (e: MouseEvent) => {
      const target = e.target as Node;
      if (menuRef && !menuRef.contains(target) && btnRef && !btnRef.contains(target)) {
        setOpen(false);
      }
    };
    window.addEventListener("pointerdown", handlePointerDown);
    onCleanup(() => window.removeEventListener("pointerdown", handlePointerDown));
  });

  const extraView = () => surfaceExtension.codeSidebarExtraView;
  const extraActive = () => Boolean(extraView()?.active());
  const extraBadge = () => extraView()?.badgeCount?.() ?? 0;

  const modeOptions: Array<{
    id: SidebarProjectMode;
    label: () => string;
    icon: typeof Folder;
  }> = [
    { id: "multi", label: () => t("leftNav.multiProject"), icon: Layers },
    { id: "single", label: () => t("leftNav.singleProject"), icon: Folder },
    { id: "attention", label: () => t("leftNav.attentionTasks"), icon: Bell },
    { id: "running", label: () => t("leftNav.runningTasks"), icon: Activity },
  ];

  return (
    <div class="relative flex items-center">
      <button
        ref={btnRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        class={`relative inline-flex h-[26px] w-[26px] shrink-0 items-center justify-center rounded-lg transition-colors cursor-pointer ${
          extraActive()
            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
            : open()
              ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
              : "text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
        }`}
        title={t("leftNav.switchSidebarMode")}
      >
        <ArrowRightLeft size={14} class="block shrink-0" />
        <Show when={!extraActive() && extraBadge() > 0}>
          <span class="absolute top-1 right-1 h-1.5 w-1.5 rounded-full bg-[var(--accent-primary)]" />
        </Show>
      </button>

      <Presence when={open()}>
        {(state) => (
          <div
            ref={menuRef}
            data-state={state()}
            style={{ "--hoxi-origin": "top right" }}
            class="hoxi-popover absolute right-0 top-full mt-1 w-48 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1 z-50 space-y-0.5"
          >
            <For each={modeOptions}>
              {(opt) => {
                const OptIcon = opt.icon;
                const isCurrentMode = () =>
                  !extraActive() &&
                  workspace.sidebarNavTab === "projects" &&
                  workspace.sidebarProjectMode === opt.id;
                return (
                  <button
                    type="button"
                    onClick={() => {
                      extraView()?.exit();
                      setSidebarProjectMode(opt.id);
                      setOpen(false);
                    }}
                    class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-[12px] transition-colors cursor-pointer ${
                      isCurrentMode()
                        ? "bg-[var(--accent-surface)] text-[var(--text-primary)] font-medium"
                        : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    <span class="inline-flex items-center gap-2">
                      <OptIcon size={13} class="text-[var(--text-muted)]" />
                      <span>{opt.label()}</span>
                    </span>
                    <span class="inline-flex items-center gap-1.5">
                      <Show when={opt.id === "attention" && props.pendingApprovalCount() > 0}>
                        <span class="rounded-full bg-[var(--status-warning)]/15 px-1.5 py-0.2 font-mono text-[10.5px] font-semibold text-[var(--status-warning)]">
                          {props.pendingApprovalCount()}
                        </span>
                      </Show>
                      <Show when={isCurrentMode()}>
                        <Check size={13} class="text-[var(--accent-primary)]" />
                      </Show>
                    </span>
                  </button>
                );
              }}
            </For>

            <Show when={extraView()}>
              {(extra) => (
                <>
                  <div class="my-1 border-t border-[var(--border-subtle)]/80" />
                  <button
                    type="button"
                    onClick={() => {
                      extra().enter();
                      setOpen(false);
                    }}
                    class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-[12px] transition-colors cursor-pointer ${
                      extra().active()
                        ? "bg-[var(--accent-surface)] text-[var(--text-primary)] font-medium"
                        : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    <span class="inline-flex items-center gap-2 min-w-0">
                      <Globe size={13} class="shrink-0 text-[var(--accent-primary)]" />
                      <span class="truncate">{extra().label()}</span>
                    </span>
                    <span class="inline-flex shrink-0 items-center gap-1.5">
                      <Show when={extra().online?.()}>
                        <span class="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                      </Show>
                      <Show when={(extra().badgeCount?.() ?? 0) > 0}>
                        <span class="rounded-full bg-[var(--accent-primary)] px-1.5 py-0.2 font-mono text-[10px] font-semibold text-[var(--accent-foreground)]">
                          {extra().badgeCount?.()}
                        </span>
                      </Show>
                      <Show when={extra().active()}>
                        <Check size={13} class="text-[var(--accent-primary)]" />
                      </Show>
                    </span>
                  </button>
                </>
              )}
            </Show>
          </div>
        )}
      </Presence>
    </div>
  );
}
