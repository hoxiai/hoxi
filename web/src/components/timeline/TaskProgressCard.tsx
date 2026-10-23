// Compact task-plan entry anchored to the composer's upper-right edge.
import {
  createEffect,
  createMemo,
  createSignal,
  createUniqueId,
  For,
  Match,
  on,
  onCleanup,
  Show,
  Switch,
} from "solid-js";
import { Check, Circle, ListTodo, Minus, Pause, X } from "lucide-solid";
import { t } from "../../i18n";
import { isSessionBusy, workspace } from "../../store/workspace";
import {
  collectTaskCycleTurns,
  findLatestTodoPart,
  normalizeTodoItems,
  type NormalizedTodoItem,
} from "../../store/task-progress";
import { hasActiveSubagentInCycle } from "./task-progress-helpers";

export default function TaskProgressCard() {
  const [preview, setPreview] = createSignal(false);
  const [pinned, setPinned] = createSignal(false);
  const [completionVisible, setCompletionVisible] = createSignal(false);
  const [fading, setFading] = createSignal(false);
  const panelID = createUniqueId();
  let container: HTMLDivElement | undefined;
  let trigger: HTMLButtonElement | undefined;
  let openTimer: ReturnType<typeof setTimeout> | undefined;
  let closeTimer: ReturnType<typeof setTimeout> | undefined;
  let finishTimer: ReturnType<typeof setTimeout> | undefined;
  let fadeTimer: ReturnType<typeof setTimeout> | undefined;

  const sessionID = () => workspace.activeSessionID;
  const cycleTurns = createMemo(() => collectTaskCycleTurns(sessionID()));
  const cycleID = createMemo(() => `${sessionID() ?? ""}:${cycleTurns()[0]?.info.id ?? ""}`);
  const isAlive = createMemo(() =>
    isSessionBusy(sessionID()) || hasActiveSubagentInCycle(cycleTurns(), sessionID())
  );
  const interrupted = () => {
    const id = sessionID();
    return Boolean(id && workspace.interruptedBySession[id]);
  };
  const todos = createMemo(() => normalizeTodoItems(
    findLatestTodoPart(cycleTurns())?.todos ?? [], isAlive(), interrupted()
  ));
  const completed = createMemo(() => todos().filter((todo) => todo.status === "completed").length);
  const isSettled = createMemo(() => todos().length > 0 && todos().every(
    (todo) => todo.status === "completed" || todo.status === "cancelled"
  ));
  const expanded = () => preview() || pinned();
  const visible = () => todos().length > 0 && (
    isAlive() || !isSettled() || completionVisible() || expanded()
  );
  const summary = () => t("timeline.tasksProgress", { completed: completed(), total: todos().length });
  const ringLength = 2 * Math.PI * 8;

  const clearHoverTimers = () => {
    clearTimeout(openTimer);
    clearTimeout(closeTimer);
  };
  const close = () => {
    clearHoverTimers();
    setPreview(false);
    setPinned(false);
  };

  // Hover/pin state belongs to one task cycle, never the next session or prompt.
  createEffect(on(cycleID, () => close()));
  createEffect(() => {
    if (todos().length === 0) close();
  });
  createEffect(on(() => [cycleID(), isAlive(), isSettled()] as const, (next, previous) => {
    clearTimeout(finishTimer);
    clearTimeout(fadeTimer);
    setFading(false);
    const justSettled = previous?.[0] === next[0] && (previous[1] || !previous[2]);
    if (!next[1] && next[2] && justSettled) {
      setCompletionVisible(true);
      finishTimer = setTimeout(() => {
        setFading(true);
        fadeTimer = setTimeout(() => setCompletionVisible(false), 200);
      }, 3000);
    } else {
      setCompletionVisible(false);
    }
  }));

  createEffect(() => {
    if (!expanded()) return;
    const onPointerDown = (event: PointerEvent) => {
      if (!container?.contains(event.target as Node)) close();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      const restoreFocus = container?.contains(document.activeElement);
      close();
      if (restoreFocus) trigger?.focus();
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown, true);
    onCleanup(() => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown, true);
    });
  });
  onCleanup(() => {
    clearHoverTimers();
    clearTimeout(finishTimer);
    clearTimeout(fadeTimer);
  });

  const statusLabel = (todo: NormalizedTodoItem) => {
    switch (todo.status) {
      case "completed": return t("timeline.taskStatusCompleted");
      case "cancelled": return t("timeline.taskStatusCancelled");
      case "interrupted": return t("timeline.taskStatusPaused");
      case "in_progress": return isAlive()
        ? t("timeline.taskStatusRunning") : t("timeline.taskStatusPaused");
      default: return t("timeline.taskStatusPending");
    }
  };

  return (
    <Show when={visible()}>
      <div
        ref={container}
        class={`relative z-40 ml-auto shrink-0 transition-opacity duration-200 motion-reduce:transition-none ${
          fading() && !expanded() ? "opacity-0" : "opacity-100"
        }`}
        onPointerEnter={(event) => {
          if (event.pointerType !== "mouse") return;
          clearHoverTimers();
          openTimer = setTimeout(() => setPreview(true), 250);
        }}
        onPointerLeave={() => {
          clearHoverTimers();
          closeTimer = setTimeout(() => setPreview(false), 150);
        }}
      >
        <button
          ref={trigger}
          type="button"
          aria-label={summary()}
          aria-expanded={expanded()}
          aria-controls={expanded() ? panelID : undefined}
          aria-haspopup="dialog"
          class={`flex h-7 w-7 cursor-pointer items-center justify-center rounded-full outline-none transition-colors focus-visible:ring-2 focus-visible:ring-[var(--accent-primary)] ${
            expanded() ? "bg-[var(--bg-hover)] text-[var(--text-primary)]"
              : "text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)]"
          }`}
          onClick={() => {
            const wasPinned = pinned();
            clearHoverTimers();
            setPreview(false);
            setPinned(!wasPinned);
          }}
          onKeyDown={(event) => {
            if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
            event.preventDefault();
            setPinned(true);
          }}
        >
          <span class="relative flex h-[18px] w-[18px] items-center justify-center" aria-hidden="true">
            <svg viewBox="0 0 20 20" class="absolute inset-0 h-full w-full -rotate-90 fill-none" stroke-width="1.25">
              <circle cx="10" cy="10" r="8" class="stroke-[var(--border-subtle)]" />
              <circle
                cx="10" cy="10" r="8" stroke="currentColor" stroke-linecap="round"
                stroke-dasharray={String(ringLength)}
                stroke-dashoffset={ringLength * (1 - completed() / Math.max(1, todos().length))}
                class={`transition-[stroke-dashoffset] duration-300 motion-reduce:transition-none ${
                  completed() === todos().length ? "text-[var(--ok-text)]" : "text-[var(--accent-primary)]/70"
                }`}
              />
            </svg>
            <Show when={completed() === todos().length} fallback={<ListTodo size={9} />}>
              <Check size={10} class="text-[var(--ok-text)]" />
            </Show>
          </span>
        </button>

        <Show when={expanded()}>
          {/* Padding bridges the gap so moving from trigger to panel keeps it open. */}
          <div class="absolute right-0 bottom-full w-[min(340px,calc(100vw-64px))] pb-1.5">
            <div
              id={panelID} role="dialog" aria-label={t("timeline.taskListLabel")}
              class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] p-3 shadow-xl"
            >
              <div class="mb-2 flex items-center justify-between gap-3">
                <span class="text-[12px] font-medium text-[var(--text-secondary)]">{summary()}</span>
                <button
                  type="button" aria-label={t("common.close")}
                  class="flex h-5 w-5 cursor-pointer items-center justify-center rounded text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] focus-visible:outline focus-visible:outline-[var(--accent-primary)]"
                  onClick={() => { close(); trigger?.focus(); }}
                >
                  <X size={12} />
                </button>
              </div>
              <ul class="max-h-[min(280px,50vh)] space-y-2 overflow-y-auto overscroll-contain selectable-text">
                <For each={todos()}>
                  {(todo) => (
                    <li class="flex items-start gap-2 text-[12px] leading-relaxed">
                      <span class="mt-1 inline-flex shrink-0" role="img" aria-label={statusLabel(todo)} title={statusLabel(todo)}>
                        <Switch fallback={<Circle size={12} class="text-[var(--text-faint)]" stroke-width={1.5} />}>
                          <Match when={todo.status === "completed"}>
                            <Check size={12} class="text-[var(--text-muted)]" />
                          </Match>
                          <Match when={todo.status === "cancelled"}>
                            <Minus size={12} class="text-[var(--text-faint)]" />
                          </Match>
                          <Match when={todo.status === "interrupted" || (todo.status === "in_progress" && !isAlive())}>
                            <Pause size={12} class="text-[var(--text-muted)]" />
                          </Match>
                          <Match when={todo.status === "in_progress"}>
                            <Circle size={12} class="text-[var(--accent-primary)]" stroke-width={3} />
                          </Match>
                        </Switch>
                      </span>
                      <span class={`min-w-0 break-words ${
                        todo.status === "completed" || todo.status === "cancelled" ? "text-[var(--text-muted)]"
                          : todo.status === "in_progress" && isAlive() ? "font-medium text-[var(--text-primary)]"
                            : "text-[var(--text-secondary)]"
                      }`}>{todo.content}</span>
                    </li>
                  )}
                </For>
              </ul>
            </div>
          </div>
        </Show>
      </div>
    </Show>
  );
}
