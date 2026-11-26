// web/src/components/tasks/PortalExecutionTimeline.tsx
// Noise-reduced, card-free execution stream shared across QA & collaboration portals:
// folds contiguous tool/todo steps into a single quiet collapsible row with +/- diff line stats
// while rendering AI Markdown prose directly on the canvas.
import { createEffect, createMemo, createSignal, For, Match, Show, Switch } from "solid-js";
import {
  CheckCircle2,
  ChevronRight,
  CircleDashed,
  FileCode2,
  ListTodo,
  LoaderCircle,
  Search,
  Terminal,
  Wrench,
  XCircle,
} from "lucide-solid";
import { t } from "../../i18n";
import type {
  SanitizedExecutionStep,
  SanitizedExecutionTurn,
} from "../../services/intake-api";
import { DiffChanges } from "../icons";
import MarkdownInline from "../timeline/MarkdownInline";
import MarkdownView from "../timeline/MarkdownView";
import {
  extractStepFileChanges,
  formatPortalDuration,
  groupSanitizedTurns,
  type PortalActivitySegment,
  type PortalDiffSummary,
  type PortalNoticeSegment,
} from "./portal-timeline-helpers";

const MAX_EXPANDED_ENTRIES = 256;
const activityExpandedState = new Map<string, boolean>();
const diffFilesExpandedState = new Map<string, boolean>();

function rememberExpanded(map: Map<string, boolean>, key: string, value: boolean) {
  if (map.has(key)) map.delete(key);
  else if (map.size >= MAX_EXPANDED_ENTRIES) {
    const oldest = map.keys().next().value;
    if (oldest !== undefined) map.delete(oldest);
  }
  map.set(key, value);
}

function StepCategoryIcon(props: { category?: string; status?: string; active?: boolean }) {
  return (
    <Switch>
      <Match when={props.active && (props.status === "running" || props.status === "pending")}>
        <LoaderCircle size={12.5} class="shrink-0 animate-spin text-[var(--accent-primary)]" />
      </Match>
      <Match when={props.status === "error"}>
        <XCircle size={12.5} class="shrink-0 text-[var(--danger-text)]" />
      </Match>
      <Match when={props.category === "search"}>
        <Search size={12} class="shrink-0 text-[var(--text-faint)]" />
      </Match>
      <Match when={props.category === "command"}>
        <Terminal size={12} class="shrink-0 text-[var(--text-faint)]" />
      </Match>
      <Match when={props.category === "edit"}>
        <Wrench size={12} class="shrink-0 text-[var(--accent-primary)]/85" />
      </Match>
      <Match when={true}>
        <FileCode2 size={12} class="shrink-0 text-[var(--text-faint)]" />
      </Match>
    </Switch>
  );
}

export function PortalDiffSummaryBar(props: {
  diff: PortalDiffSummary;
  scopeKey?: string;
  compact?: boolean;
}) {
  const hasLines = () => props.diff.additions > 0 || props.diff.deletions > 0;
  const hasFiles = () => props.diff.files.length > 0;
  const visible = () => hasLines() || hasFiles();
  const expandable = () => props.diff.files.length > 1;
  const stateKey = () => `diff:${props.scopeKey || "default"}`;

  const [open, setOpenSignal] = createSignal(diffFilesExpandedState.get(stateKey()) ?? false);
  createEffect(() => {
    setOpenSignal(diffFilesExpandedState.get(stateKey()) ?? false);
  });
  const toggleOpen = () => {
    if (!expandable()) return;
    const next = !open();
    rememberExpanded(diffFilesExpandedState, stateKey(), next);
    setOpenSignal(next);
  };

  const summaryLabel = () => {
    if (props.diff.files.length === 1) {
      return t("tasks.portalEditedSingleFile", { file: props.diff.files[0].file });
    }
    if (props.diff.files.length > 1) {
      return t("tasks.portalEditedFilesSummary", { count: String(props.diff.files.length) });
    }
    return t("tasks.portalEditedLinesOnly");
  };

  return (
    <Show when={visible()}>
      <div class={props.compact ? "text-[11.5px]" : "text-[12px]"}>
        <Show
          when={expandable()}
          fallback={
            <div class="inline-flex items-center gap-2 text-[var(--text-muted)]">
              <FileCode2 size={12.5} class="shrink-0 text-[var(--text-faint)]" />
              <span class="truncate">{summaryLabel()}</span>
              <Show when={hasLines()}>
                <DiffChanges
                  changes={{ additions: props.diff.additions, deletions: props.diff.deletions }}
                />
              </Show>
            </div>
          }
        >
          <button
            type="button"
            onClick={toggleOpen}
            aria-expanded={open()}
            class="inline-flex items-center gap-1.5 rounded-md -ml-1.5 px-1.5 py-0.5 text-[var(--text-muted)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer select-none"
          >
            <FileCode2 size={12.5} class="shrink-0 text-[var(--text-faint)]" />
            <span>{summaryLabel()}</span>
            <Show when={hasLines()}>
              <DiffChanges
                changes={{ additions: props.diff.additions, deletions: props.diff.deletions }}
              />
            </Show>
            <ChevronRight
              size={12}
              class={`shrink-0 text-[var(--text-faint)] transition-transform duration-150 ${
                open() ? "rotate-90" : ""
              }`}
            />
          </button>

          <Show when={open()}>
            <div class="mt-1 ml-[5px] border-l border-[var(--border-subtle)]/55 pl-3 space-y-0.5">
              <For each={props.diff.files}>
                {(item) => (
                  <div class="flex items-center justify-between gap-3 py-0.5 text-[11.5px]">
                    <span class="min-w-0 truncate font-mono text-[var(--text-secondary)]">
                      {item.file}
                    </span>
                    <Show when={item.additions > 0 || item.deletions > 0}>
                      <DiffChanges
                        changes={{ additions: item.additions, deletions: item.deletions }}
                      />
                    </Show>
                  </div>
                )}
              </For>
            </div>
          </Show>
        </Show>
      </div>
    </Show>
  );
}

function PortalToolStepLine(props: { step: SanitizedExecutionStep; active?: boolean }) {
  const duration = () => formatPortalDuration(props.step.startTime, props.step.endTime);
  const stepFiles = createMemo(() => extractStepFileChanges(props.step));
  const hasLineCounts = () =>
    props.step.status !== "error" &&
    ((props.step.additions ?? 0) > 0 || (props.step.deletions ?? 0) > 0);

  return (
    <div class="py-0.5">
      <div class="flex select-none items-center justify-between gap-2 text-[12px] leading-[1.45]">
        <div class="flex min-w-0 items-center gap-2">
          <StepCategoryIcon
            category={props.step.category}
            status={props.step.status}
            active={props.active}
          />
          <span
            class={`truncate ${
              props.step.status === "error"
                ? "text-[var(--danger-text)]"
                : props.step.category === "edit"
                  ? "text-[var(--text-primary)]"
                  : "text-[var(--text-secondary)]"
            }`}
          >
            {props.step.label}
          </span>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <Show when={hasLineCounts()}>
            <DiffChanges
              changes={{
                additions: props.step.additions ?? 0,
                deletions: props.step.deletions ?? 0,
              }}
            />
          </Show>
          <Show when={duration()}>
            <span class="font-mono text-[10.5px] tabular-nums text-[var(--text-faint)]">
              {duration()}
            </span>
          </Show>
        </div>
      </div>

      <Show when={stepFiles().length > 1}>
        <div class="mt-0.5 ml-5 space-y-0.5">
          <For each={stepFiles()}>
            {(f) => (
              <div class="flex items-center justify-between gap-2 text-[11px]">
                <span class="min-w-0 truncate font-mono text-[var(--text-muted)]">
                  {f.file}
                </span>
                <Show when={f.additions > 0 || f.deletions > 0}>
                  <DiffChanges changes={{ additions: f.additions, deletions: f.deletions }} />
                </Show>
              </div>
            )}
          </For>
        </div>
      </Show>
    </div>
  );
}

function PortalActivityGroupRow(props: {
  segment: PortalActivitySegment;
  busy?: boolean;
  isLiveTail?: boolean;
  scopeKey?: string;
}) {
  const persistKey = () => `${props.scopeKey || "portal"}:${props.segment.key}`;
  const [open, setOpenSignal] = createSignal(activityExpandedState.get(persistKey()) ?? false);
  createEffect(() => {
    setOpenSignal(activityExpandedState.get(persistKey()) ?? false);
  });

  const toggleOpen = () => {
    const next = !open();
    rememberExpanded(activityExpandedState, persistKey(), next);
    setOpenSignal(next);
  };

  const isRunning = () => Boolean(props.busy && (props.isLiveTail || props.segment.hasRunning));
  const duration = () => formatPortalDuration(props.segment.startTime, props.segment.endTime);
  const hasDiffLines = () =>
    props.segment.diff.additions > 0 || props.segment.diff.deletions > 0;
  const todoStats = createMemo(() => {
    const list = props.segment.latestTodos;
    const done = list.filter((it) => it.status === "completed").length;
    return { done, total: list.length };
  });

  const headline = createMemo(() => {
    if (isRunning()) {
      return (
        props.segment.activeLabel ||
        t("tasks.portalStepsSummary", { count: String(Math.max(1, props.segment.toolCount)) })
      );
    }
    if (props.segment.toolCount > 0) {
      return t("tasks.portalStepsSummary", { count: String(props.segment.toolCount) });
    }
    return t("tasks.portalTodoProgress", {
      done: String(todoStats().done),
      total: String(todoStats().total),
    });
  });

  return (
    <div>
      <button
        type="button"
        onClick={toggleOpen}
        aria-expanded={open()}
        class="group/activity flex w-full items-center justify-between gap-2 rounded-md -mx-2 px-2 py-1 text-left text-[12.5px] transition-colors hover:bg-[var(--bg-hover)] cursor-pointer select-none"
      >
        <div class="flex min-w-0 flex-1 items-center gap-2">
          <Show
            when={isRunning()}
            fallback={
              <Show
                when={props.segment.hasError}
                fallback={
                  <CheckCircle2
                    size={13}
                    class="shrink-0 text-[var(--text-faint)] group-hover/activity:text-[var(--text-secondary)] transition-colors"
                  />
                }
              >
                <XCircle size={13} class="shrink-0 text-[var(--danger-text)]" />
              </Show>
            }
          >
            <LoaderCircle size={13} class="shrink-0 animate-spin text-[var(--accent-primary)]" />
          </Show>

          <span
            class={`truncate ${
              isRunning()
                ? "font-medium text-[var(--accent-primary)]"
                : "text-[var(--text-muted)] group-hover/activity:text-[var(--text-primary)] transition-colors"
            }`}
          >
            {headline()}
          </span>

          <Show when={isRunning() && props.segment.toolCount > 1}>
            <span class="shrink-0 text-[11px] text-[var(--text-faint)]">
              · {t("tasks.portalStepsRunningCount", { count: String(props.segment.toolCount) })}
            </span>
          </Show>

          <Show when={!isRunning() && props.segment.diff.files.length > 0}>
            <span class="hidden sm:inline shrink-0 text-[11.5px] text-[var(--text-faint)]">
              ·{" "}
              {props.segment.diff.files.length === 1
                ? t("tasks.portalEditedSingleFile", { file: props.segment.diff.files[0].file })
                : t("tasks.portalEditedFilesSummary", {
                    count: String(props.segment.diff.files.length),
                  })}
            </span>
          </Show>
        </div>

        <div class="flex shrink-0 items-center gap-2">
          <Show when={hasDiffLines()}>
            <DiffChanges
              changes={{
                additions: props.segment.diff.additions,
                deletions: props.segment.diff.deletions,
              }}
            />
          </Show>
          <Show when={duration()}>
            <span class="font-mono text-[11px] tabular-nums text-[var(--text-faint)]">
              {duration()}
            </span>
          </Show>
          <ChevronRight
            size={13}
            class={`shrink-0 text-[var(--text-faint)] transition-transform duration-150 ${
              open() ? "rotate-90" : "opacity-60 group-hover/activity:opacity-100"
            }`}
          />
        </div>
      </button>

      <Show when={open()}>
        <div class="mt-1 ml-[6px] border-l border-[var(--border-subtle)]/55 pl-3.5 space-y-0.5">
          <For each={props.segment.tools}>
            {(step) => <PortalToolStepLine step={step} active={isRunning()} />}
          </For>

          <Show when={props.segment.latestTodos.length > 0}>
            <div
              class={`space-y-1 ${
                props.segment.tools.length > 0
                  ? "mt-1.5 border-t border-[var(--border-subtle)]/40 pt-1.5"
                  : ""
              }`}
            >
              <div class="flex items-center gap-1.5 text-[11px] font-medium text-[var(--text-muted)]">
                <ListTodo size={12} class="text-[var(--text-faint)]" />
                <span>
                  {t("tasks.portalTodoProgress", {
                    done: String(todoStats().done),
                    total: String(todoStats().total),
                  })}
                </span>
              </div>
              <For each={props.segment.latestTodos}>
                {(todo) => (
                  <div class="flex items-start gap-2 text-[12px] leading-[1.5] text-[var(--text-secondary)]">
                    <span class="mt-0.5 shrink-0">
                      <Show
                        when={todo.status === "completed"}
                        fallback={
                          <Show
                            when={isRunning() && todo.status === "in_progress"}
                            fallback={<CircleDashed size={11.5} class="text-[var(--text-faint)]" />}
                          >
                            <LoaderCircle
                              size={11.5}
                              class="animate-spin text-[var(--accent-primary)]"
                            />
                          </Show>
                        }
                      >
                        <CheckCircle2 size={11.5} class="text-[var(--ok-text)]" />
                      </Show>
                    </span>
                    <div
                      class={`min-w-0 flex-1 break-words ${
                        todo.status === "completed" ? "line-through opacity-65" : ""
                      }`}
                    >
                      <MarkdownInline text={todo.content} />
                    </div>
                  </div>
                )}
              </For>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}

export default function PortalExecutionTimeline(props: {
  turns: SanitizedExecutionTurn[];
  busy?: boolean;
  isLastBlock?: boolean;
  scopeKey?: string;
}) {
  const segments = createMemo(() => groupSanitizedTurns(props.turns));

  return (
    <div class="space-y-3">
      <For each={segments()}>
        {(seg, idx) => {
          const isTail = () =>
            Boolean(props.busy) &&
            Boolean(props.isLastBlock ?? true) &&
            idx() === segments().length - 1;

          return (
            <Switch>
              <Match when={seg.kind === "notice"}>
                {(() => {
                  const notice = seg as PortalNoticeSegment;
                  const label = () =>
                    notice.category === "developer_followup"
                      ? t("tasks.portalDeveloperFollowupLabel")
                      : notice.label || t("tasks.portalDeveloperFollowupLabel");
                  return (
                    <div class="space-y-2 pt-1">
                      <div class="flex items-center gap-2.5 text-[11px] font-medium text-[var(--text-faint)] select-none">
                        <span class="h-px flex-1 bg-[var(--border-subtle)]/60" />
                        <span>{label()}</span>
                        <span class="h-px flex-1 bg-[var(--border-subtle)]/60" />
                      </div>
                      <Show when={isTail()}>
                        <div class="flex items-center gap-2 text-[12.5px] text-[var(--accent-primary)]">
                          <LoaderCircle size={13} class="shrink-0 animate-spin" />
                          <span>{t("tasks.portalContinuingProgress")}</span>
                        </div>
                      </Show>
                    </div>
                  );
                })()}
              </Match>
              <Match when={seg.kind === "text"}>
                <div class="text-[var(--text-primary)]">
                  <MarkdownView
                    content={(seg as Extract<typeof seg, { kind: "text" }>).text}
                    density="chat"
                    streaming={isTail()}
                    portal
                  />
                </div>
              </Match>
              <Match when={seg.kind === "activity"}>
                <PortalActivityGroupRow
                  segment={seg as PortalActivitySegment}
                  busy={props.busy}
                  isLiveTail={isTail()}
                  scopeKey={props.scopeKey}
                />
              </Match>
            </Switch>
          );
        }}
      </For>
    </div>
  );
}
