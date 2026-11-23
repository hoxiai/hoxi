// web/src/components/schedules/ScheduledTasksSection.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  AlertTriangle,
  CalendarClock,
  CheckCircle2,
  ExternalLink,
  LoaderCircle,
  Pause,
  Play,
  Plus,
  Power,
  Square,
  XCircle,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  activeDirectory,
  openScheduleModal,
  openScheduleRunSession,
  runScheduleNow,
  schedulesForWorkspace,
  stopScheduleRun,
  updateSchedule,
  workspace,
  type ScheduleItem,
} from "../../store/workspace";
import {
  formatScheduleTrigger,
  formatShortTimestamp,
  scheduleStatusLabel,
  scheduleStatusTone,
} from "./schedule-helpers";

function ScheduleOutcomeIcon(props: { item: ScheduleItem }) {
  return (
    <Show
      when={!props.item.running}
      fallback={<LoaderCircle size={12} class="shrink-0 animate-spin text-[var(--accent-primary)]" />}
    >
      <Show
        when={props.item.lastOutcome === "alert" || props.item.lastOutcome === "blocked"}
        fallback={
          <Show
            when={props.item.lastOutcome === "error"}
            fallback={
              <Show
                when={props.item.lastOutcome === "ok"}
                fallback={<CalendarClock size={12} class="shrink-0 text-[var(--text-muted)]" />}
              >
                <CheckCircle2 size={12} class="shrink-0 text-[var(--ok-text)]" />
              </Show>
            }
          >
            <XCircle size={12} class="shrink-0 text-[var(--danger-text)]" />
          </Show>
        }
      >
        <AlertTriangle size={12} class="shrink-0 text-[var(--status-warning)]" />
      </Show>
    </Show>
  );
}

function ScheduleCompactRow(props: { item: ScheduleItem }) {
  const [busy, setBusy] = createSignal(false);

  const toggleEnabled = async (ev: MouseEvent) => {
    ev.stopPropagation();
    if (busy()) return;
    setBusy(true);
    try {
      await updateSchedule(props.item.id, { enabled: !props.item.enabled });
    } catch {
      /* errors surface inside modal */
    } finally {
      setBusy(false);
    }
  };

  const triggerRunOrStop = async (ev: MouseEvent) => {
    ev.stopPropagation();
    if (busy()) return;
    setBusy(true);
    try {
      if (props.item.running) {
        await stopScheduleRun(props.item.id);
      } else {
        await runScheduleNow(props.item.id);
      }
    } catch {
      /* errors surface inside modal */
    } finally {
      setBusy(false);
    }
  };

  const targetSession = () => props.item.lastSessionID || props.item.sessionID || "";

  return (
    <div
      onClick={() => openScheduleModal({ scheduleID: props.item.id, directory: props.item.directory, projectID: props.item.projectID })}
      class={`group rounded-lg border px-2.5 py-2 transition-colors cursor-pointer ${
        props.item.alertUnseen
          ? "border-[var(--status-warning)]/40 bg-[var(--status-warning)]/8 hover:bg-[var(--status-warning)]/12"
          : "border-[var(--border-subtle)]/70 bg-[var(--bg-inset)]/60 hover:bg-[var(--bg-hover)]/70"
      }`}
    >
      <div class="flex items-center gap-1.5 min-w-0">
        <ScheduleOutcomeIcon item={props.item} />
        <span class="min-w-0 flex-1 truncate text-[12px] font-medium text-[var(--text-primary)]">
          {props.item.name}
        </span>
        <div class="flex shrink-0 items-center gap-0.5">
          <button
            type="button"
            disabled={busy()}
            onClick={(ev) => void triggerRunOrStop(ev)}
            title={props.item.running ? t("schedules.stopBtn") : t("schedules.runNowBtn")}
            class="inline-flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:bg-[var(--bg-panel)] hover:text-[var(--text-primary)] cursor-pointer disabled:opacity-40"
          >
            <Show when={props.item.running} fallback={<Play size={11} />}>
              <Square size={10} class="text-[var(--danger-text)]" />
            </Show>
          </button>
          <button
            type="button"
            disabled={busy()}
            onClick={(ev) => void toggleEnabled(ev)}
            title={props.item.enabled ? t("schedules.pausedLabel") : t("schedules.enabledLabel")}
            class={`inline-flex h-5 w-5 items-center justify-center rounded cursor-pointer disabled:opacity-40 ${
              props.item.enabled
                ? "text-[var(--ok-text)] hover:bg-[var(--bg-panel)]"
                : "text-[var(--text-faint)] hover:bg-[var(--bg-panel)] hover:text-[var(--text-primary)]"
            }`}
          >
            <Show when={props.item.enabled} fallback={<Power size={10} />}>
              <Pause size={10} />
            </Show>
          </button>
          <Show when={targetSession()}>
            <button
              type="button"
              onClick={(ev) => {
                ev.stopPropagation();
                void openScheduleRunSession(props.item);
              }}
              title={t("schedules.openSessionBtn")}
              class="inline-flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:bg-[var(--bg-panel)] hover:text-[var(--accent-primary)] cursor-pointer"
            >
              <ExternalLink size={11} />
            </button>
          </Show>
        </div>
      </div>

      <div class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10.5px] text-[var(--text-muted)]">
        <span class="font-mono">{formatScheduleTrigger(props.item.trigger)}</span>
        <span>·</span>
        <span>{props.item.sessionID ? t("schedules.boundTag") : t("schedules.freshTag")}</span>
        <Show when={props.item.skillSelection?.name}>
          <span>·</span>
          <span class="truncate max-w-[110px]">
            {t("schedules.skillTag", { name: props.item.skillSelection!.name })}
          </span>
        </Show>
        <span
          class={`ml-auto font-medium ${
            !props.item.enabled && !props.item.running
              ? "text-[var(--text-faint)]"
              : scheduleStatusTone(props.item.lastOutcome, props.item.running)
          }`}
        >
          {!props.item.enabled && !props.item.running
            ? t("schedules.pausedLabel")
            : scheduleStatusLabel(props.item.lastOutcome, props.item.running)}
        </span>
      </div>

      <Show when={props.item.enabled && props.item.nextRunAt}>
        <div class="mt-0.5 text-[10px] text-[var(--text-faint)]">
          {t("schedules.nextAt", { time: formatShortTimestamp(props.item.nextRunAt) })}
        </div>
      </Show>
      <Show when={!props.item.enabled && props.item.pausedReason}>
        <div class="mt-0.5 truncate text-[10px] text-[var(--status-warning)]">
          {t("schedules.pausedReasonPrefix", { reason: props.item.pausedReason! })}
        </div>
      </Show>
      <Show when={props.item.lastSummary && props.item.alertUnseen}>
        <p class="mt-1 line-clamp-2 text-[10.5px] text-[var(--text-secondary)]">
          {props.item.lastSummary}
        </p>
      </Show>
    </div>
  );
}

export default function ScheduledTasksSection() {
  const dir = createMemo(() => activeDirectory() || workspace.rootDir || "");
  const items = createMemo(() => schedulesForWorkspace(dir(), workspace.activeProjectID || undefined));

  return (
    <Show when={items().length > 0}>
      <section class="mb-3">
        <div class="mb-1.5 flex items-center justify-between gap-1.5 px-1">
          <div class="flex items-center gap-1.5 text-[11px] font-medium text-[var(--text-muted)]">
            <CalendarClock size={12} class="text-[var(--accent-primary)]" />
            <span>{t("schedules.sectionTitle")}</span>
            <span class="rounded bg-[var(--bg-inset)] px-1 font-mono text-[10px]">
              {items().length}
            </span>
          </div>
          <button
            type="button"
            onClick={() => openScheduleModal({ directory: dir(), projectID: workspace.activeProjectID || undefined, createNew: true })}
            class="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10.5px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <Plus size={11} />
            <span>{t("schedules.newBtn")}</span>
          </button>
        </div>

        <div class="space-y-1.5">
          <For each={items()}>
            {(item) => <ScheduleCompactRow item={item} />}
          </For>
        </div>
      </section>
    </Show>
  );
}
