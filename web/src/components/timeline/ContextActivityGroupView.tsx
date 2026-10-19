// web/src/components/timeline/ContextActivityGroupView.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  onCleanup,
  Show,
  type JSX,
} from "solid-js";
import type { MessageInfo, MessagePart, ToolPart } from "../../services/sse-types";
import {
  extractChildSubagentSessionId,
  isSessionBusy,
  isSubagentToolAlive,
  isSubagentToolInterrupted,
  type MessageTurn,
  workspace,
} from "../../store/workspace";
import { DiffChanges, Icon, Spinner, TextShimmer } from "../icons";
import { resolveSubagentCurrentStepLabel } from "./SubagentInlineTimeline";
import { reasoningHeading, reasoningSummary } from "./ThoughtBox";

import {
  extractCommand,
  extractDescription,
  extractExitCode,
  extractTargetFile,
  formatToolDisplayName,
  getFilename,
  isEditingTool,
  isReadTool,
  isSearchOrListTool,
  isShellTool,
  isTaskTool,
} from "./tool-card-helpers";
import {
  resolveToolFileDiagnostics,
  resolveToolFileDiffs,
} from "./tool-card-state";
import { t as tI18n } from "../../i18n";

function basename(p: string): string {
  const parts = p.split("/").filter(Boolean);
  return parts.length ? parts[parts.length - 1] : p;
}

export function isTodoToolPart(part: MessagePart): boolean {
  if (part.type !== "tool") return false;
  const lower = part.tool.toLowerCase();
  return lower === "todowrite" || lower === "todoread";
}

export function isVisibleTextPart(
  part: MessagePart
): part is Extract<MessagePart, { type: "text" }> {
  return part.type === "text" && part.text.trim().length > 0;
}

export function isContextToolPart(
  part: MessagePart
): part is Extract<MessagePart, { type: "tool" }> {
  if (part.type !== "tool") return false;
  if (part.state.status === "error") return false;
  const t = part.tool.toLowerCase();
  return (
    t === "read" ||
    t.endsWith("_read") ||
    t === "grep" ||
    t === "glob" ||
    t === "list" ||
    t === "ls"
  );
}

export function isWorkPart(part: MessagePart): boolean {
  if (part.type === "reasoning") {
    return part.time?.end == null || Boolean(part.text && part.text.trim().length > 0);
  }
  return part.type === "tool";
}

export const isExplorationPart = isWorkPart;

export function isAttentionToolPart(part: MessagePart): part is ToolPart {
  if (part.type !== "tool" || isTodoToolPart(part)) return false;
  if (part.state.status === "error") return true;
  if (isShellTool(part.tool) && part.state.status === "completed") {
    const code = extractExitCode(part.state);
    if (code !== undefined && code !== 0) return true;
  }
  if (isEditingTool(part.tool) && part.state.status === "completed") {
    const file = extractTargetFile(part) || "";
    const diags = resolveToolFileDiagnostics(part, true, file);
    if (diags.some((d) => !d.severity || d.severity === 1)) return true;
  }
  if (isTaskTool(part.tool) && isSubagentToolInterrupted(part, part.sessionID)) {
    return true;
  }
  return false;
}

export type TurnRenderItem =
  | { kind: "single"; part: MessagePart }
  | { kind: "activity-group"; parts: MessagePart[] };

export function groupAssistantParts(parts: MessagePart[]): TurnRenderItem[] {
  const items: TurnRenderItem[] = [];
  let buffer: MessagePart[] = [];

  const flushBuffer = () => {
    if (buffer.length === 0) return;
    items.push({ kind: "activity-group", parts: buffer });
    buffer = [];
  };

  for (const part of parts) {
    if (isWorkPart(part)) {
      buffer.push(part);
    } else if (isVisibleTextPart(part)) {
      flushBuffer();
      items.push({ kind: "single", part });
    }
  }
  flushBuffer();
  return items;
}

export interface MergedTurnResolution {
  effectiveParts: MessagePart[];
  /** Later turns whose leading work this turn renders inside its own trailing group. */
  carriedTurnIDs: string[];
  isVisuallyEmpty: boolean;
  isFirstVisualTurnInCycle: boolean;
  isCycleEnd: boolean;
  /** This turn's trailing work run is still open at the end of the transcript. */
  ownsLiveTail: boolean;
}

const isRenderablePart = (part: MessagePart) => isWorkPart(part) || isVisibleTextPart(part);

/**
 * Aggregates contiguous work parts (`reasoning` + `tool`) across consecutive
 * assistant turns within the same user prompt cycle into a single Execution
 * Activity Bar.
 *
 * The run is anchored at the turn where it started: later work-only turns fold
 * forward into that anchor instead of the anchor moving to the newest turn. New
 * steps therefore only append parts to an already-mounted group, so streaming
 * never unmounts and rebuilds the group DOM (which caused flashes and height
 * jumps on every step).
 */
export function resolveMergedTurnParts(
  allTurns: MessageTurn[],
  globalIndex: number,
  minStartIndex = 0
): MergedTurnResolution {
  const turn = allTurns[globalIndex];
  const isCycleEnd =
    globalIndex === allTurns.length - 1 || allTurns[globalIndex + 1]?.info.role !== "assistant";
  if (!turn || turn.info.role !== "assistant") {
    return {
      effectiveParts: turn?.parts ?? [],
      carriedTurnIDs: [],
      isVisuallyEmpty: false,
      isFirstVisualTurnInCycle: true,
      isCycleEnd,
      ownsLiveTail: false,
    };
  }

  // The nearest earlier mounted turn that renders anything decides whether a work
  // run is still open (owned by some earlier turn) or was closed by an error.
  let runOpen = false;
  let hasPriorVisualTurnInCycle = false;
  for (let i = globalIndex - 1; i >= minStartIndex && allTurns[i]?.info.role === "assistant"; i--) {
    const prior = allTurns[i];
    if (prior.info.error) {
      hasPriorVisualTurnInCycle = true;
      break;
    }
    if (prior.parts.some(isRenderablePart)) {
      runOpen = true;
      hasPriorVisualTurnInCycle = true;
      break;
    }
  }

  const firstTextIdx = turn.parts.findIndex(isVisibleTextPart);
  const error = Boolean(turn.info.error);
  // Leading work joins the open run; this turn renders from its first visible text.
  const own = error || !runOpen ? turn.parts : firstTextIdx < 0 ? [] : turn.parts.slice(firstTextIdx);
  const ownsRun = !error && (runOpen ? firstTextIdx >= 0 : turn.parts.some(isRenderablePart));

  const carriedWork: MessagePart[] = [];
  const carriedTurnIDs: string[] = [];
  let ownsLiveTail = false;
  if (ownsRun) {
    let i = globalIndex + 1;
    for (; i < allTurns.length && allTurns[i].info.role === "assistant"; i++) {
      const next = allTurns[i];
      if (next.info.error) break;
      const nextTextIdx = next.parts.findIndex(isVisibleTextPart);
      const lead = (nextTextIdx < 0 ? next.parts : next.parts.slice(0, nextTextIdx)).filter(isWorkPart);
      if (lead.length > 0) {
        carriedWork.push(...lead);
        carriedTurnIDs.push(next.info.id);
      }
      if (nextTextIdx >= 0) break;
    }
    ownsLiveTail = i === allTurns.length;
  }

  const effectiveParts = carriedWork.length > 0 ? [...own, ...carriedWork] : own;
  return {
    effectiveParts,
    carriedTurnIDs,
    isVisuallyEmpty: !error && !effectiveParts.some(isRenderablePart),
    isFirstVisualTurnInCycle: !hasPriorVisualTurnInCycle,
    isCycleEnd,
    ownsLiveTail,
  };
}

// Folded turns have no visual anchor. Find the turn that actually renders their parts.
export function resolveTurnNavigationTarget(turns: MessageTurn[], messageID: string, partID?: string): string {
  const sourceIndex = turns.findIndex((turn) => turn.info.id === messageID);
  const source = turns[sourceIndex];
  if (!source || source.info.role !== "assistant") return messageID;
  const partIDs = new Set(partID ? [partID] : source.parts.map((part) => part.id));
  const ownParts = resolveMergedTurnParts(turns, sourceIndex).effectiveParts;
  if (source.info.error || ownParts.some((part) => partIDs.has(part.id))) return messageID;
  let cycleStart = sourceIndex;
  while (cycleStart > 0 && turns[cycleStart - 1]?.info.role === "assistant") cycleStart--;
  for (let index = cycleStart; index < turns.length && turns[index].info.role === "assistant"; index++) {
    if (index === sourceIndex) continue;
    const merged = resolveMergedTurnParts(turns, index);
    if (merged.effectiveParts.some((part) => partIDs.has(part.id))) return turns[index].info.id;
  }
  return messageID;
}

/**
 * Returns true when the latest assistant cycle ends with an active work group
 * (no visible text part after the last tool/reasoning part), meaning the inline
 * `<ContextActivityGroupView isLiveTail />` is already serving as the live bottom
 * status bar.
 */
export function hasLiveTailWorkGroup(
  cycleTurns: Array<{ info: MessageInfo; parts: MessagePart[] }>
): boolean {
  for (let i = cycleTurns.length - 1; i >= 0; i--) {
    const turn = cycleTurns[i];
    if (turn.info.role !== "assistant" || turn.info.error) return false;
    for (let p = turn.parts.length - 1; p >= 0; p--) {
      const part = turn.parts[p];
      if (isVisibleTextPart(part)) return false;
      if (isWorkPart(part)) return true;
    }
  }
  return false;
}

function summarizeStepTarget(part: ToolPart): string {
  if (isTaskTool(part.tool)) {
    const desc = extractDescription(part);
    const childStep = resolveSubagentCurrentStepLabel(
      extractChildSubagentSessionId(part)
    );
    if (desc && childStep) return `${desc} · ${childStep}`;
    return desc || childStep || "";
  }
  const file = extractTargetFile(part);
  if (file) return getFilename(file) || basename(file);
  if (isShellTool(part.tool)) {
    const desc = extractDescription(part);
    if (desc) return desc;
    const cmd = extractCommand(part);
    return cmd ? `$ ${cmd}` : "";
  }
  const desc = extractDescription(part);
  if (desc) return desc;
  const inp = part.state.input || {};
  if (typeof inp.pattern === "string" && inp.pattern.trim()) {
    return `"${inp.pattern.trim()}"`;
  }
  return "";
}


// Persistent manual expand state keyed by the first part ID of each group
const expandedActivityGroupKeys = new Set<string>();
const collapsedActivityGroupKeys = new Set<string>();

export default function ContextActivityGroupView(props: {
  parts: MessagePart[];
  renderFallbackPart: (part: MessagePart) => JSX.Element;
  isLiveTail?: boolean;
  startedAt?: number;
  revealKey?: string;
}) {
  const groupKey = createMemo(() => props.parts[0]?.id || "");
  const [open, setOpen] = createSignal(
    expandedActivityGroupKeys.has(groupKey()) && !collapsedActivityGroupKeys.has(groupKey())
  );

  createEffect(() => {
    if (!props.revealKey) return;
    const key = groupKey();
    if (key) expandedActivityGroupKeys.add(key);
    setOpen(true);
  });

  // Keep the live step visible, but never override an explicit user collapse.
  createEffect(() => {
    const key = groupKey();
    if (props.isLiveTail && (!key || !collapsedActivityGroupKeys.has(key))) {
      setOpen(true);
    }
  });

  createEffect(() => {
    const key = groupKey();
    if (key && !props.isLiveTail) {
      setOpen(expandedActivityGroupKeys.has(key));
    }
  });

  const toggleOpen = () => {
    const key = groupKey();
    const next = !open();
    if (key) {
      if (next) expandedActivityGroupKeys.add(key);
      else {
        expandedActivityGroupKeys.delete(key);
        collapsedActivityGroupKeys.add(key);
      }
      if (next) collapsedActivityGroupKeys.delete(key);
    }
    setOpen(next);
  };

  const sessionID = createMemo(() => props.parts[0]?.sessionID || workspace.activeSessionID);

  const isRunning = createMemo(() => {
    if (props.isLiveTail) return true;
    return props.parts.some((p) => {
      if (p.type === "reasoning") {
        return p.time?.end == null && isSessionBusy(p.sessionID);
      }
      if (p.type === "tool") {
        if (p.state.status !== "pending" && p.state.status !== "running") return false;
        if (isTaskTool(p.tool)) return isSubagentToolAlive(p, p.sessionID);
        return isSessionBusy(p.sessionID);
      }
      return false;
    });
  });

  const hasPendingApproval = createMemo(() => {
    const sid = sessionID();
    if (!sid || !isRunning()) return false;
    return workspace.pendingPermissions.some((req) => req.sessionID === sid);
  });

  const stats = createMemo(() => {
    let reads = 0;
    let searches = 0;
    let edits = 0;
    let commands = 0;
    let subagents = 0;
    let otherTools = 0;
    let thoughts = 0;

    for (const p of props.parts) {
      if (p.type === "reasoning") {
        thoughts++;
      } else if (p.type === "tool") {
        if (isReadTool(p.tool)) reads++;
        else if (isEditingTool(p.tool)) edits++;
        else if (isShellTool(p.tool)) commands++;
        else if (isSearchOrListTool(p.tool)) searches++;
        else if (isTaskTool(p.tool)) subagents++;
        else otherTools++;
      }
    }

    const toolCount = reads + searches + edits + commands + subagents + otherTools;
    const tokens: string[] = [];
    if (reads > 0) tokens.push(tI18n("timeline.reads", { count: reads }));
    if (edits > 0) tokens.push(tI18n("timeline.editsCount", { count: edits }));
    if (commands > 0) tokens.push(tI18n("timeline.commandsCount", { count: commands }));
    if (searches > 0) tokens.push(tI18n("timeline.searches", { count: searches }));
    if (subagents > 0) tokens.push(tI18n("timeline.subagentsCount", { count: subagents }));
    if (otherTools > 0 && tokens.length === 0) {
      tokens.push(tI18n("timeline.stepsCompleted", { count: otherTools }));
    }
    if (toolCount === 0 && thoughts > 0) {
      tokens.push(tI18n("timeline.thoughts", { count: thoughts }));
    }

    return {
      toolCount,
      thoughts,
      summaryText: tokens.join(", "),
    };
  });

  const activeStepDisplay = createMemo(() => {
    const runningTools: ToolPart[] = [];
    for (const p of props.parts) {
      if (
        p.type === "tool" &&
        (p.state.status === "pending" || p.state.status === "running")
      ) {
        runningTools.push(p);
      }
    }

    if (runningTools.length > 0) {
      const active = runningTools[runningTools.length - 1];
      const action = formatToolDisplayName(active.tool, active);
      const detail = summarizeStepTarget(active);
      const extra = runningTools.length > 1 ? ` (+${runningTools.length - 1})` : "";
      return {
        action,
        detail: `${detail}${extra}`.trim(),
      };
    }

    for (let i = props.parts.length - 1; i >= 0; i--) {
      const p = props.parts[i];
      if (p.type === "reasoning" && p.time?.end == null) {
        const heading = reasoningHeading(p.text ?? "", true).title;
        return {
          action: tI18n("timeline.thinking"),
          detail: heading || "",
        };
      }
    }

    // Fallback during brief transitions between steps while `isLiveTail` is true
    for (let i = props.parts.length - 1; i >= 0; i--) {
      const p = props.parts[i];
      if (p.type === "tool") {
        return {
          action: formatToolDisplayName(p.tool, p),
          detail: summarizeStepTarget(p),
        };
      }
    }

    return {
      action: tI18n("timeline.executing"),
      detail: "",
    };
  });

  const thoughtOnlyPreview = createMemo(() => {
    if (stats().toolCount > 0) return "";
    for (let i = props.parts.length - 1; i >= 0; i--) {
      const p = props.parts[i];
      if (p.type === "reasoning" && p.text?.trim()) {
        const cleaned = p.text.replace(/\[REDACTED\]/g, "").trim();
        const parsed = reasoningHeading(cleaned, false);
        return parsed.title || reasoningSummary(parsed.body);
      }
    }
    return "";
  });

  const targetPreview = createMemo(() => {
    if (stats().toolCount === 0) return thoughtOnlyPreview();
    return "";
  });

  const groupDiffStats = createMemo(() => {
    const byFile = new Map<string, { additions: number; deletions: number }>();
    for (const p of props.parts) {
      if (
        p.type !== "tool" ||
        p.state.status !== "completed" ||
        !isEditingTool(p.tool)
      ) {
        continue;
      }
      const target = extractTargetFile(p) || "";
      const diffs = resolveToolFileDiffs(p, true, target);
      for (const d of diffs) {
        if (!d.file) continue;
        byFile.set(d.file, {
          additions: d.additions || 0,
          deletions: d.deletions || 0,
        });
      }
    }
    if (byFile.size === 0) return undefined;
    let additions = 0;
    let deletions = 0;
    for (const item of byFile.values()) {
      additions += item.additions;
      deletions += item.deletions;
    }
    return additions > 0 || deletions > 0 ? { additions, deletions } : undefined;
  });

  const attentionTools = createMemo<ToolPart[]>(() =>
    props.parts.filter(isAttentionToolPart)
  );

  const timeRange = createMemo(() => {
    let start = props.startedAt && props.startedAt > 0 ? props.startedAt : undefined;
    let end: number | undefined;
    for (const p of props.parts) {
      if (p.type === "reasoning") {
        if (p.time?.start && (!start || p.time.start < start)) start = p.time.start;
        if (p.time?.end && (!end || p.time.end > end)) end = p.time.end;
      } else if (p.type === "tool" && p.state.status !== "pending") {
        const s = p.state.time?.start;
        if (s && (!start || s < start)) start = s;
        if (p.state.status === "completed" || p.state.status === "error") {
          const e = p.state.time?.end;
          if (e && (!end || e > end)) end = e;
        }
      }
    }
    return { start, end };
  });

  const [now, setNow] = createSignal(Date.now());
  createEffect(() => {
    if (!isRunning()) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 100);
    onCleanup(() => clearInterval(timer));
  });

  const durationLabel = createMemo(() => {
    const { start, end } = timeRange();
    if (!start) return "";
    if (isRunning()) {
      const sec = Math.max(0, (now() - start) / 1000);
      return `${sec.toFixed(1)}s`;
    }
    if (!end || end <= start) return "";
    const sec = (end - start) / 1000;
    return sec < 60
      ? `${sec.toFixed(1)}s`
      : `${Math.floor(sec / 60)}m ${Math.round(sec % 60)}s`;
  });

  return (
    <div>
      <button
        type="button"
        onClick={toggleOpen}
        class={`group/activity flex w-full items-center justify-between gap-2 rounded-md border border-transparent px-2 py-1.5 text-left text-[13px] transition-all cursor-pointer select-none ${
          open()
            ? "bg-[var(--bg-inset)]/65 text-[var(--text-primary)]"
            : isRunning()
              ? "bg-[var(--accent-surface)]/15 hover:bg-[var(--accent-surface)]/30 text-[var(--text-primary)]"
              : "bg-[var(--bg-inset)]/35 hover:bg-[var(--bg-inset)]/75 text-[var(--text-muted)] hover:text-[var(--text-primary)]"
        }`}
      >
        <div class="flex items-center gap-2 min-w-0 flex-1">
          <Show
            when={isRunning()}
            fallback={
              <Show
                when={attentionTools().length > 0}
                fallback={
                  <span class="text-[var(--text-muted)] group-hover/activity:text-[var(--accent-primary)] inline-flex items-center shrink-0">
                    <Icon
                      name={stats().toolCount > 0 ? "layers" : "brain"}
                      size="small"
                    />
                  </span>
                }
              >
                <span class="text-[var(--danger-text)] inline-flex items-center shrink-0">
                  <Icon name="circle-ban-sign" size="small" />
                </span>
              </Show>
            }
          >
            <span class="text-[var(--accent-primary)] inline-flex items-center shrink-0">
              <Spinner class="w-3.5 h-3.5" />
            </span>
          </Show>

          <Show
            when={isRunning()}
            fallback={
              <>
                <span class="font-medium text-[var(--text-secondary)] shrink-0">
                  {stats().toolCount > 0
                    ? tI18n("timeline.executedStepsSummary", {
                        count: stats().toolCount,
                      })
                    : tI18n("timeline.thought")}
                </span>
                <Show when={stats().toolCount > 0 && stats().summaryText}>
                  <span class="hidden sm:inline text-[12px] text-[var(--text-muted)] truncate">
                    · {stats().summaryText}
                  </span>
                </Show>
                <Show when={!open() && targetPreview()}>
                  <span class="hidden lg:inline font-mono text-[11.5px] text-[var(--text-faint)] truncate max-w-[320px]">
                    ({targetPreview()})
                  </span>
                </Show>
              </>
            }
          >
            <span class="font-medium text-[var(--accent-primary)] shrink-0">
              <TextShimmer text={activeStepDisplay().action} active={true} />
            </span>
            <Show when={activeStepDisplay().detail}>
              <span class="font-mono text-[12px] text-[var(--text-secondary)] truncate max-w-[360px]">
                {activeStepDisplay().detail}
              </span>
            </Show>
            <Show when={stats().summaryText}>
              <span class="hidden lg:inline text-[12px] text-[var(--text-muted)] shrink-0">
                · {stats().summaryText}
              </span>
            </Show>
          </Show>
        </div>

        <div class="flex items-center gap-2 shrink-0">
          <Show when={hasPendingApproval()}>
            <span class="rounded bg-[var(--warn-surface)] px-1.5 py-0.5 font-mono text-[11px] font-medium text-[var(--warn-text)]">
              {tI18n("timeline.waitingApprovalBadge")}
            </span>
          </Show>

          <Show when={attentionTools().length > 0}>
            <span class="rounded bg-[var(--danger-surface)] px-1.5 py-0.5 font-mono text-[11px] font-medium text-[var(--danger-text)]">
              {tI18n("timeline.stepsAttentionBadge", {
                count: attentionTools().length,
              })}
            </span>
          </Show>

          <Show when={groupDiffStats()}>
            {(diffStats) => <DiffChanges changes={diffStats()} />}
          </Show>

          <Show when={durationLabel()}>
            <span class="font-mono text-[11.5px] tabular-nums text-[var(--text-faint)]">
              {durationLabel()}
            </span>
          </Show>

          <span class="text-[var(--text-faint)] inline-flex items-center">
            <Icon name={open() ? "chevron-down" : "chevron-right"} size="small" />
          </span>
        </div>
      </button>

      {/* Auto-surface any errored / non-zero exit / interrupted tool cards even while collapsed */}
      <Show when={!open() && attentionTools().length > 0}>
        <div class="mt-1.5 ml-2 border-l-2 border-[var(--danger-border)]/70 pl-3 space-y-1">
          <For each={attentionTools()}>
            {(toolPart) => props.renderFallbackPart(toolPart)}
          </For>
        </div>
      </Show>

      {/* Full chronological step list when expanded */}
      <Show when={open()}>
        <div class="mt-1.5 ml-2 border-l-2 border-[var(--border-subtle)] pl-3.5 space-y-1">
          <For each={props.parts}>
            {(part) => props.renderFallbackPart(part)}
          </For>
        </div>
      </Show>
    </div>
  );
}
