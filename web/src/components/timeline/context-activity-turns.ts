// web/src/components/timeline/context-activity-turns.ts
import type { MessageInfo, MessagePart } from "../../services/sse-types";
import type { MessageTurn } from "../../store/workspace";
import { isVisibleTextPart, isWorkPart } from "./context-activity-parts";

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

/**
 * Returns true once the latest assistant cycle has already mounted any visible
 * work or text output in the timeline, preventing the bottom waiting placeholder
 * from re-mounting underneath streaming markdown responses.
 */
export function hasLiveTailRenderedOutput(
  cycleTurns: Array<{ info: MessageInfo; parts: MessagePart[] }>
): boolean {
  for (let i = cycleTurns.length - 1; i >= 0; i--) {
    const turn = cycleTurns[i];
    if (turn.info.role !== "assistant" || turn.info.error) return false;
    for (let p = turn.parts.length - 1; p >= 0; p--) {
      const part = turn.parts[p];
      if (isVisibleTextPart(part) || isWorkPart(part)) return true;
    }
  }
  return false;
}
