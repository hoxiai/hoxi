// web/src/components/schedules/ScheduleRunHistory.tsx
import { For, Show } from "solid-js";
import {
  AlertTriangle,
  CheckCircle2,
  Clock,
  ExternalLink,
  LoaderCircle,
  SkipForward,
  XCircle,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  openScheduleRunSession,
  type ScheduleItem,
  type ScheduleRunRecord,
} from "../../store/workspace";
import {
  formatShortTimestamp,
  scheduleStatusLabel,
  scheduleStatusTone,
} from "./schedule-helpers";

function RunIcon(props: { status: string }) {
  return (
    <Show
      when={props.status !== "running"}
      fallback={<LoaderCircle size={13} class="shrink-0 animate-spin text-[var(--accent-primary)]" />}
    >
      <Show
        when={props.status === "alert" || props.status === "blocked"}
        fallback={
          <Show
            when={props.status === "error"}
            fallback={
              <Show
                when={props.status === "skipped_busy"}
                fallback={
                  <Show
                    when={props.status === "ok"}
                    fallback={<Clock size={13} class="shrink-0 text-[var(--text-muted)]" />}
                  >
                    <CheckCircle2 size={13} class="shrink-0 text-[var(--ok-text)]" />
                  </Show>
                }
              >
                <SkipForward size={13} class="shrink-0 text-[var(--text-muted)]" />
              </Show>
            }
          >
            <XCircle size={13} class="shrink-0 text-[var(--danger-text)]" />
          </Show>
        }
      >
        <AlertTriangle size={13} class="shrink-0 text-[var(--status-warning)]" />
      </Show>
    </Show>
  );
}

function formatDuration(startMs: number, endMs?: number): string {
  if (!endMs || endMs <= startMs) return "";
  const sec = Math.round((endMs - startMs) / 1000);
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  return `${min}m ${sec % 60}s`;
}

export default function ScheduleRunHistory(props: {
  schedule: ScheduleItem;
  runs: ScheduleRunRecord[];
}) {
  return (
    <Show
      when={props.runs.length > 0}
      fallback={
        <div class="flex flex-col items-center justify-center py-12 text-center text-[12px] text-[var(--text-faint)]">
          <Clock size={22} class="mb-2 opacity-40" />
          <span>{t("schedules.noRunsYet")}</span>
        </div>
      }
    >
      <div class="space-y-2">
        <For each={props.runs}>
          {(run) => (
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/70 p-3">
              <div class="flex items-center gap-2">
                <RunIcon status={run.status} />
                <span class={`text-[12px] font-medium ${scheduleStatusTone(run.status)}`}>
                  {scheduleStatusLabel(run.status)}
                </span>
                <span class="rounded bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                  {run.trigger}
                </span>
                <span class="text-[11px] text-[var(--text-muted)]">
                  {formatShortTimestamp(run.startedAt)}
                </span>
                <Show when={formatDuration(run.startedAt, run.completedAt)}>
                  <span class="font-mono text-[10.5px] text-[var(--text-faint)]">
                    {formatDuration(run.startedAt, run.completedAt)}
                  </span>
                </Show>
                <Show when={run.sessionID}>
                  <button
                    type="button"
                    onClick={() => void openScheduleRunSession(props.schedule, run.sessionID)}
                    class="ml-auto inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--accent-primary)] cursor-pointer"
                  >
                    <ExternalLink size={11} />
                    <span>{t("schedules.openSessionBtn")}</span>
                  </button>
                </Show>
              </div>
              <Show when={run.summary}>
                <p class="mt-1.5 whitespace-pre-wrap text-[12px] text-[var(--text-secondary)]">
                  {run.summary}
                </p>
              </Show>
              <Show when={run.error}>
                <p class="mt-1.5 whitespace-pre-wrap font-mono text-[11px] text-[var(--danger-text)]">
                  {run.error}
                </p>
              </Show>
            </div>
          )}
        </For>
      </div>
    </Show>
  );
}
