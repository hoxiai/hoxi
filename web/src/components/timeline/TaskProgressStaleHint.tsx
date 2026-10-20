// web/src/components/timeline/TaskProgressStaleHint.tsx
import { Show } from "solid-js";
import { History } from "lucide-solid";
import { t } from "../../i18n";
import { TextShimmer } from "../icons";
import type { NormalizedTodoItem, TodoActivitySummary } from "./task-progress-helpers";

/**
 * Shown when the model keeps working without updating its todo list, so a frozen `0/4`
 * is paired with the item in flight and the execution progress that actually happened.
 */
export default function TaskProgressStaleHint(props: {
  activeTodo?: NormalizedTodoItem;
  summary: TodoActivitySummary;
  isSessionAlive: boolean;
  compact?: boolean;
}) {
  const title = () => t("timeline.todoStaleTitle", { count: props.summary.stepsSinceTodo });

  const stats = () => (
    <>
      <span class="shrink-0 font-mono text-[11px] text-[var(--text-secondary)]">
        {t("timeline.todoStaleStepsDone", { count: props.summary.stepsDone })}
      </span>
      <Show when={props.summary.subagentsTotal > 0}>
        <span class="text-[var(--text-faint)]">·</span>
        <span class="shrink-0 font-mono text-[11px] text-[var(--text-secondary)]">
          {t("timeline.todoStaleSubagents", {
            running: props.summary.subagentsRunning,
            total: props.summary.subagentsTotal,
          })}
        </span>
      </Show>
    </>
  );

  return (
    <Show
      when={props.compact}
      fallback={
        <div class="mt-2 flex flex-wrap items-center gap-1.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)]/80 px-2 py-1.5 text-[11px] text-[var(--text-muted)]">
          <History size={12} class="shrink-0 text-[var(--warn-text)]" />
          <span class="text-[var(--warn-text)]">
            {t("timeline.todoStaleBadge", { count: props.summary.stepsSinceTodo })}
          </span>
          <span class="text-[var(--text-faint)]">·</span>
          {stats()}
        </div>
      }
    >
      <span
        class="hidden sm:inline-flex items-center gap-1 min-w-0 text-[var(--text-muted)]"
        title={title()}
      >
        <Show when={props.activeTodo}>
          {(todo) => (
            <>
              <span class="text-[var(--text-faint)]">·</span>
              <TextShimmer
                text={t("timeline.todoStaleActive", { content: todo().content })}
                active={props.isSessionAlive && todo().status === "in_progress"}
                class="truncate max-w-[180px]"
              />
            </>
          )}
        </Show>
        <span class="text-[var(--text-faint)]">·</span>
        {stats()}
      </span>
    </Show>
  );
}
