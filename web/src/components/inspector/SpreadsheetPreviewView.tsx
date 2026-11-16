import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { Check, Copy, Search, Table } from "lucide-solid";
import type { SpreadsheetSheetPreview } from "../../services/api-types";
import { t } from "../../i18n";
import { excelColLabel } from "./file-tree-helpers";

export interface SpreadsheetPreviewViewProps {
  sheets: SpreadsheetSheetPreview[];
  filePath: string;
  canEdit?: boolean;
  onSaveCell?: (sheetName: string, cellRef: string, value: string) => Promise<void>;
}

export function SpreadsheetPreviewView(props: SpreadsheetPreviewViewProps) {
  const [activeSheetIdx, setActiveSheetIdx] = createSignal(0);
  const [filterQuery, setFilterQuery] = createSignal("");
  const [copiedMd, setCopiedMd] = createSignal(false);
  const [editingCell, setEditingCell] = createSignal<{ rowNumber: number; colIdx: number } | null>(null);
  const [editingDraft, setEditingDraft] = createSignal("");
  const [savingCell, setSavingCell] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  onCleanup(() => {
    if (copyTimer) clearTimeout(copyTimer);
  });

  createEffect(() => {
    void props.filePath;
    setActiveSheetIdx(0);
    setFilterQuery("");
    setEditingCell(null);
  });

  const activeSheet = createMemo<SpreadsheetSheetPreview | undefined>(() => {
    const list = props.sheets;
    if (!list || list.length === 0) return undefined;
    const idx = Math.min(activeSheetIdx(), list.length - 1);
    return list[Math.max(0, idx)];
  });

  const columnCount = createMemo(() => {
    const sheet = activeSheet();
    if (!sheet || !sheet.rows.length) return 0;
    let max = 0;
    for (const row of sheet.rows) {
      if (row.length > max) max = row.length;
    }
    return Math.min(max, 64);
  });

  const columnIndices = createMemo(() => {
    const count = columnCount();
    const out: number[] = [];
    for (let i = 0; i < count; i++) out.push(i);
    return out;
  });

  const filteredRows = createMemo(() => {
    const sheet = activeSheet();
    if (!sheet) return [];
    const q = filterQuery().trim().toLowerCase();
    const rowNums = sheet.rowNumbers;
    const indexed = sheet.rows.map((cells, idx) => ({
      rowNumber: rowNums && rowNums[idx] > 0 ? rowNums[idx] : idx + 1,
      cells,
    }));
    if (!q) return indexed;
    return indexed.filter(
      (entry, idx) =>
        idx === 0 || entry.cells.some((c) => (c || "").toLowerCase().includes(q))
    );
  });

  const handleCopyMarkdown = async () => {
    const sheet = activeSheet();
    const cols = columnCount();
    if (!sheet || !sheet.rows.length || cols === 0) return;
    const lines: string[] = [];
    const rows = filteredRows();
    rows.forEach((entry, rIdx) => {
      const cells: string[] = [];
      for (let c = 0; c < cols; c++) {
        cells.push((entry.cells[c] || "").replace(/\|/g, "\\|"));
      }
      lines.push(`| ${cells.join(" | ")} |`);
      if (rIdx === 0) {
        lines.push(`| ${Array.from({ length: cols }, () => "---").join(" | ")} |`);
      }
    });
    try {
      await navigator.clipboard.writeText(lines.join("\n"));
      setCopiedMd(true);
      if (copyTimer) clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopiedMd(false), 1500);
    } catch {
      /* ignore clipboard error */
    }
  };

  const commitCellEdit = async (rowNumber: number, colIdx: number, originalVal: string) => {
    const active = editingCell();
    if (!active || active.rowNumber !== rowNumber || active.colIdx !== colIdx) return;
    const nextVal = editingDraft();
    setEditingCell(null);
    if (!props.canEdit || !props.onSaveCell || nextVal === originalVal) return;
    const sheet = activeSheet();
    if (!sheet) return;
    const ref = `${excelColLabel(colIdx)}${rowNumber}`;
    setSavingCell(true);
    try {
      await props.onSaveCell(sheet.name, ref, nextVal);
    } finally {
      setSavingCell(false);
    }
  };

  return (
    <div class="flex flex-col h-full min-h-0 bg-[var(--bg-panel)]">
      {/* Sheet Tabs + Filter Bar */}
      <div class="shrink-0 flex flex-wrap items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1.5">
        <div class="flex items-center gap-1 overflow-x-auto max-w-full">
          <For each={props.sheets}>
            {(sheet, idx) => {
              const isSelected = () => idx() === activeSheetIdx();
              const rowTotal = () => sheet.totalRows ?? sheet.rows.length;
              return (
                <button
                  type="button"
                  onClick={() => {
                    setActiveSheetIdx(idx());
                    setEditingCell(null);
                  }}
                  class={`hoxi-press inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-[11px] transition-colors cursor-pointer shrink-0 ${
                    isSelected()
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                      : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                  }`}
                >
                  <Table size={11} class="text-[var(--accent-primary)] shrink-0" />
                  <span class="truncate max-w-[160px]">{sheet.name}</span>
                  <span class="font-mono text-[10px] text-[var(--text-faint)]">
                    ({t("inspector.files.sheetRowsCount", { count: rowTotal() })})
                  </span>
                </button>
              );
            }}
          </For>
        </div>

        <div class="flex items-center gap-1.5 ml-auto">
          <Show when={props.canEdit}>
            <span class="text-[10.5px] font-mono text-[var(--text-faint)] px-1 hidden sm:inline">
              {savingCell()
                ? t("inspector.files.sheetSavingCell")
                : t("inspector.files.sheetEditHint")}
            </span>
          </Show>
          <div class="flex items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5">
            <Search size={11} class="text-[var(--text-muted)] shrink-0" />
            <input
              type="text"
              value={filterQuery()}
              onInput={(ev) => setFilterQuery(ev.currentTarget.value)}
              placeholder={t("inspector.files.sheetFilterPlaceholder")}
              class="w-36 bg-transparent text-[11px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
            />
          </div>

          <button
            type="button"
            onClick={handleCopyMarkdown}
            title={
              copiedMd()
                ? t("inspector.files.copiedTableMarkdown")
                : t("inspector.files.copyTableMarkdown")
            }
            class="hoxi-press inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <Show when={copiedMd()} fallback={<Copy size={11} />}>
              <Check size={11} class="text-[var(--ok-text)]" />
            </Show>
            <span>
              {copiedMd()
                ? t("inspector.files.copiedTableMarkdown")
                : t("inspector.files.copyTableMarkdown")}
            </span>
          </button>
        </div>
      </div>

      <Show when={activeSheet()?.truncated}>
        <div class="shrink-0 border-b border-[var(--border-subtle)] bg-amber-500/10 px-3 py-1 text-[11px] font-mono text-amber-400">
          {t("inspector.files.sheetTruncatedNotice", {
            shown: activeSheet()?.rows.length ?? 0,
            total: activeSheet()?.totalRows ?? activeSheet()?.rows.length ?? 0,
          })}
        </div>
      </Show>

      {/* Table Body */}
      <div class="flex-1 min-h-0 overflow-auto">
        <Show
          when={columnCount() > 0 && filteredRows().length > 0}
          fallback={
            <div class="py-12 text-center text-[12px] text-[var(--text-muted)]">
              {filterQuery().trim()
                ? t("inspector.files.sheetNoMatchingRows")
                : t("inspector.files.sheetEmpty")}
            </div>
          }
        >
          <table class="w-full border-collapse text-left text-[12px] selectable-text">
            <thead class="sticky top-0 z-10 bg-[var(--bg-inset)] text-[10px] font-mono text-[var(--text-muted)]">
              <tr>
                <th class="sticky left-0 z-20 w-10 border-b border-r border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-right select-none">
                  #
                </th>
                <For each={columnIndices()}>
                  {(colIdx) => (
                    <th class="border-b border-r border-[var(--border-subtle)] px-2.5 py-1 font-medium select-none">
                      {excelColLabel(colIdx)}
                    </th>
                  )}
                </For>
              </tr>
            </thead>
            <tbody>
              <For each={filteredRows()}>
                {(entry, idx) => {
                  const isHeaderRow = () => entry.rowNumber === 1 && idx() === 0;
                  return (
                    <tr
                      class={
                        isHeaderRow()
                          ? "bg-[var(--bg-inset)]/60 font-semibold text-[var(--text-primary)]"
                          : "hover:bg-[var(--bg-hover)]/50 text-[var(--text-secondary)]"
                      }
                    >
                      <td class="sticky left-0 z-10 border-b border-r border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-right font-mono text-[10px] text-[var(--text-faint)] select-none">
                        {entry.rowNumber}
                      </td>
                      <For each={columnIndices()}>
                        {(colIdx) => {
                          const cellValue = () => entry.cells[colIdx] ?? "";
                          const isEditingThis = () => {
                            const cur = editingCell();
                            return Boolean(cur && cur.rowNumber === entry.rowNumber && cur.colIdx === colIdx);
                          };
                          return (
                            <td
                              onDblClick={() => {
                                if (!props.canEdit || savingCell()) return;
                                setEditingDraft(cellValue());
                                setEditingCell({ rowNumber: entry.rowNumber, colIdx });
                              }}
                              class="max-w-[280px] truncate border-b border-r border-[var(--border-subtle)] px-2.5 py-1.5 font-mono text-[11.5px]"
                            >
                              <Show
                                when={isEditingThis()}
                                fallback={cellValue()}
                              >
                                <input
                                  type="text"
                                  value={editingDraft()}
                                  ref={(el) => queueMicrotask(() => el.focus())}
                                  onInput={(ev) => setEditingDraft(ev.currentTarget.value)}
                                  onBlur={() => void commitCellEdit(entry.rowNumber, colIdx, cellValue())}
                                  onKeyDown={(ev) => {
                                    if (ev.key === "Enter") {
                                      ev.preventDefault();
                                      void commitCellEdit(entry.rowNumber, colIdx, cellValue());
                                    } else if (ev.key === "Escape") {
                                      ev.preventDefault();
                                      setEditingCell(null);
                                    }
                                  }}
                                  class="w-full min-w-[80px] rounded border border-[var(--accent-border)] bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[11.5px] text-[var(--text-primary)] outline-none"
                                />
                              </Show>
                            </td>
                          );
                        }}
                      </For>
                    </tr>
                  );
                }}
              </For>
            </tbody>
          </table>
        </Show>
      </div>
    </div>
  );
}

export interface PdfPreviewFrameProps {
  base64Content: string;
  filePath: string;
}

export function PdfPreviewFrame(props: PdfPreviewFrameProps) {
  const [blobUrl, setBlobUrl] = createSignal("");

  createEffect(() => {
    const b64 = props.base64Content;
    if (!b64) {
      setBlobUrl("");
      return;
    }
    let revokedUrl = "";
    try {
      const bin = atob(b64);
      const bytes = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i++) {
        bytes[i] = bin.charCodeAt(i);
      }
      const blob = new Blob([bytes], { type: "application/pdf" });
      revokedUrl = URL.createObjectURL(blob);
      setBlobUrl(revokedUrl);
    } catch {
      setBlobUrl(`data:application/pdf;base64,${b64}`);
    }
    onCleanup(() => {
      if (revokedUrl) {
        URL.revokeObjectURL(revokedUrl);
      }
    });
  });

  return (
    <Show when={blobUrl()}>
      <iframe
        src={blobUrl()}
        title={props.filePath}
        class="w-full h-full min-h-[480px] border-0 bg-[var(--bg-panel)]"
      />
    </Show>
  );
}
