// web/src/components/timeline/ToolCard.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  extractChildSubagentSessionId,
  canRevertSession,
  isSessionBusy,
  isSubagentToolAlive,
  isSubagentToolInterrupted,
  openChangedFileInInspector,
  openFileInInspector,
  resolveSubagentTitle,
  revertToHere,
  selectSession,
  sessionsWithPendingPermission,
  setWorkspace,
  stopSessionToolCommand,
} from "../../store/workspace";
import { t } from "../../i18n";
import { DiffChanges, FileIcon, Icon, Spinner, TextShimmer, ToolStatusTitle } from "../icons";
import SubagentInlineTimeline, {
  useSubagentTelemetry,
} from "./SubagentInlineTimeline";
import SubagentTaskDetails from "./SubagentTaskDetails";

import {
  ActionIconButton,
  extractCommand, extractDescription,
  extractShellOutput, extractTargetFile, formatToolDisplayName, getDirectory, getFilename,
  hasDistinctShellDescription, InlineDiagnosticsDisplay, isComputerTool, isEditingTool, isQuestionTool, isReadTool, isShellTool, isShellAwaitingFirstOutput,
  isTaskTool, isTodoTool, kiloToolIconName, processCarriageReturns,
  ToolCardExpandedBody, type ToolPart,
} from "./tool-card-helpers";
import {
  extractTargetFiles, resolveLoadedContextFiles, resolveReadLineRange, resolveToolArgPills,
  resolveToolFileDiagnostics, resolveToolFileDiff, resolveToolFileDiffs,
  resolveToolTodoItems,
} from "./tool-card-state";
import { formatElapsedSeconds, startAdaptiveElapsedTimer } from "./duration-label";
import { analyzeShellCommand } from "./shell-command-summary";

// Silence shorter than this is normal model latency, not worth flagging.
const RUNNER_IDLE_HINT_SECONDS = 15;

export { extractTargetFile, kiloToolIconName, processCarriageReturns };

// Expansion chosen by the reader survives remounts (session switches unmount the
// transcript), like ThoughtBox and activity groups; otherwise the layout shrinks
// and the restored reading position lands on different content.
const MAX_TOOL_CARD_EXPANSION_ENTRIES = 512;
const toolCardExpansion = new Map<string, boolean>();

export default function ToolCard(props: { part: ToolPart; defaultExpanded?: boolean }) {
  const initiallyExpanded = toolCardExpansion.get(props.part.id) ?? false;
  const [expanded, setExpandedSignal] = createSignal(initiallyExpanded);
  const setExpanded = (next: boolean) => {
    if (toolCardExpansion.has(props.part.id)) {
      toolCardExpansion.delete(props.part.id);
    } else if (toolCardExpansion.size >= MAX_TOOL_CARD_EXPANSION_ENTRIES) {
      const oldest = toolCardExpansion.keys().next().value;
      if (oldest !== undefined) toolCardExpansion.delete(oldest);
    }
    toolCardExpansion.set(props.part.id, next);
    setExpandedSignal(next);
  };
  const [everExpanded, setEverExpanded] = createSignal(initiallyExpanded);
  const [elapsed, setElapsed] = createSignal(0);
  const [copiedCmd, setCopiedCmd] = createSignal(false);
  const [stoppingCmd, setStoppingCmd] = createSignal(false);
  const [stopFailed, setStopFailed] = createSignal(false);
  let outputRef: HTMLPreElement | undefined;

  createEffect(() => {
    if (expanded()) setEverExpanded(true);
  });

  const status = () => props.part.state.status;
  const isShell = () => isShellTool(props.part.tool);
  const isEdit = () => isEditingTool(props.part.tool);
  const isRead = () => isReadTool(props.part.tool);
  const isTodo = () => isTodoTool(props.part.tool);
  const isTask = () => isTaskTool(props.part.tool);
  const isQuestion = () => isQuestionTool(props.part.tool);

  const isRunning = createMemo(() => {
    const s = status();
    if (s !== "pending" && s !== "running") return false;
    if (isTask()) return isSubagentToolAlive(props.part, props.part.sessionID);
    return isSessionBusy(props.part.sessionID);
  });

  const isInterruptedTool = createMemo(() =>
    isTask()
      ? isSubagentToolInterrupted(props.part, props.part.sessionID)
      : (status() === "pending" || status() === "running") && !isRunning()
  );

  const targetFile = createMemo(() => extractTargetFile(props.part));
  const extraTargetFiles = createMemo(() => extractTargetFiles(props.part).slice(1));
  const shellCmd = createMemo(() => (isShell() ? extractCommand(props.part) : ""));
  const description = createMemo(() => extractDescription(props.part));
  const shellIntent = createMemo(() => (isShell() ? analyzeShellCommand(shellCmd()) : undefined));
  const hasCustomShellDesc = createMemo(
    () => isShell() && hasDistinctShellDescription(description(), shellCmd())
  );
  const toolIconName = createMemo(() => {
    const intent = shellIntent();
    if (intent) {
      if (intent.kind === "read" && intent.targetFile) return "glasses";
      if (intent.kind === "search" && intent.searchPattern) return "magnifying-glass-menu";
      if (intent.kind === "list" && !hasCustomShellDesc()) return "bullet-list";
    }
    return kiloToolIconName(props.part.tool);
  });
  const shellOut = createMemo(() =>
    isShell() && everExpanded() ? extractShellOutput(props.part.state, props.part.id) : ""
  );
  const awaitingFirstOutput = createMemo(() =>
    expanded() &&
    isShellAwaitingFirstOutput(props.part, isRunning(), elapsed()) &&
    !sessionsWithPendingPermission().has(props.part.sessionID)
  );

  const loadedFiles = createMemo(() => (isRead() ? resolveLoadedContextFiles(props.part) : []));
  const readLineRange = createMemo(() => (isRead() ? resolveReadLineRange(props.part) : undefined));
  const toolArgPills = createMemo(() => resolveToolArgPills(props.part));
  const todoItems = createMemo(() => (isTodo() ? resolveToolTodoItems(props.part) : []));
  const childSessionID = createMemo<string | undefined>(() =>
    isTask() ? extractChildSubagentSessionId(props.part) : undefined
  );
  const subagentTelemetry = useSubagentTelemetry(childSessionID, isRunning);
  // A task's description often restates the session title; shorten or replace it.
  const headerDescription = createMemo(() => {
    const desc = description();
    if (!isTask() || !desc) return { label: desc, full: desc };
    return resolveSubagentTitle(props.part.sessionID, childSessionID(), desc);
  });
  // Antigravity / Claude Code subagents: child sessions written by the parent's CLI turn.
  const isRunnerSubagent = () => isTask() && Boolean(props.part.state.metadata?.runner);
  // Reuses the 0.1s elapsed ticker as the clock instead of starting another timer.
  const idleSeconds = createMemo(() => {
    const s = props.part.state;
    const last = isRunnerSubagent() ? Number(s.metadata?.lastActivityAt) || 0 : 0;
    if (!last || !isRunning() || s.status !== "running") return 0;
    const now = s.time.start + elapsed() * 1000;
    return Math.max(0, Math.floor((now - last) / 1000));
  });
  const fileDiff = createMemo(() => resolveToolFileDiff(props.part, isEdit(), targetFile() || ""));
  const fileDiffs = createMemo(() =>
    resolveToolFileDiffs(props.part, isEdit(), targetFile() || "")
  );
  const totalDiffStats = createMemo(() => {
    if (isTask()) return subagentTelemetry().diffStats;
    const list = fileDiffs();
    if (!list.length) return undefined;
    return list.reduce(
      (acc, d) => ({
        additions: acc.additions + (d.additions || 0),
        deletions: acc.deletions + (d.deletions || 0),
      }),
      { additions: 0, deletions: 0 }
    );
  });
  const fileDiagnostics = createMemo(() =>
    resolveToolFileDiagnostics(props.part, isEdit(), targetFile() || "")
  );

  // Auto-scroll shell output while streaming
  createEffect(() => {
    if (!isShell() || !isRunning() || !expanded()) return;
    void shellOut();
    requestAnimationFrame(() => {
      if (outputRef) outputRef.scrollTop = outputRef.scrollHeight;
    });
  });

  // Live duration ticker while actively running (100ms under 60s, 1s thereafter)
  createEffect(() => {
    const s = props.part.state;
    if (s.status === "running" && isRunning()) {
      const start = s.time.start;
      const stop = startAdaptiveElapsedTimer(
        () => start,
        (nowMs) => setElapsed(Math.max(0, (nowMs - start) / 1000))
      );
      onCleanup(stop);
    } else if (s.status === "completed" || s.status === "error") {
      setElapsed(Math.max(0, (s.time.end - s.time.start) / 1000));
    } else {
      setElapsed(0);
    }
  });

  const copyCommand = async (e: MouseEvent) => {
    e.stopPropagation();
    const text = shellOut() ? `$ ${shellCmd()}\n${shellOut()}` : shellCmd();
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopiedCmd(true);
      setTimeout(() => setCopiedCmd(false), 1600);
    } catch {
      /* ignore */
    }
  };

  const isStoppedByUser = () =>
    Boolean(
      (props.part.state as { metadata?: Record<string, unknown> }).metadata?.stoppedByUser
    );

  const stopCommand = async (e: MouseEvent) => {
    e.stopPropagation();
    if (stoppingCmd()) return;
    setStoppingCmd(true);
    setStopFailed(false);
    try {
      const stopped = await stopSessionToolCommand(props.part.sessionID, {
        callID: props.part.callID,
        partID: props.part.id,
        command: shellCmd(),
      });
      // The backend refuses when no process matches or the match is ambiguous;
      // say so instead of leaving the button looking inert.
      if (!stopped) setStopFailed(true);
    } finally {
      setStoppingCmd(false);
    }
  };

  const isSubagentPopoverMode = () => Boolean(isTask() && childSessionID());

  const hasBody = () => expanded() && !isSubagentPopoverMode();

  return (
    <div>
      <div class="group/tool relative">
        {/* Clean borderless single-line tool trigger */}
        <div
          class={`flex items-center justify-between px-2 py-1 -mx-2 rounded-lg cursor-pointer select-none transition-colors gap-2 ${
            expanded()
              ? "bg-[var(--bg-hover)]/70"
              : "hover:bg-[var(--bg-hover)]/60"
          }`}
          onClick={() => setExpanded(!expanded())}
        >
          <div class="flex items-center gap-2 min-w-0 flex-1">
            {/* Status / Tool Icon */}
            <Show
              when={!isRunning()}
              fallback={
                <span class="shrink-0 text-[var(--accent-primary)] inline-flex items-center">
                  <Spinner class="w-3.5 h-3.5" />
                </span>
              }
            >
              <span
                class={
                  status() === "error" || isInterruptedTool()
                    ? "shrink-0 text-[var(--warn-text)]/80 inline-flex items-center"
                    : "shrink-0 text-[var(--text-faint)] group-hover/tool:text-[var(--text-secondary)] transition-colors inline-flex items-center"
                }
              >
                <Icon
                  name={
                    status() === "error" || isInterruptedTool()
                      ? "warning"
                      : toolIconName()
                  }
                  size="small"
                />
              </span>
            </Show>

            {/* Tool Title & Semantic Target */}
            <Show
              when={!isShell()}
              fallback={
                <Show
                  when={shellIntent()?.kind === "read" && shellIntent()?.targetFile}
                  fallback={
                    <Show
                      when={shellIntent()?.kind === "search" && shellIntent()?.searchPattern}
                      fallback={
                        <div class="flex items-center gap-2 min-w-0 flex-1">
                          <span class="text-[13px] text-[var(--text-muted)] group-hover/tool:text-[var(--text-secondary)] transition-colors truncate">
                            <TextShimmer
                              text={
                                hasCustomShellDesc()
                                  ? description()
                                  : shellIntent()?.summary ||
                                    description() ||
                                    (shellCmd() ? `$ ${shellCmd()}` : t("toolCard.actionShell"))
                              }
                              active={isRunning()}
                            />
                          </span>
                        </div>
                      }
                    >
                      {(pattern) => (
                        <div class="flex items-center gap-2 min-w-0 flex-1">
                          <span class="text-[13px] text-[var(--text-muted)] group-hover/tool:text-[var(--text-secondary)] transition-colors shrink-0">
                            <TextShimmer text={t("toolCard.actionSearch")} active={isRunning()} />
                          </span>
                          <span class="font-mono text-[12px] text-[var(--text-muted)] truncate">
                            "{pattern()}"
                          </span>
                          <Show when={shellIntent()?.searchPath}>
                            {(p) => (
                              <span class="hidden sm:inline-block font-mono text-[12px] text-[var(--text-faint)] truncate max-w-[200px]">
                                {p()}
                              </span>
                            )}
                          </Show>
                        </div>
                      )}
                    </Show>
                  }
                >
                  {(file) => (
                    <div class="flex items-center gap-1.5 min-w-0 flex-1">
                      <span class="text-[13px] text-[var(--text-muted)] group-hover/tool:text-[var(--text-secondary)] transition-colors shrink-0">
                        <TextShimmer text={t("toolCard.actionRead")} active={isRunning()} />
                      </span>
                      <FileIcon node={{ path: file(), type: "file" }} class="w-3.5 h-3.5 shrink-0 opacity-75 group-hover/tool:opacity-100 transition-opacity" />
                      <span class="inline-flex items-baseline min-w-0">
                        <button
                          type="button"
                          onClick={(e) => {
                            e.stopPropagation();
                            openFileInInspector(file());
                          }}
                          class={`font-mono text-[13px] hover:text-[var(--accent-primary)] hover:underline truncate cursor-pointer transition-colors ${
                            isRunning()
                              ? "text-[var(--text-secondary)]"
                              : "text-[var(--text-muted)] group-hover/tool:text-[var(--text-secondary)]"
                          }`}
                          title={file()}
                        >
                          {getFilename(file())}
                        </button>
                        <Show when={shellIntent()?.lineRange}>
                          {(range) => (
                            <span class="font-mono text-[12px] text-[var(--text-faint)] shrink-0">
                              :{range()}
                            </span>
                          )}
                        </Show>
                      </span>
                    </div>
                  )}
                </Show>
              }
            >
              <Show
                when={targetFile()}
                fallback={
                  <div class="flex items-center gap-2 min-w-0 flex-1">
                    <span class="text-[13px] text-[var(--text-muted)] group-hover/tool:text-[var(--text-secondary)] transition-colors shrink-0">
                      <Show
                        when={isTodo()}
                        fallback={
                          <TextShimmer
                            text={formatToolDisplayName(props.part.tool, props.part)}
                            active={isRunning()}
                          />
                        }
                      >
                        <ToolStatusTitle
                          active={isRunning()}
                          activeText={t("toolCard.updatingTodos")}
                          doneText={t("toolCard.actionTodo")}
                        />
                      </Show>
                    </span>
                    <Show when={headerDescription().label}>
                      <span
                        class="text-[13px] text-[var(--text-muted)] truncate"
                        title={headerDescription().full}
                      >
                        {headerDescription().label}
                      </span>
                    </Show>
                    <Show when={isTask() && isRunning() && subagentTelemetry().currentStepLabel}>
                      <span class="inline-block font-mono text-[11px] text-[var(--text-faint)] truncate max-w-[220px]">
                        · {subagentTelemetry().currentStepLabel}
                      </span>
                    </Show>
                    <Show when={todoItems().length > 0}>
                      <span class="font-mono text-[12px] text-[var(--text-faint)]">
                        {todoItems().filter((t) => t.status === "completed").length}/
                        {todoItems().length}
                      </span>
                    </Show>
                    <For each={toolArgPills()}>
                      {(pill) => (
                        <span class="hidden sm:inline-block font-mono text-[12px] text-[var(--text-faint)] truncate max-w-[240px]">
                          {pill}
                        </span>
                      )}
                    </For>
                  </div>
                }
              >
                {(file) => (
                  <div class="flex items-center gap-1.5 min-w-0 flex-1">
                    <span class="text-[13px] text-[var(--text-muted)] group-hover/tool:text-[var(--text-secondary)] transition-colors shrink-0">
                      <TextShimmer
                        text={formatToolDisplayName(props.part.tool, props.part)}
                        active={isRunning()}
                      />
                    </span>
                    <FileIcon node={{ path: file(), type: "file" }} class="w-3.5 h-3.5 shrink-0 opacity-75 group-hover/tool:opacity-100 transition-opacity" />
                    <span class="inline-flex items-baseline min-w-0">
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          isEdit()
                            ? openChangedFileInInspector(fileDiff() ?? file())
                            : openFileInInspector(file());
                        }}
                        class={`font-mono text-[13px] hover:text-[var(--accent-primary)] hover:underline truncate cursor-pointer transition-colors ${
                          isRunning()
                            ? "text-[var(--text-secondary)]"
                            : "text-[var(--text-muted)] group-hover/tool:text-[var(--text-secondary)]"
                        }`}
                        title={file()}
                      >
                        {getFilename(file())}
                      </button>
                      <Show when={readLineRange()}>
                        {(range) => (
                          <span class="font-mono text-[12px] text-[var(--text-faint)] shrink-0">
                            :{range()}
                          </span>
                        )}
                      </Show>
                    </span>
                    <Show when={!isRead() && getDirectory(file())}>
                      <span class="hidden md:inline truncate font-mono text-[12px] text-[var(--text-faint)]">
                        {getDirectory(file())}/
                      </span>
                    </Show>
                    <Show when={extraTargetFiles().length > 0}>
                      <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] font-medium text-[var(--text-muted)] shrink-0">
                        +{extraTargetFiles().length}
                      </span>
                    </Show>
                    <For each={toolArgPills()}>
                      {(pill) => (
                        <span class="hidden sm:inline-block font-mono text-[12px] text-[var(--text-faint)] truncate max-w-[200px]">
                          {pill}
                        </span>
                      )}
                    </For>
                  </div>
                )}
              </Show>
            </Show>
          </div>

          {/* Right side: Subagent Resume / link / Diff +/- stats / Exit code / Revert / Duration / Chevron */}
          <div class="flex items-center gap-2 shrink-0">
            <Show when={idleSeconds() >= RUNNER_IDLE_HINT_SECONDS}>
              <span class="hidden sm:inline font-mono text-[11px] text-[var(--text-faint)]">
                {t("toolCard.lastActivity", { seconds: idleSeconds() })}
              </span>
            </Show>

            <Show when={isTask() && subagentTelemetry().stepCount > 0}>
              <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-muted)]">
                {t("timeline.stepsCompleted", { count: subagentTelemetry().stepCount })}
              </span>
            </Show>

            <Show when={childSessionID()}>
              {(subId) => (
                <ActionIconButton
                  variant="accent"
                  label={t("timeline.openSubagent")}
                  onClick={(e) => {
                    e.stopPropagation();
                    void selectSession(subId());
                  }}
                >
                  <Icon name="task" size="small" />
                </ActionIconButton>
              )}
            </Show>

            <Show when={isComputerTool(props.part.tool)}>
              <ActionIconButton
                variant="accent"
                label={t("computer.open")}
                onClick={(e) => {
                  e.stopPropagation();
                  setWorkspace({ inspectorOpen: true, inspectorTab: "computer" });
                }}
              >
                <Icon name="window-cursor" size="small" />
              </ActionIconButton>
            </Show>

            <Show when={totalDiffStats()}>
              {(stats) => <DiffChanges changes={stats()} />}
            </Show>

            <Show when={isEdit() && status() === "completed"}>
              <ActionIconButton
                variant="warn"
                disabled={!canRevertSession(props.part.sessionID)}
                label={isSessionBusy(props.part.sessionID) ? t("timeline.revertWhileBusy") : t("toolCard.undo")}
                onClick={(e) => {
                  e.stopPropagation();
                  void revertToHere(props.part.sessionID, props.part.messageID, props.part.id);
                }}
                class="opacity-0 group-hover/tool:opacity-100"
              >
                <Icon name="arrow-undo-down" size="small" />
              </ActionIconButton>
            </Show>

            <Show when={isShell() && isRunning()}>
              <button
                type="button"
                disabled={stoppingCmd()}
                onClick={(e) => void stopCommand(e)}
                title={t("toolCard.stopCommandTooltip")}
                class="-my-1 inline-flex items-center gap-1 rounded-md border border-[var(--danger-border)] bg-[var(--danger-surface)] px-2 py-0.5 text-[11px] font-medium text-[var(--danger-text)] hover:opacity-90 disabled:opacity-60 transition-opacity cursor-pointer"
              >
                <span class="inline-block w-2 h-2 rounded-[1.5px] bg-current shrink-0" />
                <span class={stoppingCmd() ? "" : "hidden group-hover/tool:inline"}>
                  {stoppingCmd() ? t("toolCard.stoppingCommand") : t("toolCard.stopCommand")}
                </span>
              </button>
            </Show>

            <Show when={isShell() && !isRunning() && isStoppedByUser()}>
              <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-muted)]">
                {t("toolCard.stoppedByUser")}
              </span>
            </Show>

            <Show when={isShell() && isRunning() && stopFailed()}>
              <span
                class="rounded bg-[var(--danger-surface)] px-1.5 py-0.5 text-[11px] text-[var(--danger-text)]"
                title={t("toolCard.stopCommandFailedTooltip")}
              >
                {t("toolCard.stopCommandFailed")}
              </span>
            </Show>

            <Show when={elapsed() >= 0.05}>
              <span
                class={`font-mono text-[12px] text-[var(--text-faint)] tabular-nums transition-opacity ${
                  isRunning() ? "" : "opacity-0 group-hover/tool:opacity-100"
                }`}
              >
                {formatElapsedSeconds(elapsed())}
              </span>
            </Show>

            <span
              class="hoxi-chevron text-[var(--text-faint)] opacity-0 group-hover/tool:opacity-100 transition-opacity"
              data-open={expanded() ? "true" : "false"}
            >
              <Icon name="chevron-right" size="small" />
            </span>
          </div>
        </div>

        <Show when={awaitingFirstOutput()}>
          <div class="ml-3 mt-1 border-l-2 border-[var(--border-subtle)] py-1 pl-3 text-[12px] leading-relaxed text-[var(--text-muted)]" role="status">
            {t("toolCard.outputPendingHint")}
          </div>
        </Show>

        {/* Inline nested step list for Subagent `task` tool calls */}
        <Show when={everExpanded() && isTask() && childSessionID()}>
          {(subId) => (
            <div
              class="hoxi-drawer-grid"
              data-open={expanded() ? "true" : "false"}
              aria-hidden={!expanded()}
            >
              <div class="min-h-0 overflow-hidden">
                <SubagentInlineTimeline
                  childSessionID={subId()}
                  parentRunning={isRunning()}
                />
                <SubagentTaskDetails part={props.part} running={isRunning()} />
              </div>
            </div>
          )}
        </Show>

        {/* Collapsible Body Container (strictly closed by default) */}
        <Show when={everExpanded() && !isSubagentPopoverMode()}>
          <div
            class="hoxi-drawer-grid"
            data-open={hasBody() ? "true" : "false"}
            aria-hidden={!hasBody()}
          >
            <div class="min-h-0 overflow-hidden">
              <div class="mt-1 ml-3 overflow-hidden border-l-2 border-[var(--border-subtle)] pl-3">
                {/* Inline Post-Edit LSP Compiler/Type Diagnostics */}
                <Show when={fileDiagnostics().length > 0}>
                  <InlineDiagnosticsDisplay diagnostics={fileDiagnostics()} />
                </Show>

                <ToolCardExpandedBody
                  part={props.part}
                  isShell={isShell()}
                  isRunning={isRunning()}
                  stoppingCmd={stoppingCmd()}
                  onStopCommand={(e) => void stopCommand(e)}
                  isEdit={isEdit()}
                  isTodo={isTodo()}
                  isQuestion={isQuestion()}
                  shellCmd={shellCmd()}
                  shellOut={shellOut()}
                  copiedCmd={copiedCmd()}
                  onCopyCommand={(e) => void copyCommand(e)}
                  setOutputRef={(el) => {
                    outputRef = el;
                  }}
                  todoItems={todoItems()}
                  fileDiff={fileDiff()}
                  fileDiffs={fileDiffs()}
                />
              </div>
            </div>
          </div>
        </Show>
      </div>

      {/* Kilo `Loaded` context files sub-rows under `read` tool */}
      <Show when={everExpanded() && loadedFiles().length > 0}>
        <div
          class="hoxi-drawer-grid"
          data-open={expanded() ? "true" : "false"}
          aria-hidden={!expanded()}
        >
          <div class="min-h-0 overflow-hidden">
            <div class="mt-1 pl-3 space-y-0.5">
              <For each={loadedFiles()}>
                {(filePath) => (
                  <button
                    type="button"
                    onClick={() => openFileInInspector(filePath)}
                    class="flex items-center gap-1.5 text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors cursor-pointer py-0.5"
                  >
                    <Icon name="enter" size="small" class="text-[var(--text-faint)]" />
                    <span>{t("toolCard.loaded")}</span>
                    <FileIcon node={{ path: filePath, type: "file" }} class="w-3.5 h-3.5 shrink-0" />
                    <span class="font-mono text-[12px] hover:underline truncate max-w-[360px]">
                      {filePath}
                    </span>
                  </button>
                )}
              </For>
            </div>
          </div>
        </div>
      </Show>
    </div>
  );
}
