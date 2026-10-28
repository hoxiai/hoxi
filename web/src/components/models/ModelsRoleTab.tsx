// web/src/components/models/ModelsRoleTab.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { ArrowRight, Bot, Check, LoaderCircle, RotateCcw, SlidersHorizontal } from "lucide-solid";
import {
  approvalPolicy,
  commitModelSpec,
  openProviderModal,
  resolveDefaultAIReviewModel,
  setApprovalAIReview,
  setCommitModelSpec,
  setObserverAutoAudit,
  setObserverModelRole,
  updateGlobalModelConfig,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import { getInternalLLMProviderModelGroups } from "./model-selector-utils";
import ModelRolePicker, { type ModelRoleExtraOption } from "./ModelRolePicker";

export default function ModelsRoleTab() {
  const [savingKey, setSavingKey] = createSignal<string | null>(null);
  const [savedKey, setSavedKey] = createSignal<string | null>(null);

  const connectedGroups = createMemo(() => getInternalLLMProviderModelGroups());

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
      setSavedKey(key);
      setTimeout(() => {
        if (savedKey() === key) setSavedKey(null);
      }, 1500);
    } finally {
      setSavingKey(null);
    }
  };

  const renderModelSelect = (opts: {
    roleKey: string;
    value: () => string;
    allowEmpty: boolean;
    emptyLabel?: string;
    extraOptions?: ModelRoleExtraOption[];
    onChange: (val: string) => void;
  }) => (
    <div class="flex items-center gap-2 shrink-0">
      <ModelRolePicker
        value={opts.value()}
        groups={connectedGroups()}
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
        <div>
          <h3 class="text-[13px] font-semibold text-[var(--text-primary)]">
            {t("models.rolesTitle")}
          </h3>
          <p class="text-[12px] text-[var(--text-secondary)]">
            {t("models.rolesSubtitle")}
          </p>
        </div>
      </div>

      {/* Global Model Roles */}
      <div class="divide-y divide-[var(--border-subtle)] rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 overflow-hidden">
        {/* Default Model */}
        <div class="flex items-center justify-between gap-4 px-4 py-3.5">
          <div class="min-w-0">
            <div class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("models.defaultModelTitle")}
            </div>
            <div class="text-[12px] text-[var(--text-muted)]">
              {t("models.defaultModelDesc")}
            </div>
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
          <div class="min-w-0">
            <div class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("models.smallModelTitle")}
            </div>
            <div class="text-[12px] text-[var(--text-muted)]">
              {t("models.smallModelDesc")}
            </div>
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
          <div class="min-w-0">
            <div class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("models.commitModelTitle")}
            </div>
            <div class="text-[12px] text-[var(--text-muted)]">
              {t("models.commitModelDesc")}
            </div>
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
              setSavedKey("commit_model");
              setTimeout(() => {
                if (savedKey() === "commit_model") setSavedKey(null);
              }, 1500);
            },
          })}
        </div>

        {/* Subagent models live per agent in the Subagents tab: OpenCode has no
            subagent_model key, so a role-level picker here would be ignored. */}

        {/* Compaction Model */}
        <div class="flex items-center justify-between gap-4 px-4 py-3.5">
          <div class="min-w-0">
            <div class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("models.compactionModelTitle")}
            </div>
            <div class="text-[12px] text-[var(--text-muted)]">
              {t("models.compactionModelDesc")}
            </div>
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

        {/* Observer / Code Auditor Model */}
        <div class="flex items-center justify-between gap-4 px-4 py-3.5 bg-[var(--accent-surface)]/25">
          <div class="min-w-0 space-y-1">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="text-[13px] font-semibold text-[var(--text-primary)]">
                {t("models.observerModelTitle")}
              </span>
              <label class="inline-flex items-center gap-1.5 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={workspace.observerAutoAudit}
                  onChange={(e) => setObserverAutoAudit(e.currentTarget.checked)}
                  class="rounded accent-[var(--accent-primary)] cursor-pointer"
                />
                <span>{t("models.observerAutoAuditLabel")}</span>
              </label>
            </div>
            <div class="text-[12px] text-[var(--text-muted)]">
              {t("models.observerModelDesc")}
            </div>
          </div>
          {renderModelSelect({
            roleKey: "observer_model",
            value: () => workspace.observerModel,
            allowEmpty: true,
            emptyLabel: t("models.useGlobalDefault"),
            onChange: (val) => {
              setObserverModelRole(val);
              setSavedKey("observer_model");
              setTimeout(() => {
                if (savedKey() === "observer_model") setSavedKey(null);
              }, 1500);
            },
          })}
        </div>

        {/* Complex Shell Command AI Reviewer Model */}
        <div class="flex items-center justify-between gap-4 px-4 py-3.5">
          <div class="min-w-0 space-y-1">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="text-[13px] font-semibold text-[var(--text-primary)]">
                {t("approval.aiReviewTitle")}
              </span>
              <label class="inline-flex items-center gap-1.5 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={Boolean(approvalPolicy().aiReview && approvalPolicy().aiReviewModel)}
                  disabled={!approvalPolicy().aiReviewModel && !resolveDefaultAIReviewModel()}
                  onChange={(e) =>
                    void setApprovalAIReview({ aiReview: e.currentTarget.checked })
                  }
                  class="rounded accent-[var(--accent-primary)] cursor-pointer disabled:opacity-50"
                />
                <span>{t("approval.aiReviewEnableLabel")}</span>
              </label>
            </div>
            <div class="text-[12px] text-[var(--text-muted)]">
              {t("approval.aiReviewEgressNotice")}
            </div>
          </div>
          {renderModelSelect({
            roleKey: "ai_review_model",
            value: () => approvalPolicy().aiReviewModel || "",
            allowEmpty: true,
            emptyLabel: t("approval.aiReviewModelEmpty"),
            onChange: (val) => {
              setSavingKey("ai_review_model");
              setSavedKey(null);
              void setApprovalAIReview({
                aiReviewModel: val,
                aiReview: val ? true : false,
              })
                .then(() => {
                  setSavedKey("ai_review_model");
                  setTimeout(() => {
                    if (savedKey() === "ai_review_model") setSavedKey(null);
                  }, 1500);
                })
                .finally(() => setSavingKey(null));
            },
          })}
        </div>
      </div>

      {/* Per-Mode Model Overrides */}
      <div class="space-y-2.5">
        <div class="flex items-center justify-between px-1 gap-3">
          <div>
            <h4 class="text-[12px] font-semibold text-[var(--text-primary)]">
              {t("models.modeModelsTitle")}
            </h4>
            <p class="text-[11px] text-[var(--text-muted)]">
              {t("models.modeModelsDesc")}
            </p>
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

        <div class="divide-y divide-[var(--border-subtle)] rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 overflow-hidden">
          <For each={workspace.agents}>
            {(agent) => {
              const modeVal = () => {
                const cfgModel = workspace.globalConfig?.agent?.[agent.name]?.model;
                return typeof cfgModel === "string" ? cfgModel : "";
              };
              return (
                <div class="flex items-center justify-between gap-4 px-4 py-3">
                  <div class="min-w-0">
                    <div class="flex items-center gap-2">
                      <span class="text-[13px] font-semibold capitalize text-[var(--text-primary)]">
                        {agent.name}
                      </span>
                      <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-muted)]">
                        {t("models.modeBadge")}
                      </span>
                    </div>
                    <Show when={agent.description}>
                      <div class="text-[11px] text-[var(--text-muted)] line-clamp-1">
                        {agent.description}
                      </div>
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
