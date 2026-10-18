// web/src/components/models/model-selector-utils.tsx
import { For, Show } from "solid-js";
import {
  Clock,
  Image,
  KeyRound,
  Pencil,
  Plus,
  Sparkles,
  Star,
  Wrench,
} from "lucide-solid";
import {
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
import { ProviderHealthBadge } from "./ProviderQualityCard";

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

export function ModelSelectorRail(props: {
  rail: string;
  hasQuery: boolean;
  connectedTotal: number;
  providers: Array<{ id: string; name: string; count: number; isCustom: boolean }>;
  onSelectRail: (rail: string) => void;
  onClosePopover: () => void;
}) {
  return (
    <div class="w-[172px] shrink-0 border-r border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 flex flex-col justify-between p-1.5">
      <div class="overflow-y-auto space-y-0.5 pr-0.5">
        <button
          type="button"
          onClick={() => props.onSelectRail("connected")}
          class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[11.5px] transition-colors cursor-pointer ${
            props.rail === "connected" && !props.hasQuery
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
        >
          <span class="flex items-center gap-2 truncate">
            <span class="h-1.5 w-1.5 rounded-full bg-[var(--ok)] shrink-0" />
            <span class="truncate">{t("models.allConnected")}</span>
          </span>
          <span class="font-mono text-[10px] opacity-70">{props.connectedTotal}</span>
        </button>

        <button
          type="button"
          onClick={() => props.onSelectRail("favorites")}
          class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[11.5px] transition-colors cursor-pointer ${
            props.rail === "favorites" && !props.hasQuery
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
        >
          <span class="flex items-center gap-2 truncate">
            <Star size={12} class="shrink-0 text-amber-500" />
            <span class="truncate">{t("models.favorites")}</span>
          </span>
          <span class="font-mono text-[10px] opacity-70">{workspace.favoriteModels.length}</span>
        </button>

        <button
          type="button"
          onClick={() => props.onSelectRail("recent")}
          class={`w-full flex items-center justify-between gap-2 rounded-lg px-2.5 py-1.5 text-left text-[11.5px] transition-colors cursor-pointer ${
            props.rail === "recent" && !props.hasQuery
              ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] font-medium"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
        >
          <span class="flex items-center gap-2 truncate">
            <Clock size={12} class="shrink-0 text-[var(--text-muted)]" />
            <span class="truncate">{t("models.recent")}</span>
          </span>
          <span class="font-mono text-[10px] opacity-70">{workspace.recentModels.length}</span>
        </button>

        <div class="my-1 border-t border-[var(--border-subtle)]" />

        <For each={props.providers}>
          {(prov) => (
            <div
              class={`group flex items-center justify-between gap-1 rounded-lg px-2 py-1.5 text-[11.5px] transition-colors cursor-pointer ${
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
                <span class="font-mono text-[10px] opacity-65">{prov.count}</span>
              </span>
            </div>
          )}
        </For>
      </div>

      <div class="mt-1 space-y-1 pt-1 border-t border-[var(--border-subtle)]">
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

export function ModelPreviewFooter(props: {
  model: FlattenedModel;
  onEditCustom: (providerID: string) => void;
}) {
  const ctxText = () => formatTokenLimit(props.model.contextLimit);
  const outText = () => formatTokenLimit(props.model.outputLimit);
  const inPrice = () => formatPricePerMillion(props.model.inputCost);
  const outPrice = () => formatPricePerMillion(props.model.outputCost);
  const srcInfo = () => getModelSourceInfo(props.model);

  return (
    <div class="border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/60 px-3 py-2 text-[11px] space-y-1">
      <div class="flex items-center justify-between gap-2">
        <div class="min-w-0 flex items-center gap-1.5">
          <span class="font-semibold text-[var(--text-primary)] truncate">
            {props.model.modelName}
          </span>
          <span
            class={`shrink-0 inline-flex items-center gap-1 rounded-full px-1.5 py-[1px] text-[9.5px] font-medium ${
              srcInfo().isImported
                ? "bg-amber-500/10 text-amber-400"
                : "bg-[var(--ok-surface)] text-[var(--ok-text)]"
            }`}
          >
            <span
              class={`h-1.5 w-1.5 rounded-full ${
                srcInfo().isImported ? "bg-amber-400" : "bg-[var(--ok)]"
              }`}
            />
            {srcInfo().label}
          </span>
          <ProviderHealthBadge
            providerID={props.model.providerID}
            modelID={props.model.modelID}
          />
          <span class="font-mono text-[10px] text-[var(--text-faint)] truncate">
            ({props.model.providerID}/{props.model.modelID})
          </span>
        </div>
        <Show when={props.model.isCustom}>
          <button
            type="button"
            onClick={() => props.onEditCustom(props.model.providerID)}
            class="shrink-0 inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-primary)] hover:border-[var(--accent-border)] cursor-pointer"
          >
            <Pencil size={10} />
            {t("models.editProvider")}
          </button>
        </Show>
      </div>

      <div class="flex items-center justify-between gap-2 text-[10.5px] text-[var(--text-muted)]">
        <div class="flex items-center gap-2 flex-wrap">
          <Show when={ctxText()}>
            <span>
              {t("models.contextWindow")}: <strong class="font-mono">{ctxText()}</strong>
            </span>
          </Show>
          <Show when={outText()}>
            <span>
              {t("models.maxOutput")}: <strong class="font-mono">{outText()}</strong>
            </span>
          </Show>
          <Show
            when={!props.model.free && (inPrice() || outPrice())}
            fallback={
              <Show when={props.model.free}>
                <span class="rounded bg-[var(--ok-surface)] px-1.5 py-[1px] text-[10px] font-medium text-[var(--ok-text)]">
                  {t("models.freeModel")}
                </span>
              </Show>
            }
          >
            <span class="font-mono">
              {inPrice() || "-"} in / {outPrice() || "-"} out
            </span>
          </Show>
        </div>

        <div class="flex items-center gap-1 shrink-0">
          <Show when={props.model.reasoning}>
            <span class="inline-flex items-center gap-0.5 rounded bg-[var(--accent-surface)] px-1.5 py-[1px] text-[9.5px] font-medium text-[var(--accent-primary)]">
              <Sparkles size={9} />
              {t("models.reasoningCap")}
            </span>
          </Show>
          <Show when={props.model.image}>
            <span class="inline-flex items-center gap-0.5 rounded bg-[var(--bg-inset)] px-1.5 py-[1px] text-[9.5px] font-medium text-[var(--text-secondary)]">
              <Image size={9} />
              {t("models.imageCap")}
            </span>
          </Show>
          <Show when={props.model.toolcall}>
            <span class="inline-flex items-center gap-0.5 rounded bg-[var(--bg-inset)] px-1.5 py-[1px] text-[9.5px] text-[var(--text-muted)]">
              <Wrench size={9} />
              {t("models.toolsCap")}
            </span>
          </Show>
        </div>
      </div>
    </div>
  );
}
