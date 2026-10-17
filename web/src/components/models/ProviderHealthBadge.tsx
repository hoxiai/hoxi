// web/src/components/models/ProviderHealthBadge.tsx
import { createMemo, Show } from "solid-js";
import {
  computeProviderHealth,
  type ProviderHealthSummary,
  type ProviderRiskTag,
} from "../../store/workspace";
import type { ProviderIncidentType } from "../../services/api";
import { t } from "../../i18n";

export function formatTtftLabel(ms: number | null | undefined): string {
  if (!ms || ms <= 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

export function riskTagLabel(tag: ProviderRiskTag): string {
  switch (tag) {
    case "low_pass_rate":
      return t("models.qualityRiskLowPassRate");
    case "timeout_prone":
      return t("models.qualityRiskTimeout");
    case "slow_ttft":
      return t("models.qualityRiskSlow");
    case "high_hallucination":
      return t("models.qualityRiskHallucination");
    case "tool_instability":
      return t("models.qualityRiskTool");
    case "frequent_retry":
      return t("models.qualityRiskRetry");
  }
}

export function incidentTypeLabel(type: ProviderIncidentType): string {
  switch (type) {
    case "code_audit_fail":
      return t("models.qualityIncidentAuditFail");
    case "timeout":
      return t("models.qualityIncidentTimeout");
    case "api_error":
      return t("models.qualityIncidentApiError");
    case "retry":
      return t("models.qualityIncidentRetry");
    case "tool_error":
      return t("models.qualityIncidentToolError");
    case "hallucination_loop":
      return t("models.qualityIncidentHallucination");
    case "user_revert":
      return t("models.qualityIncidentRevert");
  }
}

export function statusColors(status: ProviderHealthSummary["status"]) {
  if (status === "healthy") {
    return {
      pill: "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]",
      dot: "bg-[var(--ok)]",
    };
  }
  if (status === "warning") {
    return {
      pill: "border-[var(--warn-border)] bg-[var(--warn-surface)] text-[var(--warn-text)]",
      dot: "bg-amber-500",
    };
  }
  if (status === "degraded") {
    return {
      pill: "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]",
      dot: "bg-red-500",
    };
  }
  return {
    pill: "border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-muted)]",
    dot: "bg-[var(--text-faint)]",
  };
}

/**
 * Compact inline health badge for ModelSelector rows and ProviderCatalogTab.
 */
export function ProviderHealthBadge(props: {
  providerID: string;
  modelID?: string;
  compact?: boolean;
}) {
  const health = createMemo(() => computeProviderHealth(props.providerID, props.modelID));
  const colors = createMemo(() => statusColors(health().status));

  const statusText = createMemo(() => {
    const s = health().status;
    if (s === "healthy") return t("models.qualityGradeHealthy");
    if (s === "warning") return t("models.qualityGradeWarning");
    if (s === "degraded") return t("models.qualityGradeDegraded");
    return "";
  });

  const primaryRisk = createMemo(() => {
    const tags = health().riskTags;
    if (tags.includes("low_pass_rate")) return riskTagLabel("low_pass_rate");
    if (tags.includes("high_hallucination")) return riskTagLabel("high_hallucination");
    if (tags.includes("timeout_prone")) return riskTagLabel("timeout_prone");
    if (tags.length > 0) return riskTagLabel(tags[0]);
    return "";
  });

  return (
    <Show when={health().status !== "untested"}>
      <span
        class={`inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 text-[10px] font-medium leading-none ${colors().pill}`}
        title={`${statusText()} · ${t("models.qualityScoreBadge", { score: health().score })}${
          health().codePassRate !== null
            ? ` · Pass ${health().codePassRate}%`
            : ""
        }${
          health().avgTtftMs ? ` · TTFT ${formatTtftLabel(health().avgTtftMs)}` : ""
        }`}
      >
        <span class={`h-1.5 w-1.5 rounded-full shrink-0 ${colors().dot}`} />
        <span class="font-mono font-semibold">
          {t("models.qualityScoreBadge", { score: health().score })}
        </span>
        <Show when={!props.compact}>
          <Show
            when={primaryRisk()}
            fallback={
              <Show when={health().codePassRate !== null || health().avgTps || health().avgTtftMs}>
                <span class="opacity-85 font-mono">
                  ·{" "}
                  {health().codePassRate !== null
                    ? `${health().codePassRate}%`
                    : health().avgTps
                    ? `${health().avgTps}t/s`
                    : formatTtftLabel(health().avgTtftMs)}
                </span>
              </Show>
            }
          >
            <span class="opacity-95">· {primaryRisk()}</span>
          </Show>
        </Show>
      </span>
    </Show>
  );
}
