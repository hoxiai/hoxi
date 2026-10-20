// web/src/components/models/SelectedModelHoverCard.tsx
import { Show } from "solid-js";
import { Image, Sparkles, Wrench } from "lucide-solid";
import { workspace } from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";
import { ProviderHealthBadge } from "./ProviderQualityCard";
import {
  formatPricePerMillion,
  formatTokenLimit,
  getModelSourceInfo,
  getMultiplierBadgeClass,
  type FlattenedModel,
} from "./model-selector-utils";

export function SelectedModelHoverCard(props: { model: FlattenedModel }) {
  const srcInfo = () => getModelSourceInfo(props.model);
  const ctxText = () => formatTokenLimit(props.model.contextLimit);
  const outText = () => formatTokenLimit(props.model.outputLimit);
  const inPrice = () => formatPricePerMillion(props.model.inputCost, props.model.priceCurrency);
  const outPrice = () => formatPricePerMillion(props.model.outputCost, props.model.priceCurrency);

  return (
    <div class="pointer-events-none absolute bottom-full left-0 z-30 mb-2 w-[320px] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] p-3 shadow-2xl text-[11.5px] space-y-2 animate-in fade-in duration-100">
      <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)] pb-2">
        <div class="flex items-center gap-1.5 min-w-0">
          <ProviderIcon id={props.model.providerID} class="w-4 h-4 shrink-0" />
          <span class="font-semibold text-[var(--text-primary)] truncate text-[12.5px]">
            {props.model.modelName}
          </span>
          <Show when={props.model.free}>
            <span class="shrink-0 rounded bg-[var(--ok-surface)] px-1.5 py-[1px] text-[10px] font-semibold text-[var(--ok-text)]">
              {t("models.freeModel")}
            </span>
          </Show>
          <Show when={props.model.multiplier}>
            <span
              class={`shrink-0 rounded px-1.5 py-[1px] font-mono text-[10px] font-semibold ${getMultiplierBadgeClass(
                props.model.multiplier,
                props.model.free
              )}`}
            >
              {props.model.multiplier}
            </span>
          </Show>
        </div>
        <ProviderHealthBadge
          providerID={props.model.providerID}
          modelID={props.model.modelID}
          compact
        />
      </div>

      <div class="space-y-1.5 text-[11px]">
        <div class="flex items-center justify-between gap-2">
          <span class="text-[var(--text-muted)] shrink-0">
            {t("models.hoverSourceLabel")}
          </span>
          <span class="inline-flex items-center gap-1.5 min-w-0">
            <span
              class={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${
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
            <Show when={srcInfo().configHint}>
              <span class="font-mono text-[10px] text-[var(--text-faint)] truncate">
                {srcInfo().configHint}
              </span>
            </Show>
          </span>
        </div>

        <div class="flex items-center justify-between gap-2">
          <span class="text-[var(--text-muted)] shrink-0">
            {t("models.hoverProviderLabel")}
          </span>
          <span class="text-[var(--text-primary)] font-medium truncate">
            {props.model.providerName}{" "}
            <span class="font-mono text-[10px] text-[var(--text-faint)]">
              ({props.model.providerID})
            </span>
          </span>
        </div>

        <div class="flex items-center justify-between gap-2">
          <span class="text-[var(--text-muted)] shrink-0">
            {t("models.hoverModelIdLabel")}
          </span>
          <span class="font-mono text-[10.5px] text-[var(--text-secondary)] truncate">
            {props.model.modelID}
          </span>
        </div>

        <Show when={props.model.baseURL}>
          <div class="flex items-center justify-between gap-2">
            <span class="text-[var(--text-muted)] shrink-0">
              {t("models.hoverEndpointLabel")}
            </span>
            <span class="font-mono text-[10px] text-[var(--text-secondary)] truncate max-w-[205px]">
              {props.model.baseURL}
            </span>
          </div>
        </Show>

        <div class="flex items-center justify-between gap-2 pt-1.5 border-t border-[var(--border-subtle)] text-[10.5px] text-[var(--text-muted)]">
          <div class="flex items-center gap-2 flex-wrap">
            <Show when={ctxText()}>
              <span>
                {t("models.contextWindow")}:{" "}
                <strong class="font-mono text-[var(--text-secondary)]">
                  {ctxText()}
                </strong>
              </span>
            </Show>
            <Show when={outText()}>
              <span>
                {t("models.maxOutput")}:{" "}
                <strong class="font-mono text-[var(--text-secondary)]">
                  {outText()}
                </strong>
              </span>
            </Show>
            <Show when={!props.model.free && (inPrice() || outPrice())}>
              <span class="font-mono text-[10px]">
                {inPrice() || "-"} / {outPrice() || "-"}
              </span>
            </Show>
          </div>

          <div class="flex items-center gap-1 shrink-0">
            <Show when={props.model.reasoning}>
              <span class="inline-flex items-center gap-0.5 rounded bg-[var(--accent-surface)] px-1.5 py-[1px] text-[9.5px] font-medium text-[var(--accent-primary)]">
                <Sparkles size={9} />
                {t("models.reasoningCap")}
                <Show when={workspace.reasoningEffort || props.model.defaultEffort}>
                  {(eff) => (
                    <span class="uppercase font-mono">
                      ·{eff()}
                    </span>
                  )}
                </Show>
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
    </div>
  );
}
