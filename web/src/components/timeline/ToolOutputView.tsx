// web/src/components/timeline/ToolOutputView.tsx
// Renders generic tool outputs cleanly: structured JsonTreeView for JSON objects/arrays,
// or clickable workspace `path:line` links for plain-text search/diagnostic output.
// Also provides a concise error badge extractor for collapsed failed ToolCard rows.
import { createMemo, createSignal, For, Show } from "solid-js";
import { Code2, LayoutGrid } from "lucide-solid";
import type { MessagePart } from "../../services/sse-types";
import { t } from "../../i18n";
import {
  isTesterPortalMode,
  openFileInInspector,
  type StructuredRichViewSpec,
} from "../../store/workspace";
import { parseShellOutputRuns } from "./ansi-output";
import JsonTreeView, { tryParseStructuredJson } from "./JsonTreeView";
import StructuredRichView from "./StructuredRichView";
import { processCarriageReturns } from "./terminal-output-text";

type ToolPart = Extract<MessagePart, { type: "tool" }>;

const MAX_LINKIFY_CHARS = 32 * 1024;

/**
 * Extracts a short, single-line failure summary (`exit 1` or concise error head)
 * for collapsed failed ToolCard headers so errors are scannable without expanding.
 */
export function extractToolErrorBadge(part: ToolPart, exitCode?: number): string {
  if (part.state.status !== "error") return "";
  if (typeof exitCode === "number" && exitCode !== 0) {
    return `exit ${exitCode}`;
  }
  const raw = processCarriageReturns(String(part.state.error || "")).trim();
  if (!raw) return "";
  const firstLine =
    raw
      .split("\n")
      .map((line) => line.trim())
      .find((line) => line.length > 0) ?? "";
  if (!firstLine) return "";
  const compact = firstLine.replace(/^(?:error|failed)\s*:\s*/i, "").trim() || firstLine;
  return compact.length > 44 ? `${compact.slice(0, 43)}…` : compact;
}

export default function ToolOutputView(props: {
  text: string;
  richView?: StructuredRichViewSpec | null;
}) {
  const [viewMode, setViewMode] = createSignal<"rich" | "raw">("rich");
  const parsedJson = createMemo(() => tryParseStructuredJson(props.text));
  const shouldLinkify = createMemo(() => {
    if (parsedJson() || isTesterPortalMode()) return false;
    const txt = props.text;
    return txt.length > 0 && txt.length <= MAX_LINKIFY_CHARS && txt.includes(":") && txt.includes(".");
  });
  const runs = createMemo(() =>
    shouldLinkify() ? parseShellOutputRuns(props.text, { semanticColors: false }) : []
  );
  const hasAnyFileLink = createMemo(() => runs().some((r) => Boolean(r.fileRef)));

  const renderRawOutput = () => (
    <Show
      when={parsedJson()}
      fallback={
        <pre class="text-[12px] font-mono bg-[var(--bg-code)] border border-[var(--border-subtle)] p-2.5 rounded-lg overflow-x-auto max-h-56 text-[var(--text-secondary)] whitespace-pre-wrap break-all selectable-text">
          <Show when={hasAnyFileLink()} fallback={props.text}>
            <For each={runs()}>
              {(run) => (
                <Show when={run.fileRef} fallback={run.text}>
                  <button
                    type="button"
                    onClick={(ev) => {
                      ev.stopPropagation();
                      openFileInInspector(run.fileRef!.path, run.fileRef!.line);
                    }}
                    title={t("toolCard.openFileLineTooltip", {
                      file: run.fileRef!.path,
                      line: run.fileRef!.line,
                    })}
                    class="inline font-mono text-[var(--accent-primary)] underline decoration-[var(--accent-border)] underline-offset-2 hover:opacity-85 cursor-pointer"
                  >
                    {run.text}
                  </button>
                </Show>
              )}
            </For>
          </Show>
        </pre>
      }
    >
      {(data) => <JsonTreeView data={data()} rawText={props.text} />}
    </Show>
  );

  return (
    <Show when={props.richView} fallback={renderRawOutput()}>
      {(richSpec) => (
        <div class="space-y-1.5">
          <div class="flex items-center justify-end">
            <div class="inline-flex items-center rounded-md bg-[var(--bg-inset)] p-0.5 text-[10.5px]">
              <button
                type="button"
                onClick={(ev) => {
                  ev.stopPropagation();
                  setViewMode("rich");
                }}
                class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 transition-colors cursor-pointer ${
                  viewMode() === "rich"
                    ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
              >
                <LayoutGrid size={10.5} />
                <span>{t("toolCard.richViewTab")}</span>
              </button>
              <button
                type="button"
                onClick={(ev) => {
                  ev.stopPropagation();
                  setViewMode("raw");
                }}
                class={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 transition-colors cursor-pointer ${
                  viewMode() === "raw"
                    ? "bg-[var(--bg-panel)] text-[var(--text-primary)] font-medium shadow-2xs"
                    : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                }`}
              >
                <Code2 size={10.5} />
                <span>{t("toolCard.jsonViewTab")}</span>
              </button>
            </div>
          </div>
          <Show when={viewMode() === "rich"} fallback={renderRawOutput()}>
            <StructuredRichView spec={richSpec()} compact={true} />
          </Show>
        </div>
      )}
    </Show>
  );
}
