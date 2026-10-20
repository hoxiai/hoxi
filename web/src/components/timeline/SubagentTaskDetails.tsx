// web/src/components/timeline/SubagentTaskDetails.tsx
import { createMemo, createSignal, Show } from "solid-js";
import { t } from "../../i18n";
import { Icon } from "../icons";
import MarkdownView from "./MarkdownView";
import type { ToolPart } from "./tool-card-helpers";

/** OpenCode wraps a task's result in `<task_result>`; runner subagents report plain text. */
function extractTaskReport(part: ToolPart): string {
  if (part.state.status !== "completed") return "";
  const raw = part.state.output || "";
  const match = raw.match(/<task_result>([\s\S]*?)<\/task_result>/);
  return (match ? match[1] : raw).trim();
}

/**
 * Prompt (collapsed) and final report of one subagent, shown under its
 * inline step list so the hand-off and the result sit with the subagent.
 */
export default function SubagentTaskDetails(props: { part: ToolPart; running: boolean }) {
  const [showPrompt, setShowPrompt] = createSignal(false);
  const prompt = createMemo(() => {
    const value = props.part.state.input?.prompt;
    return typeof value === "string" ? value.trim() : "";
  });
  const report = createMemo(() => extractTaskReport(props.part));

  return (
    <div class="mt-1 ml-3 pl-3 space-y-1.5 text-[12px]">
      <Show when={prompt()}>
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            setShowPrompt(!showPrompt());
          }}
          class="inline-flex items-center gap-1 text-[12px] font-medium text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
        >
          <Icon name={showPrompt() ? "chevron-down" : "chevron-right"} size="small" />
          <span>{t("toolCard.subagentPrompt")}</span>
        </button>
        <Show when={showPrompt()}>
          <pre class="text-[12px] font-mono bg-[var(--bg-code)] border border-[var(--border-subtle)] p-2.5 rounded-lg max-h-48 overflow-auto text-[var(--text-secondary)] whitespace-pre-wrap selectable-text">
            {prompt()}
          </pre>
        </Show>
      </Show>

      <Show
        when={report()}
        fallback={
          <Show when={props.running}>
            <div class="text-[12px] text-[var(--text-faint)] italic">
              {t("toolCard.subagentAwaitingReport")}
            </div>
          </Show>
        }
      >
        <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-3 py-2">
          <div class="text-[11px] font-medium text-[var(--text-muted)] mb-1">
            {t("toolCard.subagentReport")}
          </div>
          <div class="max-h-80 overflow-auto selectable-text">
            <MarkdownView content={report()} />
          </div>
        </div>
      </Show>
    </div>
  );
}
