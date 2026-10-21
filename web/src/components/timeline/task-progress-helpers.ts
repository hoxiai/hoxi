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
import type { NormalizedTodoItem } from "../../store/task-progress";

export interface CycleStepItem {
  id: string;
  tool: string;
  label: string;
  file?: string;
  status: "running" | "completed" | "interrupted" | "error";
  additions: number;
  deletions: number;
  /** Step happened after the latest `todowrite`, i.e. progress the todo list does not reflect yet. */
  sinceTodo: boolean;
}

export interface TodoActivitySummary {
  stepsDone: number;
  stepsSinceTodo: number;
  subagentsRunning: number;
  subagentsTotal: number;
}

/** Finished tool steps after the last todo update before the list is considered stale. */
export const TODO_STALE_STEP_THRESHOLD = 3;

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

export function collectCycleStepItems(
  cycleTurns: MessageTurn[],
  sessionID: string | null,
  alive: boolean,
  latestTodoPartID?: string
): CycleStepItem[] {
  if (!sessionID) return [];
  const steps: CycleStepItem[] = [];
  let seenLatestTodo = false;

  const pushToolPart = (tp: ToolPart, ownerAlive: boolean) => {
    const toolName = (tp.tool ?? "").toLowerCase();
    if (toolName === "todowrite") {
      if (tp.id === latestTodoPartID) seenLatestTodo = true;
      return;
    }

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
      sinceTodo: seenLatestTodo,
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

export function summarizeTodoActivity(steps: CycleStepItem[]): TodoActivitySummary {
  const summary: TodoActivitySummary = {
    stepsDone: 0,
    stepsSinceTodo: 0,
    subagentsRunning: 0,
    subagentsTotal: 0,
  };
  for (const step of steps) {
    const finished = step.status === "completed" || step.status === "error";
    if (finished) summary.stepsDone++;
    if (finished && step.sinceTodo) summary.stepsSinceTodo++;
    if (isTaskTool(step.tool)) {
      summary.subagentsTotal++;
      if (step.status === "running") summary.subagentsRunning++;
    }
  }
  return summary;
}

/** The model kept working well past its last todo update while items remain open. */
export function isTodoListStale(
  todos: NormalizedTodoItem[],
  summary: TodoActivitySummary
): boolean {
  if (todos.length === 0) return false;
  const hasOpenItem = todos.some((t) => t.status !== "completed" && t.status !== "cancelled");
  return hasOpenItem && summary.stepsSinceTodo >= TODO_STALE_STEP_THRESHOLD;
}

export function resolveTaskChangedFiles(
  cycleTurns: MessageTurn[],
  sessionID: string | null
): SnapshotFileDiff[] {
  if (!sessionID) return [];
  return collectCycleChangedFiles(cycleTurns, sessionID);
}
