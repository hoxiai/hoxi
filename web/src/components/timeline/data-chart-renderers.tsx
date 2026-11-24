// web/src/components/timeline/data-chart-renderers.tsx
// SVG chart renderers (Cartesian Bar/Line/Area, Horizontal Bar, Pie/Donut)
// and SVG serialization helper used by DataChartView.
import { createMemo, For, Show } from "solid-js";
import { t } from "../../i18n";
import {
  formatChartNumber,
  seriesColor,
  type ChartKind,
  type ParsedChartSpec,
} from "./chart-spec";

export const CHART_WIDTH = 640;
export const CHART_HEIGHT = 276;

export interface VisibleChartSeries {
  name: string;
  values: Array<number | null>;
  color: string;
  originalIndex: number;
}

export interface ChartHoverTip {
  label: string;
  items: Array<{ name: string; value: number; color: string; sharePct?: number }>;
  xPct: number;
  yPct: number;
}

function polarToCartesian(cx: number, cy: number, r: number, angleDeg: number) {
  const rad = ((angleDeg - 90) * Math.PI) / 180;
  return { x: cx + r * Math.cos(rad), y: cy + r * Math.sin(rad) };
}

function describeArcSlice(
  cx: number,
  cy: number,
  outerR: number,
  innerR: number,
  startAngle: number,
  endAngle: number
): string {
  const span = Math.min(359.99, endAngle - startAngle);
  const startOuter = polarToCartesian(cx, cy, outerR, startAngle);
  const endOuter = polarToCartesian(cx, cy, outerR, startAngle + span);
  const largeArc = span > 180 ? 1 : 0;
  if (innerR <= 0) {
    return [
      `M ${cx} ${cy}`,
      `L ${startOuter.x.toFixed(2)} ${startOuter.y.toFixed(2)}`,
      `A ${outerR} ${outerR} 0 ${largeArc} 1 ${endOuter.x.toFixed(2)} ${endOuter.y.toFixed(2)}`,
      "Z",
    ].join(" ");
  }
  const startInner = polarToCartesian(cx, cy, innerR, startAngle + span);
  const endInner = polarToCartesian(cx, cy, innerR, startAngle);
  return [
    `M ${startOuter.x.toFixed(2)} ${startOuter.y.toFixed(2)}`,
    `A ${outerR} ${outerR} 0 ${largeArc} 1 ${endOuter.x.toFixed(2)} ${endOuter.y.toFixed(2)}`,
    `L ${startInner.x.toFixed(2)} ${startInner.y.toFixed(2)}`,
    `A ${innerR} ${innerR} 0 ${largeArc} 0 ${endInner.x.toFixed(2)} ${endInner.y.toFixed(2)}`,
    "Z",
  ].join(" ");
}

function truncateLabel(label: string, maxChars: number): string {
  if (label.length <= maxChars) return label;
  return `${label.slice(0, Math.max(1, maxChars - 1))}…`;
}

export function serializeRenderedChartSvg(el: SVGSVGElement | undefined, isDark: boolean): string {
  if (!el) return "";
  const clone = el.cloneNode(true) as SVGSVGElement;
  clone.setAttribute("xmlns", "http://www.w3.org/2000/svg");
  const vb = (el.getAttribute("viewBox") || "").trim().split(/[\s,]+/);
  if (vb.length === 4) {
    const w = parseFloat(vb[2]);
    const h = parseFloat(vb[3]);
    if (Number.isFinite(w) && w > 0 && Number.isFinite(h) && h > 0) {
      if (!clone.getAttribute("width")) clone.setAttribute("width", String(w));
      if (!clone.getAttribute("height")) clone.setAttribute("height", String(h));
    }
  }
  const textPrimary = isDark ? "#e6edf3" : "#1f2937";
  const textMuted = isDark ? "#8b949e" : "#6b7280";
  const borderSubtle = isDark ? "#30363d" : "#e5e7eb";
  const bgPanel = isDark ? "#141418" : "#ffffff";
  return new XMLSerializer()
    .serializeToString(clone)
    .replace(/var\(--text-primary\)/g, textPrimary)
    .replace(/var\(--text-secondary\)/g, textPrimary)
    .replace(/var\(--text-muted\)/g, textMuted)
    .replace(/var\(--text-faint\)/g, textMuted)
    .replace(/var\(--border-subtle\)/g, borderSubtle)
    .replace(/var\(--bg-panel\)/g, bgPanel);
}

export function CartesianChartSvg(props: {
  spec: ParsedChartSpec;
  kind: "bar" | "line" | "area";
  visibleSeries: VisibleChartSeries[];
  range: { ticks: number[]; min: number; max: number };
  hoveredIndex: number | null;
  onPointHover: (idx: number, xPx: number, yPx: number, svgH: number) => void;
  setSvgRef: (el: SVGSVGElement | undefined) => void;
}) {
  const pad = { top: 20, right: 20, bottom: 42, left: 56 };
  const plotW = CHART_WIDTH - pad.left - pad.right;
  const plotH = CHART_HEIGHT - pad.top - pad.bottom;

  const yToPx = (v: number) => {
    const ratio = (v - props.range.min) / (props.range.max - props.range.min);
    return pad.top + plotH * (1 - ratio);
  };
  const zeroY = () => yToPx(0);
  const xLabelStride = createMemo(() => {
    const n = props.spec.labels.length;
    return n <= 8 ? 1 : Math.ceil(n / 8);
  });

  return (
    <svg
      ref={props.setSvgRef}
      viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`}
      class="block w-full h-auto overflow-visible"
      role="img"
      aria-label={props.spec.title || t("timeline.chartDiagramLabel")}
    >
      <For each={props.range.ticks}>
        {(tick) => {
          const y = () => yToPx(tick);
          return (
            <g>
              <line
                x1={pad.left}
                x2={CHART_WIDTH - pad.right}
                y1={y()}
                y2={y()}
                stroke="var(--border-subtle)"
                stroke-dasharray={tick === 0 ? undefined : "3 3"}
                stroke-width={tick === 0 ? 1.2 : 0.8}
                opacity={tick === 0 ? 0.9 : 0.7}
              />
              <text
                x={pad.left - 8}
                y={y() + 3.5}
                text-anchor="end"
                fill="var(--text-muted)"
                font-size="10.5"
                font-family="ui-monospace, SFMono-Regular, Menlo, monospace"
              >
                {formatChartNumber(tick, props.spec.unit)}
              </text>
            </g>
          );
        }}
      </For>

      <For each={props.spec.labels}>
        {(label, idx) => {
          const n = () => props.spec.labels.length;
          const cx = () =>
            props.kind === "bar"
              ? pad.left + ((idx() + 0.5) / Math.max(1, n())) * plotW
              : n() <= 1
                ? pad.left + plotW / 2
                : pad.left + (idx() / (n() - 1)) * plotW;
          return (
            <Show when={idx() % xLabelStride() === 0 || idx() === n() - 1}>
              <text
                x={cx()}
                y={CHART_HEIGHT - 14}
                text-anchor="middle"
                fill="var(--text-muted)"
                font-size="10.5"
              >
                {truncateLabel(label, n() > 8 ? 10 : 16)}
              </text>
            </Show>
          );
        }}
      </For>

      <Show when={props.kind === "bar"}>
        <For each={props.spec.labels}>
          {(_, catIdx) => {
            const n = () => Math.max(1, props.spec.labels.length);
            const slotW = () => plotW / n();
            const groupPad = () => Math.min(12, slotW() * 0.16);
            const groupW = () => Math.max(4, slotW() - groupPad() * 2);
            const isStacked = () => Boolean(props.spec.stacked) && props.visibleSeries.length > 1;
            return (
              <g>
                <rect
                  x={pad.left + catIdx() * slotW()}
                  y={pad.top}
                  width={slotW()}
                  height={plotH}
                  fill={props.hoveredIndex === catIdx() ? "var(--border-subtle)" : "transparent"}
                  opacity={props.hoveredIndex === catIdx() ? 0.28 : 0}
                  onMouseEnter={() =>
                    props.onPointHover(
                      catIdx(),
                      pad.left + (catIdx() + 0.5) * slotW(),
                      pad.top + 32,
                      CHART_HEIGHT
                    )
                  }
                />
                <For each={props.visibleSeries}>
                  {(s, sIdx) => {
                    const barRect = createMemo(() => {
                      const v = s.values[catIdx()];
                      if (v === null || v === undefined) return null;
                      if (isStacked()) {
                        let base = 0;
                        for (let k = 0; k < sIdx(); k++) {
                          const prev = props.visibleSeries[k].values[catIdx()] ?? 0;
                          if ((v >= 0 && prev >= 0) || (v < 0 && prev < 0)) base += prev;
                        }
                        const y1 = yToPx(base + v);
                        const y2 = yToPx(base);
                        const barW = Math.min(44, groupW());
                        return {
                          x: pad.left + (catIdx() + 0.5) * slotW() - barW / 2,
                          y: Math.min(y1, y2),
                          w: barW,
                          h: Math.max(1, Math.abs(y2 - y1)),
                        };
                      }
                      const count = Math.max(1, props.visibleSeries.length);
                      const gap = count > 1 ? 3 : 0;
                      const barW = Math.min(40, Math.max(3, (groupW() - gap * (count - 1)) / count));
                      const totalW = barW * count + gap * (count - 1);
                      const x =
                        pad.left +
                        (catIdx() + 0.5) * slotW() -
                        totalW / 2 +
                        sIdx() * (barW + gap);
                      const yVal = yToPx(v);
                      const y0 = zeroY();
                      return {
                        x,
                        y: Math.min(yVal, y0),
                        w: barW,
                        h: Math.max(1.5, Math.abs(y0 - yVal)),
                      };
                    });
                    return (
                      <Show when={barRect()}>
                        {(r) => (
                          <rect
                            x={r().x}
                            y={r().y}
                            width={r().w}
                            height={r().h}
                            rx={Math.min(3, r().w / 3)}
                            fill={s.color}
                            opacity={
                              props.hoveredIndex === null || props.hoveredIndex === catIdx()
                                ? 0.92
                                : 0.55
                            }
                            class="transition-opacity duration-100"
                            onMouseEnter={() =>
                              props.onPointHover(catIdx(), r().x + r().w / 2, r().y, CHART_HEIGHT)
                            }
                          />
                        )}
                      </Show>
                    );
                  }}
                </For>
              </g>
            );
          }}
        </For>
      </Show>

      <Show when={props.kind === "line" || props.kind === "area"}>
        <For each={props.visibleSeries}>
          {(s) => {
            const points = createMemo(() => {
              const n = props.spec.labels.length;
              return props.spec.labels
                .map((_, idx) => {
                  const v = s.values[idx];
                  if (v === null || v === undefined) return null;
                  const x = n <= 1 ? pad.left + plotW / 2 : pad.left + (idx / (n - 1)) * plotW;
                  return { idx, x, y: yToPx(v), value: v };
                })
                .filter((p): p is { idx: number; x: number; y: number; value: number } => p !== null);
            });
            const linePath = createMemo(() =>
              points()
                .map((pt, i) => `${i === 0 ? "M" : "L"} ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`)
                .join(" ")
            );
            const areaPath = createMemo(() => {
              const pts = points();
              if (pts.length === 0) return "";
              const base = zeroY().toFixed(1);
              return `${linePath()} L ${pts[pts.length - 1].x.toFixed(1)} ${base} L ${pts[0].x.toFixed(1)} ${base} Z`;
            });
            return (
              <g>
                <Show when={props.kind === "area" && areaPath()}>
                  <path d={areaPath()} fill={s.color} opacity="0.16" />
                </Show>
                <Show when={linePath()}>
                  <path
                    d={linePath()}
                    fill="none"
                    stroke={s.color}
                    stroke-width="2.2"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                  />
                </Show>
                <For each={points()}>
                  {(pt) => (
                    <circle
                      cx={pt.x}
                      cy={pt.y}
                      r={props.hoveredIndex === pt.idx ? 4.8 : 3.2}
                      fill={s.color}
                      stroke="var(--bg-panel)"
                      stroke-width="1.5"
                    />
                  )}
                </For>
              </g>
            );
          }}
        </For>
        <For each={props.spec.labels}>
          {(_, idx) => {
            const n = () => props.spec.labels.length;
            const x = () =>
              n() <= 1 ? pad.left + plotW / 2 : pad.left + (idx() / (n() - 1)) * plotW;
            const colW = () => plotW / Math.max(1, n());
            return (
              <g>
                <Show when={props.hoveredIndex === idx()}>
                  <line
                    x1={x()}
                    x2={x()}
                    y1={pad.top}
                    y2={pad.top + plotH}
                    stroke="var(--text-muted)"
                    stroke-dasharray="3 3"
                    stroke-width="1"
                    opacity="0.55"
                  />
                </Show>
                <rect
                  x={x() - colW() / 2}
                  y={pad.top}
                  width={colW()}
                  height={plotH}
                  fill="transparent"
                  onMouseEnter={() => props.onPointHover(idx(), x(), pad.top + 28, CHART_HEIGHT)}
                />
              </g>
            );
          }}
        </For>
      </Show>
    </svg>
  );
}

export function HorizontalBarChartSvg(props: {
  spec: ParsedChartSpec;
  visibleSeries: VisibleChartSeries[];
  maxVal: number;
  hoveredIndex: number | null;
  onPointHover: (idx: number, xPx: number, yPx: number, svgH: number) => void;
  setSvgRef: (el: SVGSVGElement | undefined) => void;
}) {
  const svgH = createMemo(() => Math.max(160, props.spec.labels.length * 30 + 36));
  const leftPad = 128;
  const rightPad = 64;
  const barAreaW = CHART_WIDTH - leftPad - rightPad;

  return (
    <div class="max-h-[420px] overflow-y-auto">
    <svg
      ref={props.setSvgRef}
      viewBox={`0 0 ${CHART_WIDTH} ${svgH()}`}
      class="block w-full h-auto overflow-visible"
      role="img"
      aria-label={props.spec.title || t("timeline.chartDiagramLabel")}
    >
      <For each={props.spec.labels}>
        {(label, idx) => {
          const y = () => 18 + idx() * 30;
          const s = () => props.visibleSeries[0];
          const val = () => s()?.values[idx()] ?? 0;
          const maxV = () => (props.maxVal > 0 ? props.maxVal : 1);
          const w = () => Math.max(2, (Math.max(0, val()) / maxV()) * barAreaW);
          return (
            <g onMouseEnter={() => props.onPointHover(idx(), leftPad + w() / 2, y() + 10, svgH())}>
              <text
                x={leftPad - 10}
                y={y() + 14}
                text-anchor="end"
                fill="var(--text-secondary)"
                font-size="11"
              >
                {truncateLabel(label, 18)}
              </text>
              <rect
                x={leftPad}
                y={y() + 3}
                width={barAreaW}
                height={16}
                rx={4}
                fill="var(--border-subtle)"
                opacity="0.22"
              />
              <rect
                x={leftPad}
                y={y() + 3}
                width={w()}
                height={16}
                rx={4}
                fill={s()?.color || seriesColor(idx())}
                opacity={props.hoveredIndex === null || props.hoveredIndex === idx() ? 0.92 : 0.55}
              />
              <text
                x={leftPad + w() + 8}
                y={y() + 14.5}
                fill="var(--text-muted)"
                font-size="10.5"
                font-family="ui-monospace, SFMono-Regular, Menlo, monospace"
              >
                {formatChartNumber(val(), props.spec.unit)}
              </text>
            </g>
          );
        }}
      </For>
    </svg>
    </div>
  );
}

export function PieDonutChartView(props: {
  spec: ParsedChartSpec;
  kind: Extract<ChartKind, "pie" | "donut">;
  visibleSeries: VisibleChartSeries[];
  hoveredIndex: number | null;
  onHoverSlice: (idx: number | null, tip: ChartHoverTip | null) => void;
  setSvgRef: (el: SVGSVGElement | undefined) => void;
}) {
  const pieData = createMemo(() => {
    const first = props.visibleSeries[0];
    if (!first) return { slices: [], total: 0 };
    const entries = props.spec.labels
      .map((label, idx) => ({
        label,
        value: Math.max(0, first.values[idx] ?? 0),
        color: seriesColor(idx),
        idx,
      }))
      .filter((item) => item.value > 0);
    const total = entries.reduce((sum, item) => sum + item.value, 0);
    if (total <= 0) return { slices: [], total: 0 };
    let cursor = 0;
    const slices = entries.map((item) => {
      const startAngle = cursor;
      const sweep = (item.value / total) * 360;
      cursor += sweep;
      return {
        ...item,
        startAngle,
        endAngle: cursor,
        sharePct: (item.value / total) * 100,
      };
    });
    return { slices, total };
  });

  return (
    <Show
      when={pieData().slices.length > 0}
      fallback={
        <div class="py-8 text-center text-[12px] text-[var(--text-muted)]">
          {t("timeline.chartEmptyData")}
        </div>
      }
    >
      <div class="flex flex-col sm:flex-row items-center justify-center gap-5 py-1">
        <svg
          ref={props.setSvgRef}
          viewBox="0 0 260 220"
          class="w-[240px] max-w-full h-auto shrink-0"
          role="img"
          aria-label={props.spec.title || t("timeline.chartDiagramLabel")}
        >
          <For each={pieData().slices}>
            {(slice) => (
              <path
                d={describeArcSlice(
                  130,
                  110,
                  92,
                  props.kind === "donut" ? 52 : 0,
                  slice.startAngle,
                  slice.endAngle
                )}
                fill={slice.color}
                stroke="var(--bg-panel)"
                stroke-width="1.8"
                opacity={props.hoveredIndex === null || props.hoveredIndex === slice.idx ? 0.94 : 0.55}
                class="transition-opacity duration-100 cursor-pointer"
                onMouseEnter={() =>
                  props.onHoverSlice(slice.idx, {
                    label: slice.label,
                    items: [
                      {
                        name: props.visibleSeries[0]?.name || slice.label,
                        value: slice.value,
                        color: slice.color,
                        sharePct: slice.sharePct,
                      },
                    ],
                    xPct: 50,
                    yPct: 16,
                  })
                }
              />
            )}
          </For>
          <Show when={props.kind === "donut"}>
            <text
              x="130"
              y="106"
              text-anchor="middle"
              fill="var(--text-primary)"
              font-size="14"
              font-weight="600"
              font-family="ui-monospace, SFMono-Regular, Menlo, monospace"
            >
              {formatChartNumber(pieData().total, props.spec.unit)}
            </text>
            <text x="130" y="123" text-anchor="middle" fill="var(--text-muted)" font-size="10">
              {props.visibleSeries[0]?.name || ""}
            </text>
          </Show>
        </svg>

        <div class="flex flex-col gap-1.5 min-w-[160px] max-w-[260px] text-[11.5px]">
          <For each={pieData().slices}>
            {(slice) => (
              <div
                onMouseEnter={() => props.onHoverSlice(slice.idx, null)}
                onMouseLeave={() => props.onHoverSlice(null, null)}
                class={`flex items-center justify-between gap-3 rounded-md px-2 py-0.5 transition-colors ${
                  props.hoveredIndex === slice.idx ? "bg-[var(--bg-hover)]" : ""
                }`}
              >
                <div class="flex items-center gap-1.5 min-w-0">
                  <span class="h-2.5 w-2.5 shrink-0 rounded-xs" style={{ background: slice.color }} />
                  <span class="truncate text-[var(--text-secondary)]">{slice.label}</span>
                </div>
                <span class="shrink-0 font-mono text-[11px] text-[var(--text-primary)]">
                  {formatChartNumber(slice.value, props.spec.unit)}{" "}
                  <span class="text-[var(--text-faint)]">({slice.sharePct.toFixed(1)}%)</span>
                </span>
              </div>
            )}
          </For>
        </div>
      </div>
    </Show>
  );
}
