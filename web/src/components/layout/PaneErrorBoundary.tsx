import { ErrorBoundary, Show, type JSX } from "solid-js";
import { t } from "../../i18n";

interface PaneErrorBoundaryProps {
  children: JSX.Element;
  compact?: boolean;
  fullPage?: boolean;
}

function formatBoundaryError(err: unknown): string {
  if (err instanceof Error && err.message) return err.message;
  if (typeof err === "string" && err.trim()) return err;
  return "";
}

export default function PaneErrorBoundary(props: PaneErrorBoundaryProps) {
  return (
    <ErrorBoundary
      fallback={(err, reset) => {
        console.error("[hoxi] render error boundary caught:", err);
        const detail = formatBoundaryError(err);
        return (
          <div
            role="alert"
            class={
              props.fullPage
                ? "flex h-screen w-screen items-center justify-center bg-[var(--bg-canvas)] p-6 text-[var(--text-primary)]"
                : props.compact
                  ? "flex h-full w-full items-center justify-center p-4 text-[var(--text-primary)]"
                  : "flex h-full min-h-[160px] w-full items-center justify-center p-6 text-[var(--text-primary)]"
            }
          >
            <div class="w-full max-w-md rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-surface)] p-5 shadow-sm">
              <div class="text-sm font-semibold text-[var(--text-primary)]">
                {t("common.renderErrorTitle")}
              </div>
              <p class="mt-1.5 text-xs leading-relaxed text-[var(--text-secondary)]">
                {t("common.renderErrorHint")}
              </p>
              <Show when={detail}>
                <pre class="mt-3 max-h-24 overflow-auto rounded-lg bg-[var(--bg-code)] px-2.5 py-2 text-[11px] text-[var(--text-secondary)] whitespace-pre-wrap break-all">
                  {detail}
                </pre>
              </Show>
              <div class="mt-4 flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => reset()}
                  class="rounded-lg bg-[var(--accent-primary)] px-3 py-1.5 text-xs font-medium text-white hover:opacity-90 transition-opacity"
                >
                  {t("common.retry")}
                </button>
                <button
                  type="button"
                  onClick={() => window.location.reload()}
                  class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-canvas)] px-3 py-1.5 text-xs font-medium text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors"
                >
                  {t("common.reloadApp")}
                </button>
              </div>
            </div>
          </div>
        );
      }}
    >
      {props.children}
    </ErrorBoundary>
  );
}
