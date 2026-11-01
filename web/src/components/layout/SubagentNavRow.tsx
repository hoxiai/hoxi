import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { Bot, Square } from "lucide-solid";
import {
  resolveSubagentTitle,
  stripSubagentTitleSuffix,
  setWorkspace,
  workspace,
} from "../../store/workspace";
import { stopSessionTask } from "../../store/workspace-session-messages";
import type { SessionInfo } from "../../services/sse-types";
import { ArcSpinner, Spinner } from "../icons";
import { t } from "../../i18n";
import { badgeFor } from "./left-nav-helpers";
import {
  formatCompactRelativeTime,
  formatExactTime,
} from "./session-time-helpers";

export interface SubagentNavRowProps {
  parentSessionID: string;
  session: SessionInfo;
  active: boolean;
  pendingSessions: Set<string>;
  nowTick: number;
  onSelect: () => void;
}

export function SubagentNavRow(props: SubagentNavRowProps) {
  let rowRef: HTMLDivElement | undefined;
  const [stopping, setStopping] = createSignal(false);

  const status = () => workspace.statusBySession[props.session.id]?.type;
  const isBusy = () => status() === "busy" || status() === "retry";
  const isInterrupted = () =>
    !isBusy() && Boolean(workspace.interruptedBySession[props.session.id]);
  const badge = () => badgeFor(props.session.id, props.pendingSessions, status());

  createEffect(() => {
    if (props.active && workspace.unreadCompletedBySession[props.session.id]) {
      setWorkspace("unreadCompletedBySession", props.session.id, false);
    }
  });

  createEffect((prevActive) => {
    const active = props.active;
    if (active && !prevActive && rowRef && typeof rowRef.scrollIntoView === "function") {
      const rafId = requestAnimationFrame(() => {
        rowRef?.scrollIntoView({ block: "nearest" });
      });
      onCleanup(() => cancelAnimationFrame(rafId));
    }
    return active;
  }, false);

  const finishedTimestamp = () => {
    const local = workspace.sessionFinishedAt[props.session.id];
    const updated = props.session.time?.updated;
    const created = props.session.time?.created;
    return Math.max(local || 0, updated || 0, created || 0);
  };

  const timeText = () => formatCompactRelativeTime(finishedTimestamp(), props.nowTick);
  const fullDateTooltip = () => formatExactTime(finishedTimestamp());

  const displayTitle = () =>
    resolveSubagentTitle(props.parentSessionID, props.session.id, props.session.title).label ||
    stripSubagentTitleSuffix(props.session.title) ||
    props.session.title ||
    t("leftNav.untitled");

  const handleStop = async (ev: MouseEvent) => {
    ev.stopPropagation();
    if (stopping()) return;
    setStopping(true);
    try {
      await stopSessionTask(props.session.id);
    } finally {
      setStopping(false);
    }
  };

  return (
    <div
      ref={rowRef}
      data-subagent-row={props.session.id}
      class={`group/subrow relative flex items-center justify-between gap-1 rounded-md px-2 py-1 cursor-pointer overflow-hidden transition-colors duration-120 ${
        props.active
          ? "bg-[var(--bg-hover)] text-[var(--text-primary)] font-medium"
          : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]/60 hover:text-[var(--text-primary)]"
      }`}
      onClick={() => props.onSelect()}
    >
      <div class="flex items-center gap-1.5 min-w-0 flex-1 mr-1">
        <Show
          when={badge() !== "approval"}
          fallback={
            <span class="shrink-0 w-1.5 h-1.5 rounded-full bg-[var(--warn)] animate-pulse" />
          }
        >
          <Show
            when={!isInterrupted()}
            fallback={
              <span
                class="shrink-0 w-1.5 h-1.5 rounded-full bg-[var(--warn)]/75"
                title={t("timeline.turnPaused")}
              />
            }
          >
            <Bot
              size={11.5}
              class={`shrink-0 ${
                isBusy()
                  ? "text-[var(--accent-primary)]"
                  : props.active
                    ? "text-[var(--text-secondary)]"
                    : "text-[var(--text-faint)]"
              }`}
            />
          </Show>
        </Show>

        <span
          class="truncate text-[12.5px] leading-snug flex-1"
          title={props.session.title || displayTitle()}
        >
          {displayTitle()}
        </span>
      </div>

      <div
        class={`flex items-center gap-1 shrink-0 text-[11px] font-mono transition-opacity duration-120 ease-out ${
          isBusy()
            ? "group-hover/subrow:opacity-0 group-hover/subrow:invisible"
            : ""
        }`}
      >
        <Show
          when={!isBusy()}
          fallback={
            <span
              class="inline-flex items-center text-[var(--accent-primary)] shrink-0 select-none"
              title={
                badge() === "approval"
                  ? t("timeline.waitingApprovalBadge")
                  : t("common.working")
              }
            >
              <ArcSpinner active={badge() !== "approval"} />
            </span>
          }
        >
          <Show when={timeText()}>
            <span
              class="text-[10.5px] text-[var(--text-faint)] tabular-nums font-mono shrink-0 select-none"
              title={fullDateTooltip()}
            >
              {timeText()}
            </span>
          </Show>
        </Show>
      </div>

      <Show when={isBusy()}>
        <div
          class="absolute right-1 top-1/2 -translate-y-1/2 flex items-center z-10 invisible opacity-0 pointer-events-none group-hover/subrow:visible group-hover/subrow:opacity-100 group-hover/subrow:pointer-events-auto transition-opacity duration-120 ease-out"
          onClick={(ev) => ev.stopPropagation()}
        >
          <div class="session-action-glass flex items-center rounded-md px-1 py-0.5">
            <button
              type="button"
              disabled={stopping()}
              onClick={(ev) => void handleStop(ev)}
              class="inline-flex items-center gap-1 rounded px-1 py-0.5 text-[10.5px] font-medium text-[var(--text-secondary)] hover:text-[var(--danger-text)] hover:bg-[var(--danger-surface)] transition-colors cursor-pointer disabled:opacity-50"
              title={t("leftNav.stopTooltip")}
            >
              <Show
                when={!stopping()}
                fallback={<Spinner class="w-2.5 h-2.5 text-[var(--danger-text)]" />}
              >
                <Square size={8.5} class="shrink-0 fill-current" />
              </Show>
              <span>{t("leftNav.stopSession")}</span>
            </button>
          </div>
        </div>
      </Show>
    </div>
  );
}
