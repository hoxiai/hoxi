// web/src/components/models/ModelSelector.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import { Portal } from "solid-js/web";
import { Presence } from "../motion/Presence";
import {
  Check,
  ChevronDown,
  Cpu,
  Eye,
  KeyRound,
  Search,
  Star,
} from "lucide-solid";
import {
  AUTO_PROVIDER_ID,
  buildAutoVirtualProvider,
  clampReasoningEffort,
  lookupModelContextLimit,
  inferModelEffortCapabilities,
  isCustomOrConfiguredProvider,
  isFavoriteModel,
  isNonChatUtilityModelID,
  openProviderModal,
  peekAutoModelTarget,
  selectModel,
  setReasoningEffort,
  toggleFavoriteModel,
  workspace,
  type ConcreteReasoningEffort,
  type ReasoningEffort,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";
import {
  extractFlattenedModels,
  formatPricePerMillion,
  formatTokenLimit,
  getMultiplierBadgeClass,
  ModelSelectorRail,
  rankModelSearch,
  type FlattenedModel,
} from "./model-selector-utils";
import { ModelDetailsPopover } from "./ModelDetailsPopover";
import { ModelProbeBadge, ModelProbeButton } from "./ModelProbeControl";
import { SelectedModelHoverCard } from "./SelectedModelHoverCard";

function effortOptionLabel(level: ConcreteReasoningEffort): string {
  switch (level) {
    case "low":
      return t("models.effortLow");
    case "medium":
      return t("models.effortMedium");
    case "high":
      return t("models.effortHigh");
    case "xhigh":
      return t("models.effortXHigh");
    case "max":
      return t("models.effortMax");
  }
}

export default function ModelSelector() {
  const [open, setOpen] = createSignal(false);
  const [triggerHovered, setTriggerHovered] = createSignal(false);
  const [rail, setRail] = createSignal<string>("connected");
  const [query, setQuery] = createSignal("");
  const [details, setDetails] = createSignal<{ model: FlattenedModel; anchor: HTMLButtonElement } | null>(null);
  const [activeIndex, setActiveIndex] = createSignal(0);
  const [popoverBox, setPopoverBox] = createSignal({ left: 16, top: 56, width: 520, height: 410 });
  let containerRef: HTMLDivElement | undefined;
  let triggerBtnRef: HTMLButtonElement | undefined;
  let popoverRef: HTMLDivElement | undefined;
  let searchInputRef: HTMLInputElement | undefined;
  let effortRowRef: HTMLDivElement | undefined;
  let hoverCloseTimer: ReturnType<typeof setTimeout> | undefined;

  const updatePopoverPosition = () => {
    if (!triggerBtnRef || typeof window === "undefined") return;
    const rect = triggerBtnRef.getBoundingClientRect();
    const gap = 8;
    const margin = 12;
    const idealWidth = 520;
    const idealHeight = 410;
    const minHeight = 240;
    const width = Math.min(idealWidth, Math.max(300, window.innerWidth - margin * 2));
    const left = Math.max(margin, Math.min(rect.left, window.innerWidth - width - margin));

    const headerSafeTop = Math.min(56, Math.max(margin, rect.top - minHeight - gap));
    const spaceAbove = rect.top - gap - headerSafeTop;
    const spaceBelow = window.innerHeight - rect.bottom - gap - margin;

    let height = idealHeight;
    let top = rect.top - gap - idealHeight;

    if (spaceAbove >= idealHeight) {
      height = idealHeight;
      top = rect.top - gap - height;
    } else if (spaceBelow >= idealHeight) {
      height = idealHeight;
      top = rect.bottom + gap;
    } else if (spaceAbove >= spaceBelow) {
      height = Math.min(idealHeight, Math.max(minHeight, spaceAbove));
      top = Math.max(margin, rect.top - gap - height);
    } else {
      height = Math.min(idealHeight, Math.max(minHeight, spaceBelow));
      top = Math.min(window.innerHeight - height - margin, rect.bottom + gap);
    }

    setPopoverBox({ left, top, width, height });
  };

  const cancelHoverClose = () => {
    if (hoverCloseTimer) {
      clearTimeout(hoverCloseTimer);
      hoverCloseTimer = undefined;
    }
  };

  const showHover = () => {
    cancelHoverClose();
    if (!open()) setTriggerHovered(true);
  };

  const hideHover = () => {
    cancelHoverClose();
    hoverCloseTimer = setTimeout(() => setTriggerHovered(false), 140);
  };

  const openEffortPicker = () => {
    cancelHoverClose();
    setTriggerHovered(false);
    const wasOpen = open();
    if (!wasOpen) {
      updatePopoverPosition();
      setOpen(true);
      setActiveIndex(0);
    }
    setTimeout(
      () => {
        const row = effortRowRef;
        if (!row) return;
        const activeBtn = row.querySelector<HTMLButtonElement>("button[aria-pressed='true']");
        (activeBtn ?? row.querySelector<HTMLButtonElement>("button"))?.focus();
      },
      wasOpen ? 0 : 20
    );
  };

  onCleanup(cancelHoverClose);

  const connectedSet = createMemo(() => new Set(workspace.connectedProviders));

  const autoSystemModels = createMemo<FlattenedModel[]>(() =>
    extractFlattenedModels(
      [buildAutoVirtualProvider()],
      new Set([AUTO_PROVIDER_ID]),
      workspace.globalConfig
    )
  );

  const allModels = createMemo<FlattenedModel[]>(() =>
    extractFlattenedModels(workspace.providers, connectedSet(), workspace.globalConfig)
  );

  const connectedModels = createMemo<FlattenedModel[]>(() => {
    const conn = allModels().filter((m) => m.connected);
    if (conn.length > 0) return conn;
    const freeModels = allModels().filter((m) => m.providerID === "opencode" || m.free);
    return freeModels.length > 0 ? freeModels : allModels().slice(0, 30);
  });

  const freeConnectedModels = createMemo<FlattenedModel[]>(() =>
    connectedModels().filter((m) => m.free)
  );

  const connectedProviderRails = createMemo(() => {
    const counts = new Map<string, { id: string; name: string; count: number; isCustom: boolean; isRunner: boolean }>();
    for (const m of connectedModels()) {
      if (m.providerID === AUTO_PROVIDER_ID) continue;
      const existing = counts.get(m.providerID);
      if (existing) {
        existing.count += 1;
      } else {
        counts.set(m.providerID, {
          id: m.providerID,
          name: m.providerName,
          count: 1,
          isCustom: isCustomOrConfiguredProvider(m.providerID),
          isRunner: m.source === "runner",
        });
      }
    }
    return Array.from(counts.values()).sort((a, b) => {
      const aPrio = a.isRunner || a.isCustom;
      const bPrio = b.isRunner || b.isCustom;
      if (aPrio !== bPrio) return aPrio ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
  });

  const activeModelInfo = createMemo<FlattenedModel | null>(() => {
    const sel = workspace.selectedModel;
    if (!sel) return null;
    if (sel.providerID === AUTO_PROVIDER_ID) {
      const autoFound = autoSystemModels().find((m) => m.modelID === sel.modelID);
      if (autoFound) return autoFound;
    }
    const found = allModels().find(
      (m) => m.providerID === sel.providerID && m.modelID === sel.modelID
    );
    if (found) return found;
    const prov = workspace.providers.find((p) => p.id === sel.providerID);
    const hoxiProv = workspace.hoxiProviders?.[sel.providerID];
    const cfgProv = workspace.globalConfig?.provider?.[sel.providerID];
    const baseURL =
      hoxiProv?.options?.baseURL ||
      cfgProv?.options?.baseURL ||
      prov?.options?.baseURL;
    const inferredReasoning =
      !isNonChatUtilityModelID(sel.modelID) &&
      !/haiku|deepseek-flash|glm-.*flash/i.test(sel.modelID);
    const effortCaps = inferModelEffortCapabilities(sel.providerID, sel.modelID, inferredReasoning);
    return {
      providerID: sel.providerID,
      providerName: hoxiProv?.name || cfgProv?.name || prov?.name || sel.providerID,
      modelID: sel.modelID,
      modelName: sel.modelID,
      connected: connectedSet().has(sel.providerID),
      isCustom: isCustomOrConfiguredProvider(sel.providerID),
      source: prov?.source,
      baseURL,
      reasoning: inferredReasoning,
      efforts: effortCaps.efforts,
      defaultEffort: effortCaps.defaultEffort,
      image: false,
      toolcall: true,
      contextLimit: lookupModelContextLimit(sel.providerID, sel.modelID),
      free: false,
    };
  });

  const availableEffortOptions = createMemo<
    Array<{ value: ReasoningEffort; label: string; title?: string }>
  >(() => {
    const info = activeModelInfo();
    if (info && !info.reasoning && (!info.efforts || info.efforts.length === 0)) {
      return [];
    }
    const levels: readonly ConcreteReasoningEffort[] =
      info?.efforts && info.efforts.length > 0 ? info.efforts : ["low", "medium", "high"];
    const defHint = info?.defaultEffort
      ? t("models.defaultEffortHint", { effort: info.defaultEffort.toUpperCase() })
      : undefined;
    return [
      { value: "", label: t("models.effortAuto"), title: defHint },
      ...levels.map((lvl) => ({ value: lvl, label: effortOptionLabel(lvl) })),
    ];
  });

  const effectiveReasoningEffort = createMemo<ReasoningEffort>(() => {
    const raw = workspace.reasoningEffort;
    const info = activeModelInfo();
    if (!raw) return "";
    if (info && !info.reasoning && (!info.efforts || info.efforts.length === 0)) {
      return "";
    }
    return clampReasoningEffort(raw, info?.efforts, info?.defaultEffort);
  });

  const filteredModels = createMemo<FlattenedModel[]>(() => {
    const q = query().trim();
    const pool = connectedModels();
    const allWithAuto = [...autoSystemModels(), ...pool];

    if (q) {
      return rankModelSearch(allWithAuto, q);
    }

    const r = rail();
    if (r === AUTO_PROVIDER_ID) {
      return autoSystemModels();
    }
    if (r === "free") {
      return [...freeConnectedModels()].sort((a, b) => {
        const aFav = isFavoriteModel(a.providerID, a.modelID);
        const bFav = isFavoriteModel(b.providerID, b.modelID);
        if (aFav !== bFav) return aFav ? -1 : 1;
        const aPrio = a.source === "runner" || a.isCustom;
        const bPrio = b.source === "runner" || b.isCustom;
        if (aPrio !== bPrio) return aPrio ? -1 : 1;
        return a.providerName.localeCompare(b.providerName);
      });
    }
    if (r === "favorites") {
      return allWithAuto.filter((m) => isFavoriteModel(m.providerID, m.modelID));
    }
    if (r === "recent") {
      const out: FlattenedModel[] = [];
      for (const rm of workspace.recentModels) {
        const found = allWithAuto.find(
          (m) => m.providerID === rm.providerID && m.modelID === rm.modelID
        );
        if (found) out.push(found);
      }
      return out;
    }
    if (r === "connected") {
      return [...pool].sort((a, b) => {
        const aFav = isFavoriteModel(a.providerID, a.modelID);
        const bFav = isFavoriteModel(b.providerID, b.modelID);
        if (aFav !== bFav) return aFav ? -1 : 1;
        const aPrio = a.source === "runner" || a.isCustom;
        const bPrio = b.source === "runner" || b.isCustom;
        if (aPrio !== bPrio) return aPrio ? -1 : 1;
        return a.providerName.localeCompare(b.providerName);
      });
    }
    return pool.filter((m) => m.providerID === r);
  });

  createEffect(() => {
    if (!open()) {
      setDetails(null);
      return;
    }
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

  const toggleOpen = () => {
    const next = !open();
    if (next) {
      updatePopoverPosition();
    }
    setOpen(next);
    setTriggerHovered(false);
    if (next) {
      setActiveIndex(0);
      setTimeout(() => searchInputRef?.focus(), 20);
    } else {
      setQuery("");
    }
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.target !== searchInputRef) {
      if (e.key === "Escape") {
        e.preventDefault();
        setOpen(false);
      }
      return;
    }
    const list = filteredModels();
    if (e.key === "ArrowDown") {
      e.preventDefault();
      if (list.length > 0) setActiveIndex((i) => (i + 1) % list.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      if (list.length > 0) setActiveIndex((i) => (i - 1 + list.length) % list.length);
    } else if (e.key === "Enter") {
      const target = list[activeIndex()];
      if (target) {
        e.preventDefault();
        selectModel({ providerID: target.providerID, modelID: target.modelID });
        setOpen(false);
      }
    } else if (e.key === "Escape") {
      e.preventDefault();
      setOpen(false);
    }
  };

  onMount(() => {
    const onDocClick = (e: MouseEvent) => {
      if (!open() || !containerRef) return;
      const target = e.target as Node;
      const path = typeof e.composedPath === "function" ? e.composedPath() : [];
      if (path.some((node) => node instanceof HTMLElement && node.hasAttribute("data-model-details"))) return;
      const inTrigger = path.includes(containerRef) || containerRef.contains(target);
      const inPopover = Boolean(popoverRef && (path.includes(popoverRef) || popoverRef.contains(target)));
      if (!inTrigger && !inPopover) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    onCleanup(() => document.removeEventListener("mousedown", onDocClick));
  });

  return (
    <div ref={containerRef} class="relative">
      <button
        ref={triggerBtnRef}
        type="button"
        onClick={toggleOpen}
        onMouseEnter={showHover}
        onMouseLeave={hideHover}
        class={`hoxi-press inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] font-medium cursor-pointer max-w-[260px] ${
          open()
            ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
            : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
        }`}
        title={activeModelInfo() ? undefined : t("timeline.selectModel")}
      >
        <Show
          when={activeModelInfo()?.providerID}
          fallback={<Cpu size={12} class="shrink-0 text-[var(--accent-primary)]" />}
        >
          {(provID) => <ProviderIcon id={provID()} class="w-3.5 h-3.5 shrink-0" />}
        </Show>
        <span class="truncate">
          {activeModelInfo()?.modelName || t("models.selectModel")}
        </span>
        <Show when={activeModelInfo()?.free}>
          <span class="shrink-0 rounded bg-[var(--ok-surface)] px-1.5 py-[1px] text-[11px] font-semibold text-[var(--ok-text)]">
            {t("models.freeModel")}
          </span>
        </Show>
        <Show when={activeModelInfo()?.multiplier && !activeModelInfo()?.free}>
          <span
            class={`shrink-0 rounded px-1.5 py-[1px] font-mono text-[11px] font-semibold ${getMultiplierBadgeClass(
              activeModelInfo()?.multiplier,
              activeModelInfo()?.free
            )}`}
          >
            {activeModelInfo()?.multiplier}
          </span>
        </Show>
        <Show when={effectiveReasoningEffort()}>
          <span class="shrink-0 rounded bg-[var(--accent-surface)] px-1 py-[1px] text-[11px] uppercase font-mono text-[var(--accent-primary)]">
            {effectiveReasoningEffort()}
          </span>
        </Show>
        <ChevronDown
          size={12}
          class={`shrink-0 opacity-60 transition-transform duration-150 ${
            open() ? "rotate-180" : ""
          }`}
        />
      </button>

      <Show when={!open() && triggerHovered() && activeModelInfo()}>
        {(info) => (
          <SelectedModelHoverCard
            model={info()}
            onActivate={openEffortPicker}
            onHoverStart={showHover}
            onHoverEnd={hideHover}
          />
        )}
      </Show>

      <Presence when={open()}>
        {(state) => (
        <Portal>
          <div
            ref={popoverRef}
            onKeyDown={handleKeyDown}
            data-state={state()}
            style={{
              left: `${popoverBox().left}px`,
              top: `${popoverBox().top}px`,
              width: `${popoverBox().width}px`,
              height: `${popoverBox().height}px`,
              "--hoxi-origin": "top left",
            }}
            class="hoxi-popover fixed z-[90] flex flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-xl"
          >
          <div class="flex flex-1 min-h-0">
            <ModelSelectorRail
              rail={rail()}
              hasQuery={Boolean(query().trim())}
              connectedTotal={connectedModels().length}
              autoTotal={autoSystemModels().length}
              freeTotal={freeConnectedModels().length}
              providers={connectedProviderRails()}
              onSelectRail={(r) => {
                setRail(r);
                setDetails(null);
                setActiveIndex(0);
              }}
              onClosePopover={() => setOpen(false)}
            />

            <div class="flex-1 flex flex-col min-w-0">
              <div class="flex items-center gap-2 border-b border-[var(--border-subtle)] px-3 py-2">
                <Search size={13} class="text-[var(--text-faint)] shrink-0" />
                <input
                  ref={searchInputRef}
                  type="text"
                  placeholder={t("models.searchModels")}
                  value={query()}
                  onInput={(e) => {
                    setQuery(e.currentTarget.value);
                    setDetails(null);
                    setActiveIndex(0);
                  }}
                  class="w-full bg-transparent text-[13px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
                />
                <Show when={freeConnectedModels().length > 0}>
                  <button
                    type="button"
                    onClick={() => {
                      const nextRail = rail() === "free" && !query().trim() ? "connected" : "free";
                      setQuery("");
                      setRail(nextRail);
                      setDetails(null);
                      setActiveIndex(0);
                    }}
                    class={`shrink-0 inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] font-semibold transition-colors cursor-pointer ${
                      rail() === "free" && !query().trim()
                        ? "bg-[var(--ok-surface)] text-[var(--ok-text)] ring-1 ring-[var(--ok-text)]/30"
                        : "bg-[var(--bg-inset)] text-[var(--text-secondary)] hover:bg-[var(--ok-surface)] hover:text-[var(--ok-text)]"
                    }`}
                  >
                    <span>{t("models.freeModel")}</span>
                    <span class="font-mono text-[11px] opacity-80">{freeConnectedModels().length}</span>
                  </button>
                </Show>
              </div>

              <div class="flex-1 overflow-y-auto p-1.5 space-y-0.5">
                <Show
                  when={filteredModels().length > 0}
                  fallback={
                    <div class="flex h-full flex-col items-center justify-center p-4 text-center text-[12px] text-[var(--text-muted)]">
                      <p>{t("models.noModelsFound")}</p>
                      <button
                        type="button"
                        onClick={() => {
                          setOpen(false);
                          openProviderModal("providers", null);
                        }}
                        class="mt-2 inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-1.5 text-[12px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                      >
                        <KeyRound size={12} />
                        {t("models.connectProviderOrAdd")}
                      </button>
                    </div>
                  }
                >
                  <For each={filteredModels()}>
                    {(item, idx) => {
                      const isSelected = () =>
                        workspace.selectedModel?.providerID === item.providerID &&
                        workspace.selectedModel?.modelID === item.modelID;
                      const fav = () => isFavoriteModel(item.providerID, item.modelID);
                      const ctxText = () => formatTokenLimit(item.contextLimit);
                      const inPrice = () => formatPricePerMillion(item.inputCost, item.priceCurrency);
                      const outPrice = () => formatPricePerMillion(item.outputCost, item.priceCurrency);

                      return (
                        <div
                          onMouseEnter={() => {
                            setActiveIndex(idx());
                          }}
                          class={`group flex items-center justify-between gap-2 rounded-xl px-2.5 py-1.5 transition-colors cursor-pointer ${
                            isSelected()
                              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                              : activeIndex() === idx()
                              ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
                              : "hover:bg-[var(--bg-hover)] text-[var(--text-primary)]"
                          }`}
                        >
                          <button
                            type="button"
                            class="min-w-0 flex-1 text-left cursor-pointer rounded-lg focus-visible:outline focus-visible:outline-[var(--accent-primary)]"
                            onClick={() => {
                              selectModel({
                                providerID: item.providerID,
                                modelID: item.modelID,
                              });
                              setOpen(false);
                            }}
                          >
                            <span class="flex items-center gap-1.5">
                              <span class="truncate text-[13px] font-medium">
                                {item.modelName}
                              </span>
                              <Show when={item.free}>
                                <span class="shrink-0 inline-flex items-center rounded bg-[var(--ok-surface)] px-1.5 py-[1px] text-[11px] font-semibold text-[var(--ok-text)]">
                                  {t("models.freeModel")}
                                </span>
                              </Show>
                              <Show when={item.multiplier}>
                                <span
                                  class={`shrink-0 inline-flex items-center rounded px-1.5 py-[1px] font-mono text-[11px] font-semibold ${getMultiplierBadgeClass(
                                    item.multiplier,
                                    item.free
                                  )}`}
                                >
                                  {item.multiplier}
                                </span>
                              </Show>
                              <ModelProbeBadge
                                providerID={item.providerID}
                                modelID={item.modelID}
                              />
                            </span>
                            <span class="block truncate text-[11px] text-[var(--text-muted)]">
                              {item.providerID === AUTO_PROVIDER_ID && peekAutoModelTarget(item.modelID)
                                ? `${item.providerName} · ${t("models.autoPoolActiveTarget", {
                                    model: `${peekAutoModelTarget(item.modelID)!.providerID}/${peekAutoModelTarget(item.modelID)!.modelID}`,
                                  })}`
                                : item.providerName}
                              <Show when={ctxText()}>
                                <span class="font-mono"> · {ctxText()}</span>
                              </Show>
                              <Show
                                when={!item.free && (inPrice() || outPrice())}
                                fallback={
                                  <Show when={item.free}>
                                    <span class="font-mono text-[var(--ok-text)]"> · $0</span>
                                  </Show>
                                }
                              >
                                <span class="font-mono">
                                  {" "}
                                  · {inPrice() || "-"} / {outPrice() || "-"}
                                </span>
                              </Show>
                            </span>
                          </button>

                          <div class="flex items-center gap-1 shrink-0">
                            <ModelProbeButton
                              providerID={item.providerID}
                              modelID={item.modelID}
                              modelName={item.modelName}
                              source={item.source}
                            />
                            <button
                              type="button"
                              aria-haspopup="dialog"
                              aria-expanded={details()?.model === item}
                              aria-label={`${t("models.viewDetails")}: ${item.modelName}`}
                              title={t("models.viewDetails")}
                              onClick={(e) => {
                                setDetails(details()?.model === item ? null : { model: item, anchor: e.currentTarget });
                              }}
                              class={`rounded-md p-1 text-[var(--text-muted)] transition-opacity hover:bg-[var(--bg-panel)] hover:text-[var(--accent-primary)] focus-visible:outline focus-visible:outline-[var(--accent-primary)] cursor-pointer group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100 ${details()?.model === item ? "opacity-100" : "opacity-0"}`}
                            >
                              <Eye size={13} />
                            </button>
                            <button
                              type="button"
                              aria-label={t(fav() ? "models.removeFavorite" : "models.addFavorite")}
                              aria-pressed={fav()}
                              onClick={(e) => {
                                e.stopPropagation();
                                toggleFavoriteModel(item.providerID, item.modelID);
                              }}
                              class={`rounded p-1 transition-opacity cursor-pointer ${
                                fav()
                                  ? "text-amber-500 opacity-100"
                                  : "text-[var(--text-faint)] opacity-60 hover:opacity-100 hover:text-[var(--text-primary)]"
                              }`}
                            >
                              <Star size={12} class={fav() ? "fill-current" : ""} />
                            </button>
                            <Show when={isSelected()}>
                              <Check size={13} class="text-[var(--accent-primary)]" />
                            </Show>
                          </div>
                        </div>
                      );
                    }}
                  </For>
                </Show>
              </div>

              <div
                ref={effortRowRef}
                class="flex items-center justify-between border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 px-3 py-1.5 text-[11px]"
              >
                <span class="text-[var(--text-muted)]">
                  {t("models.reasoningEffort")}
                  <Show when={!workspace.reasoningEffort && activeModelInfo()?.defaultEffort}>
                    {(def) => (
                      <span class="ml-1 font-mono text-[11px] uppercase text-[var(--text-faint)]">
                        ({def()})
                      </span>
                    )}
                  </Show>
                </span>
                <Show
                  when={availableEffortOptions().length > 0}
                  fallback={
                    <span class="rounded-md bg-[var(--bg-inset)] px-2 py-0.5 text-[11px] text-[var(--text-faint)]">
                      {t("models.reasoningUnsupported")}
                    </span>
                  }
                >
                  <div class="inline-flex items-center gap-0.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5">
                    <For each={availableEffortOptions()}>
                      {(opt) => (
                        <button
                          type="button"
                          title={opt.title}
                          aria-pressed={effectiveReasoningEffort() === opt.value}
                          onClick={() => setReasoningEffort(opt.value)}
                          class={`rounded-md px-2 py-0.5 text-[11px] font-medium transition-colors cursor-pointer ${
                            effectiveReasoningEffort() === opt.value
                              ? "bg-[var(--bg-panel)] text-[var(--accent-primary)] shadow-2xs"
                              : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                          }`}
                        >
                          {opt.label}
                        </button>
                      )}
                    </For>
                  </div>
                </Show>
              </div>
            </div>
          </div>
          </div>
        </Portal>
        )}
      </Presence>
      <Show when={open() && details()} keyed>
        {(detail) => (
          <ModelDetailsPopover
            model={detail.model}
            anchor={detail.anchor}
            onClose={() => setDetails(null)}
            onEditCustom={(pid) => {
              setOpen(false);
              openProviderModal("custom", pid);
            }}
          />
        )}
      </Show>
    </div>
  );
}
