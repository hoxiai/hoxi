// web/src/components/schedules/schedule-helpers.ts
import { t } from "../../i18n";
import type { ScheduleItem, ScheduleTriggerSpec } from "../../store/workspace";

export interface ScheduleTemplate {
  id: string;
  name: () => string;
  prompt: () => string;
  trigger: ScheduleTriggerSpec;
  mode: "build" | "plan";
  sandboxMode: "local" | "worktree";
}

export const SCHEDULE_TEMPLATES: ScheduleTemplate[] = [
  {
    id: "pr-patrol",
    name: () => t("schedules.templatePRPatrol"),
    prompt: () => t("schedules.templatePRPatrolPrompt"),
    trigger: { kind: "cron", cron: "0 9 * * 1-5", misfire: "skip" },
    mode: "plan",
    sandboxMode: "local",
  },
  {
    id: "repo-health",
    name: () => t("schedules.templateRepoHealth"),
    prompt: () => t("schedules.templateRepoHealthPrompt"),
    trigger: { kind: "cron", cron: "30 8 * * 1-5", misfire: "skip" },
    mode: "build",
    sandboxMode: "local",
  },
  {
    id: "daily-summary",
    name: () => t("schedules.templateDailySummary"),
    prompt: () => t("schedules.templateDailySummaryPrompt"),
    trigger: { kind: "cron", cron: "0 18 * * 1-5", misfire: "skip" },
    mode: "plan",
    sandboxMode: "local",
  },
];

export function formatScheduleTrigger(trigger: ScheduleTriggerSpec): string {
  if (trigger.kind === "interval") {
    const mins = trigger.intervalMinutes || 30;
    if (mins % 60 === 0 && mins >= 60) {
      return `${mins / 60}h`;
    }
    return `${mins}m`;
  }
  return trigger.cron || "0 9 * * 1-5";
}

export function formatShortTimestamp(ms?: number): string {
  if (!ms || !Number.isFinite(ms)) return "";
  const d = new Date(ms);
  const now = new Date();
  const sameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate();
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  if (sameDay) return `${hh}:${mm}`;
  const mo = String(d.getMonth() + 1).padStart(2, "0");
  const da = String(d.getDate()).padStart(2, "0");
  return `${mo}-${da} ${hh}:${mm}`;
}

export function scheduleStatusLabel(status?: string, running?: boolean): string {
  if (running || status === "running") return t("schedules.runningLabel");
  switch (status) {
    case "ok":
      return t("schedules.statusOK");
    case "alert":
      return t("schedules.statusAlert");
    case "blocked":
      return t("schedules.statusBlocked");
    case "error":
      return t("schedules.statusError");
    case "skipped_busy":
      return t("schedules.statusSkippedBusy");
    case "interrupted":
      return t("schedules.statusInterrupted");
    default:
      return t("schedules.neverRun");
  }
}

export function scheduleStatusTone(status?: string, running?: boolean): string {
  if (running || status === "running") return "text-[var(--accent-primary)]";
  switch (status) {
    case "ok":
      return "text-[var(--ok-text)]";
    case "alert":
    case "blocked":
      return "text-[var(--status-warning)]";
    case "error":
      return "text-[var(--danger-text)]";
    default:
      return "text-[var(--text-muted)]";
  }
}

export function scheduleNeedsAttention(item: ScheduleItem): boolean {
  return (
    Boolean(item.alertUnseen) ||
    item.lastOutcome === "alert" ||
    item.lastOutcome === "blocked" ||
    item.lastOutcome === "error"
  );
}
