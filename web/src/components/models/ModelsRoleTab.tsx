// web/src/components/models/ModelsRoleTab.tsx
import { createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import { ArrowRight, Bot, Check, LoaderCircle, Plus, RotateCcw, SlidersHorizontal } from "lucide-solid";
import {
  commitModelSpec,
  getMediaRoleModelGroups,
  mediaRolePrefs,
  openProviderModal,
  refreshMediaCapabilities,
  setCommitModelSpec,
  updateGlobalModelConfig,
  updateMediaRolePrefs,
  workspace,
  type ConnectedProviderGroup,
  type MediaRolePrefs,
} from "../../store/workspace";
import { locale, t } from "../../i18n";
import { resolveSurfacePrimaryModes, surfaceExtension } from "../layout/surface-extension";
import { HelpTip } from "../settings/setting-controls";
import { getInternalLLMProviderModelGroups } from "./model-selector-utils";
import ModelRolePicker, { type ModelRoleExtraOption } from "./ModelRolePicker";

export default function ModelsRoleTab() {
  const [savingKey, setSavingKey] = createSignal<string | null>(null);
  const [savedKey, setSavedKey] = createSignal<string | null>(null);
  let savedTimer: ReturnType<typeof setTimeout> | undefined;

  const scheduleSavedIndicator = (key: string) => {
    setSavedKey(key);
    if (savedTimer !== undefined) {
      clearTimeout(savedTimer);
    }
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
    void refreshMediaCapabilities();
  });

  const connectedGroups = createMemo(() => getInternalLLMProviderModelGroups());
  const imageRoleGroups = createMemo(() => getMediaRoleModelGroups("image"));
  const videoRoleGroups = createMemo(() => getMediaRoleModelGroups("video"));
  const speechRoleGroups = createMemo(() => getMediaRoleModelGroups("speech"));
  const transcribeRoleGroups = createMemo(() => getMediaRoleModelGroups("transcribe"));

  const applyMediaRole = async (field: keyof MediaRolePrefs, val: string) => {
    const key = `media:${field}`;
    setSavingKey(key);
    setSavedKey(null);
    try {
      await updateMediaRolePrefs({ [field]: val || "" });
      scheduleSavedIndicator(key);
    } finally {
      setSavingKey(null);
    }
  };

  const currentDefaultModel = createMemo(() => {
    if (typeof workspace.globalConfig?.model === "string" && workspace.globalConfig.model) {
      return workspace.globalConfig.model;
    }
    if (workspace.selectedModel) {
      return `${workspace.selectedModel.providerID}/${workspace.selectedModel.modelID}`;
    }
    return "";
  });

  const currentSmallModel = createMemo(
    () => (typeof workspace.globalConfig?.small_model === "string" ? workspace.globalConfig.small_model : "")
  );

  const currentCompactionModel = createMemo(
    () =>
      typeof workspace.globalConfig?.agent?.compaction?.model === "string"
        ? workspace.globalConfig.agent.compaction.model
        : ""
  );

  const applyRolePatch = async (key: string, patch: Record<string, any>) => {
    setSavingKey(key);
    setSavedKey(null);
    try {
      await updateGlobalModelConfig(patch);
      scheduleSavedIndicator(key);
    } finally {
      setSavingKey(null);
    }
  };

  const renderModelSelect = (opts: {
    roleKey: string;
    value: () => string;
    allowEmpty: boolean;
    emptyLabel?: string;
    groups?: () => ConnectedProviderGroup[];
    extraOptions?: ModelRoleExtraOption[];
    onChange: (val: string) => void;
  }) => (
    <div class="flex items-center gap-2 shrink-0">
      <ModelRolePicker
        value={opts.value()}
        groups={(opts.groups ?? connectedGroups)()}
        allowEmpty={opts.allowEmpty}
        emptyLabel={opts.emptyLabel}
        extraOptions={opts.extraOptions}
        onChange={opts.onChange}
      />

      <Show when={opts.allowEmpty && opts.value()}>
        <button
          type="button"
          onClick={() => opts.onChange("")}
          class="rounded-lg p-1.5 text-[var(--text-faint)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          title={opts.emptyLabel || t("models.useGlobalDefault")}
        >
          <RotateCcw size={13} />
        </button>
      </Show>

      <div class="w-4 flex items-center justify-center">
        <Show when={savingKey() === opts.roleKey}>
          <LoaderCircle size={13} class="animate-spin text-[var(--accent-primary)]" />
        </Show>
        <Show when={savedKey() === opts.roleKey}>
          <Check size={13} class="text-[var(--ok)]" />
        </Show>
      </div>
    </div>
  );

  return (
    <div class="flex-1 overflow-y-auto p-6 sm:p-8">
      <div class="mx-auto w-full max-w-4xl space-y-5">
      <div class="flex items-center gap-3 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-4 py-3">
        <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
          <SlidersHorizontal size={15} />
        </div>
        <div class="flex items-center gap-2 min-w-0">
          <h3 class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
            {t("models.rolesTitle")}
          </h3>
          <HelpTip text={t("models.rolesSubtitle")} />
        </div>
      </div>

      {/* Global Model Roles */}
      <div class="divide-y divide-[var(--border-subtle)] rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40">
        {/* Default Model */}
        <div class="flex items-center justify-between gap-4 px-4 py-3.5">
          <div class="flex items-center gap-2 min-w-0">
            <span class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
              {t("models.defaultModelTitle")}
            </span>
            <HelpTip text={t("models.defaultModelDesc")} />
          </div>
          {renderModelSelect({
            roleKey: "model",
            value: currentDefaultModel,
            allowEmpty: false,
            onChange: (val) => void applyRolePatch("model", { model: val }),
          })}
        </div>

        {/* Small / Fast Model */}
        <div class="flex items-center justify-between gap-4 px-4 py-3.5">
          <div class="flex items-center gap-2 min-w-0">
            <span class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
              {t("models.smallModelTitle")}
            </span>
            <HelpTip text={t("models.smallModelDesc")} />
          </div>
          {renderModelSelect({
            roleKey: "small_model",
            value: currentSmallModel,
            allowEmpty: true,
            onChange: (val) =>
              void applyRolePatch("small_model", { small_model: val || null }),
          })}
        </div>

        {/* Git Commit Message Model */}
        <div class="flex items-center justify-between gap-4 px-4 py-3.5">
          <div class="flex items-center gap-2 min-w-0">
            <span class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
              {t("models.commitModelTitle")}
            </span>
            <HelpTip text={t("models.commitModelDesc")} />
          </div>
          {renderModelSelect({
            roleKey: "commit_model",
            value: commitModelSpec,
            allowEmpty: true,
            emptyLabel: t("models.commitModelFollowSmallOption"),
            extraOptions: [
              { value: "none", label: t("models.commitModelNoneOption") },
            ],
            onChange: (val) => {
              setCommitModelSpec(val);
              scheduleSavedIndicator("commit_model");
            },
          })}
        </div>

        {/* Subagent models live per agent in the Subagents tab: OpenCode has no
            subagent_model key, so a role-level picker here would be ignored. */}

        {/* Compaction Model */}
        <div class="flex items-center justify-between gap-4 px-4 py-3.5">
          <div class="flex items-center gap-2 min-w-0">
            <span class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
              {t("models.compactionModelTitle")}
            </span>
            <HelpTip text={t("models.compactionModelDesc")} />
          </div>
          {renderModelSelect({
            roleKey: "compaction",
            value: currentCompactionModel,
            allowEmpty: true,
            onChange: (val) =>
              void applyRolePatch("compaction", {
                agent: {
                  ...(workspace.globalConfig?.agent || {}),
                  compaction: {
                    ...(workspace.globalConfig?.agent?.compaction || {}),
                    model: val || null,
                  },
                },
              }),
          })}
        </div>
      </div>

      {/* Media & Voice Default Models */}
      <div class="space-y-2.5">
        <div class="flex items-center gap-2 px-1">
          <h4 class="text-[12px] font-semibold text-[var(--text-primary)]">
            {t("models.mediaRolesSectionTitle")}
          </h4>
          <HelpTip text={t("models.mediaRolesSectionDesc")} />
        </div>

        <div class="divide-y divide-[var(--border-subtle)] rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40">
          {(
            [
              {
                field: "imageModel" as const,
                title: () => t("models.imageRoleModelTitle"),
                desc: () => t("models.imageRoleModelDesc"),
                groups: imageRoleGroups,
              },
              {
                field: "videoModel" as const,
                title: () => t("models.videoRoleModelTitle"),
                desc: () => t("models.videoRoleModelDesc"),
                groups: videoRoleGroups,
              },
              {
                field: "speechModel" as const,
                title: () => t("models.speechRoleModelTitle"),
                desc: () => t("models.speechRoleModelDesc"),
                groups: speechRoleGroups,
              },
              {
                field: "transcribeModel" as const,
                title: () => t("models.transcribeRoleModelTitle"),
                desc: () => t("models.transcribeRoleModelDesc"),
                groups: transcribeRoleGroups,
              },
            ] as const
          ).map((row) => (
            <div class="flex items-center justify-between gap-4 px-4 py-3.5">
              <div class="flex items-center gap-2 min-w-0">
                <span class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
                  {row.title()}
                </span>
                <HelpTip text={row.desc()} />
                <Show when={row.groups().length === 0}>
                  <span class="shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 text-[11px] text-[var(--text-muted)]">
                    {t("models.mediaRoleUnconfiguredBadge")}
                  </span>
                </Show>
              </div>
              <Show
                when={row.groups().length > 0 || Boolean(mediaRolePrefs()[row.field])}
                fallback={
                  <button
                    type="button"
                    onClick={() => openProviderModal("providers")}
                    class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3 py-1.5 text-[12px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] transition-colors cursor-pointer shrink-0"
                  >
                    <Plus size={12} />
                    <span>{t("models.mediaRoleConfigureBtn")}</span>
                  </button>
                }
              >
                {renderModelSelect({
                  roleKey: `media:${row.field}`,
                  value: () => mediaRolePrefs()[row.field] || "",
                  allowEmpty: true,
                  emptyLabel: t("models.mediaRoleAutoBestOption"),
                  groups: row.groups,
                  onChange: (val) => void applyMediaRole(row.field, val),
                })}
              </Show>
            </div>
          ))}
        </div>
      </div>

      {/* Per-Mode Model Overrides */}
      <div class="space-y-2.5">
        <div class="flex items-center justify-between px-1 gap-3">
          <div class="flex items-center gap-2 min-w-0">
            <h4 class="text-[12px] font-semibold text-[var(--text-primary)] truncate">
              {t("models.modeModelsTitle")}
            </h4>
            <HelpTip text={t("models.modeModelsDesc")} />
          </div>
          <button
            type="button"
            onClick={() => openProviderModal("agents")}
            class="inline-flex items-center gap-1.5 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3 py-1.5 text-[12px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] transition-colors cursor-pointer shrink-0"
          >
            <Bot size={13} />
            <span>{t("models.goToAgentsSettings")}</span>
            <ArrowRight size={12} />
          </button>
        </div>

        <div class="divide-y divide-[var(--border-subtle)] rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40">
          <For each={resolveSurfacePrimaryModes(workspace.agents)}>
            {(agent) => {
              const modeVal = () => {
                const cfgModel = workspace.globalConfig?.agent?.[agent.name]?.model;
                return typeof cfgModel === "string" ? cfgModel : "";
              };
              const displayLabel = () =>
                (surfaceExtension.enabled &&
                  surfaceExtension.formatModeLabel?.(agent.name, locale())) ||
                agent.name;
              const displayDesc = () =>
                (surfaceExtension.enabled &&
                  surfaceExtension.formatModeDescription?.(agent.name, locale())) ||
                agent.description;
              return (
                <div class="flex items-center justify-between gap-4 px-4 py-3">
                  <div class="flex items-center gap-2 min-w-0">
                    <span class="text-[13px] font-semibold capitalize text-[var(--text-primary)] truncate">
                      {displayLabel()}
                    </span>
                    <span class="shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-muted)]">
                      {t("models.modeBadge")}
                    </span>
                    <Show when={displayDesc()}>
                      {(desc) => <HelpTip text={desc()} />}
                    </Show>
                  </div>
                  {renderModelSelect({
                    roleKey: `mode:${agent.name}`,
                    value: modeVal,
                    allowEmpty: true,
                    onChange: (val) =>
                      void applyRolePatch(`mode:${agent.name}`, {
                        agent: {
                          ...(workspace.globalConfig?.agent || {}),
                          [agent.name]: {
                            ...(workspace.globalConfig?.agent?.[agent.name] || {}),
                            model: val || null,
                          },
                        },
                      }),
                  })}
                </div>
              );
            }}
          </For>
        </div>
      </div>
      </div>
    </div>
  );
}
