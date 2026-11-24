// web/src/components/timeline/chart-spec.ts
// Pure parser and normalizer for declarative chart specs (```chart / ```hoxi-chart)
// and automatic numeric extraction from GFM Markdown tables.

export type ChartKind = "bar" | "horizontal-bar" | "line" | "area" | "pie" | "donut";

export interface ChartSeries {
  name: string;
  values: Array<number | null>;
  color?: string;
}

export interface ParsedChartSpec {
  type: ChartKind;
  title?: string;
  subtitle?: string;
  unit?: string;
  xLabel?: string;
  yLabel?: string;
  stacked?: boolean;
  labels: string[];
  series: ChartSeries[];
}

export const CHART_PALETTE: readonly string[] = [
  "#3b82f6", // blue
  "#10b981", // emerald
  "#f59e0b", // amber
  "#8b5cf6", // violet
  "#ef4444", // red
  "#06b6d4", // cyan
  "#ec4899", // pink
  "#84cc16", // lime
];

const MAX_CHART_POINTS = 120;
const MAX_CHART_SERIES = 12;

function isRecord(v: unknown): v is Record<string, unknown> {
  return Boolean(v) && typeof v === "object" && !Array.isArray(v);
}

function asNonEmptyString(v: unknown, maxLen = 160): string | undefined {
  if (typeof v !== "string") return undefined;
  const trimmed = v.trim();
  if (!trimmed) return undefined;
  return trimmed.length > maxLen ? `${trimmed.slice(0, maxLen - 1)}…` : trimmed;
}

function normalizeChartKind(raw: unknown): ChartKind {
  const s = typeof raw === "string" ? raw.trim().toLowerCase() : "";
  switch (s) {
    case "line":
    case "spline":
      return "line";
    case "area":
      return "area";
    case "pie":
      return "pie";
    case "donut":
    case "doughnut":
      return "donut";
    case "horizontal-bar":
    case "horizontalbar":
    case "hbar":
      return "horizontal-bar";
    case "bar":
    case "column":
    default:
      return "bar";
  }
}

function parseFiniteNumber(raw: unknown): number | null {
  if (typeof raw === "number") {
    return Number.isFinite(raw) ? raw : null;
  }
  if (typeof raw === "string") {
    const cleaned = raw.trim().replace(/,/g, "");
    if (!cleaned) return null;
    const num = Number(cleaned);
    return Number.isFinite(num) ? num : null;
  }
  return null;
}

function sanitizeColor(raw: unknown): string | undefined {
  if (typeof raw !== "string") return undefined;
  const trimmed = raw.trim();
  if (/^#(?:[0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/i.test(trimmed)) return trimmed;
  if (/^(?:rgb|hsl)a?\(\s*[\d.,\s%]+\)$/i.test(trimmed)) return trimmed;
  return undefined;
}

function normalizeSeriesList(
  rawSeries: unknown[],
  labelCount: number
): ChartSeries[] {
  const out: ChartSeries[] = [];
  for (let i = 0; i < Math.min(rawSeries.length, MAX_CHART_SERIES); i++) {
    const entry = rawSeries[i];
    if (!isRecord(entry)) continue;
    const rawVals = Array.isArray(entry.values)
      ? entry.values
      : Array.isArray(entry.data)
        ? entry.data
        : null;
    if (!rawVals || rawVals.length === 0) continue;
    const count = Math.min(MAX_CHART_POINTS, Math.max(labelCount, rawVals.length));
    const values: Array<number | null> = [];
    let hasNumber = false;
    for (let j = 0; j < count; j++) {
      const n = parseFiniteNumber(rawVals[j]);
      if (n !== null) hasNumber = true;
      values.push(n);
    }
    if (!hasNumber) continue;
    const name =
      asNonEmptyString(entry.name) ??
      asNonEmptyString(entry.label) ??
      `Series ${out.length + 1}`;
    const rawColor =
      sanitizeColor(entry.color) ??
      sanitizeColor(entry.borderColor) ??
      (typeof entry.backgroundColor === "string"
        ? sanitizeColor(entry.backgroundColor)
        : undefined);
    out.push({ name, values, ...(rawColor ? { color: rawColor } : {}) });
  }
  return out;
}

function parseFromObjectRecords(
  root: Record<string, unknown>,
  records: unknown[]
): ParsedChartSpec | null {
  const rows = records.filter(isRecord).slice(0, MAX_CHART_POINTS);
  if (rows.length === 0) return null;

  const allKeys = Array.from(
    new Set(rows.flatMap((row) => Object.keys(row)))
  );
  if (allKeys.length < 2) return null;

  const explicitX = asNonEmptyString(root.xKey) ?? asNonEmptyString(root.labelKey);
  let xKey =
    (explicitX && allKeys.includes(explicitX) ? explicitX : undefined) ??
    allKeys.find((k) =>
      /^(name|label|category|date|time|month|day|year|x|item|stage|step)$/i.test(k)
    ) ??
    allKeys.find((k) => rows.some((r) => typeof r[k] === "string" && parseFiniteNumber(r[k]) === null)) ??
    allKeys[0];

  const explicitY = Array.isArray(root.yKeys)
    ? root.yKeys.filter((k): k is string => typeof k === "string" && allKeys.includes(k))
    : typeof root.yKey === "string" && allKeys.includes(root.yKey)
      ? [root.yKey]
      : null;

  const yKeys = (
    explicitY && explicitY.length > 0
      ? explicitY
      : allKeys.filter((k) => k !== xKey && rows.some((r) => parseFiniteNumber(r[k]) !== null))
  ).slice(0, MAX_CHART_SERIES);

  if (yKeys.length === 0) return null;

  const labels = rows.map((r, idx) => {
    const raw = r[xKey];
    const s = raw !== undefined && raw !== null ? String(raw).trim() : "";
    return s || `#${idx + 1}`;
  });
  const series: ChartSeries[] = yKeys.map((key) => ({
    name: key,
    values: rows.map((r) => parseFiniteNumber(r[key])),
  }));

  return {
    type: normalizeChartKind(root.type),
    title: asNonEmptyString(root.title),
    subtitle: asNonEmptyString(root.subtitle),
    unit: asNonEmptyString(root.unit, 24),
    xLabel: asNonEmptyString(root.xLabel, 64),
    yLabel: asNonEmptyString(root.yLabel, 64),
    stacked: Boolean(root.stacked),
    labels,
    series,
  };
}

/**
 * Parses a JSON chart specification from a ```chart / ```hoxi-chart / ```chartjs block.
 * Supports:
 * 1. Canonical Hoxi schema: `{ type, title?, labels/categories, series: [{ name, values }] }`
 * 2. Chart.js-like schema: `{ type, data: { labels, datasets: [{ label, data }] }, options? }`
 * 3. Single-series shorthand: `{ type, labels, values }` or `{ type, data: [{ name, value }] }`
 */
export function parseChartSpec(raw: string): ParsedChartSpec | null {
  const trimmed = (raw || "").trim();
  if (!trimmed) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return null;
  }

  // Bare array of objects: `[{ name: "A", value: 10 }, ...]`
  if (Array.isArray(parsed)) {
    return parseFromObjectRecords({}, parsed);
  }
  if (!isRecord(parsed)) return null;

  // Nested Chart.js `{ data: { labels, datasets } }`
  const chartJsData = isRecord(parsed.data) ? parsed.data : null;
  if (chartJsData) {
    const rawLabels = Array.isArray(chartJsData.labels) ? chartJsData.labels : [];
    const rawDatasets = Array.isArray(chartJsData.datasets)
      ? chartJsData.datasets
      : Array.isArray(chartJsData.series)
        ? chartJsData.series
        : [];
    const series = normalizeSeriesList(rawDatasets, rawLabels.length);
    if (series.length > 0) {
      const pointCount = Math.max(rawLabels.length, ...series.map((s) => s.values.length));
      const labels = Array.from({ length: Math.min(pointCount, MAX_CHART_POINTS) }, (_, i) => {
        const v = rawLabels[i];
        return v !== undefined && v !== null && String(v).trim() ? String(v).trim() : `#${i + 1}`;
      });
      const options = isRecord(parsed.options) ? parsed.options : null;
      const plugins = options && isRecord(options.plugins) ? options.plugins : null;
      const pluginTitle = plugins && isRecord(plugins.title) ? plugins.title : null;
      return {
        type: normalizeChartKind(parsed.type),
        title: asNonEmptyString(parsed.title) ?? asNonEmptyString(pluginTitle?.text),
        subtitle: asNonEmptyString(parsed.subtitle),
        unit: asNonEmptyString(parsed.unit, 24),
        xLabel: asNonEmptyString(parsed.xLabel, 64),
        yLabel: asNonEmptyString(parsed.yLabel, 64),
        stacked: Boolean(parsed.stacked),
        labels,
        series,
      };
    }
  }

  // Flat records array under `data` or `rows`
  const recordArray = Array.isArray(parsed.data)
    ? parsed.data
    : Array.isArray(parsed.rows)
      ? parsed.rows
      : null;
  if (recordArray && recordArray.length > 0 && isRecord(recordArray[0])) {
    return parseFromObjectRecords(parsed, recordArray);
  }

  // Canonical Hoxi schema `{ labels/categories, series/datasets }` or shorthand `{ labels, values }`
  const rawLabels = Array.isArray(parsed.labels)
    ? parsed.labels
    : Array.isArray(parsed.categories)
      ? parsed.categories
      : Array.isArray(parsed.x)
        ? parsed.x
        : [];

  let rawSeries: unknown[] = [];
  if (Array.isArray(parsed.series)) {
    rawSeries = parsed.series;
  } else if (Array.isArray(parsed.datasets)) {
    rawSeries = parsed.datasets;
  } else if (Array.isArray(parsed.values)) {
    rawSeries = [{ name: asNonEmptyString(parsed.seriesName) ?? "Value", values: parsed.values }];
  } else if (Array.isArray(parsed.data) && parsed.data.some((v) => parseFiniteNumber(v) !== null)) {
    rawSeries = [{ name: asNonEmptyString(parsed.seriesName) ?? "Value", values: parsed.data }];
  }

  const series = normalizeSeriesList(rawSeries, rawLabels.length);
  if (series.length === 0) return null;

  const pointCount = Math.min(
    MAX_CHART_POINTS,
    Math.max(rawLabels.length, ...series.map((s) => s.values.length))
  );
  if (pointCount === 0) return null;

  const labels = Array.from({ length: pointCount }, (_, i) => {
    const v = rawLabels[i];
    return v !== undefined && v !== null && String(v).trim() ? String(v).trim() : `#${i + 1}`;
  });

  return {
    type: normalizeChartKind(parsed.type),
    title: asNonEmptyString(parsed.title),
    subtitle: asNonEmptyString(parsed.subtitle),
    unit: asNonEmptyString(parsed.unit, 24),
    xLabel: asNonEmptyString(parsed.xLabel, 64),
    yLabel: asNonEmptyString(parsed.yLabel, 64),
    stacked: Boolean(parsed.stacked),
    labels,
    series,
  };
}

function stripMarkdownDecorators(raw: string): string {
  return (raw || "")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/__([^_]+)__/g, "$1")
    .replace(/~~([^~]+)~~/g, "$1")
    .replace(/\*([^*]+)\*/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .trim();
}

export interface ParsedNumericCell {
  value: number;
  unit: string;
  comparableValue: number;
  family?: "time" | "bytes" | "scale";
  unitScale?: number;
}

const SCALE_SUFFIX_MULTIPLIERS: Record<string, number> = {
  k: 1e3,
  m: 1e6,
  b: 1e9,
  "\u4e07": 1e4,
  "\u4ebf": 1e8,
};

const BYTE_SUFFIX_MULTIPLIERS: Record<string, { scale: number; label: string }> = {
  kb: { scale: 1024, label: "KB" },
  mb: { scale: 1024 ** 2, label: "MB" },
  gb: { scale: 1024 ** 3, label: "GB" },
  tb: { scale: 1024 ** 4, label: "TB" },
};

const TIME_SUFFIX_MULTIPLIERS: Record<string, { scale: number; label: string }> = {
  ms: { scale: 1, label: "ms" },
  s: { scale: 1000, label: "s" },
};

export function parseNumericCell(raw: string): ParsedNumericCell | null {
  const text = stripMarkdownDecorators(raw);
  if (!text || text === "-" || text === "—" || text === "N/A" || text === "n/a") return null;

  // Match optional leading currency sign, signed number with optional commas, and optional simple suffix unit.
  // Uses Unicode escapes for CJK currency/count units so check:i18n stays strict.
  const m = text.match(
    /^([$€£¥\uffe5])?\s*([+-]?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?)\s*(ms|tb|gb|mb|kb|%|s|k|m|b|x|×|\u6b21|\u4e2a|\u5143|\u4e07|\u4ebf|\u4eba|\u6761|\u884c)?$/i
  );
  if (!m) return null;
  const num = Number(m[2].replace(/,/g, ""));
  if (!Number.isFinite(num)) return null;
  const prefix = m[1] || "";
  const suffix = m[3] || "";
  const lowerSuffix = suffix.toLowerCase();

  // Currency + magnitude suffix (e.g. `$1.2M`, `$500K`, `¥1.5万`): fold scale into value immediately.
  if (prefix) {
    const scale = SCALE_SUFFIX_MULTIPLIERS[lowerSuffix] ?? 1;
    const folded = num * scale;
    return {
      value: folded,
      unit: prefix,
      comparableValue: folded,
      family: "scale",
      unitScale: 1,
    };
  }

  if (lowerSuffix in TIME_SUFFIX_MULTIPLIERS) {
    const meta = TIME_SUFFIX_MULTIPLIERS[lowerSuffix];
    return {
      value: num,
      unit: meta.label,
      comparableValue: num * meta.scale,
      family: "time",
      unitScale: meta.scale,
    };
  }

  if (lowerSuffix in BYTE_SUFFIX_MULTIPLIERS) {
    const meta = BYTE_SUFFIX_MULTIPLIERS[lowerSuffix];
    return {
      value: num,
      unit: meta.label,
      comparableValue: num * meta.scale,
      family: "bytes",
      unitScale: meta.scale,
    };
  }

  if (lowerSuffix in SCALE_SUFFIX_MULTIPLIERS) {
    const scale = SCALE_SUFFIX_MULTIPLIERS[lowerSuffix];
    return {
      value: num,
      unit: suffix,
      comparableValue: num * scale,
      family: "scale",
      unitScale: scale,
    };
  }

  return {
    value: num,
    unit: suffix,
    comparableValue: num,
    ...(suffix === "" ? { family: "scale" as const, unitScale: 1 } : {}),
  };
}

/**
 * Extracts a visualizable chart specification from a Markdown table if it has
 * at least 2 data rows and at least 1 predominantly numeric column.
 */
export function extractChartFromMarkdownTable(
  headers: readonly string[],
  rows: ReadonlyArray<readonly string[]>
): ParsedChartSpec | null {
  if (headers.length < 2 || rows.length < 2) return null;
  const boundedRows = rows.slice(0, MAX_CHART_POINTS);

  // Pick the first non-numeric column as category label, falling back to column 0.
  const numericRatio = headers.map((_, colIdx) => {
    let valid = 0;
    for (const row of boundedRows) {
      if (parseNumericCell(row[colIdx] ?? "") !== null) valid++;
    }
    return valid / boundedRows.length;
  });

  let labelCol = numericRatio.findIndex((r) => r < 0.5);
  if (labelCol < 0) labelCol = 0;

  const valueCols: Array<{ colIdx: number; name: string; values: Array<number | null>; unit: string }> = [];
  for (let c = 0; c < headers.length; c++) {
    if (c === labelCol) continue;
    if (numericRatio[c] < 0.6) continue;
    const parsedCells = boundedRows.map((row) => parseNumericCell(row[c] ?? ""));
    const nonNull = parsedCells.filter((p): p is ParsedNumericCell => p !== null);
    if (nonNull.length < 2) continue;

    const unitSet = new Set(nonNull.map((p) => p.unit));
    const families = new Set(nonNull.map((p) => p.family).filter(Boolean));
    let unit = unitSet.size === 1 ? nonNull[0].unit : "";
    let values: Array<number | null>;

    // Normalize mixed units within the same dimensional family (e.g. `100ms` + `2s`, `800KB` + `1.5MB`, `500` + `1.5K`).
    if (unitSet.size > 1 && families.size === 1 && nonNull.every((p) => Boolean(p.family))) {
      const family = nonNull[0].family!;
      if (family === "scale") {
        unit = "";
        values = parsedCells.map((p) => (p ? p.comparableValue : null));
      } else {
        let minScale = Infinity;
        let minUnit = "";
        for (const p of nonNull) {
          const s = p.unitScale ?? 1;
          if (s < minScale) {
            minScale = s;
            minUnit = p.unit;
          }
        }
        const divisor = Number.isFinite(minScale) && minScale > 0 ? minScale : 1;
        unit = minUnit;
        values = parsedCells.map((p) => (p ? Number((p.comparableValue / divisor).toPrecision(10)) : null));
      }
    } else {
      values = parsedCells.map((p) => (p ? p.value : null));
    }

    valueCols.push({
      colIdx: c,
      name: stripMarkdownDecorators(headers[c] || "") || `Col ${c + 1}`,
      values,
      unit,
    });
    if (valueCols.length >= MAX_CHART_SERIES) break;
  }

  if (valueCols.length === 0) return null;

  const labels = boundedRows.map((row, idx) => {
    const clean = stripMarkdownDecorators(row[labelCol] ?? "");
    return clean || `#${idx + 1}`;
  });
  const sharedUnits = new Set(valueCols.map((c) => c.unit).filter(Boolean));
  const unit = sharedUnits.size === 1 ? Array.from(sharedUnits)[0] : undefined;

  // Heuristic: time-like labels default to line chart; otherwise bar chart.
  const looksTemporal =
    labels.length >= 3 &&
    labels.every((l) =>
      /^(?:\d{4}[-/\u5e74]\d{1,2}(?:[-/\u6708]\d{1,2}\u65e5?)?|Q[1-4]|W\d{1,2}|(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\b|\d{1,2}\u6708|\d{1,2}:\d{2}|v?\d+\.\d+)/i.test(
        l
      )
    );

  return {
    type: looksTemporal ? "line" : "bar",
    unit,
    labels,
    series: valueCols.map((c) => ({ name: c.name, values: c.values })),
  };
}

export function formatChartNumber(value: number, unit?: string): string {
  if (!Number.isFinite(value)) return "—";
  const abs = Math.abs(value);
  let formatted: string;
  if (abs >= 1_000_000_000) {
    formatted = `${(value / 1_000_000_000).toFixed(abs >= 10_000_000_000 ? 1 : 2).replace(/\.0+$/, "")}B`;
  } else if (abs >= 1_000_000) {
    formatted = `${(value / 1_000_000).toFixed(abs >= 10_000_000 ? 1 : 2).replace(/\.0+$/, "")}M`;
  } else if (abs >= 10_000) {
    formatted = `${(value / 1_000).toFixed(1).replace(/\.0$/, "")}K`;
  } else if (Number.isInteger(value)) {
    formatted = value.toLocaleString();
  } else {
    formatted = Number(value.toFixed(2)).toLocaleString();
  }
  if (!unit) return formatted;
  if (/^[$€£¥\uffe5]$/.test(unit)) {
    return value < 0 ? `-${unit}${formatted.replace(/^-/, "")}` : `${unit}${formatted}`;
  }
  return `${formatted}${unit}`;
}

export function computeNiceTicks(minVal: number, maxVal: number, targetCount = 4): number[] {
  const lo = Math.min(0, minVal);
  const hi = Math.max(0, maxVal);
  if (lo === hi) return [0, hi === 0 ? 1 : hi * 2];
  const rawStep = (hi - lo) / Math.max(1, targetCount);
  const mag = Math.pow(10, Math.floor(Math.log10(rawStep)));
  const norm = rawStep / mag;
  const niceNorm = norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 5 ? 5 : 10;
  const step = niceNorm * mag;
  const niceMin = Math.floor(lo / step) * step;
  const niceMax = Math.ceil(hi / step) * step;
  const ticks: number[] = [];
  for (let v = niceMin; v <= niceMax + step * 0.001; v += step) {
    ticks.push(Math.abs(v) < step * 1e-9 ? 0 : Number(v.toPrecision(10)));
    if (ticks.length > 10) break;
  }
  return ticks;
}

export function seriesColor(index: number, explicit?: string): string {
  return explicit || CHART_PALETTE[index % CHART_PALETTE.length];
}

export function markdownTableToCsv(
  headers: readonly string[],
  rows: ReadonlyArray<readonly string[]>
): string {
  const escapeCsv = (cell: string): string => {
    const clean = stripMarkdownDecorators(cell);
    if (/[",\r\n]/.test(clean)) {
      return `"${clean.replace(/"/g, '""')}"`;
    }
    return clean;
  };
  const lines = [headers.map(escapeCsv).join(",")];
  for (const row of rows) {
    lines.push(headers.map((_, idx) => escapeCsv(row[idx] ?? "")).join(","));
  }
  return lines.join("\n");
}

export function markdownTableToMarkdown(
  headers: readonly string[],
  rows: ReadonlyArray<readonly string[]>
): string {
  const esc = (cell: string) => (cell || "").replace(/\|/g, "\\|").replace(/\r?\n/g, " ");
  const lines = [
    `| ${headers.map(esc).join(" | ")} |`,
    `| ${headers.map(() => "---").join(" | ")} |`,
  ];
  for (const row of rows) {
    lines.push(`| ${headers.map((_, idx) => esc(row[idx] ?? "")).join(" | ")} |`);
  }
  return lines.join("\n");
}
