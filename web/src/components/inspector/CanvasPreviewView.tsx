// web/src/components/inspector/CanvasPreviewView.tsx
// Docked side-by-side live preview tab inside InspectorPane for HTML & SVG artifacts
// with version history switching, Preview/Source toggle, viewport presets, and export.
import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import {
  Check,
  ChevronLeft,
  ChevronRight,
  Code2,
  Copy,
  Download,
  Eye,
  ImageDown,
  Maximize2,
  Sparkles,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  canvasPreview,
  canvasPreviewHistory,
  openDocumentPreview,
  openImagePreview,
  selectCanvasPreview,
  workspace,
} from "../../store/workspace";
import SandboxedHtmlPreview from "../preview/SandboxedHtmlPreview";
import {
  downloadSvgAsPng,
  downloadSvgFile,
  sanitizeSvgMarkup,
  svgToDataURL,
} from "../timeline/svg-sanitizer";

export default function CanvasPreviewView() {
  const [viewMode, setViewMode] = createSignal<"preview" | "source">("preview");
  const [copied, setCopied] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  onCleanup(() => {
    if (copyTimer) clearTimeout(copyTimer);
  });

  const sessionEntries = createMemo(() => {
    const sid = workspace.activeSessionID;
    const all = canvasPreviewHistory();
    if (!sid) return all;
    const scoped = all.filter((item) => !item.sessionID || item.sessionID === sid);
    return scoped.length > 0 ? scoped : all;
  });

  const active = createMemo(() => {
    const entries = sessionEntries();
    if (entries.length === 0) return null;
    const cur = canvasPreview();
    if (cur && entries.some((item) => item.id === cur.id)) return cur;
    return entries[entries.length - 1] ?? null;
  });

  const activeIndex = createMemo(() => {
    const cur = active();
    if (!cur) return -1;
    return sessionEntries().findIndex((item) => item.id === cur.id);
  });

  createEffect(() => {
    void active()?.id;
    setViewMode("preview");
  });

  const sanitizedSvg = createMemo(() => {
    const entry = active();
    if (!entry || entry.kind !== "svg") return "";
    return sanitizeSvgMarkup(entry.content).svg;
  });

  const handleCopy = async () => {
    const content = active()?.content;
    if (!content) return;
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      if (copyTimer) clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopied(false), 1500);
    } catch {
      /* ignore */
    }
  };

  const handleDownload = () => {
    const entry = active();
    if (!entry) return;
    if (entry.kind === "svg") {
      const svg = sanitizedSvg();
      if (svg) downloadSvgFile(svg, `${entry.title.replace(/[^\w-]+/g, "-") || "graphic"}.svg`);
      return;
    }
    const blob = new Blob([entry.content], { type: "text/html;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `${entry.title.replace(/[^\w-]+/g, "-") || "preview"}.html`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10_000);
  };

  const handleExpandModal = () => {
    const entry = active();
    if (!entry) return;
    if (entry.kind === "html") {
      openDocumentPreview({
        kind: "html",
        title: entry.title,
        html: entry.content,
      });
      return;
    }
    const svg = sanitizedSvg();
    if (svg) {
      openImagePreview({ src: svgToDataURL(svg), alt: entry.title });
    }
  };

  const stepVersion = (delta: -1 | 1) => {
    const list = sessionEntries();
    const idx = activeIndex();
    if (idx < 0 || list.length <= 1) return;
    const nextIdx = Math.min(list.length - 1, Math.max(0, idx + delta));
    if (nextIdx !== idx) selectCanvasPreview(list[nextIdx].id);
  };

  return (
    <Show
      when={active()}
      fallback={
        <div class="flex h-full flex-col items-center justify-center gap-2.5 p-6 text-center">
          <Sparkles size={22} class="text-[var(--text-faint)]" />
          <div class="text-[13px] font-medium text-[var(--text-primary)]">
            {t("timeline.canvasEmpty")}
          </div>
          <p class="max-w-xs text-[12px] leading-relaxed text-[var(--text-muted)]">
            {t("timeline.canvasEmptyHint")}
          </p>
        </div>
      }
    >
      {(entry) => (
        <div class="flex h-full min-h-0 flex-col bg-[var(--bg-panel)]">
          {/* Canvas Header Bar */}
          <div class="flex shrink-0 items-center justify-between gap-2 border-b border-[var(--border-subtle)] px-2.5 py-1.5">
            <div class="flex min-w-0 items-center gap-1.5">
              <span class="shrink-0 rounded bg-[var(--accent-surface)] px-1.5 py-0.5 font-mono text-[10px] font-semibold uppercase text-[var(--accent-primary)]">
                {entry().kind}
              </span>
              <span class="truncate text-[12px] font-medium text-[var(--text-primary)]">
                {entry().title}
              </span>
              <Show when={sessionEntries().length > 1 && activeIndex() >= 0}>
                <div class="ml-1 inline-flex shrink-0 items-center gap-0.5 rounded-md bg-[var(--bg-inset)] px-1 py-0.5 text-[10.5px] font-mono text-[var(--text-muted)]">
                  <button
                    type="button"
                    disabled={activeIndex() <= 0}
                    onClick={() => stepVersion(-1)}
                    class="rounded p-0.5 hover:text-[var(--text-primary)] disabled:opacity-35 cursor-pointer"
                  >
                    <ChevronLeft size={11} />
                  </button>
                  <span>
                    {t("timeline.canvasVersionLabel", {
                      index: activeIndex() + 1,
                      total: sessionEntries().length,
                    })}
                  </span>
                  <button
                    type="button"
                    disabled={activeIndex() >= sessionEntries().length - 1}
                    onClick={() => stepVersion(1)}
                    class="rounded p-0.5 hover:text-[var(--text-primary)] disabled:opacity-35 cursor-pointer"
                  >
                    <ChevronRight size={11} />
                  </button>
                </div>
              </Show>
            </div>

            <div class="flex shrink-0 items-center gap-1">
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

              <button
                type="button"
                onClick={handleDownload}
                title={
                  entry().kind === "svg"
                    ? t("timeline.downloadSvg")
                    : t("timeline.canvasDownloadHtml")
                }
                class="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
              >
                <Download size={12} />
              </button>

              <Show when={entry().kind === "svg" && sanitizedSvg()}>
                <button
                  type="button"
                  onClick={() =>
                    void downloadSvgAsPng(
                      sanitizedSvg(),
                      `${entry().title.replace(/[^\w-]+/g, "-") || "graphic"}.png`,
                      workspace.theme === "dark" ? "#141418" : "#ffffff"
                    )
                  }
                  title={t("timeline.downloadPng")}
                  class="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
                >
                  <ImageDown size={12} />
                </button>
              </Show>

              <button
                type="button"
                onClick={handleExpandModal}
                title={t("timeline.canvasOpenOverlay")}
                class="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
              >
                <Maximize2 size={12} />
              </button>

              <button
                type="button"
                onClick={() => void handleCopy()}
                title={copied() ? t("common.copied") : t("common.copy")}
                class="rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
              >
                <Show when={copied()} fallback={<Copy size={12} />}>
                  <Check size={12} class="text-[var(--ok-text)]" />
                </Show>
              </button>
            </div>
          </div>

          {/* Canvas Body */}
          <div class="flex-1 min-h-0 overflow-auto bg-[var(--bg-code)]">
            <Show
              when={viewMode() === "preview"}
              fallback={
                <pre
                  class="p-4 font-mono leading-[1.6] text-[var(--text-primary)] whitespace-pre-wrap break-words selectable-text"
                  style={{ "font-size": "var(--app-code-text-size)" }}
                >
                  <code>{entry().content}</code>
                </pre>
              }
            >
              <Show
                when={entry().kind === "html"}
                fallback={
                  <div
                    onClick={handleExpandModal}
                    class="flex min-h-full items-center justify-center p-6 cursor-zoom-in"
                  >
                    {/* Isolated via <img> data URL so SVG <style> and element IDs never leak into Hoxi's host DOM */}
                    <img
                      src={svgToDataURL(sanitizedSvg())}
                      alt={entry().title}
                      class="h-auto max-h-full max-w-full object-contain"
                    />
                  </div>
                }
              >
                <SandboxedHtmlPreview
                  html={entry().content}
                  title={entry().title}
                  compactToolbar={true}
                />
              </Show>
            </Show>
          </div>
        </div>
      )}
    </Show>
  );
}
