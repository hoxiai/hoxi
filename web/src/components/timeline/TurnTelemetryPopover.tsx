// web/src/components/timeline/TurnTelemetryPopover.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  createUniqueId,
  For,
  onCleanup,
  Show,
} from "solid-js";
import { Presence } from "../motion/Presence";
import { AlertTriangle } from "lucide-solid";
import { t } from "../../i18n";
import { computeProviderHealth, workspace } from "../../store/workspace";
import { ProviderIcon } from "../icons";
import {
  formatTtftLabel,
  riskTagLabel,
  statusColors,
} from "../models/ProviderHealthBadge";

function formatScore(score: number | null | undefined): string {
  if (score === null || score === undefined) return "—";
  return t("models.qualityScoreBadge", { score });
}

function formatCompactTokens(count: number): string {
  if (!count || count <= 0) return "0";
  if (count >= 1_000_000) return `${(count / 1_000_000).toFixed(1)}M`;
  if (count >= 1_000) return `${(count / 1_000).toFixed(1)}k`;
  return String(Math.round(count));
}

export default function TurnTelemetryPopover(props: {
  durationLabel: string;
  durationMs: number;
  providerID?: string;
  modelID?: string;
  toolCount: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cost: number;
}) {
  const [open, setOpen] = createSignal(false);
  const popoverID = createUniqueId();
  let containerRef: HTMLDivElement | undefined;
  let closeTimer: ReturnType<typeof setTimeout> | undefined;

  const clearCloseTimer = () => {
    if (closeTimer !== undefined) {
      clearTimeout(closeTimer);
      closeTimer = undefined;
    }
  };

  const handleMouseEnter = () => {
    clearCloseTimer();
    setOpen(true);
  };

  const handleMouseLeave = () => {
    clearCloseTimer();
    closeTimer = setTimeout(() => {
      setOpen(false);
    }, 140);
  };

  onCleanup(clearCloseTimer);

  createEffect(() => {
    if (!open()) return;
    const onPointerDown = (e: PointerEvent) => {
      if (containerRef && !containerRef.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown);
    onCleanup(() => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown);
    });
  });

  const resolvedProviderID = createMemo(
    () => (props.providerID || workspace.selectedModel?.providerID || "").trim()
  );
  const resolvedModelID = createMemo(
    () => (props.modelID || workspace.selectedModel?.modelID || "").trim()
  );

  const providerObj = createMemo(() => {
    const pid = resolvedProviderID();
    if (!pid) return undefined;
    return workspace.providers.find((p) => p.id === pid);
  });

  const providerName = createMemo(
    () => providerObj()?.name || resolvedProviderID() || "—"
  );

  const modelName = createMemo(() => {
    const mid = resolvedModelID();
    if (!mid) return "—";
    return providerObj()?.models?.[mid]?.name || mid;
  });

  const health = createMemo(() =>
    computeProviderHealth(resolvedProviderID(), resolvedModelID() || undefined)
  );

  const latestTtftMs = createMemo(() => {
    const pid = resolvedProviderID();
    const mid = resolvedModelID();
    const rec = pid ? workspace.providerTelemetry[pid] : undefined;
    const modelSamples = mid ? rec?.models?.[mid]?.ttftSamplesMs : undefined;
    if (modelSamples && modelSamples.length > 0) {
      return modelSamples[modelSamples.length - 1];
    }
    const provSamples = rec?.ttftSamplesMs;
    if (provSamples && provSamples.length > 0) {
      return provSamples[provSamples.length - 1];
    }
    return health().avgTtftMs ?? rec?.lastProbeLatencyMs ?? null;
  });

  const effectiveTps = createMemo(() => {
    if (props.outputTokens > 3 && props.durationMs > 250) {
      const turnTps =
        Math.round((props.outputTokens / (props.durationMs / 1000)) * 10) / 10;
      if (turnTps > 0 && turnTps < 800) return turnTps;
    }
    return health().avgTps;
  });

  const statusBadgeText = createMemo(() => {
    const s = health().status;
    if (s === "healthy") return t("models.qualityGradeHealthy");
    if (s === "warning") return t("models.qualityGradeWarning");
    if (s === "degraded") return t("models.qualityGradeDegraded");
    return "";
  });

  const badgeColors = createMemo(() => statusColors(health().status));

  return (
    <div
      ref={containerRef}
      class="relative inline-flex items-center"
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
    >
      <button
        type="button"
        aria-expanded={open()}
        aria-controls={popoverID}
        title={t("timeline.turnTelemetryTooltip")}
        onClick={() => setOpen((v) => !v)}
        class={`inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 font-mono text-[11.5px] tabular-nums transition-colors cursor-pointer ${
          open()
            ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
            : "text-[var(--text-faint)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-secondary)]"
        }`}
      >
        <span>{props.durationLabel}</span>
      </button>

      <Presence when={open()}>
        {(state) => (
        <div
          id={popoverID}
          role="dialog"
          data-state={state()}
          style={{ "--hoxi-origin": "bottom left" }}
          class="hoxi-popover absolute left-0 bottom-[calc(100%+6px)] z-40 w-[288px] rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)]/98 backdrop-blur-xl p-3 text-[11.5px] shadow-xl space-y-2.5"
        >
          {/* Model & Health Header */}
          <div class="flex items-start justify-between gap-2">
            <div class="flex items-center gap-2 min-w-0">
              <Show when={resolvedProviderID()}>
                <div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]">
                  <ProviderIcon id={resolvedProviderID()} class="w-3.5 h-3.5" />
                </div>
              </Show>
              <div class="min-w-0 leading-tight">
                <div class="font-mono text-[12px] font-semibold text-[var(--text-primary)] truncate">
                  {modelName()}
                </div>
                <div class="text-[10.5px] text-[var(--text-muted)] truncate">
                  {providerName()}
                </div>
              </div>
            </div>

            <Show when={statusBadgeText()}>
              <span
                class={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium shrink-0 ${badgeColors().pill}`}
              >
                <span class={`h-1.5 w-1.5 rounded-full ${badgeColors().dot}`} />
                <span>{statusBadgeText()}</span>
              </span>
            </Show>
          </div>

          {/* 2x2 Latency / Speed / Stability Metric Grid */}
          <div class="grid grid-cols-2 gap-1.5">
            <div class="rounded-lg border border-[var(--border-subtle)]/70 bg-[var(--bg-inset)]/65 px-2.5 py-1.5">
              <div class="text-[10px] text-[var(--text-muted)]">
                {t("timeline.turnDurationLabel")}
              </div>
              <div class="font-mono text-[12px] font-semibold text-[var(--text-primary)] tabular-nums">
                {props.durationLabel}
                <Show when={props.toolCount > 0}>
                  <span class="ml-1 text-[10.5px] font-normal text-[var(--text-muted)]">
                    · {t("timeline.stepsCompleted", { count: props.toolCount })}
                  </span>
                </Show>
              </div>
            </div>

            <div class="rounded-lg border border-[var(--border-subtle)]/70 bg-[var(--bg-inset)]/65 px-2.5 py-1.5">
              <div class="text-[10px] text-[var(--text-muted)]">
                {t("timeline.turnTtftLabel")}
              </div>
              <div class="font-mono text-[12px] font-semibold text-[var(--text-primary)] tabular-nums">
                {formatTtftLabel(latestTtftMs())}
              </div>
            </div>

            <div class="rounded-lg border border-[var(--border-subtle)]/70 bg-[var(--bg-inset)]/65 px-2.5 py-1.5">
              <div class="text-[10px] text-[var(--text-muted)]">
                {t("timeline.turnSpeedLabel")}
              </div>
              <div class="font-mono text-[12px] font-semibold text-[var(--text-primary)] tabular-nums">
                {effectiveTps() ? `${effectiveTps()} tok/s` : "—"}
              </div>
            </div>

            <div class="rounded-lg border border-[var(--border-subtle)]/70 bg-[var(--bg-inset)]/65 px-2.5 py-1.5">
              <div class="text-[10px] text-[var(--text-muted)]">
                {t("models.qualityMetricSuccess")}
              </div>
              <div class="font-mono text-[12px] font-semibold text-[var(--text-primary)] tabular-nums">
                {health().totalTurns > 0
                  ? formatScore(health().successRate)
                  : "—"}
              </div>
            </div>
          </div>

          {/* Tokens & Cost Row */}
          <Show when={props.inputTokens > 0 || props.outputTokens > 0}>
            <div class="flex items-center justify-between gap-2 border-t border-[var(--border-subtle)]/60 pt-2 font-mono text-[10.5px] text-[var(--text-muted)]">
              <span class="truncate">
                {t("timeline.turnTokensSummary", {
                  input: formatCompactTokens(props.inputTokens),
                  output: formatCompactTokens(props.outputTokens),
                })}
              </span>
              <div class="flex items-center gap-1.5 shrink-0">
                <Show when={props.cacheReadTokens > 0}>
                  <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] text-[var(--text-secondary)]">
                    {t("timeline.turnCacheHitBadge", {
                      tokens: formatCompactTokens(props.cacheReadTokens),
                    })}
                  </span>
                </Show>
                <Show when={props.cost > 0}>
                  <span class="text-[var(--text-secondary)]">
                    ${props.cost < 0.01 ? props.cost.toFixed(4) : props.cost.toFixed(2)}
                  </span>
                </Show>
              </div>
            </div>
          </Show>

          {/* Risk Tags */}
          <Show when={health().riskTags.length > 0}>
            <div class="flex items-center gap-1 flex-wrap border-t border-[var(--border-subtle)]/60 pt-2">
              <For each={health().riskTags}>
                {(tag) => (
                  <span class="inline-flex items-center gap-1 rounded-md border border-[var(--warn-border)]/70 bg-[var(--warn-surface)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--warn-text)]">
                    <AlertTriangle size={10} />
                    {riskTagLabel(tag)}
                  </span>
                )}
              </For>
            </div>
          </Show>
        </div>
        )}
      </Presence>
    </div>
  );
}
