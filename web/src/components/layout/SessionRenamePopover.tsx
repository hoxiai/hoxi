import { createSignal, onCleanup, onMount } from "solid-js";
import { Portal } from "solid-js/web";
import { t } from "../../i18n";

export function SessionRenamePopover(props: {
  anchor: HTMLElement;
  title: string;
  onSave: (title: string) => void;
  onClose: () => void;
}) {
  const [draft, setDraft] = createSignal(props.title);
  const [position, setPosition] = createSignal({ top: 0, left: 0 });
  let panel!: HTMLFormElement;
  let input!: HTMLInputElement;

  onMount(() => {
    const updatePosition = () => {
      const rect = props.anchor.getBoundingClientRect();
      const top = rect.bottom + 6;
      setPosition({
        left: Math.max(8, Math.min(rect.left, window.innerWidth - panel.offsetWidth - 8)),
        top: Math.max(8, top + panel.offsetHeight <= window.innerHeight - 8
          ? top : rect.top - panel.offsetHeight - 6),
      });
    };
    const onPointerDown = (ev: PointerEvent) => {
      if (!panel.contains(ev.target as Node)) props.onClose();
    };
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape" && !ev.isComposing) {
        ev.preventDefault();
        ev.stopPropagation();
        props.onClose();
      }
    };
    updatePosition();
    input.focus();
    input.select();
    window.addEventListener("pointerdown", onPointerDown);
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    onCleanup(() => {
      window.removeEventListener("pointerdown", onPointerDown);
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
    });
  });

  return (
    <Portal>
      <form
        ref={panel}
        role="dialog"
        aria-label={t("leftNav.renameSessionTooltip")}
        class="fixed z-[100] w-72 max-w-[calc(100vw-16px)] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-3 shadow-xl"
        style={{ top: `${position().top}px`, left: `${position().left}px` }}
        onClick={(ev) => ev.stopPropagation()}
        onDblClick={(ev) => ev.stopPropagation()}
        onPointerDown={(ev) => ev.stopPropagation()}
        onSubmit={(ev) => {
          ev.preventDefault();
          const title = draft().trim();
          if (title) props.onSave(title);
        }}
      >
        <label class="block text-[12px] font-medium text-[var(--text-primary)]">
          {t("leftNav.renameSessionTooltip")}
          <input
            ref={input}
            type="text"
            value={draft()}
            placeholder={t("leftNav.renamePlaceholder")}
            onInput={(ev) => setDraft(ev.currentTarget.value)}
            onKeyDown={(ev) => {
              if (ev.key === "Enter" && ev.isComposing) ev.preventDefault();
            }}
            class="mt-2 w-full rounded-lg border border-[var(--accent-border)] bg-[var(--bg-panel)] px-2.5 py-2 text-[13px] text-[var(--text-primary)] outline-none focus:ring-1 focus:ring-[var(--accent-border)]"
          />
        </label>
        <div class="mt-3 flex justify-end gap-2">
          <button type="button" onClick={() => props.onClose()} class="rounded-md px-2.5 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]">
            {t("common.cancel")}
          </button>
          <button type="submit" disabled={!draft().trim()} class="rounded-md bg-[var(--accent-surface)] px-2.5 py-1 text-[12px] font-medium text-[var(--accent-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-40">
            {t("common.save")}
          </button>
        </div>
      </form>
    </Portal>
  );
}
