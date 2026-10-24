// web/src/components/timeline/tool-card-helpers.tsx
import { createMemo, For, type JSX, Show } from "solid-js";
import type { MessagePart, SnapshotFileDiff, ToolState } from "../../services/sse-types";
import { getFullToolOutput, openDiffInInspector, setWorkspace, workspace } from "../../store/workspace";
import { findQuestionReply, questionSummaryText } from "../../store/question-answers";
import { Icon, type IconName } from "../icons";
import FileDiffBlock from "../inspector/LazyFileDiffBlock";
import QuestionToolBody from "./question-card";
import { t } from "../../i18n";

export type ToolPart = Extract<MessagePart, { type: "tool" }>;

export function ActionIconButton(props: {
  label: string;
  onClick: (e: MouseEvent) => void;
  disabled?: boolean;
  variant?: "default" | "accent" | "warn" | "danger" | "dark";
  tooltipSide?: "top" | "bottom" | "left";
  class?: string;
  children: JSX.Element;
}) {
  const variantClass = () => {
    switch (props.variant) {
      case "accent":
        return "text-[var(--text-muted)] hover:text-[var(--accent-primary)] hover:bg-[var(--accent-surface)]";
      case "warn":
        return "text-[var(--text-muted)] hover:text-[var(--warn-text)] hover:bg-[var(--warn-surface)]";
      case "danger":
        return "text-[var(--text-muted)] hover:text-[var(--danger-text)] hover:bg-[var(--danger-surface)]";
      case "dark":
        return "text-[#8b949e] hover:bg-white/10 hover:text-white";
      default:
        return "text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]";
    }
  };

  const tooltipPositionClass = () => {
    switch (props.tooltipSide) {
      case "bottom":
        return "top-[calc(100%+5px)] right-0";
      case "left":
        return "right-[calc(100%+6px)] top-1/2 -translate-y-1/2";
      default:
        return "bottom-[calc(100%+5px)] left-1/2 -translate-x-1/2";
    }
  };

  return (
    <button
      type="button"
      disabled={props.disabled}
      onClick={(e) => props.onClick(e)}
      aria-label={props.label}
      class={`group/icon-btn relative inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed ${variantClass()} ${props.class ?? ""}`}
    >
      {props.children}
      <span
        role="tooltip"
        class={`pointer-events-none absolute z-30 whitespace-nowrap rounded-md border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-2 py-0.5 font-sans text-[11px] font-medium leading-tight text-[var(--text-primary)] shadow-md opacity-0 group-hover/icon-btn:opacity-100 group-focus-visible/icon-btn:opacity-100 transition-opacity duration-100 ${tooltipPositionClass()}`}
      >
        {props.label}
      </span>
    </button>
  );
}

// Strip ANSI escape sequences for clean shell rolling output (ported from kilocode)
const ANSI_REGEX =
  // eslint-disable-next-line no-control-regex
  /[\u001b\u009b][[()#;?]*(?:[0-9]{1,4}(?:;[0-9]{0,4})*)?[0-9A-ORZcf-nqry=><]/g;

/**
 * Ported from @kilocode/kilo-ui/src/components/shell-rolling-results.tsx
 * Resolves carriage returns (\r) like a real terminal: within each line (\n),
 * only text after the last \r is kept so CLI progress bars overwrite in place.
 */
export function processCarriageReturns(input: string): string {
  return (input || "")
    .replace(ANSI_REGEX, "")
    .replace(/\r\n/g, "\n")
    .split("\n")
    .map((line) => {
      const idx = line.lastIndexOf("\r");
      return idx === -1 ? line : line.slice(idx + 1);
    })
    .join("\n");
}

export function isShellTool(tool: string): boolean {
  const t = tool.toLowerCase();
  return t === "bash" || t === "shell" || t.includes("bash") || t.includes("shell");
}

export function isEditingTool(tool: string): boolean {
  const t = tool.toLowerCase();
  if (isTodoTool(t) || isReadTool(t) || isSearchOrListTool(t)) return false;
  return (
    t.includes("edit") ||
    t.includes("write") ||
    t.includes("patch") ||
    t.includes("replace") ||
    t === "create_file"
  );
}

export function isReadTool(tool: string): boolean {
  const t = tool.toLowerCase();
  return t === "read" || t.endsWith("_read");
}

export function isSearchOrListTool(tool: string): boolean {
  const t = tool.toLowerCase();
  return t === "grep" || t === "glob" || t === "list" || t === "ls";
}

export function isTodoTool(tool: string): boolean {
  const t = tool.toLowerCase();
  return t === "todowrite" || t === "todoread";
}

export function isTaskTool(tool: string): boolean {
  const t = tool.toLowerCase();
  return t === "task" || t === "agent" || t.includes("subagent");
}

export function isQuestionTool(tool: string): boolean {
  return tool.toLowerCase() === "question";
}

export function kiloToolIconName(tool: string): IconName {
  const t = tool.toLowerCase();
  if (isShellTool(t)) return "console";
  if (isEditingTool(t)) return "code-lines";
  if (isReadTool(t)) return "glasses";
  if (t === "list" || t === "ls") return "bullet-list";
  if (t.includes("grep") || t.includes("glob") || t.includes("find") || t.includes("search"))
    return "magnifying-glass-menu";
  if (t.includes("webfetch") || t.includes("web") || t.includes("fetch")) return "window-cursor";
  if (isTodoTool(t)) return "checklist";
  if (isTaskTool(t)) return "task";
  if (t === "skill") return "brain";
  if (t === "question" || t === "agent_report") return "bubble-5";
  if (t === "agent_wait" || t === "agent_status") return "task";
  return "mcp";
}

export function formatToolDisplayName(tool: string, part?: ToolPart): string {
  if (!tool) return "";
  const lower = tool.toLowerCase();
  if (lower === "agent_report") return t("toolCard.actionSystemMessage");
  if (lower === "agent_wait") {
    return part?.state.metadata?.waitingFor === "subagents"
      ? t("toolCard.actionWaitSubagents")
      : t("toolCard.actionWait");
  }
  if (lower === "agent_status") return t("toolCard.actionCheckSubagents");
  if (isReadTool(lower)) return t("toolCard.actionRead");
  if ((lower.includes("write") || lower === "create_file") && !isTodoTool(lower)) return t("toolCard.actionWrite");
  if (lower.includes("patch")) return t("toolCard.actionPatch");
  if (lower.includes("edit") || lower.includes("replace")) return t("toolCard.actionEdit");
  if (lower === "grep" || lower.includes("search")) return t("toolCard.actionSearch");
  if (lower === "glob" || lower === "find") return t("toolCard.actionFind");
  if (lower === "list" || lower === "ls") return t("toolCard.actionList");
  if (isShellTool(lower)) return t("toolCard.actionShell");
  if (isTaskTool(lower)) return t("toolCard.actionSubagent");
  if (isTodoTool(lower)) return t("toolCard.actionTodo");
  if (lower.includes("webfetch") || lower.includes("fetch")) return t("toolCard.actionWebFetch");
  if (lower === "skill") return t("toolCard.actionSkill");
  if (lower === "question") return t("toolCard.actionQuestion");
  return tool;
}

export function formatToolStatusLabel(status: string): string {
  if (status === "pending") return t("toolCard.statePending");
  if (status === "running") return t("toolCard.stateRunning");
  if (status === "completed") return t("toolCard.stateCompleted");
  if (status === "error") return t("toolCard.stateError");
  return status;
}

export function getFilename(p: string): string {
  if (!p) return "";
  const clean = p.replace(/[/\\]+$/, "");
  const parts = clean.split(/[/\\]/);
  return parts[parts.length - 1] ?? p;
}

export function getDirectory(p: string): string {
  if (!p) return "";
  const clean = p.replace(/[/\\]+$/, "");
  const idx = Math.max(clean.lastIndexOf("/"), clean.lastIndexOf("\\"));
  if (idx <= 0) return "";
  const dir = clean.slice(0, idx);
  const root = workspace.rootDir.replace(/\/+$/, "");
  if (root && (dir === root || dir.startsWith(`${root}/`))) {
    return dir.slice(root.length).replace(/^\/+/, "") || ".";
  }
  return dir;
}

function extractRawField(raw: string, key: string): string | undefined {
  const m = raw.match(new RegExp(`"${key}"\\s*:\\s*"((?:[^"\\\\]|\\\\.)*)`));
  if (!m?.[1]) return undefined;
  let value: string;
  try {
    value = JSON.parse(`"${m[1]}"`);
  } catch {
    value = m[1].replace(/\\"/g, '"').replace(/\\n/g, "\n");
  }
  // Keep command line boundaries: the first lines can be Hoxi's private
  // stop tag, which must be stripped without swallowing the real command.
  return (key === "command" || key === "cmd" ? value : value.replace(/\r?\n/g, " ")).trim();
}

const RAW_KEYS = [
  "filePath", "file_path", "path", "file", "TargetFile", "targetFile", "target_file", "command", "cmd",
  "description", "pattern", "include", "url", "subagent_type", "patchText", "name",
] as const;

export function resolveToolInput(part: ToolPart): Record<string, any> {
  const input = part.state.input;
  if (input && Object.keys(input).length > 0) return input;
  const raw = part.state.status === "pending" ? part.state.raw : "";
  if (typeof raw === "string" && raw.length > 0) {
    const out: Record<string, any> = {};
    for (const k of RAW_KEYS) {
      const v = extractRawField(raw, k);
      if (v) out[k] = v;
    }
    if (!out.filePath) {
      out.filePath = out.file_path || out.TargetFile || out.targetFile || out.target_file;
    }
    if (!out.command && out.cmd) out.command = out.cmd;
    return out;
  }
  return input || {};
}

export function extractTargetFile(part: ToolPart): string | null {
  const toolLower = (part.tool || "").toLowerCase();
  if (toolLower === "glob" || toolLower === "grep") return null;
  const input = resolveToolInput(part);
  const metaFiles = part.state.status !== "pending" ? part.state.metadata?.files : undefined;
  const firstMetaObj = Array.isArray(metaFiles) && metaFiles[0] ? metaFiles[0] : undefined;
  const firstMetaRel = typeof firstMetaObj?.relativePath === "string" ? firstMetaObj.relativePath : "";
  const firstMetaAbs = typeof firstMetaObj?.filePath === "string" ? firstMetaObj.filePath : "";
  const firstMetaFile = firstMetaObj
    ? (firstMetaRel && !firstMetaRel.startsWith("..") ? firstMetaRel : firstMetaAbs) || firstMetaRel || firstMetaObj.file || firstMetaObj.path
    : undefined;
  const patchMatch =
    typeof input.patchText === "string"
      ? input.patchText.match(/^\*\*\*\s+(?:Update|Add|Delete)\s+File:\s*(.+)$/m)
      : null;
  const raw =
    input.filePath || input.file_path || input.path || input.file ||
    input.filename || input.targetFile || input.TargetFile || input.target_file || input.Path ||
    input.AbsolutePath || input.notebook_path || firstMetaFile || patchMatch?.[1]?.trim() ||
    (part.state.status !== "pending"
      ? part.state.metadata?.filePath || part.state.metadata?.filepath
      : undefined);
  if (typeof raw === "string" && raw.trim()) {
    const trimmed = raw.trim();
    const root = workspace.rootDir.replace(/\/+$/, "");
    if (root && (trimmed === root || trimmed.startsWith(`${root}/`))) {
      return trimmed.slice(root.length).replace(/^\/+/, "") || ".";
    }
    return trimmed;
  }
  return null;
}

export function extractCommand(part: ToolPart): string {
  const input = resolveToolInput(part);
  const command = String(input.command || input.cmd || input.script || "");
  return isShellTool(part.tool) ? cleanShellDisplayText(command) : command;
}

const SHELL_STOP_TRAP_RE = String.raw`trap 'printf "\\n\[Command stopped by user\]\\n"; exit 130' USR1; trap : EXIT`;
const SHELL_STOP_MARKER_RE = new RegExp(
  String.raw`^(?:[ \t]*(?:#?[ \t]*hoxi-stop:[^\r\n]*(?:\r?\n(?:[ \t]*(?:trap : EXIT|` +
    SHELL_STOP_TRAP_RE +
    String.raw`)[ \t]*(?:\r?\n|$))?|$)|trap : EXIT[ \t]+# hoxi-stop:[^\r\n]*(?:\r?\n|$)))+`,
);
const SHELL_SANDBOX_WRAP_RE =
  /^'?\/usr\/bin\/sandbox-exec'?\s+-f\s+(?:'[^']*'|\S+)\s+--\s+\/bin\/sh\s+-c\s+'((?:[^']|'\\'')*)'$/;

// Old histories and pending/raw events may predate backend normalization.
// Clean their display text without mutating tool input or actual shell output.
function cleanShellDisplayText(text: string): string {
  let cur = text;
  for (let i = 0; i < 8; i++) {
    let next = cur.replace(SHELL_STOP_MARKER_RE, "");
    const match = SHELL_SANDBOX_WRAP_RE.exec(next.trim());
    if (match) {
      next = match[1].replace(/'\\''/g, "'").replace(SHELL_STOP_MARKER_RE, "");
    }
    if (next === cur) break;
    cur = next;
  }
  return cur;
}

export function extractDescription(part: ToolPart): string {
  const input = resolveToolInput(part);
  if (isShellTool(part.tool)) {
    for (const value of [input.description, part.state.title]) {
      if (typeof value === "string") {
        const description = cleanShellDisplayText(value).trim();
        if (description) return description;
      }
    }
    return extractCommand(part).split(/\r?\n/, 1)[0].trim();
  }
  if (isQuestionTool(part.tool)) return questionSummaryText(part, findQuestionReply(part.callID));
  if ((part.tool || "").toLowerCase() === "skill") {
    const skillName =
      (typeof input.name === "string" && input.name.trim()) ||
      (part.state.status !== "pending" &&
      typeof (part.state.metadata as Record<string, unknown> | undefined)?.name === "string"
        ? String((part.state.metadata as Record<string, unknown>).name).trim()
        : "");
    if (skillName) return skillName;
  }
  if (typeof input.description === "string" && input.description.trim()) {
    return input.description.trim();
  }
  if (typeof input.pattern === "string" && input.pattern.trim()) {
    const dir = typeof input.path === "string" && input.path.trim() ? getFilename(input.path) : "";
    return dir ? `${input.pattern.trim()} (${dir})` : input.pattern.trim();
  }
  if (typeof input.url === "string" && input.url.trim()) return input.url.trim();
  if (part.state.title) return part.state.title;
  return "";
}

const MAX_EXPANDED_TOOL_OUTPUT_CHARS = 64 * 1024;
const ROLLING_SHELL_TAIL_CHARS = 2048;

export function truncateToolOutput(text: string): string {
  if (!text || text.length <= MAX_EXPANDED_TOOL_OUTPUT_CHARS) return text;
  const omitted = text.length - MAX_EXPANDED_TOOL_OUTPUT_CHARS;
  return `${text.slice(0, MAX_EXPANDED_TOOL_OUTPUT_CHARS)}\n\n… [${omitted.toLocaleString()} chars truncated]`;
}

function rawShellOutputFromState(state: ToolState): string {
  if (state.status === "completed") {
    const metaOut = state.metadata?.output ?? state.metadata?.stdout;
    return typeof metaOut === "string" ? metaOut : state.output || "";
  }
  if (state.status === "running") {
    const metaOut = state.metadata?.output ?? state.metadata?.stdout;
    return typeof metaOut === "string" ? metaOut : "";
  }
  if (state.status === "error") {
    const metaOut = state.metadata?.output ?? state.metadata?.stderr;
    return typeof metaOut === "string" ? `${metaOut}\n${state.error}` : state.error || "";
  }
  return "";
}

// Only describe the lack of output, never infer whether the process is stuck.
// Approval waits (pending) and historical/interrupted tools must stay quiet.
export function isShellAwaitingFirstOutput(part: ToolPart, active: boolean, elapsedSeconds: number): boolean {
  return active && part.state.status === "running" && isShellTool(part.tool) &&
    elapsedSeconds >= 15 && rawShellOutputFromState(part.state).trim().length === 0;
}

export function extractShellOutput(state: ToolState, partID?: string): string {
  const full = partID ? getFullToolOutput(partID) : undefined;
  const raw = full ?? rawShellOutputFromState(state);
  return raw ? processCarriageReturns(truncateToolOutput(raw)) : "";
}

export function extractRollingShellLines(state: ToolState): string[] {
  const raw = rawShellOutputFromState(state);
  if (!raw) return [];
  const tail = raw.length > ROLLING_SHELL_TAIL_CHARS ? raw.slice(-ROLLING_SHELL_TAIL_CHARS) : raw;
  const cleaned = processCarriageReturns(tail).trim();
  return cleaned ? cleaned.split("\n").slice(-5) : [];
}

export function extractExitCode(state: ToolState): number | undefined {
  if (state.status === "pending") return undefined;
  const code = state.metadata?.exit ?? state.metadata?.exitCode;
  return typeof code === "number" ? code : undefined;
}

export function countDiffLines(
  patch?: string,
  before?: string,
  after?: string
): { additions: number; deletions: number } {
  if (patch) {
    let additions = 0, deletions = 0;
    for (const line of patch.split("\n")) {
      if (line.startsWith("+") && !line.startsWith("+++")) additions++;
      else if (line.startsWith("-") && !line.startsWith("---")) deletions++;
    }
    return { additions, deletions };
  }
  if (typeof before === "string" && typeof after === "string") {
    if (before === after) return { additions: 0, deletions: 0 };
    const bLines = before ? before.replace(/\r\n/g, "\n").split("\n") : [];
    const aLines = after ? after.replace(/\r\n/g, "\n").split("\n") : [];
    let prefix = 0;
    while (
      prefix < bLines.length &&
      prefix < aLines.length &&
      bLines[prefix] === aLines[prefix]
    ) {
      prefix++;
    }
    let suffix = 0;
    while (
      suffix < bLines.length - prefix &&
      suffix < aLines.length - prefix &&
      bLines[bLines.length - 1 - suffix] === aLines[aLines.length - 1 - suffix]
    ) {
      suffix++;
    }
    return {
      additions: Math.max(0, aLines.length - prefix - suffix),
      deletions: Math.max(0, bLines.length - prefix - suffix),
    };
  }
  if (before === undefined && typeof after === "string" && after.length > 0) {
    return {
      additions: after.replace(/\r\n/g, "\n").split("\n").length,
      deletions: 0,
    };
  }
  return { additions: 0, deletions: 0 };
}

export interface DiagnosticItem {
  range: { start: { line: number; character: number } };
  message: string;
  severity?: number;
}

/**
 * Ported from @kilocode/kilo-ui/src/components/message-part.tsx DiagnosticsDisplay
 * Surfaces up to 3 post-edit LSP compiler/type errors inline inside the file edit card.
 */
export function InlineDiagnosticsDisplay(props: { diagnostics: DiagnosticItem[] }) {
  const errors = createMemo(() =>
    (props.diagnostics || []).filter((d) => !d.severity || d.severity === 1).slice(0, 3)
  );

  return (
    <Show when={errors().length > 0}>
      <div class="border-t border-[var(--danger-border)]/50 bg-[var(--danger-surface)]/45 px-3.5 py-2 space-y-1">
        <For each={errors()}>
          {(d) => (
            <div class="flex items-baseline gap-2 text-[12px] leading-snug font-mono">
              <span class="shrink-0 font-semibold uppercase tracking-wider text-[11px] text-[var(--danger-text)]">
                {t("toolCard.error")}
              </span>
              <span class="shrink-0 text-[var(--text-faint)] tabular-nums">
                [{(d.range?.start?.line ?? 0) + 1}:{(d.range?.start?.character ?? 0) + 1}]
              </span>
              <span class="text-[var(--text-primary)] break-all">{d.message}</span>
            </div>
          )}
        </For>
      </div>
    </Show>
  );
}

export function ToolCardExpandedBody(props: {
  part: ToolPart;
  isShell: boolean;
  isRunning?: boolean;
  stoppingCmd?: boolean;
  onStopCommand?: (e: MouseEvent) => void;
  isEdit: boolean;
  isTodo: boolean;
  isQuestion: boolean;
  shellCmd: string;
  shellOut: string;
  copiedCmd: boolean;
  onCopyCommand: (e: MouseEvent) => void;
  setOutputRef: (el: HTMLPreElement | undefined) => void;
  todoItems: Array<{ content: string; status: string }>;
  fileDiff: SnapshotFileDiff | undefined;
  fileDiffs?: SnapshotFileDiff[];
}) {
  const status = () => props.part.state.status;
  const diffs = createMemo(() => {
    if (props.fileDiffs && props.fileDiffs.length > 0) return props.fileDiffs;
    return props.fileDiff ? [props.fileDiff] : [];
  });

  return (
    <div class="bg-transparent">
      {/* Case 1: Shell / Bash Tool -> Kilo-style Terminal Card */}
      <Show when={props.isShell}>
        <div class="bg-[#0d1117] text-[#e6edf3] font-mono text-[12px]">
          <div class="flex items-center justify-between border-b border-white/10 bg-white/[0.03] px-3.5 py-1.5 text-[11px] text-[#8b949e]">
            <div class="flex items-center gap-2 min-w-0 truncate">
              <span class="text-[#58a6ff] font-semibold">$</span>
              <code class="truncate text-[#e6edf3] selectable-text">{props.shellCmd}</code>
            </div>
            <div class="flex items-center gap-1 shrink-0">
              <Show when={props.isRunning && props.onStopCommand}>
                <button
                  type="button"
                  disabled={props.stoppingCmd}
                  onClick={(e) => props.onStopCommand?.(e)}
                  title={t("toolCard.stopCommandTooltip")}
                  class="inline-flex items-center gap-1 rounded border border-[#f85149]/40 bg-[#f85149]/15 px-2 py-0.5 text-[11px] font-medium text-[#ff7b72] hover:bg-[#f85149]/25 disabled:opacity-60 transition-colors cursor-pointer mr-1"
                >
                  <span class="inline-block w-2 h-2 rounded-[1.5px] bg-current shrink-0" />
                  <span>
                    {props.stoppingCmd ? t("toolCard.stoppingCommand") : t("toolCard.stopCommand")}
                  </span>
                </button>
              </Show>
              <ActionIconButton
                variant="dark"
                tooltipSide="left"
                label={t("timeline.terminalBtn")}
                onClick={(e) => {
                  e.stopPropagation();
                  setWorkspace("inspectorOpen", true);
                  setWorkspace("inspectorTab", "terminal");
                }}
              >
                <Icon name="console" size="small" />
              </ActionIconButton>
              <ActionIconButton
                variant="dark"
                tooltipSide="left"
                label={props.copiedCmd ? t("common.copied") : t("common.copy")}
                onClick={(e) => props.onCopyCommand(e)}
              >
                <Show when={props.copiedCmd} fallback={<Icon name="copy" size="small" />}>
                  <Icon name="check" size="small" class="text-[#3fb950]" />
                </Show>
              </ActionIconButton>
            </div>
          </div>
          <Show
            when={props.shellOut.trim().length > 0}
            fallback={
              <div class="px-3.5 py-2.5 text-[12px] text-[#8b949e] italic">
                {status() === "running"
                  ? t("toolCard.waitingForOutput")
                  : t("toolCard.noOutput")}
              </div>
            }
          >
            <pre
              ref={props.setOutputRef}
              class="max-h-[260px] overflow-auto px-3.5 py-2.5 text-[12px] leading-[1.55] text-[#e6edf3] whitespace-pre-wrap break-all selectable-text"
            >
              {props.shellOut}
            </pre>
          </Show>
        </div>
      </Show>

      {/* Case 2: Todo Checklist Tool (`todowrite` / `todoread`) */}
      <Show when={props.isTodo && props.todoItems.length > 0}>
        <div class="p-3 space-y-1.5">
          <For each={props.todoItems}>
            {(item) => (
              <div class="flex items-start gap-2 text-[13px] leading-snug">
                <span
                  class={
                    item.status === "completed"
                      ? "mt-0.5 text-[var(--ok-text)] shrink-0"
                      : item.status === "in_progress"
                        ? "mt-0.5 text-[var(--accent-primary)] shrink-0"
                        : "mt-0.5 text-[var(--text-faint)] shrink-0"
                  }
                >
                  <Icon
                    name={item.status === "completed" ? "check-small" : "dot-grid"}
                    size="small"
                  />
                </span>
                <span
                  class={
                    item.status === "completed"
                      ? "line-through text-[var(--text-muted)]"
                      : item.status === "in_progress"
                        ? "font-medium text-[var(--text-primary)]"
                        : "text-[var(--text-secondary)]"
                  }
                >
                  {item.content}
                </span>
              </div>
            )}
          </For>
        </div>
      </Show>

      {/* Case 3: File Edit / Write / Patch Tool -> Inline Diff Preview */}
      <Show when={props.isEdit}>
        <div class="p-3 space-y-3">
          <Show
            when={diffs().length > 0}
            fallback={
              <pre class="text-[12px] font-mono bg-[var(--bg-code)] p-2.5 rounded-lg overflow-x-auto max-h-48 text-[var(--text-secondary)] selectable-text">
                {JSON.stringify(props.part.state.input, null, 2)}
              </pre>
            }
          >
            <For each={diffs()}>
              {(diff) => (
                <div>
                  <div class="flex items-center justify-between mb-1.5">
                    <span class="text-[11px] font-medium text-[var(--text-muted)] font-mono truncate">
                      {diffs().length > 1 ? diff.file : t("toolCard.inlineChangePreview")}
                    </span>
                    <ActionIconButton
                      variant="accent"
                      tooltipSide="left"
                      label={t("timeline.viewChanges")}
                      onClick={() => openDiffInInspector(diff, "session")}
                    >
                      <Icon name="code-lines" size="small" />
                    </ActionIconButton>
                  </div>
                  <FileDiffBlock diff={diff} defaultExpanded={true} compact={true} />
                </div>
              )}
            </For>
          </Show>
        </div>
      </Show>

      {/* Case 3.5: Question Tool -> Asked questions + user selections */}
      <Show when={props.isQuestion}>
        <QuestionToolBody part={props.part} />
      </Show>

      {/* Case 4: Generic / Read / Search Tool */}
      <Show when={!props.isShell && !props.isEdit && !props.isTodo && !props.isQuestion}>
        <div class="p-3 space-y-2.5 text-[12px]">
          <Show when={Object.keys(props.part.state.input || {}).length > 0}>
            <div>
              <div class="text-[11px] font-medium text-[var(--text-muted)] mb-1">
                {t("toolCard.arguments")}
              </div>
              <pre class="text-[12px] font-mono bg-[var(--bg-code)] border border-[var(--border-subtle)] p-2.5 rounded-lg overflow-x-auto max-h-40 text-[var(--text-secondary)] selectable-text">
                {JSON.stringify(props.part.state.input, null, 2)}
              </pre>
            </div>
          </Show>

          <Show when={props.part.state.status === "completed" && props.part.state.output}>
            <div>
              <div class="text-[11px] font-medium text-[var(--text-muted)] mb-1">
                {t("toolCard.output")}
              </div>
              <pre class="text-[12px] font-mono bg-[var(--bg-code)] border border-[var(--border-subtle)] p-2.5 rounded-lg overflow-x-auto max-h-56 text-[var(--text-secondary)] whitespace-pre-wrap break-all selectable-text">
                {processCarriageReturns(
                  truncateToolOutput(
                    getFullToolOutput(props.part.id) ??
                      (props.part.state as Extract<ToolState, { status: "completed" }>).output
                  )
                )}
              </pre>
            </div>
          </Show>

          <Show when={props.part.state.status === "error"}>
            <div>
              <div class="text-[11px] font-medium text-[var(--danger-text)] mb-1">
                {t("toolCard.error")}
              </div>
              <pre class="text-[12px] font-mono bg-[var(--danger-surface)] border border-[var(--danger-border)] p-2.5 rounded-lg overflow-x-auto text-[var(--danger-text)] whitespace-pre-wrap selectable-text">
                {(props.part.state as Extract<ToolState, { status: "error" }>).error}
              </pre>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}
