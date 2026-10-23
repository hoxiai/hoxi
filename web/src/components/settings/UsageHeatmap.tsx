// web/src/components/settings/UsageHeatmap.tsx
// GitHub-style contribution heatmap for the usage overview. One square per
// day, shaded by that day's token count, laid out as weeks (Sunday-first).
import { createMemo, Index, Show } from "solid-js";
import { locale, t } from "../../i18n";
import { formatCompactTokens } from "./usage-format";

const CELL = 11;
const GAP = 3;

// Level 0 is an idle day; 1..4 tint the accent blue by intensity.
const LEVEL = [
  "bg-[var(--bg-inset)]",
  "bg-blue-500/25",
  "bg-blue-500/45",
  "bg-blue-500/70",
  "bg-blue-500",
];

interface HeatCell {
  date: string;
  tokens: number;
  level: number;
}

function parseDate(value: string): Date {
  const [year, month, day] = value.split("-").map(Number);
  return new Date(year, (month || 1) - 1, day || 1);
}

function toKey(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

function buildGrid(points: Array<{ date: string; tokens: number }>) {
  const sorted = [...points].sort((a, b) => a.date.localeCompare(b.date));
  if (sorted.length === 0) return { weeks: [] as Array<Array<HeatCell | null>>, months: [] as Array<string | null> };

  const tokenByDate = new Map(sorted.map((point) => [point.date, point.tokens]));
  const start = parseDate(sorted[0].date);
  const end = parseDate(sorted[sorted.length - 1].date);
  const max = Math.max(1, ...sorted.map((point) => point.tokens));
  const levelOf = (tokens: number) =>
    tokens <= 0 ? 0 : Math.min(4, Math.max(1, Math.ceil((tokens / max) * 4)));

  const cells: Array<HeatCell | null> = [];
  for (let i = 0; i < start.getDay(); i++) cells.push(null);
  for (const cursor = new Date(start); cursor <= end; cursor.setDate(cursor.getDate() + 1)) {
    const key = toKey(cursor);
    const tokens = tokenByDate.get(key) ?? 0;
    cells.push({ date: key, tokens, level: levelOf(tokens) });
  }
  while (cells.length % 7 !== 0) cells.push(null);

  const weeks: Array<Array<HeatCell | null>> = [];
  for (let i = 0; i < cells.length; i += 7) weeks.push(cells.slice(i, i + 7));

  const monthFormat = new Intl.DateTimeFormat(locale(), { month: "short" });
  const topDate = (week: Array<HeatCell | null>) => {
    const first = week.find((cell) => cell);
    return first ? parseDate(first.date) : null;
  };
  const months = weeks.map((week, index) => {
    const current = topDate(week);
    if (!current) return null;
    if (index === 0) return monthFormat.format(current);
    const previous = topDate(weeks[index - 1]);
    return previous && previous.getMonth() !== current.getMonth()
      ? monthFormat.format(current)
      : null;
  });

  return { weeks, months };
}

export function UsageHeatmap(props: { points: Array<{ date: string; tokens: number }> }) {
  const grid = createMemo(() => buildGrid(props.points));
  const cells = createMemo(() => grid().weeks.flat());

  return (
    <div class="flex flex-col items-center gap-2">
      <div class="w-full overflow-x-auto pb-1">
        <div class="mx-auto flex w-max gap-1">
          <div
            class="grid grid-rows-7 text-[10px] text-[var(--text-faint)]"
            style={{ "grid-auto-rows": `${CELL}px`, gap: `${GAP}px` }}
          >
            <Index each={["", "M", "", "W", "", "F", ""]}>
              {(label) => <div class="flex items-center leading-none">{label()}</div>}
            </Index>
          </div>
          <div>
            <div
              class="grid grid-flow-col"
              style={{ "grid-auto-columns": `${CELL}px`, gap: `${GAP}px` }}
            >
              <Index each={grid().months}>
                {(label) => (
                  <div class="relative h-3.5">
                    <Show when={label()}>
                      <span class="absolute left-0 top-0 whitespace-nowrap text-[10px] text-[var(--text-faint)]">
                        {label()}
                      </span>
                    </Show>
                  </div>
                )}
              </Index>
            </div>
            <div
              class="grid grid-flow-col grid-rows-7"
              style={{ "grid-auto-columns": `${CELL}px`, gap: `${GAP}px` }}
            >
              <Index each={cells()}>
                {(cell) => (
                  <div
                    class={`rounded-[2px] ${cell() ? LEVEL[cell()!.level] : ""}`}
                    style={{ width: `${CELL}px`, height: `${CELL}px` }}
                    title={
                      cell()
                        ? `${cell()!.date} · ${formatCompactTokens(cell()!.tokens)} ${t("settings.usageTotalTokens")}`
                        : undefined
                    }
                  />
                )}
              </Index>
            </div>
          </div>
        </div>
      </div>
      <div class="flex items-center gap-1.5 text-[10px] text-[var(--text-faint)]">
        <span>{t("settings.usageHeatmapLess")}</span>
        <Index each={[0, 1, 2, 3, 4]}>
          {(level) => (
            <span class={`inline-block h-[10px] w-[10px] rounded-[2px] ${LEVEL[level()]}`} />
          )}
        </Index>
        <span>{t("settings.usageHeatmapMore")}</span>
      </div>
    </div>
  );
}
