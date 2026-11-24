// web/src/components/preview/SandboxedHtmlPreview.tsx
// Shared sandboxed HTML iframe preview (`sandbox="allow-scripts"`) with
// responsive viewport presets (Fit / Tablet / Mobile) and reload control.
import { createSignal, Show } from "solid-js";
import { Monitor, RefreshCw, Smartphone, Tablet } from "lucide-solid";
import { t } from "../../i18n";

export type HtmlViewportPreset = "fit" | "tablet" | "mobile";

export default function SandboxedHtmlPreview(props: {
  html: string;
  title?: string;
  compactToolbar?: boolean;
}) {
  const [viewport, setViewport] = createSignal<HtmlViewportPreset>("fit");
  const [reloadNonce, setReloadNonce] = createSignal(0);

  const frameWidth = () => {
    switch (viewport()) {
      case "mobile":
        return "390px";
      case "tablet":
        return "768px";
      default:
        return "100%";
    }
  };

  return (
    <div class="flex h-full min-h-0 w-full flex-col bg-[var(--bg-inset)]">
      <div class="flex shrink-0 items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/70 px-3 py-1 text-[11px]">
        <div class="inline-flex items-center rounded-md bg-[var(--bg-inset)] p-0.5">
          <button
            type="button"
            onClick={() => setViewport("fit")}
            title={t("timeline.canvasViewportFit")}
            class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] transition-colors cursor-pointer ${
              viewport() === "fit"
                ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            }`}
          >
            <Monitor size={11} />
            <Show when={!props.compactToolbar}>
              <span>{t("timeline.canvasViewportFit")}</span>
            </Show>
          </button>
          <button
            type="button"
            onClick={() => setViewport("tablet")}
            title={t("timeline.canvasViewportTablet")}
            class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] transition-colors cursor-pointer ${
              viewport() === "tablet"
                ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            }`}
          >
            <Tablet size={11} />
            <Show when={!props.compactToolbar}>
              <span>{t("timeline.canvasViewportTablet")}</span>
            </Show>
          </button>
          <button
            type="button"
            onClick={() => setViewport("mobile")}
            title={t("timeline.canvasViewportMobile")}
            class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] transition-colors cursor-pointer ${
              viewport() === "mobile"
                ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            }`}
          >
            <Smartphone size={11} />
            <Show when={!props.compactToolbar}>
              <span>{t("timeline.canvasViewportMobile")}</span>
            </Show>
          </button>
        </div>

        <button
          type="button"
          onClick={() => setReloadNonce((n) => n + 1)}
          title={t("timeline.canvasReload")}
          class="inline-flex items-center justify-center rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
        >
          <RefreshCw size={11.5} />
        </button>
      </div>

      <div
        class={`flex flex-1 min-h-0 justify-center overflow-auto ${
          viewport() === "fit" ? "" : "p-4"
        }`}
      >
        <Show when={reloadNonce() + 1} keyed>
          {(_nonce) => (
            <iframe
              sandbox="allow-scripts"
              referrerPolicy="no-referrer"
              srcdoc={props.html}
              title={props.title || "HTML"}
              style={{ width: frameWidth() }}
              class={`h-full min-h-[420px] max-w-full border-0 bg-white transition-[width] duration-150 ${
                viewport() === "fit"
                  ? ""
                  : "rounded-xl border border-[var(--border-subtle)] shadow-md"
              }`}
            />
          )}
        </Show>
      </div>
    </div>
  );
}
