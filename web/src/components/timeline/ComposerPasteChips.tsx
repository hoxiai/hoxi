// web/src/components/timeline/ComposerPasteChips.tsx
// Collapsed paste chips above the composer textarea with an anchored popover editor.
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { Portal } from "solid-js/web";
import { ClipboardPaste, Pencil, X } from "lucide-solid";
import { Presence } from "../motion/Presence";
import { useAnchoredPopover } from "../layout/anchored-popover";
import { t } from "../../i18n";
import { computePasteMeta, type CollapsedPasteItem } from "./composer-drafts";

export function ComposerPasteChips(props: {
  pastes: CollapsedPasteItem[];
  onUpdatePaste?: (id: string, content: string) => void;
  onRemovePaste: (id: string) => void;
}) {
  const [editingId, setEditingId] = createSignal<string | null>(null);
  const [draft, setDraft] = createSignal("");
  const [anchorEl, setAnchorEl] = createSignal<HTMLElement | undefined>();
  const [panelRef, setPanelRef] = createSignal<HTMLDivElement | undefined>();
  let textareaRef: HTMLTextAreaElement | undefined;
  const chipRefs = new Map<string, HTMLDivElement>();

  const activePaste = createMemo(() => {
    const id = editingId();
    return id ? props.pastes.find((p) => p.id === id) ?? null : null;
  });

  const draftMeta = createMemo(() => computePasteMeta(draft()));
  const draftEmpty = () => draft().trim().length === 0;
  const canEdit = () => Boolean(props.onUpdatePaste);

  const anchored = useAnchoredPopover({
    anchor: anchorEl,
    panel: () => (activePaste() ? panelRef() : undefined),
    align: "start",
    placement: "top",
    gap: 8,
    margin: 12,
  });

  const openEditor = (item: CollapsedPasteItem) => {
    if (!props.onUpdatePaste) return;
    setAnchorEl(chipRefs.get(item.id));
    setDraft(item.content);
    setEditingId(item.id);
  };

  const closeWithoutSaving = () => {
    setEditingId(null);
  };

  const commitAndClose = () => {
    const current = activePaste();
    if (current && props.onUpdatePaste) {
      const next = draft();
      if (next !== current.content) {
        props.onUpdatePaste(current.id, next);
      }
    }
    setEditingId(null);
  };

  const commitNonEmptyOnDismiss = () => {
    const current = activePaste();
    if (current && props.onUpdatePaste) {
      const next = draft();
      if (next !== current.content && next.trim().length > 0) {
        props.onUpdatePaste(current.id, next);
      }
    }
    setEditingId(null);
  };

  const toggleEditor = (item: CollapsedPasteItem) => {
    if (!props.onUpdatePaste) return;
    if (editingId() === item.id) {
      commitNonEmptyOnDismiss();
      return;
    }
    if (editingId()) {
      commitNonEmptyOnDismiss();
    }
    openEditor(item);
  };

  const handleRemove = (id: string) => {
    if (editingId() === id) setEditingId(null);
    props.onRemovePaste(id);
  };

  // Close editor if the active paste item disappears (e.g. session switch or submit)
  createEffect(() => {
    const id = editingId();
    if (id && !props.pastes.some((p) => p.id === id)) {
      setEditingId(null);
    }
  });

  // Auto-focus editor textarea when opened or switched to another paste chip
  createEffect(() => {
    const current = activePaste();
    if (!current) return;
    let raf = requestAnimationFrame(() => {
      raf = 0;
      textareaRef?.focus();
      const len = textareaRef?.value.length ?? 0;
      textareaRef?.setSelectionRange(len, len);
    });
    onCleanup(() => {
      if (raf) cancelAnimationFrame(raf);
    });
  });

  // Dismiss on outside click (preserving non-empty edits) or Escape (discarding edits)
  createEffect(() => {
    if (!activePaste()) return;
    const handlePointerDown = (ev: MouseEvent) => {
      const target = ev.target as Node | null;
      if (!target) return;
      const panel = panelRef();
      if (panel?.contains(target)) return;
      for (const chipEl of chipRefs.values()) {
        if (chipEl.contains(target)) return;
      }
      commitNonEmptyOnDismiss();
    };
    const handleKeyDown = (ev: KeyboardEvent) => {
      if (ev.isComposing || ev.keyCode === 229) return;
      if (ev.key === "Escape") {
        ev.preventDefault();
        ev.stopPropagation();
        closeWithoutSaving();
      }
    };
    window.addEventListener("pointerdown", handlePointerDown, true);
    window.addEventListener("keydown", handleKeyDown, true);
    onCleanup(() => {
      window.removeEventListener("pointerdown", handlePointerDown, true);
      window.removeEventListener("keydown", handleKeyDown, true);
    });
  });

  return (
    <>
      <For each={props.pastes}>
        {(p) => {
          const isActive = () => editingId() === p.id;
          let elRef: HTMLDivElement | undefined;
          onCleanup(() => {
            if (elRef && chipRefs.get(p.id) === elRef) {
              chipRefs.delete(p.id);
            }
          });
          return (
            <div
              ref={(el) => {
                elRef = el;
                chipRefs.set(p.id, el);
              }}
              data-composer-paste-chip={p.id}
              class={`inline-flex items-center gap-1 rounded-lg border px-2 py-1 text-[11px] transition-colors ${
                isActive()
                  ? "border-[var(--accent-primary)] bg-[var(--accent-surface)] text-[var(--accent-text)]"
                  : "border-[var(--accent-border)] bg-[var(--accent-surface)]/50 text-[var(--accent-text)] hover:bg-[var(--accent-surface)]/80"
              }`}
              title={p.preview}
            >
              <Show
                when={canEdit()}
                fallback={
                  <span class="inline-flex items-center gap-1.5 font-mono font-medium">
                    <ClipboardPaste size={11} class="shrink-0" />
                    <span>{t("timeline.pastedLinesChip", { count: p.lines })}</span>
                  </span>
                }
              >
                <button
                  type="button"
                  onClick={() => toggleEditor(p)}
                  title={p.preview || t("timeline.editPastedText")}
                  class="inline-flex items-center gap-1.5 pr-0.5 font-mono font-medium cursor-pointer"
                >
                  <ClipboardPaste size={11} class="shrink-0" />
                  <span>{t("timeline.pastedLinesChip", { count: p.lines })}</span>
                </button>
                <button
                  type="button"
                  data-paste-edit-btn={p.id}
                  onClick={() => toggleEditor(p)}
                  title={t("timeline.editPastedText")}
                  aria-label={t("timeline.editPastedText")}
                  aria-haspopup="dialog"
                  aria-expanded={isActive()}
                  class={`inline-flex items-center justify-center rounded p-0.5 transition-colors cursor-pointer ${
                    isActive()
                      ? "bg-[var(--bg-panel)]/75 text-[var(--accent-primary)]"
                      : "text-[var(--text-muted)] hover:bg-[var(--bg-panel)]/60 hover:text-[var(--accent-primary)]"
                  }`}
                >
                  <Pencil size={10.5} />
                </button>
              </Show>
              <button
                type="button"
                data-paste-remove-btn={p.id}
                onClick={() => handleRemove(p.id)}
                title={t("timeline.removePastedText")}
                aria-label={t("timeline.removePastedText")}
                class="inline-flex items-center justify-center rounded p-0.5 text-[var(--text-muted)] hover:text-[var(--danger-text)] transition-colors cursor-pointer"
              >
                <X size={11} />
              </button>
            </div>
          );
        }}
      </For>

      <Presence when={Boolean(activePaste())}>
        {(state) => (
          <Portal>
            <div
              ref={setPanelRef}
              data-state={state()}
              data-composer-paste-popover=""
              role="dialog"
              aria-label={t("timeline.editPastedText")}
              class="hoxi-popover fixed z-[110] flex w-[min(560px,calc(100vw-24px))] flex-col overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] shadow-2xl"
              style={{
                ...anchored.positionStyle(),
                "--hoxi-origin": anchored.side() === "top" ? "bottom left" : "top left",
              }}
            >
              <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)] px-3 py-2">
                <div class="flex min-w-0 items-center gap-2">
                  <ClipboardPaste size={13} class="shrink-0 text-[var(--accent-primary)]" />
                  <span class="truncate text-[12px] font-semibold text-[var(--text-primary)]">
                    {t("timeline.editPastedText")}
                  </span>
                  <span class="shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-muted)] tabular-nums">
                    {t("timeline.pastedMetaSummary", {
                      lines: draftEmpty() ? 0 : draftMeta().lines,
                      chars: draftMeta().chars,
                    })}
                  </span>
                </div>
                <button
                  type="button"
                  onClick={closeWithoutSaving}
                  title={t("common.close")}
                  aria-label={t("common.close")}
                  class="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                >
                  <X size={13} />
                </button>
              </div>

              <div class="p-2.5">
                <textarea
                  ref={textareaRef}
                  data-paste-editor-textarea=""
                  value={draft()}
                  onInput={(ev) => setDraft(ev.currentTarget.value)}
                  onKeyDown={(ev) => {
                    if (ev.isComposing || ev.keyCode === 229) return;
                    if ((ev.metaKey || ev.ctrlKey) && ev.key === "Enter") {
                      ev.preventDefault();
                      ev.stopPropagation();
                      commitAndClose();
                    }
                  }}
                  spellcheck={false}
                  placeholder={t("timeline.pasteEditPlaceholder")}
                  class="block w-full min-h-[180px] max-h-[min(45vh,360px)] resize-y rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/75 px-3 py-2 font-mono text-[12px] leading-relaxed text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none focus:border-[var(--accent-border)] selectable-text"
                />
              </div>

              <div class="flex items-center justify-between gap-2 border-t border-[var(--border-subtle)] bg-[var(--bg-panel)]/55 px-3 py-2">
                <span class="truncate text-[11px] text-[var(--text-faint)]">
                  {t("timeline.pasteEditShortcutHint")}
                </span>
                <div class="flex shrink-0 items-center gap-1.5">
                  <button
                    type="button"
                    data-paste-editor-cancel=""
                    onClick={closeWithoutSaving}
                    class="hoxi-press rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-1 text-[11.5px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                  >
                    {t("common.cancel")}
                  </button>
                  <button
                    type="button"
                    data-paste-editor-save=""
                    onClick={commitAndClose}
                    class={`hoxi-press rounded-lg px-2.5 py-1 text-[11.5px] font-semibold transition-opacity cursor-pointer ${
                      draftEmpty()
                        ? "border border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)] hover:opacity-90"
                        : "bg-[var(--accent-primary)] text-[var(--accent-foreground)] hover:opacity-90"
                    }`}
                  >
                    {draftEmpty() ? t("common.delete") : t("common.save")}
                  </button>
                </div>
              </div>
            </div>
          </Portal>
        )}
      </Presence>
    </>
  );
}
