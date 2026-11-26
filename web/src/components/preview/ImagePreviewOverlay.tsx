// web/src/components/preview/ImagePreviewOverlay.tsx
import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { Portal } from "solid-js/web";
import { Check, Copy, Download, ExternalLink, Maximize2, X, ZoomIn, ZoomOut } from "lucide-solid";
import { Presence } from "../motion/Presence";
import {
  attachmentContextMenu,
  closeAttachmentContextMenu,
  closeImagePreview,
  copyImageSourceToClipboard,
  downloadAttachmentTarget,
  imageViewer,
  openAttachmentContextMenu,
  showAttachmentActionFeedback,
} from "../../store/workspace";
import { t } from "../../i18n";

const MIN_SCALE = 0.25;
const MAX_SCALE = 8;

function clampScale(value: number): number {
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, value));
}

export default function ImagePreviewOverlay() {
  const [scale, setScale] = createSignal(1);
  const [offset, setOffset] = createSignal({ x: 0, y: 0 });
  const [dragging, setDragging] = createSignal(false);
  const [copied, setCopied] = createSignal(false);
  let dragOrigin = { x: 0, y: 0 };
  let offsetOrigin = { x: 0, y: 0 };
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  const reset = () => {
    setScale(1);
    setOffset({ x: 0, y: 0 });
    setCopied(false);
  };

  createEffect(() => {
    if (imageViewer()) reset();
  });

  onCleanup(() => {
    if (copyTimer !== undefined) clearTimeout(copyTimer);
  });

  const zoomBy = (factor: number) =>
    setScale((prev) => clampScale(prev * factor));

  const handleCopyImage = () => {
    const payload = imageViewer();
    if (!payload?.src) return;
    void copyImageSourceToClipboard(payload.src).then((ok) => {
      if (ok) {
        setCopied(true);
        if (copyTimer !== undefined) clearTimeout(copyTimer);
        copyTimer = setTimeout(() => {
          copyTimer = undefined;
          setCopied(false);
        }, 1800);
        showAttachmentActionFeedback(t("timeline.copiedImage"), "ok");
      } else {
        showAttachmentActionFeedback(t("timeline.copyImageFailed"), "error");
      }
    });
  };

  const handleDownloadImage = () => {
    const payload = imageViewer();
    if (!payload?.src) return;
    void downloadAttachmentTarget({
      kind: "image",
      src: payload.src,
      filename: payload.filename || payload.alt,
      localPath: payload.localPath,
      downloadUrl: payload.downloadUrl,
    }).then((ok) => {
      if (!ok) {
        showAttachmentActionFeedback(t("timeline.downloadFailed"), "error");
      }
    });
  };

  const handleContextMenu = (ev: MouseEvent) => {
    const payload = imageViewer();
    if (!payload?.src) return;
    openAttachmentContextMenu(ev, {
      kind: "image",
      src: payload.src,
      filename: payload.filename || payload.alt,
      localPath: payload.localPath,
      downloadUrl: payload.downloadUrl,
      onOpenOriginal: payload.onOpenOriginal
        ? () => {
            payload.onOpenOriginal?.();
            closeImagePreview();
          }
        : undefined,
      originalLabel: payload.originalLabel,
    });
  };

  const handleWheel = (ev: WheelEvent) => {
    ev.preventDefault();
    const factor = ev.deltaY < 0 ? 1.12 : 1 / 1.12;
    zoomBy(factor);
  };

  const handlePointerDown = (ev: PointerEvent) => {
    if (ev.button !== 0) return;
    setDragging(true);
    dragOrigin = { x: ev.clientX, y: ev.clientY };
    offsetOrigin = offset();
    (ev.currentTarget as HTMLElement).setPointerCapture?.(ev.pointerId);
  };

  const handlePointerMove = (ev: PointerEvent) => {
    if (!dragging()) return;
    setOffset({
      x: offsetOrigin.x + (ev.clientX - dragOrigin.x),
      y: offsetOrigin.y + (ev.clientY - dragOrigin.y),
    });
  };

  const handlePointerUp = () => setDragging(false);

  const handleKeyDown = (ev: KeyboardEvent) => {
    if (!imageViewer()) return;
    if (ev.key === "Escape") {
      ev.preventDefault();
      ev.stopImmediatePropagation();
      if (attachmentContextMenu()) {
        closeAttachmentContextMenu();
      } else {
        closeImagePreview();
      }
      return;
    }
    if ((ev.metaKey || ev.ctrlKey) && !ev.altKey && ev.key.toLowerCase() === "c") {
      const sel = typeof window !== "undefined" ? window.getSelection()?.toString() : "";
      if (!sel) {
        ev.preventDefault();
        handleCopyImage();
      }
      return;
    }
    if (ev.key === "+" || ev.key === "=") {
      ev.preventDefault();
      zoomBy(1.25);
    } else if (ev.key === "-" || ev.key === "_") {
      ev.preventDefault();
      zoomBy(1 / 1.25);
    } else if (ev.key === "0") {
      ev.preventDefault();
      reset();
    }
  };

  const listenKeys = (ev: KeyboardEvent) => handleKeyDown(ev);
  window.addEventListener("keydown", listenKeys, { capture: true });
  onCleanup(() =>
    window.removeEventListener("keydown", listenKeys, { capture: true }),
  );

  return (
    <Presence when={imageViewer() !== null}>
      {(state) => {
        const payload = () => imageViewer();
        return (
          <Portal>
            <div
              data-state={state()}
              class="fixed inset-0 z-[120] flex items-center justify-center bg-black/55 p-6 backdrop-blur-[2px] select-none"
              onClick={(ev) => {
                if (ev.target === ev.currentTarget) closeImagePreview();
              }}
            >
              <div
                class="flex max-h-[86vh] max-w-[92vw] min-w-[260px] flex-col overflow-hidden rounded-xl border border-white/10 bg-[#141418] shadow-2xl"
                onWheel={handleWheel}
              >
                <div class="flex shrink-0 items-center justify-between gap-3 border-b border-white/5 px-3.5 py-2 text-white/80">
                  <div class="min-w-0 truncate text-[12px] font-medium text-white/60">
                    {payload()?.filename || payload()?.alt || ""}
                  </div>
                  <div class="flex shrink-0 items-center gap-0.5">
                    <Show when={payload()?.onOpenOriginal}>
                      <button
                        type="button"
                        onClick={() => {
                          payload()?.onOpenOriginal?.();
                          closeImagePreview();
                        }}
                        class="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[12px] text-white/80 hover:bg-white/10 hover:text-white cursor-pointer"
                      >
                        <ExternalLink size={13} />
                        <span>
                          {payload()?.originalLabel ||
                            t("timeline.openInInspectorTooltip")}
                        </span>
                      </button>
                    </Show>
                    <button
                      type="button"
                      onClick={handleCopyImage}
                      aria-label={copied() ? t("timeline.copiedImage") : t("timeline.copyImage")}
                      title={copied() ? t("timeline.copiedImage") : t("timeline.copyImage")}
                      class="rounded-lg p-2 text-white/80 hover:bg-white/10 hover:text-white cursor-pointer"
                    >
                      <Show when={copied()} fallback={<Copy size={15} />}>
                        <Check size={15} class="text-emerald-400" />
                      </Show>
                    </button>
                    <button
                      type="button"
                      onClick={handleDownloadImage}
                      aria-label={t("timeline.downloadImage")}
                      title={t("timeline.downloadImage")}
                      class="rounded-lg p-2 text-white/80 hover:bg-white/10 hover:text-white cursor-pointer"
                    >
                      <Download size={15} />
                    </button>
                    <button
                      type="button"
                      onClick={() => zoomBy(1 / 1.25)}
                      aria-label={t("timeline.zoomOut")}
                      title={t("timeline.zoomOut")}
                      class="rounded-lg p-2 text-white/80 hover:bg-white/10 hover:text-white cursor-pointer"
                    >
                      <ZoomOut size={16} />
                    </button>
                    <button
                      type="button"
                      onClick={() => zoomBy(1.25)}
                      aria-label={t("timeline.zoomIn")}
                      title={t("timeline.zoomIn")}
                      class="rounded-lg p-2 text-white/80 hover:bg-white/10 hover:text-white cursor-pointer"
                    >
                      <ZoomIn size={16} />
                    </button>
                    <button
                      type="button"
                      onClick={reset}
                      aria-label={t("timeline.resetZoom")}
                      title={t("timeline.resetZoom")}
                      class="rounded-lg p-2 text-white/80 hover:bg-white/10 hover:text-white cursor-pointer"
                    >
                      <Maximize2 size={15} />
                    </button>
                    <button
                      type="button"
                      onClick={() => closeImagePreview()}
                      aria-label={t("common.close")}
                      title={t("common.close")}
                      class="ml-1 rounded-lg p-2 text-white/80 hover:bg-white/10 hover:text-white cursor-pointer"
                    >
                      <X size={17} />
                    </button>
                  </div>
                </div>
                <div
                  class="flex min-h-[120px] flex-1 items-center justify-center overflow-hidden bg-black/30 p-3"
                  onPointerDown={handlePointerDown}
                  onPointerMove={handlePointerMove}
                  onPointerUp={handlePointerUp}
                  onPointerCancel={handlePointerUp}
                  onContextMenu={handleContextMenu}
                  classList={{
                    "cursor-grab": scale() > 1,
                    "cursor-grabbing": dragging(),
                  }}
                >
                  <Show when={payload()}>
                    {(img) => (
                      <img
                        src={img().src}
                        alt={img().alt}
                        draggable={false}
                        class="max-h-[70vh] max-w-full object-contain"
                        style={{
                          transform: `translate3d(${offset().x}px, ${offset().y}px, 0) scale(${scale()})`,
                          transition: dragging()
                            ? "none"
                            : "transform 120ms ease-out",
                          "will-change": "transform",
                        }}
                      />
                    )}
                  </Show>
                </div>
                <div class="shrink-0 pb-2 pt-1 text-center font-mono text-[11px] text-white/40">
                  {Math.round(scale() * 100)}%
                </div>
              </div>
            </div>
          </Portal>
        );
      }}
    </Presence>
  );
}
