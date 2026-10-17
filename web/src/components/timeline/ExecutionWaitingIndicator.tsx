import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { CircleDot, Hourglass } from "lucide-solid";
import { t } from "../../i18n";
import { Spinner, TextShimmer } from "../icons";

export default function ExecutionWaitingIndicator(props: { completedSteps: number }) {
  const [elapsed, setElapsed] = createSignal(0);
  let startedAt = Date.now();

  createEffect(() => {
    startedAt = Date.now();
    setElapsed(0);
    const timer = setInterval(() => {
      setElapsed((Date.now() - startedAt) / 1000);
    }, 250);
    onCleanup(() => clearInterval(timer));
  });

  const statusText = () =>
    elapsed() >= 8 ? t("timeline.executionStillWorking") : t("timeline.executionPreparing");

  return (
    <div data-component="execution-waiting" class="mt-4 overflow-hidden rounded-xl border border-[var(--accent-border)]/70 bg-[var(--accent-surface)]/35 px-3.5 py-2.5 shadow-[0_0_24px_var(--accent-surface)]">
      <div class="flex items-center gap-2 text-[13px] text-[var(--accent-primary)]">
        <span class="relative inline-flex shrink-0 items-center justify-center">
          <span class="absolute h-5 w-5 rounded-full bg-[var(--accent-primary)]/15 animate-ping" />
          <Spinner class="relative h-4 w-4" />
        </span>
        <TextShimmer text={t("timeline.thinking")} active={true} class="font-medium" />
        <span class="ml-auto shrink-0 font-mono text-[11px] tabular-nums text-[var(--accent-text)]">
          {t("timeline.executionElapsed", { time: elapsed().toFixed(1) })}
        </span>
      </div>
      <div class="mt-1.5 flex items-center gap-1.5 text-[11.5px] text-[var(--text-secondary)]">
        <Show when={elapsed() >= 8} fallback={<Hourglass size={12} class="shrink-0 text-[var(--accent-primary)]" />}>
          <CircleDot size={12} class="shrink-0 text-[var(--accent-primary)]" />
        </Show>
        <TextShimmer text={statusText()} active={true} />
        <Show when={props.completedSteps > 0}>
          <span class="ml-auto shrink-0 font-mono text-[10.5px] text-[var(--text-muted)]">
            {t("timeline.executionStepsDone", { count: props.completedSteps })}
          </span>
        </Show>
      </div>
      <div class="mt-2 h-0.5 overflow-hidden rounded-full bg-[var(--accent-primary)]/10">
        <div class="h-full w-1/3 rounded-full bg-[var(--accent-primary)]/70 animate-[execution-progress_1800ms_ease-in-out_infinite]" />
      </div>
    </div>
  );
}
