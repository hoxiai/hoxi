// web/src/components/timeline/MermaidBlockCard.tsx
import { createEffect, createSignal, on, onCleanup, Show } from "solid-js";
import { Check, Code2, Copy, Download, Eye, ImageDown, LoaderCircle, Maximize2, Sparkles } from "lucide-solid";
import { t } from "../../i18n";
import { isTesterPortalMode, openImagePreview, workspace } from "../../store/workspace";
import type { ContextEnvelope } from "../../store/context-protocol";
import { useMarkdownStreaming } from "./stream-reveal";
import { downloadSvgAsPng, downloadSvgFile, svgToDataURL } from "./svg-sanitizer";

let mermaidModulePromise: Promise<typeof import("mermaid")> | null = null;
let mermaidSeq = 0;

function loadMermaid() {
  if (!mermaidModulePromise) {
    mermaidModulePromise = import("mermaid");
  }
  return mermaidModulePromise;
}

export default function MermaidBlockCard(props: {
  code: string;
  ref?: (el: HTMLDivElement) => void;
}) {
  const streaming = useMarkdownStreaming();
  const [viewMode, setViewMode] = createSignal<"preview" | "source">("preview");
  const [svgMarkup, setSvgMarkup] = createSignal("");
  const [rendering, setRendering] = createSignal(false);
  const [renderError, setRenderError] = createSignal<string | null>(null);
  const [copied, setCopied] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  let debounceTimer: ReturnType<typeof setTimeout> | undefined;
  let renderVersion = 0;

  onCleanup(() => {
    renderVersion++;
    if (copyTimer) clearTimeout(copyTimer);
    if (debounceTimer) clearTimeout(debounceTimer);
  });

  createEffect(
    on(
      () => [props.code, workspace.theme, streaming()] as const,
      ([rawCode, theme, isStreaming]) => {
        const source = (rawCode || "").trim();
        if (debounceTimer) {
          clearTimeout(debounceTimer);
          debounceTimer = undefined;
        }
        if (!source) {
          setSvgMarkup("");
          setRenderError(null);
          setRendering(false);
          return;
        }
        const version = ++renderVersion;
        const delayMs = isStreaming ? 420 : 40;
        setRendering(true);
        debounceTimer = setTimeout(() => {
          debounceTimer = undefined;
          void (async () => {
            try {
              const mod = await loadMermaid();
              if (version !== renderVersion) return;
              const mermaid = mod.default;
              mermaid.initialize({
                startOnLoad: false,
                securityLevel: "strict",
                theme: theme === "dark" ? "dark" : "default",
                fontFamily: "inherit",
              });
              const renderID = `hoxi-mermaid-${++mermaidSeq}`;
              const { svg } = await mermaid.render(renderID, source);
              if (version !== renderVersion) return;
              setSvgMarkup(svg);
              setRenderError(null);
              setRendering(false);
            } catch (err) {
              if (version !== renderVersion) return;
              setRendering(false);
              if (!isStreaming) {
                const msg = err instanceof Error ? err.message : String(err || "");
                setRenderError(msg || t("timeline.mermaidRenderFailed"));
              }
            }
          })();
        }, delayMs);
      }
    )
  );

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
    const svg = svgMarkup();
    if (!svg) return;
    openImagePreview({
      src: svgToDataURL(svg),
      alt: t("timeline.mermaidDiagramLabel"),
    });
  };

  const handleDownloadPng = async () => {
    const svg = svgMarkup();
    if (!svg) return;
    const bg = workspace.theme === "dark" ? "#141418" : "#ffffff";
    const ok = await downloadSvgAsPng(svg, "mermaid-diagram.png", bg);
    if (ok) return;
    // Chromium taints <canvas> when an SVG contains <foreignObject> HTML labels.
    // Re-render once offscreen with htmlLabels disabled so PNG export still succeeds.
    const source = (props.code || "").trim();
    if (!source || !/<foreignObject\b/i.test(svg)) return;
    try {
      const mod = await loadMermaid();
      const mermaid = mod.default;
      const theme = workspace.theme === "dark" ? "dark" : "default";
      try {
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: "strict",
          theme,
          fontFamily: "inherit",
          htmlLabels: false,
          flowchart: { htmlLabels: false },
        });
        const { svg: pureSvg } = await mermaid.render(`hoxi-mermaid-png-${++mermaidSeq}`, source);
        await downloadSvgAsPng(pureSvg, "mermaid-diagram.png", bg);
      } finally {
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: "strict",
          theme,
          fontFamily: "inherit",
          htmlLabels: true,
          flowchart: { htmlLabels: true },
        });
      }
    } catch {
      /* ignore */
    }
  };

  const handleFixWithAI = () => {
    const err = renderError();
    const content = err ? `${props.code}\n\nError: ${err}` : props.code;
    const envelope: ContextEnvelope = {
      version: 1,
      items: [
        {
          kind: "quote",
          title: t("timeline.mermaidDiagramLabel"),
          content,
        },
      ],
    };
    window.dispatchEvent(
      new CustomEvent("hoxi:composer-fill", {
        detail: {
          text: t("timeline.mermaidFixPrompt"),
          contexts: [envelope],
          appendContexts: true,
        },
      })
    );
  };

  const showPreview = () => viewMode() === "preview" && Boolean(svgMarkup());

  return (
    <div
      ref={props.ref}
      class="group/mermaid my-1.5 overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-code)]"
    >
      <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/70 px-3 py-1.5 text-[11px]">
        <div class="flex items-center gap-2 min-w-0">
          <span class="font-mono font-medium text-[var(--text-secondary)]">
            {t("timeline.mermaidDiagramLabel")}
          </span>
          <Show when={rendering() && !svgMarkup()}>
            <LoaderCircle size={12} class="animate-spin text-[var(--text-muted)]" />
          </Show>
        </div>
        <div class="flex items-center gap-1 shrink-0">
          <Show when={svgMarkup()}>
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
              onClick={() => downloadSvgFile(svgMarkup(), "mermaid-diagram.svg")}
              title={t("timeline.downloadSvg")}
              class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <Download size={12} />
            </button>
            <button
              type="button"
              onClick={() => void handleDownloadPng()}
              title={t("timeline.downloadPng")}
              class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
            >
              <ImageDown size={12} />
            </button>
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
            <Show when={renderError() && viewMode() === "preview"}>
              <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--warn-surface)]/40 px-3 py-1.5 text-[11px] text-[var(--warn-text)]">
                <span>{t("timeline.mermaidRenderFailed")}</span>
                <Show when={!isTesterPortalMode() && workspace.activeSessionID}>
                  <button
                    type="button"
                    onClick={handleFixWithAI}
                    class="inline-flex shrink-0 items-center gap-1 rounded-md border border-[var(--warn-border)] bg-[var(--bg-panel)]/80 px-2 py-0.5 text-[11px] font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    <Sparkles size={11} class="text-[var(--accent-primary)]" />
                    <span>{t("timeline.mermaidFixWithAI")}</span>
                  </button>
                </Show>
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
          class="flex max-h-[520px] items-center justify-center overflow-auto bg-[var(--bg-panel)]/60 p-4 cursor-zoom-in [&>svg]:h-auto [&>svg]:max-w-full"
          // Mermaid runs with securityLevel: "strict" (DOMPurify-sanitized SVG)
          innerHTML={svgMarkup()}
        />
      </Show>
    </div>
  );
}
