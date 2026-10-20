// web/src/components/timeline/TaskProgressDetails.tsx
import { createEffect, createSignal, For, Show } from "solid-js";
import { CheckCircle2, Circle, ExternalLink, PauseCircle, Pencil, X } from "lucide-solid";
import type { SnapshotFileDiff } from "../../services/sse-types";
import { openChangedFileInInspector, openDiffInInspector, openFileInInspector, setWorkspace } from "../../store/workspace";
import { DiffChanges, FileIcon, Icon, Spinner, TextShimmer } from "../icons";
import { t } from "../../i18n";
import {
  formatToolDisplayName,
  formatToolStatusLabel,
  getFilename,
  kiloToolIconName,
} from "./tool-card-helpers";
import type { CycleStepItem, NormalizedTodoItem } from "./task-progress-helpers";

export default function TaskProgressDetails(props: {
  sessionID: string | null;
  todos: NormalizedTodoItem[];
  isSessionAlive: boolean;
  currentStep?: CycleStepItem;
  cycleSteps: CycleStepItem[];
  changedFiles: SnapshotFileDiff[];
  diffTotals: { additions: number; deletions: number };
}) {
  const [editingTodos, setEditingTodos] = createSignal(false);
  const [todoDraft, setTodoDraft] = createSignal("");
  const [todoStaged, setTodoStaged] = createSignal(false);

  createEffect(() => {
    props.sessionID;
    setEditingTodos(false);
    setTodoStaged(false);
  });

  const statusToken = (status: string) =>
    status === "completed" ? "x" : status === "in_progress" ? "~" : " ";

  const openTodoEditor = () => {
    setTodoDraft(
      props.todos
        .map((todo) => `[${statusToken(todo.status)}] ${todo.content}`)
        .join("\n")
    );
    setTodoStaged(false);
    setEditingTodos(true);
  };

  const stageTodoUpdate = () => {
    const items = todoDraft()
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean)
      .map((line) => {
        const match = line.match(/^\[([ xX~\-])\]\s*(.+)$/);
        if (!match) return { status: "pending", content: line };
        const token = match[1].toLowerCase();
        return {
          status: token === "x" ? "completed" : token === "~" ? "in_progress" : "pending",
          content: match[2].trim(),
        };
      })
      .filter((item) => item.content);
    if (items.length === 0 || !props.sessionID) return;
    const body = items
      .map((item, index) => `${index + 1}. [${item.status}] ${item.content}`)
      .join("\n");
    const prompt = `${t("timeline.todoUpdatePromptIntro")}\n\n${body}\n\n${t("timeline.todoUpdatePromptOutro")}`;
    setWorkspace("composerDraftEditBySession", props.sessionID, {
      id: `todo_${Date.now()}`,
      text: prompt,
      attachments: [],
    });
    setEditingTodos(false);
    setTodoStaged(true);
    setTimeout(() => setTodoStaged(false), 2400);
  };

  return (
    <div class="mt-2 space-y-2.5 border-t border-[var(--border-subtle)] pt-2 max-h-64 overflow-y-auto">
      {/* 1. Todo Checklist */}
      <Show when={props.todos.length > 0}>
        <div class="space-y-1.5">
          <div class="flex items-center justify-between gap-2">
            <div class="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
              <span>{t("timeline.todoPlanHeader")}</span>
              <Show when={todoStaged()}>
                <span class="normal-case tracking-normal font-medium text-[var(--ok-text)]">
                  {t("timeline.todoStagedNotice")}
                </span>
              </Show>
            </div>
            <button
              type="button"
              onClick={() => (editingTodos() ? setEditingTodos(false) : openTodoEditor())}
              class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              title={t("timeline.todoEditTooltip")}
            >
              <Show when={editingTodos()} fallback={<Pencil size={10} />}>
                <X size={10} />
              </Show>
              <span>{editingTodos() ? t("timeline.todoCancel") : t("timeline.todoEdit")}</span>
            </button>
          </div>
          <Show when={editingTodos()}>
            <div class="space-y-1.5 rounded-lg border border-[var(--accent-border)] bg-[var(--bg-panel)]/70 p-2">
              <p class="text-[11px] leading-snug text-[var(--text-muted)]">
                {t("timeline.todoEditHint")}
              </p>
              <textarea
                value={todoDraft()}
                onInput={(event) => setTodoDraft(event.currentTarget.value)}
                rows={Math.min(10, Math.max(3, props.todos.length + 1))}
                class="w-full resize-y rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1.5 font-mono text-[11px] leading-relaxed text-[var(--text-primary)] outline-none focus:border-[var(--accent-border)]"
              />
              <div class="flex items-center justify-end gap-1.5">
                <button
                  type="button"
                  onClick={() => setEditingTodos(false)}
                  class="rounded-md px-2 py-1 text-[11px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] cursor-pointer"
                >
                  {t("timeline.todoCancel")}
                </button>
                <button
                  type="button"
                  onClick={stageTodoUpdate}
                  class="rounded-md bg-[var(--accent-primary)] px-2 py-1 text-[11px] font-medium text-[var(--accent-foreground)] hover:opacity-90 cursor-pointer"
                >
                  {t("timeline.todoApplyToComposer")}
                </button>
              </div>
            </div>
          </Show>
          <Show when={!editingTodos()}>
          <For each={props.todos}>
            {(todo) => (
              <div class="space-y-1">
                <div class="flex items-start gap-2 text-[12px]">
                  <Show
                    when={todo.status === "completed"}
                    fallback={
                      <Show
                        when={todo.status === "in_progress" && props.isSessionAlive}
                        fallback={
                          <Show
                            when={todo.status === "interrupted"}
                            fallback={
                              <Circle
                                size={12}
                                class="mt-0.5 text-[var(--text-faint)] shrink-0"
                              />
                            }
                          >
                            <PauseCircle
                              size={12}
                              class="mt-0.5 text-[var(--warn-text)] shrink-0"
                            />
                          </Show>
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
                          : todo.status === "interrupted"
                            ? "text-[var(--warn-text)] font-medium"
                            : "text-[var(--text-secondary)]"
                    }
                  >
                    <TextShimmer
                      text={todo.content}
                      active={todo.status === "in_progress" && props.isSessionAlive}
                    />
                  </span>
                </div>

                {/* Inline live tool step under the currently active todo item */}
                <Show
                  when={
                    todo.status === "in_progress" &&
                    props.isSessionAlive &&
                    props.currentStep
                  }
                >
                  {(step) => (
                    <div class="ml-5 flex items-center gap-1.5 rounded-md bg-[var(--bg-panel)]/75 px-2 py-1 text-[11px] text-[var(--text-secondary)]">
                      <span class="text-[var(--accent-primary)]">↳</span>
                      <span class="font-mono text-[11px] text-[var(--text-muted)]">
                        {formatToolDisplayName(step().tool)}
                      </span>
                      <span class="font-mono truncate">{step().label}</span>
                      <Show when={step().additions > 0 || step().deletions > 0}>
                        <DiffChanges
                          changes={{
                            additions: step().additions,
                            deletions: step().deletions,
                          }}
                        />
                      </Show>
                    </div>
                  )}
                </Show>
              </div>
            )}
          </For>
          </Show>
        </div>
      </Show>

      {/* 2. Recent Execution Steps */}
      <Show when={props.cycleSteps.length > 0}>
        <div class="space-y-1 border-t border-[var(--border-subtle)]/60 pt-2">
          <div class="flex items-center justify-between text-[11px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
            <span>
              {t("timeline.liveStepsHeader")} ({props.cycleSteps.length})
            </span>
          </div>
          <div class="space-y-1">
            <For each={props.cycleSteps.slice(-6)}>
              {(step) => (
                <div class="flex items-center justify-between gap-2 rounded-md bg-[var(--bg-panel)]/70 px-2 py-1 text-[11px]">
                  <div class="flex items-center gap-1.5 min-w-0 flex-1">
                    <Show
                      when={step.status !== "running"}
                      fallback={
                        <span class="text-[var(--accent-primary)] shrink-0 inline-flex">
                          <Spinner class="w-3 h-3" />
                        </span>
                      }
                    >
                      <Icon
                        name={
                          step.status === "error" || step.status === "interrupted"
                            ? "circle-ban-sign"
                            : kiloToolIconName(step.tool)
                        }
                        size="small"
                        class={
                          step.status === "error"
                            ? "text-[var(--danger-text)] shrink-0"
                            : step.status === "interrupted"
                              ? "text-[var(--warn-text)] shrink-0"
                              : "text-[var(--text-muted)] shrink-0"
                        }
                      />
                    </Show>
                    <span class="font-mono text-[11px] text-[var(--text-secondary)] shrink-0">
                      {formatToolDisplayName(step.tool)}
                    </span>
                    <Show
                      when={step.file}
                      fallback={
                        <span class="text-[var(--text-muted)] truncate font-mono">
                          {step.label}
                        </span>
                      }
                    >
                      {(filePath) => (
                        <button
                          type="button"
                          onClick={() => {
                            if (step.additions > 0 || step.deletions > 0) {
                               openChangedFileInInspector(filePath());
                            } else {
                              openFileInInspector(filePath());
                            }
                          }}
                          class="inline-flex items-center gap-1 font-mono text-[var(--text-primary)] hover:text-[var(--accent-primary)] truncate cursor-pointer"
                        >
                          <FileIcon
                            node={{ path: filePath(), type: "file" }}
                            class="w-3 h-3 shrink-0"
                          />
                          <span class="truncate">{step.label}</span>
                        </button>
                      )}
                    </Show>
                  </div>
                  <div class="flex items-center gap-1.5 shrink-0">
                    <Show when={step.additions > 0 || step.deletions > 0}>
                      <DiffChanges
                        changes={{
                          additions: step.additions,
                          deletions: step.deletions,
                        }}
                      />
                    </Show>
                    <span
                      class={`font-mono text-[11px] ${
                        step.status === "completed"
                          ? "text-[var(--ok-text)]"
                          : step.status === "error"
                            ? "text-[var(--danger-text)]"
                            : step.status === "interrupted"
                              ? "text-[var(--warn-text)]"
                              : "text-[var(--accent-primary)]"
                      }`}
                    >
                      {step.status === "interrupted"
                        ? t("timeline.subagentInterruptedBadge")
                        : formatToolStatusLabel(step.status)}
                    </span>
                  </div>
                </div>
              )}
            </For>
          </div>
        </div>
      </Show>

      {/* 3. Codex-Style Modified Files Summary Bar */}
      <Show when={props.changedFiles.length > 0}>
        <div class="space-y-1.5 border-t border-[var(--border-subtle)]/60 pt-2">
          <div class="flex items-center justify-between text-[11px]">
            <div class="flex items-center gap-1.5 font-semibold uppercase tracking-wider text-[var(--text-faint)]">
              <span>
                {t("timeline.codeChangesHeader")} ({props.changedFiles.length})
              </span>
              <Show when={props.diffTotals.additions > 0 || props.diffTotals.deletions > 0}>
                <DiffChanges
                  changes={{
                    additions: props.diffTotals.additions,
                    deletions: props.diffTotals.deletions,
                  }}
                />
              </Show>
            </div>
            <button
              type="button"
              onClick={() => openDiffInInspector(props.changedFiles[0], "session")}
              class="inline-flex items-center gap-1 text-[11px] font-medium text-[var(--accent-primary)] hover:underline cursor-pointer"
            >
              <span>{t("timeline.reviewDiffs")}</span>
              <ExternalLink size={10} />
            </button>
          </div>

          <div class="flex flex-wrap gap-1.5">
            <For each={props.changedFiles}>
              {(item) => (
                <button
                  type="button"
                  onClick={() => openDiffInInspector(item, "session")}
                  class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-panel)]/85 px-2 py-1 font-mono text-[11px] text-[var(--text-primary)] hover:border-[var(--accent-border)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  title={item.file}
                >
                  <FileIcon
                    node={{ path: item.file, type: "file" }}
                    class="w-3.5 h-3.5 shrink-0"
                  />
                  <span class="truncate max-w-[180px]">{getFilename(item.file)}</span>
                  <Show when={(item.additions ?? 0) > 0 || (item.deletions ?? 0) > 0}>
                    <DiffChanges
                      changes={{
                        additions: item.additions ?? 0,
                        deletions: item.deletions ?? 0,
                      }}
                    />
                  </Show>
                </button>
              )}
            </For>
          </div>
        </div>
      </Show>
    </div>
  );
}
