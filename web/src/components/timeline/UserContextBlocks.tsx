// web/src/components/timeline/UserContextBlocks.tsx
// Universal collapsible display for structured hoxi-context v1 envelopes inside
// user timeline bubbles and queued prompts: light quote bars for text excerpts,
// compact collapsible cards for selected entities, table records, and snapshots.
import { createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  Check,
  ChevronDown,
  Copy,
  Database,
  FileCode2,
  Layers,
  Link2,
  Quote,
} from "lucide-solid";
import {
  deriveContextItemPreview,
  estimateContextEnvelopeChars,
  formatContextSizeLabel,
  getContextItemBodyText,
  prettyFormatContextBody,
  type ContextEnvelope,
  type ContextItem,
} from "../../store/context-protocol";
import { t } from "../../i18n";

const MAX_EXPANDED_CONTEXT_BLOCKS = 512;
const expandedContextBlocks = new Set<string>();

function rememberExpanded(key: string, open: boolean) {
  if (open) {
    expandedContextBlocks.delete(key);
    if (expandedContextBlocks.size >= MAX_EXPANDED_CONTEXT_BLOCKS) {
      const oldest = expandedContextBlocks.values().next().value;
      if (oldest !== undefined) expandedContextBlocks.delete(oldest);
    }
    expandedContextBlocks.add(key);
  } else {
    expandedContextBlocks.delete(key);
  }
}

function isLongQuote(text: string): boolean {
  if (text.length > 140) return true;
  let nl = 0;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10 && ++nl >= 2) return true;
  }
  return false;
}

function ContextKindIcon(props: { item?: ContextItem; count: number }) {
  if (props.count > 1) {
    return props.item?.kind === "entity" ? (
      <Database size={12} class="shrink-0 text-[var(--accent-primary)]" />
    ) : (
      <Layers size={12} class="shrink-0 text-[var(--accent-primary)]" />
    );
  }
  switch (props.item?.kind) {
    case "quote":
      return <Quote size={12} class="shrink-0 text-[var(--accent-primary)]" />;
    case "entity":
      return <Database size={12} class="shrink-0 text-[var(--accent-primary)]" />;
    case "snapshot":
      return <FileCode2 size={12} class="shrink-0 text-[var(--accent-primary)]" />;
    default:
      return <Layers size={12} class="shrink-0 text-[var(--accent-primary)]" />;
  }
}

function ContextItemDetailRow(props: {
  item: ContextItem;
  index: number;
  total: number;
}) {
  const [copied, setCopied] = createSignal(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  onCleanup(() => {
    if (timer !== undefined) clearTimeout(timer);
  });

  const rawBody = createMemo(() => getContextItemBodyText(props.item));
  const formatted = createMemo(() => prettyFormatContextBody(rawBody()));
  const resolveLabel = createMemo(() => {
    const r = props.item.ref?.resolve;
    if (!r) return "";
    if (r.mcpTool) return r.mcpTool;
    if (r.servicePath) return r.servicePath;
    return "";
  });

  const onCopy = async (ev: MouseEvent) => {
    ev.stopPropagation();
    const text = rawBody() || props.item.subtitle || props.item.title || "";
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      if (timer !== undefined) clearTimeout(timer);
      timer = setTimeout(() => {
        timer = undefined;
        setCopied(false);
      }, 1400);
    } catch {
      /* ignore */
    }
  };

  return (
    <div class="px-2.5 py-2 space-y-1.5">
      <div class="flex items-start gap-1.5 min-w-0">
        <Show when={props.total > 1}>
          <span class="mt-0.5 inline-flex h-4 min-w-4 shrink-0 items-center justify-center rounded bg-[var(--bg-inset)] px-1 font-mono text-[10px] font-semibold text-[var(--text-muted)]">
            {props.index + 1}
          </span>
        </Show>
        <div class="min-w-0 flex-1 space-y-0.5">
          <div class="flex flex-wrap items-center gap-1.5 min-w-0">
            <Show when={props.item.title}>
              <span class="text-[12px] font-medium text-[var(--text-primary)] break-words">
                {props.item.title}
              </span>
            </Show>
            <Show when={props.item.badge}>
              <span class="inline-flex items-center rounded-md border border-[var(--accent-border)]/70 bg-[var(--accent-surface)]/70 px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-text)]">
                {props.item.badge}
              </span>
            </Show>
            <Show when={props.item.ref?.id}>
              <span class="inline-flex items-center rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                {props.item.entityType ? `${props.item.entityType}:` : "#"}
                {props.item.ref!.id}
              </span>
            </Show>
          </div>
          <Show when={props.item.subtitle}>
            <div class="text-[11.5px] leading-snug text-[var(--text-secondary)] break-words">
              {props.item.subtitle}
            </div>
          </Show>
          <Show when={resolveLabel()}>
            <div class="inline-flex items-center gap-1 font-mono text-[10px] text-[var(--text-faint)]">
              <Link2 size={10} class="shrink-0" />
              <span>
                {t("timeline.contextResolveHint")}: {resolveLabel()}
              </span>
            </div>
          </Show>
        </div>
        <Show when={rawBody() || props.item.subtitle}>
          <button
            type="button"
            onClick={(ev) => void onCopy(ev)}
            title={t("timeline.contextCopyItem")}
            class="shrink-0 rounded p-1 text-[var(--text-faint)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Show when={copied()} fallback={<Copy size={11} />}>
              <Check size={11} class="text-[var(--ok-text)]" />
            </Show>
          </button>
        </Show>
      </div>
      <Show when={rawBody()}>
        <div
          class={`max-h-48 overflow-auto rounded-lg border border-[var(--border-subtle)]/70 bg-[var(--bg-inset)]/75 px-2.5 py-1.5 selectable-text ${
            formatted().isJson
              ? "font-mono text-[11px] leading-relaxed text-[var(--text-secondary)] whitespace-pre"
              : "text-[12px] leading-relaxed text-[var(--text-secondary)] whitespace-pre-wrap break-words"
          }`}
        >
          {formatted().text}
        </div>
      </Show>
    </div>
  );
}

function SingleQuoteEnvelopeCard(props: {
  envelope: ContextEnvelope;
  stateKey: string;
  forceExpand?: boolean;
}) {
  const item = () => props.envelope.items[0];
  const body = createMemo(() => getContextItemBodyText(item()));
  const expandable = createMemo(() => isLongQuote(body()));
  const [open, setOpenSignal] = createSignal(expandedContextBlocks.has(props.stateKey));
  const isOpen = createMemo(() => open() || Boolean(props.forceExpand));
  const sourceLabel = createMemo(
    () => props.envelope.source?.appName || props.envelope.source?.appId || ""
  );

  const toggle = () => {
    const next = !open();
    rememberExpanded(props.stateKey, next);
    setOpenSignal(next);
  };

  return (
    <div
      data-context-kind="quote"
      class="rounded-r-xl rounded-l-sm border border-[var(--border-subtle)]/80 border-l-2 border-l-[var(--accent-primary)] bg-[var(--bg-panel)]/80 px-2.5 py-1.5 space-y-1"
    >
      <div class="flex items-center gap-1.5 text-[11px] text-[var(--text-muted)]">
        <Quote size={11} class="shrink-0 text-[var(--accent-primary)]" />
        <Show when={sourceLabel()}>
          <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-medium text-[var(--text-secondary)]">
            {sourceLabel()}
          </span>
        </Show>
        <span class="truncate font-medium text-[var(--text-secondary)]">
          {item().title || t("timeline.contextQuotedLabel")}
        </span>
        <Show when={expandable() && !props.forceExpand}>
          <button
            type="button"
            onClick={toggle}
            class="ml-auto inline-flex shrink-0 items-center gap-0.5 rounded px-1 py-0.5 text-[11px] font-medium text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer select-none"
          >
            <span>{isOpen() ? t("timeline.contextCollapseBtn") : t("timeline.contextExpandBtn")}</span>
            <ChevronDown
              size={11}
              class={`transition-transform duration-150 ${isOpen() ? "rotate-180" : ""}`}
            />
          </button>
        </Show>
      </div>
      <Show when={body()}>
        <div
          class={`whitespace-pre-wrap break-words text-[12px] leading-[1.55] text-[var(--text-secondary)] selectable-text ${
            !isOpen() && expandable() ? "line-clamp-2" : "max-h-64 overflow-y-auto"
          }`}
        >
          {body()}
        </div>
      </Show>
    </div>
  );
}

function StructuredEnvelopeCard(props: {
  envelope: ContextEnvelope;
  stateKey: string;
  forceExpand?: boolean;
}) {
  const [open, setOpenSignal] = createSignal(expandedContextBlocks.has(props.stateKey));
  const isOpen = createMemo(() => open() || Boolean(props.forceExpand));

  const toggle = () => {
    const next = !open();
    rememberExpanded(props.stateKey, next);
    setOpenSignal(next);
  };

  const count = () => props.envelope.items.length;
  const first = () => props.envelope.items[0];
  const sourceLabel = createMemo(
    () => props.envelope.source?.appName || props.envelope.source?.appId || ""
  );
  const sizeLabel = createMemo(() => {
    const chars = estimateContextEnvelopeChars(props.envelope);
    return chars > 0 ? formatContextSizeLabel(chars) : "";
  });

  const headline = createMemo(() => {
    const items = props.envelope.items;
    if (items.length === 1) {
      return items[0].title || deriveContextItemPreview(items[0], 56) || t("timeline.contextReferencedSingle");
    }
    const sampleTitles = items
      .slice(0, 2)
      .map((it) => it.title || it.ref?.id || deriveContextItemPreview(it, 20))
      .filter(Boolean);
    const base = t("timeline.contextReferencedItems", { count: items.length });
    return sampleTitles.length > 0 ? `${base} · ${sampleTitles.join(" / ")}${items.length > 2 ? "…" : ""}` : base;
  });

  const singleSubtitle = createMemo(() => {
    if (count() !== 1) return "";
    const item = first();
    return item.subtitle || (item.title ? deriveContextItemPreview(item, 72) : "");
  });

  return (
    <div
      data-context-kind={count() === 1 ? first().kind : "batch"}
      class="overflow-hidden rounded-xl border border-[var(--border-subtle)]/90 bg-[var(--bg-panel)]/85"
    >
      <button
        type="button"
        onClick={toggle}
        aria-expanded={isOpen()}
        class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left transition-colors hover:bg-[var(--bg-hover)]/65 cursor-pointer select-none"
      >
        <ContextKindIcon item={first()} count={count()} />
        <Show when={sourceLabel()}>
          <span class="shrink-0 rounded bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10.5px] font-medium text-[var(--text-secondary)]">
            {sourceLabel()}
          </span>
        </Show>
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-1.5 min-w-0">
            <span class="truncate text-[12px] font-medium text-[var(--text-primary)]">
              {headline()}
            </span>
            <Show when={count() === 1 && first().badge}>
              <span class="shrink-0 rounded border border-[var(--accent-border)]/70 bg-[var(--accent-surface)]/70 px-1.5 py-0.5 text-[10px] font-medium text-[var(--accent-text)]">
                {first().badge}
              </span>
            </Show>
          </div>
          <Show when={!isOpen() && singleSubtitle()}>
            <div class="truncate text-[11px] text-[var(--text-muted)]">
              {singleSubtitle()}
            </div>
          </Show>
        </div>
        <Show when={sizeLabel()}>
          <span class="shrink-0 font-mono text-[10.5px] text-[var(--text-faint)]">
            {sizeLabel()}
          </span>
        </Show>
        <ChevronDown
          size={12}
          class={`shrink-0 text-[var(--text-muted)] transition-transform duration-150 ${
            isOpen() ? "rotate-180" : ""
          }`}
        />
      </button>

      <Show when={isOpen()}>
        <div class="max-h-72 overflow-y-auto border-t border-[var(--border-subtle)]/75 divide-y divide-[var(--border-subtle)]/60">
          <For each={props.envelope.items}>
            {(item, idx) => (
              <ContextItemDetailRow item={item} index={idx()} total={count()} />
            )}
          </For>
        </div>
      </Show>
    </div>
  );
}

export function UserContextBlocks(props: {
  envelopes: ContextEnvelope[];
  partId: string;
  forceExpand?: boolean;
}) {
  return (
    <Show when={props.envelopes.length > 0}>
      <div class="space-y-1.5 mb-1.5" data-hoxi-context-blocks>
        <For each={props.envelopes}>
          {(env, idx) => {
            const key = () => `${props.partId}:ctx:${idx()}`;
            const isSingleQuote = () =>
              env.items.length === 1 && env.items[0].kind === "quote";
            return (
              <Show
                when={isSingleQuote()}
                fallback={
                  <StructuredEnvelopeCard
                    envelope={env}
                    stateKey={key()}
                    forceExpand={props.forceExpand}
                  />
                }
              >
                <SingleQuoteEnvelopeCard
                  envelope={env}
                  stateKey={key()}
                  forceExpand={props.forceExpand}
                />
              </Show>
            );
          }}
        </For>
      </div>
    </Show>
  );
}
