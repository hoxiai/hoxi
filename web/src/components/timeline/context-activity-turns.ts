// web/src/components/timeline/context-activity-turns.ts
import type { MessageInfo, MessagePart } from "../../services/sse-types";
import type { MessageTurn } from "../../store/workspace";
import {
  expandInlineThinkingParts,
  isVisibleTextPart,
  isWorkPart,
} from "./context-activity-parts";

export type TurnRenderItem =
  | { kind: "single"; part: MessagePart }
  | { kind: "activity-group"; parts: MessagePart[] };

export function groupAssistantParts(parts: MessagePart[]): TurnRenderItem[] {
  const expanded = expandInlineThinkingParts(parts);
  const items: TurnRenderItem[] = [];
  let buffer: MessagePart[] = [];

  const flushBuffer = () => {
    if (buffer.length === 0) return;
    items.push({ kind: "activity-group", parts: buffer });
    buffer = [];
  };

  for (const part of expanded) {
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

function samePartReferenceList(a: MessagePart[], b: MessagePart[]): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

/**
 * Preserves `TurnRenderItem` object and array identities across high-frequency
 * token deltas (`message.part.delta`) when the underlying part references have
 * not changed.
 */
export function stabilizeTurnRenderItems(
  prev: TurnRenderItem[] | undefined,
  next: TurnRenderItem[]
): TurnRenderItem[] {
  if (!prev || prev.length === 0) return next;
  let reusedAll = prev.length === next.length;
  const stabilized = next.map((item, idx) => {
    const prior = prev[idx];
    if (!prior || prior.kind !== item.kind) {
      reusedAll = false;
      return item;
    }
    if (item.kind === "single" && prior.kind === "single") {
      if (prior.part === item.part) return prior;
      reusedAll = false;
      return item;
    }
    if (item.kind === "activity-group" && prior.kind === "activity-group") {
      if (samePartReferenceList(prior.parts, item.parts)) return prior;
      reusedAll = false;
      return item;
    }
    reusedAll = false;
    return item;
  });
  return reusedAll ? prev : stabilized;
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

function sameStringList(a: string[], b: string[]): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

export function stabilizeMergedTurnResolution(
  prev: MergedTurnResolution | undefined,
  next: MergedTurnResolution
): MergedTurnResolution {
  if (!prev) return next;
  if (
    prev.isVisuallyEmpty === next.isVisuallyEmpty &&
    prev.isFirstVisualTurnInCycle === next.isFirstVisualTurnInCycle &&
    prev.isCycleEnd === next.isCycleEnd &&
    prev.ownsLiveTail === next.ownsLiveTail &&
    samePartReferenceList(prev.effectiveParts, next.effectiveParts) &&
    sameStringList(prev.carriedTurnIDs, next.carriedTurnIDs)
  ) {
    return prev;
  }
  return next;
}

const isRenderablePart = (part: MessagePart) => isWorkPart(part) || isVisibleTextPart(part);

/**
 * Returns true when an assistant cycle ends with a completed turn that emitted
 * ONLY `reasoning` parts (0 `tool` parts and 0 visible `text` parts).
 * Such turns must not be swallowed into an earlier turn's collapsed tool group,
 * because the reasoning block is the sole final output of the turn.
 */
export function isCompletedStandaloneReasoningTurn(
  allTurns: MessageTurn[],
  index: number
): boolean {
  const turn = allTurns[index];
  if (!turn || turn.info.role !== "assistant" || turn.info.error || !turn.info.time?.completed) {
    return false;
  }
  const isEnd =
    index === allTurns.length - 1 || allTurns[index + 1]?.info.role !== "assistant";
  if (!isEnd) return false;
  const parts = expandInlineThinkingParts(turn.parts);
  if (parts.some(isVisibleTextPart) || parts.some((p) => p.type === "tool")) {
    return false;
  }
  return parts.some((p) => p.type === "reasoning" && Boolean(p.text?.trim()));
}

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

  const turnParts = expandInlineThinkingParts(turn.parts);
  const firstTextIdx = turnParts.findIndex(isVisibleTextPart);
  const error = Boolean(turn.info.error);
  const standaloneFinalReasoning = isCompletedStandaloneReasoningTurn(allTurns, globalIndex);
  // Leading work joins the open run; this turn renders from its first visible text
  // (or its own reasoning when it is a completed cycle-ending reasoning-only turn).
  const own =
    error || !runOpen || standaloneFinalReasoning
      ? turnParts
      : firstTextIdx < 0
        ? []
        : turnParts.slice(firstTextIdx);
  const ownsRun = !error && !standaloneFinalReasoning && (runOpen ? firstTextIdx >= 0 : turnParts.some(isRenderablePart));

  const carriedWork: MessagePart[] = [];
  const carriedTurnIDs: string[] = [];
  let ownsLiveTail = false;
  if (ownsRun) {
    let i = globalIndex + 1;
    for (; i < allTurns.length && allTurns[i].info.role === "assistant"; i++) {
      const next = allTurns[i];
      if (next.info.error || isCompletedStandaloneReasoningTurn(allTurns, i)) break;
      const nextParts = expandInlineThinkingParts(next.parts);
      const nextTextIdx = nextParts.findIndex(isVisibleTextPart);
      const lead = (nextTextIdx < 0 ? nextParts : nextParts.slice(0, nextTextIdx)).filter(isWorkPart);
      if (lead.length > 0) {
        carriedWork.push(...lead);
        carriedTurnIDs.push(next.info.id);
      }
      if (nextTextIdx >= 0) break;
    }
    const lastCarriedAssistant = allTurns[i - 1];
    const onlyTrailingSteerUsers =
      i < allTurns.length &&
      !lastCarriedAssistant?.info.time?.completed &&
      !lastCarriedAssistant?.info.error &&
      allTurns.slice(i).every((t) => t.info.role === "user");
    ownsLiveTail = i === allTurns.length || onlyTrailingSteerUsers;
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
  const partIDs = new Set(
    partID ? [partID, `${partID}#thinking`] : source.parts.map((part) => part.id)
  );
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

/**
 * Returns true once the latest assistant cycle has already mounted any visible
 * work or text output in the timeline, preventing the bottom waiting placeholder
 * from re-mounting underneath streaming markdown responses.
 */
export function hasLiveTailRenderedOutput(
  cycleTurns: Array<{ info: MessageInfo; parts: MessagePart[] }>
): boolean {
  let runStart = cycleTurns.length;
  while (runStart > 0) {
    const turn = cycleTurns[runStart - 1];
    if (turn.info.role !== "assistant" || turn.info.error) break;
    runStart--;
  }
  for (let i = runStart; i < cycleTurns.length; i++) {
    const parts = cycleTurns[i].parts;
    for (let p = 0; p < parts.length; p++) {
      const part = parts[p];
      if (isWorkPart(part) || isVisibleTextPart(part)) return true;
    }
  }
  return false;
}

/**
 * Resolves the active user->assistant cycle for live status indicators.
 * When a steer prompt is injected mid-turn, a trailing user turn sits at the
 * end of the list while the preceding assistant step is still running; keep
 * tracking that in-flight assistant cycle until its step completes.
 */
export function resolveActiveExecutionCycle(
  list: MessageTurn[]
): { userTurn: MessageTurn; cycleTurns: MessageTurn[] } | null {
  let lastUserIdx = -1;
  for (let i = list.length - 1; i >= 0; i--) {
    if (list[i].info.role === "user") {
      lastUserIdx = i;
      break;
    }
  }
  if (lastUserIdx < 0) return null;

  const trailingTurns = list.slice(lastUserIdx + 1);
  if (trailingTurns.length > 0) {
    return { userTurn: list[lastUserIdx], cycleTurns: trailingTurns };
  }

  let priorIdx = lastUserIdx - 1;
  while (priorIdx >= 0 && list[priorIdx].info.role === "user") {
    priorIdx--;
  }
  if (
    priorIdx >= 0 &&
    list[priorIdx].info.role === "assistant" &&
    !list[priorIdx].info.time?.completed &&
    !list[priorIdx].info.error
  ) {
    let cycleStart = priorIdx;
    while (cycleStart > 0 && list[cycleStart - 1].info.role === "assistant") {
      cycleStart--;
    }
    const anchorUser =
      cycleStart > 0 && list[cycleStart - 1].info.role === "user"
        ? list[cycleStart - 1]
        : list[lastUserIdx];
    return {
      userTurn: anchorUser,
      cycleTurns: list.slice(cycleStart, priorIdx + 1),
    };
  }

  return { userTurn: list[lastUserIdx], cycleTurns: trailingTurns };
}
