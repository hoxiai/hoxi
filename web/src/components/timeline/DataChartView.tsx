// web/src/components/timeline/DataChartView.tsx
// Interactive chart container supporting chart-type switching, clickable series
// legend, floating hover tooltip, lightbox zoom, and SVG/PNG download.
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import {
  BarChart3,
  Download,
  ImageDown,
  LineChart,
  Maximize2,
  PieChart,
  TrendingUp,
} from "lucide-solid";
import { t } from "../../i18n";
import { openImagePreview, workspace } from "../../store/workspace";
import {
  computeNiceTicks,
  formatChartNumber,
  seriesColor,
  type ChartKind,
  type ParsedChartSpec,
} from "./chart-spec";
import { downloadSvgAsPng, downloadSvgFile, svgToDataURL } from "./svg-sanitizer";
import {
  CartesianChartSvg,
  CHART_WIDTH,
  HorizontalBarChartSvg,
  PieDonutChartView,
  serializeRenderedChartSvg,
  type ChartHoverTip,
  type VisibleChartSeries,
} from "./data-chart-renderers";

export default function DataChartView(props: {
  spec: ParsedChartSpec;
  showExportActions?: boolean;
}) {
  let svgRef: SVGSVGElement | undefined;
  const [activeKind, setActiveKind] = createSignal<ChartKind>(props.spec.type);
  const [hiddenSeries, setHiddenSeries] = createSignal<Set<number>>(new Set());
  const [hoverTip, setHoverTip] = createSignal<ChartHoverTip | null>(null);
  const [hoveredIndex, setHoveredIndex] = createSignal<number | null>(null);

  createEffect(() => {
    setActiveKind(props.spec.type);
    setHiddenSeries(new Set<number>());
    setHoverTip(null);
    setHoveredIndex(null);
  });

  const visibleSeries = createMemo<VisibleChartSeries[]>(() =>
    props.spec.series
      .map((s, originalIndex) => ({
        ...s,
        originalIndex,
        color: seriesColor(originalIndex, s.color),
      }))
      .filter((s) => !hiddenSeries().has(s.originalIndex))
  );

  const toggleSeries = (idx: number) => {
    setHiddenSeries((prev) => {
      const next = new Set(prev);
      if (next.has(idx)) {
        next.delete(idx);
      } else if (props.spec.series.length - next.size > 1) {
        next.add(idx);
      }
      return next;
    });
  };

  const canPie = createMemo(() => {
    const first = visibleSeries()[0];
    return Boolean(first && first.values.some((v) => v !== null && v > 0));
  });

  const switchableKinds = createMemo<Array<{ kind: ChartKind; label: string }>>(() => {
    const list: Array<{ kind: ChartKind; label: string }> = [
      { kind: "bar", label: t("timeline.chartTypeBar") },
      { kind: "line", label: t("timeline.chartTypeLine") },
      { kind: "area", label: t("timeline.chartTypeArea") },
    ];
    if (props.spec.type === "horizontal-bar") {
      list.splice(1, 0, { kind: "horizontal-bar", label: t("timeline.chartTypeHorizontalBar") });
    }
    if (canPie() && props.spec.labels.length <= 16) {
      list.push({
        kind: props.spec.type === "donut" ? "donut" : "pie",
        label:
          props.spec.type === "donut"
            ? t("timeline.chartTypeDonut")
            : t("timeline.chartTypePie"),
      });
    }
    return list;
  });

  const cartesianRange = createMemo(() => {
    const list = visibleSeries();
    const stacked = Boolean(props.spec.stacked) && activeKind() === "bar";
    let minVal = 0;
    let maxVal = 0;
    if (stacked) {
      for (let i = 0; i < props.spec.labels.length; i++) {
        let posSum = 0;
        let negSum = 0;
        for (const s of list) {
          const v = s.values[i];
          if (v === null || v === undefined) continue;
          if (v >= 0) posSum += v;
          else negSum += v;
        }
        if (posSum > maxVal) maxVal = posSum;
        if (negSum < minVal) minVal = negSum;
      }
    } else {
      for (const s of list) {
        for (const v of s.values) {
          if (v === null || v === undefined) continue;
          if (v > maxVal) maxVal = v;
          if (v < minVal) minVal = v;
        }
      }
    }
    const ticks = computeNiceTicks(minVal, maxVal, 4);
    const niceMin = ticks[0] ?? 0;
    const niceMax = ticks[ticks.length - 1] ?? 1;
    return { ticks, min: niceMin, max: niceMax === niceMin ? niceMin + 1 : niceMax };
  });

  const showPointHover = (idx: number, xPx: number, yPx: number, svgH: number) => {
    setHoveredIndex(idx);
    const items = visibleSeries()
      .map((s) => {
        const val = s.values[idx];
        return val !== null && val !== undefined
          ? { name: s.name, value: val, color: s.color }
          : null;
      })
      .filter((x): x is { name: string; value: number; color: string } => x !== null);
    if (items.length === 0) {
      setHoverTip(null);
      return;
    }
    setHoverTip({
      label: props.spec.labels[idx] || `#${idx + 1}`,
      items,
      xPct: Math.min(82, Math.max(18, (xPx / CHART_WIDTH) * 100)),
      yPct: Math.min(80, Math.max(12, (yPx / svgH) * 100)),
    });
  };

  const exportStem = () => (props.spec.title || "chart").replace(/[^\w-]+/g, "-");

  const handleZoom = () => {
    const markup = serializeRenderedChartSvg(svgRef, workspace.theme === "dark");
    if (!markup) return;
    openImagePreview({
      src: svgToDataURL(markup),
      alt: props.spec.title || t("timeline.chartDiagramLabel"),
    });
  };

  const handleDownloadSvg = () => {
    const markup = serializeRenderedChartSvg(svgRef, workspace.theme === "dark");
    if (markup) downloadSvgFile(markup, `${exportStem()}.svg`);
  };

  const handleDownloadPng = () => {
    const markup = serializeRenderedChartSvg(svgRef, workspace.theme === "dark");
    if (!markup) return;
    void downloadSvgAsPng(
      markup,
      `${exportStem()}.png`,
      workspace.theme === "dark" ? "#141418" : "#ffffff"
    );
  };

  return (
    <div class="space-y-2.5 p-3.5 bg-[var(--bg-panel)]/70">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="min-w-0">
          <Show when={props.spec.title}>
            <div class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
              {props.spec.title}
            </div>
          </Show>
          <Show when={props.spec.subtitle}>
            <div class="text-[11px] text-[var(--text-muted)] truncate">
              {props.spec.subtitle}
            </div>
          </Show>
        </div>

        <div class="flex items-center gap-1.5 ml-auto shrink-0">
          <div class="inline-flex items-center rounded-md bg-[var(--bg-inset)] p-0.5">
            <For each={switchableKinds()}>
              {(opt) => {
                const selected = () => activeKind() === opt.kind;
                return (
                  <button
                    type="button"
                    onClick={() => {
                      setActiveKind(opt.kind);
                      setHoverTip(null);
                      setHoveredIndex(null);
                    }}
                    title={opt.label}
                    class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] transition-colors cursor-pointer ${
                      selected()
                        ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                        : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    <Show when={opt.kind === "bar" || opt.kind === "horizontal-bar"}>
                      <BarChart3 size={11} />
                    </Show>
                    <Show when={opt.kind === "line"}>
                      <LineChart size={11} />
                    </Show>
                    <Show when={opt.kind === "area"}>
                      <TrendingUp size={11} />
                    </Show>
                    <Show when={opt.kind === "pie" || opt.kind === "donut"}>
                      <PieChart size={11} />
                    </Show>
                    <span class="hidden sm:inline">{opt.label}</span>
                  </button>
                );
              }}
            </For>
          </div>

          <Show when={props.showExportActions !== false}>
            <button
              type="button"
              onClick={handleDownloadSvg}
              title={t("timeline.downloadSvg")}
              class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <Download size={12} />
            </button>
            <button
              type="button"
              onClick={handleDownloadPng}
              title={t("timeline.downloadPng")}
              class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <ImageDown size={12} />
            </button>
            <button
              type="button"
              onClick={handleZoom}
              title={t("timeline.previewImageTooltip")}
              class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <Maximize2 size={12} />
            </button>
          </Show>
        </div>
      </div>

      <div
        class="relative w-full select-none"
        onMouseLeave={() => {
          setHoverTip(null);
          setHoveredIndex(null);
        }}
      >
        <Show
          when={activeKind() === "pie" || activeKind() === "donut"}
          fallback={
            <Show
              when={activeKind() === "horizontal-bar"}
              fallback={
                <CartesianChartSvg
                  spec={props.spec}
                  kind={activeKind() as "bar" | "line" | "area"}
                  visibleSeries={visibleSeries()}
                  range={cartesianRange()}
                  hoveredIndex={hoveredIndex()}
                  onPointHover={showPointHover}
                  setSvgRef={(el) => {
                    svgRef = el;
                  }}
                />
              }
            >
              <HorizontalBarChartSvg
                spec={props.spec}
                visibleSeries={visibleSeries()}
                maxVal={cartesianRange().max}
                hoveredIndex={hoveredIndex()}
                onPointHover={showPointHover}
                setSvgRef={(el) => {
                  svgRef = el;
                }}
              />
            </Show>
          }
        >
          <PieDonutChartView
            spec={props.spec}
            kind={activeKind() as "pie" | "donut"}
            visibleSeries={visibleSeries()}
            hoveredIndex={hoveredIndex()}
            onHoverSlice={(idx, tip) => {
              setHoveredIndex(idx);
              setHoverTip(tip);
            }}
            setSvgRef={(el) => {
              svgRef = el;
            }}
          />
        </Show>

        <Show when={hoverTip()}>
          {(tip) => (
            <div
              class="pointer-events-none absolute z-20 min-w-[130px] max-w-[240px] -translate-x-1/2 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-elevated)]/95 px-2.5 py-1.5 text-[11px] shadow-lg backdrop-blur-xs"
              style={{
                left: `${tip().xPct}%`,
                top: `${Math.max(2, tip().yPct - 4)}%`,
              }}
            >
              <div class="mb-1 truncate font-medium text-[var(--text-primary)]">{tip().label}</div>
              <div class="space-y-0.5">
                <For each={tip().items}>
                  {(item) => (
                    <div class="flex items-center justify-between gap-3">
                      <span class="flex items-center gap-1.5 min-w-0 text-[var(--text-secondary)]">
                        <span
                          class="h-2 w-2 shrink-0 rounded-full"
                          style={{ background: item.color }}
                        />
                        <span class="truncate">{item.name}</span>
                      </span>
                      <span class="shrink-0 font-mono font-medium text-[var(--text-primary)]">
                        {formatChartNumber(item.value, props.spec.unit)}
                        {item.sharePct !== undefined ? ` (${item.sharePct.toFixed(1)}%)` : ""}
                      </span>
                    </div>
                  )}
                </For>
              </div>
            </div>
          )}
        </Show>
      </div>

      <Show
        when={
          activeKind() !== "pie" &&
          activeKind() !== "donut" &&
          props.spec.series.length > 1
        }
      >
        <div class="flex flex-wrap items-center justify-center gap-1.5 pt-0.5">
          <For each={props.spec.series}>
            {(s, idx) => {
              const hidden = () => hiddenSeries().has(idx());
              return (
                <button
                  type="button"
                  onClick={() => toggleSeries(idx())}
                  class={`inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-[11px] transition-opacity cursor-pointer ${
                    hidden()
                      ? "border-transparent bg-[var(--bg-inset)]/50 text-[var(--text-faint)] opacity-55 line-through"
                      : "border-[var(--border-subtle)] bg-[var(--bg-inset)]/80 text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <span
                    class="h-2 w-2 shrink-0 rounded-full"
                    style={{ background: seriesColor(idx(), s.color) }}
                  />
                  <span>{s.name}</span>
                </button>
              );
            }}
          </For>
        </div>
      </Show>
    </div>
  );
}
