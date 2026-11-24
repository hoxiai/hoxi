// web/src/components/timeline/ComposerContextChips.tsx
// Compact interactive pills for attached hoxi-context v1 items above the
// composer textarea, with click-to-preview popover and per-item removal.
import { createEffect, createSignal, For, onCleanup, Show } from "solid-js";
import {
  Database,
  FileCode2,
  Layers,
  Link2,
  Quote,
  Trash2,
  X,
} from "lucide-solid";
import {
  deriveContextItemPreview,
  estimateContextEnvelopeChars,
  formatContextSizeLabel,
  getContextItemBodyText,
  prettyFormatContextBody,
  type ComposerContextAttachment,
  type ContextItem,
} from "../../store/context-protocol";
import { t } from "../../i18n";

function chipIcon(items: ContextItem[]) {
  if (items.length > 1) {
    return items[0]?.kind === "entity" ? (
      <Database size={11} class="shrink-0 text-[var(--accent-primary)]" />
    ) : (
      <Layers size={11} class="shrink-0 text-[var(--accent-primary)]" />
    );
  }
  switch (items[0]?.kind) {
    case "quote":
      return <Quote size={11} class="shrink-0 text-[var(--accent-primary)]" />;
    case "entity":
      return <Database size={11} class="shrink-0 text-[var(--accent-primary)]" />;
    case "snapshot":
      return <FileCode2 size={11} class="shrink-0 text-[var(--accent-primary)]" />;
    default:
      return <Layers size={11} class="shrink-0 text-[var(--accent-primary)]" />;
  }
}

function chipLabel(att: ComposerContextAttachment): string {
  if (att.items.length === 1) {
    const item = att.items[0];
    return item.title || deriveContextItemPreview(item, 28) || t("timeline.contextReferencedSingle");
  }
  const firstTitle = att.items[0]?.title || att.items[0]?.ref?.id || "";
  const base = t("timeline.contextChipBatch", { count: att.items.length });
  return firstTitle ? `${base} · ${firstTitle}` : base;
}

export function ComposerContextChips(props: {
  contexts: ComposerContextAttachment[];
  onRemoveContext: (id: string) => void;
  onRemoveContextItem?: (attachmentId: string, itemIndex: number) => void;
}) {
  const [inspectId, setInspectId] = createSignal<string | null>(null);
  const inspected = () =>
    props.contexts.find((c) => c.id === inspectId()) ?? null;

  createEffect(() => {
    if (!inspected()) return;
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        ev.preventDefault();
        setInspectId(null);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    onCleanup(() => window.removeEventListener("keydown", onKeyDown));
  });

  return (
    <>
      <For each={props.contexts}>
        {(att) => {
          const sourceLabel = () => att.source?.appName || att.source?.appId || "";
          const sizeLabel = () => {
            const chars = estimateContextEnvelopeChars(att);
            return chars > 0 ? formatContextSizeLabel(chars) : "";
          };
          const active = () => inspectId() === att.id;
          return (
            <div
              data-composer-context-chip={att.id}
              class={`inline-flex max-w-[280px] items-center gap-1.5 rounded-lg border px-2 py-1 text-[11px] transition-colors ${
                active()
                  ? "border-[var(--accent-primary)] bg-[var(--accent-surface)] text-[var(--accent-text)]"
                  : "border-[var(--accent-border)] bg-[var(--accent-surface)]/55 text-[var(--accent-text)] hover:bg-[var(--accent-surface)]"
              }`}
            >
              <button
                type="button"
                onClick={() => setInspectId((cur) => (cur === att.id ? null : att.id))}
                class="inline-flex min-w-0 flex-1 items-center gap-1.5 text-left cursor-pointer"
                title={chipLabel(att)}
              >
                {chipIcon(att.items)}
                <Show when={sourceLabel()}>
                  <span class="shrink-0 rounded bg-[var(--bg-panel)]/80 px-1 py-0.5 text-[10px] font-medium text-[var(--text-secondary)]">
                    {sourceLabel()}
                  </span>
                </Show>
                <span class="truncate font-medium text-[var(--text-primary)]">
                  {chipLabel(att)}
                </span>
                <Show when={sizeLabel()}>
                  <span class="shrink-0 font-mono text-[10px] text-[var(--text-muted)]">
                    {sizeLabel()}
                  </span>
                </Show>
              </button>
              <button
                type="button"
                onClick={() => {
                  if (inspectId() === att.id) setInspectId(null);
                  props.onRemoveContext(att.id);
                }}
                title={t("timeline.contextRemoveTooltip")}
                class="shrink-0 text-[var(--text-muted)] hover:text-[var(--danger-text)] cursor-pointer"
              >
                <X size={11} />
              </button>
            </div>
          );
        }}
      </For>

      <Show when={inspected()}>
        {(att) => (
          <div
            data-composer-context-preview={att().id}
            class="w-full basis-full rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] shadow-md overflow-hidden"
          >
            <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)] px-2.5 py-1.5 text-[11px]">
              <div class="flex items-center gap-1.5 min-w-0">
                {chipIcon(att().items)}
                <span class="font-semibold text-[var(--text-primary)] truncate">
                  {t("timeline.contextPreviewTitle", { count: att().items.length })}
                </span>
                <Show when={att().source?.appName || att().source?.appId}>
                  <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] text-[var(--text-secondary)]">
                    {att().source?.appName || att().source?.appId}
                  </span>
                </Show>
              </div>
              <div class="flex items-center gap-1 shrink-0">
                <button
                  type="button"
                  onClick={() => {
                    setInspectId(null);
                    props.onRemoveContext(att().id);
                  }}
                  title={t("timeline.contextClearAllTooltip")}
                  class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10.5px] text-[var(--text-muted)] hover:bg-[var(--danger-surface)] hover:text-[var(--danger-text)] cursor-pointer"
                >
                  <Trash2 size={10} />
                  <span>{t("timeline.contextClearAllTooltip")}</span>
                </button>
                <button
                  type="button"
                  onClick={() => setInspectId(null)}
                  class="rounded p-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                >
                  <X size={12} />
                </button>
              </div>
            </div>
            <div class="max-h-52 overflow-y-auto divide-y divide-[var(--border-subtle)]/60">
              <For each={att().items}>
                {(item, idx) => {
                  const body = getContextItemBodyText(item);
                  const formatted = prettyFormatContextBody(body);
                  const resolveLabel = item.ref?.resolve?.mcpTool || item.ref?.resolve?.servicePath || "";
                  return (
                    <div class="px-2.5 py-2 space-y-1">
                      <div class="flex items-start gap-1.5 min-w-0">
                        <div class="min-w-0 flex-1">
                          <div class="flex flex-wrap items-center gap-1.5">
                            <Show when={item.title}>
                              <span class="text-[11.5px] font-medium text-[var(--text-primary)]">
                                {item.title}
                              </span>
                            </Show>
                            <Show when={item.badge}>
                              <span class="rounded border border-[var(--accent-border)]/70 bg-[var(--accent-surface)]/70 px-1 py-0.5 text-[10px] text-[var(--accent-text)]">
                                {item.badge}
                              </span>
                            </Show>
                            <Show when={item.ref?.id}>
                              <span class="rounded bg-[var(--bg-inset)] px-1 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                                {item.entityType ? `${item.entityType}:` : "#"}
                                {item.ref!.id}
                              </span>
                            </Show>
                          </div>
                          <Show when={item.subtitle}>
                            <div class="text-[11px] text-[var(--text-secondary)]">
                              {item.subtitle}
                            </div>
                          </Show>
                          <Show when={resolveLabel}>
                            <div class="inline-flex items-center gap-1 font-mono text-[10px] text-[var(--text-faint)]">
                              <Link2 size={10} />
                              <span>{resolveLabel}</span>
                            </div>
                          </Show>
                        </div>
                        <Show when={props.onRemoveContextItem && att().items.length > 1}>
                          <button
                            type="button"
                            onClick={() => props.onRemoveContextItem?.(att().id, idx())}
                            title={t("timeline.contextRemoveTooltip")}
                            class="shrink-0 rounded p-0.5 text-[var(--text-muted)] hover:text-[var(--danger-text)] cursor-pointer"
                          >
                            <X size={11} />
                          </button>
                        </Show>
                      </div>
                      <Show when={body}>
                        <div
                          class={`max-h-28 overflow-auto rounded border border-[var(--border-subtle)]/70 bg-[var(--bg-inset)]/70 px-2 py-1 selectable-text ${
                            formatted.isJson
                              ? "font-mono text-[10.5px] leading-snug text-[var(--text-secondary)] whitespace-pre"
                              : "text-[11.5px] leading-snug text-[var(--text-secondary)] whitespace-pre-wrap break-words"
                          }`}
                        >
                          {formatted.text}
                        </div>
                      </Show>
                    </div>
                  );
                }}
              </For>
            </div>
          </div>
        )}
      </Show>
    </>
  );
}
