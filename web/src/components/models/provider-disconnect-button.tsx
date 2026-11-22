// web/src/components/models/provider-disconnect-button.tsx
import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { Portal } from "solid-js/web";
import { LoaderCircle, Trash2 } from "lucide-solid";
import { Presence } from "../motion/Presence";
import { useAnchoredPopover, type AnchoredAlign } from "../layout/anchored-popover";
import { t } from "../../i18n";

export default function ProviderDisconnectButton(props: {
  providerName: string;
  variant?: "icon" | "button";
  label?: string;
  confirmTitle?: string;
  confirmBody?: string;
  confirmActionLabel?: string;
  align?: AnchoredAlign;
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
    align: props.align ?? "end",
  });

  const actionLabel = () => props.label || t("models.deleteProvider");
  const dialogTitle = () =>
    props.confirmTitle || t("models.deleteProviderConfirmTitle", { name: props.providerName });
  const dialogBody = () => props.confirmBody || t("models.deleteProviderConfirmBody");
  const confirmBtnText = () => props.confirmActionLabel || actionLabel();

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
        class={
          props.variant === "button"
            ? "inline-flex items-center gap-1.5 rounded-xl border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3 py-1.5 text-[12px] font-medium text-[var(--danger-text)] hover:opacity-90 transition-opacity cursor-pointer disabled:opacity-60 disabled:cursor-default"
            : "rounded-lg p-1.5 text-[var(--text-muted)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)] transition-colors cursor-pointer disabled:opacity-60 disabled:cursor-default"
        }
        title={actionLabel()}
        aria-label={actionLabel()}
        aria-haspopup="dialog"
        aria-expanded={open()}
        aria-busy={props.loading}
      >
        <Show when={!props.loading} fallback={<LoaderCircle size={13} class="animate-spin" />}>
          <Trash2 size={13} />
        </Show>
        <Show when={props.variant === "button"}>
          <span>{actionLabel()}</span>
        </Show>
      </button>

      <Presence when={open()}>
        {(state) => (
          <Portal>
            <div
              ref={setPanelRef}
              data-state={state()}
              role="dialog"
              aria-label={actionLabel()}
              class="hoxi-popover fixed z-[120] w-68 max-w-[calc(100vw-16px)] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-3 shadow-[var(--composer-shadow)]"
              style={{
                ...anchored.positionStyle(),
                "--hoxi-origin": (props.align ?? "end") === "start" ? "top left" : "top right",
              }}
            >
              <div class="text-[12.5px] font-semibold text-[var(--text-primary)]">
                {dialogTitle()}
              </div>
              <p class="mt-1 text-[11.5px] leading-relaxed text-[var(--text-secondary)]">
                {dialogBody()}
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
                  {confirmBtnText()}
                </button>
              </div>
            </div>
          </Portal>
        )}
      </Presence>
    </>
  );
}
