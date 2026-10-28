// web/src/components/orchestration/ConvoyModal.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show, untrack } from "solid-js";
import { Presence } from "../motion/Presence";
import {
  CheckCircle2,
  ExternalLink,
  LoaderCircle,
  MinusCircle,
  Play,
  Plus,
  RotateCcw,
  Sparkles,
  Square,
  Trash2,
  Workflow,
  X,
  XCircle,
} from "lucide-solid";
import { locale, t } from "../../i18n";
import {
  cancelConvoyPlan,
  createConvoyHistoryLoader,
  createConvoyPlan,
  draftConvoyPlan,
  fetchConvoyPlan,
  resolveConvoyModelForSession,
  retryConvoyPlan,
  selectSession,
  workspace,
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
  const [title, setTitle] = createSignal("");
  const [goal, setGoal] = createSignal("");
  const [draftedBy, setDraftedBy] = createSignal("");
  const [drafting, setDrafting] = createSignal(false);
  const [concurrency, setConcurrency] = createSignal(3);
  const [plan, setPlan] = createSignal<ConvoyPlan | null>(null);
  const [history, setHistory] = createSignal<ConvoyPlan[]>([]);
  const [starting, setStarting] = createSignal(false);
  const [retrying, setRetrying] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  let lastDirectory = "";
  let draftGeneration = 0;
  const historyLoader = createConvoyHistoryLoader();
  onCleanup(() => historyLoader.invalidate());

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
    const directory = props.directory?.trim() ?? "";
    if (!props.open || !directory) {
      historyLoader.invalidate();
      return;
    }
    if (directory !== lastDirectory) {
      lastDirectory = directory;
      draftGeneration++;
      setPlan(null);
      setHistory([]);
      setTitle("");
      setGoal("");
      setDraftedBy("");
      setDrafting(false);
    }
    void historyLoader.load(directory).then((plans) => {
      // Stale-read guard: drop responses for another directory or a closed modal.
      if (!plans || untrack(() => !props.open || (props.directory?.trim() ?? "") !== directory)) return;
      setHistory(plans);
      if (untrack(() => plan())) return;
      const active = plans.find((item) => !isTerminal(item.status));
      if (active) setPlan(active);
    });
  });

  // Keep the history entry in sync while the live view polls.
  createEffect(() => {
    const current = plan();
    if (!current) return;
    setHistory((items) => {
      const index = items.findIndex((item) => item.id === current.id);
      if (index < 0) return [current, ...items];
      const next = items.slice();
      next[index] = current;
      return next;
    });
  });

  createEffect(() => {
    const current = plan();
    if (!props.open || !current || isTerminal(current.status)) return;
    let disposed = false;
    const timer = setInterval(() => {
      fetchConvoyPlan(current.id)
        .then((next) => {
          if (!disposed && untrack(() => plan())?.id === next.id) setPlan(next);
        })
        .catch(() => {});
    }, 1500);
    onCleanup(() => {
      disposed = true;
      clearInterval(timer);
    });
  });

  const canRetry = createMemo(() => {
    const current = plan();
    if (!current || !isTerminal(current.status)) return false;
    return (
      current.tasks.every((task) => task.status !== "running") &&
      current.tasks.some((task) => task.status !== "succeeded")
    );
  });

  const retry = async () => {
    const current = plan();
    if (!current || retrying()) return;
    setRetrying(true);
    setError(null);
    try {
      setPlan(await retryConvoyPlan(current.id));
    } catch (err) {
      setError(String(err));
    } finally {
      setRetrying(false);
    }
  };

  const openSession = (sessionID?: string) => {
    if (!sessionID) return;
    void selectSession(sessionID);
    props.onClose();
  };

  createEffect(() => {
    if (!props.open) {
      draftGeneration++;
      setError(null);
      setStarting(false);
      setDrafting(false);
    }
  });

  const generateDraft = async () => {
    const dir = props.directory?.trim();
    const text = goal().trim();
    if (!dir || !text || drafting()) return;
    const generation = ++draftGeneration;
    setDrafting(true);
    setError(null);
    try {
      const model = workspace.selectedModel;
      const draft = await draftConvoyPlan({
        directory: dir,
        goal: text,
        locale: locale(),
        ...(model ? { providerID: model.providerID, modelID: model.modelID } : {}),
      });
      if (generation !== draftGeneration) return;
      setTitle(draft.title ?? "");
      setConcurrency(draft.concurrency > 0 ? draft.concurrency : 3);
      setDraftedBy(draft.model || "");
      setTasks(
        draft.tasks.length > 0
          ? draft.tasks.map((task) => ({
              id: task.id,
              prompt: task.prompt,
              depends: (task.dependsOn ?? []).join(", "),
            }))
          : [newTask(1)]
      );
    } catch (err) {
      if (generation !== draftGeneration) return;
      setError(String(err));
    } finally {
      if (generation === draftGeneration) setDrafting(false);
    }
  };

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
    const sid = workspace.activeSessionID;
    const selected = resolveConvoyModelForSession(sid);
    try {
      const created = await createConvoyPlan({
        directory: dir,
        ...(sid ? { parentSessionID: sid } : {}),
        ...(title().trim() ? { title: title().trim() } : {}),
        ...selected,
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
    draftGeneration++;
    setPlan(null);
    setError(null);
    setTitle("");
    setGoal("");
    setDraftedBy("");
    setDrafting(false);
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
                <Show when={history().length > 0}>
                  <div class="mb-3">
                    <div class="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-[var(--text-muted)]">
                      {t("timeline.convoyHistory")}
                    </div>
                    <div class="flex flex-col gap-1">
                      <For each={history().slice(0, 6)}>
                        {(item) => (
                          <button
                            type="button"
                            onClick={() => setPlan(item)}
                            class="flex items-center gap-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-2 py-1.5 text-left text-[12px] hover:bg-[var(--bg-hover)] cursor-pointer"
                          >
                            <StatusIcon status={item.status} />
                            <span class="min-w-0 flex-1 truncate text-[var(--text-primary)]">{item.title}</span>
                            <span class={`shrink-0 text-[11px] ${statusColor[item.status]}`}>{statusLabel(item.status)}</span>
                          </button>
                        )}
                      </For>
                    </div>
                  </div>
                </Show>
                <div class="mb-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] p-2">
                  <div class="mb-1.5 flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-wide text-[var(--text-muted)]">
                    <Sparkles size={12} class="text-[var(--accent-primary)]" />
                    {t("timeline.convoyDraftLabel")}
                  </div>
                  <textarea
                    value={goal()}
                    onInput={(event) => setGoal(event.currentTarget.value)}
                    placeholder={t("timeline.convoyDraftPlaceholder")}
                    rows={2}
                    class="w-full resize-y rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2 py-1.5 text-[12px] leading-snug text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                  />
                  <div class="mt-1.5 flex items-center gap-2">
                    <p class="min-w-0 flex-1 text-[11px] text-[var(--text-faint)]">
                      {draftedBy()
                        ? t("timeline.convoyDraftBy", { model: draftedBy() })
                        : t("timeline.convoyDraftNote")}
                    </p>
                    <button
                      type="button"
                      onClick={() => void generateDraft()}
                      disabled={drafting() || !props.directory || !goal().trim()}
                      class="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-[var(--border-strong)] px-2 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                    >
                      <Show when={!drafting()} fallback={<LoaderCircle size={12} class="animate-spin" />}>
                        <Sparkles size={12} />
                      </Show>
                      {drafting() ? t("timeline.convoyDraftGenerating") : t("timeline.convoyDraftGenerate")}
                    </button>
                  </div>
                </div>
                <input
                  value={title()}
                  onInput={(event) => setTitle(event.currentTarget.value)}
                  placeholder={t("timeline.convoyTitlePlaceholder")}
                  class="mb-2 w-full rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2 py-1 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                />
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
                        <button
                          type="button"
                          onClick={() => openSession(task.sessionID)}
                          title={t("timeline.convoyOpenSession")}
                          class="ml-auto inline-flex min-w-0 items-center gap-1 rounded-md px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-faint)] hover:bg-[var(--bg-hover)] hover:text-[var(--accent-primary)] cursor-pointer"
                        >
                          <ExternalLink size={11} class="shrink-0" />
                          <span class="truncate">
                            {t("timeline.convoySessionLabel", { id: task.sessionID || "" })}
                          </span>
                        </button>
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
              <Show when={canRetry()}>
                <button
                  type="button"
                  onClick={() => void retry()}
                  disabled={retrying()}
                  class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-strong)] px-2 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
                >
                  <Show when={!retrying()} fallback={<LoaderCircle size={11} class="animate-spin" />}>
                    <RotateCcw size={11} />
                  </Show>
                  {t("timeline.convoyRetry")}
                </button>
              </Show>
              <div class="ml-auto flex items-center gap-2">
                <Show when={isTerminal(plan()?.status ?? "queued")}>
                  <button
                    type="button"
                    onClick={reset}
                    class="rounded-lg border border-[var(--border-strong)] px-2 py-1 text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    {t("timeline.convoyNewPlan")}
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
