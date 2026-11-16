import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { Portal } from "solid-js/web";
import { browserExternalURL, type BrowserMenu } from "../../store/workspace";
import { t } from "../../i18n";

interface Props {
  menu: BrowserMenu;
  onClose: () => void;
  onCopy: () => void;
  onCopyLink: () => void;
  onPaste: () => void;
  onOpen: (link: boolean) => void;
  onPageMenu: () => void;
  onKeyboardFocus: () => void;
}

export default function BrowserContextMenu(props: Props) {
  let root!: HTMLDivElement;
  const [position, setPosition] = createSignal({ left: props.menu.clientX, top: props.menu.clientY });
  const item = "flex w-full items-center rounded px-3 py-1.5 text-left text-xs hover:bg-[var(--bg-hover)] focus:bg-[var(--bg-hover)] focus:outline-none disabled:opacity-40 disabled:pointer-events-none cursor-pointer";

  onMount(() => {
    const bounds = root.getBoundingClientRect();
    setPosition({ left: Math.max(4, Math.min(props.menu.clientX, window.innerWidth - bounds.width - 4)),
      top: Math.max(4, Math.min(props.menu.clientY, window.innerHeight - 280)) });
    root.focus({ preventScroll: true });
    const outside = (event: PointerEvent) => { if (!root.contains(event.target as Node)) props.onClose(); };
    const dismiss = () => props.onClose();
    document.addEventListener("pointerdown", outside, true);
    window.addEventListener("blur", dismiss);
    window.addEventListener("resize", dismiss);
    onCleanup(() => {
      document.removeEventListener("pointerdown", outside, true);
      window.removeEventListener("blur", dismiss);
      window.removeEventListener("resize", dismiss);
    });
  });

  function keyboard(event: KeyboardEvent) {
    event.stopPropagation();
    if (event.key === "Escape" || event.key === "Tab") {
      event.preventDefault(); props.onClose(); props.onKeyboardFocus(); return;
    }
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const buttons = Array.from(root.querySelectorAll<HTMLButtonElement>("button:not(:disabled)"));
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 :
      event.key === "ArrowDown" ? (index + 1) % buttons.length : (index <= 0 ? buttons.length : index) - 1;
    buttons[next]?.focus();
  }

  function invoke(action: () => void) { action(); props.onKeyboardFocus(); }

  return <Portal>
    <div ref={root} role="menu" tabIndex={-1} aria-label={t("inspector.browser.contextMenu")}
      class="fixed z-[100] min-w-52 max-w-[calc(100vw-8px)] max-h-[calc(100vh-8px)] overflow-y-auto rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-1 text-[var(--text-primary)] shadow-xl outline-none"
      style={{ left: `${position().left}px`, top: `${position().top}px` }}
      onKeyDown={keyboard} onContextMenu={event => event.preventDefault()}>
      <Show when={props.menu.context} fallback={<p role="status" class="px-3 py-1.5 text-xs text-[var(--text-muted)]">{t("inspector.browser.contextLoading")}</p>}>
        <button role="menuitem" class={item} disabled={!props.menu.context?.canCopy} onClick={() => invoke(props.onCopy)}>{t("inspector.browser.copySelection")}</button>
        <button role="menuitem" class={item} disabled={!props.menu.context?.editable} onClick={() => invoke(props.onPaste)}>{t("inspector.browser.paste")}</button>
        <Show when={browserExternalURL(props.menu.context?.linkURL || "")}>
          <button role="menuitem" class={item} onClick={() => invoke(props.onCopyLink)}>{t("inspector.browser.copyLink")}</button>
          <button role="menuitem" class={item} onClick={() => invoke(() => props.onOpen(true))}>{t("inspector.browser.openLinkExternal")}</button>
        </Show>
      </Show>
      <div role="separator" class="my-1 border-t border-[var(--border-subtle)]" />
      <button role="menuitem" class={item} disabled={!browserExternalURL(props.menu.pageURL)} onClick={() => invoke(() => props.onOpen(false))}>{t("inspector.browser.openInExternal")}</button>
      <button role="menuitem" class={item} onClick={() => invoke(props.onPageMenu)}>{t("inspector.browser.pageContextMenu")}</button>
    </div>
  </Portal>;
}
