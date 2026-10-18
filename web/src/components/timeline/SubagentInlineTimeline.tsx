// web/src/components/timeline/SubagentInlineTimeline.tsx
import { createEffect, createMemo, For, onCleanup, Show } from "solid-js";
import {
  isSessionBusy,
  loadSessionMessagesOnly,
  openChangedFileInInspector,
  openFileInInspector,
  workspace,
} from "../../store/workspace";
import { DiffChanges, FileIcon, Icon, Spinner } from "../icons";
import { t } from "../../i18n";
import {
  extractCommand,
  extractDescription,
  extractTargetFile,
  formatToolDisplayName,
  formatToolStatusLabel,
  getFilename,
  isEditingTool,
  isTodoTool,
  kiloToolIconName,
  type ToolPart,
} from "./tool-card-helpers";
import { collectCycleChangedFiles, resolveToolFileDiff } from "./tool-card-state";

export function resolveSubagentCurrentStepLabel(
  childSessionID: string | undefined
): string | undefined {
  if (!childSessionID) return undefined;
  const turns = workspace.messagesBySession[childSessionID] ?? [];
  const tools: ToolPart[] = [];
  for (const turn of turns) {
    for (const part of turn.parts ?? []) {
      if (part.type === "tool" && !isTodoTool(part.tool)) {
        tools.push(part as ToolPart);
      }
    }
  }
  const activeTool =
    [...tools]
      .reverse()
      .find((tp) => tp.state.status === "running" || tp.state.status === "pending") ??
    tools[tools.length - 1];
  if (!activeTool) return undefined;
  const file = extractTargetFile(activeTool);
  const cmd = extractCommand(activeTool);
  const desc = extractDescription(activeTool);
  const detail = file ? getFilename(file) : cmd ? `$ ${cmd}` : desc || "";
  const toolLabel = formatToolDisplayName(activeTool.tool);
  return detail ? `${toolLabel} ${detail}` : toolLabel;
}

/**
 * Reactive telemetry hook for a `task` (Subagent) ToolCard so the collapsed
 * single-line row can show live step count, current tool label, and +A/-D stats.
 */
export function useSubagentTelemetry(
  childSessionID: () => string | undefined,
  parentRunning: () => boolean
) {
  let prevRunning = false;

  const childAlive = createMemo(() => {
    const id = childSessionID();
    if (!id) return false;
    return parentRunning() || isSessionBusy(id);
  });

  createEffect(() => {
    const id = childSessionID();
    const running = childAlive();
    if (!id) return;
    const existing = workspace.messagesBySession[id];
    if (!existing || existing.length === 0 || (prevRunning && !running)) {
      void loadSessionMessagesOnly(id);
    }
    prevRunning = running;
    if (running) {
      const timer = setInterval(() => {
        if ((workspace.messagesBySession[id]?.length ?? 0) === 0) {
          void loadSessionMessagesOnly(id);
        }
      }, 2000);
      onCleanup(() => clearInterval(timer));
    }
  });

  return createMemo(() => {
    const id = childSessionID();
    if (!id) {
      return {
        stepCount: 0,
        currentStepLabel: undefined as string | undefined,
        changedFiles: [],
        diffStats: undefined as { additions: number; deletions: number } | undefined,
      };
    }
    const turns = workspace.messagesBySession[id] ?? [];
    let stepCount = 0;
    for (const turn of turns) {
      for (const part of turn.parts ?? []) {
        if (part.type === "tool" && !isTodoTool(part.tool)) stepCount++;
      }
    }

    const currentStepLabel = resolveSubagentCurrentStepLabel(id);
    const changedFiles = collectCycleChangedFiles(turns, id);
    let additions = 0;
    let deletions = 0;
    for (const d of changedFiles) {
      additions += d.additions ?? 0;
      deletions += d.deletions ?? 0;
    }

    return {
      stepCount,
      currentStepLabel,
      changedFiles,
      diffStats:
        additions > 0 || deletions > 0 ? { additions, deletions } : undefined,
    };
  });
}

/**
 * Inline nested timeline displaying a child subagent's tool steps in normal
 * document flow (indented under the parent `task` ToolCard row, never clipped or covered).
 */
export default function SubagentInlineTimeline(props: {
  childSessionID: string;
  parentRunning: boolean;
}) {
  const childAlive = createMemo(
    () => props.parentRunning || isSessionBusy(props.childSessionID)
  );

  const childToolParts = createMemo<ToolPart[]>(() => {
    const turns = workspace.messagesBySession[props.childSessionID] ?? [];
    const tools: ToolPart[] = [];
    for (const turn of turns) {
      for (const part of turn.parts ?? []) {
        if (part.type === "tool" && !isTodoTool(part.tool)) {
          tools.push(part as ToolPart);
        }
      }
    }
    return tools;
  });

  return (
    <div class="mt-1 ml-3 border-l-2 border-[var(--border-subtle)] pl-3 py-0.5 space-y-1">
      <Show
        when={childToolParts().length > 0}
        fallback={
          <div class="py-1 text-[11.5px] text-[var(--text-muted)]">
            {t("timeline.subagentLiveActivity", { count: 0 })}
          </div>
        }
      >
        <For each={childToolParts()}>
          {(toolPart) => {
            const rawRunning = () =>
              toolPart.state.status === "pending" || toolPart.state.status === "running";
            const running = () => rawRunning() && childAlive();
            const interrupted = () => rawRunning() && !childAlive();
            const file = () => extractTargetFile(toolPart);
            const desc = () =>
              extractDescription(toolPart) ||
              (extractCommand(toolPart) ? `$ ${extractCommand(toolPart)}` : "") ||
              String(toolPart.state.input?.pattern || toolPart.state.input?.url || "");
            const fileDiff = () => {
              const f = file();
              return f && isEditingTool(toolPart.tool)
                ? resolveToolFileDiff(toolPart, true, f)
                : undefined;
            };

            return (
              <div class="flex items-center justify-between gap-2 rounded-lg px-2 py-1 text-[12px] hover:bg-[var(--bg-hover)]/60 transition-colors">
                <div class="flex items-center gap-1.5 min-w-0 flex-1">
                  <Show
                    when={!running()}
                    fallback={
                      <span class="text-[var(--accent-primary)] shrink-0 inline-flex">
                        <Spinner class="w-3 h-3" />
                      </span>
                    }
                  >
                    <Icon
                      name={
                        toolPart.state.status === "error" || interrupted()
                          ? "circle-ban-sign"
                          : kiloToolIconName(toolPart.tool)
                      }
                      size="small"
                      class={
                        toolPart.state.status === "error"
                          ? "text-[var(--danger-text)] shrink-0"
                          : interrupted()
                            ? "text-[var(--warn-text)] shrink-0"
                            : "text-[var(--text-muted)] shrink-0"
                      }
                    />
                  </Show>

                  <span class="font-medium text-[12px] text-[var(--text-secondary)] shrink-0">
                    {formatToolDisplayName(toolPart.tool)}
                  </span>

                  <Show
                    when={file()}
                    fallback={
                      <span class="font-mono text-[11.5px] text-[var(--text-muted)] truncate">
                        {desc()}
                      </span>
                    }
                  >
                    {(f) => (
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          const diff = fileDiff();
                          diff ? openChangedFileInInspector(diff) : openFileInInspector(f());
                        }}
                        class="inline-flex items-center gap-1 font-mono text-[11.5px] text-[var(--text-primary)] hover:text-[var(--accent-primary)] hover:underline truncate cursor-pointer"
                        title={f()}
                      >
                        <FileIcon node={{ path: f(), type: "file" }} class="w-3.5 h-3.5 shrink-0" />
                        <span class="truncate">{getFilename(f())}</span>
                        <Show
                          when={
                            fileDiff() &&
                            ((fileDiff()!.additions ?? 0) > 0 || (fileDiff()!.deletions ?? 0) > 0)
                          }
                        >
                          <DiffChanges
                            changes={{
                              additions: fileDiff()!.additions ?? 0,
                              deletions: fileDiff()!.deletions ?? 0,
                            }}
                          />
                        </Show>
                      </button>
                    )}
                  </Show>
                </div>

                <span
                  class={`font-mono text-[10.5px] shrink-0 ${
                    toolPart.state.status === "completed"
                      ? "text-[var(--text-faint)]"
                      : toolPart.state.status === "error"
                        ? "text-[var(--danger-text)]"
                        : interrupted()
                          ? "text-[var(--warn-text)]"
                          : "text-[var(--accent-primary)]"
                  }`}
                >
                  {interrupted()
                    ? t("timeline.subagentInterruptedBadge")
                    : formatToolStatusLabel(toolPart.state.status)}
                </span>
              </div>
            );
          }}
        </For>
      </Show>
    </div>
  );
}

