// web/src/components/layout/WindowControls.tsx
// Windows Fluent-style caption controls (Minimize, Maximize/Restore, Close)
// embedded flush into the top-right edge of frameless desktop headers.

import { Show } from "solid-js";
import {
  closeDesktopWindowAction,
  desktopWindowMaximised,
  hasWindowsWindowControls,
  minimiseDesktopWindowAction,
  toggleDesktopWindowMaximiseAction,
} from "../../store/workspace";
import { t } from "../../i18n";

const NON_DRAG_INTERACTIVE_SELECTOR =
  'button, a, input, textarea, select, [role="button"], [role="menuitem"], [contenteditable="true"], [data-no-window-drag]';

/**
 * Returns true only when double-clicking genuine draggable header blank space on
 * Windows desktop (matching Wails's `--wails-draggable: drag` check and ignoring
 * interactive buttons/menus so rapid toolbar clicks never toggle maximize).
 */
export function shouldToggleWindowMaximiseOnDoubleClick(
  target: EventTarget | null,
  win?: Window,
  nav?: Navigator
): boolean {
  const w = win ?? (typeof window !== "undefined" ? window : undefined);
  if (!w || !hasWindowsWindowControls(w, nav) || !target) return false;

  const node = target as { nodeType?: number; parentElement?: Element | null };
  const el: Element | null =
    node.nodeType === 1
      ? (target as Element)
      : node.parentElement ?? null;
  if (!el) return false;

  if (typeof el.closest === "function" && el.closest(NON_DRAG_INTERACTIVE_SELECTOR)) {
    return false;
  }
  if (typeof w.getComputedStyle !== "function") return false;
  const dragProp = w.getComputedStyle(el).getPropertyValue("--wails-draggable").trim();
  return dragProp === "drag";
}

export function handleFramelessHeaderDoubleClick(ev: MouseEvent): void {
  if (shouldToggleWindowMaximiseOnDoubleClick(ev.target)) {
    toggleDesktopWindowMaximiseAction();
  }
}

interface WindowControlsProps {
  showDivider?: boolean;
}

export default function WindowControls(props: WindowControlsProps = {}) {
  const showControls = hasWindowsWindowControls();

  return (
    <Show when={showControls}>
      <div
        class="inline-flex h-full shrink-0 items-stretch select-none"
        style={{ "--wails-draggable": "no-drag" } as any}
        data-no-window-drag="true"
        onDblClick={(ev) => ev.stopPropagation()}
      >
        <Show when={props.showDivider !== false}>
          <span
            aria-hidden="true"
            class="my-auto mr-0.5 ml-1 h-3.5 w-px shrink-0 bg-[var(--border-subtle)]"
          />
        </Show>

        <button
          type="button"
          onClick={(ev) => {
            ev.currentTarget.blur();
            minimiseDesktopWindowAction();
          }}
          title={t("header.windowMinimize")}
          aria-label={t("header.windowMinimize")}
          class="inline-flex h-full w-[46px] items-center justify-center text-[var(--text-secondary)] transition-colors hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] active:bg-[var(--bg-selected)] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-[var(--border-focus)] cursor-pointer"
        >
          <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true" class="shrink-0">
            <line
              x1="0"
              y1="5"
              x2="10"
              y2="5"
              stroke="currentColor"
              stroke-width="1"
              shape-rendering="crispEdges"
            />
          </svg>
        </button>

        <button
          type="button"
          onClick={(ev) => {
            ev.currentTarget.blur();
            toggleDesktopWindowMaximiseAction();
          }}
          title={desktopWindowMaximised() ? t("header.windowRestore") : t("header.windowMaximize")}
          aria-label={desktopWindowMaximised() ? t("header.windowRestore") : t("header.windowMaximize")}
          class="inline-flex h-full w-[46px] items-center justify-center text-[var(--text-secondary)] transition-colors hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] active:bg-[var(--bg-selected)] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-[var(--border-focus)] cursor-pointer"
        >
          <Show
            when={desktopWindowMaximised()}
            fallback={
              <svg width="10" height="10" viewBox="0 0 10 10" fill="none" aria-hidden="true" class="shrink-0">
                <rect
                  x="0.5"
                  y="0.5"
                  width="9"
                  height="9"
                  stroke="currentColor"
                  stroke-width="1"
                  shape-rendering="crispEdges"
                />
              </svg>
            }
          >
            <svg width="10" height="10" viewBox="0 0 10 10" fill="none" aria-hidden="true" class="shrink-0">
              <path
                d="M2.5 2.5V0.5H9.5V7.5H7.5"
                stroke="currentColor"
                stroke-width="1"
                shape-rendering="crispEdges"
              />
              <rect
                x="0.5"
                y="2.5"
                width="7"
                height="7"
                stroke="currentColor"
                stroke-width="1"
                shape-rendering="crispEdges"
              />
            </svg>
          </Show>
        </button>

        <button
          type="button"
          onClick={(ev) => {
            ev.currentTarget.blur();
            void closeDesktopWindowAction();
          }}
          title={t("header.windowClose")}
          aria-label={t("header.windowClose")}
          class="inline-flex h-full w-[46px] items-center justify-center text-[var(--text-secondary)] transition-colors hover:bg-[#e81123] hover:text-white active:bg-[#c50f1f] active:text-white focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-[#e81123] cursor-pointer"
        >
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" aria-hidden="true" class="shrink-0">
            <path
              d="M0.75 0.75L9.25 9.25M9.25 0.75L0.75 9.25"
              stroke="currentColor"
              stroke-width="1.05"
              stroke-linecap="square"
            />
          </svg>
        </button>
      </div>
    </Show>
  );
}
