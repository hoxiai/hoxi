// web/src/components/timeline/turn-window.tsx
import {
  type Accessor,
  createEffect,
  createMemo,
  createSignal,
  Index,
  Show,
} from "solid-js";
import type { MessageInfo } from "../../services/sse-types";
import type { MessageTurn } from "../../store/workspace";
import { Icon } from "../icons";
import { t } from "../../i18n";
import {
  AssistantErrorCard,
  AssistantTurnFooter,
  ContextActivityGroupView,
  groupAssistantParts,
  PartView,
  resolveMergedTurnParts,
  stabilizeTurnRenderItems,
  type TurnRenderItem,
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
  // Per-session window size. Turns appended while a session is open widen the
  // window instead of sliding it: unmounting the oldest turn on every new step
  // shifted everything above the reader and re-anchored activity groups.
  const windows = new Map<string, { limit: number; seen: number }>();
  const [revision, setRevision] = createSignal(0);
  let prependingLock = false;

  const windowFor = (sid: string | null) => {
    const key = sid ?? "";
    let state = windows.get(key);
    if (!state) {
      if (windows.size >= 64) {
        const oldest = windows.keys().next().value;
        if (oldest !== undefined) windows.delete(oldest);
      }
      state = { limit: INITIAL_VISIBLE_TURNS, seen: 0 };
      windows.set(key, state);
    }
    return state;
  };

  const visibleLimit = createMemo(() => {
    revision();
    const total = turns().length;
    const state = windowFor(sessionID());
    if (state.seen > 0 && total > state.seen) state.limit += total - state.seen;
    state.seen = total;
    return state.limit;
  });

  const setVisibleLimit = (next: number) => {
    windowFor(sessionID()).limit = next;
    setRevision((value) => value + 1);
  };

  createEffect(() => {
    sessionID();
    prependingLock = false;
  });

  // Never cut a prompt cycle in half: folded step turns render nothing, so a
  // window starting mid-cycle showed a bare activity group without its prompt.
  const startIndex = createMemo(() => {
    const list = turns();
    let start = Math.max(0, list.length - visibleLimit());
    while (start > 0 && list[start]?.info.role === "assistant") start--;
    return start;
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
      setVisibleLimit(visibleLimit() + Math.max(count, TURN_BATCH_SIZE));
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
    if (needed > visibleLimit()) setVisibleLimit(needed);
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

/**
 * Renders an assistant turn while aggregating contiguous tool/reasoning steps
 * across multi-turn OpenCode assistant cycles into a single collapsible
 * `<ContextActivityGroupView />`, keeping the main stream focused on user-facing
 * responses and final file change summaries.
 */
export function AssistantTurnItem(props: {
  turn: MessageTurn;
  globalIndex: number;
  allTurns: MessageTurn[];
  windowStartIndex?: number;
  isSessionBusy: boolean;
  activeMatchTurnID?: string | null;
  navigationRevision?: number;
  isHighlighted: boolean;
  cycleStartedAt?: number;
}) {
  const merged = createMemo(() =>
    resolveMergedTurnParts(
      props.allTurns,
      props.globalIndex,
      props.windowStartIndex ?? 0
    )
  );

  const groupedItems = createMemo<TurnRenderItem[]>((prev) =>
    stabilizeTurnRenderItems(prev, groupAssistantParts(merged().effectiveParts))
  );

  const isHighlighted = createMemo(() => {
    if (props.isHighlighted) return true;
    const matchID = props.activeMatchTurnID;
    if (!matchID) return false;
    return merged().carriedTurnIDs.includes(matchID);
  });

  const revealPartIDs = createMemo(() => new Set(
    props.allTurns.find((turn) => turn.info.id === props.activeMatchTurnID)?.parts.map((part) => part.id) ?? []
  ));

  return (
    <Show
      when={!merged().isVisuallyEmpty || merged().isCycleEnd || Boolean(props.turn.info.error)}
      fallback={
        <div
          data-merged-turn-id={props.turn.info.id}
          class="h-0 overflow-hidden pointer-events-none"
        />
      }
    >
      <div
        data-turn-id={props.turn.info.id}
        style={
          props.globalIndex < props.allTurns.length - 4 && !isHighlighted()
            ? { "content-visibility": "auto", "contain-intrinsic-size": "auto 140px" }
            : undefined
        }
        class={`flex flex-col gap-y-2.5 px-1 rounded-xl transition-colors ${
          merged().isVisuallyEmpty && !props.turn.info.error
            ? "mt-0"
            : props.globalIndex === 0
              ? "mt-0"
                : merged().isFirstVisualTurnInCycle
                  ? "mt-4"
                  : "mt-2.5"
        } ${
          isHighlighted()
            ? "ring-2 ring-[var(--accent-primary)] bg-[var(--accent-surface)]/20 p-2"
            : ""
        }`}
      >
        <Index each={groupedItems()}>
          {(item, itemIdx) => {
            const isTailActivityGroup = () =>
              props.isSessionBusy &&
              merged().ownsLiveTail &&
              item().kind === "activity-group" &&
              itemIdx === groupedItems().length - 1;
            return (
              <Show
                when={item().kind === "activity-group"}
                fallback={
                  <Show
                    when={
                      item().kind === "single"
                        ? (item() as Extract<ReturnType<typeof item>, { kind: "single" }>).part
                        : undefined
                    }
                  >
                    {(part) => <PartView part={part()} />}
                  </Show>
                }
              >
                <ContextActivityGroupView
                  revealKey={item().kind === "activity-group" &&
                    (item() as { parts: MessageTurn["parts"] }).parts.some((part) => revealPartIDs().has(part.id))
                    ? `${props.activeMatchTurnID}:${props.navigationRevision ?? 0}` : undefined}
                  parts={
                    item().kind === "activity-group"
                      ? (item() as Extract<ReturnType<typeof item>, { kind: "activity-group" }>)
                          .parts
                      : []
                  }
                  isLiveTail={isTailActivityGroup()}
                  startedAt={isTailActivityGroup() ? props.cycleStartedAt : undefined}
                />
              </Show>
            );
          }}
        </Index>

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
