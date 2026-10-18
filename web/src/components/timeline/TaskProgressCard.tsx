// web/src/components/timeline/TaskProgressCard.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  onCleanup,
  Show,
  untrack,
} from "solid-js";
import {
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  ListTodo,
  PauseCircle,
  X,
} from "lucide-solid";
import type { SnapshotFileDiff } from "../../services/sse-types";
import {
  isSessionBusy,
  openDiffInInspector,
  workspace,
} from "../../store/workspace";
import { DiffChanges, Spinner, TextShimmer } from "../icons";
import { t } from "../../i18n";
import { formatToolDisplayName } from "./tool-card-helpers";
import {
  collectCycleStepItems,
  collectTaskCycleTurns,
  type CycleStepItem,
  findLatestTodoPart,
  hasActiveSubagentInCycle,
  isTodoListStale,
  type NormalizedTodoItem,
  normalizeTodoItems,
  resolveTaskChangedFiles,
  summarizeTodoActivity,
} from "./task-progress-helpers";
import TaskProgressDetails from "./TaskProgressDetails";
import TaskProgressStaleHint from "./TaskProgressStaleHint";

export default function TaskProgressCard() {
  const [expanded, setExpanded] = createSignal(false);
  const [dismissedSigBySession, setDismissedSigBySession] = createSignal<
    Record<string, string>
  >({});
  const [justFinishedBySession, setJustFinishedBySession] = createSignal<
    Record<string, boolean>
  >({});
  const wasAliveSessions = new Set<string>();
  const finishTimers = new Map<string, ReturnType<typeof setTimeout>>();

  onCleanup(() => {
    for (const timer of finishTimers.values()) clearTimeout(timer);
    finishTimers.clear();
  });

  const sessionID = () => workspace.activeSessionID;
  const isBusy = createMemo(() => isSessionBusy(sessionID()));
  const interruptedState = createMemo(() => {
    const id = sessionID();
    return id ? workspace.interruptedBySession[id] : undefined;
  });

  const cycleTurns = createMemo(() => collectTaskCycleTurns(sessionID()));

  const latestTodo = createMemo(() => findLatestTodoPart(cycleTurns()));
  const rawTodos = createMemo(() => latestTodo()?.todos ?? []);

  const hasRunningSubagent = createMemo(() =>
    hasActiveSubagentInCycle(cycleTurns(), sessionID())
  );

  const isSessionAlive = createMemo(() => isBusy() || hasRunningSubagent());

  const todos = createMemo<NormalizedTodoItem[]>(() =>
    normalizeTodoItems(
      rawTodos(),
      isSessionAlive(),
      Boolean(interruptedState())
    )
  );

  const cycleSteps = createMemo<CycleStepItem[]>(() =>
    collectCycleStepItems(cycleTurns(), sessionID(), isSessionAlive(), latestTodo()?.partID)
  );

  const todoActivity = createMemo(() => summarizeTodoActivity(cycleSteps()));
  const isTodoStale = createMemo(() => isTodoListStale(todos(), todoActivity()));

  const currentStep = createMemo<CycleStepItem | undefined>(() => {
    const list = cycleSteps();
    if (list.length === 0) return undefined;
    const running = [...list].reverse().find((s) => s.status === "running");
    return running ?? list[list.length - 1];
  });

  const changedFiles = createMemo<SnapshotFileDiff[]>(() =>
    resolveTaskChangedFiles(cycleTurns(), sessionID())
  );

  const diffTotals = createMemo(() => {
    let additions = 0;
    let deletions = 0;
    for (const d of changedFiles()) {
      additions += d.additions ?? 0;
      deletions += d.deletions ?? 0;
    }
    return { additions, deletions };
  });

  const completedTodoCount = createMemo(
    () => todos().filter((t) => t.status === "completed").length
  );

  const activeTodo = createMemo(
    () =>
      todos().find((t) => t.status === "in_progress") ??
      todos().find((t) => t.status === "interrupted") ??
      todos().find((t) => t.status === "pending")
  );

  const allTodosCompleted = createMemo(
    () =>
      todos().length > 0 &&
      todos().every((t) => t.status === "completed" || t.status === "cancelled") &&
      !isSessionAlive()
  );

  const isCompletedSummary = createMemo(
    () =>
      !isSessionAlive() &&
      !interruptedState() &&
      Boolean(sessionID() && justFinishedBySession()[sessionID()!]) &&
      changedFiles().length > 0 &&
      todos().length === 0
  );

  const progressSignature = createMemo(() => {
    const todoPart = todos()
      .map((item) => `${item.id || item.content}:${item.status}`)
      .join("|");
    return `${todoPart}#steps:${cycleSteps().length}#files:${changedFiles().length}#alive:${isSessionAlive()}`;
  });

  const isDismissed = createMemo(() => {
    const id = sessionID();
    if (!id || isSessionAlive()) return false;
    return dismissedSigBySession()[id] === progressSignature();
  });

  const clearFinishTimer = (id: string) => {
    const existing = finishTimers.get(id);
    if (existing) {
      clearTimeout(existing);
      finishTimers.delete(id);
    }
  };

  const dismissCard = () => {
    const id = sessionID();
    const sig = progressSignature();
    if (!id || !sig) return;
    clearFinishTimer(id);
    setExpanded(false);
    setJustFinishedBySession((prev) => ({ ...prev, [id]: false }));
    setDismissedSigBySession((prev) => ({ ...prev, [id]: sig }));
  };

  // Track live running -> completed transition so only sessions that JUST finished
  // display the 3-second completion summary before auto-closing.
  createEffect(() => {
    const id = sessionID();
    if (!id) return;
    const alive = isSessionAlive();
    if (alive) {
      wasAliveSessions.add(id);
      clearFinishTimer(id);
      if (untrack(() => justFinishedBySession()[id])) {
        setJustFinishedBySession((prev) => ({ ...prev, [id]: false }));
      }
      if (untrack(() => dismissedSigBySession()[id])) {
        setDismissedSigBySession((prev) => {
          const next = { ...prev };
          delete next[id];
          return next;
        });
      }
      return;
    }

    if (wasAliveSessions.has(id)) {
      wasAliveSessions.delete(id);
      clearFinishTimer(id);
      if (!untrack(() => interruptedState())) {
        setJustFinishedBySession((prev) => ({ ...prev, [id]: true }));
        const timer = setTimeout(() => {
          finishTimers.delete(id);
          setExpanded(false);
          setJustFinishedBySession((prev) => ({ ...prev, [id]: false }));
        }, 3000);
        finishTimers.set(id, timer);
      }
    }
  });

  createEffect(() => {
    if (!expanded()) return;
    const onPointerDown = (e: PointerEvent) => {
      if (containerRef && !containerRef.contains(e.target as Node)) {
        setExpanded(false);
      }
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setExpanded(false);
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown);
    onCleanup(() => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown);
    });
  });

  let containerRef: HTMLDivElement | undefined;

  // Strictly show TaskProgressCard ONLY when:
  // 1. The active session is currently running (`isSessionAlive()`), OR
  // 2. The active session was explicitly paused by the user (`interruptedState() === "paused"`), OR
  // 3. The active session just transitioned from running -> completed within the last 3 seconds.
  const shouldShow = createMemo(() => {
    const id = sessionID();
    if (!id || isDismissed()) return false;
    const hasContent =
      todos().length > 0 || cycleSteps().length > 0 || changedFiles().length > 0;
    if (!hasContent) return false;
    if (isSessionAlive()) return true;
    if (interruptedState() === "paused") {
      return todos().length > 0 || cycleSteps().length > 0;
    }
    return Boolean(justFinishedBySession()[id]);
  });

  return (
    <Show when={shouldShow()}>
      <div ref={containerRef} class="relative inline-flex items-center">
        {/* Compact Pill Trigger */}
        <div
          class={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11.5px] transition-colors ${
            expanded()
              ? "border-[var(--accent-border)] bg-[var(--accent-surface)]/70 text-[var(--text-primary)]"
              : "border-[var(--border-subtle)] bg-[var(--bg-inset)]/90 text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]"
          }`}
        >
          <button
            type="button"
            class="inline-flex items-center gap-1.5 text-left min-w-0 cursor-pointer"
            onClick={() => {
              if (isCompletedSummary()) {
                openDiffInInspector(changedFiles()[0]?.file);
                return;
              }
              setExpanded((v) => !v);
            }}
            title={isCompletedSummary() ? t("timeline.viewChanges") : undefined}
          >
            <Show
              when={isCompletedSummary()}
              fallback={
                <>
                  <Show
                    when={isSessionAlive()}
                    fallback={
                      <Show
                        when={interruptedState()}
                        fallback={
                          <Show
                            when={allTodosCompleted()}
                            fallback={<ListTodo size={12.5} class="text-[var(--accent-primary)] shrink-0" />}
                          >
                            <CheckCircle2 size={12.5} class="text-[var(--ok-text)] shrink-0" />
                          </Show>
                        }
                      >
                        <PauseCircle size={12.5} class="text-[var(--warn-text)] shrink-0" />
                      </Show>
                    }
                  >
                    <span class="text-[var(--accent-primary)] shrink-0 inline-flex items-center">
                      <Spinner class="w-3 h-3" />
                    </span>
                  </Show>

                  <span class="font-medium text-[var(--text-primary)] shrink-0">
                    {todos().length > 0
                      ? t("timeline.tasksProgress", {
                          completed: completedTodoCount(),
                          total: todos().length,
                        })
                      : t("timeline.taskProgressActiveOnly")}
                  </span>

                  <Show when={!expanded() && isTodoStale()}>
                    <TaskProgressStaleHint
                      compact
                      activeTodo={activeTodo()}
                      summary={todoActivity()}
                      isSessionAlive={isSessionAlive()}
                    />
                  </Show>

                  <Show
                    when={!expanded() && !isTodoStale() && currentStep()}
                    fallback={
                      <Show when={!expanded() && !isTodoStale() && activeTodo()}>
                        <span class="hidden sm:inline-flex items-center gap-1 text-[var(--text-muted)] truncate max-w-[200px]">
                          <span>—</span>
                          <TextShimmer
                            text={activeTodo()!.content}
                            active={isSessionAlive() && activeTodo()!.status === "in_progress"}
                            class="truncate"
                          />
                        </span>
                      </Show>
                    }
                  >
                    {(step) => (
                      <span class="hidden sm:inline-flex items-center gap-1 text-[var(--text-muted)] truncate max-w-[220px]">
                        <span class="text-[var(--text-faint)]">·</span>
                        <span class="font-mono text-[10.5px] text-[var(--text-secondary)] shrink-0">
                          {formatToolDisplayName(step().tool)}
                        </span>
                        <TextShimmer
                          text={step().label}
                          active={step().status === "running"}
                          class="font-mono text-[11px] truncate"
                        />
                      </span>
                    )}
                  </Show>

                  <Show when={diffTotals().additions > 0 || diffTotals().deletions > 0}>
                    <span class="shrink-0">
                      <DiffChanges
                        changes={{
                          additions: diffTotals().additions,
                          deletions: diffTotals().deletions,
                        }}
                      />
                    </span>
                  </Show>

                  <span class="text-[var(--text-muted)] inline-flex items-center">
                    {expanded() ? <ChevronDown size={12} /> : <ChevronUp size={12} />}
                  </span>
                </>
              }
            >
              <CheckCircle2 size={12.5} class="text-[var(--ok-text)] shrink-0" />
              <span class="font-medium text-[var(--text-primary)]">
                {t("timeline.changesSummary", {
                  files: changedFiles().length,
                  additions: diffTotals().additions,
                  deletions: diffTotals().deletions,
                })}
              </span>
              <span class="text-[var(--accent-primary)]">{t("timeline.viewChanges")}</span>
            </Show>
          </button>

          <Show when={!isSessionAlive()}>
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                dismissCard();
              }}
              class="p-0.5 rounded-full text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
              title={t("common.close")}
            >
              <X size={12} />
            </button>
          </Show>
        </div>

        {/* Upward Floating Popover */}
        <Show when={expanded()}>
          <div class="absolute left-0 bottom-[calc(100%+6px)] z-40 w-[min(560px,calc(100vw-32px))] rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/98 backdrop-blur-xl p-3 shadow-2xl animate-in fade-in zoom-in-95 duration-150">
            <div class="flex items-center justify-between gap-2 text-[11.5px]">
              <div class="flex items-center gap-2 font-medium text-[var(--text-primary)]">
                <ListTodo size={13} class="text-[var(--accent-primary)]" />
                <span>
                  {todos().length > 0
                    ? t("timeline.tasksProgress", {
                        completed: completedTodoCount(),
                        total: todos().length,
                      })
                    : t("timeline.taskProgressActiveOnly")}
                </span>
                <Show when={cycleSteps().length > 0}>
                  <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                    {t("timeline.stepsCompleted", { count: cycleSteps().length })}
                  </span>
                </Show>
              </div>
              <div class="flex items-center gap-1.5">
                <Show when={changedFiles().length > 0}>
                  <button
                    type="button"
                    onClick={() => openDiffInInspector(changedFiles()[0]?.file)}
                    class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-0.5 text-[10.5px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] cursor-pointer"
                  >
                    <span>{t("timeline.filesAttached", { count: changedFiles().length })}</span>
                  </button>
                </Show>
                <button
                  type="button"
                  onClick={() => setExpanded(false)}
                  class="p-0.5 rounded text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  title={t("common.close")}
                >
                  <X size={13} />
                </button>
              </div>
            </div>

            <Show when={isTodoStale()}>
              <TaskProgressStaleHint
                activeTodo={activeTodo()}
                summary={todoActivity()}
                isSessionAlive={isSessionAlive()}
              />
            </Show>

            <TaskProgressDetails
              todos={todos()}
              isSessionAlive={isSessionAlive()}
              currentStep={currentStep()}
              cycleSteps={cycleSteps()}
              changedFiles={changedFiles()}
              diffTotals={diffTotals()}
            />
          </div>
        </Show>
      </div>
    </Show>
  );
}
