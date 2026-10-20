// web/src/components/timeline/SessionGoalBar.tsx
import { Show } from "solid-js";
import { CheckCircle2, LoaderCircle, OctagonAlert, Pause, Play, Target, X } from "lucide-solid";
import {
  clearSessionGoal,
  pauseSessionGoal,
  resumeSessionGoal,
  sessionGoal,
  workspace,
  type GoalPauseReason,
  type SessionGoal,
} from "../../store/workspace";
import { t } from "../../i18n";

function pauseReasonText(reason: GoalPauseReason): string {
  switch (reason) {
    case "stopped":
      return t("timeline.goalReasonStopped");
    case "error":
      return t("timeline.goalReasonError");
    case "no-progress":
      return t("timeline.goalReasonNoProgress");
    case "budget":
      return t("timeline.goalReasonBudget");
    case "manual":
      return t("timeline.goalReasonManual");
    case "restart":
      return t("timeline.goalReasonRestart");
  }
}

function statusLabel(goal: SessionGoal): string {
  switch (goal.status) {
    case "active":
      return t("timeline.goalStatusActive");
    case "paused":
      return t("timeline.goalStatusPaused");
    case "blocked":
      return t("timeline.goalStatusBlocked");
    case "complete":
      return t("timeline.goalStatusComplete");
  }
}

function detailText(goal: SessionGoal): string {
  if (goal.status === "paused" && goal.reason && isPauseReason(goal.reason)) {
    return pauseReasonText(goal.reason);
  }
  return goal.reason || "";
}

function isPauseReason(value: string): value is GoalPauseReason {
  return ["stopped", "error", "no-progress", "budget", "manual", "restart"].includes(value);
}

/** Status strip above the composer for the active session's goal. */
export default function SessionGoalBar() {
  const activeID = () => workspace.activeSessionID ?? "";
  const goal = () => sessionGoal(activeID());

  return (
    <Show when={goal()}>
      {(g) => (
        <div class="mx-auto mb-1.5 flex w-full items-center gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/80 px-3 py-1.5 text-[12px]">
          <Target size={13} class="shrink-0 text-[var(--accent-primary)]" />
          <span class="shrink-0 font-semibold text-[var(--text-secondary)]">{t("timeline.goalLabel")}</span>
          <span class="min-w-0 flex-1 truncate text-[var(--text-primary)]" title={g().objective}>
            {g().objective}
          </span>
          <span class="inline-flex shrink-0 items-center gap-1 rounded-full border border-[var(--border-subtle)] px-2 py-0.5 text-[11px] font-medium text-[var(--text-secondary)]">
            <Show when={g().status === "active"}>
              <LoaderCircle size={10} class="animate-spin text-[var(--accent-primary)]" />
            </Show>
            <Show when={g().status === "complete"}>
              <CheckCircle2 size={10} class="text-[var(--ok-text)]" />
            </Show>
            <Show when={g().status === "blocked"}>
              <OctagonAlert size={10} class="text-[var(--danger-text)]" />
            </Show>
            <span>{statusLabel(g())}</span>
          </span>
          <Show when={detailText(g())}>
            <span class="hidden min-w-0 max-w-[260px] truncate text-[var(--text-muted)] md:inline" title={detailText(g())}>
              {detailText(g())}
            </span>
          </Show>
          <Show
            when={g().status === "active"}
            fallback={
              <button
                type="button"
                onClick={() => void resumeSessionGoal(activeID())}
                class="inline-flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 font-medium text-[var(--accent-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                <Play size={11} />
                <span>{g().status === "complete" ? t("timeline.goalRestart") : t("timeline.goalResume")}</span>
              </button>
            }
          >
            <button
              type="button"
              onClick={() => pauseSessionGoal(activeID(), "manual")}
              class="inline-flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
            >
              <Pause size={11} />
              <span>{t("timeline.goalPause")}</span>
            </button>
          </Show>
          <button
            type="button"
            onClick={() => clearSessionGoal(activeID())}
            class="inline-flex shrink-0 items-center gap-1 rounded-lg px-2 py-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <X size={11} />
            <span>{t("timeline.goalClear")}</span>
          </button>
        </div>
      )}
    </Show>
  );
}
