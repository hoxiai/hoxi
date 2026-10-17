// web/src/components/models/ModelSelector.tsx
import { createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import {
  Check,
  ChevronDown,
  Cpu,
  Image,
  KeyRound,
  Search,
  Sparkles,
  Star,
} from "lucide-solid";
import {
  isCustomOrConfiguredProvider,
  isFavoriteModel,
  openProviderModal,
  selectModel,
  setReasoningEffort,
  toggleFavoriteModel,
  workspace,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";
import {
  extractFlattenedModels,
  formatTokenLimit,
  ModelPreviewFooter,
  ModelSelectorRail,
  rankModelSearch,
  type FlattenedModel,
} from "./model-selector-utils";

const EFFORT_OPTIONS: Array<{ value: "" | "low" | "medium" | "high"; label: string }> = [
  { value: "", label: "Auto" },
  { value: "low", label: "Low" },
  { value: "medium", label: "Med" },
  { value: "high", label: "High" },
];

export default function ModelSelector() {
  const [open, setOpen] = createSignal(false);
  const [rail, setRail] = createSignal<string>("connected");
  const [query, setQuery] = createSignal("");
  const [hoveredModel, setHoveredModel] = createSignal<FlattenedModel | null>(null);
  const [activeIndex, setActiveIndex] = createSignal(0);
  let containerRef: HTMLDivElement | undefined;
  let searchInputRef: HTMLInputElement | undefined;

  const connectedSet = createMemo(() => new Set(workspace.connectedProviders));

  const allModels = createMemo<FlattenedModel[]>(() =>
    extractFlattenedModels(workspace.providers, connectedSet(), workspace.globalConfig)
  );

  const connectedModels = createMemo<FlattenedModel[]>(() => {
    const conn = allModels().filter((m) => m.connected);
    if (conn.length > 0) return conn;
    const freeModels = allModels().filter((m) => m.providerID === "opencode" || m.free);
    return freeModels.length > 0 ? freeModels : allModels().slice(0, 30);
  });

  const connectedProviderRails = createMemo(() => {
    const counts = new Map<string, { id: string; name: string; count: number; isCustom: boolean }>();
    for (const m of connectedModels()) {
      const existing = counts.get(m.providerID);
      if (existing) {
        existing.count += 1;
      } else {
        counts.set(m.providerID, {
          id: m.providerID,
          name: m.providerName,
          count: 1,
          isCustom: isCustomOrConfiguredProvider(m.providerID),
        });
      }
    }
    return Array.from(counts.values()).sort((a, b) => {
      if (a.isCustom !== b.isCustom) return a.isCustom ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
  });

  const activeModelInfo = createMemo<FlattenedModel | null>(() => {
    const sel = workspace.selectedModel;
    if (!sel) return null;
    const found = allModels().find(
      (m) => m.providerID === sel.providerID && m.modelID === sel.modelID
    );
    if (found) return found;
    return {
      providerID: sel.providerID,
      providerName: sel.providerID,
      modelID: sel.modelID,
      modelName: sel.modelID,
      connected: connectedSet().has(sel.providerID),
      isCustom: isCustomOrConfiguredProvider(sel.providerID),
      reasoning: false,
      image: false,
      toolcall: true,
      free: false,
    };
  });

  const filteredModels = createMemo<FlattenedModel[]>(() => {
    const q = query().trim();
    const pool = connectedModels();

    if (q) {
      return rankModelSearch(pool, q);
    }

    const r = rail();
    if (r === "favorites") {
      return pool.filter((m) => isFavoriteModel(m.providerID, m.modelID));
    }
    if (r === "recent") {
      const out: FlattenedModel[] = [];
      for (const rm of workspace.recentModels) {
        const found = pool.find(
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
        if (a.isCustom !== b.isCustom) return a.isCustom ? -1 : 1;
        return a.providerName.localeCompare(b.providerName);
      });
    }
    return pool.filter((m) => m.providerID === r);
  });

  const previewTarget = createMemo<FlattenedModel | null>(
    () => hoveredModel() || filteredModels()[activeIndex()] || activeModelInfo()
  );

  const toggleOpen = () => {
    const next = !open();
    setOpen(next);
    if (next) {
      setActiveIndex(0);
      setHoveredModel(null);
      setTimeout(() => searchInputRef?.focus(), 20);
    } else {
      setQuery("");
    }
  };

  const handleKeyDown = (e: KeyboardEvent) => {
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
      const path = typeof e.composedPath === "function" ? e.composedPath() : [];
      if (!path.includes(containerRef) && !containerRef.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    onCleanup(() => document.removeEventListener("mousedown", onDocClick));
  });

  return (
    <div ref={containerRef} class="relative">
      <button
        type="button"
        onClick={toggleOpen}
        class="inline-flex items-center gap-1.5 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] hover:border-[var(--border-strong)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-all cursor-pointer max-w-[250px]"
        title={t("timeline.selectModel")}
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
        <Show when={workspace.reasoningEffort}>
          <span class="shrink-0 rounded bg-[var(--accent-surface)] px-1 py-[1px] text-[9.5px] uppercase font-mono text-[var(--accent-primary)]">
            {workspace.reasoningEffort}
          </span>
        </Show>
        <ChevronDown size={12} class="shrink-0 opacity-60" />
      </button>

      <Show when={open()}>
        <div
          onKeyDown={handleKeyDown}
          class="absolute bottom-full left-0 z-40 mb-2 flex h-[410px] w-[520px] flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl"
        >
          <div class="flex flex-1 min-h-0">
            <ModelSelectorRail
              rail={rail()}
              hasQuery={Boolean(query().trim())}
              connectedTotal={connectedModels().length}
              providers={connectedProviderRails()}
              onSelectRail={(r) => {
                setRail(r);
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
                    setActiveIndex(0);
                  }}
                  class="w-full bg-transparent text-[12.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
                />
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
                        class="mt-2 inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-1.5 text-[11.5px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
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
                      const ctxBadge = () => formatTokenLimit(item.contextLimit);

                      return (
                        <div
                          onMouseEnter={() => {
                            setHoveredModel(item);
                            setActiveIndex(idx());
                          }}
                          class={`group flex items-center justify-between gap-2 rounded-xl px-2.5 py-1.5 transition-colors cursor-pointer ${
                            isSelected()
                              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                              : activeIndex() === idx()
                              ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
                              : "hover:bg-[var(--bg-hover)] text-[var(--text-primary)]"
                          }`}
                          onClick={() => {
                            selectModel({
                              providerID: item.providerID,
                              modelID: item.modelID,
                            });
                            setOpen(false);
                          }}
                        >
                          <div class="min-w-0 flex-1">
                            <div class="flex items-center gap-1.5">
                              <span class="truncate text-[12.5px] font-medium">
                                {item.modelName}
                              </span>
                              <Show when={item.reasoning}>
                                <span
                                  class="inline-flex items-center rounded bg-[var(--bg-inset)] px-1 py-[1px] text-[9.5px] font-mono text-[var(--text-muted)]"
                                  title={t("models.supportsReasoning")}
                                >
                                  <Sparkles size={9} class="mr-0.5 text-[var(--accent-primary)]" />
                                  {t("models.reasoningCap")}
                                </span>
                              </Show>
                              <Show when={item.image}>
                                <span class="inline-flex items-center rounded bg-[var(--bg-inset)] px-1 py-[1px] text-[9.5px] font-mono text-[var(--text-muted)]">
                                  <Image size={9} class="mr-0.5" />
                                  {t("models.imageCap")}
                                </span>
                              </Show>
                              <Show when={ctxBadge()}>
                                <span class="rounded bg-[var(--bg-inset)] px-1 py-[1px] text-[9.5px] font-mono text-[var(--text-faint)]">
                                  {ctxBadge()}
                                </span>
                              </Show>
                            </div>
                            <div class="flex items-center gap-1.5 text-[10.5px] text-[var(--text-muted)]">
                              <span class="truncate">{item.providerName}</span>
                              <span class="font-mono text-[10px] text-[var(--text-faint)] truncate">
                                · {item.modelID}
                              </span>
                            </div>
                          </div>

                          <div class="flex items-center gap-1 shrink-0">
                            <button
                              type="button"
                              onClick={(e) => {
                                e.stopPropagation();
                                toggleFavoriteModel(item.providerID, item.modelID);
                              }}
                              class={`rounded p-1 transition-opacity cursor-pointer ${
                                fav()
                                  ? "text-amber-500 opacity-100"
                                  : "text-[var(--text-faint)] opacity-0 group-hover:opacity-100 hover:text-[var(--text-primary)]"
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

              <Show when={previewTarget()}>
                {(model) => (
                  <ModelPreviewFooter
                    model={model()}
                    onEditCustom={(pid) => {
                      setOpen(false);
                      openProviderModal("custom", pid);
                    }}
                  />
                )}
              </Show>

              <div class="flex items-center justify-between border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 px-3 py-1.5 text-[11px]">
                <span class="text-[var(--text-muted)]">{t("models.reasoningEffort")}</span>
                <div class="inline-flex items-center gap-0.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] p-0.5">
                  <For each={EFFORT_OPTIONS}>
                    {(opt) => (
                      <button
                        type="button"
                        onClick={() => setReasoningEffort(opt.value)}
                        class={`rounded-md px-2 py-0.5 text-[10.5px] font-medium transition-colors cursor-pointer ${
                          workspace.reasoningEffort === opt.value
                            ? "bg-[var(--bg-panel)] text-[var(--accent-primary)] shadow-2xs"
                            : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                        }`}
                      >
                        {opt.label}
                      </button>
                    )}
                  </For>
                </div>
              </div>
            </div>
          </div>
        </div>
      </Show>
    </div>
  );
}
