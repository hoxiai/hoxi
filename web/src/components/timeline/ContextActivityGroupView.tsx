// web/src/components/timeline/ContextActivityGroupView.tsx
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  on,
  onCleanup,
  Show,
  untrack,
  type JSX,
} from "solid-js";
import type { MessagePart } from "../../services/sse-types";
import {
  appSettings,
  workspace,
} from "../../store/workspace";
import {
  DiffChanges,
  Icon,
  LIVE_STATUS_SWAP_MS,
  TextReveal,
  TextShimmer,
} from "../icons";
import HoxiActivityLogo from "./HoxiActivityLogo";
import { createDeferredOpen, Presence } from "../motion/Presence";
import { formatElapsedSeconds, startAdaptiveElapsedTimer } from "./duration-label";
import { isFollowingLiveTail } from "./timeline-scroll";
import { t as tI18n } from "../../i18n";
import {
  computeActivityStepSummary,
  createPrefixCache,
  extractThoughtOnlyPreview,
  resolveActiveStepDisplay,
  type StepDisplay,
} from "./context-activity-summary";

import {
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
  isTodoToolPart,
  isVisibleTextPart,
  isContextToolPart,
  isWorkPart,
  isExplorationPart,
  isAttentionToolPart,
  splitInlineThinking,
  extractVisibleAssistantText,
  expandInlineThinkingParts,
};
export {
  groupAssistantParts,
  stabilizeTurnRenderItems,
  resolveMergedTurnParts,
  resolveTurnNavigationTarget,
  hasLiveTailWorkGroup,
  hasLiveTailRenderedOutput,
} from "./context-activity-turns";
export type { TurnRenderItem, MergedTurnResolution } from "./context-activity-turns";

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
  isActiveGroup?: boolean;
  startedAt?: number;
  revealKey?: string;
  defaultOpenReasoning?: boolean;
}) {
  let rootRef: HTMLDivElement | undefined;
  let trackedOpenGroupKey = "";
  const groupKey = createMemo(() => (props.parts[0]?.id || "").replace(/#thinking$/, ""));

  const prefixCacheRef = { current: createPrefixCache() };
  const stepSummary = createMemo(() =>
    computeActivityStepSummary(props.parts, props.startedAt, prefixCacheRef)
  );

  const isRunning = createMemo(() => {
    if (props.isLiveTail) return true;
    return stepSummary().hasRunningPart;
  });

  const stats = () => stepSummary().stats;
  const isActiveGroup = () => props.isActiveGroup ?? true;

  // Level-1 activity group is expanded while it belongs to the active task,
  // and collapses once a new task starts (unless manually toggled or globally set).
  const resolveOpen = () => {
    const key = groupKey();
    const manual = activityGroupExpansion.get(key);
    if (manual !== undefined) return manual;
    if (appSettings.reasoningDisplay === "collapsed") return false;
    if (appSettings.reasoningDisplay === "expanded") return true;
    return isActiveGroup();
  };

  const [open, setOpen] = createSignal(resolveOpen());
  const [animateToggle, setAnimateToggle] = createSignal(false);
  // The body mounts when it opens; render it closed for a frame only on interactive toggles.
  const bodyOpen = createDeferredOpen(open, animateToggle);
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
  const displayParts = createMemo(() => {
    const raw = props.parts;
    if (props.revealKey || raw.length === 0) return raw;
    const hideReasoning =
      stats().toolCount > 0 &&
      appSettings.reasoningDisplay === "auto-collapse" &&
      !props.defaultOpenReasoning;
    const filtered = raw.filter((part) => {
      if (isTodoToolPart(part)) return false;
      if (part.type === "text" && stats().toolCount > 0) return false;
      if (hideReasoning && part.type === "reasoning") return false;
      return true;
    });
    return filtered.length > 0 ? filtered : raw;
  });
  const visibleSteps = createMemo(() => displayParts().slice(-stepLimit()));

  // Sync open state across virtualized/reused turns, new-task auto-collapse, or setting changes.
  createEffect(() => {
    const key = groupKey();
    if (!key) return;
    const nextOpen = resolveOpen();
    const prevKey = trackedOpenGroupKey;
    const prevOpen = untrack(open);
    trackedOpenGroupKey = key;
    setStepLimitSignal(activityGroupStepLimits.get(key) ?? STEP_PAGE);

    if (prevKey === key && prevOpen && !nextOpen && rootRef) {
      const scroller = rootRef.closest(".timeline-result-scroll");
      // While the transcript follows the live tail its pin keeps the reader at
      // the bottom; anchoring this older group would yank them back up to it.
      if (scroller instanceof HTMLElement && !isFollowingLiveTail(scroller)) {
        const groupRect = rootRef.getBoundingClientRect();
        const scrollerRect = scroller.getBoundingClientRect();
        // Entirely above the viewport: collapse in 0ms and compensate scrollTop
        // so the visible viewport does not shift by even 1px.
        if (groupRect.bottom <= scrollerRect.top + 4) {
          const beforeHeight = rootRef.offsetHeight;
          const beforeScrollTop = scroller.scrollTop;
          setAnimateToggle(false);
          setOpen(false);
          queueMicrotask(() => {
            // A send resumed following meanwhile; the tail pin owns the viewport now.
            if (!rootRef || isFollowingLiveTail(scroller)) return;
            const delta = beforeHeight - rootRef.offsetHeight;
            if (delta > 0) {
              scroller.scrollTop = Math.max(0, beforeScrollTop - delta);
            }
          });
          return;
        }
        // Partially scrolled past the top (sticky header pinned): anchor the header at top.
        if (groupRect.top < scrollerRect.top) {
          scroller.scrollTop = Math.max(
            0,
            scroller.scrollTop + (groupRect.top - scrollerRect.top - 8)
          );
        }
      }
      setAnimateToggle(true);
      setOpen(false);
      return;
    }

    if (prevKey === key && !prevOpen && nextOpen) {
      setAnimateToggle(true);
      setOpen(true);
      return;
    }

    setAnimateToggle(false);
    setOpen(nextOpen);
  });

  createEffect(on(() => props.revealKey, (revealKey) => {
    if (!revealKey) return;
    const key = groupKey();
    if (key) {
      rememberGroupValue(activityGroupExpansion, key, true);
    }
    setAnimateToggle(true);
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
          scroller.scrollTop = Math.max(0, scroller.scrollTop + (groupTop - scrollerTop - 8));
        }
      }
    }
    if (key) {
      rememberGroupValue(activityGroupExpansion, key, next);
    }
    setAnimateToggle(true);
    setOpen(next);
  };

  const sessionID = createMemo(() => props.parts[0]?.sessionID || workspace.activeSessionID);

  let trackedGroupKey = "";
  const seenStepIDs = new Set<string>();
  const syncSeenGroupSteps = () => {
    const key = groupKey();
    if (key === trackedGroupKey) return;
    trackedGroupKey = key;
    seenStepIDs.clear();
    for (const part of props.parts) {
      if (part?.id) seenStepIDs.add(part.id);
    }
  };
  syncSeenGroupSteps();

  const renderStepItem = (part: MessagePart) => {
    syncSeenGroupSteps();
    const id = part?.id || "";
    const isFirstToolReplacingInitialStep =
      part.type === "tool" &&
      stats().toolCount <= 1 &&
      props.parts.length >= 2 &&
      props.parts[0]?.type !== "tool";
    const isAppendedLive = Boolean(
      id && !seenStepIDs.has(id) && isRunning() && !isFirstToolReplacingInitialStep
    );
    const isInitialLiveSingle = Boolean(
      (!isAppendedLive && props.isLiveTail && props.parts.length === 1) ||
        isFirstToolReplacingInitialStep
    );
    if (id) seenStepIDs.add(id);

    if (!isAppendedLive) {
      return (
        <div class={isInitialLiveSingle ? "hoxi-step-enter" : undefined}>
          {props.renderFallbackPart(part, props.revealKey, false)}
        </div>
      );
    }

    const [unfolded, setUnfolded] = createSignal(false);
    if (typeof requestAnimationFrame === "function") {
      const frame = requestAnimationFrame(() => setUnfolded(true));
      onCleanup(() => cancelAnimationFrame(frame));
    } else {
      setUnfolded(true);
    }

    return (
      <div class="hoxi-step-grid" data-open={unfolded() ? "true" : "false"}>
        <div class="-mx-2 px-2 min-h-0 overflow-hidden hoxi-step-enter">
          {props.renderFallbackPart(part, props.revealKey, false)}
        </div>
      </div>
    );
  };

  const renderStepWindow = () => <>
    <Show when={displayParts().length > stepLimit()}>
      <button type="button" class="text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
        onClick={() => setStepLimit(stepLimit() + STEP_PAGE)}>
        {tI18n("timeline.loadEarlierSteps", { count: Math.min(STEP_PAGE, displayParts().length - stepLimit()) })}
      </button>
    </Show>
    <For each={visibleSteps()}>
      {(part) => renderStepItem(part)}
    </For>
  </>;

  const hasPendingApproval = createMemo(() => {
    const sid = sessionID();
    if (!sid || !isRunning()) return false;
    return workspace.pendingPermissions.some((req) => req.sessionID === sid);
  });

  const isInterStepGap = createMemo(
    () =>
      isRunning() &&
      stepSummary().runningTools.length === 0 &&
      !hasPendingApproval() &&
      stats().toolCount > 0
  );
  const [awaitingNextStep, setAwaitingNextStep] = createSignal(false);
  createEffect(() => {
    if (!isInterStepGap()) {
      setAwaitingNextStep(false);
      return;
    }
    if (stepSummary().activeReasoning) {
      setAwaitingNextStep(true);
      return;
    }
    const timer = setTimeout(() => setAwaitingNextStep(true), 220);
    onCleanup(() => clearTimeout(timer));
  });

  const activeStepDisplay = createMemo<StepDisplay, StepDisplay | undefined>(
    (previous) => resolveActiveStepDisplay(stepSummary(), previous),
    undefined,
    { equals: (a, b) => a.action === b.action && a.detail === b.detail }
  );

  const targetPreview = createMemo(() =>
    extractThoughtOnlyPreview(props.parts, stats().toolCount)
  );

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
          class={`group/activity flex w-full items-center justify-between gap-2 rounded-md -mx-2 px-2 py-1.5 text-left text-[13px] transition-colors cursor-pointer select-none ${
            open()
              ? "sticky top-0 z-10 backdrop-blur-md bg-[var(--bg-app)]/90 hover:bg-[var(--bg-hover)] text-[var(--text-primary)]"
              : isRunning()
                ? "hover:bg-[var(--bg-hover)] text-[var(--text-primary)]"
                : "hover:bg-[var(--bg-hover)] text-[var(--text-muted)] hover:text-[var(--text-primary)]"
          }`}
        >
          <div class="flex items-center gap-2 min-w-0 flex-1">
            <Show
              when={isRunning()}
              fallback={
                <span class="inline-grid place-items-center shrink-0">
                  <span class="[grid-area:1/1] inline-flex items-center text-[var(--text-faint)] transition-opacity duration-150 group-hover/activity:opacity-0">
                    <Icon name="circle-check" size="small" />
                  </span>
                  <span class="[grid-area:1/1] inline-flex items-center text-[var(--text-secondary)] opacity-0 transition-opacity duration-150 group-hover/activity:opacity-100">
                    <Icon
                      name={stats().toolCount > 0 ? "layers" : "brain"}
                      size="small"
                    />
                  </span>
                </span>
              }
            >
              <span class="-my-1 text-[var(--accent-primary)] inline-flex items-center shrink-0">
                <HoxiActivityLogo active={!hasPendingApproval()} />
              </span>
            </Show>

            <Show
              when={isRunning()}
              fallback={
                <>
                  <span class="font-normal text-[var(--text-faint)] group-hover/activity:text-[var(--text-secondary)] transition-colors shrink-0">
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
              <Show
                when={!open()}
                fallback={
                  <span class="inline-flex items-center gap-1.5 font-medium text-[var(--accent-primary)] shrink-0">
                    <TextShimmer
                      text={
                        stats().toolCount > 0
                          ? tI18n("timeline.executedStepsSummary", {
                              count: stats().toolCount,
                            })
                          : tI18n("timeline.thinking")
                      }
                      active={!stepSummary().hasRunningPart && !hasPendingApproval()}
                    />
                    <Show when={awaitingNextStep()}>
                      <span class="inline-flex items-center gap-1.5 text-[var(--text-muted)] font-normal hoxi-step-enter">
                        <span class="text-[var(--text-faint)]">·</span>
                        <TextShimmer text={tI18n("timeline.thinking")} active={true} />
                      </span>
                    </Show>
                  </span>
                }
              >
                <span class="font-medium text-[var(--accent-primary)] shrink-0">
                  <TextShimmer text={activeStepDisplay().action} active={!hasPendingApproval()} />
                </span>
                <Show when={activeStepDisplay().detail}>
                  <span class="font-mono text-[12px] text-[var(--text-secondary)] truncate max-w-[360px] inline-flex items-center min-w-0">
                    <TextReveal text={activeStepDisplay().detail} truncate minSwapMs={LIVE_STATUS_SWAP_MS} />
                  </span>
                </Show>
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
              {(diffStats) => (
                <span class={isRunning() ? "" : "opacity-70 group-hover/activity:opacity-100 transition-opacity"}>
                  <DiffChanges changes={diffStats()} />
                </span>
              )}
            </Show>

            <Show when={durationLabel()}>
              <span
                class={`font-mono text-[12px] tabular-nums text-[var(--text-faint)] transition-opacity ${
                  isRunning() ? "" : "opacity-0 group-hover/activity:opacity-100"
                }`}
              >
                {durationLabel()}
              </span>
            </Show>

            <span
              class={`hoxi-chevron text-[var(--text-faint)] transition-opacity ${
                isRunning() || open() ? "" : "opacity-0 group-hover/activity:opacity-100"
              }`}
              data-open={open() ? "true" : "false"}
            >
              <Icon name="chevron-right" size="small" />
            </span>
          </div>
        </button>

        {/* Full chronological step list with smooth collapse transition */}
        <Presence when={open()} exitMs={animateToggle() ? 180 : 0}>
          {() => (
          <div
            data-activity-group-body
            class={`grid ${
              animateToggle()
                ? "transition-[grid-template-rows,opacity,margin-top] duration-150 ease-out motion-reduce:transition-none"
                : "transition-none"
            } ${
              bodyOpen()
                ? "mt-1 grid-rows-[1fr] opacity-100"
                : "mt-0 grid-rows-[0fr] opacity-0 pointer-events-none"
            }`}
            aria-hidden={!open()}
            inert={!open()}
          >
            <div class="min-h-0 overflow-hidden">
              <div class="ml-[7.5px] border-l border-[var(--border-subtle)]/50 pl-4 space-y-0.5">
                {renderStepWindow()}
              </div>
            </div>
          </div>
          )}
        </Presence>
      </div>
  );
}
