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
import { formatToolDisplayName } from "./tool-card-helpers";
import { t as tI18n } from "../../i18n";
import {
  computeActivityStepSummary,
  createPrefixCache,
  extractThoughtOnlyPreview,
  resolveActiveStepDisplay,
  summarizeStepTarget,
  type StepDisplay,
} from "./context-activity-summary";

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
  const groupKey = createMemo(() => (props.parts[0]?.id || "").replace(/#thinking$/, ""));
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

  const prefixCacheRef = { current: createPrefixCache() };
  const stepSummary = createMemo(() =>
    computeActivityStepSummary(props.parts, props.startedAt, prefixCacheRef)
  );

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
                  <TextReveal text={activeStepDisplay().detail} truncate minSwapMs={LIVE_STATUS_SWAP_MS} />
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
