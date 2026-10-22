import { createMemo, Index, onMount, Show } from "solid-js";
import { BarChart3, ChevronRight, Trophy } from "lucide-solid";
import {
  computeDailyDevPulseStats,
  openDiffInInspector,
  openLeaderboardModal,
  openSettingsModal,
  refreshDailyDevPulse,
} from "../../store/workspace";
import { ProviderIcon } from "../icons";
import { t } from "../../i18n";
import { formatCompactNumber as formatCompactTokens, formatUsd } from "../../i18n/format";

export default function DevPulseContent(props: { onNavigate: () => void }) {
  const stats = createMemo(() => computeDailyDevPulseStats());

  const modelsUsed = createMemo(
    () => stats().modelsUsed.slice(0, 5),
    [],
    {
      equals: (prev, next) =>
        prev.length === next.length &&
        prev.every(
          (item, i) =>
            item.providerID === next[i].providerID &&
            item.modelID === next[i].modelID &&
            item.totalTokens === next[i].totalTokens
        ),
    }
  );

  onMount(() => {
    void refreshDailyDevPulse(false);
  });

  return (
    <div class="select-text">
      <div class="mt-3 grid grid-cols-2 gap-2">
        <div class="rounded-lg bg-[var(--bg-inset)]/60 px-2.5 py-2">
          <div class="text-[11px] text-[var(--text-muted)]">{t("header.pulseTodayTokens")}</div>
          <div
            class="mt-1 font-mono text-[15px] font-semibold text-[var(--text-primary)] leading-tight"
            title={stats().totalTokens.toLocaleString()}
          >
            {formatCompactTokens(stats().totalTokens)}
          </div>
        </div>

        <div
          class="rounded-lg bg-[var(--bg-inset)]/60 px-2.5 py-2"
          title={
            stats().unpricedModels.length > 0
              ? t("header.pulseCostUnpriced", { models: stats().unpricedModels.join(", ") })
              : t("header.pulseCostTooltip")
          }
        >
          <div class="text-[11px] text-[var(--text-muted)]">{t("header.pulseTodayCost")}</div>
          <div class="mt-1 font-mono text-[15px] font-semibold text-[var(--text-primary)] leading-tight">
            {formatUsd(stats().totalCost)}
          </div>
          <Show when={stats().unpricedModels.length > 0}>
            <div class="mt-0.5 text-[10px] leading-tight text-[var(--warn-text)]">
              {t("header.pulseCostPartial")}
            </div>
          </Show>
        </div>

        <button
          type="button"
          onClick={() => {
            props.onNavigate();
            openDiffInInspector();
          }}
          class="rounded-lg bg-[var(--bg-inset)]/60 hover:bg-[var(--bg-hover)] px-2.5 py-2 text-left transition-colors cursor-pointer"
        >
          <div class="text-[11px] text-[var(--text-muted)]">{t("header.pulseLinesAdded")}</div>
          <div class="mt-1 font-mono text-[15px] font-semibold text-emerald-500 leading-tight">
            +{stats().totalAdditions.toLocaleString()}
          </div>
        </button>

        <button
          type="button"
          onClick={() => {
            props.onNavigate();
            openDiffInInspector();
          }}
          class="rounded-lg bg-[var(--bg-inset)]/60 hover:bg-[var(--bg-hover)] px-2.5 py-2 text-left transition-colors cursor-pointer"
        >
          <div class="text-[11px] text-[var(--text-muted)]">{t("header.pulseLinesDeleted")}</div>
          <div class="mt-1 font-mono text-[15px] font-semibold text-rose-500 leading-tight">
            -{stats().totalDeletions.toLocaleString()}
          </div>
        </button>
      </div>

      <Show when={modelsUsed().length > 0}>
        <div class="mt-2.5 pt-2.5 border-t border-[var(--border-subtle)] space-y-1">
          <Index each={modelsUsed()}>
            {(m) => (
              <div class="flex items-center justify-between gap-2 px-1 py-1 text-[12px]">
                <div class="flex items-center gap-1.5 min-w-0 flex-1">
                  <ProviderIcon id={m().providerID} class="w-3.5 h-3.5 shrink-0" />
                  <span class="font-mono truncate text-[var(--text-secondary)]">{m().modelID}</span>
                </div>
                <span class="font-mono text-[11px] text-[var(--text-muted)] shrink-0">
                  {formatCompactTokens(m().totalTokens)}
                </span>
              </div>
            )}
          </Index>
        </div>
      </Show>

      <div class="mt-2 pt-2 border-t border-[var(--border-subtle)]">
        <button
          type="button"
          onClick={() => {
            props.onNavigate();
            openSettingsModal("usage");
          }}
          class="w-full flex items-center justify-between rounded-lg px-2 py-1.5 text-[12px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer group"
        >
          <div class="flex items-center gap-1.5 truncate">
            <BarChart3 size={13} class="text-[var(--accent-primary)] shrink-0" />
            <span class="truncate font-medium">{t("header.pulseViewUsageHistory")}</span>
          </div>
          <ChevronRight size={13} class="text-[var(--text-muted)] group-hover:translate-x-0.5 transition-transform shrink-0" />
        </button>
        <button
          type="button"
          onClick={() => {
            props.onNavigate();
            openLeaderboardModal();
          }}
          class="w-full flex items-center justify-between rounded-lg px-2 py-1.5 text-[12px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer group"
        >
          <div class="flex items-center gap-1.5 truncate">
            <Trophy size={13} class="text-amber-500 shrink-0" />
            <span class="truncate font-medium">{t("header.pulseLeaderboardEntry")}</span>
          </div>
          <ChevronRight size={13} class="text-[var(--text-muted)] group-hover:translate-x-0.5 transition-transform shrink-0" />
        </button>
      </div>
    </div>
  );
}
