// web/src/components/models/ModelRolePicker.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import { Portal } from "solid-js/web";
import { Check, ChevronDown, Clock, Cpu, Search, Star, X } from "lucide-solid";
import { Presence } from "../motion/Presence";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";
import {
  isCustomOrConfiguredProvider,
  rankModelSearch,
  resolveFavoriteModelEntries,
  resolveRecentModelEntries,
  type ConnectedProviderGroup,
} from "../../store/workspace";
import { getMultiplierBadgeClass } from "./model-selector-utils";

export interface ModelRoleExtraOption {
  value: string;
  label: string;
}

interface PickerModel {
  providerID: string;
  providerName: string;
  modelID: string;
  modelName: string;
  fullKey: string;
  free: boolean;
  multiplier?: string;
  connected?: boolean;
  isCustom?: boolean;
}

type Row =
  | { kind: "section"; key: string; title: string; icon?: "clock" | "star"; count?: number }
  | {
      kind: "option";
      key: string;
      value: string;
      label: string;
      hint?: string;
      providerID?: string;
      free?: boolean;
      multiplier?: string;
    };

interface PopoverBox {
  left: number;
  top?: number;
  bottom?: number;
  width: number;
  maxHeight: number;
}

function modelRow(prefix: string, model: PickerModel): Row {
  return {
    kind: "option",
    key: `${prefix}:${model.fullKey}`,
    value: model.fullKey,
    label: model.modelName,
    hint: model.providerName,
    providerID: model.providerID,
    free: model.free,
    multiplier: model.multiplier,
  };
}

export default function ModelRolePicker(props: {
  value: string;
  groups: ConnectedProviderGroup[];
  allowEmpty?: boolean;
  emptyLabel?: string;
  extraOptions?: ModelRoleExtraOption[];
  triggerClass?: string;
  onChange: (value: string) => void;
}) {
  const [open, setOpen] = createSignal(false);
  const [query, setQuery] = createSignal("");
  const [activeIndex, setActiveIndex] = createSignal(0);
  const [popoverBox, setPopoverBox] = createSignal<PopoverBox>({
    left: 16,
    top: 0,
    width: 360,
    maxHeight: 420,
  });
  let triggerRef: HTMLButtonElement | undefined;
  let popoverRef: HTMLDivElement | undefined;
  let searchRef: HTMLInputElement | undefined;

  const allModels = createMemo<PickerModel[]>(() =>
    props.groups.flatMap((group) => {
      const custom = isCustomOrConfiguredProvider(group.providerID);
      return group.models.map((model) => ({
        providerID: group.providerID,
        providerName: group.providerName,
        modelID: model.id,
        modelName: model.name,
        fullKey: model.fullKey,
        free: model.free,
        multiplier: model.multiplier,
        connected: true,
        isCustom: custom,
      }));
    })
  );

  const currentModel = createMemo(() => allModels().find((model) => model.fullKey === props.value));
  const currentExtra = createMemo(() =>
    (props.extraOptions || []).find((option) => option.value === props.value)
  );
  const currentLabel = createMemo(() => {
    if (!props.value) return props.emptyLabel || t("models.useGlobalDefault");
    const extra = currentExtra();
    if (extra) return extra.label;
    const model = currentModel();
    if (model) return model.modelName;
    const slash = props.value.indexOf("/");
    return slash >= 0 ? props.value.slice(slash + 1) : props.value;
  });

  const recentModels = createMemo<PickerModel[]>(() => resolveRecentModelEntries(allModels(), 4));

  const favoriteModels = createMemo<PickerModel[]>(() =>
    resolveFavoriteModelEntries(allModels(), { limit: 6, excludeKey: props.value })
  );

  const rows = createMemo<Row[]>(() => {
    const out: Row[] = [];
    if (props.allowEmpty) {
      out.push({
        kind: "option",
        key: "special:empty",
        value: "",
        label: props.emptyLabel || t("models.useGlobalDefault"),
      });
    }
    for (const option of props.extraOptions || []) {
      out.push({
        kind: "option",
        key: `special:${option.value}`,
        value: option.value,
        label: option.label,
      });
    }

    const q = query().trim();
    if (q) {
      for (const model of rankModelSearch(allModels(), q)) out.push(modelRow("search", model));
      return out;
    }

    const recents = recentModels();
    if (recents.length > 0) {
      out.push({
        kind: "section",
        key: "section:recent",
        title: t("models.lastSelectedTitle"),
        icon: "clock",
      });
      for (const model of recents) out.push(modelRow("recent", model));
    }

    const favorites = favoriteModels();
    if (favorites.length > 0) {
      out.push({
        kind: "section",
        key: "section:favorites",
        title: t("models.favorites"),
        icon: "star",
      });
      for (const model of favorites) out.push(modelRow("favorite", model));
    }

    for (const group of props.groups) {
      if (group.models.length === 0) continue;
      out.push({
        kind: "section",
        key: `section:provider:${group.providerID}`,
        title: group.providerName,
        count: group.models.length,
      });
      for (const model of group.models) {
        out.push(
          modelRow(`provider:${group.providerID}`, {
            providerID: group.providerID,
            providerName: group.providerName,
            modelID: model.id,
            modelName: model.name,
            fullKey: model.fullKey,
            free: model.free,
            multiplier: model.multiplier,
          })
        );
      }
    }
    return out;
  });

  const optionRows = createMemo<Array<Extract<Row, { kind: "option" }>>>(() =>
    rows().filter((row): row is Extract<Row, { kind: "option" }> => row.kind === "option")
  );

  const optionIndexOf = createMemo(() => {
    const map = new Map<string, number>();
    optionRows().forEach((row, index) => map.set(row.key, index));
    return map;
  });

  const updatePopoverPosition = () => {
    if (!triggerRef || typeof window === "undefined") return;
    const rect = triggerRef.getBoundingClientRect();
    const gap = 6;
    const margin = 12;
    const width = Math.max(200, Math.min(360, window.innerWidth - margin * 2));
    const spaceBelow = window.innerHeight - rect.bottom - gap - margin;
    const spaceAbove = rect.top - gap - margin;
    const placeBelow = spaceBelow >= 300 || spaceBelow >= spaceAbove;
    const maxHeight = Math.max(160, Math.min(430, placeBelow ? spaceBelow : spaceAbove));
    const left = Math.max(margin, Math.min(rect.right - width, window.innerWidth - width - margin));
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
    const selectedIndex = optionRows().findIndex((row) => row.value === props.value);
    setActiveIndex(selectedIndex >= 0 ? selectedIndex : 0);
    setOpen(true);
    setTimeout(() => searchRef?.focus(), 20);
  };

  const toggle = () => {
    if (open()) close();
    else openPicker();
  };

  const choose = (value: string) => {
    setOpen(false);
    if (value !== props.value) props.onChange(value);
    triggerRef?.focus();
  };

  const scrollOptionIntoView = (index: number) => {
    requestAnimationFrame(() => {
      popoverRef
        ?.querySelector<HTMLElement>(`[data-option-index="${index}"]`)
        ?.scrollIntoView({ block: "nearest" });
    });
  };

  const moveActive = (delta: number) => {
    const options = optionRows();
    if (options.length === 0) return;
    const next = (activeIndex() + delta + options.length) % options.length;
    setActiveIndex(next);
    scrollOptionIntoView(next);
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
      const row = optionRows()[activeIndex()];
      if (row) {
        e.preventDefault();
        choose(row.value);
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
      const inTrigger = triggerRef ? path.includes(triggerRef) || triggerRef.contains(target) : false;
      const inPopover = popoverRef ? path.includes(popoverRef) || popoverRef.contains(target) : false;
      if (!inTrigger && !inPopover) close();
    };
    document.addEventListener("mousedown", onDocMouseDown);
    onCleanup(() => document.removeEventListener("mousedown", onDocMouseDown));
  });

  return (
    <div class="relative shrink-0">
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
        title={
          currentModel()
            ? `${currentModel()!.providerName} · ${currentModel()!.fullKey}`
            : currentLabel()
        }
        class={`flex ${props.triggerClass || "w-[280px]"} items-center justify-between gap-2 rounded-xl border bg-[var(--bg-panel)] px-3 py-1.5 text-[12px] font-medium text-[var(--text-primary)] outline-none transition-colors cursor-pointer ${
          open()
            ? "border-[var(--accent-primary)]"
            : "border-[var(--border-strong)] hover:border-[var(--accent-primary)]/60"
        }`}
      >
        <span class="flex min-w-0 items-center gap-1.5">
          <Show
            when={currentModel()?.providerID}
            fallback={<Cpu size={13} class="shrink-0 text-[var(--text-faint)]" />}
          >
            {(providerID) => <ProviderIcon id={providerID()} class="h-3.5 w-3.5 shrink-0" />}
          </Show>
          <span class={`truncate ${props.value ? "" : "text-[var(--text-muted)]"}`}>
            {currentLabel()}
          </span>
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
                bottom: popoverBox().bottom !== undefined ? `${popoverBox().bottom}px` : undefined,
                width: `${popoverBox().width}px`,
                "max-height": `${popoverBox().maxHeight}px`,
                "--hoxi-origin": popoverBox().bottom !== undefined ? "bottom right" : "top right",
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
                    setActiveIndex(0);
                  }}
                  class="w-full bg-transparent text-[13px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
                />
                <Show when={query()}>
                  <button
                    type="button"
                    onClick={() => {
                      setQuery("");
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

              <div class="min-h-0 flex-1 overflow-y-auto p-1.5">
                <Show
                  when={optionRows().length > 0}
                  fallback={
                    <div class="flex h-full flex-col items-center justify-center p-4 text-center text-[12px] text-[var(--text-muted)]">
                      {t("models.noModelsFound")}
                    </div>
                  }
                >
                  <For each={rows()}>
                    {(row) =>
                      row.kind === "section" ? (
                        <div class="flex items-center justify-between gap-2 px-2 pb-0.5 pt-2 text-[11px] font-semibold uppercase tracking-wide text-[var(--text-faint)]">
                          <span class="flex min-w-0 items-center gap-1.5">
                            <Show when={row.icon === "clock"}>
                              <Clock size={10} class="shrink-0" />
                            </Show>
                            <Show when={row.icon === "star"}>
                              <Star size={10} class="shrink-0 text-amber-500" />
                            </Show>
                            <span class="truncate">{row.title}</span>
                          </span>
                          <Show when={row.count !== undefined}>
                            <span class="shrink-0 font-mono text-[10px] opacity-70">{row.count}</span>
                          </Show>
                        </div>
                      ) : (
                        <button
                          type="button"
                          role="option"
                          aria-selected={row.value === props.value}
                          data-option-index={optionIndexOf().get(row.key)}
                          onMouseEnter={() => {
                            const index = optionIndexOf().get(row.key);
                            if (index !== undefined) setActiveIndex(index);
                          }}
                          onClick={() => choose(row.value)}
                          class={`flex w-full items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-left transition-colors cursor-pointer ${
                            row.value === props.value
                              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                              : activeIndex() === optionIndexOf().get(row.key)
                                ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
                                : "text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                          }`}
                        >
                          <span class="flex min-w-0 items-center gap-2">
                            <Show when={row.providerID} fallback={<span class="h-3.5 w-3.5 shrink-0" />}>
                              {(providerID) => (
                                <ProviderIcon id={providerID()} class="h-3.5 w-3.5 shrink-0" />
                              )}
                            </Show>
                            <span class="min-w-0">
                              <span class="flex items-center gap-1.5">
                                <span class="truncate text-[12px] font-medium">{row.label}</span>
                                <Show when={row.free}>
                                  <span class="shrink-0 rounded bg-[var(--ok-surface)] px-1.5 py-[1px] text-[11px] font-semibold text-[var(--ok-text)]">
                                    {t("models.freeModel")}
                                  </span>
                                </Show>
                                <Show when={row.multiplier}>
                                  <span
                                    class={`shrink-0 rounded px-1.5 py-[1px] font-mono text-[11px] font-semibold ${getMultiplierBadgeClass(
                                      row.multiplier,
                                      row.free
                                    )}`}
                                  >
                                    {row.multiplier}
                                  </span>
                                </Show>
                              </span>
                              <Show when={row.hint}>
                                <span class="block truncate text-[11px] text-[var(--text-muted)]">
                                  {row.hint}
                                </span>
                              </Show>
                            </span>
                          </span>
                          <Show when={row.value === props.value}>
                            <Check size={13} class="shrink-0" />
                          </Show>
                        </button>
                      )
                    }
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
