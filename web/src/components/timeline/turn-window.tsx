// web/src/components/timeline/turn-window.tsx
import {
  type Accessor,
  createEffect,
  createMemo,
  createSignal,
  Index,
  onCleanup,
  Show,
} from "solid-js";
import type { MessageInfo } from "../../services/sse-types";
import type { MessageTurn } from "../../store/workspace";
import { Icon } from "../icons";
import { t } from "../../i18n";
import { isVisibleTextPart, isWorkPart } from "./context-activity-parts";
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

export const INITIAL_VISIBLE_TURNS = 12;
export const TURN_BATCH_SIZE = 12;
export const MAX_AUTO_EXPANDED_TURNS = 32;
// Older turns are mounted in small animation-frame chunks so opening the
// "earlier messages" fold (or "load all") never blocks a single long frame.
export const TURN_MOUNT_CHUNK = 4;

export function nextProgressiveTurnLimit(
  current: number,
  target: number,
  chunk = TURN_MOUNT_CHUNK
): number {
  if (current >= target) return current;
  return Math.min(current + Math.max(1, chunk), target);
}

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
  hiddenUserCount: Accessor<number>;
  startIndex: Accessor<number>;
  mergeStartIndex: Accessor<number>;
  globalIndex: (visibleIndex: number) => number;
  loadEarlierBatch: (scroller?: HTMLElement, count?: number) => void;
  loadAllEarlier: (scroller?: HTMLElement) => void;
  ensureTurnVisible: (messageID: string) => void;
  handleTopScroll: (scroller: HTMLElement) => void;
}

export function computeTurnWindowSlice(
  turns: MessageTurn[],
  limit: number
): {
  startIndex: number;
  visibleTurns: MessageTurn[];
  hiddenCount: number;
  hiddenUserCount: number;
  promptIndex?: number;
  anchorIndex?: number;
  indexes: number[];
} {
  let start = Math.max(0, turns.length - limit);
  // Preserve short prompt cycles, but never mount an unbounded number of steps
  // just to find their user prompt. Carry that prompt as a separate context row.
  const earliest = Math.max(0, turns.length - limit - TURN_BATCH_SIZE);
  while (start > earliest && turns[start]?.info.role === "assistant") start--;
  let promptIndex: number | undefined;
  let anchorIndex: number | undefined;
  if (start > 0 && turns[start]?.info.role === "assistant") {
    let previous = start - 1;
    while (previous >= 0 && turns[previous]?.info.role === "assistant") previous--;
    if (previous >= 0 && turns[previous]?.info.role === "user") promptIndex = previous;
    // Keep the owner of the leading work run mounted. Its activity drawer is
    // paged separately; moving this anchor on every new step caused flicker.
    for (let index = start - 1; index > previous; index--) {
      const prior = turns[index];
      if (prior.info.error) break;
      if (prior.parts.some(isWorkPart) || prior.parts.some(isVisibleTextPart)) anchorIndex = index;
      if (prior.parts.some(isVisibleTextPart)) break;
    }
  }
  const context = [promptIndex, anchorIndex].filter((index): index is number => index !== undefined);
  const indexes = [...context, ...Array.from({ length: turns.length - start }, (_, index) => start + index)];
  // Only older user messages make the "earlier messages" banner meaningful.
  // Long agent runs fold their tool/reasoning steps into activity groups, so a
  // single-message conversation can hide many turns without hiding a message.
  let hiddenUserCount = 0;
  for (let index = 0; index < start; index++) {
    if (index !== promptIndex && turns[index]?.info.role === "user") hiddenUserCount++;
  }
  return {
    startIndex: start,
    visibleTurns: indexes.map((index) => turns[index]),
    hiddenCount: start - context.length,
    hiddenUserCount,
    promptIndex,
    anchorIndex,
    indexes,
  };
}

export function resolveNextWindowLimit(params: {
  currentSession: string | null;
  lastActiveSessionID: string | null;
  state: { limit: number; seen: number; lastTailID: string };
  total: number;
  tailID: string;
  list: MessageTurn[];
}): number {
  if (params.currentSession !== params.lastActiveSessionID) {
    params.state.limit = INITIAL_VISIBLE_TURNS;
    params.state.seen = params.total;
    params.state.lastTailID = params.tailID;
    return params.state.limit;
  }

  if (params.state.seen > 0 && params.total > params.state.seen && params.state.lastTailID) {
    const prevTailIdx = params.list.findIndex((t) => t.info.id === params.state.lastTailID);
    if (prevTailIdx >= 0) {
      const appendedAtTail = params.total - 1 - prevTailIdx;
      if (appendedAtTail > 0 && appendedAtTail <= TURN_BATCH_SIZE) {
        if (params.state.limit < MAX_AUTO_EXPANDED_TURNS) {
          params.state.limit = Math.min(params.state.limit + appendedAtTail, MAX_AUTO_EXPANDED_TURNS);
        }
      }
    }
  }
  params.state.seen = params.total;
  params.state.lastTailID = params.tailID;
  return params.state.limit;
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
  const windows = new Map<string, { limit: number; seen: number; lastTailID: string }>();
  const [revision, setRevision] = createSignal(0);
  let prependingLock = false;
  let lastActiveSessionID: string | null = null;
  let mountFrame: number | undefined;
  let mountRevision = 0;

  const windowFor = (sid: string | null) => {
    const key = sid ?? "";
    let state = windows.get(key);
    if (!state) {
      if (windows.size >= 64) {
        const oldest = windows.keys().next().value;
        if (oldest !== undefined) windows.delete(oldest);
      }
      state = { limit: INITIAL_VISIBLE_TURNS, seen: 0, lastTailID: "" };
      windows.set(key, state);
    }
    return state;
  };

  const visibleLimit = createMemo(() => {
    revision();
    const currentSession = sessionID();
    const list = turns();
    const total = list.length;
    const tailID = total > 0 ? list[total - 1]!.info.id : "";
    const state = windowFor(currentSession);

    const limit = resolveNextWindowLimit({
      currentSession,
      lastActiveSessionID,
      state,
      total,
      tailID,
      list,
    });
    if (currentSession !== lastActiveSessionID) {
      lastActiveSessionID = currentSession;
    }
    return limit;
  });

  const setVisibleLimit = (next: number, sid = sessionID()) => {
    windowFor(sid).limit = next;
    setRevision((value) => value + 1);
  };

  const cancelProgressiveMount = () => {
    mountRevision++;
    if (mountFrame !== undefined) {
      cancelAnimationFrame(mountFrame);
      mountFrame = undefined;
    }
    prependingLock = false;
  };

  createEffect(() => {
    sessionID();
    cancelProgressiveMount();
  });
  onCleanup(cancelProgressiveMount);

  const slice = createMemo(() => computeTurnWindowSlice(turns(), visibleLimit()));
  const startIndex = createMemo(() => slice().startIndex);
  const hiddenCount = createMemo(() => slice().hiddenCount);
  const hiddenUserCount = createMemo(() => slice().hiddenUserCount);
  const visibleTurns = createMemo(() => slice().visibleTurns);

  // Mounts older turns in animation-frame chunks and compensates the scroll
  // position after each chunk. A single big `setVisibleLimit` used to mount the
  // whole hidden history (or a full 12-turn batch of heavy turns) in one task,
  // which is what made opening the folded conversation stall.
  const mountEarlierProgressively = (
    scroller: HTMLElement | undefined,
    targetLimit: number
  ) => {
    const sid = sessionID();
    const target = Math.min(targetLimit, turns().length);
    if (!sid || hiddenCount() <= 0 || target <= visibleLimit()) return;

    cancelProgressiveMount();
    const revision = mountRevision;
    prependingLock = true;

    if (typeof requestAnimationFrame !== "function") {
      setVisibleLimit(target, sid);
      prependingLock = false;
      return;
    }

    const pump = () => {
      mountFrame = undefined;
      if (revision !== mountRevision || sessionID() !== sid) return;
      if (hiddenCount() <= 0 || visibleLimit() >= target) {
        prependingLock = false;
        return;
      }

      const prevScrollHeight = scroller?.scrollHeight ?? 0;
      const prevScrollTop = scroller?.scrollTop ?? 0;
      setVisibleLimit(
        nextProgressiveTurnLimit(visibleLimit(), target),
        sid
      );
      if (scroller) {
        const delta = scroller.scrollHeight - prevScrollHeight;
        if (delta > 0) scroller.scrollTop = prevScrollTop + delta;
      }

      if (sessionID() === sid && visibleLimit() < target) {
        mountFrame = requestAnimationFrame(pump);
      } else {
        prependingLock = false;
      }
    };
    pump();
  };

  const loadEarlierBatch = (scroller?: HTMLElement, count = TURN_BATCH_SIZE) => {
    mountEarlierProgressively(
      scroller,
      visibleLimit() + Math.max(count, TURN_BATCH_SIZE)
    );
  };

  const loadAllEarlier = (scroller?: HTMLElement) => {
    mountEarlierProgressively(scroller, turns().length);
  };

  const ensureTurnVisible = (messageID: string) => {
    const list = turns();
    const idx = list.findIndex((t) => t.info.id === messageID);
    if (idx < 0) return;
    if (slice().indexes.includes(idx)) return;
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
    hiddenUserCount,
    startIndex,
    mergeStartIndex: () => slice().anchorIndex ?? startIndex(),
    globalIndex: (index) => slice().indexes[index],
    loadEarlierBatch,
    loadAllEarlier,
    ensureTurnVisible,
    handleTopScroll,
  };
}

export function EarlierTurnsBanner(props: {
  hiddenCount: number;
  hiddenUserCount: number;
  totalCount: number;
  onLoadBatch: () => void;
  onLoadAll: () => void;
}) {
  const nextBatchCount = () => Math.min(props.hiddenCount, TURN_BATCH_SIZE);

  return (
    <Show when={props.hiddenUserCount > 0}>
      <div class="mb-5 flex flex-col items-center justify-center gap-1.5 rounded-xl border border-dashed border-[var(--border-subtle)] bg-[var(--bg-inset)]/50 px-4 py-2.5 text-center select-none">
        <div class="text-[12px] text-[var(--text-faint)]">
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
              class="inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
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

  const revealPartIDs = createMemo(() => {
    const matchID = props.activeMatchTurnID;
    if (!matchID) return new Set<string>();
    return new Set(
      props.allTurns.find((turn) => turn.info.id === matchID)?.parts.map((part) => part.id) ?? []
    );
  });

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
        style={{
          "--hoxi-order": String(Math.min(Math.max(props.allTurns.length - 1 - props.globalIndex, 0), 5)),
          ...(props.globalIndex < props.allTurns.length - 4 && !isHighlighted()
            ? { "content-visibility": "auto", "contain-intrinsic-size": "auto 140px" }
            : {}),
        }}
        class={`hoxi-item-enter flex flex-col gap-y-2.5 px-1 rounded-xl transition-colors ${
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
