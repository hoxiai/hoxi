// web/src/components/models/OrchestratorSelectorRow.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { Check, ChevronDown, Layers, RotateCcw, Settings2 } from "lucide-solid";
import {
  clearSessionOrchestratorOverride,
  formatOrchestratorModelLabel,
  getEffectiveOrchestratorPair,
  inferModelEffortCapabilities,
  isOrchestratorConfigured,
  isOrchestratorModel,
  openProviderModal,
  ORCHESTRATOR_MODEL_ID,
  ORCHESTRATOR_PROVIDER_ID,
  parseModelString,
  selectModel,
  updateSessionOrchestratorOverride,
  workspace,
  type ConcreteReasoningEffort,
  type ReasoningEffort,
} from "../../store/workspace";
import { t } from "../../i18n";
import { getInternalLLMProviderModelGroups } from "./model-selector-utils";
import ModelRolePicker from "./ModelRolePicker";

function inlineEffortLabel(level: ConcreteReasoningEffort): string {
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

function inlineEffortOptions(fullKey: string): readonly ConcreteReasoningEffort[] {
  const parsed = parseModelString(fullKey);
  if (!parsed) return [];
  const prov = workspace.providers.find((p) => p.id === parsed.providerID);
  const info = prov?.models?.[parsed.modelID];
  return inferModelEffortCapabilities(
    parsed.providerID,
    parsed.modelID,
    Boolean(info?.reasoning ?? true)
  ).efforts;
}

export function OrchestratorSelectorRow(props: {
  onClosePopover: () => void;
}) {
  const [expanded, setExpanded] = createSignal(false);
  const nativeGroups = createMemo(() => getInternalLLMProviderModelGroups());
  const pair = createMemo(() => getEffectiveOrchestratorPair(workspace.activeSessionID));
  const configured = createMemo(() => isOrchestratorConfigured(workspace.activeSessionID));
  const selected = createMemo(() => isOrchestratorModel(workspace.selectedModel));

  const subtitle = createMemo(() => {
    const p = pair();
    if (!p.orchestratorModel || !p.executorModel) {
      return t("models.orchestratorRowSubtitleUnset");
    }
    const orchName = formatOrchestratorModelLabel(p.orchestratorModel);
    const execName = formatOrchestratorModelLabel(p.executorModel);
    const orchTag = p.orchestratorEffort ? `${orchName} (${p.orchestratorEffort.toUpperCase()})` : orchName;
    const execTag = p.executorEffort ? `${execName} (${p.executorEffort.toUpperCase()})` : execName;
    return t("models.orchestratorRowSubtitlePair", {
      orchestrator: orchTag,
      executor: execTag,
    });
  });

  const handleActivate = () => {
    if (!configured()) {
      setExpanded(true);
      return;
    }
    selectModel({
      providerID: ORCHESTRATOR_PROVIDER_ID,
      modelID: ORCHESTRATOR_MODEL_ID,
    });
    props.onClosePopover();
  };

  return (
    <div class="mb-1 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/45 overflow-hidden">
      <div
        class={`group flex items-center justify-between gap-2 px-2.5 py-2 transition-colors ${
          selected()
            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
            : "hover:bg-[var(--bg-hover)] text-[var(--text-primary)]"
        }`}
      >
        <button
          type="button"
          onClick={handleActivate}
          class="min-w-0 flex-1 text-left cursor-pointer"
        >
          <span class="flex items-center gap-1.5">
            <Layers size={13} class="shrink-0 text-[var(--accent-primary)]" />
            <span class="truncate text-[13px] font-semibold">
              {t("models.orchestratorTitle")}
            </span>
            <Show when={pair().hasOverride}>
              <span class="shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-[1px] text-[10px] font-medium text-[var(--text-muted)]">
                {t("models.orchestratorPerSessionOverride")}
              </span>
            </Show>
          </span>
          <span class="block truncate text-[11px] text-[var(--text-muted)] mt-0.5">
            {subtitle()}
          </span>
        </button>

        <div class="flex items-center gap-1 shrink-0">
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              setExpanded((v) => !v);
            }}
            title={t("models.orchestratorPerSessionOverride")}
            aria-expanded={expanded()}
            class="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-panel)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <ChevronDown
              size={13}
              class={`transition-transform duration-150 ${expanded() ? "rotate-180" : ""}`}
            />
          </button>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              props.onClosePopover();
              openProviderModal("models", null);
            }}
            title={t("models.orchestratorSettingsTitle")}
            class="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-panel)] hover:text-[var(--accent-primary)] cursor-pointer"
          >
            <Settings2 size={13} />
          </button>
          <Show when={selected()}>
            <Check size={13} class="text-[var(--accent-primary)]" />
          </Show>
        </div>
      </div>

      <Show when={expanded()}>
        <div
          onClick={(e) => e.stopPropagation()}
          class="border-t border-[var(--border-subtle)] bg-[var(--bg-panel)]/70 px-2.5 py-2 space-y-2 text-[11px]"
        >
          <div class="flex items-center justify-between gap-2">
            <span class="text-[var(--text-muted)] font-medium shrink-0">
              {t("models.orchestratorHoverBrainLabel")}
            </span>
            <div class="flex items-center gap-1.5 min-w-0">
              <ModelRolePicker
                value={pair().orchestratorModel}
                groups={nativeGroups()}
                allowEmpty
                emptyLabel={t("models.orchestratorSelectModelPlaceholder")}
                onChange={(val) =>
                  updateSessionOrchestratorOverride({
                    orchestratorModel: val || undefined,
                    ...(val ? {} : { orchestratorEffort: undefined }),
                  })
                }
              />
              <Show when={pair().orchestratorModel && inlineEffortOptions(pair().orchestratorModel).length > 0}>
                <select
                  value={pair().orchestratorEffort}
                  onChange={(e) =>
                    updateSessionOrchestratorOverride({
                      orchestratorEffort: e.currentTarget.value as ReasoningEffort,
                    })
                  }
                  aria-label={t("models.reasoningEffort")}
                  class="h-[28px] rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-1.5 text-[11px] text-[var(--text-primary)] outline-none cursor-pointer"
                >
                  <option value="">{t("models.effortAuto")}</option>
                  <For each={inlineEffortOptions(pair().orchestratorModel)}>
                    {(lvl) => <option value={lvl}>{inlineEffortLabel(lvl)}</option>}
                  </For>
                </select>
              </Show>
            </div>
          </div>
          <div class="flex items-center justify-between gap-2">
            <span class="text-[var(--text-muted)] font-medium shrink-0">
              {t("models.orchestratorHoverExecutorLabel")}
            </span>
            <div class="flex items-center gap-1.5 min-w-0">
              <ModelRolePicker
                value={pair().executorModel}
                groups={nativeGroups()}
                allowEmpty
                emptyLabel={t("models.orchestratorSelectModelPlaceholder")}
                onChange={(val) =>
                  updateSessionOrchestratorOverride({
                    executorModel: val || undefined,
                    ...(val ? {} : { executorEffort: undefined }),
                  })
                }
              />
              <Show when={pair().executorModel && inlineEffortOptions(pair().executorModel).length > 0}>
                <select
                  value={pair().executorEffort}
                  onChange={(e) =>
                    updateSessionOrchestratorOverride({
                      executorEffort: e.currentTarget.value as ReasoningEffort,
                    })
                  }
                  aria-label={t("models.reasoningEffort")}
                  class="h-[28px] rounded-lg border border-[var(--border-strong)] bg-[var(--bg-panel)] px-1.5 text-[11px] text-[var(--text-primary)] outline-none cursor-pointer"
                >
                  <option value="">{t("models.effortAuto")}</option>
                  <For each={inlineEffortOptions(pair().executorModel)}>
                    {(lvl) => <option value={lvl}>{inlineEffortLabel(lvl)}</option>}
                  </For>
                </select>
              </Show>
            </div>
          </div>
          <div class="flex items-center justify-between gap-2 pt-0.5">
            <Show
              when={pair().hasOverride}
              fallback={
                <span class="text-[10px] text-[var(--text-faint)]">
                  {t("models.orchestratorNativeOnlyHint")}
                </span>
              }
            >
              <button
                type="button"
                onClick={() => clearSessionOrchestratorOverride()}
                class="inline-flex items-center gap-1 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <RotateCcw size={11} />
                <span>{t("models.orchestratorResetOverride")}</span>
              </button>
            </Show>
            <Show when={configured()}>
              <button
                type="button"
                onClick={handleActivate}
                class="rounded-lg bg-[var(--accent-surface)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
              >
                {t("models.orchestratorUseBtn")}
              </button>
            </Show>
          </div>
        </div>
      </Show>
    </div>
  );
}
