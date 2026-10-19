// web/src/components/timeline/TaskProgressDetails.tsx
import { For, Show } from "solid-js";
import { CheckCircle2, Circle, ExternalLink, PauseCircle } from "lucide-solid";
import type { SnapshotFileDiff } from "../../services/sse-types";
import { openChangedFileInInspector, openDiffInInspector, openFileInInspector } from "../../store/workspace";
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
  todos: NormalizedTodoItem[];
  isSessionAlive: boolean;
  currentStep?: CycleStepItem;
  cycleSteps: CycleStepItem[];
  changedFiles: SnapshotFileDiff[];
  diffTotals: { additions: number; deletions: number };
}) {
  return (
    <div class="mt-2 space-y-2.5 border-t border-[var(--border-subtle)] pt-2 max-h-64 overflow-y-auto">
      {/* 1. Todo Checklist */}
      <Show when={props.todos.length > 0}>
        <div class="space-y-1.5">
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
                      <span class="font-mono text-[10.5px] text-[var(--text-muted)]">
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
        </div>
      </Show>

      {/* 2. Recent Execution Steps */}
      <Show when={props.cycleSteps.length > 0}>
        <div class="space-y-1 border-t border-[var(--border-subtle)]/60 pt-2">
          <div class="flex items-center justify-between text-[10.5px] font-semibold uppercase tracking-wider text-[var(--text-faint)]">
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
                    <span class="font-mono text-[10.5px] text-[var(--text-secondary)] shrink-0">
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
                      class={`font-mono text-[10px] ${
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
          <div class="flex items-center justify-between text-[10.5px]">
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
