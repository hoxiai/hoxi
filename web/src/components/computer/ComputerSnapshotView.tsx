import { createMemo, For, Show } from "solid-js";
import {
  ChevronDown,
  ChevronRight,
  Copy,
  Crosshair,
  Lock,
  Maximize2,
  Search,
} from "lucide-solid";
import type {
  ComputerElement,
  ComputerSnapshot,
  ComputerWindow,
} from "../../services/computer";
import {
  openImagePreview,
  resolveElementWindowBounds,
} from "../../store/workspace";
import { t } from "../../i18n";

export interface ComputerSnapshotViewProps {
  snapshot: ComputerSnapshot;
  activeWindow: ComputerWindow | null;
  windowID: string;
  onWindowChange: (windowID: string) => void;
  cursorPoint: { x: number; y: number } | null;
  onCursorPoint: (pt: { x: number; y: number } | null) => void;
  hoveredElement: ComputerElement | null;
  hoveredElementID: number | null;
  onHoveredElementID: (id: number | null) => void;
  elementFilter: string;
  onElementFilter: (value: string) => void;
  filteredElements: ComputerElement[];
  showElements: boolean;
  onToggleElements: () => void;
  onCopy: (text: string) => void;
}

export default function ComputerSnapshotView(props: ComputerSnapshotViewProps) {
  const hoveredBoxStyle = createMemo(() => {
    const el = props.hoveredElement;
    const win = props.activeWindow;
    if (!el || !win || win.bounds.width <= 0 || win.bounds.height <= 0) return null;
    const wb = resolveElementWindowBounds(el, win.bounds);
    if (!wb || wb.width <= 0 || wb.height <= 0) return null;
    const left = Math.max(0, Math.min(100, (wb.x / win.bounds.width) * 100));
    const top = Math.max(0, Math.min(100, (wb.y / win.bounds.height) * 100));
    const width = Math.max(0.5, Math.min(100 - left, (wb.width / win.bounds.width) * 100));
    const height = Math.max(0.5, Math.min(100 - top, (wb.height / win.bounds.height) * 100));
    return {
      left: `${left}%`,
      top: `${top}%`,
      width: `${width}%`,
      height: `${height}%`,
    };
  });

  const handlePreviewMouseMove = (e: MouseEvent & { currentTarget: HTMLDivElement }) => {
    const rect = e.currentTarget.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) return;
    const win = props.activeWindow;
    const snap = props.snapshot;
    const targetW = win?.bounds.width || snap.width || rect.width;
    const targetH = win?.bounds.height || snap.height || rect.height;
    const x = Math.max(
      0,
      Math.min(Math.round(targetW - 1), Math.round(((e.clientX - rect.left) / rect.width) * targetW))
    );
    const y = Math.max(
      0,
      Math.min(Math.round(targetH - 1), Math.round(((e.clientY - rect.top) / rect.height) * targetH))
    );
    props.onCursorPoint({ x, y });
  };

  const elementCount = () => props.snapshot.elements?.length || 0;

  return (
    <div class="space-y-2.5">
      {/* Target Window Selector (dropdown only when multiple windows exist) */}
      <Show
        when={props.snapshot.windows.length > 1}
        fallback={
          <Show when={props.activeWindow}>
            {(win) => (
              <div class="flex items-center justify-between gap-2 px-0.5 text-[11px] text-[var(--text-muted)]">
                <span class="truncate">{win().title || win().id}</span>
                <span class="shrink-0 font-mono text-[10.5px] text-[var(--text-faint)]">
                  {Math.round(win().bounds.width)}×{Math.round(win().bounds.height)}
                </span>
              </div>
            )}
          </Show>
        }
      >
        <select
          class="w-full h-7 rounded-md border border-transparent bg-[var(--bg-inset)] px-2 text-[11.5px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-border)]"
          aria-label={t("computer.targetWindow")}
          value={props.windowID || props.snapshot.windowID}
          onChange={(event) => props.onWindowChange(event.currentTarget.value)}
        >
          <For each={props.snapshot.windows}>
            {(window) => (
              <option
                value={window.id}
                selected={window.id === (props.windowID || props.snapshot.windowID)}
              >
                {window.title || window.id} ({Math.round(window.bounds.width)}×
                {Math.round(window.bounds.height)})
              </option>
            )}
          </For>
        </select>
      </Show>

      {/* Screenshot Preview with Hover Coordinate Pill & Element Overlay */}
      <Show when={props.snapshot.image}>
        <div
          class="group/preview relative overflow-hidden rounded-xl bg-[var(--bg-inset)] cursor-crosshair select-none"
          onMouseMove={handlePreviewMouseMove}
          onMouseLeave={() => props.onCursorPoint(null)}
          onClick={() => {
            const pt = props.cursorPoint;
            if (pt) props.onCopy(`(${pt.x}, ${pt.y})`);
          }}
          title={t("computer.clickToCopyCoords")}
        >
          <img
            class="block h-auto w-full"
            alt={t("computer.preview")}
            src={`data:image/jpeg;base64,${props.snapshot.image}`}
          />

          {/* Highlighted Element Bounding Box */}
          <Show when={hoveredBoxStyle()}>
            {(style) => (
              <div
                class="pointer-events-none absolute border-2 border-[var(--accent-primary)] bg-[var(--accent-primary)]/15 rounded-sm transition-all duration-75"
                style={style()}
              >
                <Show when={props.hoveredElement}>
                  {(el) => (
                    <span class="absolute -top-5 left-0 whitespace-nowrap rounded bg-[var(--accent-primary)] px-1.5 py-0.5 font-mono text-[10px] font-semibold text-white shadow">
                      #{el().id} {el().role}
                    </span>
                  )}
                </Show>
              </div>
            )}
          </Show>

          {/* Hover Coordinate Overlay */}
          <Show when={props.cursorPoint}>
            {(pt) => (
              <span class="pointer-events-none absolute bottom-2 left-2 inline-flex items-center gap-1 rounded bg-black/65 px-1.5 py-0.5 font-mono text-[10px] text-white">
                <Crosshair size={10} />
                <span>
                  ({pt().x}, {pt().y})
                </span>
              </span>
            )}
          </Show>

          {/* Fullscreen Zoom Button */}
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              openImagePreview({
                src: `data:image/jpeg;base64,${props.snapshot.image}`,
                alt: t("computer.preview"),
              });
            }}
            title={t("computer.zoomPreview")}
            aria-label={t("computer.zoomPreview")}
            class="absolute right-2 top-2 inline-flex h-6 w-6 items-center justify-center rounded-md bg-black/60 text-white opacity-0 group-hover/preview:opacity-100 hover:bg-black/80 transition-opacity cursor-pointer"
          >
            <Maximize2 size={12} />
          </button>
        </div>
      </Show>

      {/* Collapsible Accessible Elements Section */}
      <div class="rounded-xl bg-[var(--bg-inset)]/60">
        <Show
          when={elementCount() > 0}
          fallback={
            <div class="px-2.5 py-2 text-[11.5px] text-[var(--text-muted)]">
              {t("computer.elementCount", { count: 0 })}
            </div>
          }
        >
          <button
            type="button"
            class="flex w-full items-center justify-between gap-2 px-2.5 py-2 text-[11.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
            aria-expanded={props.showElements}
            onClick={() => props.onToggleElements()}
          >
            <span class="inline-flex items-center gap-1">
              <Show when={props.showElements} fallback={<ChevronRight size={13} />}>
                <ChevronDown size={13} />
              </Show>
              <span>{t("computer.elementCount", { count: elementCount() })}</span>
            </span>
          </button>
        </Show>

        <Show when={props.showElements && elementCount() > 0}>
          <div class="space-y-2 px-2.5 pb-2.5">
            <Show when={elementCount() > 5 || props.elementFilter.trim().length > 0}>
              <div class="relative">
                <Search
                  size={12}
                  class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--text-faint)]"
                />
                <input
                  type="text"
                  value={props.elementFilter}
                  onInput={(e) => props.onElementFilter(e.currentTarget.value)}
                  placeholder={t("computer.filterElementsPlaceholder")}
                  class="w-full rounded-md border border-transparent bg-[var(--bg-panel)] pl-7 pr-2.5 py-1 text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)]"
                />
              </div>
            </Show>

            <div class="max-h-60 space-y-1 overflow-y-auto pr-0.5">
              <Show
                when={props.filteredElements.length > 0}
                fallback={
                  <div class="py-3 text-center text-[11px] text-[var(--text-faint)]">
                    {t("computer.noMatchingElements")}
                  </div>
                }
              >
                <For each={props.filteredElements}>
                  {(el) => {
                    const wb = () =>
                      resolveElementWindowBounds(el, props.activeWindow?.bounds);
                    return (
                      <div
                        onMouseEnter={() => props.onHoveredElementID(el.id)}
                        onMouseLeave={() => {
                          if (props.hoveredElementID === el.id) {
                            props.onHoveredElementID(null);
                          }
                        }}
                        onClick={() => props.onCopy(`elementID: ${el.id}`)}
                        title={t("computer.copyElementTarget", { id: el.id })}
                        class={`flex items-center justify-between gap-2 rounded-md border px-2 py-1 text-[11px] transition-colors cursor-pointer ${
                          props.hoveredElementID === el.id
                            ? "border-[var(--accent-border)] bg-[var(--accent-surface)]/40"
                            : "border-transparent bg-[var(--bg-inset)]/60 hover:bg-[var(--bg-hover)]"
                        }`}
                      >
                        <div class="flex items-center gap-1.5 min-w-0 flex-1">
                          <span class="shrink-0 rounded bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[10px] font-semibold text-[var(--accent-primary)]">
                            #{el.id}
                          </span>
                          <span class="shrink-0 font-mono text-[10.5px] text-[var(--text-muted)]">
                            {el.role}
                          </span>
                          <Show
                            when={!el.secure}
                            fallback={
                              <span class="inline-flex items-center gap-1 rounded bg-[var(--warn-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--warn-text)]">
                                <Lock size={9} />
                                <span>{t("computer.secureElement")}</span>
                              </span>
                            }
                          >
                            <Show when={el.name || el.value}>
                              <span class="truncate text-[var(--text-primary)]">
                                {el.name || el.value}
                              </span>
                            </Show>
                          </Show>
                          <Show when={el.focused}>
                            <span class="shrink-0 rounded bg-[var(--accent-surface)] px-1 py-0.5 text-[9.5px] font-medium text-[var(--accent-primary)]">
                              {t("computer.focusedElement")}
                            </span>
                          </Show>
                          <Show when={el.disabled}>
                            <span class="shrink-0 rounded bg-[var(--bg-panel)] px-1 py-0.5 text-[9.5px] text-[var(--text-faint)]">
                              {t("computer.disabledElement")}
                            </span>
                          </Show>
                        </div>
                        <div class="flex items-center gap-1.5 shrink-0 font-mono text-[10px] text-[var(--text-faint)]">
                          <Show when={wb()}>
                            {(b) => (
                              <span>
                                ({Math.round(b().x + b().width / 2)},{" "}
                                {Math.round(b().y + b().height / 2)})
                              </span>
                            )}
                          </Show>
                          <Copy size={10} class="opacity-60" />
                        </div>
                      </div>
                    );
                  }}
                </For>
              </Show>
            </div>
          </div>
        </Show>
      </div>
    </div>
  );
}
