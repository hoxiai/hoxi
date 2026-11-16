// web/src/components/models/CandidateModelPicker.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import { Portal } from "solid-js/web";
import { Check, ChevronDown, Cpu, Search, X } from "lucide-solid";
import { Presence } from "../motion/Presence";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";
import { rankModelSearch, type FlattenedModel } from "../../store/workspace";

interface PopoverBox {
  left: number;
  top?: number;
  bottom?: number;
  width: number;
  maxHeight: number;
}

const RENDER_STEP = 40;

export default function CandidateModelPicker(props: {
  models: FlattenedModel[];
  value: string;
  onChange: (value: string) => void;
}) {
  const [open, setOpen] = createSignal(false);
  const [query, setQuery] = createSignal("");
  const [activeIndex, setActiveIndex] = createSignal(0);
  const [renderLimit, setRenderLimit] = createSignal(RENDER_STEP);
  const [popoverBox, setPopoverBox] = createSignal<PopoverBox>({
    left: 16,
    top: 0,
    width: 360,
    maxHeight: 380,
  });
  let triggerRef: HTMLButtonElement | undefined;
  let popoverRef: HTMLDivElement | undefined;
  let searchRef: HTMLInputElement | undefined;

  const keyOf = (m: FlattenedModel) => `${m.providerID}/${m.modelID}`;

  const selectedModel = createMemo(() => props.models.find((m) => keyOf(m) === props.value));

  const filtered = createMemo(() => rankModelSearch(props.models, query()));

  const visibleModels = createMemo(() => filtered().slice(0, renderLimit()));

  const updatePopoverPosition = () => {
    if (!triggerRef || typeof window === "undefined") return;
    const rect = triggerRef.getBoundingClientRect();
    const gap = 6;
    const margin = 12;
    const width = Math.max(220, Math.min(420, window.innerWidth - margin * 2));
    const spaceBelow = window.innerHeight - rect.bottom - gap - margin;
    const spaceAbove = rect.top - gap - margin;
    const placeBelow = spaceBelow >= 240 || spaceBelow >= spaceAbove;
    const maxHeight = Math.max(160, Math.min(380, placeBelow ? spaceBelow : spaceAbove));
    const left = Math.max(margin, Math.min(rect.left, window.innerWidth - width - margin));
    setPopoverBox(
      placeBelow
        ? { left, top: rect.bottom + gap, width, maxHeight }
        : { left, bottom: window.innerHeight - rect.top + gap, width, maxHeight }
    );
  };

  const close = () => setOpen(false);

  const openPicker = () => {
    updatePopoverPosition();
    setQuery("");
    setRenderLimit(RENDER_STEP);
    const index = filtered().findIndex((m) => keyOf(m) === props.value);
    setActiveIndex(index >= 0 ? index : 0);
    setOpen(true);
    setTimeout(() => searchRef?.focus(), 20);
  };

  const loadMoreIfNeeded = (el: HTMLElement) => {
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 48) {
      if (renderLimit() < filtered().length) setRenderLimit((v) => v + RENDER_STEP);
    }
  };

  const toggle = () => {
    if (open()) close();
    else openPicker();
  };

  const choose = (model: FlattenedModel) => {
    setOpen(false);
    props.onChange(keyOf(model));
    triggerRef?.focus();
  };

  const scrollActiveIntoView = (index: number) => {
    if (index >= renderLimit()) setRenderLimit(index + RENDER_STEP);
    requestAnimationFrame(() => {
      popoverRef
        ?.querySelector<HTMLElement>(`[data-model-index="${index}"]`)
        ?.scrollIntoView({ block: "nearest" });
    });
  };

  const moveActive = (delta: number) => {
    const list = filtered();
    if (list.length === 0) return;
    const next = (activeIndex() + delta + list.length) % list.length;
    setActiveIndex(next);
    scrollActiveIntoView(next);
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.isComposing) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      moveActive(1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      moveActive(-1);
    } else if (e.key === "Enter") {
      const model = filtered()[activeIndex()];
      if (model) {
        e.preventDefault();
        choose(model);
      }
    } else if (e.key === "Escape") {
      e.preventDefault();
      close();
      triggerRef?.focus();
    }
  };

  createEffect(() => {
    if (!open()) return;
    updatePopoverPosition();
    const raf = requestAnimationFrame(updatePopoverPosition);
    window.addEventListener("resize", updatePopoverPosition);
    window.addEventListener("scroll", updatePopoverPosition, true);
    onCleanup(() => {
      cancelAnimationFrame(raf);
      window.removeEventListener("resize", updatePopoverPosition);
      window.removeEventListener("scroll", updatePopoverPosition, true);
    });
  });

  onMount(() => {
    const onDocMouseDown = (e: MouseEvent) => {
      if (!open()) return;
      const target = e.target as Node;
      const path = typeof e.composedPath === "function" ? e.composedPath() : [];
      const inTrigger = triggerRef
        ? path.includes(triggerRef) || triggerRef.contains(target)
        : false;
      const inPopover = popoverRef
        ? path.includes(popoverRef) || popoverRef.contains(target)
        : false;
      if (!inTrigger && !inPopover) close();
    };
    document.addEventListener("mousedown", onDocMouseDown);
    onCleanup(() => document.removeEventListener("mousedown", onDocMouseDown));
  });

  return (
    <div class="relative flex-1 min-w-0">
      <button
        ref={triggerRef}
        type="button"
        onClick={toggle}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown" && !open()) {
            e.preventDefault();
            openPicker();
          } else if (e.key === "Escape" && open()) {
            e.preventDefault();
            close();
          }
        }}
        aria-haspopup="listbox"
        aria-expanded={open()}
        title={selectedModel() ? props.value : t("models.autoPoolSelectModel")}
        class={`flex w-full items-center justify-between gap-2 rounded-xl border bg-[var(--bg-panel)] px-2.5 py-1 text-left text-[12px] font-medium outline-none transition-colors cursor-pointer ${
          open()
            ? "border-[var(--accent-primary)]"
            : "border-[var(--border-strong)] hover:border-[var(--accent-primary)]/60"
        }`}
      >
        <span class="flex min-w-0 items-center gap-1.5">
          <Show
            when={selectedModel()}
            fallback={<Cpu size={12} class="shrink-0 text-[var(--text-faint)]" />}
          >
            {(model) => (
              <>
                <ProviderIcon id={model().providerID} class="h-3.5 w-3.5 shrink-0" />
                <span class="truncate text-[var(--text-primary)]">
                  {model().providerName} / {model().modelName}
                </span>
                <Show when={model().free}>
                  <span class="shrink-0 rounded bg-[var(--ok-surface)] px-1.5 py-0.2 text-[11px] font-semibold text-[var(--ok-text)]">
                    {t("models.freeModel")}
                  </span>
                </Show>
              </>
            )}
          </Show>
          <Show when={!selectedModel()}>
            <span class="truncate text-[var(--text-muted)]">{t("models.autoPoolSelectModel")}</span>
          </Show>
        </span>
        <ChevronDown
          size={13}
          class={`shrink-0 opacity-60 transition-transform duration-150 ${open() ? "rotate-180" : ""}`}
        />
      </button>

      <Presence when={open()}>
        {(state) => (
          <Portal>
            <div
              ref={popoverRef}
              role="listbox"
              data-state={state()}
              onKeyDown={handleKeyDown}
              style={{
                left: `${popoverBox().left}px`,
                top: popoverBox().top !== undefined ? `${popoverBox().top}px` : undefined,
                bottom:
                  popoverBox().bottom !== undefined ? `${popoverBox().bottom}px` : undefined,
                width: `${popoverBox().width}px`,
                "max-height": `${popoverBox().maxHeight}px`,
                "--hoxi-origin": popoverBox().bottom !== undefined ? "bottom left" : "top left",
              }}
              class="hoxi-popover fixed z-[95] flex flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-xl"
            >
              <div class="flex shrink-0 items-center gap-2 border-b border-[var(--border-subtle)] px-3 py-2">
                <Search size={13} class="shrink-0 text-[var(--text-faint)]" />
                <input
                  ref={searchRef}
                  type="text"
                  value={query()}
                  placeholder={t("models.searchModels")}
                  onInput={(e) => {
                    setQuery(e.currentTarget.value);
                    setRenderLimit(RENDER_STEP);
                    setActiveIndex(0);
                  }}
                  class="w-full bg-transparent text-[13px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
                />
                <Show when={query()}>
                  <button
                    type="button"
                    onClick={() => {
                      setQuery("");
                      setRenderLimit(RENDER_STEP);
                      setActiveIndex(0);
                      searchRef?.focus();
                    }}
                    class="shrink-0 rounded p-0.5 text-[var(--text-faint)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                    aria-label={t("models.clearSearch")}
                  >
                    <X size={12} />
                  </button>
                </Show>
              </div>

              <div
                class="min-h-0 flex-1 overflow-y-auto p-1.5"
                onScroll={(e) => loadMoreIfNeeded(e.currentTarget)}
              >
                <Show
                  when={filtered().length > 0}
                  fallback={
                    <div class="flex h-full flex-col items-center justify-center p-4 text-center text-[12px] text-[var(--text-muted)]">
                      {t("models.noModelsFound")}
                    </div>
                  }
                >
                  <For each={visibleModels()}>
                    {(model, index) => (
                      <button
                        type="button"
                        role="option"
                        aria-selected={keyOf(model) === props.value}
                        data-model-index={index()}
                        style={{ "content-visibility": "auto", "contain-intrinsic-size": "auto 40px" }}
                        onMouseEnter={() => setActiveIndex(index())}
                        onClick={() => choose(model)}
                        class={`flex w-full items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-left transition-colors cursor-pointer ${
                          keyOf(model) === props.value
                            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                            : activeIndex() === index()
                              ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
                              : "text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                        }`}
                      >
                        <span class="flex min-w-0 items-center gap-2">
                          <ProviderIcon id={model.providerID} class="h-3.5 w-3.5 shrink-0" />
                          <span class="min-w-0">
                            <span class="flex items-center gap-1.5">
                              <span class="truncate text-[12px] font-medium">{model.modelName}</span>
                              <Show when={model.free}>
                                <span class="shrink-0 rounded bg-[var(--ok-surface)] px-1.5 py-[1px] text-[11px] font-semibold text-[var(--ok-text)]">
                                  {t("models.freeModel")}
                                </span>
                              </Show>
                            </span>
                            <span class="block truncate text-[11px] text-[var(--text-muted)]">
                              {model.providerName} · {model.modelID}
                            </span>
                          </span>
                        </span>
                        <Show when={keyOf(model) === props.value}>
                          <Check size={13} class="shrink-0" />
                        </Show>
                      </button>
                    )}
                  </For>
                </Show>
              </div>
            </div>
          </Portal>
        )}
      </Presence>
    </div>
  );
}
