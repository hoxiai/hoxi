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
import { unwrap } from "solid-js/store";
import type { MessagePart, ToolPart } from "../../services/sse-types";
import {
  extractChildSubagentSessionId,
  appSettings,
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
import { createDeferredOpen, Presence } from "../motion/Presence";
import { formatElapsedSeconds, startAdaptiveElapsedTimer } from "./duration-label";

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
import {
  isSessionScratchFile,
  normalizeWorkspaceRelPath,
  resolveToolFileDiffs,
} from "./tool-card-state";
import { t as tI18n } from "../../i18n";
import { isAttentionToolPart } from "./context-activity-parts";

export {
  isTodoToolPart,
  isVisibleTextPart,
  isContextToolPart,
  isWorkPart,
  isExplorationPart,
  isAttentionToolPart,
  splitInlineThinking,
  extractVisibleAssistantText,
  expandInlineThinkingParts,
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
const MAX_ACTIVITY_GROUP_ENTRIES = 512;
const activityGroupExpansion = new Map<string, boolean>();
// Earlier steps the reader revealed, kept across session-switch remounts.
const STEP_PAGE = 40;
const activityGroupStepLimits = new Map<string, number>();

function rememberGroupValue<T>(values: Map<string, T>, key: string, value: T) {
  if (values.has(key)) values.delete(key);
  else if (values.size >= MAX_ACTIVITY_GROUP_ENTRIES) {
    const oldest = values.keys().next().value;
    if (oldest !== undefined) values.delete(oldest);
  }
  values.set(key, value);
}

export default function ContextActivityGroupView(props: {
  parts: MessagePart[];
  renderFallbackPart: (part: MessagePart, revealKey?: string, defaultOpenReasoning?: boolean) => JSX.Element;
  isLiveTail?: boolean;
  startedAt?: number;
  revealKey?: string;
  defaultOpenReasoning?: boolean;
}) {
  let rootRef: HTMLDivElement | undefined;
  const groupKey = createMemo(() => props.parts[0]?.id || "");
  // A completed reasoning-only reply can later acquire a tool part. Keep its
  // opened reasoning visible instead of retracting the final-output hint then.
  const reasoningDefault = createMemo<{ key: string; open: boolean }>((previous) => {
    const key = groupKey();
    return { key, open: Boolean(props.defaultOpenReasoning || (previous?.key === key && previous.open)) };
  });
  const resolveOpen = () => activityGroupExpansion.get(groupKey()) ?? (
    appSettings.reasoningDisplay === "expanded" ||
    (reasoningDefault().open && appSettings.reasoningDisplay !== "collapsed")
  );
  const [open, setOpen] = createSignal(resolveOpen());
  // The body mounts when it opens; render it closed for a frame so it can grow.
  const bodyOpen = createDeferredOpen(open);
  const [stepLimit, setStepLimitSignal] = createSignal(
    activityGroupStepLimits.get(groupKey()) ?? STEP_PAGE
  );
  const setStepLimit = (next: number) => {
    const key = groupKey();
    if (key) {
      if (next > STEP_PAGE) rememberGroupValue(activityGroupStepLimits, key, next);
      else activityGroupStepLimits.delete(key);
    }
    setStepLimitSignal(next);
  };
  const visibleSteps = createMemo(() => props.parts.slice(-stepLimit()));

  // Sync open state when groupKey changes across virtualized/reused turns,
  // keeping the bar compact by default so transitioning from live steps to
  // answer text never collapses a multi-hundred-pixel drawer.
  createEffect(() => {
    const key = groupKey();
    if (!key) return;
    setStepLimitSignal(activityGroupStepLimits.get(key) ?? STEP_PAGE);
    setOpen(resolveOpen());
  });

  createEffect(on(() => props.revealKey, (revealKey) => {
    if (!revealKey) return;
    const key = groupKey();
    if (key) {
      rememberGroupValue(activityGroupExpansion, key, true);
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
      rememberGroupValue(activityGroupExpansion, key, next);
    }
    setOpen(next);
  };

  const sessionID = createMemo(() => props.parts[0]?.sessionID || workspace.activeSessionID);

  const renderStepWindow = () => <>
    <Show when={props.parts.length > stepLimit()}>
      <button type="button" class="text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
        onClick={() => setStepLimit(stepLimit() + STEP_PAGE)}>
        {tI18n("timeline.loadEarlierSteps", { count: Math.min(STEP_PAGE, props.parts.length - stepLimit()) })}
      </button>
    </Show>
    <For each={visibleSteps()}>{(part) => props.renderFallbackPart(part, props.revealKey, reasoningDefault().open)}</For>
  </>;

  interface TargetBucket {
    preview: string[];
    seen: Set<string>;
  }
  const makeBucket = (): TargetBucket => ({ preview: [], seen: new Set() });
  const addBucketTarget = (bucket: TargetBucket, val: string) => {
    const clean = val.trim();
    if (!clean || bucket.seen.has(clean)) return;
    bucket.seen.add(clean);
    if (bucket.preview.length < 6) bucket.preview.push(clean);
  };
  const buildBucketTooltip = (
    header: string,
    bucket: TargetBucket,
    tailItems: string[]
  ): string => {
    let total = bucket.seen.size;
    let preview = bucket.preview;
    let tailSeen: Set<string> | undefined;
    for (const raw of tailItems) {
      const clean = raw.trim();
      if (!clean || bucket.seen.has(clean) || tailSeen?.has(clean)) continue;
      (tailSeen ||= new Set()).add(clean);
      total++;
      if (preview.length < 6) {
        if (preview === bucket.preview) preview = preview.slice();
        preview.push(clean);
      }
    }
    if (total === 0) return header;
    const lines = preview.map((item) => `• ${item}`).join("\n");
    const extra = total > 6 ? `\n… +${total - 6}` : "";
    return `${header}\n${lines}${extra}`;
  };

  const createPrefixCache = () => ({
    cursor: 0,
    firstID: "",
    lastID: "",
    reads: 0,
    searches: 0,
    edits: 0,
    commands: 0,
    subagents: 0,
    otherTools: 0,
    thoughts: 0,
    readBucket: makeBucket(),
    editBucket: makeBucket(),
    cmdBucket: makeBucket(),
    searchBucket: makeBucket(),
    subagentBucket: makeBucket(),
    diffByFile: new Map<string, { additions: number; deletions: number }>(),
    diffAdditions: 0,
    diffDeletions: 0,
    attentionTools: [] as ToolPart[],
    lastTool: undefined as ToolPart | undefined,
    start: undefined as number | undefined,
    end: undefined as number | undefined,
  });
  let prefixCache = createPrefixCache();

  const isSettledPrefixStep = (p: MessagePart): boolean => {
    if (p.type === "tool") {
      return !isTaskTool(p.tool) && (p.state.status === "completed" || p.state.status === "error");
    }
    if (p.type === "reasoning") {
      return p.time?.end != null;
    }
    return true;
  };

  const accumulatePrefixStep = (p: MessagePart) => {
    if (p.type === "reasoning") {
      prefixCache.thoughts++;
      if (p.time?.start && (!prefixCache.start || p.time.start < prefixCache.start)) {
        prefixCache.start = p.time.start;
      }
      if (p.time?.end && (!prefixCache.end || p.time.end > prefixCache.end)) {
        prefixCache.end = p.time.end;
      }
      return;
    }
    if (p.type !== "tool") return;
    prefixCache.lastTool = p;
    const target = summarizeStepTarget(p);
    if (isReadTool(p.tool)) {
      prefixCache.reads++;
      addBucketTarget(prefixCache.readBucket, target);
    } else if (isEditingTool(p.tool)) {
      prefixCache.edits++;
      addBucketTarget(prefixCache.editBucket, target);
      if (p.state.status === "completed") {
        const fileTarget = extractTargetFile(p) || "";
        for (const d of resolveToolFileDiffs(p, true, fileTarget)) {
          if (!d.file || isSessionScratchFile(normalizeWorkspaceRelPath(d.file), p.sessionID)) {
            continue;
          }
          const prev = prefixCache.diffByFile.get(d.file);
          if (prev) {
            prefixCache.diffAdditions -= prev.additions;
            prefixCache.diffDeletions -= prev.deletions;
          }
          const next = { additions: d.additions || 0, deletions: d.deletions || 0 };
          prefixCache.diffByFile.set(d.file, next);
          prefixCache.diffAdditions += next.additions;
          prefixCache.diffDeletions += next.deletions;
        }
      }
    } else if (isShellTool(p.tool)) {
      prefixCache.commands++;
      addBucketTarget(prefixCache.cmdBucket, target);
    } else if (isSearchOrListTool(p.tool)) {
      prefixCache.searches++;
      addBucketTarget(prefixCache.searchBucket, target);
    } else if (isTaskTool(p.tool)) {
      prefixCache.subagents++;
      addBucketTarget(prefixCache.subagentBucket, target);
    } else {
      prefixCache.otherTools++;
    }
    if (isAttentionToolPart(p)) {
      prefixCache.attentionTools.push(p);
    }
    if (p.state.status !== "pending") {
      const s = p.state.time?.start;
      if (s && (!prefixCache.start || s < prefixCache.start)) prefixCache.start = s;
      if (p.state.status === "completed" || p.state.status === "error") {
        const e = p.state.time?.end;
        if (e && (!prefixCache.end || e > prefixCache.end)) prefixCache.end = e;
      }
    }
  };

  const stepSummary = createMemo(() => {
    const len = props.parts.length;
    const rawParts = unwrap(props.parts);
    if (
      len < prefixCache.cursor ||
      (prefixCache.cursor > 0 &&
        (rawParts[0]?.id !== prefixCache.firstID ||
          rawParts[prefixCache.cursor - 1]?.id !== prefixCache.lastID))
    ) {
      prefixCache = createPrefixCache();
    }
    while (prefixCache.cursor < len - 1) {
      const raw = rawParts[prefixCache.cursor];
      if (!raw || !isSettledPrefixStep(raw)) break;
      if (prefixCache.cursor === 0) prefixCache.firstID = raw.id;
      accumulatePrefixStep(raw);
      prefixCache.lastID = raw.id;
      prefixCache.cursor++;
    }

    let reads = prefixCache.reads;
    let searches = prefixCache.searches;
    let edits = prefixCache.edits;
    let commands = prefixCache.commands;
    let subagents = prefixCache.subagents;
    let otherTools = prefixCache.otherTools;
    let thoughts = prefixCache.thoughts;
    const tailReads: string[] = [];
    const tailEdits: string[] = [];
    const tailCmds: string[] = [];
    const tailSearches: string[] = [];
    const tailSubagents: string[] = [];
    const runningTools: ToolPart[] = [];
    let tailDiffByFile: Map<string, { additions: number; deletions: number }> | undefined;
    let tailAttention: ToolPart[] | undefined;
    let start = props.startedAt && props.startedAt > 0 ? props.startedAt : prefixCache.start;
    if (prefixCache.start && (!start || prefixCache.start < start)) start = prefixCache.start;
    let end = prefixCache.end;
    let hasRunningPart = false;
    let activeReasoning: Extract<MessagePart, { type: "reasoning" }> | undefined;
    let lastTool = prefixCache.lastTool;

    for (let i = prefixCache.cursor; i < len; i++) {
      const p = props.parts[i];
      if (p.type === "reasoning") {
        thoughts++;
        if (p.time?.start && (!start || p.time.start < start)) start = p.time.start;
        if (p.time?.end && (!end || p.time.end > end)) end = p.time.end;
        if (p.time?.end == null) {
          activeReasoning = p;
          if (!hasRunningPart && isSessionBusy(p.sessionID)) hasRunningPart = true;
        }
      } else if (p.type === "tool") {
        lastTool = p;
        const status = p.state.status;
        if (status === "pending" || status === "running") {
          runningTools.push(p);
          if (!hasRunningPart) {
            hasRunningPart = isTaskTool(p.tool)
              ? isSubagentToolAlive(p, p.sessionID)
              : isSessionBusy(p.sessionID);
          }
        }
        const target = summarizeStepTarget(p);
        if (isReadTool(p.tool)) {
          reads++;
          if (target) tailReads.push(target);
        } else if (isEditingTool(p.tool)) {
          edits++;
          if (target) tailEdits.push(target);
          if (status === "completed") {
            const fileTarget = extractTargetFile(p) || "";
            for (const d of resolveToolFileDiffs(p, true, fileTarget)) {
              if (!d.file || isSessionScratchFile(normalizeWorkspaceRelPath(d.file), p.sessionID)) {
                continue;
              }
              (tailDiffByFile ||= new Map()).set(d.file, {
                additions: d.additions || 0,
                deletions: d.deletions || 0,
              });
            }
          }
        } else if (isShellTool(p.tool)) {
          commands++;
          if (target) tailCmds.push(target);
        } else if (isSearchOrListTool(p.tool)) {
          searches++;
          if (target) tailSearches.push(target);
        } else if (isTaskTool(p.tool)) {
          subagents++;
          if (target) tailSubagents.push(target);
        } else {
          otherTools++;
        }
        if (isAttentionToolPart(p)) {
          (tailAttention ||= prefixCache.attentionTools.slice()).push(p);
        }
        if (status !== "pending") {
          const s = p.state.time?.start;
          if (s && (!start || s < start)) start = s;
          if (status === "completed" || status === "error") {
            const e = p.state.time?.end;
            if (e && (!end || e > end)) end = e;
          }
        }
      }
    }

    const toolCount = reads + searches + edits + commands + subagents + otherTools;
    const badges: Array<{ icon: IconName; count: number; tooltip: string }> = [];
    const pushBadge = (
      icon: IconName,
      count: number,
      key: Parameters<typeof tI18n>[0],
      bucket: TargetBucket,
      tail: string[]
    ) => {
      if (count > 0) {
        badges.push({
          icon,
          count,
          tooltip: buildBucketTooltip(tI18n(key, { count }), bucket, tail),
        });
      }
    };
    pushBadge("glasses", reads, "timeline.reads", prefixCache.readBucket, tailReads);
    pushBadge("edit-small-2", edits, "timeline.editsCount", prefixCache.editBucket, tailEdits);
    pushBadge("console", commands, "timeline.commandsCount", prefixCache.cmdBucket, tailCmds);
    pushBadge("magnifying-glass", searches, "timeline.searches", prefixCache.searchBucket, tailSearches);
    pushBadge("task", subagents, "timeline.subagentsCount", prefixCache.subagentBucket, tailSubagents);

    let additions = prefixCache.diffAdditions;
    let deletions = prefixCache.diffDeletions;
    if (tailDiffByFile) {
      for (const [file, item] of tailDiffByFile) {
        const prev = prefixCache.diffByFile.get(file);
        if (prev) {
          additions -= prev.additions;
          deletions -= prev.deletions;
        }
        additions += item.additions;
        deletions += item.deletions;
      }
    }
    const diffStats = additions > 0 || deletions > 0 ? { additions, deletions } : undefined;
    const attentionTools = tailAttention ?? prefixCache.attentionTools;

    return {
      stats: { toolCount, thoughts, badges },
      runningTools,
      activeReasoning,
      lastTool,
      hasRunningPart,
      diffStats,
      attentionTools,
      timeRange: { start, end },
    };
  });

  const isRunning = createMemo(() => {
    if (props.isLiveTail) return true;
    return stepSummary().hasRunningPart;
  });

  const hasPendingApproval = createMemo(() => {
    const sid = sessionID();
    if (!sid || !isRunning()) return false;
    return workspace.pendingPermissions.some((req) => req.sessionID === sid);
  });

  const stats = () => stepSummary().stats;

  const activeStepDisplay = createMemo(() => {
    const { runningTools, activeReasoning, lastTool } = stepSummary();
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

    if (activeReasoning) {
      const heading = liveReasoningTicker(activeReasoning.text ?? "", true);
      return {
        action: tI18n("timeline.thinking"),
        detail: heading || "",
      };
    }

    // Fallback during brief transitions between steps while `isLiveTail` is true
    if (lastTool) {
      return {
        action: formatToolDisplayName(lastTool.tool, lastTool),
        detail: summarizeStepTarget(lastTool),
      };
    }

    return {
      action: tI18n("timeline.executing"),
      detail: "",
    };
  }, undefined, {
    equals: (a, b) => a.action === b.action && a.detail === b.detail,
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

  const groupDiffStats = () => stepSummary().diffStats;

  const attentionTools = () => stepSummary().attentionTools;

  const timeRange = () => stepSummary().timeRange;

  const [now, setNow] = createSignal(Date.now());
  createEffect(() => {
    if (!isRunning() || stats().toolCount === 0) return;
    const stop = startAdaptiveElapsedTimer(
      () => timeRange().start,
      (currentNow) => setNow(currentNow)
    );
    onCleanup(stop);
  });

  const durationLabel = createMemo(() => {
    const { start, end } = timeRange();
    if (!start) return "";
    const endAt = isRunning() ? now() : end;
    if (!endAt || endAt <= start) return "";
    const sec = (endAt - start) / 1000;
    if (sec < 0.05) return "";
    return formatElapsedSeconds(sec);
  });

  return (
      <div ref={rootRef} data-activity-group={groupKey()}>
        <button
          type="button"
          onClick={toggleOpen}
          aria-expanded={open()}
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
        <Presence when={open()} exitMs={220}>
          {() => (
          <div
            data-activity-group-body
            class={`grid transition-[grid-template-rows,opacity,margin-top] duration-200 ease-out motion-reduce:transition-none ${
              bodyOpen()
                ? "mt-1.5 grid-rows-[1fr] opacity-100"
                : "mt-0 grid-rows-[0fr] opacity-0 pointer-events-none"
            }`}
            aria-hidden={!open()}
            inert={!open()}
          >
            <div class="min-h-0 overflow-hidden">
              <div class="ml-2 border-l-2 border-[var(--border-subtle)] pl-3.5 space-y-1">
                {renderStepWindow()}
              </div>
            </div>
          </div>
          )}
        </Presence>
      </div>
  );
}
