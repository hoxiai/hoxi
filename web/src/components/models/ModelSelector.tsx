import { createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import {
  Check,
  ChevronDown,
  Cpu,
  KeyRound,
  Search,
  Sparkles,
  Star,
} from "lucide-solid";
import {
  isFavoriteModel,
  selectModel,
  setReasoningEffort,
  setWorkspace,
  toggleFavoriteModel,
  workspace,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";

interface FlattenedModel {
  providerID: string;
  providerName: string;
  modelID: string;
  modelName: string;
  connected: boolean;
  reasoning?: boolean;
  contextLimit?: number;
}

const EFFORT_OPTIONS: Array<{ value: "" | "low" | "medium" | "high"; label: string }> = [
  { value: "", label: "Auto" },
  { value: "low", label: "Low" },
  { value: "medium", label: "Med" },
  { value: "high", label: "High" },
];

function formatContext(n?: number): string | null {
  if (!n || n <= 0) return null;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(0)}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(n);
}

export default function ModelSelector() {
  const [open, setOpen] = createSignal(false);
  const [rail, setRail] = createSignal<string>("connected");
  const [query, setQuery] = createSignal("");
  let containerRef: HTMLDivElement | undefined;
  let searchInputRef: HTMLInputElement | undefined;

  const connectedSet = createMemo(() => new Set(workspace.connectedProviders));

  const allModels = createMemo<FlattenedModel[]>(() => {
    const out: FlattenedModel[] = [];
    const connected = connectedSet();
    for (const p of workspace.providers) {
      const isConn = connected.has(p.id);
      for (const [mID, mInfo] of Object.entries(p.models || {})) {
        out.push({
          providerID: p.id,
          providerName: p.name || p.id,
          modelID: mID,
          modelName: mInfo?.name || mID,
          connected: isConn,
          reasoning: mInfo?.reasoning,
          contextLimit: mInfo?.limit?.context,
        });
      }
    }
    return out;
  });

  const providerRails = createMemo(() => {
    const connected = connectedSet();
    return [...workspace.providers]
      .filter((p) => Object.keys(p.models || {}).length > 0)
      .sort((a, b) => {
        const ca = connected.has(a.id);
        const cb = connected.has(b.id);
        if (ca !== cb) return ca ? -1 : 1;
        return (a.name || a.id).localeCompare(b.name || b.id);
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
    };
  });

  const filteredModels = createMemo(() => {
    const q = query().trim().toLowerCase();
    const list = allModels();

    // If user is typing a search query, search across all models
    if (q) {
      return list.filter(
        (m) =>
          m.modelID.toLowerCase().includes(q) ||
          m.modelName.toLowerCase().includes(q) ||
          m.providerName.toLowerCase().includes(q) ||
          m.providerID.toLowerCase().includes(q)
      );
    }

    const r = rail();
    if (r === "favorites") {
      return list.filter((m) => isFavoriteModel(m.providerID, m.modelID));
    }
    if (r === "connected") {
      const conn = list.filter((m) => m.connected);
      return conn.length > 0 ? conn : list.slice(0, 40);
    }
    return list.filter((m) => m.providerID === r);
  });

  const toggleOpen = () => {
    const next = !open();
    setOpen(next);
    if (next) {
      if (workspace.favoriteModels.length > 0 && rail() === "connected" && workspace.connectedProviders.length === 0) {
        setRail("favorites");
      }
      setTimeout(() => searchInputRef?.focus(), 20);
    } else {
      setQuery("");
    }
  };

  onMount(() => {
    const onDocClick = (e: MouseEvent) => {
      if (!open() || !containerRef) return;
      if (!containerRef.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    onCleanup(() => document.removeEventListener("mousedown", onDocClick));
  });

  return (
    <div ref={containerRef} class="relative">
      {/* Trigger Pill inside Composer */}
      <button
        type="button"
        onClick={toggleOpen}
        class="inline-flex items-center gap-1.5 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] hover:border-[var(--border-strong)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-all cursor-pointer max-w-[230px]"
        title="Select model or configure providers"
      >
        <Show
          when={activeModelInfo()?.providerID}
          fallback={<Cpu size={12} class="shrink-0 text-[var(--accent-primary)]" />}
        >
          {(provID) => <ProviderIcon id={provID()} class="w-3.5 h-3.5 shrink-0" />}
        </Show>
        <span class="truncate">
          {activeModelInfo()?.modelName || "Select model"}
        </span>
        <Show when={workspace.reasoningEffort}>
          <span class="shrink-0 rounded bg-[var(--accent-surface)] px-1 py-[1px] text-[9.5px] uppercase font-mono text-[var(--accent-primary)]">
            {workspace.reasoningEffort}
          </span>
        </Show>
        <ChevronDown size={12} class="shrink-0 opacity-60" />
      </button>

      {/* Kilo Two-Column Model Selector Popover */}
      <Show when={open()}>
        <div class="absolute bottom-full left-0 z-40 mb-2 flex h-[340px] w-[460px] flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl">
          <div class="flex flex-1 min-h-0">
            {/* Left Provider Rail */}
            <div class="w-[152px] shrink-0 border-r border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 flex flex-col justify-between p-1.5">
              <div class="overflow-y-auto space-y-0.5 pr-0.5">
                <button
                  type="button"
                  onClick={() => setRail("favorites")}
                  class={`w-full flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[11.5px] transition-colors cursor-pointer ${
                    rail() === "favorites" && !query()
                      ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                      : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <Star size={12} class="shrink-0 text-amber-500" />
                  <span class="truncate">Favorites</span>
                </button>

                <button
                  type="button"
                  onClick={() => setRail("connected")}
                  class={`w-full flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[11.5px] transition-colors cursor-pointer ${
                    rail() === "connected" && !query()
                      ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                      : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <span class="h-1.5 w-1.5 rounded-full bg-[var(--ok)] shrink-0" />
                  <span class="truncate">Connected</span>
                </button>

                <div class="my-1 border-t border-[var(--border-subtle)]" />

                <For each={providerRails()}>
                  {(prov) => {
                    const isConn = () => connectedSet().has(prov.id);
                    return (
                      <button
                        type="button"
                        onClick={() => setRail(prov.id)}
                        class={`w-full flex items-center justify-between gap-1.5 rounded-lg px-2.5 py-1.5 text-left text-[11.5px] transition-colors cursor-pointer ${
                          rail() === prov.id && !query()
                            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                            : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
                        }`}
                      >
                        <span class="flex items-center gap-1.5 min-w-0 truncate">
                          <ProviderIcon id={prov.id} class="w-3.5 h-3.5 shrink-0" />
                          <span class="truncate">{prov.name || prov.id}</span>
                        </span>
                        <Show when={isConn()}>
                          <span class="h-1.5 w-1.5 rounded-full bg-[var(--ok)] shrink-0" />
                        </Show>
                      </button>
                    );
                  }}
                </For>
              </div>

              <button
                type="button"
                onClick={() => {
                  setOpen(false);
                  setWorkspace("providerModalOpen", true);
                }}
                class="mt-1 w-full inline-flex items-center justify-center gap-1.5 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1.5 text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 transition-opacity cursor-pointer"
              >
                <KeyRound size={11} />
                Manage Keys
              </button>
            </div>

            {/* Right Models Search & List */}
            <div class="flex-1 flex flex-col min-w-0">
              <div class="flex items-center gap-2 border-b border-[var(--border-subtle)] px-3 py-2">
                <Search size={13} class="text-[var(--text-faint)] shrink-0" />
                <input
                  ref={searchInputRef}
                  type="text"
                  placeholder="Search models…"
                  value={query()}
                  onInput={(e) => setQuery(e.currentTarget.value)}
                  class="w-full bg-transparent text-[12.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none selectable-text"
                />
              </div>

              <div class="flex-1 overflow-y-auto p-1.5 space-y-0.5">
                <Show
                  when={filteredModels().length > 0}
                  fallback={
                    <div class="flex h-full flex-col items-center justify-center p-4 text-center text-[12px] text-[var(--text-muted)]">
                      <p>No models found in this view.</p>
                      <button
                        type="button"
                        onClick={() => {
                          setOpen(false);
                          setWorkspace("providerModalOpen", true);
                        }}
                        class="mt-2 inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-1.5 text-[11.5px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                      >
                        <KeyRound size={12} />
                        Connect Provider / Add Custom Model
                      </button>
                    </div>
                  }
                >
                  <For each={filteredModels()}>
                    {(item) => {
                      const isSelected = () =>
                        workspace.selectedModel?.providerID === item.providerID &&
                        workspace.selectedModel?.modelID === item.modelID;
                      const fav = () => isFavoriteModel(item.providerID, item.modelID);
                      const ctxBadge = () => formatContext(item.contextLimit);

                      return (
                        <div
                          class={`group flex items-center justify-between gap-2 rounded-xl px-2.5 py-2 transition-colors cursor-pointer ${
                            isSelected()
                              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
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
                                  title="Supports extended reasoning"
                                >
                                  <Sparkles size={9} class="mr-0.5 text-[var(--accent-primary)]" />
                                  Reason
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
                              <Show when={!item.connected}>
                                <span class="text-[var(--warn-text)]">· Needs API Key</span>
                              </Show>
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
                              title={fav() ? "Remove from favorites" : "Add to favorites"}
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

              {/* Bottom Reasoning Effort Bar */}
              <div class="flex items-center justify-between border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 px-3 py-1.5 text-[11px]">
                <span class="text-[var(--text-muted)]">Reasoning Effort</span>
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
