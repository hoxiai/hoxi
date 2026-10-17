// web/src/components/timeline/tool-card-helpers.tsx
import { createMemo, For, Show } from "solid-js";
import type { MessagePart, SnapshotFileDiff, ToolState } from "../../services/sse-types";
import { openDiffInInspector, setWorkspace, workspace } from "../../store/workspace";
import { Icon, type IconName } from "../icons";
import { FileDiffBlock } from "../inspector/PierreDiffViewer";
import { t } from "../../i18n";

export type ToolPart = Extract<MessagePart, { type: "tool" }>;

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
  return t.includes("edit") || t.includes("write") || t.includes("patch");
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
  return t === "task" || t.includes("subagent");
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
  if (t === "question") return "bubble-5";
  return "mcp";
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
  const root = workspace.rootDir;
  if (root && dir.startsWith(root)) {
    return dir.slice(root.length).replace(/^\/+/, "") || ".";
  }
  return dir;
}

export function extractTargetFile(part: ToolPart): string | null {
  const input = part.state.input || {};
  const raw =
    input.filePath ||
    input.path ||
    input.file ||
    input.filename ||
    input.targetFile ||
    (part.state.status !== "pending"
      ? part.state.metadata?.filePath || part.state.metadata?.filepath
      : undefined);
  if (typeof raw === "string" && raw.trim()) {
    const root = workspace.rootDir;
    if (root && raw.startsWith(root)) {
      return raw.slice(root.length).replace(/^\/+/, "");
    }
    return raw.trim();
  }
  return null;
}

export function extractCommand(part: ToolPart): string {
  const input = part.state.input || {};
  return String(input.command || input.cmd || input.script || "");
}

export function extractDescription(part: ToolPart): string {
  const input = part.state.input || {};
  if (typeof input.description === "string" && input.description.trim()) {
    return input.description.trim();
  }
  if (part.state.status === "running" && part.state.title) return part.state.title;
  if (part.state.status === "completed" && part.state.title) return part.state.title;
  return "";
}

export function extractShellOutput(state: ToolState): string {
  if (state.status === "completed") {
    const metaOut = state.metadata?.output ?? state.metadata?.stdout;
    return processCarriageReturns(typeof metaOut === "string" ? metaOut : state.output || "");
  }
  if (state.status === "running") {
    const metaOut = state.metadata?.output ?? state.metadata?.stdout;
    return processCarriageReturns(typeof metaOut === "string" ? metaOut : "");
  }
  if (state.status === "error") {
    const metaOut = state.metadata?.output ?? state.metadata?.stderr;
    return processCarriageReturns(
      typeof metaOut === "string" ? `${metaOut}\n${state.error}` : state.error || ""
    );
  }
  return "";
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
    let additions = 0;
    let deletions = 0;
    for (const line of patch.split("\n")) {
      if (line.startsWith("+") && !line.startsWith("+++")) additions++;
      else if (line.startsWith("-") && !line.startsWith("---")) deletions++;
    }
    return { additions, deletions };
  }
  if (typeof before === "string" && typeof after === "string") {
    const bLen = before ? before.split("\n").length : 0;
    const aLen = after ? after.split("\n").length : 0;
    return {
      additions: Math.max(0, aLen - bLen) || (aLen > 0 ? 1 : 0),
      deletions: Math.max(0, bLen - aLen),
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
            <div class="flex items-baseline gap-2 text-[11.5px] leading-snug font-mono">
              <span class="shrink-0 font-semibold uppercase tracking-wider text-[10px] text-[var(--danger-text)]">
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
  isEdit: boolean;
  isTodo: boolean;
  shellCmd: string;
  shellOut: string;
  copiedCmd: boolean;
  onCopyCommand: (e: MouseEvent) => void;
  setOutputRef: (el: HTMLPreElement | undefined) => void;
  todoItems: Array<{ content: string; status: string }>;
  fileDiff: SnapshotFileDiff | undefined;
}) {
  const status = () => props.part.state.status;

  return (
    <div class="border-t border-[var(--border-subtle)] bg-[var(--bg-elevated)]/45">
      {/* Case 1: Shell / Bash Tool -> Kilo-style Terminal Card */}
      <Show when={props.isShell}>
        <div class="bg-[#0d1117] text-[#e6edf3] font-mono text-[12px]">
          <div class="flex items-center justify-between border-b border-white/10 bg-white/[0.03] px-3.5 py-1.5 text-[11px] text-[#8b949e]">
            <div class="flex items-center gap-2 min-w-0 truncate">
              <span class="text-[#58a6ff] font-semibold">$</span>
              <code class="truncate text-[#e6edf3] selectable-text">{props.shellCmd}</code>
            </div>
            <div class="flex items-center gap-1.5 shrink-0">
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  setWorkspace("inspectorOpen", true);
                  setWorkspace("inspectorTab", "terminal");
                }}
                class="inline-flex items-center gap-1 rounded px-2 py-0.5 text-[10.5px] text-[#8b949e] hover:bg-white/10 hover:text-white transition-colors cursor-pointer"
                title={t("timeline.openTerminalTooltip")}
              >
                <Icon name="console" size="small" />
                <span>{t("timeline.terminalBtn")}</span>
              </button>
              <button
                type="button"
                onClick={(e) => props.onCopyCommand(e)}
                class="inline-flex items-center gap-1 rounded px-2 py-0.5 text-[10.5px] text-[#8b949e] hover:bg-white/10 hover:text-white transition-colors cursor-pointer"
              >
                <Show when={props.copiedCmd} fallback={<Icon name="copy" size="small" />}>
                  <Icon name="check" size="small" class="text-[#3fb950]" />
                </Show>
                <span>{props.copiedCmd ? t("common.copied") : t("common.copy")}</span>
              </button>
            </div>
          </div>
          <Show
            when={props.shellOut.trim().length > 0}
            fallback={
              <div class="px-3.5 py-2.5 text-[11.5px] text-[#8b949e] italic">
                {status() === "running"
                  ? "Waiting for output…"
                  : "Command completed with no output."}
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
              <div class="flex items-start gap-2 text-[12.5px] leading-snug">
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
        <div class="p-3 space-y-2">
          <Show
            when={props.fileDiff}
            fallback={
              <pre class="text-[11.5px] font-mono bg-[var(--bg-code)] p-2.5 rounded-lg overflow-x-auto max-h-48 text-[var(--text-secondary)] selectable-text">
                {JSON.stringify(props.part.state.input, null, 2)}
              </pre>
            }
          >
            {(diff) => (
              <div>
                <div class="flex items-center justify-between mb-1.5">
                  <span class="text-[11px] font-medium text-[var(--text-muted)]">
                    {t("toolCard.inlineChangePreview")}
                  </span>
                  <button
                    type="button"
                    onClick={() => openDiffInInspector()}
                    class="inline-flex items-center gap-1 text-[11px] font-medium text-[var(--accent-primary)] hover:underline cursor-pointer"
                  >
                    <Icon name="code-lines" size="small" />
                    {t("toolCard.openInDiffInspector")}
                  </button>
                </div>
                <FileDiffBlock diff={diff()} defaultExpanded={true} compact={true} />
              </div>
            )}
          </Show>
        </div>
      </Show>

      {/* Case 4: Generic / Read / Search Tool */}
      <Show when={!props.isShell && !props.isEdit && !props.isTodo}>
        <div class="p-3 space-y-2.5 text-[12px]">
          <Show when={Object.keys(props.part.state.input || {}).length > 0}>
            <div>
              <div class="text-[10.5px] font-medium text-[var(--text-muted)] mb-1">
                {t("toolCard.arguments")}
              </div>
              <pre class="text-[11.5px] font-mono bg-[var(--bg-code)] border border-[var(--border-subtle)] p-2.5 rounded-lg overflow-x-auto max-h-40 text-[var(--text-secondary)] selectable-text">
                {JSON.stringify(props.part.state.input, null, 2)}
              </pre>
            </div>
          </Show>

          <Show when={props.part.state.status === "completed" && props.part.state.output}>
            <div>
              <div class="text-[10.5px] font-medium text-[var(--text-muted)] mb-1">
                {t("toolCard.output")}
              </div>
              <pre class="text-[11.5px] font-mono bg-[var(--bg-code)] border border-[var(--border-subtle)] p-2.5 rounded-lg overflow-x-auto max-h-56 text-[var(--text-secondary)] whitespace-pre-wrap break-all selectable-text">
                {processCarriageReturns(
                  (props.part.state as Extract<ToolState, { status: "completed" }>).output
                )}
              </pre>
            </div>
          </Show>

          <Show when={props.part.state.status === "error"}>
            <div>
              <div class="text-[10.5px] font-medium text-[var(--danger-text)] mb-1">
                {t("toolCard.error")}
              </div>
              <pre class="text-[11.5px] font-mono bg-[var(--danger-surface)] border border-[var(--danger-border)] p-2.5 rounded-lg overflow-x-auto text-[var(--danger-text)] whitespace-pre-wrap selectable-text">
                {(props.part.state as Extract<ToolState, { status: "error" }>).error}
              </pre>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}
