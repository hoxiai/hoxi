// web/src/components/tasks/TaskOverviewView.tsx
import {
  createComputed,
  createEffect,
  createMemo,
  createSignal,
  For,
  Match,
  onCleanup,
  onMount,
  Show,
  Switch,
} from "solid-js";
import { createStore, reconcile } from "solid-js/store";
import { Portal } from "solid-js/web";
import {
  Bot,
  CalendarClock,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CircleDashed,
  Link2,
  ListTodo,
  LoaderCircle,
  MinusCircle,
  MoreHorizontal,
  Undo2,
  Workflow,
  XCircle,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  activeDirectory,
  buildTaskOverview,
  canDismissTaskOverviewNode,
  collectAncestorSessionIDs,
  dismissTaskOverviewNode,
  intakeLinksModalOpen,
  intakeTasks,
  isTaskNodeExpanded,
  loadIntakeTasks,
  loadSchedules,
  loadTaskOverviewPlans,
  openScheduleModal,
  resetIntakeState,
  resetTaskOverviewPlans,
  restoreTaskOverviewNode,
  schedulesForWorkspace,
  selectSession,
  setIntakeLinksModalOpen,
  startIntakeTasksPolling,
  startSchedulesPolling,
  startTaskOverviewPolling,
  taskOverviewFailed,
  taskOverviewLoading,
  taskOverviewPlans,
  toggleTaskNodeExpanded,
  workspace,
  type TaskGroupID,
  type TaskNodeStatus,
  type TaskOverviewGroup,
  type TaskOverviewNode,
} from "../../store/workspace";
import { useAnchoredPopover } from "../layout/anchored-popover";
import { surfaceExtension } from "../layout/surface-extension";
import ScheduledTasksSection from "../schedules/ScheduledTasksSection";
import IntakeLinksModal from "./IntakeLinksModal";
import IntakeTasksSection from "./IntakeTasksSection";

const statusTextColor: Record<TaskNodeStatus, string> = {
  queued: "text-[var(--text-muted)]",
  pending: "text-[var(--text-muted)]",
  running: "text-[var(--accent-primary)]",
  succeeded: "text-[var(--ok-text)]",
  failed: "text-[var(--danger-text)]",
  blocked: "text-[var(--danger-text)]",
  cancelled: "text-[var(--text-muted)]",
};

function statusLabel(status: TaskNodeStatus): string {
  switch (status) {
    case "queued":
      return t("timeline.convoyQueued");
    case "pending":
      return t("tasks.statusPending");
    case "running":
      return t("timeline.convoyRunning");
    case "succeeded":
      return t("timeline.convoySucceeded");
    case "failed":
      return t("timeline.convoyFailed");
    case "blocked":
      return t("timeline.convoyBlocked");
    default:
      return t("timeline.convoyCancelled");
  }
}

function groupLabel(id: TaskGroupID): string {
  switch (id) {
    case "convoy":
      return t("tasks.groupConvoy");
    case "subagents":
      return t("tasks.groupSubagents");
    default:
      return t("tasks.groupTodos");
  }
}

function TaskLoadingIcon(props: { class?: string }) {
  let icon: SVGSVGElement | undefined;

  const restartAnimation = () => {
    if (!icon || (typeof document !== "undefined" && document.visibilityState === "hidden")) return;
    // WebKit can leave a CSS animation on its last compositor frame after the
    // desktop window regains focus. Force a style/layout boundary so it starts
    // from a fresh animation timeline instead of appearing stuck.
    icon.classList.remove("animate-spin");
    void icon.getBoundingClientRect().width;
    icon.classList.add("animate-spin");
  };

  onMount(() => {
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") restartAnimation();
    };
    window.addEventListener("focus", restartAnimation);
    document.addEventListener("visibilitychange", onVisibilityChange);
    onCleanup(() => {
      window.removeEventListener("focus", restartAnimation);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    });
  });

  return <LoaderCircle ref={icon} size={13} class={`shrink-0 animate-spin ${props.class ?? ""}`} />;
}

function StatusIcon(props: { status: TaskNodeStatus }) {
  return (
    <Switch>
      <Match when={props.status === "running"}>
        <TaskLoadingIcon class="text-[var(--accent-primary)]" />
      </Match>
      <Match when={props.status === "succeeded"}>
        <CheckCircle2 size={12} class="shrink-0 text-[var(--ok-text)]" />
      </Match>
      <Match when={props.status === "failed" || props.status === "blocked"}>
        <XCircle size={12} class="shrink-0 text-[var(--danger-text)]" />
      </Match>
      <Match when={props.status === "queued" || props.status === "pending"}>
        <CircleDashed size={12} class="shrink-0 text-[var(--text-muted)]" />
      </Match>
      <Match when={props.status === "cancelled"}>
        <MinusCircle size={12} class="shrink-0 text-[var(--text-muted)]" />
      </Match>
    </Switch>
  );
}

function TaskRowActions(props: { onRemove: () => void }) {
  const [open, setOpen] = createSignal(false);
  const [panel, setPanel] = createSignal<HTMLDivElement>();
  let trigger: HTMLButtonElement | undefined;
  const anchored = useAnchoredPopover({ anchor: () => trigger, panel: () => open() ? panel() : undefined });

  createEffect(() => {
    if (!open()) {
      setPanel(undefined);
      return;
    }
    const menu = panel();
    if (!menu) return;
    menu.querySelector("button")?.focus();
    const outside = (event: Event) => {
      if (event.target instanceof Node && !trigger?.contains(event.target) && !menu.contains(event.target)) {
        setOpen(false);
      }
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      event.preventDefault();
      setOpen(false);
      trigger?.focus();
    };
    window.addEventListener("pointerdown", outside);
    window.addEventListener("focusin", outside);
    window.addEventListener("keydown", escape);
    onCleanup(() => {
      window.removeEventListener("pointerdown", outside);
      window.removeEventListener("focusin", outside);
      window.removeEventListener("keydown", escape);
    });
  });

  return (
    <>
      <button
        ref={trigger}
        type="button"
        title={t("tasks.rowActions")}
        aria-label={t("tasks.rowActions")}
        aria-haspopup="menu"
        aria-expanded={open()}
        onClick={() => setOpen(!open())}
        class="inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded text-[var(--text-muted)] hover:bg-[var(--bg-inset)] hover:text-[var(--text-primary)]"
      >
        <MoreHorizontal size={14} />
      </button>
      <Show when={open()}>
        <Portal>
          <div
            ref={setPanel}
            role="menu"
            class="hoxi-popover fixed z-[100] w-48 max-w-[calc(100vw-16px)] rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1"
            style={anchored.positionStyle()}
          >
            <button
              type="button"
              role="menuitem"
              onClick={() => { setOpen(false); props.onRemove(); }}
              class="w-full cursor-pointer rounded-lg px-2.5 py-1.5 text-left text-[12px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)]"
            >
              <span class="block font-medium">{t("tasks.removeFromList")}</span>
              <span class="block text-[11px] text-[var(--text-muted)]">{t("tasks.removeFromListHint")}</span>
            </button>
          </div>
        </Portal>
      </Show>
    </>
  );
}

function TaskNodeRow(props: { node: TaskOverviewNode; depth: number; onRemove: (node: TaskOverviewNode) => void }) {
  const expanded = () => isTaskNodeExpanded(props.node.id, props.depth);
  const hasChildren = () => props.node.children.length > 0;
  const openSession = () => {
    const id = props.node.sessionID;
    if (id) void selectSession(id);
  };

  return (
    <div>
      <div class="group flex items-center gap-1.5 rounded-lg px-1.5 py-1 hover:bg-[var(--bg-hover)]">
        <button
          type="button"
          onClick={() => toggleTaskNodeExpanded(props.node.id, props.depth)}
          class={`shrink-0 rounded p-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-inset)] hover:text-[var(--text-primary)] cursor-pointer ${
            hasChildren() ? "" : "invisible"
          }`}
        >
          {expanded() ? <ChevronDown size={11} /> : <ChevronRight size={11} />}
        </button>
        <StatusIcon status={props.node.status} />
        <Show
          when={props.node.sessionID}
          fallback={
            <span class="min-w-0 flex-1 truncate text-[12px] text-[var(--text-primary)]">
              {props.node.title}
            </span>
          }
        >
          <button
            type="button"
            onClick={openSession}
            title={t("tasks.openSession")}
            class="min-w-0 flex-1 truncate rounded text-left text-[12px] text-[var(--text-primary)] hover:text-[var(--accent-primary)] cursor-pointer"
          >
            {props.node.title}
          </button>
        </Show>
        <Show when={props.node.kind === "convoy" && props.node.detail}>
          <span class="hidden shrink-0 font-mono text-[10px] text-[var(--text-faint)] group-hover:inline">
            {props.node.detail}
          </span>
        </Show>
        <span class={`shrink-0 text-[10px] ${statusTextColor[props.node.status]}`}>
          {statusLabel(props.node.status)}
        </span>
        <Show when={canDismissTaskOverviewNode(props.node)}>
          <TaskRowActions onRemove={() => props.onRemove(props.node)} />
        </Show>
      </div>
      <Show when={props.node.dependsOn && props.node.dependsOn.length > 0}>
        <p class="pl-8 pr-2 text-[10px] text-[var(--text-faint)]">
          {t("tasks.dependsOn", { ids: (props.node.dependsOn ?? []).join(", ") })}
        </p>
      </Show>
      <Show when={expanded() && hasChildren()}>
        <div class="ml-3 border-l border-[var(--border-subtle)]/50 pl-1">
          <For each={props.node.children}>
            {(child) => <TaskNodeRow node={child} depth={props.depth + 1} onRemove={props.onRemove} />}
          </For>
        </div>
      </Show>
    </div>
  );
}

export default function TaskOverviewView() {
  const directory = createMemo(() => activeDirectory() ?? "");
  const [lastRemoval, setLastRemoval] = createSignal<{ key: string; sessionID: string | null }>();
  const undoKey = () => {
    const removal = lastRemoval();
    return removal?.sessionID === workspace.activeSessionID ? removal?.key : undefined;
  };
  const onRemove = (node: TaskOverviewNode) => {
    const key = dismissTaskOverviewNode(node);
    if (key) setLastRemoval({ key, sessionID: workspace.activeSessionID });
  };
  // Joined-ID equality keeps stream flushes from re-triggering plan loads.
  const relatedSessionIDs = createMemo(
    () => collectAncestorSessionIDs(workspace.activeSessionID),
    [],
    { equals: (prev, next) => prev.join(",") === next.join(",") }
  );
  const visible = () => workspace.inspectorOpen && workspace.inspectorTab === "tasks";

  // Fetch and poll only while the Tasks tab is visible.
  createEffect(() => {
    if (!visible()) return;
    const dir = directory();
    const sessionIDs = relatedSessionIDs();
    void loadSchedules().catch(() => {});
    const stopSchedules = startSchedulesPolling();
    if (!dir && sessionIDs.length === 0) {
      resetTaskOverviewPlans();
      resetIntakeState();
      onCleanup(stopSchedules);
      return;
    }
    void loadTaskOverviewPlans(dir, sessionIDs);
    const stopPlans = startTaskOverviewPolling(dir, sessionIDs);
    let stopIntake: (() => void) | undefined;
    if (dir) {
      void loadIntakeTasks(dir).catch(() => {});
      stopIntake = startIntakeTasksPolling(dir);
    } else {
      resetIntakeState();
    }
    onCleanup(() => {
      stopSchedules();
      stopPlans();
      stopIntake?.();
    });
  });

  // Reconcile groups and nodes by stable ID so plan polling and stream flushes
  // update rows in place without unmounting open row-action menus or stealing focus.
  const [renderedGroups, setRenderedGroups] = createStore<TaskOverviewGroup[]>([]);
  createComputed(() => {
    const next = visible()
      ? buildTaskOverview(taskOverviewPlans()).filter((group) => group.nodes.length > 0)
      : [];
    setRenderedGroups(reconcile(next, { key: "id" }));
  });
  const total = createMemo(() => renderedGroups.reduce((sum, group) => sum + group.nodes.length, 0));

  return (
    <div class="flex h-full flex-col">
      <Show when={directory() || undoKey()}>
        <div class="flex shrink-0 flex-wrap items-center justify-end gap-2 border-b border-[var(--border-subtle)]/50 px-2.5 py-1.5">
          <Show when={undoKey()}>
            {(key) => (
              <button
                type="button"
                onClick={() => { restoreTaskOverviewNode(key()); setLastRemoval(undefined); }}
                class="mr-auto inline-flex cursor-pointer items-center gap-1 rounded-lg px-2 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
              >
                <Undo2 size={12} />
                {t("tasks.undoRemoval")}
              </button>
            )}
          </Show>
          <button
            type="button"
            onClick={() =>
              openScheduleModal({
                directory: directory() || workspace.rootDir || undefined,
                projectID: workspace.activeProjectID || undefined,
              })
            }
            class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
          >
            <CalendarClock size={11} class="text-[var(--accent-primary)]" />
            <span>{t("schedules.manageBtn")}</span>
          </button>
          <Show when={directory()}>
            <button
              type="button"
              onClick={() => setIntakeLinksModalOpen(true)}
              class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1 text-[11px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <Link2 size={11} class="text-[var(--accent-primary)]" />
              <span>
                {surfaceExtension.enabled && surfaceExtension.collaborationLinksButtonLabel
                  ? surfaceExtension.collaborationLinksButtonLabel()
                  : t("tasks.intakeLinksBtn")}
              </span>
            </button>
          </Show>
        </div>
      </Show>

      <div class="min-h-0 flex-1 overflow-y-auto px-2.5 py-2">
        <ScheduledTasksSection />
        {surfaceExtension.enabled
          ? surfaceExtension.renderExtraTasksSection?.({ directory, visible })
          : null}
        <Show when={!(surfaceExtension.enabled && surfaceExtension.shouldHideDefaultIntakeSection?.())}>
          <IntakeTasksSection />
        </Show>
        <Show
          when={workspace.activeSessionID}
          fallback={
            <Show
              when={
                intakeTasks().length === 0 &&
                !(surfaceExtension.enabled && surfaceExtension.hasExtraTasks?.()) &&
                schedulesForWorkspace(directory(), workspace.activeProjectID || undefined).length === 0
              }
            >
              <p class="px-2 py-4 text-center text-[12px] text-[var(--text-faint)]">
                {t("tasks.noSession")}
              </p>
            </Show>
          }
        >
          <Show
            when={total() > 0}
            fallback={
              <Show
                when={
                  intakeTasks().length === 0 &&
                  !(surfaceExtension.enabled && surfaceExtension.hasExtraTasks?.()) &&
                  schedulesForWorkspace(directory(), workspace.activeProjectID || undefined).length === 0
                }
              >
                <div class="flex items-center justify-center gap-2 px-2 py-8 text-[12px] text-[var(--text-faint)]">
                  <Show when={taskOverviewLoading()}>
                    <TaskLoadingIcon class="text-[var(--text-muted)]" />
                  </Show>
                  <span>{taskOverviewFailed() ? t("tasks.loadFailed") : t("tasks.empty")}</span>
                </div>
              </Show>
            }
          >
            <For each={renderedGroups}>
              {(group) => (
                <section class="mb-3">
                  <div class="mb-1 flex items-center gap-1.5 px-1 text-[11px] font-medium text-[var(--text-muted)]">
                    <Switch>
                      <Match when={group.id === "convoy"}>
                        <Workflow size={12} class="text-[var(--accent-primary)]" />
                      </Match>
                      <Match when={group.id === "subagents"}>
                        <Bot size={12} class="text-[var(--accent-primary)]" />
                      </Match>
                      <Match when={group.id === "todos"}>
                        <ListTodo size={12} class="text-[var(--accent-primary)]" />
                      </Match>
                    </Switch>
                    <span>{groupLabel(group.id)}</span>
                    <span class="rounded bg-[var(--bg-inset)] px-1 font-mono text-[10px]">
                      {group.nodes.length}
                    </span>
                  </div>
                  <For each={group.nodes}>
                    {(node) => <TaskNodeRow node={node} depth={0} onRemove={onRemove} />}
                  </For>
                </section>
              )}
            </For>
          </Show>
        </Show>
      </div>

      <Show
        when={
          surfaceExtension.enabled &&
          surfaceExtension.isCustomCollaborationLinksModalActive?.() &&
          surfaceExtension.renderCollaborationLinksModal
        }
        fallback={
          <IntakeLinksModal
            open={intakeLinksModalOpen()}
            directory={directory()}
            onClose={() => setIntakeLinksModalOpen(false)}
          />
        }
      >
        {surfaceExtension.renderCollaborationLinksModal?.({
          open: intakeLinksModalOpen,
          directory,
          onClose: () => setIntakeLinksModalOpen(false),
        })}
      </Show>
    </div>
  );
}
