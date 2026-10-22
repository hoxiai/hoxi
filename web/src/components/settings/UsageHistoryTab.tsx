// web/src/components/settings/UsageHistoryTab.tsx
// Daily token/cost/code history. Reads raw counters from the gateway and prices
// them with the same resolver as the header pulse.
import { createMemo, For, onMount, Show } from "solid-js";
import { RefreshCw } from "lucide-solid";
import { t } from "../../i18n";
import {
  loadUsageHistory,
  setUsageHistoryRange,
  usageHistory,
  usageHistoryError,
  usageHistoryLoading,
  usageHistoryRange,
  usageHistoryTotals,
  USAGE_HISTORY_RANGES,
  type UsageHistoryDay,
  type UsageHistoryRange,
} from "../../store/workspace";
import { formatCompactTokens, formatUsd } from "./usage-format";
import { UsageTrendChart } from "./UsageTrendChart";

const button =
  "inline-flex items-center justify-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1.5 text-[12px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] disabled:opacity-50 cursor-pointer";

const card = "rounded-xl bg-[var(--bg-inset)]/60 px-3 py-2";

function Metric(props: { label: string; value: string; title?: string; tone?: "cost" }) {
  return (
    <div class={card} title={props.title}>
      <div class="text-[11px] text-[var(--text-muted)]">{props.label}</div>
      <div
        class={`mt-1 font-mono text-[15px] font-semibold leading-tight ${
          props.tone === "cost" ? "text-[var(--text-primary)]" : "text-[var(--text-primary)]"
        }`}
      >
        {props.value}
      </div>
    </div>
  );
}

function DayRow(props: { day: UsageHistoryDay }) {
  return (
    <tr class="border-t border-[var(--border-subtle)]/60 hover:bg-[var(--bg-hover)]/40">
      <td class="px-2 py-1.5 font-mono text-[11px] text-[var(--text-secondary)] whitespace-nowrap">
        {props.day.date}
      </td>
      <td class="px-2 py-1.5 text-right font-mono text-[11px] text-[var(--text-secondary)]">
        {props.day.turns.toLocaleString()}
      </td>
      <td class="px-2 py-1.5 text-right font-mono text-[11px] text-[var(--text-secondary)]">
        {props.day.sessions.toLocaleString()}
      </td>
      <td class="px-2 py-1.5 text-right font-mono text-[11px] text-[var(--text-primary)]">
        {formatCompactTokens(props.day.totalTokens)}
      </td>
      <td class="px-2 py-1.5 text-right font-mono text-[11px] text-[var(--text-muted)]">
        {formatCompactTokens(props.day.inputTokens)}
      </td>
      <td class="px-2 py-1.5 text-right font-mono text-[11px] text-[var(--text-muted)]">
        {formatCompactTokens(props.day.outputTokens)}
      </td>
      <td class="px-2 py-1.5 text-right font-mono text-[11px] text-[var(--text-primary)]">
        {formatUsd(props.day.cost)}
      </td>
      <td class="px-2 py-1.5 text-right font-mono text-[11px] text-[var(--text-muted)]">
        <Show when={props.day.commits > 0} fallback="—">
          {props.day.commits}
          <span class="ml-1 text-emerald-500">+{props.day.additions}</span>
          <span class="ml-1 text-rose-500">-{props.day.deletions}</span>
        </Show>
      </td>
    </tr>
  );
}

export default function UsageHistoryTab() {
  onMount(() => {
    void loadUsageHistory(usageHistoryRange());
  });

  const totals = createMemo(() => usageHistoryTotals(usageHistory()));
  const points = createMemo(() =>
    usageHistory().map((d) => ({ date: d.date, tokens: d.totalTokens, cost: d.cost }))
  );
  const hasData = createMemo(() => usageHistory().some((d) => d.totalTokens > 0 || d.turns > 0));

  return (
    <div class="space-y-5 select-text">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="space-y-1">
          <h3 class="text-[15px] font-semibold text-[var(--text-primary)]">
            {t("settings.usageTitle")}
          </h3>
          <p class="text-[12px] leading-relaxed text-[var(--text-muted)]">
            {t("settings.usageSubtitle")}
          </p>
        </div>
        <div class="flex items-center gap-1.5">
          <div class="flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5">
            <For each={USAGE_HISTORY_RANGES}>
              {(range: UsageHistoryRange) => (
                <button
                  type="button"
                  class={`rounded-md px-2 py-1 text-[11px] font-medium transition-colors cursor-pointer ${
                    usageHistoryRange() === range
                      ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                      : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
                  }`}
                  onClick={() => void loadUsageHistory(range)}
                >
                  {t("settings.usageRangeDays", { days: range })}
                </button>
              )}
            </For>
          </div>
          <button
            type="button"
            class={button}
            disabled={usageHistoryLoading()}
            title={t("settings.usageRefresh")}
            aria-label={t("settings.usageRefresh")}
            onClick={() => void loadUsageHistory(usageHistoryRange(), true)}
          >
            <RefreshCw size={13} class={usageHistoryLoading() ? "animate-spin" : ""} />
          </button>
        </div>
      </div>

      <Show when={usageHistoryError()}>
        <p class="rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] px-3 py-2 text-[12px] text-[var(--danger-text)]">
          {t("settings.usageLoadError", { error: usageHistoryError() })}
        </p>
      </Show>

      <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <Metric
          label={t("settings.usageTotalTokens")}
          value={formatCompactTokens(totals().totalTokens)}
          title={`${totals().totalTokens.toLocaleString()} tokens`}
        />
        <Metric
          label={t("settings.usageTotalCost")}
          value={formatUsd(totals().cost)}
          tone="cost"
          title={
            totals().unpricedModels.length > 0
              ? t("header.pulseCostUnpriced", { models: totals().unpricedModels.join(", ") })
              : t("header.pulseCostTooltip")
          }
        />
        <Metric
          label={t("settings.usageTotalTurns")}
          value={totals().turns.toLocaleString()}
        />
        <Metric
          label={t("settings.usageTotalCommits")}
          value={`${totals().commits} · +${totals().additions}/-${totals().deletions}`}
        />
      </div>

      <Show when={totals().unpricedModels.length > 0}>
        <p class="text-[11px] leading-relaxed text-[var(--warn-text)]">
          {t("header.pulseCostUnpriced", { models: totals().unpricedModels.join(", ") })}
        </p>
      </Show>

      <section class="space-y-2">
        <h4 class="text-[12px] font-semibold text-[var(--text-secondary)]">
          {t("settings.usageTrendTokens")}
        </h4>
        <UsageTrendChart points={points()} metric="tokens" />
      </section>

      <section class="space-y-2">
        <h4 class="text-[12px] font-semibold text-[var(--text-secondary)]">
          {t("settings.usageTrendCost")}
        </h4>
        <UsageTrendChart points={points()} metric="cost" height={90} />
      </section>

      <Show
        when={hasData()}
        fallback={
          <p class="rounded-xl border border-dashed border-[var(--border-subtle)] px-3 py-6 text-center text-[12px] text-[var(--text-muted)]">
            {t("settings.usageEmpty")}
          </p>
        }
      >
        <section class="space-y-2">
          <h4 class="text-[12px] font-semibold text-[var(--text-secondary)]">
            {t("settings.usageDailyTable")}
          </h4>
          <div class="overflow-x-auto rounded-xl border border-[var(--border-subtle)]">
            <table class="w-full min-w-[640px] border-collapse text-[11px]">
              <thead>
                <tr class="bg-[var(--bg-inset)]/60 text-[var(--text-muted)]">
                  <th class="px-2 py-1.5 text-left font-medium">{t("settings.usageColDate")}</th>
                  <th class="px-2 py-1.5 text-right font-medium">{t("settings.usageColTurns")}</th>
                  <th class="px-2 py-1.5 text-right font-medium">
                    {t("settings.usageColSessions")}
                  </th>
                  <th class="px-2 py-1.5 text-right font-medium">{t("settings.usageColTokens")}</th>
                  <th class="px-2 py-1.5 text-right font-medium">{t("settings.usageColInput")}</th>
                  <th class="px-2 py-1.5 text-right font-medium">{t("settings.usageColOutput")}</th>
                  <th class="px-2 py-1.5 text-right font-medium">{t("settings.usageColCost")}</th>
                  <th class="px-2 py-1.5 text-right font-medium">{t("settings.usageColCode")}</th>
                </tr>
              </thead>
              <tbody>
                <For each={usageHistory().slice().reverse()}>
                  {(day) => <DayRow day={day} />}
                </For>
              </tbody>
            </table>
          </div>
          <p class="text-[10px] leading-relaxed text-[var(--text-faint)]">
            {t("settings.usageNote")}
          </p>
        </section>
      </Show>
    </div>
  );
}
