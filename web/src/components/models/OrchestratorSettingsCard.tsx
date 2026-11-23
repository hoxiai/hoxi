// web/src/components/models/OrchestratorSettingsCard.tsx
import { createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import { AlertTriangle, Check, Layers, LoaderCircle, RotateCcw } from "lucide-solid";
import {
  fetchOrchestratorConfig,
  inferModelEffortCapabilities,
  isOrchestratorModel,
  ORCHESTRATOR_MODEL_ID,
  ORCHESTRATOR_PROVIDER_ID,
  orchestratorConfig,
  parseModelString,
  selectModel,
  updateOrchestratorConfig,
  workspace,
  type ConcreteReasoningEffort,
  type OrchestratorConfig,
  type ReasoningEffort,
} from "../../store/workspace";
import { t } from "../../i18n";
import { HelpTip } from "../settings/setting-controls";
import { getInternalLLMProviderModelGroups } from "./model-selector-utils";
import ModelRolePicker from "./ModelRolePicker";

function effortLabel(level: ConcreteReasoningEffort): string {
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

function modelEffortOptions(fullKey: string): readonly ConcreteReasoningEffort[] {
  const parsed = parseModelString(fullKey);
  if (!parsed) return [];
  const prov = workspace.providers.find((p) => p.id === parsed.providerID);
  const info = prov?.models?.[parsed.modelID];
  const caps = inferModelEffortCapabilities(
    parsed.providerID,
    parsed.modelID,
    Boolean(info?.reasoning ?? true)
  );
  return caps.efforts;
}

export default function OrchestratorSettingsCard() {
  const [savingKey, setSavingKey] = createSignal<string | null>(null);
  const [savedKey, setSavedKey] = createSignal<string | null>(null);
  const [errorMsg, setErrorMsg] = createSignal<string | null>(null);
  let savedTimer: ReturnType<typeof setTimeout> | undefined;

  const scheduleSaved = (key: string) => {
    setSavedKey(key);
    if (savedTimer !== undefined) clearTimeout(savedTimer);
    savedTimer = setTimeout(() => {
      savedTimer = undefined;
      if (savedKey() === key) setSavedKey(null);
    }, 1500);
  };

  onCleanup(() => {
    if (savedTimer !== undefined) {
      clearTimeout(savedTimer);
      savedTimer = undefined;
    }
  });

  onMount(() => {
    void fetchOrchestratorConfig();
  });

  const nativeGroups = createMemo(() => getInternalLLMProviderModelGroups());
  const cfg = () => orchestratorConfig();
  const isReady = createMemo(() => Boolean(cfg().orchestratorModel && cfg().executorModel));
  const isActive = createMemo(() => isOrchestratorModel(workspace.selectedModel));
  const samePair = createMemo(() =>
    Boolean(
      cfg().orchestratorModel &&
        cfg().executorModel &&
        cfg().orchestratorModel === cfg().executorModel &&
        cfg().orchestratorEffort === cfg().executorEffort
    )
  );

  const orchEfforts = createMemo(() => modelEffortOptions(cfg().orchestratorModel));
  const execEfforts = createMemo(() => modelEffortOptions(cfg().executorModel));

  const applyPatch = async (
    key: string,
    patch: Partial<Record<keyof OrchestratorConfig, string | null>>
  ) => {
    setSavingKey(key);
    setSavedKey(null);
    setErrorMsg(null);
    try {
      await updateOrchestratorConfig(patch);
      scheduleSaved(key);
    } catch (err: any) {
      setErrorMsg(err?.message || t("common.error"));
    } finally {
      setSavingKey(null);
    }
  };

  const renderSlot = (opts: {
    key: string;
    title: () => string;
    desc: () => string;
    modelValue: () => string;
    effortValue: () => string;
    efforts: () => readonly ConcreteReasoningEffort[];
    onModelChange: (val: string) => void;
    onEffortChange: (val: ReasoningEffort) => void;
  }) => (
    <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3 px-4 py-3.5">
      <div class="flex items-center gap-2 min-w-0">
        <span class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
          {opts.title()}
        </span>
        <HelpTip text={opts.desc()} />
      </div>
      <div class="flex items-center gap-2 shrink-0 flex-wrap sm:flex-nowrap">
        <ModelRolePicker
          value={opts.modelValue()}
          groups={nativeGroups()}
          allowEmpty
          emptyLabel={t("models.orchestratorSelectModelPlaceholder")}
          onChange={opts.onModelChange}
        />

        <Show when={opts.modelValue() && opts.efforts().length > 0}>
          <select
            value={opts.effortValue()}
            onChange={(e) => opts.onEffortChange(e.currentTarget.value as ReasoningEffort)}
            aria-label={t("models.reasoningEffort")}
            class="h-[30px] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2 text-[11px] font-medium text-[var(--text-primary)] outline-none hover:border-[var(--accent-border)] cursor-pointer"
          >
            <option value="">{t("models.effortAuto")}</option>
            <For each={opts.efforts()}>
              {(lvl) => <option value={lvl}>{effortLabel(lvl)}</option>}
            </For>
          </select>
        </Show>

        <Show when={opts.modelValue()}>
          <button
            type="button"
            onClick={() => opts.onModelChange("")}
            class="rounded-lg p-1.5 text-[var(--text-faint)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            title={t("common.clear")}
            aria-label={t("common.clear")}
          >
            <RotateCcw size={13} />
          </button>
        </Show>

        <div class="w-4 flex items-center justify-center">
          <Show when={savingKey() === opts.key}>
            <LoaderCircle size={13} class="animate-spin text-[var(--accent-primary)]" />
          </Show>
          <Show when={savedKey() === opts.key}>
            <Check size={13} class="text-[var(--ok)]" />
          </Show>
        </div>
      </div>
    </div>
  );

  return (
    <div class="space-y-2.5">
      <div class="flex flex-wrap items-center justify-between gap-2 px-1">
        <div class="flex items-center gap-2 min-w-0">
          <Layers size={14} class="shrink-0 text-[var(--accent-primary)]" />
          <h4 class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
            {t("models.orchestratorSettingsTitle")}
          </h4>
          <HelpTip text={t("models.orchestratorSettingsSubtitle")} />
        </div>
        <Show when={isReady()}>
          <button
            type="button"
            onClick={() =>
              selectModel({
                providerID: ORCHESTRATOR_PROVIDER_ID,
                modelID: ORCHESTRATOR_MODEL_ID,
              })
            }
            class={`shrink-0 inline-flex items-center gap-1.5 rounded-xl px-3 py-1.5 text-[11px] font-medium transition-colors cursor-pointer ${
              isActive()
                ? "border border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                : "border border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)] hover:opacity-90"
            }`}
          >
            <Show when={isActive()}>
              <Check size={12} />
            </Show>
            <span>
              {isActive() ? t("models.orchestratorActiveBadge") : t("models.orchestratorUseBtn")}
            </span>
          </button>
        </Show>
      </div>

      <div class="divide-y divide-[var(--border-subtle)] rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40">
        {renderSlot({
          key: "orchestrator",
          title: () => t("models.orchestratorModelTitle"),
          desc: () => t("models.orchestratorModelDesc"),
          modelValue: () => cfg().orchestratorModel,
          effortValue: () => cfg().orchestratorEffort,
          efforts: orchEfforts,
          onModelChange: (val) =>
            void applyPatch("orchestrator", {
              orchestratorModel: val || null,
              ...(val ? {} : { orchestratorEffort: null }),
            }),
          onEffortChange: (val) =>
            void applyPatch("orchestrator", { orchestratorEffort: val || null }),
        })}

        {renderSlot({
          key: "executor",
          title: () => t("models.executorModelTitle"),
          desc: () => t("models.executorModelDesc"),
          modelValue: () => cfg().executorModel,
          effortValue: () => cfg().executorEffort,
          efforts: execEfforts,
          onModelChange: (val) =>
            void applyPatch("executor", {
              executorModel: val || null,
              ...(val ? {} : { executorEffort: null }),
            }),
          onEffortChange: (val) =>
            void applyPatch("executor", { executorEffort: val || null }),
        })}
      </div>

      <Show when={samePair()}>
        <div class="flex items-center gap-2 rounded-xl border border-[var(--warn-border)] bg-[var(--warn-surface)] px-3 py-2 text-[11px] text-[var(--warn-text)]">
          <AlertTriangle size={13} class="shrink-0" />
          <span>{t("models.orchestratorSamePairWarning")}</span>
        </div>
      </Show>

      <Show when={errorMsg()}>
        {(msg) => (
          <div class="rounded-xl border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-[11px] text-rose-400">
            {msg()}
          </div>
        )}
      </Show>
    </div>
  );
}
