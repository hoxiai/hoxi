// web/src/store/task-overview.ts
// Unified, read-only task tree for the active session: convoy tasks, subagent
// child sessions and the latest todo plan. No new backend entity; every node is
// derived from state that already exists on the session plane.
import type { ConvoyPlan, ConvoyTaskStatus } from "./orchestration";
import { isSessionBusy } from "./session-query";
import { collectChildSubagentSessionIDs, resolveParentSessionID } from "./subagent-sessions";
import {
  collectTaskCycleTurns,
  findLatestTodoPart,
  normalizeTodoItems,
  type NormalizedTodoItem,
} from "./task-progress";
import { workspace } from "./workspace-state";

export type TaskNodeStatus = ConvoyTaskStatus | "pending";
export type TaskNodeKind = "convoy" | "subagent" | "todo";
export type TaskGroupID = "convoy" | "subagents" | "todos";

export interface TaskOverviewNode {
  id: string;
  kind: TaskNodeKind;
  title: string;
  status: TaskNodeStatus;
  detail?: string;
  sessionID?: string;
  dependsOn?: string[];
  children: TaskOverviewNode[];
}

export interface TaskOverviewGroup {
  id: TaskGroupID;
  nodes: TaskOverviewNode[];
}

const TERMINAL_PLAN_STATUSES = new Set<ConvoyTaskStatus>(["succeeded", "failed", "cancelled"]);

export function isConvoyPlanTerminal(status: ConvoyTaskStatus): boolean {
  return TERMINAL_PLAN_STATUSES.has(status);
}

export function buildTaskOverview(plans: ConvoyPlan[]): TaskOverviewGroup[] {
  const activeSessionID = workspace.activeSessionID;
  if (!activeSessionID) {
    return [
      { id: "convoy", nodes: [] },
      { id: "subagents", nodes: [] },
      { id: "todos", nodes: [] },
    ];
  }
  const relatedSessions = relatedSessionIDs(activeSessionID);
  const relevantPlans = plans.filter(
    (plan) =>
      !isConvoyPlanTerminal(plan.status) ||
      plan.tasks.some((task) => Boolean(task.sessionID && relatedSessions.has(task.sessionID)))
  );
  return [
    { id: "convoy", nodes: relevantPlans.slice(0, 5).map(convoyPlanNode) },
    { id: "subagents", nodes: subagentNodes(activeSessionID, new Set([activeSessionID]), 0) },
    { id: "todos", nodes: todoNodes(activeSessionID) },
  ];
}

function relatedSessionIDs(sessionID: string): Set<string> {
  const ids = new Set<string>();
  let current: string | null = sessionID;
  for (let depth = 0; depth < 8 && current; depth++) {
    if (ids.has(current)) break;
    ids.add(current);
    current = resolveParentSessionID(current);
  }
  return ids;
}

function convoyPlanNode(plan: ConvoyPlan): TaskOverviewNode {
  return {
    id: `convoy:${plan.id}`,
    kind: "convoy",
    title: plan.title,
    status: plan.status,
    children: plan.tasks.map((task) => ({
      id: `convoy:${plan.id}:${task.id}`,
      kind: "convoy",
      title: firstLine(task.prompt),
      status: task.status,
      detail: task.id,
      sessionID: task.sessionID,
      dependsOn: task.dependsOn,
      children: [],
    })),
  };
}

function subagentNodes(parentID: string, seen: Set<string>, depth: number): TaskOverviewNode[] {
  if (depth >= 8) return [];
  const nodes: TaskOverviewNode[] = [];
  for (const id of collectChildSubagentSessionIDs(parentID)) {
    if (seen.has(id)) continue;
    seen.add(id);
    const session = workspace.sessions.find((item) => item.id === id);
    nodes.push({
      id: `subagent:${id}`,
      kind: "subagent",
      title: session?.title?.trim() || id,
      status: subagentStatus(id),
      detail: id,
      sessionID: id,
      children: subagentNodes(id, seen, depth + 1),
    });
  }
  nodes.sort((a, b) => sessionCreatedAt(a.sessionID) - sessionCreatedAt(b.sessionID));
  return nodes;
}

function subagentStatus(sessionID: string): TaskNodeStatus {
  if (isSessionBusy(sessionID)) return "running";
  const interrupted = workspace.interruptedBySession[sessionID];
  if (interrupted === "stopped") return "cancelled";
  if (interrupted) return "blocked";
  return "succeeded";
}

function sessionCreatedAt(sessionID?: string): number {
  if (!sessionID) return 0;
  return workspace.sessions.find((item) => item.id === sessionID)?.time?.created ?? 0;
}

function todoNodes(sessionID: string): TaskOverviewNode[] {
  const latest = findLatestTodoPart(collectTaskCycleTurns(sessionID));
  if (!latest) return [];
  const items = normalizeTodoItems(
    latest.todos,
    isSessionBusy(sessionID),
    Boolean(workspace.interruptedBySession[sessionID])
  );
  return items.map((item, index) => ({
    id: `todo:${sessionID}:${latest.partID}:${index}`,
    kind: "todo",
    title: item.content,
    status: todoStatus(item.status),
    children: [],
  }));
}

function todoStatus(status: NormalizedTodoItem["status"]): TaskNodeStatus {
  switch (status) {
    case "in_progress":
      return "running";
    case "completed":
      return "succeeded";
    case "cancelled":
      return "cancelled";
    case "interrupted":
      return "failed";
    default:
      return "pending";
  }
}

function firstLine(value: string): string {
  const line = value
    .split(/\r?\n/)
    .map((part) => part.trim())
    .find(Boolean);
  const text = line ?? value.trim();
  return text.length > 120 ? `${text.slice(0, 120)}…` : text;
}
