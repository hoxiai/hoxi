// web/src/components/timeline/composer-attachment-chips.tsx
// Attachment, paste, active-command, context-mention chips, and autocomplete popover.
// Kept out of composer-subcomponents so the Work surface composer can reuse them
// without pulling in ModeSwitcher, which reads the surface extension back.
import { createEffect, createSignal, For, onCleanup, Show } from "solid-js";
import { Portal } from "solid-js/web";
import {
  AlertCircle,
  AtSign,
  Check,
  ClipboardPaste,
  ExternalLink,
  FileText,
  LoaderCircle,
  Play,
  RefreshCw,
  Slash,
  Sparkles,
  Terminal,
  X,
} from "lucide-solid";
import { FileIcon } from "../icons";
import { t } from "../../i18n";
import type { SkillSelection } from "../../services/api";
import {
  isLoopbackMediaURL,
  openStagedMediaExternally,
  resolveMediaPlaybackURL,
} from "../../store/workspace";
import { classifyAttachmentKind } from "./composer-hooks";
import type {
  ActiveSlashCommandInfo,
  AutocompleteItem,
  CollapsedPasteItem,
  ContextMentionChipInfo,
  ImageItem,
} from "./composer-hooks";
import type { ComposerContextAttachment } from "../../store/workspace";
import { ComposerContextChips } from "./ComposerContextChips";

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
                  ) : item.type === "command" ? (
                    <Terminal size={12} class="text-[var(--accent-primary)] shrink-0" />
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
  contexts?: ComposerContextAttachment[];
  onRemoveContext?: (id: string) => void;
  onRemoveContextItem?: (attachmentId: string, itemIndex: number) => void;
  activeCommand?: ActiveSlashCommandInfo | null;
  onRemoveCommand?: () => void;
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

  const isVideoItem = (img: ImageItem) => {
    const k = img.kind ?? classifyAttachmentKind(img.filename, img.mime);
    return k === "video" || (img.mime || "").toLowerCase().startsWith("video/");
  };

  const [previewVideo, setPreviewVideo] = createSignal<{
    url: string;
    sourceUrl: string;
    filename: string;
  } | null>(null);
  const [previewVideoError, setPreviewVideoError] = createSignal(false);

  createEffect(() => {
    void previewVideo();
    setPreviewVideoError(false);
    if (!previewVideo()) return;
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        ev.preventDefault();
        setPreviewVideo(null);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    onCleanup(() => window.removeEventListener("keydown", onKeyDown));
  });

  const extBadge = (filename: string, mime: string) => {
    const lower = (mime || "").toLowerCase();
    if (lower === "application/pdf" || /\.pdf$/i.test(filename)) return "PDF";
    if (lower.startsWith("audio/")) return (filename.split(".").pop() || "AUD").toUpperCase().slice(0, 4);
    if (lower.startsWith("video/")) return (filename.split(".").pop() || "VID").toUpperCase().slice(0, 4);
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
    if (kind === "audio" || (mime || "").toLowerCase().startsWith("audio/")) {
      return "text-amber-500";
    }
    if (kind === "video" || (mime || "").toLowerCase().startsWith("video/")) {
      return "text-purple-500";
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
          Boolean(props.activeCommand) ||
          Boolean(props.selectedSkill) ||
          (props.contextMentions?.length ?? 0) > 0 ||
          props.images.length > 0 ||
          props.pastes.length > 0 ||
          (props.contexts?.length ?? 0) > 0 ||
          (props.pendingCount ?? 0) > 0
        }
      >
        <div class="mb-2 flex flex-wrap items-center gap-1.5">
          <Show when={props.activeCommand}>
            {(cmd) => (
              <div
                class="inline-flex max-w-full items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2.5 py-1 text-[11px] text-[var(--accent-text)]"
                title={cmd().description || `/${cmd().name}`}
              >
                <Terminal size={11} class="shrink-0 text-[var(--accent-primary)]" />
                <span class="shrink-0 font-mono font-semibold text-[var(--accent-primary)]">
                  /{cmd().name}
                </span>
                <Show
                  when={
                    cmd().hintSlots &&
                    cmd().hintSlots!.length > 0 &&
                    !(cmd().hasArgs && cmd().hintSlots!.length === 1 && cmd().hintSlots![0] === "<args>")
                  }
                >
                  <span class="inline-flex shrink-0 items-center gap-1 font-mono text-[10px]">
                    <For each={cmd().hintSlots!}>
                      {(slot, slotIdx) => {
                        const isActiveSlot = () =>
                          slotIdx() === cmd().activeArgIndex ||
                          (slotIdx() === cmd().hintSlots!.length - 1 &&
                            cmd().activeArgIndex >= cmd().hintSlots!.length);
                        const isFilledSlot = () => slotIdx() < cmd().activeArgIndex && !isActiveSlot();
                        return (
                          <span
                            class={`rounded px-1.5 py-0.5 transition-colors ${
                              isActiveSlot()
                                ? "border border-[var(--accent-border)] bg-[var(--bg-panel)] font-semibold text-[var(--accent-primary)]"
                                : isFilledSlot()
                                  ? "bg-[var(--bg-panel)]/50 text-[var(--text-faint)]"
                                  : "bg-[var(--bg-panel)]/65 text-[var(--text-muted)]"
                            }`}
                          >
                            {slot}
                          </span>
                        );
                      }}
                    </For>
                  </span>
                </Show>
                <Show when={cmd().description}>
                  <span class="max-w-[260px] truncate text-[11px] text-[var(--text-secondary)]">
                    · {cmd().description}
                  </span>
                </Show>
                <Show when={cmd().agent}>
                  <span class="shrink-0 rounded bg-[var(--bg-panel)]/75 px-1.5 py-0.5 font-mono text-[10px] font-medium text-[var(--text-muted)]">
                    {cmd().agent}
                  </span>
                </Show>
                <Show when={cmd().subtask}>
                  <span class="shrink-0 rounded bg-[var(--bg-panel)]/75 px-1.5 py-0.5 font-mono text-[10px] font-medium text-[var(--text-muted)]">
                    subtask
                  </span>
                </Show>
                <Show when={props.onRemoveCommand}>
                  <button
                    type="button"
                    onClick={() => props.onRemoveCommand?.()}
                    title={t("timeline.removeSlashCommand")}
                    class="shrink-0 text-[var(--text-muted)] hover:text-[var(--danger-text)] cursor-pointer"
                  >
                    <X size={11} />
                  </button>
                </Show>
              </div>
            )}
          </Show>
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
                    <Show
                      when={isVideoItem(img)}
                      fallback={
                        <span
                          class={`inline-flex h-6 items-center gap-1 rounded bg-[var(--bg-inset)] px-1.5 font-mono text-[11px] font-semibold ${extBadgeColor(img.filename, img.mime, img.kind)}`}
                        >
                          <FileText size={12} class="shrink-0" />
                          {extBadge(img.filename, img.mime)}
                        </span>
                      }
                    >
                      <button
                        type="button"
                        onClick={() =>
                          setPreviewVideo({
                            url: resolveMediaPlaybackURL(img.previewUrl || img.dataUrl),
                            sourceUrl: img.dataUrl,
                            filename: img.filename,
                          })
                        }
                        title={img.filename}
                        class="group/vid relative block h-8 w-12 shrink-0 overflow-hidden rounded bg-black cursor-pointer"
                      >
                        <video
                          src={resolveMediaPlaybackURL(img.previewUrl || img.dataUrl)}
                          crossOrigin={
                            isLoopbackMediaURL(resolveMediaPlaybackURL(img.previewUrl || img.dataUrl))
                              ? "anonymous"
                              : undefined
                          }
                          muted
                          playsinline
                          preload="metadata"
                          class="h-full w-full object-cover pointer-events-none"
                        />
                        <span class="absolute inset-0 flex items-center justify-center bg-black/35 group-hover/vid:bg-black/50 transition-colors">
                          <Play size={12} class="fill-white text-white translate-x-[0.5px]" />
                        </span>
                      </button>
                    </Show>
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
          <Show when={(props.contexts?.length ?? 0) > 0 && props.onRemoveContext}>
            <ComposerContextChips
              contexts={props.contexts ?? []}
              onRemoveContext={(id) => props.onRemoveContext?.(id)}
              onRemoveContextItem={props.onRemoveContextItem}
            />
          </Show>
        </div>
      </Show>
      <Show when={previewVideo()}>
        {(vid) => (
          <Portal>
            <div
              class="fixed inset-0 z-[120] flex items-center justify-center bg-black/75 p-6 backdrop-blur-xs"
              onClick={() => setPreviewVideo(null)}
            >
              <div
                class="flex max-h-[90vh] max-w-[90vw] flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] shadow-2xl"
                onClick={(ev) => ev.stopPropagation()}
              >
                <div class="flex items-center justify-between gap-3 border-b border-[var(--border-subtle)] px-3.5 py-2 text-[12px]">
                  <span class="truncate font-mono text-[var(--text-primary)]">
                    {vid().filename}
                  </span>
                  <button
                    type="button"
                    onClick={() => setPreviewVideo(null)}
                    class="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                  >
                    <X size={14} />
                  </button>
                </div>
                <Show
                  when={!previewVideoError()}
                  fallback={
                    <div class="flex flex-col items-center justify-center gap-3 px-6 py-8 text-center">
                      <AlertCircle size={20} class="text-amber-500" />
                      <div class="max-w-sm text-[12px] text-[var(--text-secondary)]">
                        {t("timeline.videoPlaybackFailed")}
                      </div>
                      <Show when={vid().sourceUrl.startsWith("/api/hoxi/file/content?")}>
                        <button
                          type="button"
                          onClick={() => openStagedMediaExternally(vid().sourceUrl, "open")}
                          class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)] px-3 py-1.5 text-[12px] font-medium text-[var(--accent-primary)] hover:opacity-90 cursor-pointer"
                        >
                          <ExternalLink size={13} />
                          <span>{t("inspector.files.openInDefaultApp")}</span>
                        </button>
                      </Show>
                    </div>
                  }
                >
                  <video
                    src={vid().url}
                    crossOrigin={isLoopbackMediaURL(vid().url) ? "anonymous" : undefined}
                    controls
                    autoplay
                    playsinline
                    onError={() => setPreviewVideoError(true)}
                    class="max-h-[80vh] max-w-[88vw] bg-black object-contain"
                  />
                </Show>
              </div>
            </div>
          </Portal>
        )}
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
