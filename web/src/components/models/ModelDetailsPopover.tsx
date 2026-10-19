import { createSignal, onCleanup, onMount, Show } from "solid-js";
import { Portal } from "solid-js/web";
import { Image, Pencil, Sparkles, Wrench, X } from "lucide-solid";
import { t } from "../../i18n";
import {
  formatPricePerMillion,
  formatTokenLimit,
  getModelSourceInfo,
  type FlattenedModel,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { ProviderHealthBadge } from "./ProviderHealthBadge";

export function ModelDetailsPopover(props: {
  model: FlattenedModel;
  anchor: HTMLButtonElement;
  onClose: () => void;
  onEditCustom: (providerID: string) => void;
}) {
  let panel!: HTMLDivElement;
  const [position, setPosition] = createSignal({ left: 12, top: 12 });
  const source = () => getModelSourceInfo(props.model);
  const context = () => formatTokenLimit(props.model.contextLimit);
  const output = () => formatTokenLimit(props.model.outputLimit);
  const inputPrice = () => formatPricePerMillion(props.model.inputCost, props.model.priceCurrency);
  const outputPrice = () => formatPricePerMillion(props.model.outputCost, props.model.priceCurrency);

  function updatePosition() {
    const anchor = props.anchor.getBoundingClientRect();
    const bounds = panel.getBoundingClientRect();
    const maxLeft = Math.max(12, window.innerWidth - bounds.width - 12);
    const maxTop = Math.max(12, window.innerHeight - bounds.height - 12);
    const preferredLeft = anchor.right + 8;
    const left = preferredLeft <= maxLeft
      ? preferredLeft
      : anchor.left - bounds.width - 8;
    setPosition({
      left: Math.max(12, Math.min(left, maxLeft)),
      top: Math.max(12, Math.min(anchor.top, maxTop)),
    });
  }

  function closeAndFocus() {
    const anchor = props.anchor;
    props.onClose();
    anchor.focus({ preventScroll: true });
  }

  onMount(() => {
    updatePosition();
    panel.focus({ preventScroll: true });

    const onMouseDown = (event: MouseEvent) => {
      const target = event.target;
      if (target instanceof Node && !panel.contains(target) && !props.anchor.contains(target)) {
        props.onClose();
      }
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      closeAndFocus();
    };

    document.addEventListener("mousedown", onMouseDown);
    document.addEventListener("keydown", onKeyDown, true);
    window.addEventListener("resize", updatePosition);
    document.addEventListener("scroll", updatePosition, true);
    const observer = new ResizeObserver(updatePosition);
    observer.observe(panel);

    onCleanup(() => {
      document.removeEventListener("mousedown", onMouseDown);
      document.removeEventListener("keydown", onKeyDown, true);
      window.removeEventListener("resize", updatePosition);
      document.removeEventListener("scroll", updatePosition, true);
      observer.disconnect();
    });
  });

  return (
    <Portal>
      <div
        ref={panel}
        role="dialog"
        data-model-details
        aria-label={t("models.viewDetails")}
        tabIndex={-1}
        class="selectable-text select-text fixed z-[100] w-[360px] max-w-[calc(100vw-24px)] max-h-[calc(100vh-24px)] overflow-y-auto rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] p-4 text-[11.5px] text-[var(--text-secondary)] shadow-2xl focus:outline-none space-y-3 [overflow-wrap:anywhere]"
        style={{ left: `${position().left}px`, top: `${position().top}px` }}
      >
        <div class="flex items-start justify-between gap-3 border-b border-[var(--border-subtle)] pb-3">
          <div class="flex min-w-0 items-start gap-2">
            <ProviderIcon id={props.model.providerID} class="mt-0.5 h-4 w-4 shrink-0" />
            <h2 class="min-w-0 text-[13px] font-semibold text-[var(--text-primary)]">
              {props.model.modelName}
            </h2>
          </div>
          <button
            type="button"
            aria-label={t("common.close")}
            title={t("common.close")}
            onClick={closeAndFocus}
            class="shrink-0 cursor-pointer rounded p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] focus-visible:outline focus-visible:outline-[var(--accent-primary)]"
          >
            <X size={14} />
          </button>
        </div>

        <dl class="space-y-2.5">
          <div>
            <dt class="text-[var(--text-muted)]">{t("models.hoverProviderLabel")}</dt>
            <dd class="mt-0.5 font-medium text-[var(--text-primary)]">
              {props.model.providerName}
              <span class="ml-1.5 font-mono text-[10.5px] text-[var(--text-muted)]">
                ({props.model.providerID})
              </span>
            </dd>
          </div>
          <div>
            <dt class="text-[var(--text-muted)]">{t("models.hoverModelIdLabel")}</dt>
            <dd class="mt-0.5 font-mono">{props.model.modelID}</dd>
          </div>
          <div>
            <dt class="text-[var(--text-muted)]">{t("models.hoverSourceLabel")}</dt>
            <dd class="mt-1 space-y-1">
              <span class={`inline-block rounded-full px-2 py-0.5 text-[10.5px] font-medium ${
                source().isImported
                  ? "bg-[var(--warn-surface)] text-[var(--warn-text)]"
                  : "bg-[var(--ok-surface)] text-[var(--ok-text)]"
              }`}>
                {source().label}
              </span>
              <Show when={source().configHint}>
                <div class="font-mono text-[10.5px] text-[var(--text-muted)]">
                  {source().configHint}
                </div>
              </Show>
            </dd>
          </div>
          <Show when={props.model.baseURL}>
            <div>
              <dt class="text-[var(--text-muted)]">{t("models.hoverEndpointLabel")}</dt>
              <dd class="mt-0.5 whitespace-pre-wrap font-mono text-[10.5px]">{props.model.baseURL}</dd>
            </div>
          </Show>
        </dl>

        <section class="space-y-2 border-t border-[var(--border-subtle)] pt-3">
          <h3 class="font-medium text-[var(--text-primary)]">{t("models.hoverCapabilitiesLabel")}</h3>
          <dl class="flex flex-wrap gap-x-5 gap-y-2">
            <Show when={context()}>
              <div>
                <dt class="text-[var(--text-muted)]">{t("models.contextWindow")}</dt>
                <dd class="font-mono">{context()}</dd>
              </div>
            </Show>
            <Show when={output()}>
              <div>
                <dt class="text-[var(--text-muted)]">{t("models.maxOutput")}</dt>
                <dd class="font-mono">{output()}</dd>
              </div>
            </Show>
          </dl>
          <div class="flex flex-wrap gap-1.5">
            <Show when={props.model.reasoning}>
              <span class="inline-flex items-center gap-1 rounded bg-[var(--accent-surface)] px-2 py-1 text-[10px] font-medium text-[var(--accent-primary)]">
                <Sparkles size={11} />{t("models.reasoningCap")}
              </span>
            </Show>
            <Show when={props.model.image}>
              <span class="inline-flex items-center gap-1 rounded bg-[var(--bg-inset)] px-2 py-1 text-[10px]">
                <Image size={11} />{t("models.imageCap")}
              </span>
            </Show>
            <Show when={props.model.toolcall}>
              <span class="inline-flex items-center gap-1 rounded bg-[var(--bg-inset)] px-2 py-1 text-[10px]">
                <Wrench size={11} />{t("models.toolsCap")}
              </span>
            </Show>
          </div>
        </section>

        <Show when={props.model.free || props.model.multiplier || inputPrice() || outputPrice()}>
          <section class="space-y-2 border-t border-[var(--border-subtle)] pt-3">
            <h3 class="font-medium text-[var(--text-primary)]">{t("models.hoverPricingLabel")}</h3>
            <dl class="space-y-1.5">
              <Show when={props.model.free}>
                <div class="flex flex-wrap items-center justify-between gap-x-3">
                  <dt class="text-[var(--text-muted)]">{t("models.hoverPricingLabel")}</dt>
                  <dd>
                    <span class="inline-block rounded bg-[var(--ok-surface)] px-2 py-0.5 font-medium text-[var(--ok-text)]">
                      {t("models.freeModel")} ($0)
                    </span>
                  </dd>
                </div>
              </Show>
              <Show when={props.model.multiplier}>
                <div class="flex flex-wrap items-center justify-between gap-x-3">
                  <dt class="text-[var(--text-muted)]">{t("models.detailCreditMultiplier")}</dt>
                  <dd class="font-mono font-semibold text-[var(--text-primary)]">
                    {props.model.multiplier}
                  </dd>
                </div>
              </Show>
              <Show when={!props.model.free && inputPrice()}>
                <div class="flex flex-wrap justify-between gap-x-3">
                  <dt class="text-[var(--text-muted)]">{t("models.detailInputPrice")}</dt>
                  <dd class="font-mono">{inputPrice()}</dd>
                </div>
              </Show>
              <Show when={!props.model.free && outputPrice()}>
                <div class="flex flex-wrap justify-between gap-x-3">
                  <dt class="text-[var(--text-muted)]">{t("models.detailOutputPrice")}</dt>
                  <dd class="font-mono">{outputPrice()}</dd>
                </div>
              </Show>
            </dl>
          </section>
        </Show>

        <section class="space-y-2 border-t border-[var(--border-subtle)] pt-3">
          <h3 class="font-medium text-[var(--text-primary)]">{t("models.detailStatus")}</h3>
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-[var(--text-muted)]">
              {t(props.model.connected ? "models.connected" : "models.needsApiKey")}
            </span>
            <ProviderHealthBadge providerID={props.model.providerID} modelID={props.model.modelID} compact={false} />
          </div>
        </section>

        <Show when={props.model.isCustom}>
          <div class="border-t border-[var(--border-subtle)] pt-3">
            <button
              type="button"
              onClick={() => {
                const providerID = props.model.providerID;
                props.onClose();
                props.onEditCustom(providerID);
              }}
              class="inline-flex cursor-pointer items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-1.5 font-medium text-[var(--accent-primary)] hover:opacity-90 focus-visible:outline focus-visible:outline-[var(--accent-primary)]"
            >
              <Pencil size={12} />{t("models.editCustomEndpoint")}
            </button>
          </div>
        </Show>
      </div>
    </Portal>
  );
}
