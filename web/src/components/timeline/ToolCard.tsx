// web/src/components/timeline/ToolCard.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import {
  openDiffInInspector,
  openFileInInspector,
  revertToHere,
  selectSession,
  workspace,
} from "../../store/workspace";
import { t } from "../../i18n";
import {
  DiffChanges,
  FileIcon,
  Icon,
  Spinner,
  TextShimmer,
  ToolStatusTitle,
} from "../icons";
import SubagentInlineTimeline from "./SubagentInlineTimeline";
import {
  extractCommand,
  extractDescription,
  extractExitCode,
  extractShellOutput,
  extractTargetFile,
  getDirectory,
  getFilename,
  InlineDiagnosticsDisplay,
  isEditingTool,
  isReadTool,
  isShellTool,
  isTaskTool,
  isTodoTool,
  kiloToolIconName,
  processCarriageReturns,
  ToolCardExpandedBody,
  type ToolPart,
} from "./tool-card-helpers";
import {
  resolveLoadedContextFiles,
  resolveToolArgPills,
  resolveToolFileDiagnostics,
  resolveToolFileDiff,
  resolveToolTodoItems,
} from "./tool-card-state";

export { extractTargetFile, kiloToolIconName, processCarriageReturns };

export default function ToolCard(props: { part: ToolPart }) {
  const [expanded, setExpanded] = createSignal(false);
  const [elapsed, setElapsed] = createSignal(0);
  const [copiedCmd, setCopiedCmd] = createSignal(false);
  let outputRef: HTMLPreElement | undefined;
  let rollingShellRef: HTMLDivElement | undefined;

  const status = () => props.part.state.status;
  const isRunning = () => status() === "pending" || status() === "running";
  const isShell = () => isShellTool(props.part.tool);
  const isEdit = () => isEditingTool(props.part.tool);
  const isRead = () => isReadTool(props.part.tool);
  const isTodo = () => isTodoTool(props.part.tool);
  const isTask = () => isTaskTool(props.part.tool);

  const targetFile = createMemo(() => extractTargetFile(props.part));
  const shellCmd = createMemo(() => extractCommand(props.part));
  const description = createMemo(() => extractDescription(props.part));
  const shellOut = createMemo(() => extractShellOutput(props.part.state));
  const exitCode = createMemo(() => extractExitCode(props.part.state));

  // Rolling 5 lines of shell output while running & collapsed (ported from Kilo ShellRollingResults)
  const rollingShellLines = createMemo(() => {
    const out = shellOut().trim();
    if (!out) return [];
    return out.split("\n").slice(-5);
  });

  const loadedFiles = createMemo(() => resolveLoadedContextFiles(props.part));
  const toolArgPills = createMemo(() => resolveToolArgPills(props.part));
  const todoItems = createMemo(() => resolveToolTodoItems(props.part));

  // Subagent task child session ID
  const childSessionID = createMemo<string | undefined>(() => {
    if (props.part.state.status === "pending") return undefined;
    const id = props.part.state.metadata?.sessionId || props.part.state.metadata?.sessionID;
    return typeof id === "string" && id ? id : undefined;
  });

  const fileDiff = createMemo(() =>
    resolveToolFileDiff(props.part, isEdit(), targetFile() || "")
  );
  const fileDiagnostics = createMemo(() =>
    resolveToolFileDiagnostics(props.part, isEdit(), targetFile() || "")
  );

  // Auto-scroll shell output while streaming
  createEffect(() => {
    void shellOut();
    if (isShell() && status() === "running") {
      requestAnimationFrame(() => {
        if (outputRef) outputRef.scrollTop = outputRef.scrollHeight;
        if (rollingShellRef) rollingShellRef.scrollTop = rollingShellRef.scrollHeight;
      });
    }
  });

  // 0.1s live duration ticker while running
  createEffect(() => {
    const s = props.part.state;
    if (s.status === "running") {
      const start = s.time.start;
      setElapsed(Math.max(0, (Date.now() - start) / 1000));
      const timer = setInterval(() => {
        setElapsed(Math.max(0, (Date.now() - start) / 1000));
      }, 100);
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

  const editVerb = () => {
    const t = props.part.tool.toLowerCase();
    if (t.includes("write")) return "Write";
    if (t.includes("patch")) return "Patch";
    return "Edit";
  };

  return (
    <div>
      <div class="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-panel)] transition-all overflow-hidden shadow-[var(--island-shadow)]">
        {/* Compact Kilo-style single-line tool trigger */}
        <div
          class="flex items-center justify-between px-3 py-2 cursor-pointer select-none hover:bg-[var(--bg-hover)] transition-colors gap-2"
          onClick={() => setExpanded(!expanded())}
        >
          <div class="flex items-center gap-2 min-w-0 flex-1">
            {/* Status / Tool Icon (Kilo Spinner when running, Semantic Icon when done) */}
            <Show
              when={!isRunning()}
              fallback={
                <span class="shrink-0 text-[var(--accent-primary)] inline-flex items-center">
                  <Spinner class="w-4 h-4" />
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
                  <span class="text-[12.5px] font-medium text-[var(--text-primary)] truncate">
                    <TextShimmer
                      text={description() || (shellCmd() ? `$ ${shellCmd()}` : "Run command")}
                      active={isRunning()}
                    />
                  </span>
                  <Show when={description() && shellCmd()}>
                    <code class="hidden sm:inline-block truncate max-w-[420px] rounded bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[11px] text-[var(--text-muted)]">
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
                    <span class="text-[12.5px] font-medium text-[var(--text-primary)] shrink-0">
                      <Show
                        when={isTodo()}
                        fallback={
                          <TextShimmer
                            text={isTask() ? "Subagent" : props.part.tool}
                            active={isRunning()}
                          />
                        }
                      >
                        <ToolStatusTitle
                          active={isRunning()}
                          activeText="Updating to-dos"
                          doneText="To-dos"
                        />
                      </Show>
                    </span>
                    <Show when={description()}>
                      <span class="text-[12px] text-[var(--text-muted)] truncate">
                        {description()}
                      </span>
                    </Show>
                    <Show when={todoItems().length > 0}>
                      <span class="rounded-md bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-muted)]">
                        {todoItems().filter((t) => t.status === "completed").length}/
                        {todoItems().length}
                      </span>
                    </Show>
                    <For each={toolArgPills()}>
                      {(pill) => (
                        <span class="hidden sm:inline-block rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-muted)]">
                          {pill}
                        </span>
                      )}
                    </For>
                  </div>
                }
              >
                {(file) => (
                  <div class="flex items-center gap-1.5 min-w-0 flex-1">
                    <span class="text-[12px] font-medium text-[var(--text-secondary)] shrink-0">
                      <TextShimmer
                        text={isEdit() ? editVerb() : isRead() ? "Read" : props.part.tool}
                        active={isRunning()}
                      />
                    </span>
                    <FileIcon node={{ path: file(), type: "file" }} class="w-3.5 h-3.5 shrink-0" />
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        if (isEdit()) {
                          openDiffInInspector();
                        } else {
                          openFileInInspector(file());
                        }
                      }}
                      class="font-mono text-[12px] font-medium text-[var(--text-primary)] hover:text-[var(--accent-primary)] hover:underline truncate cursor-pointer"
                      title={file()}
                    >
                      {getFilename(file())}
                    </button>
                    <Show when={getDirectory(file())}>
                      <span class="hidden md:inline truncate font-mono text-[11px] text-[var(--text-faint)]">
                        {getDirectory(file())}/
                      </span>
                    </Show>
                    <For each={toolArgPills()}>
                      {(pill) => (
                        <span class="hidden sm:inline-block rounded-md border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-1.5 py-0.5 font-mono text-[10.5px] text-[var(--text-muted)]">
                          {pill}
                        </span>
                      )}
                    </For>
                  </div>
                )}
              </Show>
            </Show>
          </div>

          {/* Right side: Subagent link / Diff +/- stats / Exit code / Revert / Duration / Chevron */}
          <div class="flex items-center gap-2 shrink-0">
            <Show when={childSessionID()}>
              {(subId) => (
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    void selectSession(subId());
                  }}
                  class="inline-flex items-center gap-1 rounded-md border border-[var(--accent-border)] bg-[var(--accent-surface)] px-2 py-0.5 text-[11px] font-medium text-[var(--accent-text)] hover:opacity-90 transition-opacity cursor-pointer"
                >
                  <Icon name="task" size="small" />
                  <span>{t("toolCard.inspectSubagent")}</span>
                </button>
              )}
            </Show>

            <Show when={fileDiff()}>
              {(d) => (
                <div class="flex items-center gap-1.5">
                  <DiffChanges changes={{ additions: d().additions, deletions: d().deletions }} />
                  <DiffChanges
                    changes={{ additions: d().additions, deletions: d().deletions }}
                    variant="bars"
                  />
                </div>
              )}
            </Show>

            <Show when={isShell() && exitCode() !== undefined && exitCode() !== 0}>
              <span class="rounded bg-[var(--danger-surface)] px-1.5 py-0.5 font-mono text-[10.5px] font-medium text-[var(--danger-text)]">
                exit {exitCode()}
              </span>
            </Show>

            <Show when={isEdit() && status() === "completed"}>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  void revertToHere(props.part.sessionID, props.part.messageID, props.part.id);
                }}
                title={t("toolCard.revertTooltip")}
                class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-md text-[11px] text-[var(--text-muted)] hover:text-[var(--warn-text)] hover:bg-[var(--warn-surface)] transition-colors cursor-pointer"
              >
                <Icon name="arrow-undo-down" size="small" />
                <span class="hidden sm:inline">{t("toolCard.undo")}</span>
              </button>
            </Show>

            <Show when={elapsed() > 0}>
              <span class="font-mono text-[11px] text-[var(--text-faint)] tabular-nums">
                {elapsed().toFixed(1)}s
              </span>
            </Show>

            <span class="text-[var(--text-faint)] inline-flex items-center">
              <Icon name={expanded() ? "chevron-down" : "chevron-right"} size="small" />
            </span>
          </div>
        </div>

        {/* Kilo TaskToolExpanded: Live child subagent tool calls inline */}
        <Show when={isTask() && childSessionID()}>
          {(subId) => (
            <SubagentInlineTimeline childSessionID={subId()} parentRunning={isRunning()} />
          )}
        </Show>

        {/* Kilo ShellRollingResults: Live 5-line rolling output preview while bash command is running & collapsed */}
        <Show when={isShell() && isRunning() && !expanded() && rollingShellLines().length > 0}>
          <div
            ref={rollingShellRef}
            onClick={() => setExpanded(true)}
            class="border-t border-[var(--border-subtle)] bg-[#0d1117]/95 px-3.5 py-2 font-mono text-[11.5px] leading-[1.55] text-[#c9d1d9] max-h-[104px] overflow-hidden cursor-pointer relative"
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

        {/* Inline Post-Edit LSP Compiler/Type Diagnostics (Kilo DiagnosticsDisplay) */}
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
          />
        </Show>
      </div>

      {/* Kilo `Loaded` context files sub-rows under `read` tool */}
      <Show when={loadedFiles().length > 0}>
        <div class="mt-1 pl-3 space-y-0.5">
          <For each={loadedFiles()}>
            {(filePath) => (
              <button
                type="button"
                onClick={() => openFileInInspector(filePath)}
                class="flex items-center gap-1.5 text-[11.5px] text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors cursor-pointer py-0.5"
              >
                <Icon name="enter" size="small" class="text-[var(--text-faint)]" />
                <span>{t("toolCard.loaded")}</span>
                <FileIcon node={{ path: filePath, type: "file" }} class="w-3.5 h-3.5 shrink-0" />
                <span class="font-mono text-[11px] hover:underline truncate max-w-[360px]">
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
