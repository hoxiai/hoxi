// web/src/components/tasks/TaskOverviewView.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  Match,
  onCleanup,
  Show,
  Switch,
} from "solid-js";
import {
  Bot,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CircleDashed,
  ListChecks,
  ListTodo,
  LoaderCircle,
  MinusCircle,
  Workflow,
  XCircle,
} from "lucide-solid";
import { t } from "../../i18n";
import {
  activeDirectory,
  activeSession,
  buildTaskOverview,
  fetchConvoyPlans,
  fetchConvoyPlansForSession,
  isConvoyPlanTerminal,
  resolveParentSessionID,
  selectSession,
  workspace,
  type ConvoyPlan,
  type TaskGroupID,
  type TaskNodeStatus,
  type TaskOverviewNode,
} from "../../store/workspace";

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

function StatusIcon(props: { status: TaskNodeStatus }) {
  return (
    <Switch>
      <Match when={props.status === "running"}>
        <LoaderCircle size={12} class="shrink-0 animate-spin text-[var(--accent-primary)]" />
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

function TaskNodeRow(props: { node: TaskOverviewNode; depth: number }) {
  const [expanded, setExpanded] = createSignal(props.depth === 0);
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
          onClick={() => setExpanded((value) => !value)}
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
      </div>
      <Show when={props.node.dependsOn && props.node.dependsOn.length > 0}>
        <p class="pl-8 pr-2 text-[10px] text-[var(--text-faint)]">
          {t("tasks.dependsOn", { ids: (props.node.dependsOn ?? []).join(", ") })}
        </p>
      </Show>
      <Show when={expanded() && hasChildren()}>
        <div class="ml-3 border-l border-[var(--border-subtle)] pl-1">
          <For each={props.node.children}>
            {(child) => <TaskNodeRow node={child} depth={props.depth + 1} />}
          </For>
        </div>
      </Show>
    </div>
  );
}

export default function TaskOverviewView() {
  const [plans, setPlans] = createSignal<ConvoyPlan[]>([]);
  const [loading, setLoading] = createSignal(false);
  const [failed, setFailed] = createSignal(false);
  let requestSeq = 0;
  let lastScope = "";

  const directory = createMemo(() => activeDirectory() ?? "");
  const relatedSessionIDs = createMemo(() => {
    const ids: string[] = [];
    const seen = new Set<string>();
    let current = workspace.activeSessionID;
    for (let depth = 0; depth < 8; depth++) {
      const sessionID = current;
      if (!sessionID || seen.has(sessionID)) break;
      seen.add(sessionID);
      ids.push(sessionID);
      current = resolveParentSessionID(sessionID);
    }
    return ids;
  });

  const load = (dir: string, sessionIDs: string[]) => {
    const seq = ++requestSeq;
    const scope = `${dir}|${sessionIDs.join(",")}`;
    if (scope !== lastScope) {
      lastScope = scope;
      setPlans([]);
    }
    setLoading(true);
    const scoped = sessionIDs.map((id) =>
      fetchConvoyPlansForSession(id).catch(() => [] as ConvoyPlan[])
    );
    Promise.all([
      dir ? fetchConvoyPlans(dir) : Promise.resolve([] as ConvoyPlan[]),
      ...scoped,
    ])
      .then(([byDirectory, ...bySession]) => {
        if (seq !== requestSeq) return;
        const merged = new Map<string, ConvoyPlan>();
        for (const plan of byDirectory) merged.set(plan.id, plan);
        for (const list of bySession) {
          for (const plan of list) merged.set(plan.id, plan);
        }
        setPlans([...merged.values()]);
        setFailed(false);
      })
      .catch(() => {
        if (seq === requestSeq) setFailed(true);
      })
      .finally(() => {
        if (seq === requestSeq) setLoading(false);
      });
  };

  createEffect(() => {
    const dir = directory();
    const sessionIDs = relatedSessionIDs();
    if (!dir && sessionIDs.length === 0) {
      requestSeq++;
      setPlans([]);
      setFailed(false);
      setLoading(false);
      return;
    }
    load(dir, sessionIDs);
  });

  createEffect(() => {
    if (!workspace.inspectorOpen || workspace.inspectorTab !== "tasks") return;
    const hasActive = plans().some((plan) => !isConvoyPlanTerminal(plan.status));
    if (!hasActive) return;
    const dir = directory();
    const sessionIDs = relatedSessionIDs();
    const timer = setInterval(() => load(dir, sessionIDs), 2500);
    onCleanup(() => clearInterval(timer));
  });

  const groups = createMemo(() => buildTaskOverview(plans()));
  const total = createMemo(() => groups().reduce((sum, group) => sum + group.nodes.length, 0));

  return (
    <div class="flex h-full flex-col">
      <div class="shrink-0 border-b border-[var(--border-subtle)] px-3 py-2">
        <div class="flex items-center gap-2">
          <ListChecks size={14} class="shrink-0 text-[var(--accent-primary)]" />
          <div class="min-w-0">
            <div class="text-[12px] font-semibold text-[var(--text-primary)]">{t("tasks.title")}</div>
            <div class="truncate text-[11px] text-[var(--text-muted)]">
              {activeSession()?.title || t("tasks.noSession")}
            </div>
          </div>
          <Show when={loading()}>
            <LoaderCircle size={12} class="ml-auto shrink-0 animate-spin text-[var(--text-muted)]" />
          </Show>
        </div>
      </div>

      <div class="min-h-0 flex-1 overflow-y-auto px-2 py-2">
        <Show
          when={workspace.activeSessionID}
          fallback={<p class="px-2 py-1 text-[11px] text-[var(--text-faint)]">{t("tasks.noSession")}</p>}
        >
          <Show
            when={total() > 0}
            fallback={
              <p class="px-2 py-1 text-[11px] text-[var(--text-faint)]">
                {failed() ? t("tasks.loadFailed") : t("tasks.empty")}
              </p>
            }
          >
            <For each={groups()}>
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
                    <Show when={group.nodes.length > 0}>
                      <span class="rounded bg-[var(--bg-inset)] px-1 font-mono text-[10px]">
                        {group.nodes.length}
                      </span>
                    </Show>
                  </div>
                  <Show
                    when={group.nodes.length > 0}
                    fallback={
                      <p class="px-2 py-0.5 text-[11px] text-[var(--text-faint)]">
                        {t("tasks.groupEmpty")}
                      </p>
                    }
                  >
                    <For each={group.nodes}>
                      {(node) => <TaskNodeRow node={node} depth={0} />}
                    </For>
                  </Show>
                </section>
              )}
            </For>
          </Show>
        </Show>
      </div>
    </div>
  );
}
