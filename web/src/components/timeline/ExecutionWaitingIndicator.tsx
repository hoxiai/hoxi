import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { t } from "../../i18n";
import { Spinner, TextShimmer } from "../icons";
import type { ToolPart } from "../../services/sse-types";

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
      class="mt-3 overflow-hidden border-l-2 border-[var(--accent-border)]/60 py-2.5 pl-3"
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
          <Show when={props.completedSteps > 0}>
            <span class="hidden sm:inline text-[11px] text-[var(--text-muted)]">
              {t("timeline.executionStepsDone", { count: props.completedSteps })}
            </span>
            <Show when={elapsed() >= 0.05}>
              <span class="text-[var(--text-faint)]">·</span>
            </Show>
          </Show>
          <Show when={elapsed() >= 0.05}>
            <span class="font-mono text-[11px] tabular-nums text-[var(--accent-text)]">
              {t("timeline.executionElapsed", { time: elapsed().toFixed(1) })}
            </span>
          </Show>
        </div>
      </div>
    </div>
  );
}
