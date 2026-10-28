// web/src/store/orchestration.ts
import { createSignal } from "solid-js";
import {
  cancelConvoyPlan as apiCancelConvoyPlan,
  createConvoyPlan as apiCreateConvoyPlan,
  draftConvoyPlan as apiDraftConvoyPlan,
  fetchConvoyPlan as apiFetchConvoyPlan,
  fetchConvoyPlans as apiFetchConvoyPlans,
  fetchConvoyPlansForSession as apiFetchConvoyPlansForSession,
  mergeConvoyPlan as apiMergeConvoyPlan,
  retryConvoyPlan as apiRetryConvoyPlan,
  type ConvoyDraftRequest,
  type ConvoyPlan,
  type ConvoyPlanRequest,
  type ConvoyTaskSpec,
  type ConvoyTaskState,
  type ConvoyTaskStatus,
} from "../services/orchestration-api";
import { sessionDirectory } from "./session-query";
import { workspace } from "./workspace-state";

export type {
  ConvoyDraftRequest,
  ConvoyPlan,
  ConvoyPlanRequest,
  ConvoyTaskSpec,
  ConvoyTaskState,
  ConvoyTaskStatus,
};

export interface ParsedSubtaskPlan {
  title: string;
  concurrency: number;
  tasks: ConvoyTaskSpec[];
}

export type SubtasksCommand =
  | { kind: "propose"; goal: string }
  | { kind: "spawn"; prompt: string }
  | { kind: "usage" };

const INLINE_CONVOY_STORAGE_KEY = "hoxi.inlineConvoyByKey";
const MAX_STORED_INLINE_CONVOYS = 100;

function loadStoredInlineConvoyMap(): Record<string, string> {
  if (typeof localStorage === "undefined") return {};
  try {
    const raw = localStorage.getItem(INLINE_CONVOY_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof k === "string" && typeof v === "string" && v.trim()) {
        out[k] = v.trim();
      }
    }
    return out;
  } catch {
    return {};
  }
}

function saveStoredInlineConvoyMap(map: Record<string, string>): void {
  if (typeof localStorage === "undefined") return;
  try {
    const entries = Object.entries(map);
    const trimmed =
      entries.length > MAX_STORED_INLINE_CONVOYS
        ? Object.fromEntries(entries.slice(entries.length - MAX_STORED_INLINE_CONVOYS))
        : map;
    localStorage.setItem(INLINE_CONVOY_STORAGE_KEY, JSON.stringify(trimmed));
  } catch {
    /* ignore storage quota errors */
  }
}

const [inlineConvoyByKey, setInlineConvoyByKey] = createSignal<Record<string, string>>(
  loadStoredInlineConvoyMap()
);
const [spawnedPlansBySession, setSpawnedPlansBySession] = createSignal<
  Record<string, ConvoyPlan[]>
>({});

export function getBoundInlineConvoyPlanID(cardKey: string): string | undefined {
  return inlineConvoyByKey()[cardKey];
}

export function bindInlineConvoyPlan(cardKey: string, planID: string): void {
  const cleanKey = cardKey.trim();
  const cleanID = planID.trim();
  if (!cleanKey || !cleanID) return;
  setInlineConvoyByKey((prev) => {
    const next = { ...prev, [cleanKey]: cleanID };
    saveStoredInlineConvoyMap(next);
    return next;
  });
}

export function clearBoundInlineConvoyPlan(cardKey: string): void {
  const cleanKey = cardKey.trim();
  if (!cleanKey) return;
  setInlineConvoyByKey((prev) => {
    if (!(cleanKey in prev)) return prev;
    const next = { ...prev };
    delete next[cleanKey];
    saveStoredInlineConvoyMap(next);
    return next;
  });
}

export function recordSessionConvoyPlan(sessionID: string | null | undefined, plan: ConvoyPlan): void {
  const sid = (sessionID || plan.parentSessionID || "").trim();
  if (!sid) return;
  setSpawnedPlansBySession((prev) => {
    const existing = prev[sid] ?? [];
    const idx = existing.findIndex((p) => p.id === plan.id);
    const nextList =
      idx >= 0
        ? existing.map((item, i) => (i === idx ? plan : item))
        : [plan, ...existing].slice(0, 12);
    return { ...prev, [sid]: nextList };
  });
}

export function getSessionSpawnedConvoyPlans(sessionID: string | null | undefined): ConvoyPlan[] {
  const sid = (sessionID || "").trim();
  if (!sid) return [];
  return spawnedPlansBySession()[sid] ?? [];
}

export function dismissSessionSpawnedConvoyPlan(
  sessionID: string | null | undefined,
  planID: string
): void {
  const sid = (sessionID || "").trim();
  if (!sid) return;
  setSpawnedPlansBySession((prev) => {
    const existing = prev[sid];
    if (!existing) return prev;
    return { ...prev, [sid]: existing.filter((p) => p.id !== planID) };
  });
}

export function isSubtaskPlanCodeBlockLang(lang?: string): boolean {
  const clean = (lang || "").trim().toLowerCase();
  return clean === "hoxi-subtasks" || clean === "hoxi-convoy" || clean === "subtasks-plan";
}

function sanitizeSubtaskID(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/-{2,}/g, "-")
    .replace(/^-+|-+$/g, "");
}

function hasDependencyCycle(tasks: ConvoyTaskSpec[]): boolean {
  const indegree = new Map<string, number>();
  const edges = new Map<string, string[]>();
  for (const t of tasks) {
    const deps = t.dependsOn ?? [];
    indegree.set(t.id, deps.length);
    for (const dep of deps) {
      const list = edges.get(dep) ?? [];
      list.push(t.id);
      edges.set(dep, list);
    }
  }
  const queue: string[] = [];
  for (const [id, deg] of indegree.entries()) {
    if (deg === 0) queue.push(id);
  }
  let visited = 0;
  while (queue.length > 0) {
    const id = queue.shift()!;
    visited++;
    for (const next of edges.get(id) ?? []) {
      const nextDeg = (indegree.get(next) ?? 0) - 1;
      indegree.set(next, nextDeg);
      if (nextDeg === 0) queue.push(next);
    }
  }
  return visited !== tasks.length;
}

export function parseSubtaskPlanBlock(raw: string): ParsedSubtaskPlan | null {
  const trimmed = (raw || "").trim();
  if (!trimmed) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    const start = trimmed.indexOf("{");
    const end = trimmed.lastIndexOf("}");
    if (start < 0 || end <= start) return null;
    try {
      parsed = JSON.parse(trimmed.slice(start, end + 1));
    } catch {
      return null;
    }
  }

  const obj = Array.isArray(parsed)
    ? { title: "", concurrency: 3, tasks: parsed }
    : (parsed as Record<string, unknown> | null);
  if (!obj || typeof obj !== "object" || !Array.isArray(obj.tasks)) return null;

  const rawTasks = obj.tasks as Array<Record<string, unknown>>;
  if (rawTasks.length === 0 || rawTasks.length > 20) return null;

  const idMap = new Map<string, string>();
  const usedIDs = new Set<string>();
  const normalizedTasks: Array<{ id: string; prompt: string; rawDeps: string[] }> = [];

  for (let i = 0; i < rawTasks.length; i++) {
    const item = rawTasks[i];
    if (!item || typeof item !== "object") continue;
    const prompt = typeof item.prompt === "string" ? item.prompt.trim() : "";
    if (!prompt) continue;
    const rawID = typeof item.id === "string" ? item.id.trim() : "";
    let id = sanitizeSubtaskID(rawID) || `task-${i + 1}`;
    const base = id;
    for (let n = 2; usedIDs.has(id); n++) {
      id = `${base}-${n}`;
    }
    usedIDs.add(id);
    if (rawID) idMap.set(rawID, id);
    idMap.set(id, id);

    const rawDeps = Array.isArray(item.dependsOn)
      ? item.dependsOn.filter((d): d is string => typeof d === "string")
      : [];
    normalizedTasks.push({ id, prompt, rawDeps });
  }

  if (normalizedTasks.length === 0) return null;

  const tasks: ConvoyTaskSpec[] = normalizedTasks.map((t) => {
    const seen = new Set<string>();
    const dependsOn: string[] = [];
    for (const rawDep of t.rawDeps) {
      const mapped = idMap.get(rawDep.trim());
      if (!mapped || mapped === t.id || seen.has(mapped)) continue;
      seen.add(mapped);
      dependsOn.push(mapped);
    }
    return dependsOn.length > 0
      ? { id: t.id, prompt: t.prompt, dependsOn }
      : { id: t.id, prompt: t.prompt };
  });

  if (hasDependencyCycle(tasks)) return null;

  const rawConcurrency = typeof obj.concurrency === "number" ? Math.trunc(obj.concurrency) : 3;
  const concurrency = Math.max(1, Math.min(8, rawConcurrency || 3));
  const title = typeof obj.title === "string" ? obj.title.trim() : "";

  return { title, concurrency, tasks };
}

export function computeSubtaskPlanCardKey(
  plan: ParsedSubtaskPlan,
  sessionID?: string | null
): string {
  const sig = plan.tasks.map((t) => `${t.id}:${t.prompt.slice(0, 48)}`).join("|");
  return `${sessionID || "root"}::${plan.title}::${sig}`;
}

export function parseSubtasksCommand(text: string): SubtasksCommand | null {
  const trimmed = (text || "").trim();
  const match = trimmed.match(/^\/(subtasks|convoy|subtask)(?:\s+([\s\S]*))?$/i);
  if (!match) return null;
  const cmd = match[1].toLowerCase();
  const rest = (match[2] ?? "").trim();
  if (cmd === "subtask") {
    if (!rest) return { kind: "usage" };
    return { kind: "spawn", prompt: rest };
  }
  return { kind: "propose", goal: rest };
}

export function buildProposeSubtasksPrompt(goal?: string, planContext?: string): string {
  const cleanGoal = (goal || "").trim();
  const cleanContext = (planContext || "").trim();
  const header = cleanGoal
    ? `Please split the following objective into parallel coding subtasks:\n${cleanGoal}`
    : cleanContext
      ? `Please split our agreed plan below into parallel coding subtasks:\n\n${cleanContext}`
      : "Please split the current task and our agreed approach in this conversation into parallel coding subtasks.";

  return [
    header,
    "",
    "Output a brief overview followed by a fenced `hoxi-subtasks` JSON block with this exact schema so I can review and launch them in isolated Git worktrees:",
    "```hoxi-subtasks",
    '{"title":"Short plan title","concurrency":3,"tasks":[{"id":"task-1","prompt":"Self-contained instructions, file scope, constraints, and verification command","dependsOn":[]}]}',
    "```",
    "Rules:",
    "1. Prefer 2–6 self-contained subtasks. Each subtask runs in its own isolated Git worktree and cannot see the other subtasks' prompts.",
    "2. Put independent tasks in parallel (`dependsOn: []`); only add `dependsOn` when a task genuinely requires an earlier task's merged changes.",
  ].join("\n");
}

export function buildConvoyCompletionSummaryPrompt(plan: ConvoyPlan): string {
  const lines: string[] = [
    `The parallel subtask convoy "${plan.title}" (${plan.id}) has finished with status: ${plan.status}.`,
    plan.mergedAt
      ? "All leaf subtask changes have been merged into this session's workspace."
      : "Here are the results from each subtask:",
    "",
  ];
  for (const task of plan.tasks) {
    const statusTag = task.status.toUpperCase();
    const detail = task.summary?.trim() || task.error?.trim() || "(no summary)";
    lines.push(`- **${task.id}** [${statusTag}]${task.sessionID ? ` (session \`${task.sessionID}\`)` : ""}: ${detail}`);
  }
  lines.push(
    "",
    "Please review the completed subtask outcomes, verify the integrated state if needed, and summarize the final result."
  );
  return lines.join("\n");
}

export async function spawnBackgroundSubtask(
  promptText: string,
  parentSessionID?: string | null,
  directory?: string | null
): Promise<ConvoyPlan> {
  const cleanPrompt = promptText.trim();
  if (!cleanPrompt) {
    throw new Error("Subtask prompt is required");
  }
  const sid = parentSessionID ?? workspace.activeSessionID;
  const dir = (directory || (sid ? sessionDirectory(sid) : "") || workspace.rootDir || "").trim();
  if (!dir) {
    throw new Error("Workspace directory is required to spawn a subtask");
  }
  const firstLine = cleanPrompt.split(/\r?\n/)[0]?.trim() || "Subtask";
  const title = firstLine.length > 64 ? `${firstLine.slice(0, 64)}…` : firstLine;
  const selected = workspace.selectedModel;
  const created = await apiCreateConvoyPlan({
    directory: dir,
    ...(sid ? { parentSessionID: sid } : {}),
    title,
    mode: "build",
    ...(selected?.providerID ? { providerID: selected.providerID } : {}),
    ...(selected?.modelID ? { model: selected.modelID } : {}),
    concurrency: 1,
    tasks: [{ id: "subtask-1", prompt: cleanPrompt }],
  });
  recordSessionConvoyPlan(sid, created);
  return created;
}

export function draftConvoyPlan(request: ConvoyDraftRequest): Promise<ConvoyPlanRequest> {
  return apiDraftConvoyPlan(request);
}

export async function createConvoyPlan(request: ConvoyPlanRequest): Promise<ConvoyPlan> {
  const created = await apiCreateConvoyPlan(request);
  if (request.parentSessionID) {
    recordSessionConvoyPlan(request.parentSessionID, created);
  }
  return created;
}

export function fetchConvoyPlans(directory: string): Promise<ConvoyPlan[]> {
  return apiFetchConvoyPlans(directory);
}

export function fetchConvoyPlansForSession(sessionID: string): Promise<ConvoyPlan[]> {
  return apiFetchConvoyPlansForSession(sessionID);
}

export function fetchConvoyPlan(planID: string): Promise<ConvoyPlan> {
  return apiFetchConvoyPlan(planID);
}

export function cancelConvoyPlan(planID: string): Promise<ConvoyPlan> {
  return apiCancelConvoyPlan(planID);
}

export function retryConvoyPlan(planID: string): Promise<ConvoyPlan> {
  return apiRetryConvoyPlan(planID);
}

export function mergeConvoyPlan(planID: string, sessionID?: string): Promise<ConvoyPlan> {
  return apiMergeConvoyPlan(planID, sessionID);
}

export interface ConvoyHistoryLoader {
  /** Resolves the directory's plans, or null when superseded or invalidated. */
  load(directory: string): Promise<ConvoyPlan[] | null>;
  /** Drops every in-flight request (modal closed, directory changed, unmount). */
  invalidate(): void;
}

/**
 * Generation-guarded convoy history loader: a late response for a previous
 * directory or a closed modal can never overwrite the current view.
 */
export function createConvoyHistoryLoader(
  fetcher: (directory: string) => Promise<ConvoyPlan[]> = fetchConvoyPlans
): ConvoyHistoryLoader {
  let generation = 0;
  return {
    async load(directory: string): Promise<ConvoyPlan[] | null> {
      const current = ++generation;
      try {
        const plans = await fetcher(directory);
        return current === generation ? plans : null;
      } catch {
        return null;
      }
    },
    invalidate() {
      generation++;
    },
  };
}
