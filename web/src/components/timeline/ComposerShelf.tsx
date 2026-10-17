// web/src/components/timeline/ComposerShelf.tsx
import { createMemo, createSignal, For, Show } from "solid-js";
import {
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Circle,
  ExternalLink,
  FileDiff,
  ListTodo,
  LoaderCircle,
  MessageSquareText,
  X,
} from "lucide-solid";
import {
  clearReviewComments,
  openDiffInInspector,
  openFileInInspector,
  removeReviewComment,
  workspace,
} from "../../store/workspace";
import ApprovalGateCard from "../approval/ApprovalGateCard";
import QuestionDock from "../approval/QuestionDock";
import { DiffChanges, FileIcon, Spinner, TextShimmer } from "../icons";
import AgentStackPill from "./AgentStackPill";
import PlanFollowupBar from "./PlanFollowupBar";
import RevertBanner from "./RevertBanner";
import { t } from "../../i18n";

interface TodoItem {
  id?: string;
  content: string;
  status: "pending" | "in_progress" | "completed" | "cancelled" | string;
}

export default function ComposerShelf() {
  const [todosExpanded, setTodosExpanded] = createSignal(false);
  const [reviewsExpanded, setReviewsExpanded] = createSignal(true);

  const sessionID = () => workspace.activeSessionID;

  const pendingPermissions = createMemo(() => {
    const id = sessionID();
    return id ? workspace.pendingPermissions.filter((p) => p.sessionID === id) : [];
  });

  const pendingQuestions = createMemo(() => {
    const id = sessionID();
    return id ? workspace.pendingQuestions.filter((q) => q.sessionID === id) : [];
  });

  const reviewComments = createMemo(() => {
    const id = sessionID();
    return id ? (workspace.reviewCommentsBySession[id] ?? []) : [];
  });

  const reviewFileCount = createMemo(
    () => new Set(reviewComments().map((c) => c.file)).size
  );

  // Extract latest TodoWrite items from session messages (Kilo Composer Shelf pattern)
  const todos = createMemo<TodoItem[]>(() => {
    const id = sessionID();
    if (!id) return [];
    const turns = workspace.messagesBySession[id] ?? [];
    for (let i = turns.length - 1; i >= 0; i--) {
      const parts = turns[i].parts ?? [];
      for (let j = parts.length - 1; j >= 0; j--) {
        const part = parts[j];
        if (part.type === "tool" && (part.tool ?? "").toLowerCase() === "todowrite") {
          const raw =
            (part.state?.input?.todos as TodoItem[] | undefined) ??
            ((part.state as any)?.metadata?.todos as TodoItem[] | undefined);
          if (Array.isArray(raw) && raw.length > 0) {
            return raw;
          }
        }
      }
    }
    return [];
  });

  const completedTodoCount = createMemo(
    () => todos().filter((t) => t.status === "completed").length
  );

  const activeTodo = createMemo(
    () =>
      todos().find((t) => t.status === "in_progress") ??
      todos().find((t) => t.status === "pending")
  );

  // Session Diff Summary
  const sessionDiffs = createMemo(() => {
    const id = sessionID();
    return id ? (workspace.diffsBySession[id] ?? []) : [];
  });

  const diffTotals = createMemo(() => {
    let additions = 0;
    let deletions = 0;
    for (const d of sessionDiffs()) {
      additions += d.additions || 0;
      deletions += d.deletions || 0;
    }
    return { additions, deletions };
  });

  return (
    <>
      {/* Kilo QuestionDock: Docked Interactive Questions (question.asked) */}
      <Show when={pendingQuestions().length > 0}>
        <div class="mb-2.5 space-y-2">
          <For each={pendingQuestions()}>
            {(request) => <QuestionDock request={request} />}
          </For>
        </div>
      </Show>

      {/* Kilo SessionDock: Docked Permission Approvals (Never scrolls out of view) */}
      <Show when={pendingPermissions().length > 0}>
        <div class="mb-2.5 space-y-2">
          <For each={pendingPermissions()}>
            {(request) => <ApprovalGateCard request={request} />}
          </For>
        </div>
      </Show>

      {/* Kilo RevertBanner: Checkpoint Time-Travel Status & Redo Bar */}
      <div class="mb-2 space-y-2">
        <RevertBanner />
        <AgentStackPill />
      </div>

      {/* Kilo Plan Follow-up Suggestion Bar */}
      <PlanFollowupBar />

      {/* Kilo Contextual Shelf: ReviewComments, TodoWrite Checklist & Session Diff Summary */}
      <Show
        when={
          reviewComments().length > 0 ||
          todos().length > 0 ||
          sessionDiffs().length > 0
        }
      >
        <div class="mb-2 flex flex-col gap-1.5">
          {/* 1. Ported from kilocode ReviewComments.tsx: Queued Inline Diff Comments */}
          <Show when={reviewComments().length > 0}>
            <div class="rounded-xl border border-[var(--accent-border)] bg-[var(--accent-surface)]/40 px-3 py-2 text-xs">
              <div class="flex items-center justify-between gap-2">
                <button
                  type="button"
                  class="flex items-center gap-2 text-left cursor-pointer"
                  onClick={() => setReviewsExpanded((v) => !v)}
                >
                  <MessageSquareText size={13} class="text-[var(--accent-primary)] shrink-0" />
                  <span class="font-semibold text-[var(--text-primary)]">
                    {reviewComments().length === 1
                      ? t("timeline.reviewCommentSingular", { count: reviewComments().length })
                      : t("timeline.reviewCommentPlural", { count: reviewComments().length })}
                  </span>
                  <Show when={reviewFileCount() > 1}>
                    <span class="text-[11px] text-[var(--text-muted)]">
                      {t("timeline.filesCount", { count: reviewFileCount() })}
                    </span>
                  </Show>
                  <span class="text-[var(--text-muted)]">
                    {reviewsExpanded() ? <ChevronDown size={13} /> : <ChevronUp size={13} />}
                  </span>
                </button>

                <div class="flex items-center gap-2">
                  <button
                    type="button"
                    class="text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
                    onClick={() => {
                      const id = sessionID();
                      if (id) clearReviewComments(id);
                    }}
                  >
                    {t("timeline.clearAll")}
                  </button>
                </div>
              </div>

              <Show when={reviewsExpanded()}>
                <div class="mt-2 space-y-1 border-t border-[var(--border-subtle)] pt-2 max-h-36 overflow-y-auto">
                  <For each={reviewComments()}>
                    {(item) => (
                      <div class="flex items-center justify-between gap-2 rounded-lg bg-[var(--bg-panel)]/80 px-2.5 py-1.5 text-[11.5px]">
                        <div class="flex items-center gap-2 min-w-0 flex-1">
                          <button
                            type="button"
                            onClick={() => openFileInInspector(item.file)}
                            class="inline-flex items-center gap-1.5 font-mono text-[11px] font-medium text-[var(--accent-text)] hover:underline shrink-0 cursor-pointer"
                            title={t("timeline.openInInspectorTooltip")}
                          >
                            <FileIcon
                              node={{ path: item.file, type: "file" }}
                              class="w-3.5 h-3.5 shrink-0"
                            />
                            <span>
                              {item.file}
                              {item.line ? `:${item.line}` : ""}
                            </span>
                            <ExternalLink size={10} />
                          </button>
                          <span class="text-[var(--text-secondary)] truncate">{item.body}</span>
                        </div>
                        <button
                          type="button"
                          onClick={() => {
                            const id = sessionID();
                            if (id) removeReviewComment(id, item.id);
                          }}
                          class="text-[var(--text-muted)] hover:text-[var(--danger-text)] p-0.5 cursor-pointer"
                          title={t("timeline.removeCommentTooltip")}
                        >
                          <X size={12} />
                        </button>
                      </div>
                    )}
                  </For>
                </div>
              </Show>
            </div>
          </Show>

          {/* 2. TodoWrite Task Progress Bar */}
          <Show when={todos().length > 0}>
            <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/80 px-3 py-2 text-xs transition-all">
              <button
                type="button"
                class="flex w-full items-center justify-between gap-2 text-left cursor-pointer"
                onClick={() => setTodosExpanded((v) => !v)}
              >
                <div class="flex items-center gap-2 min-w-0">
                  <Show
                    when={activeTodo()?.status === "in_progress"}
                    fallback={<ListTodo size={13} class="text-[var(--accent-primary)] shrink-0" />}
                  >
                    <span class="text-[var(--accent-primary)] shrink-0 inline-flex items-center">
                      <Spinner class="w-3.5 h-3.5" />
                    </span>
                  </Show>
                  <span class="font-medium text-[var(--text-primary)] shrink-0">
                    {t("timeline.tasksProgress", {
                      completed: completedTodoCount(),
                      total: todos().length,
                    })}
                  </span>
                  <Show when={!todosExpanded() && activeTodo()}>
                    <span class="text-[var(--text-muted)] truncate inline-flex items-center gap-1 min-w-0">
                      <span>—</span>
                      <TextShimmer
                        text={activeTodo()!.content}
                        active={activeTodo()!.status === "in_progress"}
                        class="truncate"
                      />
                    </span>
                  </Show>
                </div>
                <span class="text-[var(--text-muted)] shrink-0">
                  {todosExpanded() ? <ChevronDown size={13} /> : <ChevronUp size={13} />}
                </span>
              </button>

              <Show when={todosExpanded()}>
                <div class="mt-2 space-y-1.5 border-t border-[var(--border-subtle)] pt-2 max-h-40 overflow-y-auto">
                  <For each={todos()}>
                    {(todo) => (
                      <div class="flex items-start gap-2 text-[12px]">
                        <Show
                          when={todo.status === "completed"}
                          fallback={
                            <Show
                              when={todo.status === "in_progress"}
                              fallback={
                                <Circle
                                  size={12}
                                  class="mt-0.5 text-[var(--text-faint)] shrink-0"
                                />
                              }
                            >
                              <span class="mt-0.5 text-[var(--accent-primary)] shrink-0 inline-flex items-center">
                                <Spinner class="w-3 h-3" />
                              </span>
                            </Show>
                          }
                        >
                          <CheckCircle2
                            size={12}
                            class="mt-0.5 text-[var(--ok-text)] shrink-0"
                          />
                        </Show>
                        <span
                          class={
                            todo.status === "completed"
                              ? "text-[var(--text-muted)] line-through"
                              : todo.status === "in_progress"
                                ? "text-[var(--text-primary)] font-medium"
                                : "text-[var(--text-secondary)]"
                          }
                        >
                          <TextShimmer
                            text={todo.content}
                            active={todo.status === "in_progress"}
                          />
                        </span>
                      </div>
                    )}
                  </For>
                </div>
              </Show>
            </div>
          </Show>

          {/* 3. Changed Files Summary Shelf */}
          <Show when={sessionDiffs().length > 0}>
            <div class="flex items-center justify-between gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-inset)]/80 px-3 py-1.5 text-[11.5px]">
              <div class="flex items-center gap-2 text-[var(--text-secondary)]">
                <FileDiff size={13} class="text-[var(--accent-primary)]" />
                <span class="font-medium text-[var(--text-primary)]">
                  {t("timeline.filesChangedCount", { count: sessionDiffs().length })}
                </span>
                <DiffChanges changes={diffTotals()} />
                <DiffChanges changes={diffTotals()} variant="bars" />
              </div>
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-lg px-2 py-0.5 text-[11px] font-medium text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] transition-colors cursor-pointer"
                onClick={() => openDiffInInspector()}
              >
                {t("timeline.reviewDiffs")}
              </button>
            </div>
          </Show>
        </div>
      </Show>
    </>
  );
}
