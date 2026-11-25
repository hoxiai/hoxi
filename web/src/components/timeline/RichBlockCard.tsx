// web/src/components/timeline/RichBlockCard.tsx
// Inline declarative card/metric block for ```hoxi-cards / ```cards / ```hoxi-metrics / ```metrics.
// Reuses StructuredRichView with Cards / Table / Source toggle, copy, and AI syntax-fix handoff.
import { createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { Check, Code2, Copy, LayoutGrid, Sparkles, Table } from "lucide-solid";
import { t } from "../../i18n";
import { isTesterPortalMode, workspace } from "../../store/workspace";
import type { ContextEnvelope } from "../../store/context-protocol";
import { parseRichMarkdownBlock, richEntitiesToTable } from "./rich-view-spec";
import StructuredRichView from "./StructuredRichView";
import { useMarkdownStreaming } from "./stream-reveal";

export default function RichBlockCard(props: {
  lang: string;
  code: string;
  ref?: (el: HTMLDivElement) => void;
}) {
  const streaming = useMarkdownStreaming();
  const [viewMode, setViewMode] = createSignal<"rich" | "table" | "source">("rich");
  const [copied, setCopied] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  onCleanup(() => {
    if (copyTimer) clearTimeout(copyTimer);
  });

  const isMetricsBlock = () => {
    const clean = (props.lang || "").trim().toLowerCase();
    return clean === "hoxi-metrics" || clean === "metrics";
  };

  const spec = createMemo(() => parseRichMarkdownBlock(props.lang, props.code));
  const tableData = createMemo(() => {
    const s = spec();
    return s?.entities ? richEntitiesToTable(s.entities) : null;
  });
  const showError = createMemo(() => !streaming() && !spec() && props.code.trim().length > 0);

  const fallbackLabel = () =>
    isMetricsBlock() ? t("timeline.richMetricsLabel") : t("timeline.richCardsLabel");

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

  const handleFixWithAI = () => {
    const envelope: ContextEnvelope = {
      version: 1,
      items: [
        {
          kind: "quote",
          title: fallbackLabel(),
          content: props.code,
        },
      ],
    };
    window.dispatchEvent(
      new CustomEvent("hoxi:composer-fill", {
        detail: {
          text: t("timeline.richCardsFixPrompt"),
          contexts: [envelope],
          appendContexts: true,
        },
      })
    );
  };

  return (
    <div
      ref={props.ref}
      data-hoxi-rich-block-card="true"
      class="my-1.5 overflow-hidden rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-code)]"
    >
      <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/70 px-3 py-1.5 text-[11px]">
        <div class="flex items-center gap-1.5 min-w-0">
          <LayoutGrid size={12} class="shrink-0 text-[var(--accent-primary)]" />
          <span class="font-mono font-medium text-[var(--text-secondary)] truncate">
            {spec()?.title || fallbackLabel()}
          </span>
        </div>

        <div class="flex items-center gap-1 shrink-0">
          <Show when={spec()}>
            <div class="inline-flex items-center rounded-md bg-[var(--bg-inset)] p-0.5">
              <button
                type="button"
                onClick={() => setViewMode("rich")}
                class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] transition-colors cursor-pointer ${
                  viewMode() === "rich"
                    ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
              >
                <LayoutGrid size={11} />
                <span>{t("timeline.richCardsViewCards")}</span>
              </button>
              <Show when={tableData()}>
                <button
                  type="button"
                  onClick={() => setViewMode("table")}
                  class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] transition-colors cursor-pointer ${
                    viewMode() === "table"
                      ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                      : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <Table size={11} />
                  <span>{t("timeline.richCardsViewTable")}</span>
                </button>
              </Show>
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
        when={spec() && viewMode() === "rich"}
        fallback={
          <Show
            when={viewMode() === "table" ? tableData() : null}
            fallback={
              <div>
                <Show when={showError()}>
                  <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--warn-surface)]/40 px-3 py-1.5 text-[11px] text-[var(--warn-text)]">
                    <span>{t("timeline.richCardsRenderFailed")}</span>
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
            {(tb) => (
              <div class="overflow-x-auto bg-[var(--bg-panel)]/70">
                <table class="w-full border-collapse text-left text-[12px] selectable-text">
                  <thead>
                    <tr class="border-b border-[var(--border-subtle)] bg-[var(--bg-elevated)]/70">
                      <For each={tb().headers}>
                        {(h) => (
                          <th class="px-3 py-1.5 font-semibold text-[var(--text-primary)]">
                            {h}
                          </th>
                        )}
                      </For>
                    </tr>
                  </thead>
                  <tbody>
                    <For each={tb().rows}>
                      {(row) => (
                        <tr class="border-b last:border-b-0 border-[var(--border-subtle)]/70">
                          <For each={row}>
                            {(cell) => (
                              <td class="px-3 py-1.5 text-[var(--text-secondary)]">{cell}</td>
                            )}
                          </For>
                        </tr>
                      )}
                    </For>
                  </tbody>
                </table>
              </div>
            )}
          </Show>
        }
      >
        <div class="bg-[var(--bg-panel)]/45 p-2">
          <StructuredRichView
            spec={spec()!}
            hideHeader={!spec()!.entities || spec()!.entities!.length <= 1}
          />
        </div>
      </Show>
    </div>
  );
}
