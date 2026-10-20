// web/src/components/models/model-selector-utils.tsx
import { For, Show } from "solid-js";
import {
  Clock,
  KeyRound,
  Pencil,
  Plus,
  Star,
  Zap,
} from "lucide-solid";
import {
  AUTO_PROVIDER_ID,
  type ConnectedProviderGroup,
  extractFlattenedModels,
  type FlattenedModel,
  formatPricePerMillion,
  formatTokenLimit,
  getConnectedProviderModelGroups,
  getModelSourceInfo,
  isModelFree,
  type ModelSourceInfo,
  openProviderModal,
  rankModelSearch,
  workspace,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";

export {
  type ConnectedProviderGroup,
  extractFlattenedModels,
  type FlattenedModel,
  formatPricePerMillion,
  formatTokenLimit,
  getConnectedProviderModelGroups,
  getModelSourceInfo,
  isModelFree,
  type ModelSourceInfo,
  rankModelSearch,
};

export function getMultiplierBadgeClass(multiplier?: string, free?: boolean): string {
  if (!multiplier) return "bg-[var(--bg-inset)] text-[var(--text-secondary)]";
  const num = parseFloat(multiplier.replace(/[^\d.]/g, ""));
  if (free || multiplier === "0x" || num === 0) {
    return "bg-[var(--ok-surface)] text-[var(--ok-text)]";
  }
  if (!Number.isNaN(num) && num <= 0.35) {
    return "bg-emerald-500/12 text-emerald-500";
  }
  if (!Number.isNaN(num) && num > 1.0) {
    return "bg-amber-500/15 text-amber-500";
  }
  return "bg-[var(--bg-inset)] text-[var(--text-secondary)]";
}

export function ModelSelectorRail(props: {
  rail: string;
  hasQuery: boolean;
  connectedTotal: number;
  autoTotal: number;
  freeTotal: number;
  providers: Array<{ id: string; name: string; count: number; isCustom: boolean }>;
  onSelectRail: (rail: string) => void;
  onClosePopover: () => void;
}) {
  return (
    <div class="w-[172px] shrink-0 border-r border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 flex flex-col justify-between p-1.5">
      <div class="min-h-0 flex-1 overflow-y-auto space-y-0.5 pr-0.5">
        <button
          type="button"
          onClick={() => props.onSelectRail("connected")}
          class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[12px] transition-colors cursor-pointer ${
            props.rail === "connected" && !props.hasQuery
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
        >
          <span class="flex items-center gap-2 truncate">
            <span class="h-1.5 w-1.5 rounded-full bg-[var(--ok)] shrink-0" />
            <span class="truncate">{t("models.allConnected")}</span>
          </span>
          <span class="font-mono text-[11px] opacity-70">{props.connectedTotal}</span>
        </button>

        <div
          onClick={() => props.onSelectRail(AUTO_PROVIDER_ID)}
          class={`group w-full flex items-center justify-between gap-1 rounded-lg px-2.5 py-1.5 text-left text-[12px] transition-colors cursor-pointer ${
            props.rail === AUTO_PROVIDER_ID && !props.hasQuery
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
        >
          <span class="flex items-center gap-2 truncate">
            <Zap size={12} class="shrink-0 text-[var(--accent-primary)]" />
            <span class="truncate">{t("models.tabAutoPools")}</span>
          </span>
          <span class="flex items-center gap-1 shrink-0">
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                props.onClosePopover();
                openProviderModal("auto", null);
              }}
              class="opacity-0 group-hover:opacity-100 rounded p-0.5 hover:bg-[var(--bg-panel)] text-[var(--text-muted)] hover:text-[var(--accent-primary)] cursor-pointer"
              title={t("models.tabAutoPools")}
            >
              <Pencil size={10} />
            </button>
            <span class="font-mono text-[11px] opacity-70">{props.autoTotal}</span>
          </span>
        </div>

        <button
          type="button"
          onClick={() => props.onSelectRail("free")}
          class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[12px] transition-colors cursor-pointer ${
            props.rail === "free" && !props.hasQuery
              ? "bg-[var(--ok-surface)] text-[var(--ok-text)] font-medium"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
        >
          <span class="flex items-center gap-1.5 truncate">
            <span class="rounded bg-[var(--ok-surface)] px-1 py-[0.5px] font-mono text-[11px] font-semibold text-[var(--ok-text)] shrink-0">
              0x
            </span>
            <span class="truncate">{t("models.freeModel")}</span>
          </span>
          <span class="font-mono text-[11px] opacity-70">{props.freeTotal}</span>
        </button>

        <button
          type="button"
          onClick={() => props.onSelectRail("favorites")}
          class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[12px] transition-colors cursor-pointer ${
            props.rail === "favorites" && !props.hasQuery
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
        >
          <span class="flex items-center gap-2 truncate">
            <Star size={12} class="shrink-0 text-amber-500" />
            <span class="truncate">{t("models.favorites")}</span>
          </span>
          <span class="font-mono text-[11px] opacity-70">{workspace.favoriteModels.length}</span>
        </button>

        <button
          type="button"
          onClick={() => props.onSelectRail("recent")}
          class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[12px] transition-colors cursor-pointer ${
            props.rail === "recent" && !props.hasQuery
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
        >
          <span class="flex items-center gap-2 truncate">
            <Clock size={12} class="shrink-0 text-[var(--text-muted)]" />
            <span class="truncate">{t("models.recent")}</span>
          </span>
          <span class="font-mono text-[11px] opacity-70">{workspace.recentModels.length}</span>
        </button>

        <div class="my-1 border-t border-[var(--border-subtle)]" />

        <For each={props.providers}>
          {(prov) => (
            <div
              class={`group flex items-center justify-between gap-1 rounded-lg px-2 py-1.5 text-[12px] transition-colors cursor-pointer ${
                props.rail === prov.id && !props.hasQuery
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
                  : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
              }`}
              onClick={() => props.onSelectRail(prov.id)}
            >
              <span class="flex items-center gap-1.5 min-w-0 truncate">
                <ProviderIcon id={prov.id} class="w-3.5 h-3.5 shrink-0" />
                <span class="truncate">{prov.name}</span>
              </span>
              <span class="flex items-center gap-1 shrink-0">
                <Show when={prov.isCustom}>
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation();
                      props.onClosePopover();
                      openProviderModal("custom", prov.id);
                    }}
                    class="opacity-0 group-hover:opacity-100 rounded p-0.5 hover:bg-[var(--bg-panel)] text-[var(--text-muted)] hover:text-[var(--accent-primary)] cursor-pointer"
                    title={t("models.editProvider")}
                  >
                    <Pencil size={10} />
                  </button>
                </Show>
                <span class="font-mono text-[11px] opacity-65">{prov.count}</span>
              </span>
            </div>
          )}
        </For>
      </div>

      <div class="mt-1 shrink-0 space-y-1 pt-1 border-t border-[var(--border-subtle)]">
        <button
          type="button"
          onClick={() => {
            props.onClosePopover();
            openProviderModal("custom", null);
          }}
          class="w-full inline-flex items-center justify-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
        >
          <Plus size={11} />
          {t("models.tabCustom")}
        </button>
        <button
          type="button"
          onClick={() => {
            props.onClosePopover();
            openProviderModal("providers", null);
          }}
          class="w-full inline-flex items-center justify-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-1.5 text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 transition-opacity cursor-pointer"
        >
          <KeyRound size={11} />
          {t("models.manageKeys")}
        </button>
      </div>
    </div>
  );
}
