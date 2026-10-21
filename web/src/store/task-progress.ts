// web/src/store/task-progress.ts
// Todo extraction and normalization shared by the timeline progress card and
// the unified task overview.
import { workspace, type MessageTurn } from "./workspace-state";

export interface RawTodoItem {
  id?: string;
  content: string;
  status: "pending" | "in_progress" | "completed" | "cancelled" | string;
}

export interface NormalizedTodoItem {
  id?: string;
  content: string;
  status: "pending" | "in_progress" | "completed" | "interrupted" | "cancelled";
}

/**
 * Collects turns belonging to the latest user task cycle in `sessionID`.
 * Does not leak turns from older, unrelated conversation cycles.
 */
export function collectTaskCycleTurns(sessionID: string | null): MessageTurn[] {
  if (!sessionID) return [];
  const turns = workspace.messagesBySession[sessionID] ?? [];
  if (turns.length === 0) return [];

  let lastUserIdx = -1;
  for (let i = turns.length - 1; i >= 0; i--) {
    if (turns[i].info.role === "user") {
      lastUserIdx = i;
      break;
    }
  }

  return turns.slice(Math.max(0, lastUserIdx));
}

/** Finds the latest `todowrite` tool call strictly within the current task cycle turns. */
export function findLatestTodoPart(
  cycleTurns: MessageTurn[]
): { partID: string; todos: RawTodoItem[] } | undefined {
  for (let i = cycleTurns.length - 1; i >= 0; i--) {
    const parts = cycleTurns[i].parts ?? [];
    for (let j = parts.length - 1; j >= 0; j--) {
      const part = parts[j];
      if (part.type === "tool" && (part.tool ?? "").toLowerCase() === "todowrite") {
        const raw =
          (part.state?.input?.todos as RawTodoItem[] | undefined) ??
          ((part.state as any)?.metadata?.todos as RawTodoItem[] | undefined);
        if (Array.isArray(raw) && raw.length > 0) {
          return { partID: part.id, todos: raw };
        }
      }
    }
  }
  return undefined;
}

export function findLatestTodoContext(cycleTurns: MessageTurn[]): RawTodoItem[] {
  return findLatestTodoPart(cycleTurns)?.todos ?? [];
}

export function normalizeTodoItems(
  raw: RawTodoItem[],
  alive: boolean,
  interrupted: boolean
): NormalizedTodoItem[] {
  if (raw.length === 0) return [];
  return raw.map((item) => {
    const st = item.status;
    if (st === "completed") return { ...item, status: "completed" };
    if (st === "cancelled") return { ...item, status: "cancelled" };
    if (!alive && interrupted) {
      return {
        ...item,
        status: st === "in_progress" ? "interrupted" : "pending",
      };
    }
    // A finished session keeps the statuses the model actually recorded; items it never
    // marked done are not promoted to completed.
    return {
      ...item,
      status: st === "in_progress" ? "in_progress" : "pending",
    };
  });
}

/** ID of the latest user turn in `turns`, or null when there is none. */
export function findLatestUserTurnID(turns: readonly MessageTurn[]): string | null {
  for (let i = turns.length - 1; i >= 0; i--) {
    if (turns[i].info.role === "user") return turns[i].info.id;
  }
  return null;
}

/**
 * Revert/fork are task controls, not message metadata: completed exchanges
 * keep only time + copy, while the in-flight, interrupted, or failed exchange
 * keeps them. `index` is the position of the user turn within `turns`.
 */
export function isUserTurnTaskActive(
  turns: readonly MessageTurn[],
  index: number,
  state: { busy: boolean; interrupted: boolean; latestUserTurnID?: string | null }
): boolean {
  const turn = turns[index];
  if (!turn) return false;
  const latestID = state.latestUserTurnID !== undefined ? state.latestUserTurnID : findLatestUserTurnID(turns);
  if (turn.info.id !== latestID) return false;
  if (state.busy || state.interrupted) return true;
  let lastAssistant: MessageTurn | undefined;
  for (let i = index + 1; i < turns.length && turns[i].info.role === "assistant"; i++) {
    lastAssistant = turns[i];
  }
  // A stopped turn is still completed server-side, so its persisted finish
  // reason must keep the controls available after a reload.
  if (!lastAssistant || lastAssistant.info.error || lastAssistant.info.finish === "aborted") {
    return true;
  }
  return !lastAssistant.info.time?.completed;
}
