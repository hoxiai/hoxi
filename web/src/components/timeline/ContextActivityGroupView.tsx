// web/src/components/timeline/ContextActivityGroupView.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  on,
  onCleanup,
  Show,
  type JSX,
} from "solid-js";
import type { MessagePart, ToolPart } from "../../services/sse-types";
import {
  extractChildSubagentSessionId,
  isSessionBusy,
  isSubagentToolAlive,
  resolveSubagentTitle,
  workspace,
} from "../../store/workspace";
import {
  DiffChanges,
  Icon,
  type IconName,
  TextReveal,
  TextShimmer,
} from "../icons";
import { resolveSubagentCurrentStepLabel } from "./SubagentInlineTimeline";
import { liveReasoningTicker, reasoningHeading, reasoningSummary } from "./ThoughtBox";
import HoxiActivityLogo from "./HoxiActivityLogo";

import {
  extractCommand,
  extractDescription,
  extractTargetFile,
  formatToolDisplayName,
  getFilename,
  isEditingTool,
  isReadTool,
  isSearchOrListTool,
  isShellTool,
  isTaskTool,
} from "./tool-card-helpers";
import { resolveToolFileDiffs } from "./tool-card-state";
import { t as tI18n } from "../../i18n";
import { isAttentionToolPart } from "./context-activity-parts";

export {
  isTodoToolPart,
  isVisibleTextPart,
  isContextToolPart,
  isWorkPart,
  isExplorationPart,
  isAttentionToolPart,
} from "./context-activity-parts";
export {
  groupAssistantParts,
  stabilizeTurnRenderItems,
  resolveMergedTurnParts,
  resolveTurnNavigationTarget,
  hasLiveTailWorkGroup,
  hasLiveTailRenderedOutput,
} from "./context-activity-turns";
export type { TurnRenderItem, MergedTurnResolution } from "./context-activity-turns";

function basename(p: string): string {
  const parts = p.split("/").filter(Boolean);
  return parts.length ? parts[parts.length - 1] : p;
}

function summarizeStepTarget(part: ToolPart): string {
  if (isTaskTool(part.tool)) {
    const childID = extractChildSubagentSessionId(part);
    const rawDesc = extractDescription(part);
    const desc = rawDesc && resolveSubagentTitle(part.sessionID, childID, rawDesc).label;
    const childStep = resolveSubagentCurrentStepLabel(childID);
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
  renderFallbackPart: (part: MessagePart, revealKey?: string) => JSX.Element;
  isLiveTail?: boolean;
  startedAt?: number;
  revealKey?: string;
}) {
  let rootRef: HTMLDivElement | undefined;
  const groupKey = createMemo(() => props.parts[0]?.id || "");
  const [open, setOpen] = createSignal(
    expandedActivityGroupKeys.has(groupKey()) && !collapsedActivityGroupKeys.has(groupKey())
  );
  const [stepLimit, setStepLimit] = createSignal(40);
  const visibleSteps = createMemo(() => props.parts.slice(-stepLimit()));

  // Sync open state when groupKey changes across virtualized/reused turns,
  // keeping the bar compact by default so transitioning from live steps to
  // answer text never collapses a multi-hundred-pixel drawer.
  createEffect(() => {
    const key = groupKey();
    if (!key) return;
    setStepLimit(40);
    setOpen(expandedActivityGroupKeys.has(key) && !collapsedActivityGroupKeys.has(key));
  });

  createEffect(on(() => props.revealKey, (revealKey) => {
    if (!revealKey) return;
    const key = groupKey();
    if (key) {
      expandedActivityGroupKeys.add(key);
      collapsedActivityGroupKeys.delete(key);
    }
    setOpen(true);
    setStepLimit(props.parts.length);
  }));

  const toggleOpen = () => {
    const key = groupKey();
    const next = !open();
    if (!next && rootRef) {
      const scroller = rootRef.closest(".timeline-result-scroll");
      if (scroller instanceof HTMLElement) {
        const groupTop = rootRef.getBoundingClientRect().top;
        const scrollerTop = scroller.getBoundingClientRect().top;
        if (groupTop < scrollerTop) {
          scroller.scrollTop += groupTop - scrollerTop - 8;
        }
      }
    }
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

  const renderStepWindow = () => <>
    <Show when={props.parts.length > stepLimit()}>
      <button type="button" class="text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
        onClick={() => setStepLimit((limit) => limit + 40)}>
        {tI18n("timeline.loadEarlierSteps", { count: Math.min(40, props.parts.length - stepLimit()) })}
      </button>
    </Show>
    <For each={visibleSteps()}>{(part) => props.renderFallbackPart(part, props.revealKey)}</For>
  </>;

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
    const readTargets: string[] = [];
    const editTargets: string[] = [];
    const cmdTargets: string[] = [];
    const searchTargets: string[] = [];
    const subagentTargets: string[] = [];

    const pushUnique = (arr: string[], val: string) => {
      const clean = val.trim();
      if (clean && !arr.includes(clean)) arr.push(clean);
    };

    for (const p of props.parts) {
      if (p.type === "reasoning") {
        thoughts++;
      } else if (p.type === "tool") {
        const target = summarizeStepTarget(p);
        if (isReadTool(p.tool)) {
          reads++;
          pushUnique(readTargets, target);
        } else if (isEditingTool(p.tool)) {
          edits++;
          pushUnique(editTargets, target);
        } else if (isShellTool(p.tool)) {
          commands++;
          pushUnique(cmdTargets, target);
        } else if (isSearchOrListTool(p.tool)) {
          searches++;
          pushUnique(searchTargets, target);
        } else if (isTaskTool(p.tool)) {
          subagents++;
          pushUnique(subagentTargets, target);
        } else {
          otherTools++;
        }
      }
    }

    const buildTooltip = (header: string, items: string[]): string => {
      if (items.length === 0) return header;
      const preview = items.slice(0, 6).map((item) => `• ${item}`).join("\n");
      const extra = items.length > 6 ? `\n… +${items.length - 6}` : "";
      return `${header}\n${preview}${extra}`;
    };

    const toolCount = reads + searches + edits + commands + subagents + otherTools;
    const badges: Array<{ icon: IconName; count: number; tooltip: string }> = [];
    if (reads > 0) {
      badges.push({
        icon: "glasses",
        count: reads,
        tooltip: buildTooltip(tI18n("timeline.reads", { count: reads }), readTargets),
      });
    }
    if (edits > 0) {
      badges.push({
        icon: "edit-small-2",
        count: edits,
        tooltip: buildTooltip(tI18n("timeline.editsCount", { count: edits }), editTargets),
      });
    }
    if (commands > 0) {
      badges.push({
        icon: "console",
        count: commands,
        tooltip: buildTooltip(tI18n("timeline.commandsCount", { count: commands }), cmdTargets),
      });
    }
    if (searches > 0) {
      badges.push({
        icon: "magnifying-glass",
        count: searches,
        tooltip: buildTooltip(tI18n("timeline.searches", { count: searches }), searchTargets),
      });
    }
    if (subagents > 0) {
      badges.push({
        icon: "task",
        count: subagents,
        tooltip: buildTooltip(
          tI18n("timeline.subagentsCount", { count: subagents }),
          subagentTargets
        ),
      });
    }

    return {
      toolCount,
      thoughts,
      badges,
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
        const heading = liveReasoningTicker(p.text ?? "", true);
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
      return sec >= 0.05 ? `${sec.toFixed(1)}s` : "";
    }
    if (!end || end <= start) return "";
    const sec = (end - start) / 1000;
    if (sec < 0.05) return "";
    return sec < 60
      ? `${sec.toFixed(1)}s`
      : `${Math.floor(sec / 60)}m ${Math.round(sec % 60)}s`;
  });

  return (
    <Show
      when={stats().toolCount > 0}
      fallback={
        <div class="space-y-1">
          {renderStepWindow()}
        </div>
      }
    >
      <div ref={rootRef}>
        <button
          type="button"
          onClick={toggleOpen}
          class={`group/activity flex w-full items-center justify-between gap-2 rounded-md border border-transparent px-2 py-1.5 text-left text-[13px] transition-all cursor-pointer select-none ${
            open()
              ? "sticky top-0 z-10 bg-[var(--bg-panel)]/92 backdrop-blur-md border-[var(--border-subtle)]/50 text-[var(--text-primary)] shadow-xs"
              : isRunning()
                ? "bg-[var(--accent-surface)]/15 hover:bg-[var(--accent-surface)]/30 text-[var(--text-primary)]"
                : "bg-[var(--bg-inset)]/35 hover:bg-[var(--bg-inset)]/75 text-[var(--text-muted)] hover:text-[var(--text-primary)]"
          }`}
        >
          <div class="flex items-center gap-2 min-w-0 flex-1">
            <Show
              when={isRunning()}
              fallback={
                <span class="inline-grid place-items-center shrink-0">
                  <span class="[grid-area:1/1] inline-flex items-center text-[var(--ok-text)] transition-opacity duration-150 group-hover/activity:opacity-0">
                    <Icon name="circle-check" size="small" />
                  </span>
                  <span class="[grid-area:1/1] inline-flex items-center text-[var(--accent-primary)] opacity-0 transition-opacity duration-150 group-hover/activity:opacity-100">
                    <Icon
                      name={stats().toolCount > 0 ? "layers" : "brain"}
                      size="small"
                    />
                  </span>
                </span>
              }
            >
              <span class="text-[var(--accent-primary)] inline-flex items-center shrink-0">
                <HoxiActivityLogo active={!hasPendingApproval()} />
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
                  <Show when={stats().badges.length > 0}>
                    <span class="hidden sm:inline-flex items-center gap-2 text-[var(--text-muted)] shrink-0 opacity-0 transition-opacity duration-150 group-hover/activity:opacity-100 group-focus-within/activity:opacity-100">
                      <span class="text-[var(--text-faint)]">·</span>
                      <For each={stats().badges}>
                        {(badge) => (
                          <span
                            class="inline-flex items-center gap-1 font-mono text-[12px] tabular-nums text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors"
                            title={badge.tooltip}
                          >
                            <Icon name={badge.icon} size="small" />
                            <span>{badge.count}</span>
                          </span>
                        )}
                      </For>
                    </span>
                  </Show>
                  <Show when={!open() && targetPreview()}>
                    <span class="hidden lg:inline font-mono text-[12px] text-[var(--text-faint)] truncate max-w-[320px]">
                      ({targetPreview()})
                    </span>
                  </Show>
                </>
              }
            >
              <span class="font-medium text-[var(--accent-primary)] shrink-0">
                <TextShimmer text={activeStepDisplay().action} active={!hasPendingApproval()} />
              </span>
              <Show when={activeStepDisplay().detail}>
                <span class="font-mono text-[12px] text-[var(--text-secondary)] truncate max-w-[360px] inline-flex items-center min-w-0">
                  <TextReveal text={activeStepDisplay().detail} truncate />
                </span>
              </Show>
              <Show when={stats().badges.length > 0}>
                <span class="hidden lg:inline-flex items-center gap-2 text-[var(--text-muted)] shrink-0">
                  <span class="text-[var(--text-faint)]">·</span>
                  <For each={stats().badges}>
                    {(badge) => (
                      <span
                        class="inline-flex items-center gap-1 font-mono text-[12px] tabular-nums text-[var(--text-muted)]"
                        title={badge.tooltip}
                      >
                        <Icon name={badge.icon} size="small" />
                        <span>{badge.count}</span>
                      </span>
                    )}
                  </For>
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
              <span
                class="inline-flex items-center text-[var(--warn-text)]/80"
                title={tI18n("timeline.stepsAttentionBadge", {
                  count: attentionTools().length,
                })}
                aria-label={tI18n("timeline.stepsAttentionBadge", {
                  count: attentionTools().length,
                })}
              >
                <Icon name="warning" size="small" />
              </span>
            </Show>

            <Show when={groupDiffStats()}>
              {(diffStats) => <DiffChanges changes={diffStats()} />}
            </Show>

            <Show when={durationLabel()}>
              <span class="font-mono text-[12px] tabular-nums text-[var(--text-faint)]">
                {durationLabel()}
              </span>
            </Show>

            <span
              class="hoxi-chevron text-[var(--text-faint)]"
              data-open={open() ? "true" : "false"}
            >
              <Icon name="chevron-right" size="small" />
            </span>
          </div>
        </button>

        {/* Full chronological step list with smooth collapse transition */}
        <Show when={open()}>
          <div
            class={`grid transition-[grid-template-rows,opacity,margin-top] duration-200 ease-out ${
              open()
                ? "mt-1.5 grid-rows-[1fr] opacity-100"
                : "mt-0 grid-rows-[0fr] opacity-0 pointer-events-none"
            }`}
            aria-hidden={!open()}
          >
            <div class="min-h-0 overflow-hidden">
              <div class="ml-2 border-l-2 border-[var(--border-subtle)] pl-3.5 space-y-1">
                {renderStepWindow()}
              </div>
            </div>
          </div>
        </Show>
      </div>
    </Show>
  );
}
