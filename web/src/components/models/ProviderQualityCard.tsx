// web/src/components/models/ProviderQualityCard.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Clock,
  RotateCcw,
  ShieldAlert,
  Wrench,
  X,
  Zap,
} from "lucide-solid";
import {
  computeProviderHealth,
  openDiagnostics,
  resetProviderTelemetry,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import {
  formatTtftLabel,
  formatHealthScore,
  incidentTypeLabel,
  ProviderHealthBadge,
  riskTagLabel,
} from "./ProviderHealthBadge";

export { formatTtftLabel, ProviderHealthBadge, riskTagLabel };

/**
 * Independent stability and quality scores with supporting provider diagnostics.
 */
export default function ProviderQualityCard(props: { providerID: string }) {
  const pid = () => props.providerID.trim();
  const health = createMemo(() => computeProviderHealth(pid()));
  const rawRecord = createMemo(() => workspace.providerTelemetry[pid()]);
  const [detailsOpen, setDetailsOpen] = createSignal(false);

  return (
    <Show when={pid().length > 0}>
      <div class="flex items-center justify-between gap-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/45 px-3 py-2">
        <div class="flex min-w-0 items-center gap-2">
          <Activity size={14} class="shrink-0 text-[var(--accent-primary)]" />
          <span class="truncate text-[11.5px] font-medium text-[var(--text-secondary)]">
            {t("models.qualityTitle")}
          </span>
          <ProviderHealthBadge providerID={pid()} compact />
        </div>
        <button
          type="button"
          onClick={() => setDetailsOpen(true)}
          class="inline-flex shrink-0 items-center rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1.5 text-[11px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
        >
          {t("diagnostics.view")}
        </button>
      </div>
      <Presence when={detailsOpen()}>
        {(state) => (
        <div
          data-state={state()}
          class="hoxi-modal-backdrop fixed inset-0 z-[60] flex items-center justify-center p-4"
          onClick={(event) => {
            if (event.target === event.currentTarget) setDetailsOpen(false);
          }}
        >
          <div
            role="dialog"
            aria-modal="true"
            aria-label={t("models.qualityTitle")}
            class="hoxi-modal-panel hoxi-elevation-4 flex max-h-[85dvh] w-full max-w-[680px] flex-col overflow-hidden rounded-xl border border-[var(--border-strong)] bg-[var(--bg-panel)] shadow-2xl"
            onClick={(event) => event.stopPropagation()}
          >
            <div class="flex items-center justify-between gap-3 border-b border-[var(--border-subtle)] px-4 py-3">
              <div class="flex min-w-0 items-center gap-2">
                <Activity size={15} class="shrink-0 text-[var(--accent-primary)]" />
                <span class="truncate text-[13px] font-semibold text-[var(--text-primary)]">
                  {t("models.qualityTitle")}
                </span>
                <ProviderHealthBadge providerID={pid()} compact />
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <button
                  type="button"
                  onClick={() => openDiagnostics({ providerID: pid() })}
                  class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2.5 py-1.5 text-[11px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  {t("diagnostics.view")}
                </button>
                <button
                  type="button"
                  onClick={() => setDetailsOpen(false)}
                  aria-label={t("common.close")}
                  class="rounded-lg px-2 py-1.5 text-[12px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                >
                  <X size={14} />
                </button>
              </div>
            </div>
            <div class="space-y-3 overflow-y-auto p-4">
              <div class="space-y-3">
                <div class="flex items-center justify-between gap-2 flex-wrap">
                  <div class="flex min-w-0 items-center gap-2">
                    <div class="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-[var(--accent-surface)] text-[var(--accent-primary)]">
                      <Activity size={14} />
                    </div>
                    <div class="min-w-0">
                      <div class="flex flex-wrap items-center gap-2">
                        <span class="text-[12px] font-semibold text-[var(--text-primary)]">
                          {t("models.qualityTitle")}
                        </span>
                        <ProviderHealthBadge providerID={pid()} compact />
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

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
          <div class="py-2 space-y-1">
            <div class="text-[11px] font-medium text-[var(--text-secondary)]">
              {t("models.qualityStabilityScore")}
            </div>
            <div class="font-mono text-base text-[var(--text-secondary)]">
              {formatHealthScore(health().stabilityScore)}
            </div>
            <div class="text-[10.5px] text-[var(--text-muted)]">
              {t("models.qualityTurnsCount", { count: health().totalTurns })}
            </div>
            <p class="text-[10.5px] text-[var(--text-muted)]">
              {t("models.qualityStabilityExplanation")}
            </p>
          </div>
          <div class="py-2 space-y-1">
            <div class="text-[11px] font-medium text-[var(--text-secondary)]">
              {t("models.qualityQualityScore")}
            </div>
            <div class="font-mono text-base text-[var(--text-secondary)]">
              {formatHealthScore(health().qualityScore)}
            </div>
            <div class="text-[10.5px] text-[var(--text-muted)]">
              {t("models.qualitySamplesCount", { count: health().qualitySampleCount })}
            </div>
            <p class="text-[10.5px] text-[var(--text-muted)]">
              {t("models.qualityQualityExplanation")}
            </p>
          </div>
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
                {health().toolSuccessRate !== null ? `${health().toolSuccessRate}%` : t("models.qualityNoData")}
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
                    avgScore: health().avgAuditScore ?? t("models.qualityNoData"),
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
            </div>
          </div>
        </div>
        )}
      </Presence>
    </Show>
  );
}
