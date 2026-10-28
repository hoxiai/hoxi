// web/src/components/timeline/composer-subcomponents.tsx
import { createEffect, For, Show } from "solid-js";
import {
  AlertCircle,
  ArrowUp,
  AtSign,
  Check,
  ClipboardPaste,
  Clock,
  FileText,
  LoaderCircle,
  RefreshCw,
  Slash,
  Sparkles,
  Square,
  Workflow,
  X,
} from "lucide-solid";
import { FileIcon } from "../icons";
import { t } from "../../i18n";
import type { SkillSelection } from "../../services/api";
import {
  classifyAttachmentKind,
  type AutocompleteItem,
  type CollapsedPasteItem,
  type ContextMentionChipInfo,
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
        <div class="px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
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
                class={`w-full flex flex-col gap-0.5 rounded-lg px-2.5 py-1 text-left text-[13px] transition-colors cursor-pointer ${
                  idx() === props.selectedIndex
                    ? "bg-[var(--accent-surface)] text-[var(--accent-text)]"
                    : "text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
                }`}
                onMouseEnter={() => props.onSelectIndex(idx())}
                onClick={() => props.onApply(item)}
              >
                <span class="flex w-full items-center gap-2 min-w-0 font-mono">
                  {item.source === "skill" ? (
                    <Sparkles size={12} class="text-[var(--accent-primary)] shrink-0" />
                  ) : item.type === "context" ? (
                    <AtSign size={12} class="text-[var(--accent-primary)] shrink-0" />
                  ) : item.type === "file" ? (
                    <FileIcon
                      node={{
                        path: item.label,
                        type: item.label.endsWith("/") ? "directory" : "file",
                      }}
                      class="w-3.5 h-3.5 shrink-0"
                    />
                  ) : null}
                  <span class="truncate">
                    {item.type === "context" ? item.label.replace(/^@/, "") : item.label}
                  </span>
                  <Show when={item.hints && item.hints.length > 0}>
                    <span class="shrink-0 font-sans text-[11px] text-[var(--text-faint)]">
                      {item.hints!.join(" ")}
                    </span>
                  </Show>
                  <Show when={item.source}>
                    <span class="ml-auto shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-sans text-[10px] font-medium uppercase tracking-wider text-[var(--text-faint)]">
                      {item.source === "skill"
                        ? t("skills.groupSkills")
                        : item.source === "mcp"
                        ? t("skills.groupMcp")
                        : t("skills.groupCommands")}
                    </span>
                  </Show>
                </span>
                <Show when={item.detail}>
                  <span class="w-full truncate font-sans text-[11px] leading-tight text-[var(--text-muted)]">
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
  contextMentions?: ContextMentionChipInfo[];
  onRemoveMention?: (token: string) => void;
  pendingCount?: number;
  notice?: string | null;
  onDismissNotice?: () => void;
  selectedSkill?: SkillSelection | null;
  skillNotice?: string | null;
  onDismissSkillNotice?: () => void;
  onRemoveSkill?: () => void;
  onInspectSkill?: (skill: SkillSelection) => void;
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
        <div class="mb-2 flex items-center justify-between gap-2 rounded-lg border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2.5 py-1.5 text-[12px] text-[var(--danger-text)]">
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

      <Show when={props.skillNotice}>
        <div class="mb-2 flex items-center justify-between gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-2.5 py-1.5 text-[12px] text-amber-500">
          <span class="inline-flex items-center gap-1.5 min-w-0">
            <AlertCircle size={13} class="shrink-0" />
            <span class="truncate">{props.skillNotice}</span>
          </span>
          <Show when={props.onDismissSkillNotice}>
            <button
              type="button"
              onClick={() => props.onDismissSkillNotice?.()}
              class="shrink-0 opacity-75 hover:opacity-100 cursor-pointer"
            >
              <X size={12} />
            </button>
          </Show>
        </div>
      </Show>

      <Show
        when={
          Boolean(props.selectedSkill) ||
          (props.contextMentions?.length ?? 0) > 0 ||
          props.images.length > 0 ||
          props.pastes.length > 0 ||
          (props.pendingCount ?? 0) > 0
        }
      >
        <div class="mb-2 flex flex-wrap items-center gap-1.5">
          <For each={props.contextMentions ?? []}>
            {(m) => (
              <div
                class={`inline-flex items-center gap-1.5 rounded-lg border px-2 py-1 text-[11px] ${
                  m.status === "error"
                    ? "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]"
                    : m.status === "empty"
                      ? "border-amber-500/30 bg-amber-500/10 text-amber-500"
                      : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-secondary)]"
                }`}
              >
                <Slash size={11} class="shrink-0 text-[var(--accent-primary)]" />
                <span class="font-mono font-medium text-[var(--text-primary)]">{m.label}</span>
                <span class="text-[11px] text-[var(--text-muted)]">· {m.summary}</span>
                <Show when={m.status === "truncated"}>
                  <span class="rounded bg-amber-500/15 px-1 py-0.5 text-[11px] font-medium text-amber-500">
                    {t("timeline.mentionStatusTruncated")}
                  </span>
                </Show>
                <Show when={props.onRemoveMention}>
                  <button
                    type="button"
                    onClick={() => props.onRemoveMention?.(m.token)}
                    class="text-[var(--text-muted)] hover:text-[var(--danger-text)] cursor-pointer"
                  >
                    <X size={11} />
                  </button>
                </Show>
              </div>
            )}
          </For>
          <Show when={props.selectedSkill}>
            {(skill) => (
              <div class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1 text-[11px] text-[var(--accent-text)]">
                <button
                  type="button"
                  onClick={() => props.onInspectSkill?.(skill())}
                  title={t("skills.selectedInComposer", { name: skill().name })}
                  class="inline-flex items-center gap-1.5 font-mono font-medium hover:underline cursor-pointer"
                >
                  <Sparkles size={11} class="shrink-0 text-[var(--accent-primary)]" />
                  <span>{t("skills.composerPillPrefix")}: {skill().name}</span>
                </button>
                <button
                  type="button"
                  onClick={() => props.onRemoveSkill?.()}
                  title={t("skills.removeFromComposer")}
                  class="text-[var(--text-muted)] hover:text-[var(--danger-text)] cursor-pointer"
                >
                  <X size={11} />
                </button>
              </div>
            )}
          </Show>
          <For each={props.images}>
            {(img) => (
              <div class="group relative flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] pl-1.5 pr-2 py-1 text-[11px]">
                <Show
                  when={isImageItem(img)}
                  fallback={
                    <span
                      class={`inline-flex h-6 items-center gap-1 rounded bg-[var(--bg-inset)] px-1.5 font-mono text-[11px] font-semibold ${extBadgeColor(img.filename, img.mime, img.kind)}`}
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

export function ComposerEnhancePreview(props: {
  state: { original: string; enhanced: string } | null;
  error: string | null;
  enhancing: boolean;
  onRegenerate: () => void;
  onAccept: () => void;
  onDiscard: () => void;
}) {
  return (
    <>
      <Show when={props.state}>
        {(state) => (
          <div class="mt-2 rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)]/35 px-2.5 py-2">
            <div class="flex items-center gap-1.5">
              <Sparkles size={12} class="text-[var(--accent-primary)]" />
              <span class="text-[11px] font-semibold text-[var(--text-primary)]">
                {t("timeline.enhancedPromptTitle")}
              </span>
              <div class="ml-auto flex items-center gap-1">
                <button
                  type="button"
                  class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer disabled:opacity-40"
                  disabled={props.enhancing}
                  onClick={props.onRegenerate}
                >
                  <RefreshCw size={10} class={props.enhancing ? "animate-spin" : ""} />
                  {t("timeline.enhanceRegenerate")}
                </button>
                <button
                  type="button"
                  class="inline-flex items-center gap-1 rounded-md bg-[var(--accent-primary)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 cursor-pointer"
                  onClick={props.onAccept}
                >
                  <Check size={10} />
                  {t("timeline.enhanceAccept")}
                </button>
                <button
                  type="button"
                  class="inline-flex h-5 w-5 items-center justify-center rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  title={t("timeline.enhanceDiscard")}
                  onClick={props.onDiscard}
                >
                  <X size={11} />
                </button>
              </div>
            </div>
            <div class="mt-1.5 max-h-40 overflow-y-auto whitespace-pre-wrap text-[12px] leading-relaxed text-[var(--text-secondary)] selectable-text">
              {state().enhanced}
            </div>
          </div>
        )}
      </Show>
      <Show when={props.error}>
        <p class="mt-1.5 text-[11px] text-[var(--danger-text)]">{props.error}</p>
      </Show>
    </>
  );
}

export function ComposerSendButtons(props: {
  busy: boolean;
  canSend: boolean;
  canSpawnSubtask: boolean;
  sending: boolean;
  spawningSubtask: boolean;
  stopping: boolean;
  sendShortcut: string;
  onSend: () => void;
  onSpawnSubtask: () => void;
  onStop: () => void;
}) {
  return (
    <>
      <Show when={props.busy}>
        <Show when={props.canSpawnSubtask}>
          <button
            type="button"
            disabled={props.spawningSubtask || props.sending}
            onClick={props.onSpawnSubtask}
            title={t("timeline.spawnSubtaskAsideTooltip")}
            class="inline-flex h-7 items-center justify-center gap-1 rounded-full border border-[var(--accent-border)] bg-[var(--accent-surface)]/70 px-2.5 text-[11px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] disabled:opacity-50 transition-colors cursor-pointer"
          >
            <Show
              when={!props.spawningSubtask}
              fallback={<LoaderCircle size={12} class="animate-spin" />}
            >
              <Workflow size={12} />
            </Show>
            <span>{t("timeline.spawnSubtaskAside")}</span>
          </button>
        </Show>

        <Show when={props.canSend}>
          <button
            type="button"
            class="inline-flex h-7 items-center justify-center gap-1 rounded-full bg-[var(--accent-primary)] px-2.5 text-[var(--accent-foreground)] shadow-xs hover:opacity-90 transition-all cursor-pointer"
            onClick={props.onSend}
            title={t("timeline.queueSend")}
          >
            <Show
              when={!props.sending}
              fallback={<LoaderCircle size={14} class="animate-spin" />}
            >
              <Clock size={12.5} strokeWidth={2.2} />
              <ArrowUp size={13.5} strokeWidth={2.5} />
            </Show>
          </button>
        </Show>

        <button
          type="button"
          class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-[var(--bg-inset)] text-[var(--text-secondary)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)] disabled:opacity-50 transition-colors cursor-pointer"
          disabled={props.stopping}
          onClick={props.onStop}
          title={t("timeline.stopTooltip")}
        >
          <Show
            when={!props.stopping}
            fallback={<LoaderCircle size={13} class="animate-spin" />}
          >
            <Square size={10} class="fill-current" />
          </Show>
        </button>
      </Show>

      <Show when={!props.busy}>
        <button
          type="button"
          class="inline-flex h-7 w-7 items-center justify-center rounded-full bg-[var(--accent-primary)] text-[var(--accent-foreground)] shadow-xs hover:opacity-90 disabled:opacity-30 disabled:cursor-not-allowed transition-all cursor-pointer"
          disabled={!props.canSend}
          onClick={props.onSend}
          title={`${t("timeline.send")} (${
            props.sendShortcut === "mod-enter" ? "⌘/Ctrl+Enter" : "Enter"
          })`}
        >
          <Show
            when={!props.sending}
            fallback={<LoaderCircle size={14} class="animate-spin" />}
          >
            <ArrowUp size={15} strokeWidth={2.5} />
          </Show>
        </button>
      </Show>
    </>
  );
}
