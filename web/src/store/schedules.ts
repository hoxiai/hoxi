// web/src/store/schedules.ts
import { createMemo, createSignal } from "solid-js";
import { hoxiAPI } from "../services/api";
import { errorMessageOf } from "../services/http";
import type { SessionInfo } from "../services/sse-types";
import {
  schedulesAPI,
  type ScheduleItem,
  type SchedulePatchPayload,
  type SchedulePreviewResponse,
  type ScheduleRunRecord,
  type ScheduleTriggerSpec,
  type ScheduleUpsertPayload,
} from "../services/schedules-api";
import { normalizeFolderPath } from "./workspace-folders";
import { bootstrapProjectSessions, switchWorkspace } from "./workspace-projects";
import { selectSession } from "./workspace-sessions";
import { workspace } from "./workspace-state";

export interface ScheduleModalTarget {
  projectID?: string;
  directory?: string;
  sessionID?: string;
  scheduleID?: string;
  createNew?: boolean;
}

const [schedulesList, setSchedulesList] = createSignal<ScheduleItem[]>([]);
const [schedulesLoading, setSchedulesLoading] = createSignal(false);
const [schedulesError, setSchedulesError] = createSignal<string | null>(null);
const [scheduleModalOpen, setScheduleModalOpen] = createSignal(false);
const [scheduleModalTarget, setScheduleModalTarget] = createSignal<ScheduleModalTarget>({});
const [scheduleRunsByID, setScheduleRunsByID] = createSignal<Record<string, ScheduleRunRecord[]>>({});

let loadSchedulesSeq = 0;
let runsSeqByID: Record<string, number> = {};
let refreshDebounceTimer: ReturnType<typeof setTimeout> | null = null;

export {
  scheduleModalOpen,
  scheduleModalTarget,
  scheduleRunsByID,
  schedulesError,
  schedulesList,
  schedulesLoading,
};

export const unseenScheduleAlertCount = createMemo(
  () => schedulesList().filter((item) => Boolean(item.alertUnseen)).length
);

export function schedulesForWorkspace(directory?: string, projectID?: string): ScheduleItem[] {
  const cleanDir = normalizeFolderPath(directory || "");
  const cleanProj = (projectID || "").trim();
  if (!cleanDir && !cleanProj) return schedulesList();
  return schedulesList().filter((item) => {
    if (cleanProj && item.projectID && item.projectID === cleanProj) return true;
    if (cleanDir && normalizeFolderPath(item.directory) === cleanDir) return true;
    return false;
  });
}

export function unseenScheduleAlertsForWorkspace(directory?: string, projectID?: string): number {
  return schedulesForWorkspace(directory, projectID).filter((item) => Boolean(item.alertUnseen)).length;
}

export function isScheduledTurnSession(sessionID: string): boolean {
  const sid = sessionID.trim();
  if (!sid) return false;
  for (const item of schedulesList()) {
    if (item.running && (item.sessionID === sid || item.lastSessionID === sid)) {
      return true;
    }
  }
  const sess = workspace.sessions.find((s) => s.id === sid);
  return Boolean(sess?.title?.startsWith("[Schedule] "));
}

export function openScheduleModal(target: ScheduleModalTarget = {}): void {
  setScheduleModalTarget({
    projectID: target.projectID?.trim() || workspace.activeProjectID || undefined,
    directory: target.directory?.trim() || workspace.rootDir || undefined,
    sessionID: target.sessionID?.trim() || undefined,
    scheduleID: target.scheduleID?.trim() || undefined,
    createNew: Boolean(target.createNew),
  });
  setScheduleModalOpen(true);
  void loadSchedules().catch(() => {});
}

export function closeScheduleModal(): void {
  setScheduleModalOpen(false);
}

export async function loadSchedules(): Promise<ScheduleItem[]> {
  const seq = ++loadSchedulesSeq;
  setSchedulesLoading(true);
  setSchedulesError(null);
  try {
    const items = await schedulesAPI.list();
    if (seq === loadSchedulesSeq) {
      setSchedulesList(Array.isArray(items) ? items : []);
    }
    return items;
  } catch (err) {
    if (seq === loadSchedulesSeq) {
      setSchedulesError(errorMessageOf(err, "Failed to load scheduled tasks"));
    }
    throw err;
  } finally {
    if (seq === loadSchedulesSeq) {
      setSchedulesLoading(false);
    }
  }
}

export async function loadScheduleRuns(scheduleID: string): Promise<ScheduleRunRecord[]> {
  const id = scheduleID.trim();
  if (!id) return [];
  const seq = (runsSeqByID[id] ?? 0) + 1;
  runsSeqByID[id] = seq;
  const runs = await schedulesAPI.listRuns(id);
  if (runsSeqByID[id] === seq) {
    setScheduleRunsByID((prev) => ({ ...prev, [id]: Array.isArray(runs) ? runs : [] }));
  }
  return runs;
}

export function handleScheduleUpdatedEvent(scheduleID?: string): void {
  if (refreshDebounceTimer !== null) clearTimeout(refreshDebounceTimer);
  refreshDebounceTimer = setTimeout(() => {
    refreshDebounceTimer = null;
    void loadSchedules().catch(() => {});
    const id = (scheduleID || "").trim();
    if (id && scheduleModalOpen()) {
      void loadScheduleRuns(id).catch(() => {});
    }
  }, 150);
}

export const SCHEDULES_POLL_MS = 10_000;

export function startSchedulesPolling(): () => void {
  const timer = setInterval(() => {
    void loadSchedules().catch(() => {});
  }, SCHEDULES_POLL_MS);
  return () => clearInterval(timer);
}

export async function createSchedule(payload: ScheduleUpsertPayload): Promise<ScheduleItem> {
  const created = await schedulesAPI.create(payload);
  setSchedulesList((prev) => [created, ...prev.filter((item) => item.id !== created.id)]);
  return created;
}

export async function updateSchedule(
  id: string,
  payload: SchedulePatchPayload
): Promise<ScheduleItem> {
  const updated = await schedulesAPI.update(id, payload);
  setSchedulesList((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
  return updated;
}

export async function deleteSchedule(id: string): Promise<void> {
  await schedulesAPI.remove(id);
  setSchedulesList((prev) => prev.filter((item) => item.id !== id));
  setScheduleRunsByID((prev) => {
    if (!(id in prev)) return prev;
    const next = { ...prev };
    delete next[id];
    return next;
  });
}

export async function runScheduleNow(id: string): Promise<ScheduleRunRecord> {
  const run = await schedulesAPI.runNow(id);
  setSchedulesList((prev) =>
    prev.map((item) =>
      item.id === id ? { ...item, running: true, lastRunAt: run.startedAt } : item
    )
  );
  setScheduleRunsByID((prev) => ({
    ...prev,
    [id]: [run, ...(prev[id] ?? []).filter((r) => r.id !== run.id)],
  }));
  return run;
}

export async function stopScheduleRun(id: string): Promise<void> {
  await schedulesAPI.stopRun(id);
  void loadSchedules().catch(() => {});
}

export async function acknowledgeScheduleAlert(id: string): Promise<ScheduleItem> {
  const updated = await schedulesAPI.acknowledge(id);
  setSchedulesList((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
  return updated;
}

export async function openScheduleRunSession(
  schedule: ScheduleItem,
  sessionID?: string
): Promise<void> {
  const targetSession = (sessionID || schedule.lastSessionID || schedule.sessionID || "").trim();
  if (schedule.alertUnseen) {
    void acknowledgeScheduleAlert(schedule.id).catch(() => {});
  }
  if (!targetSession) return;
  const dir = schedule.directory.trim();
  const normDir = normalizeFolderPath(dir);
  const normCurrent = normalizeFolderPath(workspace.rootDir);
  if (normDir && normDir !== normCurrent) {
    await switchWorkspace(dir, { targetSessionID: targetSession, selectInitial: false }).catch(() => {});
  } else {
    if (normDir) {
      await bootstrapProjectSessions(dir).catch(() => {});
    }
    await selectSession(targetSession).catch(() => {});
  }
  closeScheduleModal();
}

export function previewScheduleTrigger(
  trigger: ScheduleTriggerSpec,
  count = 4
): Promise<SchedulePreviewResponse> {
  return schedulesAPI.previewTrigger(trigger, count);
}

export async function listSessionsForScheduleDir(
  directory: string,
  projectID?: string
): Promise<SessionInfo[]> {
  const dir = directory.trim();
  if (!dir) return [];
  const items = await hoxiAPI.listSessions(dir, projectID?.trim() || undefined);
  return Array.isArray(items) ? items : [];
}

export function formatScheduleError(err: unknown, fallback: string): string {
  return errorMessageOf(err, fallback);
}
