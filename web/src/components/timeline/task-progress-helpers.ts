// web/src/components/timeline/task-progress-helpers.ts
import type { SnapshotFileDiff } from "../../services/sse-types";
import {
  extractChildSubagentSessionId,
  isSessionBusy,
  isSubagentToolAlive,
  type MessageTurn,
  workspace,
} from "../../store/workspace";
import {
  extractCommand,
  extractDescription,
  extractTargetFile,
  getFilename,
  isEditingTool,
  isTaskTool,
  type ToolPart,
} from "./tool-card-helpers";
import { collectCycleChangedFiles, resolveToolFileDiff } from "./tool-card-state";

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

export interface CycleStepItem {
  id: string;
  tool: string;
  label: string;
  file?: string;
  status: "running" | "completed" | "interrupted" | "error";
  additions: number;
  deletions: number;
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

/**
 * Finds the latest `todowrite` tool call strictly within the current task cycle turns.
 */
export function findLatestTodoContext(cycleTurns: MessageTurn[]): RawTodoItem[] {
  for (let i = cycleTurns.length - 1; i >= 0; i--) {
    const parts = cycleTurns[i].parts ?? [];
    for (let j = parts.length - 1; j >= 0; j--) {
      const part = parts[j];
      if (part.type === "tool" && (part.tool ?? "").toLowerCase() === "todowrite") {
        const raw =
          (part.state?.input?.todos as RawTodoItem[] | undefined) ??
          ((part.state as any)?.metadata?.todos as RawTodoItem[] | undefined);
        if (Array.isArray(raw) && raw.length > 0) {
          return raw;
        }
      }
    }
  }
  return [];
}

export function hasActiveSubagentInCycle(
  cycleTurns: MessageTurn[],
  sessionID: string | null
): boolean {
  if (!sessionID) return false;
  for (const turn of cycleTurns) {
    for (const part of turn.parts ?? []) {
      if (part.type === "tool" && isTaskTool(part.tool)) {
        if (isSubagentToolAlive(part as ToolPart, sessionID)) return true;
      }
    }
  }
  return false;
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
    if (!alive) {
      return { ...item, status: "completed" };
    }
    return {
      ...item,
      status: st === "in_progress" ? "in_progress" : "pending",
    };
  });
}

export function collectCycleStepItems(
  cycleTurns: MessageTurn[],
  sessionID: string | null,
  alive: boolean
): CycleStepItem[] {
  if (!sessionID) return [];
  const steps: CycleStepItem[] = [];

  const pushToolPart = (tp: ToolPart, ownerAlive: boolean) => {
    const toolName = (tp.tool ?? "").toLowerCase();
    if (toolName === "todowrite") return;

    const rawStatus = tp.state.status;
    const isTask = isTaskTool(tp.tool);
    const stepRunning = isTask
      ? isSubagentToolAlive(tp, sessionID)
      : (rawStatus === "running" || rawStatus === "pending") && ownerAlive;
    const stepInterrupted =
      !stepRunning && (rawStatus === "running" || rawStatus === "pending");

    const status: CycleStepItem["status"] = stepRunning
      ? "running"
      : stepInterrupted
        ? "interrupted"
        : rawStatus === "error"
          ? "error"
          : "completed";

    const file = extractTargetFile(tp);
    const cmd = extractCommand(tp);
    const desc = extractDescription(tp);
    const pattern = String(tp.state.input?.pattern || tp.state.input?.url || "");
    const label = file
      ? getFilename(file)
      : cmd
        ? `$ ${cmd}`
        : desc || pattern || tp.state.title || "";

    let additions = 0;
    let deletions = 0;
    if (file && isEditingTool(tp.tool)) {
      const d = resolveToolFileDiff(tp, true, file);
      if (d) {
        additions = d.additions ?? 0;
        deletions = d.deletions ?? 0;
      }
    }

    steps.push({
      id: tp.id,
      tool: tp.tool,
      label,
      file: file || undefined,
      status,
      additions,
      deletions,
    });

    if (isTask) {
      const subID = extractChildSubagentSessionId(tp);
      if (subID) {
        const subAlive = isSessionBusy(subID) || stepRunning;
        const subTurns = workspace.messagesBySession[subID] ?? [];
        for (const st of subTurns) {
          for (const sp of st.parts ?? []) {
            if (sp.type === "tool") {
              pushToolPart(sp as ToolPart, subAlive);
            }
          }
        }
      }
    }
  };

  for (const turn of cycleTurns) {
    for (const part of turn.parts ?? []) {
      if (part.type === "tool") {
        pushToolPart(part as ToolPart, alive);
      }
    }
  }

  return steps;
}

export function resolveTaskChangedFiles(
  cycleTurns: MessageTurn[],
  sessionID: string | null
): SnapshotFileDiff[] {
  if (!sessionID) return [];
  return collectCycleChangedFiles(cycleTurns, sessionID);
}
