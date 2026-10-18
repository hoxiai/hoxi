// web/src/components/timeline/turn-window.tsx
import {
  type Accessor,
  createEffect,
  createMemo,
  createSignal,
  Index,
  Show,
} from "solid-js";
import type { MessageInfo, MessagePart } from "../../services/sse-types";
import type { MessageTurn } from "../../store/workspace";
import { Icon } from "../icons";
import { t } from "../../i18n";
import {
  AssistantErrorCard,
  AssistantTurnFooter,
  ContextActivityGroupView,
  groupAssistantParts,
  resolveMergedTurnParts,
  isTodoTool,
  PartView,
} from "./message-stream-parts";

export const INITIAL_VISIBLE_TURNS = 10;
export const TURN_BATCH_SIZE = 12;

export function filterVisibleTurnsByRevert(
  rawTurns: MessageTurn[],
  revert?: { messageID: string; partID?: string }
): MessageTurn[] {
  if (!revert?.messageID) return rawTurns;
  const idx = rawTurns.findIndex((t) => t.info.id === revert.messageID);
  if (idx >= 0) {
    if (!revert.partID) {
      return rawTurns.slice(0, idx);
    }
    const boundaryTurn = rawTurns[idx];
    const pIdx = boundaryTurn.parts.findIndex((p) => p.id === revert.partID);
    const slicedParts =
      pIdx >= 0 ? boundaryTurn.parts.slice(0, pIdx) : boundaryTurn.parts;
    return [
      ...rawTurns.slice(0, idx),
      { info: boundaryTurn.info, parts: slicedParts },
    ];
  }
  return rawTurns.filter((t) => t.info.id < revert.messageID);
}

export interface TurnWindowController {
  visibleTurns: Accessor<MessageTurn[]>;
  hiddenCount: Accessor<number>;
  startIndex: Accessor<number>;
  loadEarlierBatch: (scroller?: HTMLElement, count?: number) => void;
  loadAllEarlier: (scroller?: HTMLElement) => void;
  ensureTurnVisible: (messageID: string) => void;
  handleTopScroll: (scroller: HTMLElement) => void;
}

/**
 * Ported from Kilo Desktop / OpenCode Desktop timeline windowing (`turnInit` + `turnBatch`):
 * Keeps only the latest N turns mounted initially and prepends older turns on scroll-up
 * while preserving exact scroll offset (`scrollTop += newScrollHeight - prevScrollHeight`).
 */
export function createTurnWindow(
  sessionID: Accessor<string | null>,
  turns: Accessor<MessageTurn[]>
): TurnWindowController {
  const [visibleLimit, setVisibleLimit] = createSignal(INITIAL_VISIBLE_TURNS);
  let lastSessionID: string | null = null;
  let prependingLock = false;

  createEffect(() => {
    const sid = sessionID();
    if (sid !== lastSessionID) {
      lastSessionID = sid;
      setVisibleLimit(INITIAL_VISIBLE_TURNS);
      prependingLock = false;
    }
  });

  const startIndex = createMemo(() => {
    const total = turns().length;
    const limit = visibleLimit();
    return Math.max(0, total - limit);
  });

  const hiddenCount = createMemo(() => startIndex());

  const visibleTurns = createMemo(() => {
    const list = turns();
    const start = startIndex();
    return start > 0 ? list.slice(start) : list;
  });

  const preserveScrollOnPrepend = (scroller: HTMLElement | undefined, fn: () => void) => {
    if (!scroller) {
      fn();
      return;
    }
    const prevScrollHeight = scroller.scrollHeight;
    const prevScrollTop = scroller.scrollTop;
    fn();
    queueMicrotask(() => {
      const delta = scroller.scrollHeight - prevScrollHeight;
      if (delta > 0) {
        scroller.scrollTop = prevScrollTop + delta;
      }
      requestAnimationFrame(() => {
        prependingLock = false;
      });
    });
  };

  const loadEarlierBatch = (scroller?: HTMLElement, count = TURN_BATCH_SIZE) => {
    const hidden = hiddenCount();
    if (hidden <= 0) return;
    prependingLock = true;
    preserveScrollOnPrepend(scroller, () => {
      setVisibleLimit((prev) => prev + Math.max(count, TURN_BATCH_SIZE));
    });
  };

  const loadAllEarlier = (scroller?: HTMLElement) => {
    const total = turns().length;
    if (hiddenCount() <= 0) return;
    prependingLock = true;
    preserveScrollOnPrepend(scroller, () => {
      setVisibleLimit(total + INITIAL_VISIBLE_TURNS);
    });
  };

  const ensureTurnVisible = (messageID: string) => {
    const list = turns();
    const idx = list.findIndex((t) => t.info.id === messageID);
    if (idx < 0) return;
    const needed = list.length - idx + 2;
    if (needed > visibleLimit()) {
      setVisibleLimit(needed);
    }
  };

  const handleTopScroll = (scroller: HTMLElement) => {
    if (prependingLock || hiddenCount() <= 0) return;
    if (scroller.scrollTop <= 120) {
      loadEarlierBatch(scroller, TURN_BATCH_SIZE);
    }
  };

  return {
    visibleTurns,
    hiddenCount,
    startIndex,
    loadEarlierBatch,
    loadAllEarlier,
    ensureTurnVisible,
    handleTopScroll,
  };
}

/**
 * Ported from Kilo Desktop `J9` (`thread-BuiFWozN.js`):
 * Enables CSS `content-visibility: auto` on turns older than the latest 3 turns
 * so WebKit skips layout/paint for off-viewport history.
 */
export function turnContentVisibilityClass(
  globalIndex: number,
  totalTurns: number
): string {
  if (globalIndex < totalTurns - 3) {
    return "[content-visibility:auto] [contain-intrinsic-size:auto_180px]";
  }
  return "";
}

export function EarlierTurnsBanner(props: {
  hiddenCount: number;
  totalCount: number;
  onLoadBatch: () => void;
  onLoadAll: () => void;
}) {
  const nextBatchCount = () => Math.min(props.hiddenCount, TURN_BATCH_SIZE);

  return (
    <Show when={props.hiddenCount > 0}>
      <div class="mb-5 flex flex-col items-center justify-center gap-1.5 rounded-xl border border-dashed border-[var(--border-subtle)] bg-[var(--bg-inset)]/50 px-4 py-2.5 text-center select-none">
        <div class="text-[11.5px] text-[var(--text-faint)]">
          {t("timeline.hiddenTurnsHint", { count: props.hiddenCount })}
        </div>
        <div class="flex items-center gap-2">
          <button
            type="button"
            onClick={props.onLoadBatch}
            class="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-elevated)] hover:bg-[var(--bg-hover)] hover:border-[var(--border-strong)] px-3 py-1 text-[12px] font-medium text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Icon name="arrow-up" size="small" class="text-[var(--accent-primary)]" />
            <span>{t("timeline.loadEarlierTurns", { count: nextBatchCount() })}</span>
          </button>

          <Show when={props.hiddenCount > TURN_BATCH_SIZE}>
            <button
              type="button"
              onClick={props.onLoadAll}
              class="inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-[11.5px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            >
              <span>{t("timeline.loadAllEarlierTurns", { total: props.totalCount })}</span>
            </button>
          </Show>
        </div>
      </div>
    </Show>
  );
}

// Persistent manual expand state for historical assistant turn steps
const expandedHistoricalTurnIDs = new Set<string>();

/**
 * Ported from Kilo Desktop `group-turn-work` (`$We` in `thread-BuiFWozN.js`):
 * For older completed assistant turns (`globalIndex < totalTurns - 2`), folds intermediate
 * tool cards and reasoning blocks into a lightweight summary toggle while keeping final text
 * visible. Prevents hundreds of historical ToolCards & SubagentInlineTimelines from bogging down DOM.
 */
export function AssistantTurnItem(props: {
  turn: MessageTurn;
  globalIndex: number;
  allTurns: MessageTurn[];
  isSessionBusy: boolean;
  isHighlighted: boolean;
}) {
  const merged = createMemo(() => resolveMergedTurnParts(props.allTurns, props.globalIndex));
  const effectiveParts = () => merged().effectiveParts;

  const [stepsExpanded, setStepsExpanded] = createSignal(
    expandedHistoricalTurnIDs.has(props.turn.info.id)
  );

  const toggleSteps = () => {
    const id = props.turn.info.id;
    const next = !stepsExpanded();
    if (next) expandedHistoricalTurnIDs.add(id);
    else expandedHistoricalTurnIDs.delete(id);
    setStepsExpanded(next);
  };

  const isInLatestUserCycle = createMemo(() => {
    const list = props.allTurns;
    for (let i = list.length - 1; i >= 0; i--) {
      if (list[i]?.info.role === "user") {
        return props.globalIndex > i;
      }
    }
    return true;
  });

  const isRecentTurn = createMemo(
    () => isInLatestUserCycle() || props.globalIndex >= props.allTurns.length - 2
  );

  const workParts = createMemo(() =>
    effectiveParts().filter(
      (p) => p.type === "reasoning" || (p.type === "tool" && !isTodoTool(p))
    )
  );

  const textParts = createMemo(() =>
    effectiveParts().filter(
      (p): p is Extract<MessagePart, { type: "text" }> =>
        p.type === "text" && p.text.trim().length > 0
    )
  );

  const hasRunningWork = createMemo(() =>
    workParts().some((p) => {
      if (p.type === "reasoning") return p.time?.end == null;
      if (p.type === "tool") {
        return p.state.status === "pending" || p.state.status === "running";
      }
      return false;
    })
  );

  // Fold intermediate tool/reasoning work only on historical turns from prior user prompt cycles
  const shouldFoldWork = createMemo(
    () =>
      !isRecentTurn() &&
      !hasRunningWork() &&
      workParts().length > 0
  );

  const groupedAll = createMemo(() => groupAssistantParts(effectiveParts()));
  const groupedWorkOnly = createMemo(() => groupAssistantParts(workParts()));

  const prevRole = () =>
    props.globalIndex > 0 ? props.allTurns[props.globalIndex - 1]?.info.role : undefined;

  return (
    <Show when={!merged().isVisuallyEmpty || merged().isCycleEnd || Boolean(props.turn.info.error)}>
    <div
      data-turn-id={props.turn.info.id}
      class={`flex flex-col gap-y-3.5 px-1 rounded-xl transition-colors ${turnContentVisibilityClass(
        props.globalIndex,
        props.allTurns.length
      )} ${
        props.globalIndex === 0
          ? "mt-0"
          : prevRole() === "assistant"
            ? "mt-3.5"
            : "mt-6"
      } ${
        props.isHighlighted
          ? "ring-2 ring-[var(--accent-primary)] bg-[var(--accent-surface)]/20 p-2"
          : ""
      }`}
    >
      <Show
        when={shouldFoldWork()}
        fallback={
          <Index each={groupedAll()}>
            {(item, itemIndex) => (
              <Show
                when={item().kind === "activity-group"}
                fallback={
                  <Show
                    when={
                      item().kind === "single"
                        ? (item() as Extract< ReturnType<typeof item>, { kind: "single" } >).part
                        : undefined
                    }
                  >
                    {(part) => <PartView part={part()} />}
                  </Show>
                }
              >
                <ContextActivityGroupView
                  isLiveTail={props.isSessionBusy && props.globalIndex === props.allTurns.length - 1 && itemIndex === groupedAll().length - 1 && !props.turn.info.error}
                  parts={
                    item().kind === "activity-group"
                      ? (item() as Extract< ReturnType<typeof item>, { kind: "activity-group" } >)
                          .parts
                      : []
                  }
                />
              </Show>
            )}
          </Index>
        }
      >
        {/* Compact folded work summary bar for historical turns */}
        <div>
          <button
            type="button"
            onClick={toggleSteps}
            class="inline-flex items-center gap-2 rounded-xl border border-transparent hover:border-[var(--border-subtle)] hover:bg-[var(--bg-inset)] px-2.5 py-1 text-[12.5px] text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Icon name="layers" size="small" class="text-[var(--text-faint)]" />
            <span class="font-medium text-[var(--text-secondary)]">
              {t("timeline.completedWorkSummary", { count: workParts().length })}
            </span>
            <span class="text-[11.5px] text-[var(--text-faint)]">
              · {stepsExpanded() ? t("timeline.collapseTurnSteps") : t("timeline.expandTurnSteps")}
            </span>
            <Icon
              name={stepsExpanded() ? "chevron-down" : "chevron-right"}
              size="small"
              class="text-[var(--text-faint)]"
            />
          </button>

          <Show when={stepsExpanded()}>
            <div class="mt-2 ml-2 border-l-2 border-[var(--border-subtle)] pl-3.5 flex flex-col gap-y-3">
              <Index each={groupedWorkOnly()}>
                {(item) => (
                  <Show
                    when={item().kind === "activity-group"}
                    fallback={
                      <Show
                        when={
                          item().kind === "single"
                            ? (
                                item() as Extract<
                                  ReturnType<typeof item>,
                                  { kind: "single" }
                                >
                              ).part
                            : undefined
                        }
                      >
                        {(part) => <PartView part={part()} />}
                      </Show>
                    }
                  >
                    <ContextActivityGroupView
                      parts={
                        item().kind === "activity-group"
                          ? (
                              item() as Extract<
                                ReturnType<typeof item>,
                                { kind: "activity-group" }
                              >
                            ).parts
                          : []
                      }
                    />
                  </Show>
                )}
              </Index>
            </div>
          </Show>
        </div>

        {/* Always keep any final assistant text visible even when work steps are folded */}
        <Index each={textParts()}>
          {(part) => <PartView part={part()} />}
        </Index>
      </Show>

      <AssistantErrorCard info={props.turn.info as MessageInfo} />
      <AssistantTurnFooter
        info={props.turn.info}
        parts={props.turn.parts}
        turns={props.allTurns}
        turnIndex={props.globalIndex}
        isSessionBusy={props.isSessionBusy}
      />
    </div>
    </Show>
  );
}
