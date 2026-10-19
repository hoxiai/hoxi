// web/src/components/timeline/ToolCard.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { RotateCcw } from "lucide-solid";
import {
  extractChildSubagentSessionId,
  isSessionBusy,
  isSubagentToolAlive,
  isSubagentToolInterrupted,
   openChangedFileInInspector,
  openFileInInspector,
  resumeSubagentTask,
  revertToHere,
  selectSession,
} from "../../store/workspace";
import { t } from "../../i18n";
import { DiffChanges, FileIcon, Icon, Spinner, TextShimmer, ToolStatusTitle } from "../icons";
import SubagentInlineTimeline, {
  useSubagentTelemetry,
} from "./SubagentInlineTimeline";
import SubagentTaskDetails from "./SubagentTaskDetails";

import {
  ActionIconButton,
  extractCommand, extractDescription, extractExitCode, extractRollingShellLines,
  extractShellOutput, extractTargetFile, formatToolDisplayName, getDirectory, getFilename,
  InlineDiagnosticsDisplay, isEditingTool, isReadTool, isShellTool,
  isTaskTool, isTodoTool, kiloToolIconName, processCarriageReturns,
  ToolCardExpandedBody, type ToolPart,
} from "./tool-card-helpers";
import {
  extractTargetFiles, resolveLoadedContextFiles, resolveToolArgPills,
  resolveToolFileDiagnostics, resolveToolFileDiff, resolveToolFileDiffs,
  resolveToolTodoItems,
} from "./tool-card-state";

// Silence shorter than this is normal model latency, not worth flagging.
const RUNNER_IDLE_HINT_SECONDS = 15;

export { extractTargetFile, kiloToolIconName, processCarriageReturns };

export default function ToolCard(props: { part: ToolPart; defaultExpanded?: boolean }) {
  const [expanded, setExpanded] = createSignal(props.defaultExpanded ?? false);
  const [elapsed, setElapsed] = createSignal(0);
  const [copiedCmd, setCopiedCmd] = createSignal(false);
  let outputRef: HTMLPreElement | undefined;
  let rollingShellRef: HTMLDivElement | undefined;

  const status = () => props.part.state.status;
  const isShell = () => isShellTool(props.part.tool);
  const isEdit = () => isEditingTool(props.part.tool);
  const isRead = () => isReadTool(props.part.tool);
  const isTodo = () => isTodoTool(props.part.tool);
  const isTask = () => isTaskTool(props.part.tool);

  const isRunning = createMemo(() => {
    const s = status();
    if (s !== "pending" && s !== "running") return false;
    if (isTask()) return isSubagentToolAlive(props.part, props.part.sessionID);
    return isSessionBusy(props.part.sessionID);
  });

  const isInterruptedSubagent = createMemo(
    () => isTask() && isSubagentToolInterrupted(props.part, props.part.sessionID)
  );

  const targetFile = createMemo(() => extractTargetFile(props.part));
  const extraTargetFiles = createMemo(() => extractTargetFiles(props.part).slice(1));
  const shellCmd = createMemo(() => (isShell() ? extractCommand(props.part) : ""));
  const description = createMemo(() => extractDescription(props.part));
  const shellOut = createMemo(() =>
    isShell() && expanded() ? extractShellOutput(props.part.state) : ""
  );
  const exitCode = createMemo(() => (isShell() ? extractExitCode(props.part.state) : undefined));

  // Rolling 5 lines of shell output while running & collapsed (ported from Kilo ShellRollingResults)
  const rollingShellLines = createMemo(() =>
    isShell() && isRunning() && !expanded() ? extractRollingShellLines(props.part.state) : []
  );

  const loadedFiles = createMemo(() => (isRead() ? resolveLoadedContextFiles(props.part) : []));
  const toolArgPills = createMemo(() => resolveToolArgPills(props.part));
  const todoItems = createMemo(() => (isTodo() ? resolveToolTodoItems(props.part) : []));
  const childSessionID = createMemo<string | undefined>(() =>
    isTask() ? extractChildSubagentSessionId(props.part) : undefined
  );
  const subagentTelemetry = useSubagentTelemetry(childSessionID, isRunning);
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
    if (!isShell() || !isRunning()) return;
    expanded() ? void shellOut() : void rollingShellLines();
    requestAnimationFrame(() => {
      if (outputRef) outputRef.scrollTop = outputRef.scrollHeight;
      if (rollingShellRef) rollingShellRef.scrollTop = rollingShellRef.scrollHeight;
    });
  });

  // 0.1s live duration ticker while actively running
  createEffect(() => {
    const s = props.part.state;
    if (s.status === "running" && isRunning()) {
      const start = s.time.start;
      setElapsed(Math.max(0, (Date.now() - start) / 1000));
      const timer = setInterval(() => setElapsed(Math.max(0, (Date.now() - start) / 1000)), 100);
      onCleanup(() => clearInterval(timer));
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

  const isSubagentPopoverMode = () => Boolean(isTask() && childSessionID());

  const hasBody = () =>
    (expanded() && !isSubagentPopoverMode()) ||
    Boolean(isShell() && isRunning() && rollingShellLines().length > 0) ||
    fileDiagnostics().length > 0;

  return (
    <div>
      <div class="group/tool relative">
        {/* Clean borderless single-line tool trigger */}
        <div
          class={`flex items-center justify-between px-2 py-1.5 -mx-2 rounded-lg cursor-pointer select-none transition-colors gap-2 ${
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
                  status() === "error"
                    ? "shrink-0 text-[var(--danger-text)] inline-flex items-center"
                    : "shrink-0 text-[var(--text-muted)] inline-flex items-center"
                }
              >
                <Icon
                  name={
                    status() === "error"
                      ? "circle-ban-sign"
                      : kiloToolIconName(props.part.tool)
                  }
                  size="small"
                />
              </span>
            </Show>

            {/* Tool Title & Semantic Target */}
            <Show
              when={!isShell()}
              fallback={
                <div class="flex items-center gap-2 min-w-0 flex-1">
                  <span class="text-[13px] font-medium text-[var(--text-secondary)] truncate">
                    <TextShimmer
                      text={description() || (shellCmd() ? `$ ${shellCmd()}` : t("toolCard.actionShell"))}
                      active={isRunning()}
                    />
                  </span>
                  <Show when={description() && shellCmd()}>
                    <code class="hidden sm:inline-block truncate max-w-[420px] font-mono text-[12px] text-[var(--text-faint)]">
                      $ {shellCmd()}
                    </code>
                  </Show>
                </div>
              }
            >
              <Show
                when={targetFile()}
                fallback={
                  <div class="flex items-center gap-2 min-w-0 flex-1">
                    <span class="text-[13px] font-medium text-[var(--text-secondary)] shrink-0">
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
                    <Show when={description()}>
                      <span class="text-[12.5px] text-[var(--text-muted)] truncate">
                        {description()}
                      </span>
                    </Show>
                    <Show when={isTask() && isRunning() && subagentTelemetry().currentStepLabel}>
                      <span class="inline-block font-mono text-[11px] text-[var(--text-faint)] truncate max-w-[220px]">
                        · {subagentTelemetry().currentStepLabel}
                      </span>
                    </Show>
                    <Show when={todoItems().length > 0}>
                      <span class="font-mono text-[11.5px] text-[var(--text-faint)]">
                        {todoItems().filter((t) => t.status === "completed").length}/
                        {todoItems().length}
                      </span>
                    </Show>
                    <For each={toolArgPills()}>
                      {(pill) => (
                        <span class="hidden sm:inline-block font-mono text-[11.5px] text-[var(--text-faint)] truncate max-w-[240px]">
                          {pill}
                        </span>
                      )}
                    </For>
                  </div>
                }
              >
                {(file) => (
                  <div class="flex items-center gap-1.5 min-w-0 flex-1">
                    <span class="text-[12.5px] font-medium text-[var(--text-secondary)] shrink-0">
                      <TextShimmer
                        text={formatToolDisplayName(props.part.tool, props.part)}
                        active={isRunning()}
                      />
                    </span>
                    <FileIcon node={{ path: file(), type: "file" }} class="w-3.5 h-3.5 shrink-0" />
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        isEdit()
                          ? openChangedFileInInspector(fileDiff() ?? file())
                          : openFileInInspector(file());
                      }}
                      class="font-mono text-[12.5px] font-medium text-[var(--text-primary)] hover:text-[var(--accent-primary)] hover:underline truncate cursor-pointer"
                      title={file()}
                    >
                      {getFilename(file())}
                    </button>
                    <Show when={getDirectory(file())}>
                      <span class="hidden md:inline truncate font-mono text-[11.5px] text-[var(--text-faint)]">
                        {getDirectory(file())}/
                      </span>
                    </Show>
                    <Show when={extraTargetFiles().length > 0}>
                      <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10.5px] font-medium text-[var(--text-muted)] shrink-0">
                        +{extraTargetFiles().length}
                      </span>
                    </Show>
                    <For each={toolArgPills()}>
                      {(pill) => (
                        <span class="hidden sm:inline-block font-mono text-[11.5px] text-[var(--text-faint)] truncate max-w-[200px]">
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
              <span class="rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-muted)]">
                {t("timeline.stepsCompleted", { count: subagentTelemetry().stepCount })}
              </span>
            </Show>

            {/* Runner subagents cannot be resumed through an OpenCode task_id */}
            <Show when={isInterruptedSubagent() && !isRunnerSubagent() && childSessionID()}>
              {(subId) => (
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    const inp = props.part.state.input as Record<string, any> | undefined;
                    void resumeSubagentTask({
                      parentSessionID: props.part.sessionID,
                      childSessionID: subId(),
                      agentType: String(inp?.subagent_type || "general"),
                      description: description() || subId(),
                    });
                  }}
                  title={t("timeline.resumeSubagentTooltip")}
                  class="inline-flex items-center gap-1 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-0.5 text-[11.5px] font-medium text-[var(--accent-primary)] hover:opacity-90 transition-opacity cursor-pointer"
                >
                  <RotateCcw size={11} />
                  <span>{t("timeline.resumeSubagentBtn")}</span>
                </button>
              )}
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

            <Show when={totalDiffStats()}>
              {(stats) => <DiffChanges changes={stats()} />}
            </Show>

            <Show when={isShell() && exitCode() !== undefined && exitCode() !== 0}>
              <span class="rounded bg-[var(--danger-surface)] px-1.5 py-0.5 font-mono text-[11px] font-medium text-[var(--danger-text)]">
                exit {exitCode()}
              </span>
            </Show>

            <Show when={isEdit() && status() === "completed"}>
              <ActionIconButton
                variant="warn"
                label={t("toolCard.undo")}
                onClick={(e) => {
                  e.stopPropagation();
                  void revertToHere(props.part.sessionID, props.part.messageID, props.part.id);
                }}
                class="opacity-0 group-hover/tool:opacity-100"
              >
                <Icon name="arrow-undo-down" size="small" />
              </ActionIconButton>
            </Show>

            <Show when={elapsed() > 0}>
              <span class="font-mono text-[11.5px] text-[var(--text-faint)] tabular-nums">
                {elapsed().toFixed(1)}s
              </span>
            </Show>

            <span class="text-[var(--text-faint)] opacity-0 group-hover/tool:opacity-100 transition-opacity inline-flex items-center">
              <Icon name={expanded() ? "chevron-down" : "chevron-right"} size="small" />
            </span>
          </div>
        </div>

        {/* Inline nested step list for Subagent `task` tool calls */}
        <Show when={expanded() && isTask() && childSessionID()}>
          {(subId) => (
            <>
              <SubagentInlineTimeline
                childSessionID={subId()}
                parentRunning={isRunning()}
              />
              <SubagentTaskDetails part={props.part} running={isRunning()} />
            </>
          )}
        </Show>


        {/* Collapsible / Live Body Container (only bordered when visible) */}
        <Show when={hasBody()}>
          <div class="mt-1 ml-3 overflow-hidden border-l-2 border-[var(--border-subtle)] pl-3">
            {/* Kilo ShellRollingResults: Live 5-line rolling output preview while bash command is running & collapsed */}
            <Show when={isShell() && isRunning() && !expanded() && rollingShellLines().length > 0}>
              <div
                ref={rollingShellRef}
                onClick={() => setExpanded(true)}
                class="bg-[#0d1117]/95 px-3.5 py-2 font-mono text-[12px] leading-[1.55] text-[#c9d1d9] max-h-[104px] overflow-hidden cursor-pointer relative"
                style={{
                  "-webkit-mask-image":
                    "linear-gradient(to bottom, transparent 0%, black 24%, black 100%)",
                  "mask-image": "linear-gradient(to bottom, transparent 0%, black 24%, black 100%)",
                }}
              >
                <For each={rollingShellLines()}>
                  {(line) => <div class="truncate kilo-rolling-row">{line || " "}</div>}
                </For>
              </div>
            </Show>

            {/* Inline Post-Edit LSP Compiler/Type Diagnostics */}
            <Show when={fileDiagnostics().length > 0}>
              <InlineDiagnosticsDisplay diagnostics={fileDiagnostics()} />
            </Show>

            {/* Expanded Body */}
            <Show when={expanded()}>
              <ToolCardExpandedBody
                part={props.part}
                isShell={isShell()}
                isEdit={isEdit()}
                isTodo={isTodo()}
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
            </Show>
          </div>
        </Show>
      </div>

      {/* Kilo `Loaded` context files sub-rows under `read` tool */}
      <Show when={expanded() && loadedFiles().length > 0}>
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
                <span class="font-mono text-[11.5px] hover:underline truncate max-w-[360px]">
                  {filePath}
                </span>
              </button>
            )}
          </For>
        </div>
      </Show>
    </div>
  );
}
