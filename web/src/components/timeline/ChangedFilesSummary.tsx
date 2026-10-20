import {
  createEffect,
  createMemo,
  createSignal,
  createUniqueId,
  For,
  onCleanup,
  Show,
} from "solid-js";
import { ChevronDown, ChevronUp } from "lucide-solid";
import type { SnapshotFileDiff } from "../../services/sse-types";
import { t } from "../../i18n";
import { openDiffInInspector } from "../../store/workspace";
import { DiffChanges, FileIcon, Icon } from "../icons";
import { getDirectory, getFilename } from "./tool-card-helpers";

const PREVIEW_FILE_COUNT = 3;

function ChangedFileRow(props: {
  diff: SnapshotFileDiff;
  onSelect: (diff: SnapshotFileDiff) => void;
  compact?: boolean;
}) {
  const dir = createMemo(() => getDirectory(props.diff.file));
  const name = createMemo(() => getFilename(props.diff.file));

  return (
    <button
      type="button"
      onClick={() => props.onSelect(props.diff)}
      title={props.diff.file}
      class={`flex w-full items-center justify-between gap-4 text-left text-[12.5px] transition-colors cursor-pointer hover:bg-[var(--bg-hover)] ${
        props.compact ? "rounded-xl px-3 py-1.5" : "px-4 py-2"
      }`}
    >
      <span class="min-w-0 flex-1 flex items-center gap-2 font-mono text-[12.5px]">
        <FileIcon
          node={{ path: props.diff.file, type: "file" }}
          class="w-4 h-4 shrink-0"
        />
        <span class="min-w-0 truncate">
          <Show when={dir()}>
            <span class="text-[var(--text-muted)]">{dir()}/</span>
          </Show>
          <span class="font-medium text-[var(--text-primary)]">{name()}</span>
        </span>
      </span>
      <DiffChanges
        class="shrink-0 text-[12px] tabular-nums"
        changes={props.diff}
      />
    </button>
  );
}

export default function ChangedFilesSummary(props: {
  files: SnapshotFileDiff[];
  variant?: "card" | "pill";
  align?: "center" | "start";
  onUndo?: () => void;
}) {
  const [open, setOpen] = createSignal(false);
  const [expanded, setExpanded] = createSignal(false);
  const popoverID = createUniqueId();
  let containerRef: HTMLDivElement | undefined;
  let closeTimer: ReturnType<typeof setTimeout> | undefined;

  const isPill = () => props.variant === "pill";
  const visibleFiles = createMemo(() =>
    expanded() ? props.files : props.files.slice(0, PREVIEW_FILE_COUNT)
  );
  const hiddenCount = createMemo(() =>
    Math.max(0, props.files.length - PREVIEW_FILE_COUNT)
  );

  const clearCloseTimer = () => {
    if (closeTimer !== undefined) {
      clearTimeout(closeTimer);
      closeTimer = undefined;
    }
  };

  const handleMouseEnter = () => {
    clearCloseTimer();
    setOpen(true);
  };

  const handleMouseLeave = () => {
    clearCloseTimer();
    closeTimer = setTimeout(() => {
      setOpen(false);
    }, 140);
  };

  onCleanup(clearCloseTimer);

  createEffect(() => {
    if (props.files.length === 0) {
      clearCloseTimer();
      setOpen(false);
      setExpanded(false);
    }
  });

  createEffect(() => {
    if (!open()) return;
    const onPointerDown = (e: PointerEvent) => {
      if (containerRef && !containerRef.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown);
    onCleanup(() => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown);
    });
  });

  return (
    <Show when={props.files.length > 0}>
      <Show
        when={isPill()}
        fallback={
          <div class="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] overflow-hidden shadow-2xs">
            {/* Header Row */}
            <div class="flex items-center justify-between gap-3 px-4 py-3">
              <div class="flex items-center gap-3 min-w-0">
                <div class="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-secondary)]">
                  <svg
                    viewBox="0 0 16 16"
                    class="h-4 w-4"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="1.5"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    aria-hidden="true"
                  >
                    <rect x="2.25" y="2.25" width="11.5" height="11.5" rx="2.5" />
                    <path d="M8 5.25v3.5M6.25 7h3.5M6.25 10.75h3.5" />
                  </svg>
                </div>
                <div class="flex flex-col min-w-0 leading-tight">
                  <span class="text-[13px] font-semibold text-[var(--text-primary)] truncate">
                    {t("timeline.editedFilesCount", { count: props.files.length })}
                  </span>
                  <DiffChanges
                    class="mt-0.5 self-start text-[12px] tabular-nums"
                    changes={props.files}
                  />
                </div>
              </div>

              <div class="flex items-center gap-2 shrink-0">
                <Show when={props.onUndo}>
                  <button
                    type="button"
                    onClick={() => props.onUndo?.()}
                    title={t("toolCard.revertTooltip")}
                    class="inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-[12px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                  >
                    <span>{t("toolCard.undo")}</span>
                    <Icon name="arrow-undo-down" size="small" />
                  </button>
                </Show>

                <button
                  type="button"
                  onClick={() => {
                    if (props.files[0]) openDiffInInspector(props.files[0], "session");
                  }}
                  class="inline-flex items-center rounded-full border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3 py-1 text-[12px] font-medium text-[var(--text-primary)] hover:border-[var(--border-strong)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                >
                  {t("timeline.viewChanges")}
                </button>
              </div>
            </div>

            {/* One-file-per-row list */}
            <div class="border-t border-[var(--border-subtle)] divide-y divide-[var(--border-subtle)]/60">
              <For each={visibleFiles()}>
                {(diff) => (
                  <ChangedFileRow
                    diff={diff}
                    onSelect={(item) => openDiffInInspector(item, "session")}
                  />
                )}
              </For>
            </div>

            {/* Expand / Collapse remaining files footer */}
            <Show when={hiddenCount() > 0}>
              <button
                type="button"
                onClick={() => setExpanded((v) => !v)}
                class="flex w-full items-center gap-1 border-t border-[var(--border-subtle)]/60 px-4 py-2 text-left text-[12px] font-medium text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
              >
                <span>
                  {expanded()
                    ? t("timeline.showLessFiles")
                    : t("timeline.showMoreFiles", { count: hiddenCount() })}
                </span>
                <Show
                  when={expanded()}
                  fallback={<ChevronDown size={13} class="shrink-0" />}
                >
                  <ChevronUp size={13} class="shrink-0" />
                </Show>
              </button>
            </Show>
          </div>
        }
      >
        <div
          ref={containerRef}
          class="relative inline-flex items-center"
          onMouseEnter={handleMouseEnter}
          onMouseLeave={handleMouseLeave}
        >
          <button
            type="button"
            aria-expanded={open()}
            aria-controls={popoverID}
            onClick={() => {
              if (!open()) {
                setOpen(true);
                return;
              }
              if (props.files[0]) {
                openDiffInInspector(props.files[0], "session");
              }
            }}
            class="inline-flex items-center gap-2 rounded-full border border-[var(--border-subtle)] bg-[var(--bg-panel)]/95 backdrop-blur-md px-3.5 py-1 text-[12px] text-[var(--text-primary)] shadow-xs transition-colors cursor-pointer hover:border-[var(--border-strong)] hover:bg-[var(--bg-hover)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-primary)]"
          >
            <span class="font-medium text-[var(--text-primary)]">
              {t("timeline.filesChangedCount", { count: props.files.length })}
            </span>
            <DiffChanges class="shrink-0 text-[12px] tabular-nums" changes={props.files} />
          </button>

          <Show when={open()}>
            <div
              id={popoverID}
              role="dialog"
              class={`absolute bottom-[calc(100%+8px)] z-40 min-w-[280px] max-w-[min(420px,calc(100vw-32px))] rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-panel)]/98 backdrop-blur-xl p-1.5 shadow-xl animate-in fade-in zoom-in-95 duration-150 ${
                props.align === "start" ? "left-0" : "left-1/2 -translate-x-1/2"
              }`}
            >
              <div class="max-h-[248px] overflow-y-auto py-0.5 space-y-0.5">
                <For each={props.files}>
                  {(diff) => (
                    <ChangedFileRow
                      diff={diff}
                      compact={true}
                      onSelect={(item) => {
                        openDiffInInspector(item, "session");
                        setOpen(false);
                      }}
                    />
                  )}
                </For>
              </div>
            </div>
          </Show>
        </div>
      </Show>
    </Show>
  );
}



