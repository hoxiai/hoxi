// web/src/store/task-overview.ts
// Unified, read-only task tree for the active session: convoy tasks, subagent
// child sessions and the latest todo plan. No new backend entity; every node is
// derived from state that already exists on the session plane.
import { createSignal } from "solid-js";
import { createStore } from "solid-js/store";
import {
  fetchConvoyPlans,
  fetchConvoyPlansForSession,
  type ConvoyPlan,
  type ConvoyTaskStatus,
} from "./orchestration";
import { isSessionBusy } from "./session-query";
import { collectChildSubagentSessionIDs, resolveParentSessionID } from "./subagent-sessions";
import {
  collectTaskCycleTurns,
  findLatestTodoPart,
  normalizeTodoItems,
  type NormalizedTodoItem,
} from "./task-progress";
import { workspace, type MessageTurn } from "./workspace-state";

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
  const relatedSessions = new Set(collectAncestorSessionIDs(activeSessionID));
  const relevantPlans = plans
    .filter(
      (plan) =>
        !isConvoyPlanTerminal(plan.status) ||
        plan.tasks.some((task) => Boolean(task.sessionID && relatedSessions.has(task.sessionID)))
    )
    .sort((a, b) => (b.updatedAt ?? 0) - (a.updatedAt ?? 0));
  return [
    { id: "convoy", nodes: relevantPlans.slice(0, 5).map(convoyPlanNode) },
    { id: "subagents", nodes: subagentNodes(activeSessionID, new Set([activeSessionID]), 0) },
    { id: "todos", nodes: todoNodes(activeSessionID) },
  ];
}

/** The session itself followed by its parent chain (max depth 8, cycle-safe). */
export function collectAncestorSessionIDs(sessionID: string | null | undefined): string[] {
  const ids: string[] = [];
  const seen = new Set<string>();
  let current: string | null = sessionID ?? null;
  for (let depth = 0; depth < 8 && current; depth++) {
    if (seen.has(current)) break;
    seen.add(current);
    ids.push(current);
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

// Interrupted sessions map to the same node status for subagents and todos.
function interruptedNodeStatus(sessionID: string): TaskNodeStatus | null {
  const interrupted = workspace.interruptedBySession[sessionID];
  if (interrupted === "stopped") return "cancelled";
  if (interrupted) return "blocked";
  return null;
}

function lastAssistantTurn(turns: readonly MessageTurn[]): MessageTurn | undefined {
  for (let i = turns.length - 1; i >= 0; i--) {
    if (turns[i].info.role === "assistant") return turns[i];
  }
  return undefined;
}

function subagentStatus(sessionID: string): TaskNodeStatus {
  if (isSessionBusy(sessionID)) return "running";
  const interrupted = interruptedNodeStatus(sessionID);
  if (interrupted) return interrupted;
  const turns = workspace.messagesBySession[sessionID];
  // Transcript not loaded yet: no evidence of failure, keep the settled state.
  if (turns === undefined) return "succeeded";
  const last = lastAssistantTurn(turns);
  if (!last) return "pending";
  if (last.info.error) return "failed";
  if (last.info.finish === "aborted") return "cancelled";
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
    status: todoStatus(item.status, sessionID),
    children: [],
  }));
}

function todoStatus(status: NormalizedTodoItem["status"], sessionID: string): TaskNodeStatus {
  switch (status) {
    case "in_progress":
      return "running";
    case "completed":
      return "succeeded";
    case "cancelled":
      return "cancelled";
    case "interrupted":
      return interruptedNodeStatus(sessionID) ?? "blocked";
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

// ---- Plan loading, polling and row expansion for the Tasks inspector ----

const [taskOverviewPlans, setTaskOverviewPlans] = createSignal<ConvoyPlan[]>([]);
const [taskOverviewLoading, setTaskOverviewLoading] = createSignal(false);
const [taskOverviewFailed, setTaskOverviewFailed] = createSignal(false);
let taskOverviewRequestSeq = 0;
let taskOverviewScope = "";

export { taskOverviewFailed, taskOverviewLoading, taskOverviewPlans };

export interface TaskOverviewPlanFetchers {
  byDirectory: (directory: string) => Promise<ConvoyPlan[]>;
  bySession: (sessionID: string) => Promise<ConvoyPlan[]>;
}

const defaultPlanFetchers: TaskOverviewPlanFetchers = {
  byDirectory: fetchConvoyPlans,
  bySession: fetchConvoyPlansForSession,
};

/** Drops loaded plans and invalidates any in-flight request. */
export function resetTaskOverviewPlans(): void {
  taskOverviewRequestSeq++;
  taskOverviewScope = "";
  setTaskOverviewPlans([]);
  setTaskOverviewFailed(false);
  setTaskOverviewLoading(false);
}

/**
 * Loads convoy plans for a directory plus the active session ancestry and
 * merges them by plan ID. A request generation guard drops late responses,
 * and a scope change clears stale plans before the new ones arrive.
 */
export async function loadTaskOverviewPlans(
  directory: string,
  sessionIDs: readonly string[],
  fetchers: TaskOverviewPlanFetchers = defaultPlanFetchers
): Promise<void> {
  if (!directory && sessionIDs.length === 0) {
    resetTaskOverviewPlans();
    return;
  }
  const seq = ++taskOverviewRequestSeq;
  const scope = `${directory}|${sessionIDs.join(",")}`;
  if (scope !== taskOverviewScope) {
    taskOverviewScope = scope;
    setTaskOverviewPlans([]);
  }
  setTaskOverviewLoading(true);
  try {
    const [byDirectory, ...bySession] = await Promise.all([
      directory ? fetchers.byDirectory(directory) : Promise.resolve([] as ConvoyPlan[]),
      ...sessionIDs.map((id) => fetchers.bySession(id).catch(() => [] as ConvoyPlan[])),
    ]);
    if (seq !== taskOverviewRequestSeq) return;
    const merged = new Map<string, ConvoyPlan>();
    for (const plan of byDirectory) merged.set(plan.id, plan);
    for (const list of bySession) {
      for (const plan of list) merged.set(plan.id, plan);
    }
    setTaskOverviewPlans([...merged.values()]);
    setTaskOverviewFailed(false);
  } catch {
    if (seq === taskOverviewRequestSeq) setTaskOverviewFailed(true);
  } finally {
    if (seq === taskOverviewRequestSeq) setTaskOverviewLoading(false);
  }
}

export const TASK_OVERVIEW_POLL_MS = 2500;

/** Polls while any loaded plan is still active. Returns the stop function. */
export function startTaskOverviewPolling(directory: string, sessionIDs: readonly string[]): () => void {
  const timer = setInterval(() => {
    if (!taskOverviewPlans().some((plan) => !isConvoyPlanTerminal(plan.status))) return;
    void loadTaskOverviewPlans(directory, sessionIDs);
  }, TASK_OVERVIEW_POLL_MS);
  return () => clearInterval(timer);
}

// Row expansion is keyed by node ID so rebuilding nodes on every stream flush
// never resets what the user expanded or collapsed.
const [taskNodeExpansion, setTaskNodeExpansion] = createStore<Record<string, boolean>>({});

export function isTaskNodeExpanded(nodeID: string, depth: number): boolean {
  return taskNodeExpansion[nodeID] ?? depth === 0;
}

export function toggleTaskNodeExpanded(nodeID: string, depth: number): void {
  setTaskNodeExpansion(nodeID, !isTaskNodeExpanded(nodeID, depth));
}
