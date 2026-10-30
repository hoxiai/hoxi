// web/src/components/models/provider-disconnect-button.tsx
import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { Portal } from "solid-js/web";
import { LoaderCircle, Trash2 } from "lucide-solid";
import { Presence } from "../motion/Presence";
import { useAnchoredPopover } from "../layout/anchored-popover";
import { t } from "../../i18n";

export default function ProviderDisconnectButton(props: {
  providerName: string;
  disabled?: boolean;
  loading: boolean;
  onConfirm: () => void;
}) {
  const [open, setOpen] = createSignal(false);
  let buttonRef: HTMLButtonElement | undefined;
  const [panelRef, setPanelRef] = createSignal<HTMLDivElement | undefined>();
  const anchored = useAnchoredPopover({
    anchor: () => buttonRef,
    panel: () => (open() ? panelRef() : undefined),
    align: "end",
  });

  createEffect(() => {
    if (props.disabled || props.loading) setOpen(false);
  });

  createEffect(() => {
    if (!open()) return;
    const handlePointerDown = (ev: MouseEvent) => {
      const target = ev.target as Node;
      const panel = panelRef();
      if (panel && !panel.contains(target) && buttonRef && !buttonRef.contains(target)) {
        setOpen(false);
      }
    };
    const handleKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") setOpen(false);
    };
    window.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      window.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        disabled={props.disabled || props.loading}
        onClick={() => setOpen((v) => !v)}
        class="rounded-lg p-1.5 text-[var(--text-faint)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)] transition-colors cursor-pointer disabled:opacity-60 disabled:cursor-default"
        title={t("models.disconnect")}
        aria-haspopup="dialog"
        aria-expanded={open()}
        aria-busy={props.loading}
      >
        <Show when={!props.loading} fallback={<LoaderCircle size={13} class="animate-spin" />}>
          <Trash2 size={13} />
        </Show>
      </button>

      <Presence when={open()}>
        {(state) => (
          <Portal>
            <div
              ref={setPanelRef}
              data-state={state()}
              role="dialog"
              aria-label={t("models.disconnect")}
              class="hoxi-popover fixed z-[120] w-64 max-w-[calc(100vw-16px)] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-3 shadow-[var(--composer-shadow)]"
              style={{ ...anchored.positionStyle(), "--hoxi-origin": "top right" }}
            >
              <div class="text-[12.5px] font-semibold text-[var(--text-primary)]">
                {t("models.disconnectConfirmTitle", { name: props.providerName })}
              </div>
              <p class="mt-1 text-[11.5px] leading-relaxed text-[var(--text-secondary)]">
                {t("models.disconnectConfirmBody")}
              </p>
              <div class="mt-3 flex justify-end gap-1.5">
                <button
                  type="button"
                  onClick={() => setOpen(false)}
                  class="hoxi-press rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[12px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                >
                  {t("common.cancel")}
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setOpen(false);
                    props.onConfirm();
                  }}
                  class="hoxi-press rounded-lg bg-[var(--danger-surface)] px-2.5 py-1 text-[12px] font-semibold text-[var(--danger-text)] border border-[var(--danger-border)] hover:opacity-90 transition-opacity cursor-pointer"
                >
                  {t("models.disconnect")}
                </button>
              </div>
            </div>
          </Portal>
        )}
      </Presence>
    </>
  );
}