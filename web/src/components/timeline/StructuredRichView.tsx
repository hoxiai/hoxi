// web/src/components/timeline/StructuredRichView.tsx
// Universal renderer for `hoxi.ui/v1` structured rich views (KPI metrics strip,
// responsive entity cards grid/list, media gallery, links, and QR receipts).
// Automatically adapts to both wide chat streams and narrow embedded docks,
// and supports 1-click / multi-select reverse handoff into `hoxi-context v1`.
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  Check,
  ChevronDown,
  ChevronUp,
  ExternalLink,
  LayoutGrid,
  List,
  MessageSquarePlus,
  X,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  appSettings,
  isAnyPortalMode,
  openExternalURL,
  openImagePreview,
  openURLInInspector,
  type RichEntityItem,
  type RichLinkAction,
  type RichMetricItem,
  type RichTone,
  type StructuredRichViewSpec,
} from "../../store/workspace";
import {
  buildContextEnvelopeFromEntities,
  buildContextEnvelopeFromMetric,
  isSafePortalImageSrc,
} from "./rich-view-spec";
import MediaGalleryView from "./MediaGalleryView";
import { useMarkdownPortalMode } from "./stream-reveal";

const INITIAL_VISIBLE_ENTITIES = 6;

function badgeClass(tone?: RichTone): string {
  switch (tone) {
    case "ok":
      return "border-[var(--ok-border)] bg-[var(--ok-surface)]/80 text-[var(--ok-text)]";
    case "warn":
      return "border-[var(--warn-border)] bg-[var(--warn-surface)]/80 text-[var(--warn-text)]";
    case "danger":
      return "border-[var(--danger-border)] bg-[var(--danger-surface)]/80 text-[var(--danger-text)]";
    case "accent":
      return "border-[var(--accent-border)] bg-[var(--accent-surface)]/80 text-[var(--accent-primary)]";
    default:
      return "border-[var(--border-subtle)] bg-[var(--bg-inset)] text-[var(--text-secondary)]";
  }
}

function metricValueClass(tone?: RichTone): string {
  switch (tone) {
    case "ok":
      return "text-[var(--ok-text)]";
    case "warn":
      return "text-[var(--warn-text)]";
    case "danger":
      return "text-[var(--danger-text)]";
    case "accent":
      return "text-[var(--accent-primary)]";
    default:
      return "text-[var(--text-primary)]";
  }
}

function openSafeLink(url: string, forceExternal?: boolean, portalMode?: boolean) {
  if (portalMode || isAnyPortalMode() || forceExternal || appSettings.linkOpenTarget === "external") {
    openExternalURL(url);
  } else {
    openURLInInspector(url);
  }
}

function EntityThumbnail(props: { src: string; alt: string; compact?: boolean }) {
  const [broken, setBroken] = createSignal(false);
  return (
    <Show when={!broken() && props.src}>
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          openImagePreview({ src: props.src, alt: props.alt });
        }}
        title={t("timeline.previewImageTooltip")}
        class={`shrink-0 overflow-hidden rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] cursor-zoom-in hover:border-[var(--accent-border)] transition-colors ${
          props.compact ? "h-12 w-12" : "h-14 w-14"
        }`}
      >
        <img
          src={props.src}
          alt={props.alt}
          loading="lazy"
          decoding="async"
          referrerpolicy="no-referrer"
          onError={() => setBroken(true)}
          class="h-full w-full object-cover"
        />
      </button>
    </Show>
  );
}

function EntityCard(props: {
  item: RichEntityItem;
  layout: "grid" | "list";
  selected: boolean;
  selectable: boolean;
  portal?: boolean;
  onToggleSelect: () => void;
  onQuickReference: () => void;
}) {
  const isList = () => props.layout === "list";
  const safeThumb = () => {
    const src = props.item.thumbnailUrl;
    if (!src) return undefined;
    if (props.portal && !isSafePortalImageSrc(src)) return undefined;
    return src;
  };

  return (
    <div
      data-hoxi-entity-card="true"
      data-selected={props.selected ? "true" : "false"}
      class={`group/entity relative flex flex-col justify-between gap-2 rounded-xl border p-2.5 transition-colors bg-[var(--bg-panel)]/85 ${
        props.selected
          ? "border-[var(--accent-primary)] bg-[var(--accent-surface)]/25"
          : "border-[var(--border-subtle)] hover:border-[var(--accent-border)]"
      }`}
    >
      <div class="flex items-start gap-2.5 min-w-0">
        <Show when={props.selectable}>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              props.onToggleSelect();
            }}
            aria-pressed={props.selected}
            class={`mt-0.5 inline-flex h-4 w-4 shrink-0 items-center justify-center rounded border transition-colors cursor-pointer ${
              props.selected
                ? "border-[var(--accent-primary)] bg-[var(--accent-primary)] text-[var(--accent-foreground)]"
                : "border-[var(--border-strong)] bg-[var(--bg-inset)] text-transparent hover:border-[var(--accent-primary)]"
            }`}
          >
            <Show when={props.selected}>
              <Check size={10} strokeWidth={3} />
            </Show>
          </button>
        </Show>

        <Show when={safeThumb()}>
          {(src) => (
            <EntityThumbnail src={src()} alt={props.item.title} compact={isList()} />
          )}
        </Show>

        <div class="min-w-0 flex-1 space-y-1">
          <div class="flex items-start justify-between gap-1.5 min-w-0">
            <span
              class="text-[12.5px] font-semibold leading-snug text-[var(--text-primary)] line-clamp-2 break-words"
              title={props.item.title}
            >
              {props.item.title}
            </span>
            <Show when={props.item.badge}>
              <span
                class={`shrink-0 rounded-md border px-1.5 py-0.5 text-[10px] font-medium leading-tight ${badgeClass(
                  props.item.badgeTone
                )}`}
              >
                {props.item.badge}
              </span>
            </Show>
          </div>

          <Show when={props.item.subtitle}>
            <div class="text-[11.5px] leading-snug text-[var(--text-secondary)] line-clamp-2 break-words">
              {props.item.subtitle}
            </div>
          </Show>
        </div>
      </div>

      <Show when={props.item.metrics && props.item.metrics.length > 0}>
        <div
          class={`grid gap-1.5 ${
            isList() ? "grid-cols-2 sm:grid-cols-4" : "grid-cols-2"
          }`}
        >
          <For each={props.item.metrics}>
            {(m) => (
              <div class="min-w-0 rounded-lg bg-[var(--bg-inset)]/80 px-2 py-1">
                <div class="truncate text-[10px] text-[var(--text-muted)]">{m.label}</div>
                <div
                  class={`truncate font-mono text-[11.5px] font-semibold tabular-nums ${metricValueClass(
                    m.tone
                  )}`}
                  title={m.value}
                >
                  {m.value}
                </div>
              </div>
            )}
          </For>
        </div>
      </Show>

      <Show
        when={
          (props.item.tags && props.item.tags.length > 0) ||
          props.item.url ||
          props.selectable
        }
      >
        <div class="flex flex-wrap items-center justify-between gap-1.5 pt-0.5 border-t border-[var(--border-subtle)]/55">
          <div class="flex flex-wrap items-center gap-1 min-w-0">
            <For each={props.item.tags || []}>
              {(tag) => (
                <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 text-[10px] text-[var(--text-muted)]">
                  {tag}
                </span>
              )}
            </For>
          </div>

          <div class="flex items-center gap-1 ml-auto shrink-0">
            <Show when={props.item.url}>
              {(url) => (
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    openSafeLink(url(), false, props.portal);
                  }}
                  class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10.5px] font-medium text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--accent-primary)] transition-colors cursor-pointer"
                >
                  <ExternalLink size={10.5} />
                  <span>{props.item.urlLabel || t("timeline.richCardsOpenLink")}</span>
                </button>
              )}
            </Show>
            <Show when={props.selectable}>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  props.onQuickReference();
                }}
                title={t("timeline.gallerySendToChatBtn")}
                class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10.5px] font-medium text-[var(--text-muted)] hover:bg-[var(--accent-surface)] hover:text-[var(--accent-primary)] transition-colors cursor-pointer"
              >
                <MessageSquarePlus size={10.5} />
                <span>{t("timeline.gallerySendToChatBtn")}</span>
              </button>
            </Show>
          </div>
        </div>
      </Show>
    </div>
  );
}

export default function StructuredRichView(props: {
  spec: StructuredRichViewSpec;
  compact?: boolean;
  hideHeader?: boolean;
  portal?: boolean;
  ref?: (el: HTMLDivElement) => void;
}) {
  const [selectedIdx, setSelectedIdx] = createSignal<number[]>([]);
  const [layoutOverride, setLayoutOverride] = createSignal<"grid" | "list" | null>(null);
  const [showAll, setShowAll] = createSignal(false);

  const markdownPortal = useMarkdownPortalMode();
  const isPortal = () => Boolean(props.portal) || isAnyPortalMode() || markdownPortal();
  const selectable = () => !isPortal();
  const entities = () => props.spec.entities || [];
  const metrics = () => props.spec.metrics || [];
  const gallery = () => {
    const raw = props.spec.gallery || [];
    if (!isPortal()) return raw;
    return raw
      .filter((img) => isSafePortalImageSrc(img.src))
      .map((img) =>
        img.downloadUrl && !isSafePortalImageSrc(img.downloadUrl)
          ? { ...img, downloadUrl: undefined }
          : img
      );
  };
  const safeQrImage = () => {
    const qr = props.spec.qrImage;
    if (!qr) return undefined;
    if (isPortal() && !isSafePortalImageSrc(qr.src)) return undefined;
    return qr;
  };
  const links = () => props.spec.links || [];
  const effectiveLayout = (): "grid" | "list" =>
    layoutOverride() ?? (props.compact ? "list" : props.spec.layout || "grid");

  const visibleEntities = createMemo(() => {
    const list = entities();
    if (showAll() || list.length <= INITIAL_VISIBLE_ENTITIES) return list;
    return list.slice(0, INITIAL_VISIBLE_ENTITIES);
  });

  const toggleEntity = (idx: number) => {
    setSelectedIdx((prev) =>
      prev.includes(idx) ? prev.filter((i) => i !== idx) : [...prev, idx].sort((a, b) => a - b)
    );
  };

  const dispatchContext = (items: RichEntityItem[]) => {
    if (items.length === 0) return;
    const envelope = buildContextEnvelopeFromEntities(items, props.spec.source);
    window.dispatchEvent(
      new CustomEvent("hoxi:composer-fill", {
        detail: {
          contexts: [envelope],
          appendContexts: true,
        },
      })
    );
  };

  const handleSendSelectedEntities = () => {
    const picked = selectedIdx()
      .map((idx) => entities()[idx])
      .filter((item): item is RichEntityItem => Boolean(item));
    if (picked.length === 0) return;
    dispatchContext(picked);
    setSelectedIdx([]);
  };

  const handleAskMetric = (metric: RichMetricItem) => {
    if (!selectable()) return;
    const envelope = buildContextEnvelopeFromMetric(metric, props.spec.title, props.spec.source);
    window.dispatchEvent(
      new CustomEvent("hoxi:composer-fill", {
        detail: {
          contexts: [envelope],
          appendContexts: true,
        },
      })
    );
  };

  const showHeader = () =>
    selectedIdx().length > 0 ||
    (!props.hideHeader &&
      Boolean(
        props.spec.title ||
          props.spec.badge ||
          entities().length > 1
      ));

  return (
    <div
      ref={props.ref}
      data-hoxi-rich-view="true"
      class="overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)]/55"
    >
      <Show when={showHeader()}>
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--border-subtle)]/75 bg-[var(--bg-elevated)]/65 px-3 py-1.5">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-1.5 min-w-0">
              <Show when={props.spec.title}>
                <span class="truncate text-[12px] font-semibold text-[var(--text-primary)]">
                  {props.spec.title}
                </span>
              </Show>
              <Show when={props.spec.badge}>
                <span
                  class={`shrink-0 rounded-md border px-1.5 py-0.5 text-[10px] font-medium ${badgeClass(
                    props.spec.badgeTone
                  )}`}
                >
                  {props.spec.badge}
                </span>
              </Show>
            </div>
            <Show when={props.spec.subtitle}>
              <div class="truncate text-[11px] text-[var(--text-muted)]">
                {props.spec.subtitle}
              </div>
            </Show>
          </div>

          <div class="flex items-center gap-1.5 shrink-0">
            <Show when={selectedIdx().length > 0 && selectable()}>
              <button
                type="button"
                data-hoxi-rich-send-selected="true"
                onClick={handleSendSelectedEntities}
                class="inline-flex items-center gap-1 rounded-md bg-[var(--accent-primary)] px-2 py-0.5 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 transition-opacity cursor-pointer"
              >
                <MessageSquarePlus size={11} />
                <span>{t("timeline.richCardsSelectedAction", { count: selectedIdx().length })}</span>
              </button>
              <button
                type="button"
                onClick={() => setSelectedIdx([])}
                title={t("timeline.galleryClearSelectionBtn")}
                class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <X size={11} />
              </button>
            </Show>

            <Show when={entities().length > 1}>
              <div class="inline-flex items-center rounded-md bg-[var(--bg-inset)] p-0.5">
                <button
                  type="button"
                  onClick={() => setLayoutOverride("grid")}
                  title={t("timeline.richCardsViewCards")}
                  class={`inline-flex items-center rounded p-1 transition-colors cursor-pointer ${
                    effectiveLayout() === "grid"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <LayoutGrid size={11} />
                </button>
                <button
                  type="button"
                  onClick={() => setLayoutOverride("list")}
                  title={t("timeline.richCardsViewList")}
                  class={`inline-flex items-center rounded p-1 transition-colors cursor-pointer ${
                    effectiveLayout() === "list"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <List size={11} />
                </button>
              </div>
            </Show>
          </div>
        </div>
      </Show>

      <div class="space-y-2.5 p-2.5">
        {/* KPI Metric Strip */}
        <Show when={metrics().length > 0}>
          <div
            data-hoxi-metric-strip="true"
            class="grid grid-cols-[repeat(auto-fill,minmax(128px,1fr))] gap-2"
          >
            <For each={metrics()}>
              {(metric) => (
                <button
                  type="button"
                  disabled={!selectable()}
                  onClick={() => handleAskMetric(metric)}
                  title={selectable() ? t("timeline.richCardsAskMetricTooltip") : undefined}
                  class={`group/metric flex flex-col items-start rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/70 px-2.5 py-2 text-left transition-colors ${
                    selectable()
                      ? "hover:border-[var(--accent-border)] hover:bg-[var(--bg-hover)]/50 cursor-pointer"
                      : "cursor-default"
                  }`}
                >
                  <span class="w-full truncate text-[10.5px] text-[var(--text-muted)]">
                    {metric.label}
                  </span>
                  <div class="mt-0.5 flex w-full items-baseline justify-between gap-1.5">
                    <span
                      class={`truncate font-mono text-[15px] font-semibold tabular-nums leading-tight ${metricValueClass(
                        metric.tone
                      )}`}
                      title={metric.value}
                    >
                      {metric.value}
                    </span>
                    <Show when={metric.delta}>
                      <span
                        class={`shrink-0 rounded border px-1 py-0.5 font-mono text-[10px] font-medium leading-none ${badgeClass(
                          metric.tone
                        )}`}
                      >
                        {metric.delta}
                      </span>
                    </Show>
                  </div>
                  <Show when={metric.hint}>
                    <span class="mt-0.5 w-full truncate text-[10px] text-[var(--text-faint)]">
                      {metric.hint}
                    </span>
                  </Show>
                </button>
              )}
            </For>
          </div>
        </Show>

        {/* Entity Cards Grid / List */}
        <Show when={entities().length > 0}>
          <div class="space-y-2">
            <div
              data-hoxi-entity-grid="true"
              class={
                effectiveLayout() === "list"
                  ? "flex flex-col gap-2"
                  : "grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-2"
              }
            >
              <For each={visibleEntities()}>
                {(item, idx) => (
                  <EntityCard
                    item={item}
                    layout={effectiveLayout()}
                    selected={selectedIdx().includes(idx())}
                    selectable={selectable()}
                    portal={isPortal()}
                    onToggleSelect={() => toggleEntity(idx())}
                    onQuickReference={() => dispatchContext([item])}
                  />
                )}
              </For>
            </div>

            <Show when={entities().length > INITIAL_VISIBLE_ENTITIES}>
              <button
                type="button"
                onClick={() => setShowAll((v) => !v)}
                class="flex w-full items-center justify-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]/60 py-1.5 text-[11.5px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
              >
                <Show when={showAll()} fallback={<ChevronDown size={12} />}>
                  <ChevronUp size={12} />
                </Show>
                <span>
                  {showAll()
                    ? t("timeline.richCardsShowLess")
                    : t("timeline.richCardsShowAll", { count: entities().length })}
                </span>
              </button>
            </Show>
          </div>
        </Show>

        {/* Embedded Media Gallery */}
        <Show when={gallery().length > 0}>
          <MediaGalleryView
            images={gallery()}
            source={props.spec.source}
            compact={props.compact}
            portal={isPortal()}
          />
        </Show>

        {/* QR Receipt + Action Links */}
        <Show when={safeQrImage() || links().length > 0}>
          <div class="flex flex-wrap items-center gap-2.5 pt-1">
            <Show when={safeQrImage()}>
              {(qr) => (
                <button
                  type="button"
                  onClick={() =>
                    openImagePreview({
                      src: qr().src,
                      alt: qr().label || "QR",
                    })
                  }
                  class="inline-flex items-center gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)]/80 px-2 py-1.5 text-left hover:border-[var(--accent-border)] transition-colors cursor-zoom-in"
                >
                  <img
                    src={qr().src}
                    alt={qr().label || "QR"}
                    class="h-11 w-11 shrink-0 rounded bg-white p-0.5 object-contain"
                  />
                  <div class="min-w-0">
                    <Show when={qr().label}>
                      <div class="text-[11.5px] font-medium text-[var(--text-primary)]">
                        {qr().label}
                      </div>
                    </Show>
                    <Show when={qr().caption}>
                      <div class="text-[10.5px] text-[var(--text-muted)]">{qr().caption}</div>
                    </Show>
                  </div>
                </button>
              )}
            </Show>

            <Show when={links().length > 0}>
              <div class="flex flex-wrap items-center gap-1.5">
                <For each={links()}>
                  {(link: RichLinkAction) => (
                    <button
                      type="button"
                      onClick={() => openSafeLink(link.url, link.external, isPortal())}
                      class={`inline-flex items-center gap-1 rounded-lg border px-2.5 py-1 text-[11.5px] font-medium transition-colors hover:opacity-90 cursor-pointer ${badgeClass(
                        link.tone || "accent"
                      )}`}
                    >
                      <ExternalLink size={11} />
                      <span>{link.label}</span>
                    </button>
                  )}
                </For>
              </div>
            </Show>
          </div>
        </Show>

        <Show when={props.spec.note}>
          <div class="flex items-center justify-between gap-2 pt-0.5 text-[10.5px] text-[var(--text-faint)]">
            <span class="truncate">{props.spec.note}</span>
          </div>
        </Show>
      </div>
    </div>
  );
}
