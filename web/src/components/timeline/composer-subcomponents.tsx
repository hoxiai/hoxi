// web/src/components/timeline/composer-subcomponents.tsx
import { For, Show } from "solid-js";
import { ClipboardPaste, Folder, GitBranch, LoaderCircle, Slash, X } from "lucide-solid";
import { FileIcon } from "../icons";
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
}) {
  return (
    <div class="inline-flex items-center rounded-full border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-0.5 text-[11px] font-medium select-none">
      <button
        type="button"
        disabled={props.switching || props.busy}
        onClick={() => props.onToggle("local")}
        class={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 transition-all cursor-pointer disabled:opacity-50 ${
          props.mode === "local"
            ? "bg-[var(--bg-hover)] text-[var(--text-primary)] shadow-2xs"
            : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
        }`}
        title={t("timeline.sandboxLocalTooltip")}
      >
        <Folder size={11} class="shrink-0" />
        <span>{t("timeline.sandboxLocalMode")}</span>
      </button>
      <button
        type="button"
        disabled={props.switching || props.busy}
        onClick={() => props.onToggle("worktree")}
        class={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 transition-all cursor-pointer disabled:opacity-50 ${
          props.mode === "worktree"
            ? "bg-[var(--accent-surface)] text-[var(--accent-primary)] shadow-2xs"
            : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
        }`}
        title={t("timeline.sandboxWorktreeTooltip")}
      >
        <Show
          when={!props.switching}
          fallback={<LoaderCircle size={11} class="animate-spin shrink-0" />}
        >
          <GitBranch size={11} class="shrink-0" />
        </Show>
        <span>{t("timeline.sandboxWorktreeMode")}</span>
      </button>
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
