// web/src/components/layout/SessionNavItem.tsx
import { createEffect, createMemo, createSignal, onCleanup, Show } from "solid-js";
import { Pencil, Square } from "lucide-solid";
import {
  isPlaceholderSessionTitle,
  renameSessionTitle,
  sessionDirectory,
  setWorkspace,
  workspace,
} from "../../store/workspace";
import { stopSessionTask } from "../../store/workspace-session-messages";
import type { SessionInfo } from "../../services/sse-types";
import { Icon, Spinner } from "../icons";
import { downloadSessionExport } from "../timeline/session-export";
import { t } from "../../i18n";
import { badgeFor } from "./left-nav-helpers";
import SessionPullRequestPill from "./SessionPullRequestPill";
import { fetchPullRequests, type ForgePullRequest } from "../../services/forge";
import { SessionRenamePopover } from "./SessionRenamePopover";
import {
  formatCompactRelativeTime,
  formatExactTime,
  useNowTicker,
} from "./session-time-helpers";

export interface SessionNavItemProps {
  session: SessionInfo;
  active: boolean;
  canDelete: boolean;
  isArchived?: boolean;
  showBranchIcon?: boolean;
  pendingSessions: Set<string>;
  onSelect: () => void;
  onArchive?: (ev: MouseEvent, sessionID: string) => void;
  onUnarchive?: (ev: MouseEvent, sessionID: string) => void;
  onDiscard: (ev: MouseEvent, sessionID: string) => Promise<void> | void;
}

export function SessionNavItem(props: SessionNavItemProps) {
  const [editing, setEditing] = createSignal(false);
  const [confirmingDelete, setConfirmingDelete] = createSignal(false);
  const [deleting, setDeleting] = createSignal(false);
  let rowRef: HTMLDivElement | undefined;

  const nowTick = useNowTicker(30000);
  const isEffectiveActive = () => props.active;
  const status = () => workspace.statusBySession[props.session.id]?.type;
  const isBusy = () => status() === "busy" || status() === "retry";
  const isInterrupted = () =>
    !isBusy() && Boolean(workspace.interruptedBySession[props.session.id]);
  const isPaused = () =>
    !isBusy() && workspace.interruptedBySession[props.session.id] === "paused";
  const queuedCount = () =>
    workspace.queuedPromptsBySession[props.session.id]?.length ?? 0;
  const badge = () => badgeFor(props.session.id, props.pendingSessions, status());
  const wt = () => workspace.worktreeBySession[props.session.id];
  const [pullRequest, setPullRequest] = createSignal<ForgePullRequest | null>(null);
  let pullRequestGeneration = 0;
  const pullRequestDirectory = createMemo(
    () => wt()?.directory || sessionDirectory(props.session.id) || workspace.rootDir
  );
  const pullRequestBranch = createMemo(() => wt()?.branch || props.session.branch || "");

  createEffect(() => {
    const directory = pullRequestDirectory();
    const branch = pullRequestBranch();
    const current = ++pullRequestGeneration;
    setPullRequest(null);
    if (!directory || !branch || props.isArchived) return;

    let disposed = false;
    fetchPullRequests(directory)
      .then((response) => {
        if (disposed || current !== pullRequestGeneration) return;
        setPullRequest(response.available ? response.pullRequests[branch] ?? null : null);
      })
      .catch(() => {
        if (!disposed && current === pullRequestGeneration) setPullRequest(null);
      });
    onCleanup(() => {
      disposed = true;
    });
  });

  createEffect(() => {
    if (isEffectiveActive() && workspace.unreadCompletedBySession[props.session.id]) {
      setWorkspace("unreadCompletedBySession", props.session.id, false);
    }
  });

  const finishedTimestamp = () => {
    const local = workspace.sessionFinishedAt[props.session.id];
    const updated = props.session.time?.updated;
    const created = props.session.time?.created;
    return Math.max(local || 0, updated || 0, created || 0);
  };

  const timeText = () => formatCompactRelativeTime(finishedTimestamp(), nowTick());
  const fullDateTooltip = () => formatExactTime(finishedTimestamp());

  createEffect(() => {
    if (!confirmingDelete()) return;
    const handlePointerDown = (ev: MouseEvent) => {
      if (rowRef && !rowRef.contains(ev.target as Node)) {
        setConfirmingDelete(false);
      }
    };
    const handleKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        setConfirmingDelete(false);
      }
    };
    window.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      window.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  const startEditing = (ev?: MouseEvent) => {
    ev?.stopPropagation();
    setConfirmingDelete(false);
    setEditing(true);
  };

  const commitEdit = (next: string) => {
    if (!editing()) return;
    setEditing(false);
    if (next && next !== props.session.title) {
      void renameSessionTitle(props.session.id, next);
    }
  };

  const [stopping, setStopping] = createSignal(false);

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

  const handleConfirmDelete = async (ev: MouseEvent) => {
    ev.stopPropagation();
    if (deleting()) return;
    setDeleting(true);
    try {
      await props.onDiscard(ev, props.session.id);
    } finally {
      setDeleting(false);
      setConfirmingDelete(false);
    }
  };

  return (
    <div
      ref={rowRef}
      class={`hoxi-press group/row relative flex items-center justify-between gap-1 rounded-lg px-2.5 py-1.5 cursor-pointer overflow-hidden ${
        confirmingDelete()
          ? "bg-[var(--danger-surface)] text-[var(--text-primary)] ring-1 ring-[var(--danger-border)]"
          : isEffectiveActive()
            ? "bg-[var(--bg-hover)] text-[var(--text-primary)] font-medium"
            : badge() === "completed"
              ? "text-[var(--text-primary)] font-medium hover:bg-[var(--bg-hover)]/60"
              : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]/60 hover:text-[var(--text-primary)]"
      }`}
      onClick={() => {
        if (!editing() && !confirmingDelete()) props.onSelect();
      }}
    >
      <div class="flex items-center gap-1.5 min-w-0 flex-1 mr-1">
        <Show when={badge() === "approval"}>
          <span class="shrink-0 w-2 h-2 rounded-full bg-[var(--warn)] animate-pulse" />
        </Show>
        <Show when={badge() === "completed"}>
          <span
            class="shrink-0 w-2 h-2 rounded-full bg-blue-500 shadow-[0_0_6px_rgba(59,130,246,0.5)]"
            title={t("timeline.turnCompleted")}
          />
        </Show>
        <Show when={badge() === "idle" && isInterrupted() && !isPaused()}>
          <span
            class="shrink-0 w-1.5 h-1.5 rounded-full bg-[var(--warn)]/75"
            title={t("timeline.turnPaused")}
          />
        </Show>
        <Show when={isPaused()}>
          <span class="shrink-0 rounded border border-[var(--border-subtle)] bg-[var(--bg-hover)] px-1 py-0.5 text-[11px] font-medium leading-none text-[var(--text-muted)]">
            {t("timeline.turnPaused")}
          </span>
        </Show>
        <span
          class={`session-row-title truncate text-[15px] flex-1 leading-snug ${
            props.isArchived ? "text-[var(--text-muted)]" : ""
          }`}
          title={props.session.title || ""}
          onDblClick={(ev) => startEditing(ev)}
        >
          {isPlaceholderSessionTitle(props.session.title)
            ? t("leftNav.newThread")
            : props.session.title}
        </span>
      </div>

      {/* Static Badges (Branch, Queue, Time or Loading) shown when not hovered and not confirming delete */}
      <Show when={!confirmingDelete()}>
        <div class="flex items-center gap-1.5 shrink-0 text-[12px] font-mono group-hover/row:opacity-0 group-hover/row:invisible group-focus-within/row:opacity-0 group-focus-within/row:invisible transition-opacity duration-120 ease-out">
          <Show when={pullRequest()}>
            {(pr) => <SessionPullRequestPill pr={pr()} />}
          </Show>

          <Show when={wt()?.isolated && wt()?.branch}>
            <span class="inline-flex items-center gap-0.5 text-[var(--text-faint)] truncate max-w-[68px]">
              <Show when={props.showBranchIcon}>
                <Icon name="branch" size="small" class="w-3 h-3" />
              </Show>
              <span class="truncate">{wt()?.branch}</span>
            </span>
          </Show>

          <Show when={queuedCount() > 0}>
            <span
              class="inline-flex items-center rounded px-1 py-0.2 text-[11px] font-medium bg-[var(--accent-surface)] text-[var(--accent-primary)] border border-[var(--accent-border)]/60 shrink-0"
              title={t("timeline.queueCountBadge", { count: queuedCount() })}
            >
              +{queuedCount()}
            </span>
          </Show>

          {/* Right-side status: Spinner icon when running / busy; relative time when finished / idle */}
          <Show
            when={!isBusy()}
            fallback={
              <span
                class="inline-flex items-center text-[var(--status-running)] shrink-0 select-none"
                title={t("common.working")}
              >
                <Spinner class="w-3 h-3 text-[var(--status-running)]" />
              </span>
            }
          >
            <Show when={timeText()}>
              <span
                class="text-[11px] text-[var(--text-faint)] tabular-nums font-mono shrink-0 select-none"
                title={fullDateTooltip()}
              >
                {timeText()}
              </span>
            </Show>
          </Show>
        </div>
      </Show>

      {/* Hover Actions / Delete Gate Overlay */}
      <Show when={!editing()}>
        <div
          class={`absolute right-1 top-1/2 -translate-y-1/2 flex items-center z-10 transition-opacity duration-120 ease-out ${
            confirmingDelete()
              ? "visible opacity-100 pointer-events-auto"
              : "invisible opacity-0 pointer-events-none group-hover/row:visible group-hover/row:opacity-100 group-hover/row:pointer-events-auto group-focus-within/row:visible group-focus-within/row:opacity-100 group-focus-within/row:pointer-events-auto"
          }`}
          onClick={(ev) => ev.stopPropagation()}
        >
          {/* Soft horizontal feather mask to prevent hard visual cut over long session titles */}
          <div
            aria-hidden="true"
            class="pointer-events-none h-6 w-5 -mr-1.5 bg-gradient-to-l from-[var(--bg-canvas)]/75 via-[var(--bg-canvas)]/35 to-transparent rounded-l-lg"
          />

          <Show
            when={!confirmingDelete()}
            fallback={
              <div class="session-action-danger-glass flex items-center gap-1 rounded-lg px-1.5 py-0.5">
                <button
                  type="button"
                  disabled={deleting()}
                  onClick={(ev) => void handleConfirmDelete(ev)}
                  class="inline-flex items-center gap-0.5 rounded-md bg-[var(--danger-text)] px-1.5 py-0.5 text-[11px] font-medium text-white hover:opacity-90 transition-opacity cursor-pointer shadow-xs"
                  title={t("leftNav.discardConfirm")}
                >
                  <Show
                    when={!deleting()}
                    fallback={<Spinner class="w-2.5 h-2.5 text-white" />}
                  >
                    <span>{t("leftNav.confirmDelete")}</span>
                  </Show>
                </button>
                <button
                  type="button"
                  disabled={deleting()}
                  onClick={(ev) => {
                    ev.stopPropagation();
                    setConfirmingDelete(false);
                  }}
                  class="rounded-md px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                >
                  {t("leftNav.cancelDelete")}
                </button>
              </div>
            }
          >
            <div class="session-action-glass flex items-center gap-0.5 rounded-lg px-1 py-0.5">
              <button
                type="button"
                class="inline-flex h-5 w-5 items-center justify-center rounded-md text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                onClick={(ev) => startEditing(ev)}
                title={t("leftNav.renameSessionTooltip")}
              >
                <Pencil size={11} class="shrink-0 block" />
              </button>
              <button
                type="button"
                class="inline-flex h-5 w-5 items-center justify-center rounded-md text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                onClick={(ev) => {
                  ev.stopPropagation();
                  downloadSessionExport(props.session.id);
                }}
                title={t("leftNav.exportSessionTooltip")}
              >
                <Icon name="download" size="small" class="w-3 h-3" />
              </button>
              <Show
                when={!isBusy()}
                fallback={
                  <button
                    type="button"
                    disabled={stopping()}
                    onClick={(ev) => void handleStop(ev)}
                    class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--danger-text)] hover:bg-[var(--danger-surface)] transition-colors cursor-pointer disabled:opacity-50"
                    title={t("leftNav.stopTooltip")}
                  >
                    <Show
                      when={!stopping()}
                      fallback={<Spinner class="w-2.5 h-2.5 text-[var(--danger-text)]" />}
                    >
                      <Square size={9.5} class="shrink-0 fill-current" />
                    </Show>
                    <span>{t("leftNav.stopSession")}</span>
                  </button>
                }
              >
                <Show when={!props.isArchived && props.onArchive}>
                  <button
                    type="button"
                    class="inline-flex h-5 w-5 items-center justify-center rounded-md text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                    onClick={(ev) => {
                      ev.stopPropagation();
                      props.onArchive?.(ev, props.session.id);
                    }}
                    title={t("leftNav.archiveSessionTooltip")}
                  >
                    <Icon name="archive" size="small" class="w-3 h-3" />
                  </button>
                </Show>

                <Show when={props.isArchived && props.onUnarchive}>
                  <button
                    type="button"
                    class="inline-flex h-5 w-5 items-center justify-center rounded-md text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                    onClick={(ev) => {
                      ev.stopPropagation();
                      props.onUnarchive?.(ev, props.session.id);
                    }}
                    title={t("leftNav.unarchiveSessionTooltip")}
                  >
                    <Icon name="reset" size="small" class="w-3 h-3" />
                  </button>
                </Show>

                <Show when={props.canDelete}>
                  <button
                    type="button"
                    class="inline-flex h-5 w-5 items-center justify-center rounded-md text-[var(--text-secondary)] hover:text-[var(--danger-text)] hover:bg-[var(--danger-surface)] transition-colors cursor-pointer"
                    onClick={(ev) => {
                      ev.stopPropagation();
                      setConfirmingDelete(true);
                    }}
                    title={t("leftNav.deleteSessionTooltip")}
                  >
                    <Icon name="trash" size="small" class="w-3 h-3" />
                  </button>
                </Show>
              </Show>
            </div>
          </Show>
        </div>
      </Show>
      <Show when={editing() && rowRef}>
        <SessionRenamePopover
          anchor={rowRef!}
          title={props.session.title || ""}
          onSave={commitEdit}
          onClose={() => setEditing(false)}
        />
      </Show>
    </div>
  );
}
