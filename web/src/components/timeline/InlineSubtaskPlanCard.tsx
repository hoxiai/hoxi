// web/src/components/timeline/InlineSubtaskPlanCard.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  onCleanup,
  Show,
  untrack,
} from "solid-js";
import {
  CheckCircle2,
  Edit3,
  ExternalLink,
  GitMerge,
  LoaderCircle,
  MessageSquareShare,
  MinusCircle,
  Play,
  Plus,
  RotateCcw,
  Square,
  Trash2,
  Workflow,
  XCircle,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  bindInlineConvoyPlan,
  buildConvoyCompletionSummaryPrompt,
  cancelConvoyPlan,
  clearBoundInlineConvoyPlan,
  computeSubtaskPlanCardKey,
  createConvoyPlan,
  fetchConvoyPlan,
  getBoundInlineConvoyPlanID,
  isConvoyPlanTerminal,
  mergeConvoyPlan,
  parseSubtaskPlanBlock,
  recordSessionConvoyPlan,
  retryConvoyPlan,
  selectSession,
  sessionDirectory,
  submitSessionPrompt,
  workspace,
  type ConvoyPlan,
  type ConvoyTaskSpec,
  type ConvoyTaskStatus,
} from "../../store/workspace";

type EditableSubtask = {
  id: string;
  prompt: string;
  depends: string;
};

const statusColor: Record<ConvoyTaskStatus, string> = {
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
              fallback={<MinusCircle size={13} class={`shrink-0 ${statusColor[props.status]}`} />}
            >
              <XCircle size={13} class={`shrink-0 ${statusColor[props.status]}`} />
            </Show>
          }
        >
          <CheckCircle2 size={13} class={`shrink-0 ${statusColor[props.status]}`} />
        </Show>
      }
    >
      <LoaderCircle size={13} class="shrink-0 animate-spin text-[var(--accent-primary)]" />
    </Show>
  );
}

function statusLabel(status: ConvoyTaskStatus): string {
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
}

export default function InlineSubtaskPlanCard(props: {
  code: string;
  ref?: (el: HTMLElement) => void;
}) {
  const parsed = createMemo(() => parseSubtaskPlanBlock(props.code));
  const sessionID = () => workspace.activeSessionID;
  const directory = createMemo(
    () => (sessionID() ? sessionDirectory(sessionID()!) : "") || workspace.rootDir || ""
  );

  const cardKey = createMemo(() => {
    const p = parsed();
    return p ? computeSubtaskPlanCardKey(p, sessionID()) : "";
  });

  const [editing, setEditing] = createSignal(false);
  const [title, setTitle] = createSignal("");
  const [concurrency, setConcurrency] = createSignal(3);
  const [tasks, setTasks] = createSignal<EditableSubtask[]>([]);
  const [autoReport, setAutoReport] = createSignal(true);
  const [plan, setPlan] = createSignal<ConvoyPlan | null>(null);
  const [starting, setStarting] = createSignal(false);
  const [retrying, setRetrying] = createSignal(false);
  const [merging, setMerging] = createSignal(false);
  const [summarizing, setSummarizing] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  let initializedSig = "";
  let fetchGen = 0;
  const autoReportedPlans = new Set<string>();

  // Sync initial parsed tasks when the block finishes streaming
  createEffect(() => {
    const p = parsed();
    if (!p) return;
    const sig = `${p.title}::${p.concurrency}::${p.tasks.map((t) => `${t.id}:${t.prompt}`).join("|")}`;
    if (sig === initializedSig) return;
    initializedSig = sig;
    setTitle(p.title);
    setConcurrency(p.concurrency);
    setTasks(
      p.tasks.map((item) => ({
        id: item.id,
        prompt: item.prompt,
        depends: (item.dependsOn ?? []).join(", "),
      }))
    );
  });

  // Restore bound plan if this card was already started earlier
  createEffect(() => {
    const key = cardKey();
    if (!key) return;
    const boundID = getBoundInlineConvoyPlanID(key);
    if (!boundID) return;
    if (untrack(() => plan()?.id) === boundID) return;
    const gen = ++fetchGen;
    fetchConvoyPlan(boundID)
      .then((loaded) => {
        if (gen !== fetchGen || untrack(cardKey) !== key) return;
        setPlan(loaded);
        recordSessionConvoyPlan(sessionID(), loaded);
      })
      .catch(() => {});
  });

  // Poll live status while the plan is running
  createEffect(() => {
    const current = plan();
    if (!current || isConvoyPlanTerminal(current.status)) return;
    const activeID = current.id;
    const ownerSessionID = sessionID();
    let disposed = false;
    const timer = setInterval(() => {
      fetchConvoyPlan(activeID)
        .then((next) => {
          if (disposed || untrack(() => plan()?.id) !== next.id) return;
          const prevStatus = untrack(() => plan()?.status);
          setPlan(next);
          recordSessionConvoyPlan(ownerSessionID, next);
          if (
            prevStatus &&
            !isConvoyPlanTerminal(prevStatus) &&
            isConvoyPlanTerminal(next.status) &&
            untrack(autoReport) &&
            ownerSessionID &&
            !autoReportedPlans.has(next.id)
          ) {
            autoReportedPlans.add(next.id);
            void submitSessionPrompt(buildConvoyCompletionSummaryPrompt(next), ownerSessionID);
          }
        })
        .catch(() => {});
    }, 1500);
    onCleanup(() => {
      disposed = true;
      clearInterval(timer);
    });
  });

  onCleanup(() => {
    fetchGen++;
  });

  const updateTask = (index: number, patch: Partial<EditableSubtask>) => {
    setTasks((current) => current.map((item, i) => (i === index ? { ...item, ...patch } : item)));
  };

  const addTask = () => {
    setTasks((current) => [
      ...current,
      { id: `task-${current.length + 1}`, prompt: "", depends: "" },
    ]);
  };

  const removeTask = (index: number) => {
    setTasks((current) => (current.length <= 1 ? current : current.filter((_, i) => i !== index)));
  };

  const buildSpecs = (): ConvoyTaskSpec[] | null => {
    const specs: ConvoyTaskSpec[] = [];
    const seen = new Set<string>();
    for (const item of tasks()) {
      const id = item.id.trim();
      const prompt = item.prompt.trim();
      if (!id || !prompt || seen.has(id)) return null;
      seen.add(id);
      const dependsOn = item.depends
        .split(",")
        .map((d) => d.trim())
        .filter((d) => Boolean(d) && d !== id);
      specs.push(dependsOn.length > 0 ? { id, prompt, dependsOn } : { id, prompt });
    }
    return specs.length > 0 ? specs : null;
  };

  const onStart = async () => {
    const dir = directory().trim();
    if (!dir || starting()) return;
    const specs = buildSpecs();
    if (!specs) {
      setError(t("timeline.convoyInvalidTasks"));
      return;
    }
    setStarting(true);
    setError(null);
    const sid = sessionID();
    const key = cardKey();
    const selected = workspace.selectedModel;
    try {
      const created = await createConvoyPlan({
        directory: dir,
        ...(sid ? { parentSessionID: sid } : {}),
        ...(title().trim() ? { title: title().trim() } : {}),
        mode: "build",
        ...(selected?.providerID ? { providerID: selected.providerID } : {}),
        ...(selected?.modelID ? { model: selected.modelID } : {}),
        concurrency: concurrency(),
        tasks: specs,
      });
      setPlan(created);
      setEditing(false);
      if (key) bindInlineConvoyPlan(key, created.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setStarting(false);
    }
  };

  const onCancel = async () => {
    const current = plan();
    if (!current || isConvoyPlanTerminal(current.status)) return;
    try {
      const updated = await cancelConvoyPlan(current.id);
      setPlan(updated);
      recordSessionConvoyPlan(sessionID(), updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const onRetry = async () => {
    const current = plan();
    if (!current || retrying()) return;
    setRetrying(true);
    setError(null);
    try {
      const updated = await retryConvoyPlan(current.id);
      setPlan(updated);
      recordSessionConvoyPlan(sessionID(), updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setRetrying(false);
    }
  };

  const onMerge = async () => {
    const current = plan();
    const sid = sessionID();
    if (!current || !sid || merging()) return;
    setMerging(true);
    setError(null);
    try {
      const updated = await mergeConvoyPlan(current.id, sid);
      setPlan(updated);
      recordSessionConvoyPlan(sid, updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setMerging(false);
    }
  };

  const onSummarizeInChat = async () => {
    const current = plan();
    const sid = sessionID();
    if (!current || !sid || summarizing()) return;
    setSummarizing(true);
    try {
      autoReportedPlans.add(current.id);
      await submitSessionPrompt(buildConvoyCompletionSummaryPrompt(current), sid);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSummarizing(false);
    }
  };

  const onResetPlan = () => {
    const key = cardKey();
    if (key) clearBoundInlineConvoyPlan(key);
    setPlan(null);
    setError(null);
  };

  const completedCount = createMemo(
    () => (plan()?.tasks ?? []).filter((item) => item.status === "succeeded").length
  );

  const canRetry = createMemo(() => {
    const current = plan();
    if (!current || !isConvoyPlanTerminal(current.status)) return false;
    return (
      current.tasks.every((task) => task.status !== "running") &&
      current.tasks.some((task) => task.status !== "succeeded")
    );
  });

  return (
    <div
      ref={(el) => props.ref?.(el)}
      class="my-2.5 overflow-hidden rounded-xl border border-[var(--accent-border)] bg-[var(--bg-elevated)]/90 shadow-sm"
    >
      <Show
        when={parsed()}
        fallback={
          <div class="flex items-center gap-2.5 px-3.5 py-3 text-[12px] text-[var(--text-muted)]">
            <LoaderCircle size={14} class=" shrink-0 animate-spin text-[var(--accent-primary)]" />
            <span>{t("timeline.inlineSubtasksDrafting")}</span>
          </div>
        }
      >
        {/* Header */}
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--accent-surface)]/35 px-3.5 py-2">
          <div class="flex items-center gap-2 min-w-0">
            <Workflow size={14} class="shrink-0 text-[var(--accent-primary)]" />
            <span class="rounded-md bg-[var(--accent-surface)] px-1.5 py-0.5 text-[11px] font-semibold text-[var(--accent-primary)]">
              {t("timeline.inlineSubtasksBadge")}
            </span>
            <span class="truncate text-[13px] font-semibold text-[var(--text-primary)]">
              {plan()?.title || title() || t("timeline.convoyTitle")}
            </span>
          </div>

          <div class="flex items-center gap-2 shrink-0 text-[11px] text-[var(--text-muted)]">
            <Show
              when={plan()}
              fallback={
                <>
                  <span>{t("timeline.inlineSubtasksCount", { count: tasks().length })}</span>
                  <button
                    type="button"
                    onClick={() => setEditing((v) => !v)}
                    class="inline-flex items-center gap-1 rounded-md border border-[var(--border-strong)] bg-[var(--bg-panel)] px-2 py-0.5 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                  >
                    <Edit3 size={11} />
                    <span>
                      {editing()
                        ? t("timeline.inlineSubtasksDoneEditBtn")
                        : t("timeline.inlineSubtasksEditBtn")}
                    </span>
                  </button>
                </>
              }
            >
              {(livePlan) => (
                <div class="flex items-center gap-2">
                  <span class="font-mono">
                    {t("timeline.inlineSubtasksProgress", {
                      completed: completedCount(),
                      total: livePlan().tasks.length,
                    })}
                  </span>
                  <span class={`font-medium ${statusColor[livePlan().status]}`}>
                    {statusLabel(livePlan().status)}
                  </span>
                </div>
              )}
            </Show>
          </div>
        </div>

        <Show when={error()}>
          <div class="border-b border-[var(--danger-border)] bg-[var(--danger-surface)]/60 px-3.5 py-1.5 text-[12px] text-[var(--danger-text)] break-words">
            {error()}
          </div>
        </Show>

        {/* Body: Pre-launch vs Live Plan */}
        <Show
          when={plan()}
          fallback={
            <div class="p-3 space-y-2">
              <Show when={editing()}>
                <div class="flex flex-wrap items-center gap-2 pb-1">
                  <input
                    value={title()}
                    onInput={(e) => setTitle(e.currentTarget.value)}
                    placeholder={t("timeline.convoyTitlePlaceholder")}
                    class="min-w-[180px] flex-1 rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2.5 py-1 text-[12px] text-[var(--text-primary)] outline-none focus:border-[var(--accent-primary)]"
                  />
                  <label class="inline-flex items-center gap-1.5 text-[12px] text-[var(--text-muted)]">
                    {t("timeline.convoyConcurrency")}
                    <select
                      value={concurrency()}
                      onChange={(e) => setConcurrency(Number(e.currentTarget.value))}
                      class="rounded-lg border border-[var(--border-strong)] bg-[var(--bg-inset)] px-1.5 py-1 text-[12px] text-[var(--text-primary)] outline-none cursor-pointer"
                    >
                      <For each={[1, 2, 3, 4, 5, 6, 8]}>
                        {(val) => <option value={val}>{val}</option>}
                      </For>
                    </select>
                  </label>
                </div>
              </Show>

              <For each={tasks()}>
                {(task, idx) => (
                  <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/85 px-3 py-2">
                    <Show
                      when={editing()}
                      fallback={
                        <div>
                          <div class="flex items-center gap-2 text-[12px]">
                            <span class="inline-flex h-4 min-w-4 items-center justify-center rounded bg-[var(--bg-inset)] px-1 font-mono text-[10px] font-semibold text-[var(--text-muted)]">
                              {idx() + 1}
                            </span>
                            <span class="font-mono font-medium text-[var(--text-primary)]">
                              {task.id}
                            </span>
                            <Show
                              when={task.depends.trim()}
                              fallback={
                                <span class="rounded bg-[var(--ok-surface)]/60 px-1.5 py-0.5 text-[10px] font-medium text-[var(--ok-text)]">
                                  {t("timeline.inlineSubtasksIndependent")}
                                </span>
                              }
                            >
                              <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                                {t("timeline.inlineSubtasksDependsOn", { ids: task.depends.trim() })}
                              </span>
                            </Show>
                          </div>
                          <p class="mt-1 whitespace-pre-wrap break-words text-[12px] leading-relaxed text-[var(--text-secondary)]">
                            {task.prompt}
                          </p>
                        </div>
                      }
                    >
                      <div class="space-y-1.5">
                        <div class="flex items-center gap-2">
                          <input
                            value={task.id}
                            onInput={(e) => updateTask(idx(), { id: e.currentTarget.value })}
                            placeholder={t("timeline.convoyTaskID")}
                            class="w-28 rounded border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2 py-0.5 font-mono text-[12px] text-[var(--text-primary)] outline-none"
                          />
                          <input
                            value={task.depends}
                            onInput={(e) => updateTask(idx(), { depends: e.currentTarget.value })}
                            placeholder={t("timeline.convoyDependsPlaceholder")}
                            class="min-w-0 flex-1 rounded border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2 py-0.5 text-[12px] text-[var(--text-primary)] outline-none"
                          />
                          <button
                            type="button"
                            onClick={() => removeTask(idx())}
                            disabled={tasks().length <= 1}
                            title={t("timeline.convoyRemoveTask")}
                            class="rounded p-1 text-[var(--text-muted)] hover:text-[var(--danger-text)] disabled:opacity-30 cursor-pointer"
                          >
                            <Trash2 size={12} />
                          </button>
                        </div>
                        <textarea
                          value={task.prompt}
                          onInput={(e) => updateTask(idx(), { prompt: e.currentTarget.value })}
                          rows={2}
                          class="w-full resize-y rounded border border-[var(--border-strong)] bg-[var(--bg-inset)] px-2 py-1 text-[12px] text-[var(--text-primary)] outline-none"
                        />
                      </div>
                    </Show>
                  </div>
                )}
              </For>

              {/* Footer Actions */}
              <div class="flex flex-wrap items-center justify-between gap-2 pt-1">
                <div class="flex items-center gap-3">
                  <Show when={editing()}>
                    <button
                      type="button"
                      onClick={addTask}
                      class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-strong)] px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                    >
                      <Plus size={11} />
                      <span>{t("timeline.convoyAddTask")}</span>
                    </button>
                  </Show>
                  <label class="inline-flex items-center gap-1.5 text-[11px] text-[var(--text-muted)] cursor-pointer select-none">
                    <input
                      type="checkbox"
                      checked={autoReport()}
                      onChange={(e) => setAutoReport(e.currentTarget.checked)}
                      class="rounded accent-[var(--accent-primary)]"
                    />
                    <span>{t("timeline.inlineSubtasksAutoReport")}</span>
                  </label>
                </div>

                <button
                  type="button"
                  disabled={starting() || !directory().trim()}
                  onClick={() => void onStart()}
                  class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-3 py-1.5 text-[12px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-40 cursor-pointer"
                >
                  <Show
                    when={!starting()}
                    fallback={<LoaderCircle size={12} class="animate-spin" />}
                  >
                    <Play size={11} class="fill-current" />
                  </Show>
                  <span>{t("timeline.inlineSubtasksStartBtn", { count: tasks().length })}</span>
                </button>
              </div>
            </div>
          }
        >
          {(livePlan) => (
            <div class="p-3 space-y-2">
              <For each={livePlan().tasks}>
                {(task) => (
                  <div class="rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/85 px-3 py-2">
                    <div class="flex items-center gap-2 text-[12px]">
                      <StatusIcon status={task.status} />
                      <span class="font-mono font-medium text-[var(--text-primary)]">
                        {task.id}
                      </span>
                      <Show when={task.dependsOn && task.dependsOn.length > 0}>
                        <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)]">
                          {t("timeline.inlineSubtasksDependsOn", {
                            ids: (task.dependsOn ?? []).join(", "),
                          })}
                        </span>
                      </Show>
                      <span class={`text-[11px] ${statusColor[task.status]}`}>
                        {statusLabel(task.status)}
                      </span>
                      <Show when={task.sessionID}>
                        <button
                          type="button"
                          onClick={() => void selectSession(task.sessionID!)}
                          title={t("timeline.convoyOpenSession")}
                          class="ml-auto inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--accent-primary)] cursor-pointer"
                        >
                          <ExternalLink size={11} />
                          <span>{t("timeline.convoyOpenSession")}</span>
                        </button>
                      </Show>
                    </div>
                    <Show
                      when={task.summary || task.error}
                      fallback={
                        <p class="mt-1 line-clamp-2 text-[11px] text-[var(--text-muted)]">
                          {task.prompt}
                        </p>
                      }
                    >
                      <Show when={task.summary}>
                        <p class="mt-1 line-clamp-3 whitespace-pre-wrap break-words text-[12px] text-[var(--text-secondary)]">
                          {task.summary}
                        </p>
                      </Show>
                      <Show when={task.error}>
                        <p class="mt-1 break-words text-[12px] text-[var(--danger-text)]">
                          {task.error}
                        </p>
                      </Show>
                    </Show>
                  </div>
                )}
              </For>

              {/* Live / Completed Controls */}
              <div class="flex flex-wrap items-center justify-between gap-2 pt-1">
                <div class="flex items-center gap-1.5">
                  <Show when={!isConvoyPlanTerminal(livePlan().status)}>
                    <button
                      type="button"
                      onClick={() => void onCancel()}
                      class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-strong)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                    >
                      <Square size={10} />
                      <span>{t("timeline.convoyCancel")}</span>
                    </button>
                  </Show>
                  <Show when={canRetry()}>
                    <button
                      type="button"
                      disabled={retrying()}
                      onClick={() => void onRetry()}
                      class="inline-flex items-center gap-1 rounded-lg border border-[var(--border-strong)] px-2.5 py-1 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] disabled:opacity-40 cursor-pointer"
                    >
                      <Show
                        when={!retrying()}
                        fallback={<LoaderCircle size={11} class="animate-spin" />}
                      >
                        <RotateCcw size={11} />
                      </Show>
                      <span>{t("timeline.convoyRetry")}</span>
                    </button>
                  </Show>
                  <Show when={isConvoyPlanTerminal(livePlan().status)}>
                    <button
                      type="button"
                      onClick={onResetPlan}
                      class="rounded-lg border border-[var(--border-strong)] px-2 py-1 text-[11px] text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] cursor-pointer"
                    >
                      {t("timeline.convoyNewPlan")}
                    </button>
                  </Show>
                </div>

                <div class="flex items-center gap-1.5">
                  <Show
                    when={
                      livePlan().status === "succeeded" &&
                      livePlan().isolated &&
                      sessionID()
                    }
                  >
                    <Show
                      when={!livePlan().mergedAt}
                      fallback={
                        <span class="inline-flex items-center gap-1 rounded-lg bg-[var(--ok-surface)]/70 px-2.5 py-1 text-[11px] font-medium text-[var(--ok-text)]">
                          <CheckCircle2 size={11} />
                          <span>{t("timeline.inlineSubtasksMergedBadge")}</span>
                        </span>
                      }
                    >
                      <button
                        type="button"
                        disabled={merging()}
                        onClick={() => void onMerge()}
                        class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--accent-surface)]/60 px-2.5 py-1 text-[11px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] disabled:opacity-40 cursor-pointer"
                      >
                        <Show
                          when={!merging()}
                          fallback={<LoaderCircle size={11} class="animate-spin" />}
                        >
                          <GitMerge size={11} />
                        </Show>
                        <span>{t("timeline.inlineSubtasksMergeBtn")}</span>
                      </button>
                    </Show>
                  </Show>

                  <Show when={isConvoyPlanTerminal(livePlan().status) && sessionID()}>
                    <button
                      type="button"
                      disabled={summarizing()}
                      onClick={() => void onSummarizeInChat()}
                      class="inline-flex items-center gap-1.5 rounded-lg bg-[var(--accent-primary)] px-2.5 py-1 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 disabled:opacity-40 cursor-pointer"
                    >
                      <Show
                        when={!summarizing()}
                        fallback={<LoaderCircle size={11} class="animate-spin" />}
                      >
                        <MessageSquareShare size={11} />
                      </Show>
                      <span>{t("timeline.inlineSubtasksSummarizeBtn")}</span>
                    </button>
                  </Show>
                </div>
              </div>
            </div>
          )}
        </Show>
      </Show>
    </div>
  );
}
