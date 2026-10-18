import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { t } from "../../i18n";
import { Spinner, TextShimmer } from "../icons";
import type { ToolPart } from "../../services/sse-types";
import ExecutionStepDetails from "./ExecutionStepDetails";

export default function ExecutionWaitingIndicator(props: {
  completedSteps: number;
  steps: ToolPart[];
  startedAt?: number;
  activeStepLabel?: string;
}) {
  const mountedAt = Date.now();
  const resolveStart = () =>
    props.startedAt && props.startedAt > 0 ? props.startedAt : mountedAt;
  const calcElapsed = () => Math.max(0, (Date.now() - resolveStart()) / 1000);

  const [elapsed, setElapsed] = createSignal(calcElapsed());
  const [detailsOpen, setDetailsOpen] = createSignal(false);

  createEffect(() => {
    setElapsed(calcElapsed());
    const timer = setInterval(() => {
      setElapsed(calcElapsed());
    }, 100);
    onCleanup(() => clearInterval(timer));
  });

  const headerText = () =>
    props.completedSteps > 0 || Boolean(props.activeStepLabel)
      ? t("timeline.executing")
      : t("timeline.thinking");

  return (
    <div
      data-component="execution-waiting"
      class="mt-3 overflow-hidden py-2.5"
    >
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[13px] text-[var(--accent-primary)]">
        <div class="flex min-w-0 flex-1 basis-48 items-center gap-2">
          <span class="relative inline-flex shrink-0 items-center justify-center">
            <span class="absolute h-5 w-5 rounded-full bg-[var(--accent-primary)]/15 animate-ping" />
            <Spinner class="relative h-4 w-4" />
          </span>
          <Show
            when={props.activeStepLabel}
            fallback={<TextShimmer text={headerText()} active={true} class="shrink-0 font-medium" />}
          >
            <span class="min-w-0 truncate text-[12px] text-[var(--text-secondary)]" title={props.activeStepLabel}>
              {props.activeStepLabel}
            </span>
          </Show>
        </div>
        <div class="ml-auto flex shrink-0 items-center gap-2">
          <span class="font-mono text-[11px] tabular-nums text-[var(--accent-text)]">
            {t("timeline.executionElapsed", { time: elapsed().toFixed(1) })}
          </span>
          <Show when={props.steps.length > 0}>
            <span aria-hidden="true" class="text-[var(--text-faint)]">·</span>
            <button
              type="button"
              aria-expanded={detailsOpen()}
              onClick={() => setDetailsOpen((open) => !open)}
              title={t("timeline.executionStepDetails")}
              class="shrink-0 rounded px-1.5 py-1 font-mono text-[10.5px] text-[var(--accent-text)] hover:bg-[var(--bg-hover)] focus-visible:outline focus-visible:outline-[var(--accent-primary)] cursor-pointer"
            >
              {props.completedSteps > 0
                ? t("timeline.executionStepsDone", { count: props.completedSteps })
                : t("timeline.executionStepNumber", { count: props.steps.length })}
            </button>
          </Show>
        </div>
      </div>
      <Show when={detailsOpen()}>
        <ExecutionStepDetails steps={props.steps} />
      </Show>
    </div>
  );
}
