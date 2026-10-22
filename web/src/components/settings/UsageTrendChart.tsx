// web/src/components/settings/UsageTrendChart.tsx
// Dependency-free bar chart for the daily usage series. SVG keeps it crisp at
// any width and avoids pulling a charting library into the settings bundle.
import { createMemo, For, Show } from "solid-js";
import { t } from "../../i18n";
import { shortDayLabel, formatCompactTokens, formatUsd } from "./usage-format";

export interface UsageTrendPoint {
  date: string;
  tokens: number;
  cost: number;
}

export function UsageTrendChart(props: {
  points: UsageTrendPoint[];
  metric: "tokens" | "cost";
  height?: number;
}) {
  const height = () => props.height ?? 120;
  const maxValue = createMemo(() => {
    const values = props.points.map((p) => (props.metric === "tokens" ? p.tokens : p.cost));
    return Math.max(...values, 0);
  });

  const valueOf = (p: UsageTrendPoint) => (props.metric === "tokens" ? p.tokens : p.cost);
  const format = (n: number) => (props.metric === "tokens" ? formatCompactTokens(n) : formatUsd(n));

  return (
    <div class="w-full">
      <div
        class="flex items-end gap-[2px] w-full rounded-lg bg-[var(--bg-inset)]/40 px-2 pt-2 pb-1"
        style={{ height: `${height()}px` }}
        role="img"
        aria-label={t("settings.usageChartLabel")}
      >
        <For each={props.points}>
          {(point) => {
            const value = valueOf(point);
            const ratio = maxValue() > 0 ? value / maxValue() : 0;
            // Keep a 2px stub for zero days so the axis stays readable.
            const barHeight = value > 0 ? Math.max(2, Math.round(ratio * (height() - 28))) : 2;
            return (
              <div class="flex-1 min-w-0 flex flex-col items-center justify-end h-full group relative">
                <div
                  class={`w-full max-w-[26px] rounded-t transition-colors ${
                    value > 0
                      ? "bg-[var(--accent-primary)]/70 group-hover:bg-[var(--accent-primary)]"
                      : "bg-[var(--border-subtle)]"
                  }`}
                  style={{ height: `${barHeight}px` }}
                />
                <span class="pointer-events-none absolute bottom-full mb-1 z-10 hidden group-hover:block whitespace-nowrap rounded-md border border-[var(--border-strong)] bg-[var(--bg-panel)] px-1.5 py-0.5 text-[10px] text-[var(--text-secondary)] shadow-lg">
                  {point.date} · {format(value)}
                </span>
              </div>
            );
          }}
        </For>
      </div>
      <Show when={props.points.length > 0}>
        <div class="mt-1 flex items-center justify-between px-2 text-[10px] text-[var(--text-faint)]">
          <span>{shortDayLabel(props.points[0].date)}</span>
          <Show when={props.points.length > 2}>
            <span>{shortDayLabel(props.points[Math.floor(props.points.length / 2)].date)}</span>
          </Show>
          <span>{shortDayLabel(props.points[props.points.length - 1].date)}</span>
        </div>
      </Show>
    </div>
  );
}
