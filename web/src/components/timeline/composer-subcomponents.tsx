// web/src/components/timeline/composer-subcomponents.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  Check,
  ChevronDown,
  ClipboardPaste,
  FileDiff,
  Folder,
  GitBranch,
  GitFork,
  LoaderCircle,
  Slash,
  X,
} from "lucide-solid";
import { FileIcon } from "../icons";
import { openDiffInInspector, workspace } from "../../store/workspace";
import { t } from "../../i18n";
import type {
  AutocompleteItem,
  CollapsedPasteItem,
  ImageItem,
} from "./composer-hooks";

export function ComposerSandboxPill(props: {
  mode: "local" | "worktree";
  switching: boolean;
  busy: boolean;
  onToggle: (target: "local" | "worktree") => void;
  sessionID?: string | null;
  forking?: boolean;
  onFork?: () => void;
}) {
  const [open, setOpen] = createSignal(false);
  let buttonRef: HTMLButtonElement | undefined;
  let menuRef: HTMLDivElement | undefined;

  createEffect(() => {
    if (!open()) return;
    const handleClick = (e: MouseEvent) => {
      const target = e.target as Node;
      if (
        menuRef &&
        !menuRef.contains(target) &&
        buttonRef &&
        !buttonRef.contains(target)
      ) {
        setOpen(false);
      }
    };
    window.addEventListener("pointerdown", handleClick);
    onCleanup(() => window.removeEventListener("pointerdown", handleClick));
  });

  const activeWorktree = createMemo(() => {
    const id = props.sessionID;
    return (id && workspace.worktreeBySession[id]) || workspace.worktreeBySession["__root__"];
  });

  const gitBranch = createMemo(() => activeWorktree()?.branch || "");

  return (
    <div class="relative inline-block select-none">
      <button
        ref={buttonRef}
        type="button"
        onClick={() => setOpen((v) => !v)}
        class={`inline-flex items-center gap-1.5 rounded-lg px-2 py-1 text-[12px] font-medium transition-colors cursor-pointer ${
          open()
            ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
            : props.mode === "worktree"
              ? "text-[var(--accent-primary)] hover:bg-[var(--accent-surface)]"
              : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
        }`}
        title={
          props.mode === "worktree"
            ? t("timeline.sandboxWorktreeTooltip")
            : t("timeline.sandboxLocalTooltip")
        }
      >
        <Show
          when={!props.switching}
          fallback={<LoaderCircle size={12} class="animate-spin shrink-0" />}
        >
          <Show
            when={props.mode === "worktree"}
            fallback={<Folder size={12.5} class="shrink-0 text-[var(--text-muted)]" />}
          >
            <GitBranch size={12.5} class="shrink-0 text-[var(--accent-primary)]" />
          </Show>
        </Show>

        <span>
          {props.mode === "worktree"
            ? t("timeline.sandboxWorktreeMode")
            : t("timeline.sandboxLocalMode")}
        </span>

        <ChevronDown
          size={11}
          class={`text-[var(--text-muted)] transition-transform duration-200 ${
            open() ? "rotate-180" : ""
          }`}
        />
      </button>

      <Show when={open()}>
        <div
          ref={menuRef}
          class="absolute bottom-full left-0 mb-2 w-[268px] rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-2xl z-50 animate-in fade-in zoom-in-95 duration-100"
        >
          {/* Mode Options */}
          <div class="space-y-0.5">
            <button
              type="button"
              disabled={props.switching || props.busy}
              onClick={() => {
                props.onToggle("local");
                setOpen(false);
              }}
              class={`w-full flex items-start gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors cursor-pointer disabled:opacity-50 ${
                props.mode === "local"
                  ? "bg-[var(--accent-surface)] text-[var(--text-primary)]"
                  : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
              }`}
            >
              <div
                class={`mt-0.5 p-1 rounded-lg shrink-0 ${
                  props.mode === "local"
                    ? "bg-[var(--accent-primary)] text-white"
                    : "bg-[var(--bg-inset)] text-[var(--text-muted)]"
                }`}
              >
                <Folder size={12} />
              </div>
              <div class="flex-1 min-w-0">
                <div class="text-[12px] font-medium">
                  {t("timeline.sandboxLocalMode")}
                </div>
                <p class="mt-0.5 text-[11px] leading-snug text-[var(--text-muted)]">
                  {t("timeline.sandboxLocalTooltip")}
                </p>
              </div>
              <Show when={props.mode === "local"}>
                <Check size={14} class="shrink-0 text-[var(--accent-primary)] mt-1" />
              </Show>
            </button>

            <button
              type="button"
              disabled={props.switching || props.busy}
              onClick={() => {
                props.onToggle("worktree");
                setOpen(false);
              }}
              class={`w-full flex items-start gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors cursor-pointer disabled:opacity-50 ${
                props.mode === "worktree"
                  ? "bg-[var(--accent-surface)] text-[var(--text-primary)]"
                  : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
              }`}
            >
              <div
                class={`mt-0.5 p-1 rounded-lg shrink-0 ${
                  props.mode === "worktree"
                    ? "bg-[var(--accent-primary)] text-white"
                    : "bg-[var(--bg-inset)] text-[var(--text-muted)]"
                }`}
              >
                <GitBranch size={12} />
              </div>
              <div class="flex-1 min-w-0">
                <div class="text-[12px] font-medium">
                  {t("timeline.sandboxWorktreeMode")}
                </div>
                <p class="mt-0.5 text-[11px] leading-snug text-[var(--text-muted)]">
                  {t("timeline.sandboxWorktreeTooltip")}
                </p>
              </div>
              <Show when={props.mode === "worktree"}>
                <Check size={14} class="shrink-0 text-[var(--accent-primary)] mt-1" />
              </Show>
            </button>
          </div>

          {/* Git Branch, Diff Review & Session Fork */}
          <Show when={gitBranch() || props.sessionID}>
            <div class="mt-1.5 pt-1.5 border-t border-[var(--border-subtle)] space-y-0.5">
              <Show when={gitBranch()}>
                <button
                  type="button"
                  onClick={() => {
                    openDiffInInspector();
                    setOpen(false);
                  }}
                  class="w-full flex items-center justify-between gap-2 rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                >
                  <div class="flex items-center gap-2 min-w-0">
                    <FileDiff size={13} class="text-[var(--accent-primary)] shrink-0" />
                    <span class="truncate">{t("timeline.reviewDiffs")}</span>
                  </div>
                  <span class="truncate max-w-[120px] font-mono text-[11px] text-[var(--text-muted)]">
                    {gitBranch()}
                  </span>
                </button>
              </Show>

              <Show when={props.sessionID && props.onFork}>
                <button
                  type="button"
                  disabled={props.forking}
                  onClick={() => {
                    props.onFork?.();
                    setOpen(false);
                  }}
                  class="w-full flex items-center justify-between gap-2 rounded-xl px-2.5 py-1.5 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] disabled:opacity-50 transition-colors cursor-pointer"
                >
                  <div class="flex items-center gap-2 min-w-0">
                    <Show
                      when={!props.forking}
                      fallback={<LoaderCircle size={13} class="animate-spin shrink-0" />}
                    >
                      <GitFork size={13} class="text-[var(--text-muted)] shrink-0" />
                    </Show>
                    <span class="truncate">{t("timeline.forkHere")}</span>
                  </div>
                </button>
              </Show>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}

export function ComposerAutocompletePopover(props: {
  triggerMode: "file" | "command" | null;
  suggestions: AutocompleteItem[];
  selectedIndex: number;
  onSelectIndex: (idx: number) => void;
  onApply: (item: AutocompleteItem) => void;
}) {
  return (
    <Show when={props.triggerMode && props.suggestions.length > 0}>
      <div class="absolute bottom-full left-0 mb-2 w-full max-w-[32rem] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1.5 shadow-xl z-40">
        <div class="px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
          {props.triggerMode === "command"
            ? t("timeline.slashCommandsHeader")
            : t("timeline.mentionFileOrContextHeader")}
        </div>
        <div class="max-h-56 overflow-y-auto space-y-0.5">
          <For each={props.suggestions}>
            {(item, idx) => (
              <button
                type="button"
                class={`w-full flex items-center justify-between gap-3 rounded-lg px-2.5 py-1.5 text-left text-[12.5px] transition-colors cursor-pointer ${
                  idx() === props.selectedIndex
                    ? "bg-[var(--accent-surface)] text-[var(--accent-text)]"
                    : "text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                }`}
                onMouseEnter={() => props.onSelectIndex(idx())}
                onClick={() => props.onApply(item)}
              >
                <span class="inline-flex items-center gap-2 min-w-0 font-mono truncate">
                  {item.type === "command" || item.type === "context" ? (
                    <Slash size={12} class="text-[var(--accent-primary)] shrink-0" />
                  ) : (
                    <FileIcon
                      node={{
                        path: item.label,
                        type: item.label.endsWith("/") ? "directory" : "file",
                      }}
                      class="w-3.5 h-3.5 shrink-0"
                    />
                  )}
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
  onRemoveImage: (id: string) => void;
  onRemovePaste: (id: string) => void;
}) {
  return (
    <Show when={props.images.length > 0 || props.pastes.length > 0}>
      <div class="mb-2 flex flex-wrap items-center gap-1.5">
        <For each={props.images}>
          {(img) => (
            <div class="group relative flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] pl-1 pr-2 py-1 text-[11px]">
              <img
                src={img.dataUrl}
                alt={img.filename}
                class="h-6 w-6 rounded object-cover"
              />
              <span class="max-w-[110px] truncate font-mono text-[var(--text-secondary)]">
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
  );
}
