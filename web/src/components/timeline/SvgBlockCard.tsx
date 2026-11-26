// web/src/components/timeline/SvgBlockCard.tsx
// Inline sanitized SVG preview card for ```svg code blocks.
// Supports Preview/Source toggle, backdrop switcher (auto/light/dark/checker),
// lightbox zoom, side-canvas docking, and SVG/PNG download.
import { createMemo, createSignal, onCleanup, Show } from "solid-js";
import {
  Check,
  Code2,
  Contrast,
  Copy,
  Download,
  Eye,
  Grid2x2,
  ImageDown,
  LoaderCircle,
  Maximize2,
  Moon,
  PanelRightOpen,
  Sun,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  isAnyPortalMode,
  openCanvasPreview,
  openImagePreview,
  workspace,
} from "../../store/workspace";
import { useMarkdownPortalMode, useMarkdownStreaming } from "./stream-reveal";
import {
  downloadSvgAsPng,
  downloadSvgFile,
  hasCompleteSvgMarkup,
  sanitizeSvgMarkup,
  svgToDataURL,
} from "./svg-sanitizer";

type SvgBackdrop = "auto" | "light" | "dark" | "checker";

const CHECKER_STYLE = {
  "background-image":
    "linear-gradient(45deg, rgba(128,128,128,0.14) 25%, transparent 25%), linear-gradient(-45deg, rgba(128,128,128,0.14) 25%, transparent 25%), linear-gradient(45deg, transparent 75%, rgba(128,128,128,0.14) 75%), linear-gradient(-45deg, transparent 75%, rgba(128,128,128,0.14) 75%)",
  "background-size": "16px 16px",
  "background-position": "0 0, 0 8px, 8px -8px, -8px 0px",
};

export default function SvgBlockCard(props: {
  code: string;
  ref?: (el: HTMLDivElement) => void;
}) {
  const streaming = useMarkdownStreaming();
  const markdownPortal = useMarkdownPortalMode();
  const isPortal = () => isAnyPortalMode() || markdownPortal();
  const [viewMode, setViewMode] = createSignal<"preview" | "source">("preview");
  const [backdrop, setBackdrop] = createSignal<SvgBackdrop>("auto");
  const [copied, setCopied] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  onCleanup(() => {
    if (copyTimer) clearTimeout(copyTimer);
  });

  const complete = createMemo(() => hasCompleteSvgMarkup(props.code));
  const sanitized = createMemo(() => {
    if (!complete()) return { svg: "" };
    return sanitizeSvgMarkup(props.code);
  });
  const showPreview = () => viewMode() === "preview" && Boolean(sanitized().svg);
  const showError = () =>
    !streaming() && viewMode() === "preview" && !sanitized().svg && props.code.trim().length > 0;

  const backdropClass = () => {
    switch (backdrop()) {
      case "light":
        return "bg-white";
      case "dark":
        return "bg-[#141418]";
      case "checker":
        return "bg-[var(--bg-panel)]";
      default:
        return "bg-[var(--bg-panel)]/65";
    }
  };

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(props.code);
      setCopied(true);
      if (copyTimer) clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopied(false), 1600);
    } catch {
      /* ignore */
    }
  };

  const handleZoom = () => {
    const svg = sanitized().svg;
    if (!svg) return;
    openImagePreview({
      src: svgToDataURL(svg),
      alt: t("timeline.svgPreviewLabel"),
    });
  };

  const handleDock = () => {
    const svg = sanitized().svg;
    if (!svg) return;
    openCanvasPreview({
      kind: "svg",
      title: t("timeline.svgPreviewLabel"),
      content: svg,
    });
  };

  const handleDownloadPng = () => {
    const svg = sanitized().svg;
    if (!svg) return;
    const bg =
      backdrop() === "light"
        ? "#ffffff"
        : backdrop() === "dark"
          ? "#141418"
          : backdrop() === "checker"
            ? undefined
            : workspace.theme === "dark"
              ? "#141418"
              : "#ffffff";
    void downloadSvgAsPng(svg, "graphic.png", bg);
  };

  return (
    <div
      ref={props.ref}
      data-hoxi-svg-card="true"
      class="group/svg my-1.5 overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-code)]"
    >
      <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/70 px-3 py-1.5 text-[11px]">
        <div class="flex items-center gap-2 min-w-0">
          <span class="font-mono font-medium text-[var(--text-secondary)]">
            {t("timeline.svgPreviewLabel")}
          </span>
          <Show when={streaming() && !complete()}>
            <LoaderCircle size={12} class="animate-spin text-[var(--text-muted)]" />
          </Show>
        </div>

        <div class="flex items-center gap-1 shrink-0">
          <Show when={sanitized().svg}>
            <Show when={viewMode() === "preview"}>
              <div class="hidden sm:inline-flex items-center rounded-md bg-[var(--bg-inset)] p-0.5">
                <button
                  type="button"
                  onClick={() => setBackdrop("auto")}
                  title={t("timeline.svgBgAuto")}
                  class={`rounded p-0.5 cursor-pointer ${
                    backdrop() === "auto"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <Contrast size={11} />
                </button>
                <button
                  type="button"
                  onClick={() => setBackdrop("light")}
                  title={t("timeline.svgBgLight")}
                  class={`rounded p-0.5 cursor-pointer ${
                    backdrop() === "light"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <Sun size={11} />
                </button>
                <button
                  type="button"
                  onClick={() => setBackdrop("dark")}
                  title={t("timeline.svgBgDark")}
                  class={`rounded p-0.5 cursor-pointer ${
                    backdrop() === "dark"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <Moon size={11} />
                </button>
                <button
                  type="button"
                  onClick={() => setBackdrop("checker")}
                  title={t("timeline.svgBgChecker")}
                  class={`rounded p-0.5 cursor-pointer ${
                    backdrop() === "checker"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <Grid2x2 size={11} />
                </button>
              </div>
            </Show>

            <Show when={!isPortal()}>
              <div class="inline-flex items-center rounded-md bg-[var(--bg-inset)] p-0.5">
                <button
                  type="button"
                  onClick={() => setViewMode("preview")}
                  class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] transition-colors cursor-pointer ${
                    viewMode() === "preview"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <Eye size={11} />
                  <span>{t("inspector.files.previewMode")}</span>
                </button>
                <button
                  type="button"
                  onClick={() => setViewMode("source")}
                  class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] transition-colors cursor-pointer ${
                    viewMode() === "source"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <Code2 size={11} />
                  <span>{t("inspector.files.sourceMode")}</span>
                </button>
              </div>
            </Show>

            <button
              type="button"
              onClick={() => downloadSvgFile(sanitized().svg, "graphic.svg")}
              title={t("timeline.downloadSvg")}
              class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <Download size={12} />
            </button>
            <button
              type="button"
              onClick={handleDownloadPng}
              title={t("timeline.downloadPng")}
              class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <ImageDown size={12} />
            </button>
            <Show when={!isPortal()}>
              <button
                type="button"
                onClick={handleDock}
                title={t("timeline.dockHtmlPreview")}
                class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
              >
                <PanelRightOpen size={12} />
              </button>
            </Show>
            <button
              type="button"
              onClick={handleZoom}
              title={t("timeline.previewImageTooltip")}
              class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <Maximize2 size={12} />
            </button>
          </Show>

          <button
            type="button"
            onClick={() => void handleCopy()}
            title={copied() ? t("common.copied") : t("common.copy")}
            class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Show when={copied()} fallback={<Copy size={12} />}>
              <Check size={12} class="text-[var(--ok-text)]" />
            </Show>
          </button>
        </div>
      </div>

      <Show
        when={showPreview()}
        fallback={
          <div>
            <Show when={showError()}>
              <div class="border-b border-[var(--border-subtle)] bg-[var(--warn-surface)]/40 px-3 py-1.5 text-[11px] text-[var(--warn-text)]">
                {t("timeline.svgRenderFailed")}
              </div>
            </Show>
            <pre
              data-scrollable
              class="overflow-x-auto p-3.5 font-mono leading-[1.6] text-[var(--text-primary)] whitespace-pre selectable-text"
              style={{ "font-size": "var(--app-code-text-size)" }}
            >
              <code>{props.code}</code>
            </pre>
          </div>
        }
      >
        <div
          onClick={handleZoom}
          title={t("timeline.previewImageTooltip")}
          class={`flex max-h-[480px] min-h-[96px] items-center justify-center overflow-auto p-4 cursor-zoom-in ${backdropClass()}`}
          style={backdrop() === "checker" ? CHECKER_STYLE : undefined}
        >
          {/* Rendered via <img> data URL so SVG <style> and element IDs stay strictly isolated from Hoxi's DOM */}
          <img
            data-hoxi-svg-img="true"
            src={svgToDataURL(sanitized().svg)}
            alt={t("timeline.svgPreviewLabel")}
            class="h-auto max-h-[430px] max-w-full object-contain"
          />
        </div>
      </Show>
    </div>
  );
}
