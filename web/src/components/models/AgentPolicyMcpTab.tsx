// web/src/components/models/AgentPolicyMcpTab.tsx
import { createMemo, createSignal, For, onMount, Show } from "solid-js";
import {
  Check,
  ChevronRight,
  Shield,
  ShieldAlert,
  ShieldCheck,
  ShieldOff,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  appSettings,
  approvalGrants,
  approvalPolicy,
  fetchGlobalConfig,
  resolveDefaultAIReviewModel,
  resolveEffectivePermissionSummary,
  setApprovalAIReview,
  setApprovalMode,
  setApprovalTimeout,
  updateToolPermissionRule,
  workspace,
  type ApprovalMode,
} from "../../store/workspace";
import DeferredList from "../approval/DeferredList";
import { AgyApprovalHookControls } from "../settings/AgyApprovalHookControls";
import { ApprovalAuditGrantsSection } from "../settings/ApprovalAuditGrantsSection";
import {
  SegmentedPills,
  SettingGroup,
  SettingRow,
  ToggleSwitch,
} from "../settings/setting-controls";
import { getInternalLLMProviderModelGroups } from "./model-selector-utils";
import ModelRolePicker from "./ModelRolePicker";

interface PresetCardOption {
  id: ApprovalMode;
  labelKey:
    | "approval.modeAutoLabel"
    | "approval.modeSmartLabel"
    | "approval.modeManualLabel"
    | "approval.modeTrustedLabel"
    | "approval.modeUnattendedLabel";
  descKey:
    | "approval.modeAutoDesc"
    | "approval.modeSmartDesc"
    | "approval.modeManualDesc"
    | "approval.modeTrustedDesc"
    | "approval.modeUnattendedDesc";
  badgeKey?: "approval.modeAutoBadge" | "approval.modeTrustedBadge" | "approval.modeUnattendedBadge";
}

const PRESET_OPTIONS: PresetCardOption[] = [
  {
    id: "auto",
    labelKey: "approval.modeAutoLabel",
    descKey: "approval.modeAutoDesc",
    badgeKey: "approval.modeAutoBadge",
  },
  {
    id: "manual",
    labelKey: "approval.modeManualLabel",
    descKey: "approval.modeManualDesc",
  },
  {
    id: "unattended",
    labelKey: "approval.modeUnattendedLabel",
    descKey: "approval.modeUnattendedDesc",
    badgeKey: "approval.modeUnattendedBadge",
  },
];

function normalizePresetMode(mode: ApprovalMode): ApprovalMode {
  if (mode === "smart") return "auto";
  if (mode === "trusted") return "unattended";
  return mode;
}

function renderPresetIcon(mode: ApprovalMode, size = 15) {
  if (mode === "trusted" || mode === "unattended") return <ShieldOff size={size} class="shrink-0" />;
  if (mode === "auto") return <ShieldCheck size={size} class="shrink-0" />;
  if (mode === "smart") return <Shield size={size} class="shrink-0" />;
  return <ShieldAlert size={size} class="shrink-0" />;
}

export default function AgentPolicyMcpTab() {
  const [feedback, setFeedback] = createSignal<{ ok: boolean; text: string } | null>(null);

  onMount(async () => {
    try {
      await fetchGlobalConfig();
    } catch {
      /* ignore */
    }
  });

  const currentMode = (): ApprovalMode => appSettings.approvalMode || "auto";
  const connectedGroups = createMemo(() => getInternalLLMProviderModelGroups());

  const effective = createMemo(() =>
    resolveEffectivePermissionSummary(
      currentMode(),
      workspace.globalConfig?.permission,
      approvalPolicy()
    )
  );

  const showSavedFeedback = (ok: boolean, text: string) => {
    setFeedback({ ok, text });
    setTimeout(() => {
      if (feedback()?.text === text) setFeedback(null);
    }, 2200);
  };

  const onSelectPreset = async (mode: ApprovalMode) => {
    try {
      await setApprovalMode(mode);
      showSavedFeedback(true, t("models.policyUpdated"));
    } catch (err: any) {
      showSavedFeedback(false, err?.message || t("models.policyUpdateFailed"));
    }
  };

  const onChangeToolRule = async (
    tool: "edit" | "webfetch" | "bash",
    val: "allow" | "smart" | "ask"
  ) => {
    try {
      await updateToolPermissionRule(tool, val);
      showSavedFeedback(true, t("models.policyUpdated"));
    } catch (err: any) {
      showSavedFeedback(false, err?.message || t("models.policyUpdateFailed"));
    }
  };

  return (
    <div class="flex-1 overflow-y-auto p-6 sm:p-8">
      <div class="mx-auto w-full max-w-4xl space-y-6">
        {/* Page Header */}
        <div class="flex items-center gap-3 rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/50 px-4 py-3">
          <div class="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-[var(--accent-surface)] text-[var(--accent-primary)]">
            <ShieldCheck size={16} />
          </div>
          <div>
            <h3 class="text-[13px] font-semibold text-[var(--text-primary)]">
              {t("approval.pageTitle")}
            </h3>
            <p class="text-[12px] text-[var(--text-secondary)]">
              {t("approval.pageSubtitle")}
            </p>
          </div>
        </div>

        {/* 1. Approval Mode Preset */}
        <div class="space-y-2.5">
          <div class="flex items-center justify-between px-1">
            <div>
              <h4 class="text-[13px] font-semibold text-[var(--text-primary)]">
                {t("approval.presetSectionTitle")}
              </h4>
              <p class="text-[12px] text-[var(--text-muted)]">
                {t("approval.presetSectionDesc")}
              </p>
            </div>
            <span class="rounded-md bg-[var(--warn-surface)] border border-[var(--warn-border)] px-2 py-0.5 text-[11px] font-medium text-[var(--warn-text)]">
              {t("approval.modeMenuBadge")}
            </span>
          </div>

          <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
            <For each={PRESET_OPTIONS}>
              {(opt) => {
                const isSelected = () => normalizePresetMode(currentMode()) === opt.id;
                return (
                  <button
                    type="button"
                    onClick={() => void onSelectPreset(opt.id)}
                    class={`flex flex-col justify-between rounded-2xl border p-4 text-left transition-colors cursor-pointer ${
                      isSelected()
                        ? "border-[var(--accent-border)] bg-[var(--accent-surface)]/45"
                        : "border-[var(--border-subtle)] bg-[var(--bg-elevated)]/40 hover:border-[var(--border-strong)]"
                    }`}
                  >
                    <div class="space-y-2">
                      <div class="flex items-center justify-between gap-2">
                        <div class="flex items-center gap-2">
                          <span
                            class={`flex h-7 w-7 items-center justify-center rounded-lg ${
                              isSelected()
                                ? opt.id === "trusted" || opt.id === "unattended"
                                  ? "bg-[var(--danger-text)] text-white"
                                  : opt.id === "manual"
                                  ? "bg-[var(--warn-text)] text-white"
                                  : opt.id === "auto"
                                    ? "bg-[var(--ok-text)] text-white"
                                    : "bg-[var(--accent-primary)] text-[var(--accent-foreground)]"
                                : "bg-[var(--bg-inset)] text-[var(--text-muted)]"
                            }`}
                          >
                            {renderPresetIcon(opt.id, 14)}
                          </span>
                          <span class="text-[13px] font-semibold text-[var(--text-primary)]">
                            {t(opt.labelKey)}
                          </span>
                        </div>
                        <Show when={isSelected()}>
                          <Check size={14} class="text-[var(--accent-primary)] shrink-0" />
                        </Show>
                      </div>

                      <p class="text-[12px] leading-relaxed text-[var(--text-muted)]">
                        {t(opt.descKey)}
                      </p>
                    </div>

                    <Show when={opt.badgeKey}>
                      <div class="mt-3 pt-2 border-t border-[var(--border-subtle)]/60 flex items-center">
                        <span
                          class={`rounded px-1.5 py-0.5 text-[11px] font-medium ${
                            opt.id === "trusted" || opt.id === "unattended" ? "bg-[var(--danger-surface)] text-[var(--danger-text)] border border-[var(--danger-border)]" : "bg-[var(--ok-surface)] text-[var(--ok-text)] border border-[var(--ok-border)]"
                          }`}
                        >
                          {t(opt.badgeKey!)}
                        </span>
                      </div>
                    </Show>
                  </button>
                );
              }}
            </For>
          </div>
        </div>

        {/* 2. Per-Tool Fine-Grained Rules */}
        <SettingGroup
          title={t("approval.toolRulesSectionTitle")}
          tip={t("approval.toolRulesSectionDesc")}
        >
          <SettingRow
            label={t("models.fileEdits")}
            tip={t("models.fileEditsHint")}
          >
            <SegmentedPills<"allow" | "ask">
              value={effective().edit}
              options={[
                { id: "allow", label: () => t("models.autoAllow") },
                { id: "ask", label: () => t("models.ask") },
              ]}
              onChange={(m) => void onChangeToolRule("edit", m)}
            />
          </SettingRow>

          <SettingRow
            label={t("models.shellCommands")}
            tip={t("models.shellCommandsHint")}
          >
            <SegmentedPills<"allow" | "smart" | "ask">
              value={effective().bash}
              options={[
                { id: "allow", label: () => t("approval.shellAllowNormal") },
                { id: "smart", label: () => t("approval.shellAllowSafeOnly") },
                { id: "ask", label: () => t("models.ask") },
              ]}
              onChange={(m) => void onChangeToolRule("bash", m)}
            />
          </SettingRow>

          <SettingRow
            label={t("models.webFetch")}
            tip={t("models.webFetchHint")}
          >
            <SegmentedPills<"allow" | "ask">
              value={effective().webfetch}
              options={[
                { id: "allow", label: () => t("models.autoAllow") },
                { id: "ask", label: () => t("models.ask") },
              ]}
              onChange={(m) => void onChangeToolRule("webfetch", m)}
            />
          </SettingRow>

          <SettingRow label={t("approval.timeoutLabel")} tip={t("approval.timeoutHint")}>
            <input
              type="number"
              min="60"
              max="3600"
              value={approvalPolicy().approvalTimeoutSeconds || 600}
              onChange={(e) => {
                const n = Number(e.currentTarget.value);
                if (n >= 60 && n <= 3600) void setApprovalTimeout(n).catch((err) => showSavedFeedback(false, String(err)));
              }}
              class="w-24 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[12px]"
            />
          </SettingRow>
        </SettingGroup>

        <SettingGroup title={t("approval.aiReviewTitle")} tip={t("approval.aiReviewTip")}>
          <SettingRow label={t("approval.aiReviewEnableLabel")} tip={t("approval.aiReviewTip")}>
            <ToggleSwitch
              checked={Boolean(approvalPolicy().aiReview && approvalPolicy().aiReviewModel)}
              onChange={(enabled) => {
                if (enabled && !approvalPolicy().aiReviewModel && !resolveDefaultAIReviewModel()) {
                  showSavedFeedback(false, t("approval.aiReviewModelEmpty"));
                  return;
                }
                void setApprovalAIReview({ aiReview: enabled })
                  .then(() => showSavedFeedback(true, t("models.policyUpdated")))
                  .catch((err) => showSavedFeedback(false, err?.message || String(err)));
              }}
            />
          </SettingRow>
          <SettingRow label={t("approval.aiReviewModelLabel")}>
            <ModelRolePicker
              value={approvalPolicy().aiReviewModel || ""}
              groups={connectedGroups()}
              allowEmpty={true}
              emptyLabel={t("approval.aiReviewModelEmpty")}
              onChange={(val) => {
                void setApprovalAIReview({
                  aiReviewModel: val,
                  aiReview: val ? true : false,
                })
                  .then(() => showSavedFeedback(true, t("models.policyUpdated")))
                  .catch((err) => showSavedFeedback(false, err?.message || String(err)));
              }}
            />
          </SettingRow>
          <div class="px-5 py-3 text-[12px] leading-relaxed text-[var(--text-muted)]">
            {t("approval.aiReviewEgressNotice")}
          </div>
        </SettingGroup>

        <DeferredList />

        <Show when={feedback()}>
          <div
            class={`rounded-xl border px-3.5 py-2 text-[12px] ${
              feedback()!.ok
                ? "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]"
                : "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
            }`}
          >
            {feedback()!.text}
          </div>
        </Show>

        {/* 4. Collapsible Advanced Section: Antigravity Hook & Approval Audit / Session Grants */}
        <details
          class="group/adv rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/25 p-4"
          open={approvalGrants().length > 0}
        >
          <summary class="flex cursor-pointer list-none items-center gap-2 text-[13px] font-semibold text-[var(--text-secondary)] hover:text-[var(--text-primary)] [&::-webkit-details-marker]:hidden">
            <ChevronRight size={15} class="transition-transform group-open/adv:rotate-90" />
            <span>{t("approval.advancedHookAndAuditTitle")}</span>
          </summary>
          <div class="mt-4 space-y-6">
            <AgyApprovalHookControls />
            <ApprovalAuditGrantsSection />
          </div>
        </details>
      </div>
    </div>
  );
}
