// web/src/store/intake.ts
import { createMemo, createSignal } from "solid-js";
import { createStore } from "solid-js/store";
import { errorMessageOf } from "../services/http";
import {
  intakeAPI,
  type IntakeLinkItem,
  type IntakeTaskItem,
} from "../services/intake-api";
import { resolveConvoyModelForSession } from "./orchestration";
import { bootstrapProjectSessions } from "./workspace-projects";
import { selectSession } from "./workspace-sessions";
import { workspace } from "./workspace-state";

const [intakeLinks, setIntakeLinks] = createSignal<IntakeLinkItem[]>([]);
const [intakeTasks, setIntakeTasks] = createSignal<IntakeTaskItem[]>([]);
const [intakeLoading, setIntakeLoading] = createSignal(false);
const [intakeLinksModalOpen, setIntakeLinksModalOpen] = createSignal(false);

let intakeTasksSeq = 0;
let intakeTasksScope = "";
let intakeLinksSeq = 0;

export {
  intakeLinks,
  intakeLinksModalOpen,
  intakeLoading,
  intakeTasks,
  setIntakeLinksModalOpen,
};

export const pendingIntakeCount = createMemo(
  () =>
    intakeTasks().filter(
      (item) => item.status === "staged" || item.status === "reopened"
    ).length
);

export function resetIntakeState(): void {
  intakeTasksSeq++;
  intakeLinksSeq++;
  intakeTasksScope = "";
  setIntakeTasks([]);
  setIntakeLinks([]);
  setIntakeLoading(false);
}

export async function loadIntakeLinks(directory: string): Promise<IntakeLinkItem[]> {
  const cleanDir = directory.trim();
  if (!cleanDir) {
    setIntakeLinks([]);
    return [];
  }
  const seq = ++intakeLinksSeq;
  const list = await intakeAPI.listLinks(cleanDir);
  if (seq === intakeLinksSeq) {
    setIntakeLinks(list);
  }
  return list;
}

export function parseScopedPathInput(raw: string): string[] {
  if (!raw || !raw.trim()) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const piece of raw.split(/[\n,;，；、]/)) {
    const cleaned = piece.trim();
    if (!cleaned || seen.has(cleaned)) continue;
    seen.add(cleaned);
    out.push(cleaned);
  }
  return out;
}

export async function createIntakeLink(payload: {
  directory: string;
  label: string;
  projectName?: string;
  readOnlyPaths?: string[];
  writablePaths?: string[];
  expiresInHours?: number;
}): Promise<IntakeLinkItem> {
  const created = await intakeAPI.createLink(payload);
  await loadIntakeLinks(payload.directory);
  return created;
}

export async function updateIntakeLink(payload: {
  id: string;
  directory: string;
  label?: string;
  readOnlyPaths?: string[];
  writablePaths?: string[];
}): Promise<IntakeLinkItem> {
  const updated = await intakeAPI.updateLink({
    id: payload.id,
    label: payload.label,
    readOnlyPaths: payload.readOnlyPaths ?? [],
    writablePaths: payload.writablePaths ?? [],
  });
  setIntakeLinks((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
  if (payload.directory.trim()) {
    void loadIntakeLinks(payload.directory).catch(() => {});
  }
  return updated;
}

export async function removeIntakeLink(id: string, directory: string): Promise<void> {
  await intakeAPI.deleteLink(id);
  setIntakeLinks((prev) => prev.filter((item) => item.id !== id));
  if (directory.trim()) {
    void loadIntakeLinks(directory).catch(() => {});
  }
}

export async function loadIntakeTasks(directory: string): Promise<IntakeTaskItem[]> {
  const cleanDir = directory.trim();
  if (!cleanDir) {
    resetIntakeState();
    return [];
  }
  const seq = ++intakeTasksSeq;
  if (cleanDir !== intakeTasksScope) {
    intakeTasksScope = cleanDir;
    setIntakeTasks([]);
  }
  setIntakeLoading(true);
  try {
    const list = await intakeAPI.listTasks(cleanDir);
    if (seq === intakeTasksSeq) {
      setIntakeTasks(list);
    }
    return list;
  } finally {
    if (seq === intakeTasksSeq) {
      setIntakeLoading(false);
    }
  }
}

export const INTAKE_TASKS_POLL_MS = 3500;

export function startIntakeTasksPolling(directory: string): () => void {
  const cleanDir = directory.trim();
  if (!cleanDir) return () => {};
  const timer = setInterval(() => {
    void loadIntakeTasks(cleanDir).catch(() => {});
  }, INTAKE_TASKS_POLL_MS);
  return () => clearInterval(timer);
}

export async function approveIntakeTask(
  taskID: string,
  options?: { openSession?: boolean }
): Promise<IntakeTaskItem> {
  const selected = resolveConvoyModelForSession(workspace.activeSessionID);
  const updated = await intakeAPI.approveTask({
    taskId: taskID,
    ...selected,
  });
  setIntakeTasks((prev) =>
    prev.map((item) => (item.id === updated.id ? updated : item))
  );
  const dir = (updated.directory || intakeTasksScope || workspace.rootDir || "").trim();
  if (dir) {
    await bootstrapProjectSessions(dir).catch(() => {});
  }
  if (options?.openSession !== false && updated.sessionID) {
    await selectSession(updated.sessionID).catch(() => {});
  }
  return updated;
}

export async function approveIntakeTasksBatch(taskIDs: readonly string[]): Promise<void> {
  const ids = taskIDs.map((id) => id.trim()).filter(Boolean);
  if (ids.length === 0) return;
  await Promise.all(
    ids.map((id) => approveIntakeTask(id, { openSession: false }))
  );
}

export async function updateIntakeTaskStatus(payload: {
  taskId: string;
  status: "awaiting_retest" | "rejected" | "resolved" | "cancel";
  developerNote?: string;
}): Promise<IntakeTaskItem> {
  const updated = await intakeAPI.updateTaskStatus(payload);
  setIntakeTasks((prev) =>
    prev.map((item) => (item.id === updated.id ? updated : item))
  );
  return updated;
}

export function formatIntakeError(err: unknown, fallback: string): string {
  return errorMessageOf(err, fallback);
}

const MAX_INTAKE_EXPANSIONS = 256;
const intakeExpansionOrder: string[] = [];
const [intakeExpandedMap, setIntakeExpandedMap] = createStore<Record<string, boolean | undefined>>({});

export function isIntakeTaskExpanded(taskID: string, status: string): boolean {
  const stored = intakeExpandedMap[taskID];
  if (stored !== undefined) return stored;
  return status === "staged" || status === "reopened";
}

export function toggleIntakeTaskExpanded(taskID: string, status: string): void {
  const next = !isIntakeTaskExpanded(taskID, status);
  const idx = intakeExpansionOrder.indexOf(taskID);
  if (idx >= 0) intakeExpansionOrder.splice(idx, 1);
  intakeExpansionOrder.push(taskID);
  setIntakeExpandedMap(taskID, next);
  while (intakeExpansionOrder.length > MAX_INTAKE_EXPANSIONS) {
    const oldest = intakeExpansionOrder.shift();
    if (oldest !== undefined) setIntakeExpandedMap(oldest, undefined);
  }
}

