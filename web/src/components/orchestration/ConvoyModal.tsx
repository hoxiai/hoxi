// web/src/components/orchestration/ConvoyModal.tsx
import { createEffect, createSignal, For, onCleanup, Show } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  CheckCircle2,
  LoaderCircle,
  MinusCircle,
  Play,
  Plus,
  Square,
  Trash2,
  Workflow,
  X,
  XCircle,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  cancelConvoyPlan,
  createConvoyPlan,
  fetchConvoyPlan,
  type ConvoyPlan,
  type ConvoyTaskSpec,
  type ConvoyTaskStatus,
} from "../../store/workspace";

type DraftTask = { id: string; prompt: string; depends: string };

function newTask(index: number): DraftTask {
  return { id: `task-${index}`, prompt: "", depends: "" };
}

function parseDependencies(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function isTerminal(status: ConvoyTaskStatus): boolean {
  return status === "succeeded" || status === "failed" || status === "cancelled";
}

const statusColor: Record<string, string> = {
  queued: "text-[var(--text-muted)]",
  running: "text-[var(--accent-primary)]",
  succeeded: "text-[var(--ok-text)]",
  failed: "text-[var(--danger-text)]",
  blocked: "text-[var(--danger-text)]",
  cancelled: "text-[var(--text-muted)]",
};

function StatusIcon(props: { status: ConvoyTaskStatus }) {
  return (
    <Show
      when={props.status === "running"}
      fallback={
        <Show
          when={props.status === "succeeded"}
          fallback={
            <Show
              when={props.status === "failed" || props.status === "blocked"}
              fallback={<MinusCircle size={13} class={statusColor[props.status]} />}
            >
              <XCircle size={13} class={statusColor[props.status]} />
            </Show>
          }
        >
          <CheckCircle2 size={13} class={statusColor[props.status]} />
        </Show>
      }
    >
      <LoaderCircle size={13} class="animate-spin text-[var(--accent-primary)]" />
    </Show>
  );
}

export default function ConvoyModal(props: {
  open: boolean;
  directory?: string | null;
  onClose: () => void;
}) {
  const [tasks, setTasks] = createSignal<DraftTask[]>([newTask(1)]);
  const [concurrency, setConcurrency] = createSignal(3);
  const [plan, setPlan] = createSignal<ConvoyPlan | null>(null);
  const [starting, setStarting] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  const updateTask = (index: number, patch: Partial<DraftTask>) => {
    setTasks((current) => current.map((task, i) => (i === index ? { ...task, ...patch } : task)));
  };

  const addTask = () => setTasks((current) => [...current, newTask(current.length + 1)]);
  const removeTask = (index: number) => {
    setTasks((current) => (current.length <= 1 ? current : current.filter((_, i) => i !== index)));
  };

  const statusLabel = (status: ConvoyTaskStatus) => {
    switch (status) {
      case "queued":
        return t("timeline.convoyQueued");
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
  };

  createEffect(() => {
    const current = plan();
    if (!props.open || !current || isTerminal(current.status)) return;
    const timer = setInterval(() => {
      fetchConvoyPlan(current.id)
        .then((next) => setPlan(next))
        .catch(() => {});
    }, 1500);
    onCleanup(() => clearInterval(timer));
  });

  createEffect(() => {
    if (!props.open) {
      setError(null);
      setStarting(false);
    }
  });

  const start = async () => {
    const dir = props.directory?.trim();
    if (!dir || starting()) return;
    const specs: ConvoyTaskSpec[] = [];
    const ids = new Set<string>();
    for (const task of tasks()) {
      const id = task.id.trim();
      const prompt = task.prompt.trim();
      if (!id || !prompt || ids.has(id)) {
        setError(t("timeline.convoyInvalidTasks"));
        return;
      }
      ids.add(id);
      const dependsOn = parseDependencies(task.depends);
      specs.push({ id, prompt, ...(dependsOn.length ? { dependsOn } : {}) });
    }
    if (specs.length === 0) {
      setError(t("timeline.convoyEmptyTasks"));
      return;
    }
    setStarting(true);
    setError(null);
    try {
      const created = await createConvoyPlan({
        directory: dir,
        concurrency: concurrency(),
        tasks: specs,
      });
      setPlan(created);
    } catch (err) {
      setError(String(err));
    } finally {
      setStarting(false);
    }
  };

  const cancel = async () => {
    const current = plan();
    if (!current || isTerminal(current.status)) return;
    try {
      setPlan(await cancelConvoyPlan(current.id));
    } catch (err) {
      setError(String(err));
    }
  };

  const reset = () => {
    setPlan(null);
    setError(null);
  };

  return (
    <Presence when={props.open}>
      {(state) => (
      <div
        data-state={state()}
        class="hoxi-modal-backdrop fixed inset-0 z-[80] flex items-start justify-center px-4 pt-[8vh]"
        onMouseDown={(event) => {
          if (event.target === event.currentTarget) props.onClose();
        }}
      >
        <div class="hoxi-modal-panel hoxi-elevation-4 w-full max-w-3xl overflow-hidden rounded-2xl border border-[var(--border-strong)] bg-[var(--bg-elevated)]">
          <div class="flex items-center gap-2 border-b border-[var(--border-subtle)] px-3 py-2">
            <Workflow size={15} class="shrink-0 text-[var(--accent-primary)]" />
            <div class="min-w-0">
              <div class="text-[13px] font-semibold text-[var(--text-primary)]">{t("timeline.convoyTitle")}</div>
              <div class="truncate text-[11px] text-[var(--text-muted)]">{t("timeline.convoySubtitle")}</div>
            </div>
            <button
              type="button"
              onClick={props.onClose}
              class="ml-auto rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
            >
              <X size={14} />
            </button>
          </div>

          <Show when={error()}>
            <p class="border-b border-[var(--border-subtle)] px-3 py-2 text-[12px] text-[var(--danger-text)] break-words">
              {plan() ? t("timeline.convoyRunError", { error: error() || "" }) : error()}
            </p>
          </Show>

          <Show
            when={plan()}
            fallback={
              <div class="max-h-[60vh] overflow-y-auto p-3">
                <For each={tasks()}>
                  {(task, index) => (
                    <div class="mb-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-2">
                      <div class="flex items-center gap-2">
                        <input
                          value={task.id}
                          onInput={(event) => updateTask(index(), { id: event.currentTarget.value })}
                          placeholder={t("timeline.convoyTaskID")}
                          class="w-32 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2 py-1 font-mono text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                        />
                        <input
                          value={task.depends}
                          onInput={(event) => updateTask(index(), { depends: event.currentTarget.value })}
                          placeholder={t("timeline.convoyDependsPlaceholder")}
                          class="min-w-0 flex-1 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2 py-1 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                        />
                        <button
                          type="button"
                          onClick={() => removeTask(index())}
                          disabled={tasks().length <= 1}
                          title={t("timeline.convoyRemoveTask")}
                          class="rounded-lg p-1 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--danger-text)] disabled:opacity-30 cursor-pointer"
                        >
                          <Trash2 size={13} />
                        </button>
                      </div>
                      <textarea
                        value={task.prompt}
                        onInput={(event) => updateTask(index(), { prompt: event.currentTarget.value })}
                        placeholder={t("timeline.convoyTaskPlaceholder")}
                        rows={2}
                        class="mt-2 w-full resize-y rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2 py-1.5 text-[12px] leading-snug text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                      />
                    </div>
                  )}
                </For>

                <div class="mt-1 flex items-center gap-2">
                  <button
                    type="button"
                    onClick={addTask}
                    class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-strong)] px-2 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    <Plus size={12} /> {t("timeline.convoyAddTask")}
                  </button>
                  <label class="ml-auto inline-flex items-center gap-1.5 text-[12px] text-[var(--text-muted)]">
                    {t("timeline.convoyConcurrency")}
                    <select
                      value={concurrency()}
                      onChange={(event) => setConcurrency(Number(event.currentTarget.value))}
                      class="rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-1.5 py-1 text-[12px] text-[var(--text-primary)] outline-none cursor-pointer"
                    >
                      <For each={[1, 2, 3, 4, 5, 6, 8]}>{(value) => <option value={value}>{value}</option>}</For>
                    </select>
                  </label>
                  <button
                    type="button"
                    onClick={() => void start()}
                    disabled={starting() || !props.directory}
                    class="inline-flex items-center gap-1.5 rounded-xl bg-[var(--accent-primary)] px-3 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                  >
                    <Show when={!starting()} fallback={<LoaderCircle size={13} class="animate-spin" />}>
                      <Play size={12} />
                    </Show>
                    {t("timeline.convoyStart")}
                  </button>
                </div>
              </div>
            }
          >
            <div class="max-h-[60vh] overflow-y-auto p-3">
              <p class="mb-2 px-0.5 text-[11px] text-[var(--text-muted)]">
                {plan()?.isolated ? t("timeline.convoyIsolatedNote") : t("timeline.convoySharedNote")}
              </p>
              <For each={plan()?.tasks ?? []}>
                {(task) => (
                  <div class="mb-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2.5 py-2">
                    <div class="flex items-center gap-2 text-[12px]">
                      <StatusIcon status={task.status} />
                      <span class="font-mono text-[var(--text-primary)]">{task.id}</span>
                      <span class={`text-[11px] ${statusColor[task.status]}`}>{statusLabel(task.status)}</span>
                      <Show when={task.sessionID}>
                        <span class="ml-auto truncate font-mono text-[11px] text-[var(--text-faint)]">
                          {t("timeline.convoySessionLabel", { id: task.sessionID || "" })}
                        </span>
                      </Show>
                    </div>
                    <Show when={task.summary}>
                      <p class="mt-1 line-clamp-3 whitespace-pre-wrap break-words text-[12px] text-[var(--text-secondary)]">
                        {task.summary}
                      </p>
                    </Show>
                    <Show when={task.error}>
                      <p class="mt-1 break-words text-[12px] text-[var(--danger-text)]">{task.error}</p>
                    </Show>
                  </div>
                )}
              </For>
            </div>
            <div class="flex items-center gap-2 border-t border-[var(--border-subtle)] px-3 py-2">
              <span class={`text-[12px] ${statusColor[plan()?.status ?? "queued"]}`}>{statusLabel(plan()?.status ?? "queued")}</span>
              <Show when={!isTerminal(plan()?.status ?? "queued")}>
                <button
                  type="button"
                  onClick={() => void cancel()}
                  class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] px-2 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  <Square size={11} /> {t("timeline.convoyCancel")}
                </button>
              </Show>
              <div class="ml-auto flex items-center gap-2">
                <Show when={isTerminal(plan()?.status ?? "queued")}>
                  <button
                    type="button"
                    onClick={reset}
                    class="rounded-lg border border-[var(--border-strong)] px-2 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    {t("timeline.convoyStart")}
                  </button>
                </Show>
                <button
                  type="button"
                  onClick={props.onClose}
                  class="rounded-lg bg-[var(--bg-inset)] px-2 py-1 text-[12px] text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  {t("timeline.convoyClose")}
                </button>
              </div>
            </div>
          </Show>
        </div>
      </div>
      )}
    </Presence>
  );
}
