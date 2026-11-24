// web/src/components/timeline/MarkdownTableCard.tsx
// Enhanced interactive GFM table card: click-to-sort columns, optional quick
// filter, one-click Copy Markdown / CSV, and one-click inline Chart visualization
// whenever numeric data columns are detected.
import { createMemo, createSignal, Index, onCleanup, Show } from "solid-js";
import {
  ArrowDown,
  ArrowUp,
  BarChart3,
  Check,
  Copy,
  FileSpreadsheet,
  Search,
  Table,
  X,
} from "lucide-solid";
import { t } from "../../i18n";
import type { TableAlign } from "./markdown-parser";
import MarkdownInline from "./MarkdownInline";
import { createRevealBirths, revealBlock } from "./stream-reveal";
import {
  extractChartFromMarkdownTable,
  markdownTableToCsv,
  markdownTableToMarkdown,
  parseNumericCell,
} from "./chart-spec";
import DataChartView from "./DataChartView";

function alignClass(align?: TableAlign): string {
  if (align === "center") return "text-center";
  if (align === "right") return "text-right";
  return "text-left";
}

export default function MarkdownTableCard(props: {
  headers: string[];
  aligns: TableAlign[];
  rows: string[][];
  tableBirth: number;
  ref?: (el: HTMLDivElement) => void;
}) {
  const rowBirth = createRevealBirths();
  const [sortCol, setSortCol] = createSignal<number | null>(null);
  const [sortDir, setSortDir] = createSignal<"asc" | "desc">("asc");
  const [filterOpen, setFilterOpen] = createSignal(false);
  const [filterQuery, setFilterQuery] = createSignal("");
  const [mode, setMode] = createSignal<"table" | "chart">("table");
  const [copiedKind, setCopiedKind] = createSignal<"md" | "csv" | null>(null);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  onCleanup(() => {
    if (copyTimer) clearTimeout(copyTimer);
  });

  const isFilteredOrSorted = () => sortCol() !== null || filterQuery().trim().length > 0;

  const displayRows = createMemo(() => {
    const q = filterQuery().trim().toLowerCase();
    const sc = sortCol();
    const dir = sortDir();
    if (!q && sc === null) return props.rows;

    let list = q
      ? props.rows.filter((row) => row.some((cell) => (cell || "").toLowerCase().includes(q)))
      : props.rows.slice();

    if (sc !== null) {
      list = list.slice().sort((a, b) => {
        const rawA = a[sc] ?? "";
        const rawB = b[sc] ?? "";
        const numA = parseNumericCell(rawA);
        const numB = parseNumericCell(rawB);
        let cmp: number;
        if (numA !== null && numB !== null) {
          cmp = numA.comparableValue - numB.comparableValue;
        } else {
          cmp = rawA.localeCompare(rawB, undefined, { numeric: true, sensitivity: "base" });
        }
        return dir === "asc" ? cmp : -cmp;
      });
    }
    return list;
  });

  const chartSpec = createMemo(() => extractChartFromMarkdownTable(props.headers, displayRows()));

  const toggleSort = (colIdx: number) => {
    if (sortCol() !== colIdx) {
      setSortCol(colIdx);
      setSortDir("asc");
    } else if (sortDir() === "asc") {
      setSortDir("desc");
    } else {
      setSortCol(null);
    }
  };

  const handleCopy = async (kind: "md" | "csv") => {
    const payload =
      kind === "csv"
        ? markdownTableToCsv(props.headers, displayRows())
        : markdownTableToMarkdown(props.headers, displayRows());
    try {
      await navigator.clipboard.writeText(payload);
      setCopiedKind(kind);
      if (copyTimer) clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopiedKind(null), 1500);
    } catch {
      /* ignore */
    }
  };

  const showFilterButton = () => props.rows.length >= 5 || filterOpen();

  return (
    <div
      ref={props.ref}
      data-hoxi-table-card="true"
      class="group/table relative my-1.5 overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)]/40"
    >
      {/* Compact Toolbar: always visible when chart/filter active, subtle hover otherwise */}
      <div
        class={`flex flex-wrap items-center justify-between gap-1.5 border-b border-[var(--border-subtle)]/70 bg-[var(--bg-elevated)]/55 px-2.5 py-1 text-[11px] transition-opacity ${
          mode() === "chart" || filterOpen() || isFilteredOrSorted()
            ? "opacity-100"
            : "opacity-80 group-hover/table:opacity-100"
        }`}
      >
        <div class="flex items-center gap-1.5 min-w-0">
          <Show when={filterOpen()}>
            <div class="flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-1.5 py-0.5">
              <Search size={10} class="text-[var(--text-muted)] shrink-0" />
              <input
                type="text"
                value={filterQuery()}
                onInput={(ev) => setFilterQuery(ev.currentTarget.value)}
                placeholder={t("timeline.tableFilterPlaceholder")}
                class="w-32 bg-transparent text-[11px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
              />
              <Show when={filterQuery()}>
                <button
                  type="button"
                  onClick={() => setFilterQuery("")}
                  class="text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
                >
                  <X size={10} />
                </button>
              </Show>
            </div>
          </Show>
          <Show when={filterQuery().trim().length > 0}>
            <span class="font-mono text-[10px] text-[var(--text-muted)]">
              {t("timeline.tableRowCount", {
                shown: displayRows().length,
                total: props.rows.length,
              })}
            </span>
          </Show>
        </div>

        <div class="flex items-center gap-1 ml-auto shrink-0">
          <Show when={chartSpec()}>
            <div class="inline-flex items-center rounded-md bg-[var(--bg-inset)] p-0.5">
              <button
                type="button"
                onClick={() => setMode("table")}
                class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] transition-colors cursor-pointer ${
                  mode() === "table"
                    ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
              >
                <Table size={10.5} />
                <span>{t("timeline.tableBackToTable")}</span>
              </button>
              <button
                type="button"
                onClick={() => setMode("chart")}
                class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] transition-colors cursor-pointer ${
                  mode() === "chart"
                    ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
              >
                <BarChart3 size={10.5} />
                <span>{t("timeline.tableVisualizeChart")}</span>
              </button>
            </div>
          </Show>

          <Show when={showFilterButton() && mode() === "table"}>
            <button
              type="button"
              onClick={() => {
                if (filterOpen()) setFilterQuery("");
                setFilterOpen((v) => !v);
              }}
              title={t("timeline.tableFilterPlaceholder")}
              class={`inline-flex items-center justify-center rounded-md p-1 transition-colors cursor-pointer ${
                filterOpen()
                  ? "bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
              }`}
            >
              <Search size={11.5} />
            </button>
          </Show>

          <button
            type="button"
            onClick={() => void handleCopy("csv")}
            title={copiedKind() === "csv" ? t("timeline.tableCopiedCsv") : t("timeline.tableCopyCsv")}
            class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Show when={copiedKind() === "csv"} fallback={<FileSpreadsheet size={11} />}>
              <Check size={11} class="text-[var(--ok-text)]" />
            </Show>
            <span>CSV</span>
          </button>

          <button
            type="button"
            onClick={() => void handleCopy("md")}
            title={copiedKind() === "md" ? t("common.copied") : t("timeline.tableCopyMarkdown")}
            class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Show when={copiedKind() === "md"} fallback={<Copy size={11.5} />}>
              <Check size={11.5} class="text-[var(--ok-text)]" />
            </Show>
          </button>
        </div>
      </div>

      <Show
        when={mode() === "chart" && chartSpec()}
        fallback={
          <div class="overflow-x-auto">
            <table class="w-full border-collapse text-left text-[0.92em]">
              <thead>
                <tr class="border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/60">
                  <Index each={props.headers}>
                    {(h, colIdx) => {
                      const isSorted = () => sortCol() === colIdx;
                      return (
                        <th
                          onClick={() => toggleSort(colIdx)}
                          title={t("timeline.tableSortTooltip")}
                          class={`px-3 py-2 font-semibold text-[var(--text-primary)] cursor-pointer select-none hover:bg-[var(--bg-hover)]/60 transition-colors ${alignClass(
                            props.aligns[colIdx]
                          )}`}
                        >
                          <span class="inline-flex items-center gap-1">
                            <MarkdownInline text={h()} />
                            <Show when={isSorted()}>
                              <Show
                                when={sortDir() === "asc"}
                                fallback={<ArrowDown size={11} class="shrink-0 text-[var(--accent-primary)]" />}
                              >
                                <ArrowUp size={11} class="shrink-0 text-[var(--accent-primary)]" />
                              </Show>
                            </Show>
                          </span>
                        </th>
                      );
                    }}
                  </Index>
                </tr>
              </thead>
              <tbody>
                <Index each={displayRows()}>
                  {(row, rowIdx) => (
                    <tr
                      ref={(el) => {
                        if (isFilteredOrSorted()) return;
                        const at = rowBirth(rowIdx);
                        if (at - props.tableBirth >= 8) {
                          revealBlock(el, at, { startOpacity: 0, translateY: 2 });
                        }
                      }}
                      class="border-b last:border-b-0 border-[var(--border-subtle)]/80 hover:bg-[var(--bg-hover)]/35 transition-colors"
                    >
                      <Index each={row()}>
                        {(cell, colIdx) => (
                          <td
                            class={`px-3 py-2 align-top text-[var(--text-secondary)] ${alignClass(
                              props.aligns[colIdx]
                            )}`}
                          >
                            <MarkdownInline text={cell()} />
                          </td>
                        )}
                      </Index>
                    </tr>
                  )}
                </Index>
              </tbody>
            </table>
          </div>
        }
      >
        {(spec) => <DataChartView spec={spec()} />}
      </Show>
    </div>
  );
}
