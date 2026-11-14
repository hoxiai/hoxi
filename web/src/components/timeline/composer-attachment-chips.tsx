// web/src/components/timeline/composer-attachment-chips.tsx
// Attachment, paste and context-mention chips shown above the composer input.
// Kept out of composer-subcomponents so the Work surface composer can reuse it
// without pulling in ModeSwitcher, which reads the surface extension back.
import { For, Show } from "solid-js";
import {
  AlertCircle,
  Check,
  ClipboardPaste,
  FileText,
  LoaderCircle,
  RefreshCw,
  Slash,
  Sparkles,
  X,
} from "lucide-solid";
import { t } from "../../i18n";
import type { SkillSelection } from "../../services/api";
import { classifyAttachmentKind } from "./composer-hooks";
import type {
  CollapsedPasteItem,
  ContextMentionChipInfo,
  ImageItem,
} from "./composer-hooks";

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
