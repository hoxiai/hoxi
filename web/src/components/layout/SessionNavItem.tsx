// web/src/components/layout/SessionNavItem.tsx
import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { Pause, Pencil, Square } from "lucide-solid";
import {
  isPlaceholderSessionTitle,
  renameSessionTitle,
  workspace,
} from "../../store/workspace";
import { pauseSessionTask, stopSessionTask } from "../../store/workspace-session-messages";
import type { SessionInfo } from "../../services/sse-types";
import { DiffChanges, Icon, Spinner } from "../icons";
import { t } from "../../i18n";
import { badgeFor, diffStats } from "./left-nav-helpers";
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
  const [draftTitle, setDraftTitle] = createSignal("");
  const [confirmingDelete, setConfirmingDelete] = createSignal(false);
  const [deleting, setDeleting] = createSignal(false);
  let rowRef: HTMLDivElement | undefined;
  let inputRef: HTMLInputElement | undefined;

  const nowTick = useNowTicker(30000);
  const status = () => workspace.statusBySession[props.session.id]?.type;
  const isBusy = () => status() === "busy";
  const badge = () => badgeFor(props.session.id, props.pendingSessions, status());
  const wt = () => workspace.worktreeBySession[props.session.id];
  const stats = () => diffStats(props.session.id);

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
    setDraftTitle(props.session.title || "");
    setEditing(true);
    requestAnimationFrame(() => {
      inputRef?.focus();
      inputRef?.select();
    });
  };

  const commitEdit = () => {
    if (!editing()) return;
    setEditing(false);
    const next = draftTitle().trim();
    if (next && next !== props.session.title) {
      void renameSessionTitle(props.session.id, next);
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
      class={`group/row relative flex items-center justify-between gap-1 rounded-lg px-2.5 py-1.5 cursor-pointer transition-colors overflow-hidden ${
        confirmingDelete()
          ? "bg-[var(--danger-surface)] text-[var(--text-primary)] ring-1 ring-[var(--danger-border)]"
          : props.active
            ? "bg-[var(--bg-hover)] text-[var(--text-primary)] font-medium"
            : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]/60 hover:text-[var(--text-primary)]"
      }`}
      onClick={() => {
        if (!editing() && !confirmingDelete()) props.onSelect();
      }}
      onDblClick={(ev) => startEditing(ev)}
    >
      <div class="flex items-center gap-1.5 min-w-0 flex-1 mr-1">
        <Show when={badge() === "approval"}>
          <span class="shrink-0 w-2 h-2 rounded-full bg-[var(--warn)] animate-pulse" />
        </Show>
        <Show
          when={!editing()}
          fallback={
            <input
              ref={inputRef}
              type="text"
              value={draftTitle()}
              placeholder={t("leftNav.renamePlaceholder")}
              onInput={(ev) => setDraftTitle(ev.currentTarget.value)}
              onClick={(ev) => ev.stopPropagation()}
              onBlur={commitEdit}
              onKeyDown={(ev) => {
                if (ev.key === "Enter" && !ev.isComposing) {
                  ev.preventDefault();
                  commitEdit();
                } else if (ev.key === "Escape") {
                  ev.preventDefault();
                  setEditing(false);
                }
              }}
              class="flex-1 min-w-0 rounded border border-[var(--accent-border)] bg-[var(--bg-panel)] px-1.5 py-0.5 text-[13px] text-[var(--text-primary)] outline-none"
            />
          }
        >
          <span
            class={`truncate text-[13px] flex-1 leading-snug ${
              props.isArchived ? "text-[var(--text-muted)]" : ""
            }`}
            title={props.session.title || ""}
          >
            {isPlaceholderSessionTitle(props.session.title)
              ? t("leftNav.newThread")
              : props.session.title}
          </span>
        </Show>
      </div>

      {/* Static Badges (Branch, Diff, Time or Loading) shown when not hovered and not confirming delete */}
      <Show when={!confirmingDelete() && !editing()}>
        <div class="flex items-center gap-1.5 shrink-0 text-[11.5px] font-mono group-hover/row:opacity-0 transition-opacity">
          <Show when={wt()?.isolated && wt()?.branch}>
            <span class="inline-flex items-center gap-0.5 text-[var(--text-faint)] truncate max-w-[68px]">
              <Show when={props.showBranchIcon}>
                <Icon name="branch" size="small" class="w-3 h-3" />
              </Show>
              <span class="truncate">{wt()?.branch}</span>
            </span>
          </Show>

          <Show when={stats()}>
            <DiffChanges changes={stats()!} />
          </Show>

          {/* Right-side status: Loading when running / busy; relative time when finished / idle */}
          <Show
            when={!isBusy()}
            fallback={
              <span
                class="inline-flex items-center gap-1 text-[11px] text-[var(--status-running)] font-mono shrink-0 select-none"
                title={t("leftNav.sessionLoading")}
              >
                <Spinner class="w-2.5 h-2.5 text-[var(--status-running)]" />
                <span>{t("leftNav.sessionLoading")}</span>
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

      {/* Hover Actions / Delete Gate Overlay: solid floating action pill covering text cleanly */}
      <Show when={!editing()}>
        <div
          class={`absolute right-1 top-1/2 -translate-y-1/2 flex items-center z-10 transition-all ${
            confirmingDelete()
              ? "opacity-100 pointer-events-auto"
              : "opacity-0 pointer-events-none group-hover/row:opacity-100 group-hover/row:pointer-events-auto"
          }`}
          onClick={(ev) => ev.stopPropagation()}
        >
          <Show
            when={!confirmingDelete()}
            fallback={
              <div class="flex items-center gap-1 rounded-md border border-[var(--danger-border)] bg-[var(--danger-surface)] px-1.5 py-0.5 shadow-md">
                <button
                  type="button"
                  disabled={deleting()}
                  onClick={(ev) => void handleConfirmDelete(ev)}
                  class="inline-flex items-center gap-0.5 rounded bg-[var(--danger-text)] px-1.5 py-0.5 text-[11px] font-medium text-white hover:opacity-90 transition-opacity cursor-pointer shadow-xs"
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
                  class="rounded px-1.5 py-0.5 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                >
                  {t("leftNav.cancelDelete")}
                </button>
              </div>
            }
          >
            <div class="flex items-center gap-0.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-1 py-0.5 shadow-md">
              <button
                type="button"
                class="inline-flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                onClick={(ev) => startEditing(ev)}
                title={t("leftNav.renameSessionTooltip")}
              >
                <Pencil size={11} class="shrink-0 block" />
              </button>

              <Show when={!props.isArchived && props.onArchive}>
                <button
                  type="button"
                  class="inline-flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
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
                  class="inline-flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
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
                  class="inline-flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:text-[var(--danger-text)] hover:bg-[var(--danger-surface)] transition-colors cursor-pointer"
                  onClick={(ev) => {
                    ev.stopPropagation();
                    setConfirmingDelete(true);
                  }}
                  title={t("leftNav.deleteSessionTooltip")}
                >
                  <Icon name="trash" size="small" class="w-3 h-3" />
                </button>
              </Show>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}
