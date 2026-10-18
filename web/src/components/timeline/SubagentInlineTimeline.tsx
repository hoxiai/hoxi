// web/src/components/timeline/SubagentInlineTimeline.tsx
import { createEffect, createMemo, For, onCleanup, onMount, Show } from "solid-js";
import { Bot, ExternalLink, X } from "lucide-solid";
import {
  isSessionBusy,
  loadSessionMessagesOnly,
   openChangedFileInInspector,
  openFileInInspector,
  selectSession,
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
  kiloToolIconName,
  type ToolPart,
} from "./tool-card-helpers";
import { collectCycleChangedFiles, resolveToolFileDiff } from "./tool-card-state";

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
        recentTools: [] as ToolPart[],
        changedFiles: [],
        diffStats: undefined as { additions: number; deletions: number } | undefined,
      };
    }
    const turns = workspace.messagesBySession[id] ?? [];
    const tools: ToolPart[] = [];
    for (const turn of turns) {
      for (const part of turn.parts ?? []) {
        if (part.type === "tool") tools.push(part as ToolPart);
      }
    }
    const activeTool =
      [...tools]
        .reverse()
        .find((tp) => tp.state.status === "running" || tp.state.status === "pending") ??
      tools[tools.length - 1];

    let currentStepLabel: string | undefined;
    if (activeTool) {
      const file = extractTargetFile(activeTool);
      const cmd = extractCommand(activeTool);
      const desc = extractDescription(activeTool);
      const detail = file ? getFilename(file) : cmd ? `$ ${cmd}` : desc || "";
      const toolLabel = formatToolDisplayName(activeTool.tool);
      currentStepLabel = detail ? `${toolLabel} ${detail}` : toolLabel;
    }

    const changedFiles = collectCycleChangedFiles(turns, id);
    let additions = 0;
    let deletions = 0;
    for (const d of changedFiles) {
      additions += d.additions ?? 0;
      deletions += d.deletions ?? 0;
    }

    return {
      stepCount: tools.length,
      currentStepLabel,
      recentTools: tools.slice(-3),
      changedFiles,
      diffStats:
        additions > 0 || deletions > 0 ? { additions, deletions } : undefined,
    };
  });
}

export function SubagentRollingPreview(props: {
  tools: ToolPart[];
  onExpand: () => void;
}) {
  return (
    <Show when={props.tools.length > 0}>
      <div
        onClick={(e) => {
          e.stopPropagation();
          props.onExpand();
        }}
        class="mt-1 ml-3.5 pl-2.5 border-l-2 border-[var(--accent-border)]/50 space-y-1 py-0.5 cursor-pointer select-none"
      >
        <For each={props.tools}>
          {(tp) => {
            const running = () =>
              tp.state.status === "running" || tp.state.status === "pending";
            const file = () => extractTargetFile(tp);
            const cmd = () => extractCommand(tp);
            const desc = () => extractDescription(tp);
            const detail = () =>
              file() ? getFilename(file()!) : cmd() ? `$ ${cmd()}` : desc() || "";
            return (
              <div class="flex items-center gap-1.5 text-[11.5px] text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors">
                <Show
                  when={running()}
                  fallback={
                    <span class="text-[var(--ok-text)]/80 shrink-0 inline-flex items-center">
                      <Icon name="check" size="small" />
                    </span>
                  }
                >
                  <span class="text-[var(--accent-primary)] shrink-0 inline-flex items-center">
                    <Spinner class="w-3 h-3" />
                  </span>
                </Show>
                <span class="font-medium text-[var(--text-secondary)] shrink-0">
                  {formatToolDisplayName(tp.tool)}
                </span>
                <Show when={detail()}>
                  <span class="font-mono text-[11px] text-[var(--text-faint)] truncate max-w-[360px]">
                    {detail()}
                  </span>
                </Show>
              </div>
            );
          }}
        </For>
      </div>
    </Show>
  );
}

/**
 * Floating Popover displaying a child subagent's tool steps and modified files
 * when the user clicks a `task` ToolCard in the timeline.
 */
export default function SubagentInlineTimeline(props: {
  childSessionID: string;
  parentRunning: boolean;
  description?: string;
  agentType?: string;
  onClose: () => void;
}) {
  let popoverRef: HTMLDivElement | undefined;

  const childAlive = createMemo(
    () => props.parentRunning || isSessionBusy(props.childSessionID)
  );

  const telemetry = useSubagentTelemetry(
    () => props.childSessionID,
    () => props.parentRunning
  );

  const childToolParts = createMemo<ToolPart[]>(() => {
    const turns = workspace.messagesBySession[props.childSessionID] ?? [];
    const tools: ToolPart[] = [];
    for (const turn of turns) {
      for (const part of turn.parts ?? []) {
        if (part.type === "tool") {
          tools.push(part as ToolPart);
        }
      }
    }
    return tools.slice(-20);
  });

  onMount(() => {
    const onPointerDown = (e: PointerEvent) => {
      if (popoverRef && !popoverRef.contains(e.target as Node)) {
        props.onClose();
      }
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") props.onClose();
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown);
    onCleanup(() => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown);
    });
  });

  return (
    <div
      ref={popoverRef}
      onClick={(e) => e.stopPropagation()}
      class="absolute left-0 top-[calc(100%+6px)] z-40 w-[min(540px,calc(100vw-48px))] rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)]/98 backdrop-blur-xl shadow-2xl overflow-hidden animate-in fade-in zoom-in-95 duration-150"
    >
      {/* Popover Header */}
      <div class="flex items-center justify-between gap-2 border-b border-[var(--border-subtle)] bg-[var(--bg-inset)]/70 px-3 py-2">
        <div class="flex items-center gap-2 min-w-0 flex-1">
          <Show
            when={childAlive()}
            fallback={<Bot size={13} class="text-[var(--accent-primary)] shrink-0" />}
          >
            <span class="text-[var(--accent-primary)] shrink-0 inline-flex items-center">
              <Spinner class="w-3.5 h-3.5" />
            </span>
          </Show>
          <Show when={props.agentType}>
            <span class="rounded bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--text-muted)] shrink-0">
              {props.agentType}
            </span>
          </Show>
          <span class="text-[12px] font-medium text-[var(--text-primary)] truncate">
            {props.description || t("timeline.subagentLiveActivity", { count: childToolParts().length })}
          </span>
          <Show when={telemetry().diffStats}>
            {(stats) => <DiffChanges changes={stats()} />}
          </Show>
        </div>

        <div class="flex items-center gap-1.5 shrink-0">
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              props.onClose();
              void selectSession(props.childSessionID);
            }}
            class="inline-flex items-center gap-1 rounded-md bg-[var(--accent-surface)] px-2 py-0.5 text-[11px] font-medium text-[var(--accent-primary)] hover:opacity-90 transition-opacity cursor-pointer"
          >
            <span>{t("timeline.openChildSession")}</span>
            <ExternalLink size={10} />
          </button>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              props.onClose();
            }}
            class="p-0.5 rounded text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={t("common.close")}
          >
            <X size={13} />
          </button>
        </div>
      </div>

      {/* Modified files strip inside popover */}
      <Show when={telemetry().changedFiles.length > 0}>
        <div class="flex items-center gap-1.5 flex-wrap border-b border-[var(--border-subtle)] bg-[var(--bg-panel)]/50 px-3 py-1.5">
          <For each={telemetry().changedFiles}>
            {(diff) => (
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  openChangedFileInInspector(diff);
                }}
                title={diff.file}
                class="inline-flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-0.5 font-mono text-[11px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] cursor-pointer"
              >
                <FileIcon node={{ path: diff.file, type: "file" }} class="w-3 h-3 shrink-0" />
                <span class="truncate max-w-[160px]">{getFilename(diff.file)}</span>
                <Show when={diff.additions > 0 || diff.deletions > 0}>
                  <DiffChanges changes={{ additions: diff.additions, deletions: diff.deletions }} />
                </Show>
              </button>
            )}
          </For>
        </div>
      </Show>

      {/* Step list */}
      <div class="px-3 py-2 space-y-1 max-h-56 overflow-y-auto">
        <Show
          when={childToolParts().length > 0}
          fallback={
            <div class="py-3 text-center text-[11.5px] text-[var(--text-muted)]">
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
                extractCommand(toolPart) ||
                String(toolPart.state.input?.pattern || toolPart.state.input?.url || "");
              const fileDiff = () => {
                const f = file();
                return f && isEditingTool(toolPart.tool)
                  ? resolveToolFileDiff(toolPart, true, f)
                  : undefined;
              };

              return (
                <div class="flex items-center justify-between gap-2 rounded-md bg-[var(--bg-panel)]/80 px-2 py-1 text-[11.5px]">
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

                    <span class="font-mono text-[11px] text-[var(--text-secondary)] shrink-0">
                      {formatToolDisplayName(toolPart.tool)}
                    </span>

                    <Show
                      when={file()}
                      fallback={
                        <span class="text-[11px] text-[var(--text-muted)] truncate">
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
                          class="inline-flex items-center gap-1 font-mono text-[11px] text-[var(--text-primary)] hover:text-[var(--accent-primary)] truncate cursor-pointer"
                        >
                          <FileIcon node={{ path: f(), type: "file" }} class="w-3 h-3 shrink-0" />
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
                    class={`font-mono text-[10px] shrink-0 ${
                      toolPart.state.status === "completed"
                        ? "text-[var(--ok-text)]"
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
    </div>
  );
}
