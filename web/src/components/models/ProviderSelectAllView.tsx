// web/src/components/models/ProviderSelectAllView.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { ArrowLeft, Search } from "lucide-solid";
import type { ProviderInfo } from "../../services/api";
import { POPULAR_PROVIDER_IDS } from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";

export function ProviderSelectAllView(props: {
  providers: ProviderInfo[];
  connectedSet: Set<string>;
  disabledSet: Set<string>;
  onSelect: (providerID: string) => void;
  onBack: () => void;
}) {
  const [search, setSearch] = createSignal("");

  const filtered = createMemo(() => {
    const q = search().trim().toLowerCase();
    const popularSet = new Set<string>(POPULAR_PROVIDER_IDS);
    const list = props.providers
      .filter((p) => !props.disabledSet.has(p.id))
      .sort((a, b) => {
        const ap = popularSet.has(a.id);
        const bp = popularSet.has(b.id);
        if (ap !== bp) return ap ? -1 : 1;
        return (a.name || a.id).localeCompare(b.name || b.id);
      });
    if (!q) return list;
    return list.filter(
      (p) => p.id.toLowerCase().includes(q) || (p.name || "").toLowerCase().includes(q)
    );
  });

  return (
    <div class="flex-1 flex flex-col min-h-0 p-6 sm:p-8">
      <div class="mx-auto w-full max-w-5xl flex-1 flex flex-col min-h-0 space-y-4">
        <div class="flex items-center justify-between gap-3">
          <button
            type="button"
            onClick={() => props.onBack()}
            class="inline-flex items-center gap-1.5 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <ArrowLeft size={14} />
            {t("models.tabCatalog")}
          </button>
          <div class="flex-1 max-w-[320px] flex items-center gap-2 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] px-3 py-1.5">
            <Search size={13} class="text-[var(--text-faint)] shrink-0" />
            <input
              type="text"
              placeholder={t("models.searchProviders")}
              value={search()}
              onInput={(e) => setSearch(e.currentTarget.value)}
              class="w-full bg-transparent text-[13px] text-[var(--text-primary)] outline-none"
            />
          </div>
        </div>

        <div class="flex-1 overflow-y-auto grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 content-start gap-2.5 pr-1">
        <For each={filtered()}>
          {(item) => {
            const isConn = () => props.connectedSet.has(item.id);
            return (
              <button
                type="button"
                onClick={() => props.onSelect(item.id)}
                class="flex items-center justify-between gap-2.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 px-3.5 py-2.5 text-left hover:border-[var(--accent-border)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              >
                <div class="flex items-center gap-2.5 min-w-0">
                  <ProviderIcon id={item.id} class="w-4 h-4 shrink-0" />
                  <div class="min-w-0">
                    <div class="truncate text-[13px] font-medium text-[var(--text-primary)]">
                      {item.name || item.id}
                    </div>
                    <div class="truncate font-mono text-[11px] text-[var(--text-faint)]">
                      {item.id} ·{" "}
                      {t("models.modelsCount", {
                        count: Object.keys(item.models || {}).length,
                      })}
                    </div>
                  </div>
                </div>
                <Show
                  when={isConn()}
                  fallback={
                    <span class="shrink-0 text-[11px] font-medium text-[var(--accent-primary)]">
                      {t("models.connect")}
                    </span>
                  }
                >
                  <span class="shrink-0 rounded-full bg-[var(--ok-surface)] px-2 py-0.5 text-[11px] font-medium text-[var(--ok-text)]">
                    {t("models.connected")}
                  </span>
                </Show>
              </button>
            );
          }}
        </For>
        </div>
      </div>
    </div>
  );
}
