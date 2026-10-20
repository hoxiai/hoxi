// web/src/components/timeline/composer-subcomponents.tsx
import { createEffect, For, Show } from "solid-js";
import { AlertCircle, ClipboardPaste, FileText, LoaderCircle, Slash, X } from "lucide-solid";
import { FileIcon } from "../icons";
import { t } from "../../i18n";
import {
  classifyAttachmentKind,
  type AutocompleteItem,
  type CollapsedPasteItem,
  type ImageItem,
} from "./composer-hooks";

export function ComposerAutocompletePopover(props: {
  triggerMode: "file" | "command" | null;
  suggestions: AutocompleteItem[];
  selectedIndex: number;
  onSelectIndex: (idx: number) => void;
  onApply: (item: AutocompleteItem) => void;
}) {
  let listRef: HTMLDivElement | undefined;

  createEffect(() => {
    const idx = props.selectedIndex;
    if (!props.triggerMode || props.suggestions.length === 0) return;
    queueMicrotask(() => {
      const btn = listRef?.querySelector<HTMLButtonElement>(
        `[data-ac-index="${idx}"]`
      );
      btn?.scrollIntoView({ block: "nearest" });
    });
  });

  return (
    <Show when={props.triggerMode && props.suggestions.length > 0}>
      <div class="absolute bottom-full left-0 mb-2 w-full max-w-[32rem] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-xl z-40">
        <div class="px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
          {props.triggerMode === "command"
            ? t("timeline.slashCommandsHeader")
            : t("timeline.mentionFileOrContextHeader")}
        </div>
        <div ref={listRef} class="max-h-56 overflow-y-auto space-y-0.5">
          <For each={props.suggestions}>
            {(item, idx) => (
              <button
                type="button"
                data-ac-index={idx()}
                class={`w-full flex items-center justify-between gap-3 rounded-lg px-2.5 py-1.5 text-left text-[12.5px] transition-colors cursor-pointer ${
                  idx() === props.selectedIndex
                    ? "bg-[var(--accent-surface)] text-[var(--accent-text)]"
                    : "text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                }`}
                onMouseEnter={() => props.onSelectIndex(idx())}
                onClick={() => props.onApply(item)}
              >
                <span class="inline-flex items-center gap-2 min-w-0 font-mono truncate">
                  {item.type === "context" ? (
                    <Slash size={12} class="text-[var(--accent-primary)] shrink-0" />
                  ) : item.type === "file" ? (
                    <FileIcon
                      node={{
                        path: item.label,
                        type: item.label.endsWith("/") ? "directory" : "file",
                      }}
                      class="w-3.5 h-3.5 shrink-0"
                    />
                  ) : null}
                  <span class="truncate">{item.label}</span>
                </span>
                <Show when={item.detail}>
                  <span class="text-[11px] text-[var(--text-muted)] truncate max-w-[180px]">
                    {item.detail}
                  </span>
                </Show>
              </button>
            )}
          </For>
        </div>
      </div>
    </Show>
  );
}

export function ComposerAttachmentChips(props: {
  images: ImageItem[];
  pastes: CollapsedPasteItem[];
  pendingCount?: number;
  notice?: string | null;
  onDismissNotice?: () => void;
  onRemoveImage: (id: string) => void;
  onRemovePaste: (id: string) => void;
}) {
  const isImageItem = (img: ImageItem) => {
    const k = img.kind ?? classifyAttachmentKind(img.filename, img.mime);
    return k === "image" || (img.mime || "").toLowerCase().startsWith("image/");
  };

  const extBadge = (filename: string, mime: string) => {
    if ((mime || "").toLowerCase() === "application/pdf" || /\.pdf$/i.test(filename)) {
      return "PDF";
    }
    const ext = (filename.split(".").pop() || "TXT").toUpperCase();
    return ext.slice(0, 4);
  };

  const extBadgeColor = (filename: string, mime: string, kind?: string) => {
    if (
      kind === "spreadsheet" ||
      /\.(xlsx|xls|ods|csv|tsv)$/i.test(filename) ||
      (mime || "").includes("spreadsheet") ||
      (mime || "").includes("excel") ||
      (mime || "").includes("csv")
    ) {
      return "text-emerald-500";
    }
    if (
      kind === "pdf" ||
      (mime || "").toLowerCase() === "application/pdf" ||
      /\.pdf$/i.test(filename)
    ) {
      return "text-rose-500";
    }
    return "text-[var(--accent-primary)]";
  };

  return (
    <>
      <Show when={props.notice}>
        <div class="mb-2 flex items-center justify-between gap-2 rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2.5 py-1.5 text-[11.5px] text-[var(--danger-text)]">
          <span class="inline-flex items-center gap-1.5 min-w-0">
            <AlertCircle size={13} class="shrink-0" />
            <span class="truncate">{props.notice}</span>
          </span>
          <Show when={props.onDismissNotice}>
            <button
              type="button"
              onClick={() => props.onDismissNotice?.()}
              class="shrink-0 opacity-75 hover:opacity-100 cursor-pointer"
            >
              <X size={12} />
            </button>
          </Show>
        </div>
      </Show>

      <Show
        when={
          props.images.length > 0 ||
          props.pastes.length > 0 ||
          (props.pendingCount ?? 0) > 0
        }
      >
        <div class="mb-2 flex flex-wrap items-center gap-1.5">
          <For each={props.images}>
            {(img) => (
              <div class="group relative flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] pl-1.5 pr-2 py-1 text-[11px]">
                <Show
                  when={isImageItem(img)}
                  fallback={
                    <span
                      class={`inline-flex h-6 items-center gap-1 rounded bg-[var(--bg-inset)] px-1.5 font-mono text-[10px] font-semibold ${extBadgeColor(img.filename, img.mime, img.kind)}`}
                    >
                      <FileText size={12} class="shrink-0" />
                      {extBadge(img.filename, img.mime)}
                    </span>
                  }
                >
                  <img
                    src={img.previewUrl || img.dataUrl}
                    alt={img.filename}
                    class="h-6 w-6 rounded object-cover"
                  />
                </Show>
                <span class="max-w-[130px] truncate font-mono text-[var(--text-secondary)]">
                  {img.filename}
                </span>
                <button
                  type="button"
                  onClick={() => props.onRemoveImage(img.id)}
                  class="text-[var(--text-muted)] hover:text-[var(--danger-text)] cursor-pointer"
                >
                  <X size={11} />
                </button>
              </div>
            )}
          </For>

          <Show when={(props.pendingCount ?? 0) > 0}>
            <div class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11px] text-[var(--text-muted)]">
              <LoaderCircle size={12} class="animate-spin text-[var(--accent-primary)]" />
              <span>{t("timeline.attachmentReading")}</span>
            </div>
          </Show>

          <For each={props.pastes}>
            {(p) => (
              <div
                class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)]/50 px-2.5 py-1 text-[11px] text-[var(--accent-text)]"
                title={p.preview}
              >
                <ClipboardPaste size={11} class="shrink-0" />
                <span class="font-mono font-medium">
                  {t("timeline.pastedLinesChip", { count: p.lines })}
                </span>
                <button
                  type="button"
                  onClick={() => props.onRemovePaste(p.id)}
                  class="hover:text-[var(--danger-text)] cursor-pointer"
                >
                  <X size={11} />
                </button>
              </div>
            )}
          </For>
        </div>
      </Show>
    </>
  );
}
