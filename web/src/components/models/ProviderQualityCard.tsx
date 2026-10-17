// web/src/components/models/ProviderQualityCard.tsx
import { createMemo, For, Show } from "solid-js";
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Clock,
  RotateCcw,
  ShieldAlert,
  Wrench,
  Zap,
} from "lucide-solid";
import {
  computeProviderHealth,
  resetProviderTelemetry,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import {
  formatTtftLabel,
  incidentTypeLabel,
  ProviderHealthBadge,
  riskTagLabel,
  statusColors,
} from "./ProviderHealthBadge";

export { formatTtftLabel, ProviderHealthBadge, riskTagLabel };

/**
 * Full 4-dimension Relay Quality & Anti-Hallucination Scorecard for CustomEndpointTab / ProviderConnectView.
 */
export default function ProviderQualityCard(props: { providerID: string }) {
  const pid = () => props.providerID.trim();
  const health = createMemo(() => computeProviderHealth(pid()));
  const rawRecord = createMemo(() => workspace.providerTelemetry[pid()]);
  const colors = createMemo(() => statusColors(health().status));

  const gradeLabel = createMemo(() => {
    const s = health().status;
    if (s === "healthy") return t("models.qualityGradeHealthy");
    if (s === "warning") return t("models.qualityGradeWarning");
    if (s === "degraded") return t("models.qualityGradeDegraded");
    return "—";
  });

  return (
    <Show when={pid().length > 0}>
      <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/45 p-3.5 space-y-3">
        {/* Header Row */}
        <div class="flex items-center justify-between gap-2 flex-wrap">
          <div class="flex items-center gap-2 min-w-0">
            <div class="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-[var(--accent-surface)] text-[var(--accent-primary)]">
              <Activity size={14} />
            </div>
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <span class="text-[12px] font-semibold text-[var(--text-primary)]">
                  {t("models.qualityTitle")}
                </span>
                <Show when={health().status !== "untested"}>
                  <span
                    class={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10.5px] font-semibold ${colors().pill}`}
                  >
                    <span class={`h-1.5 w-1.5 rounded-full ${colors().dot}`} />
                    <span>
                      {health().grade} · {t("models.qualityScoreBadge", { score: health().score })} ·{" "}
                      {gradeLabel()}
                    </span>
                  </span>
                </Show>
              </div>
              <div class="text-[10.5px] text-[var(--text-muted)] truncate">
                {t("models.qualitySubtitle")}
              </div>
            </div>
          </div>

          <Show when={health().status !== "untested"}>
            <div class="flex items-center gap-2 shrink-0">
              <span class="font-mono text-[10.5px] text-[var(--text-faint)]">
                {t("models.qualityTurnsCount", { count: health().totalTurns })}
              </span>
              <button
                type="button"
                onClick={() => void resetProviderTelemetry(pid())}
                class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[10.5px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <RotateCcw size={10} />
                {t("models.qualityResetStats")}
              </button>
            </div>
          </Show>
        </div>

        <Show
          when={health().status !== "untested"}
          fallback={
            <div class="rounded-xl border border-dashed border-[var(--border-subtle)] bg-[var(--bg-panel)]/50 px-3 py-2.5 text-[11.5px] text-[var(--text-muted)]">
              {t("models.qualityUntested")}
            </div>
          }
        >
          {/* Risk Tags Row */}
          <Show when={health().riskTags.length > 0}>
            <div class="flex items-center gap-1.5 flex-wrap">
              <For each={health().riskTags}>
                {(tag) => (
                  <span class="inline-flex items-center gap-1 rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2 py-0.5 text-[10.5px] font-medium text-[var(--danger-text)]">
                    <AlertTriangle size={11} />
                    {riskTagLabel(tag)}
                  </span>
                )}
              </For>
            </div>
          </Show>

          {/* 4-Dimension Metric Grid */}
          <div class="grid grid-cols-2 sm:grid-cols-4 gap-2">
            {/* 1. Speed: TTFT & TPS */}
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-2 space-y-0.5">
              <div class="flex items-center gap-1 text-[10.5px] text-[var(--text-muted)]">
                <Zap size={11} class="text-[var(--accent-primary)]" />
                <span class="truncate">{t("models.qualityMetricSpeed")}</span>
              </div>
              <div class="font-mono text-[13px] font-semibold text-[var(--text-primary)]">
                {formatTtftLabel(health().avgTtftMs)}
                <Show when={health().avgTps}>
                  <span class="text-[11px] font-normal text-[var(--text-secondary)]">
                    {" "}
                    · {health().avgTps} tok/s
                  </span>
                </Show>
              </div>
            </div>

            {/* 2. Reliability: Success Rate & Timeouts */}
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-2 space-y-0.5">
              <div class="flex items-center gap-1 text-[10.5px] text-[var(--text-muted)]">
                <Clock size={11} class="text-[var(--ok-text)]" />
                <span class="truncate">{t("models.qualityMetricSuccess")}</span>
              </div>
              <div class="font-mono text-[13px] font-semibold text-[var(--text-primary)]">
                {health().successRate}%
              </div>
              <div class="text-[10px] font-mono text-[var(--text-faint)] truncate">
                {t("models.qualityTimeoutsRetrySub", {
                  timeouts: rawRecord()?.timeoutTurns ?? 0,
                  retries: health().retryCount,
                })}
              </div>
            </div>

            {/* 3. Tool Accuracy & Anti-Hallucination */}
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-2 space-y-0.5">
              <div class="flex items-center gap-1 text-[10.5px] text-[var(--text-muted)]">
                <Wrench size={11} class="text-amber-500" />
                <span class="truncate">{t("models.qualityMetricTools")}</span>
              </div>
              <div class="font-mono text-[13px] font-semibold text-[var(--text-primary)]">
                {health().toolSuccessRate !== null ? `${health().toolSuccessRate}%` : "100%"}
              </div>
              <div class="text-[10px] font-mono text-[var(--text-faint)] truncate">
                {t("models.qualityHallucinationSub", {
                  loops: health().hallucinationFlags,
                  reverts: health().revertCount,
                })}
              </div>
            </div>

            {/* 4. Observer Code Pass Rate & Quality Score */}
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-2 space-y-0.5">
              <div class="flex items-center gap-1 text-[10.5px] text-[var(--text-muted)]">
                <CheckCircle2 size={11} class="text-[var(--accent-primary)]" />
                <span class="truncate">{t("models.qualityMetricPassRate")}</span>
              </div>
              <div class="font-mono text-[13px] font-semibold text-[var(--text-primary)]">
                {health().codePassRate !== null ? `${health().codePassRate}%` : "—"}
              </div>
              <div class="text-[10px] font-mono text-[var(--text-faint)] truncate">
                <Show
                  when={health().auditTotalCount > 0}
                  fallback={<span>{t("models.qualityNoAuditYet")}</span>}
                >
                  {t("models.qualityPassRateSub", {
                    passed: health().auditPassCount,
                    total: health().auditTotalCount,
                    avgScore: health().avgAuditScore ?? 0,
                  })}
                </Show>
              </div>
            </div>
          </div>

          {/* Recent Anomaly & Hallucination Incidents */}
          <Show when={health().recentIncidents.length > 0}>
            <div class="space-y-1 pt-0.5">
              <div class="flex items-center gap-1 text-[10.5px] font-medium text-[var(--text-secondary)]">
                <ShieldAlert size={11} class="text-[var(--warn-text)]" />
                <span>{t("models.qualityRecentIncidents")}</span>
              </div>
              <div class="max-h-[96px] overflow-y-auto space-y-1 pr-1">
                <For each={health().recentIncidents.slice(0, 5)}>
                  {(inc) => (
                    <div class="flex items-center justify-between gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/80 px-2.5 py-1 text-[11px]">
                      <div class="flex items-center gap-1.5 min-w-0">
                        <span class="shrink-0 rounded bg-[var(--danger-surface)] px-1.5 py-[1px] text-[10px] font-medium text-[var(--danger-text)]">
                          {incidentTypeLabel(inc.type)}
                        </span>
                        <Show when={inc.modelID}>
                          <span class="shrink-0 font-mono text-[10px] text-[var(--text-muted)]">
                            [{inc.modelID}]
                          </span>
                        </Show>
                        <span class="truncate font-mono text-[10.5px] text-[var(--text-secondary)]">
                          {inc.detail}
                        </span>
                      </div>
                      <span class="shrink-0 font-mono text-[10px] text-[var(--text-faint)]">
                        {new Date(inc.timestamp).toLocaleTimeString([], {
                          hour: "2-digit",
                          minute: "2-digit",
                        })}
                      </span>
                    </div>
                  )}
                </For>
              </div>
            </div>
          </Show>
        </Show>
      </div>
    </Show>
  );
}
