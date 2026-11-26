// web/src/components/timeline/MediaGalleryView.tsx
// Responsive multi-image gallery with zoom preview, download, multi-select
// handoff to composer (`hoxi-context v1`), and 2-image A/B split comparison.
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { Check, Columns2, Download, Grid2x2, MessageSquarePlus, X } from "lucide-solid";
import { t } from "../../i18n";
import {
  activeDirectory,
  isAnyPortalMode,
  openFileInInspector,
  openImagePreview,
  workspace,
  type ContextSource,
  type RichGalleryImage,
  type RichTone,
} from "../../store/workspace";
import { getCachedMarkdownImage, loadMarkdownImage } from "../../store/markdown-images";
import { Icon } from "../icons";
import { resolveMarkdownRelativePath } from "./markdown-parser";
import { useMarkdownBaseFilePath } from "./stream-reveal";
import { buildContextEnvelopeFromImages } from "./rich-view-spec";

function isDirectImageSrc(src: string): boolean {
  const s = src.trim();
  return (
    /^https?:\/\//i.test(s) ||
    /^\/api\/hoxi\/(?:attachments|file\/content|[a-z0-9-]+\/portal\/attachments|[a-z0-9-]+\/[a-z0-9-]+\/[a-z0-9_-]+\/proxy\/attachments)/i.test(s) ||
    /^data:image\//i.test(s)
  );
}

function toneBadgeClass(tone?: RichTone): string {
  switch (tone) {
    case "ok":
      return "border-[var(--ok-border)] bg-[var(--ok-surface)] text-[var(--ok-text)]";
    case "warn":
      return "border-[var(--warn-border)] bg-[var(--warn-surface)] text-[var(--warn-text)]";
    case "danger":
      return "border-[var(--danger-border)] bg-[var(--danger-surface)] text-[var(--danger-text)]";
    case "accent":
      return "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]";
    default:
      return "border-[var(--border-subtle)] bg-[var(--bg-panel)]/90 text-[var(--text-secondary)]";
  }
}

function useResolvedGallerySrc(getRawSrc: () => string) {
  const baseFilePath = useMarkdownBaseFilePath();
  const [resolved, setResolved] = createSignal("");
  const [failed, setFailed] = createSignal(false);

  const localPath = createMemo(() => {
    const raw = (getRawSrc() || "").trim();
    if (!raw || isDirectImageSrc(raw)) return "";
    return resolveMarkdownRelativePath(raw, baseFilePath());
  });

  createEffect(() => {
    const raw = (getRawSrc() || "").trim();
    if (!raw) {
      setResolved("");
      setFailed(true);
      return;
    }
    if (isDirectImageSrc(raw)) {
      setResolved(raw);
      setFailed(false);
      return;
    }
    const path = localPath();
    const dir = activeDirectory() || workspace.rootDir || "";
    const sid = workspace.activeSessionID || undefined;
    if (!path || isAnyPortalMode() || (!workspace.connected && !dir)) {
      setResolved("");
      setFailed(true);
      return;
    }
    const cached = getCachedMarkdownImage(path, dir, sid);
    if (cached) {
      setResolved(cached);
      setFailed(false);
      return;
    }
    let cancelled = false;
    onCleanup(() => {
      cancelled = true;
    });
    setFailed(false);
    void loadMarkdownImage(path, dir, sid).then((url) => {
      if (cancelled) return;
      if (url) {
        setResolved(url);
        setFailed(false);
      } else {
        setResolved("");
        setFailed(true);
      }
    });
  });

  return { src: resolved, failed, setFailed, localPath };
}

function GalleryComparePanel(props: {
  left: RichGalleryImage;
  right: RichGalleryImage;
}) {
  const [splitPct, setSplitPct] = createSignal(50);
  const leftRes = useResolvedGallerySrc(() => props.left.src);
  const rightRes = useResolvedGallerySrc(() => props.right.src);

  const leftLabel = () => props.left.alt || props.left.caption || props.left.filename || "A";
  const rightLabel = () => props.right.alt || props.right.caption || props.right.filename || "B";

  return (
    <div data-hoxi-gallery-compare="true" class="p-2.5 space-y-2">
      <div class="relative mx-auto aspect-[4/3] max-h-[360px] w-full overflow-hidden rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] select-none">
        <Show when={rightRes.src()}>
          <img
            src={rightRes.src()}
            alt={rightLabel()}
            referrerpolicy="no-referrer"
            class="absolute inset-0 h-full w-full object-contain"
          />
        </Show>
        <Show when={leftRes.src()}>
          <div
            class="absolute inset-0 overflow-hidden"
            style={{ "clip-path": `inset(0 ${100 - splitPct()}% 0 0)` }}
          >
            <img
              src={leftRes.src()}
              alt={leftLabel()}
              referrerpolicy="no-referrer"
              class="h-full w-full object-contain"
            />
          </div>
        </Show>
        <div
          class="pointer-events-none absolute top-0 bottom-0 w-0.5 bg-[var(--accent-primary)] shadow"
          style={{ left: `${splitPct()}%` }}
        />
        <span class="pointer-events-none absolute left-2 top-2 max-w-[44%] truncate rounded-md bg-black/65 px-2 py-0.5 text-[11px] font-medium text-white backdrop-blur-xs">
          A · {leftLabel()}
        </span>
        <span class="pointer-events-none absolute right-2 top-2 max-w-[44%] truncate rounded-md bg-black/65 px-2 py-0.5 text-[11px] font-medium text-white backdrop-blur-xs">
          B · {rightLabel()}
        </span>
      </div>

      <div class="flex items-center gap-2.5 px-0.5 text-[11px] text-[var(--text-muted)]">
        <span class="shrink-0">{t("timeline.galleryCompareHint")}</span>
        <input
          type="range"
          min={5}
          max={95}
          value={splitPct()}
          aria-label={t("timeline.galleryCompareHint")}
          onInput={(e) => setSplitPct(Number(e.currentTarget.value))}
          class="flex-1 accent-[var(--accent-primary)] cursor-ew-resize"
        />
        <span class="w-9 shrink-0 text-right font-mono tabular-nums">{splitPct()}%</span>
      </div>
    </div>
  );
}

function GalleryTile(props: {
  image: RichGalleryImage;
  index: number;
  selected: boolean;
  selectable: boolean;
  onToggleSelect: () => void;
}) {
  const res = useResolvedGallerySrc(() => props.image.src);
  const label = () =>
    props.image.alt || props.image.caption || props.image.filename || `Image ${props.index + 1}`;
  const downloadHref = () => props.image.downloadUrl || res.src();

  const handleOpen = () => {
    const display = res.src();
    if (!display) return;
    const lp = res.localPath();
    if (lp && !isAnyPortalMode()) {
      openImagePreview({
        src: display,
        alt: label(),
        originalLabel: t("timeline.openInInspectorTooltip"),
        onOpenOriginal: () => openFileInInspector(lp),
      });
      return;
    }
    openImagePreview({ src: display, alt: label() });
  };

  return (
    <div
      data-hoxi-gallery-tile="true"
      data-selected={props.selected ? "true" : "false"}
      class={`group/tile relative flex flex-col overflow-hidden rounded-lg border transition-colors bg-[var(--bg-inset)] ${
        props.selected
          ? "border-[var(--accent-primary)] ring-1 ring-[var(--accent-primary)]/40"
          : "border-[var(--border-subtle)] hover:border-[var(--accent-border)]"
      }`}
    >
      <Show when={props.selectable}>
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            props.onToggleSelect();
          }}
          aria-pressed={props.selected}
          title={t("timeline.gallerySelectImageTooltip")}
          class={`absolute left-1.5 top-1.5 z-10 inline-flex h-5 w-5 items-center justify-center rounded-md border transition-all cursor-pointer ${
            props.selected
              ? "border-[var(--accent-primary)] bg-[var(--accent-primary)] text-[var(--accent-foreground)] opacity-100"
              : "border-white/50 bg-black/50 text-white opacity-0 group-hover/tile:opacity-100 focus-visible:opacity-100"
          }`}
        >
          <Show when={props.selected}>
            <Check size={11} strokeWidth={3} />
          </Show>
        </button>
      </Show>

      <Show when={props.image.badge}>
        <span
          class={`pointer-events-none absolute right-1.5 top-1.5 z-10 max-w-[65%] truncate rounded-md border px-1.5 py-0.5 text-[10px] font-medium backdrop-blur-xs ${toneBadgeClass(
            props.image.badgeTone
          )}`}
        >
          {props.image.badge}
        </span>
      </Show>

      <button
        type="button"
        onClick={handleOpen}
        title={t("timeline.previewImageTooltip")}
        class="relative flex aspect-square w-full items-center justify-center overflow-hidden bg-[var(--bg-code)]/50 cursor-zoom-in"
      >
        <Show
          when={!res.failed() && res.src()}
          fallback={
            <div class="flex flex-col items-center justify-center gap-1 p-3 text-[11px] text-[var(--text-muted)]">
              <Icon name="photo" size="small" />
              <span class="max-w-full truncate">{label()}</span>
            </div>
          }
        >
          <img
            src={res.src()}
            alt={label()}
            loading="lazy"
            decoding="async"
            referrerpolicy="no-referrer"
            onError={() => res.setFailed(true)}
            class="h-full w-full object-cover transition-transform duration-150 group-hover/tile:scale-[1.02]"
          />
        </Show>
      </button>

      <Show when={props.image.caption || props.image.alt || props.image.filename || downloadHref()}>
        <div class="flex items-center justify-between gap-1.5 border-t border-[var(--border-subtle)]/70 bg-[var(--bg-panel)]/85 px-2 py-1 text-[11px]">
          <span class="min-w-0 flex-1 truncate text-[var(--text-secondary)]" title={label()}>
            {props.image.caption || props.image.alt || props.image.filename || `#${props.index + 1}`}
          </span>
          <Show when={downloadHref()}>
            <a
              href={downloadHref()}
              download={props.image.filename || `image-${props.index + 1}.png`}
              onClick={(e) => e.stopPropagation()}
              title={t("timeline.downloadImage")}
              class="shrink-0 rounded p-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors"
            >
              <Download size={11} />
            </a>
          </Show>
        </div>
      </Show>
    </div>
  );
}

export default function MediaGalleryView(props: {
  images: RichGalleryImage[];
  title?: string;
  source?: ContextSource;
  compact?: boolean;
  ref?: (el: HTMLDivElement) => void;
}) {
  const [selectedIdx, setSelectedIdx] = createSignal<number[]>([]);
  const [compareOpen, setCompareOpen] = createSignal(false);

  const selectable = () => !isAnyPortalMode();

  const toggleSelect = (idx: number) => {
    setSelectedIdx((prev) =>
      prev.includes(idx) ? prev.filter((i) => i !== idx) : [...prev, idx].sort((a, b) => a - b)
    );
  };

  const comparePair = createMemo<[RichGalleryImage, RichGalleryImage] | null>(() => {
    const sel = selectedIdx();
    if (sel.length === 2) {
      const a = props.images[sel[0]];
      const b = props.images[sel[1]];
      return a && b ? [a, b] : null;
    }
    if (props.images.length === 2) {
      return [props.images[0], props.images[1]];
    }
    return null;
  });

  const handleSendSelected = () => {
    const sel = selectedIdx()
      .map((i) => props.images[i])
      .filter((img): img is RichGalleryImage => Boolean(img));
    if (sel.length === 0) return;
    const envelope = buildContextEnvelopeFromImages(sel, props.source);
    window.dispatchEvent(
      new CustomEvent("hoxi:composer-fill", {
        detail: {
          contexts: [envelope],
          appendContexts: true,
        },
      })
    );
    setSelectedIdx([]);
  };

  const gridColsClass = () =>
    props.compact || props.images.length === 2
      ? "grid-cols-2"
      : "grid-cols-[repeat(auto-fill,minmax(136px,1fr))]";

  return (
    <div
      ref={props.ref}
      data-hoxi-media-gallery="true"
      class="my-1.5 overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)]/55"
    >
      <div class="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--border-subtle)]/75 bg-[var(--bg-elevated)]/65 px-2.5 py-1.5 text-[11px]">
        <div class="flex items-center gap-1.5 min-w-0">
          <Icon name="photo" size="small" class="shrink-0 text-[var(--accent-primary)]" />
          <span class="truncate font-medium text-[var(--text-primary)]">
            {props.title || t("timeline.galleryCountLabel", { count: props.images.length })}
          </span>
          <Show when={props.title}>
            <span class="shrink-0 font-mono text-[10.5px] text-[var(--text-muted)]">
              ({props.images.length})
            </span>
          </Show>
        </div>

        <div class="flex items-center gap-1.5 shrink-0">
          <Show when={selectedIdx().length > 0 && selectable()}>
            <button
              type="button"
              onClick={handleSendSelected}
              class="inline-flex items-center gap-1 rounded-md bg-[var(--accent-primary)] px-2 py-0.5 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 transition-opacity cursor-pointer"
            >
              <MessageSquarePlus size={11} />
              <span>
                {t("timeline.gallerySendToChatBtn")} ({selectedIdx().length})
              </span>
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

          <Show when={comparePair()}>
            <button
              type="button"
              onClick={() => setCompareOpen((v) => !v)}
              class={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-medium transition-colors cursor-pointer ${
                compareOpen()
                  ? "border-[var(--accent-border)] bg-[var(--accent-surface)] text-[var(--accent-primary)]"
                  : "border-[var(--border-subtle)] bg-[var(--bg-panel)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
              }`}
            >
              <Show when={compareOpen()} fallback={<Columns2 size={11} />}>
                <Grid2x2 size={11} />
              </Show>
              <span>
                {compareOpen()
                  ? t("timeline.galleryExitCompareBtn")
                  : t("timeline.galleryCompareBtn")}
              </span>
            </button>
          </Show>
        </div>
      </div>

      <Show
        when={compareOpen() && comparePair()}
        fallback={
          <div class={`grid gap-2 p-2.5 ${gridColsClass()}`}>
            <For each={props.images}>
              {(img, idx) => (
                <GalleryTile
                  image={img}
                  index={idx()}
                  selected={selectedIdx().includes(idx())}
                  selectable={selectable()}
                  onToggleSelect={() => toggleSelect(idx())}
                />
              )}
            </For>
          </div>
        }
      >
        {(pair) => <GalleryComparePanel left={pair()[0]} right={pair()[1]} />}
      </Show>
    </div>
  );
}
