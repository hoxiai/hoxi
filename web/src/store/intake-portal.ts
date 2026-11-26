// web/src/store/intake-portal.ts
import { createMemo, createSignal } from "solid-js";
import { errorMessageOf } from "../services/http";
import {
  extractPortalRouteToken,
  intakeAPI,
  type IntakeAttachmentRef,
  type IntakeTaskItem,
  type SanitizedExecutionTimeline,
} from "../services/intake-api";

const initialRouteToken = extractPortalRouteToken();

const [portalToken, setPortalToken] = createSignal<string | null>(initialRouteToken);
const [portalProjectName, setPortalProjectName] = createSignal("");
const [portalTesterLabel, setPortalTesterLabel] = createSignal("");
const [portalTasks, setPortalTasks] = createSignal<IntakeTaskItem[]>([]);
const [activePortalTaskID, setActivePortalTaskID] = createSignal<string | null>(null);
const [portalTimeline, setPortalTimeline] = createSignal<SanitizedExecutionTimeline | null>(null);
const [portalLoading, setPortalLoading] = createSignal(false);
const [portalAuthFailed, setPortalAuthFailed] = createSignal(false);
const [portalConnected, setPortalConnected] = createSignal(false);
const [portalError, setPortalError] = createSignal<string | null>(null);

let portalInfoSeq = 0;
let portalTimelineSeq = 0;

export {
  activePortalTaskID,
  portalAuthFailed,
  portalConnected,
  portalError,
  portalLoading,
  portalProjectName,
  portalTasks,
  portalTesterLabel,
  portalTimeline,
  portalToken,
  setPortalError,
};

export function isTesterPortalMode(): boolean {
  return Boolean(portalToken());
}

const externalPortalDetectors: Array<() => boolean> = [];

export function registerExternalPortalDetector(fn: () => boolean): void {
  if (!externalPortalDetectors.includes(fn)) {
    externalPortalDetectors.push(fn);
  }
}

export function isExternalPortalMode(): boolean {
  return externalPortalDetectors.some((fn) => fn());
}

export function isAnyPortalMode(): boolean {
  return isTesterPortalMode() || isExternalPortalMode();
}

export const activePortalTask = createMemo(() => {
  const id = activePortalTaskID();
  if (!id) return null;
  return portalTasks().find((t) => t.id === id) ?? null;
});

export async function refreshPortalInfo(): Promise<void> {
  const tok = portalToken();
  if (!tok) return;
  const seq = ++portalInfoSeq;
  try {
    const info = await intakeAPI.getPortalInfo(tok);
    if (seq !== portalInfoSeq) return;
    setPortalProjectName(info.projectName || "");
    setPortalTesterLabel(info.label || "");
    setPortalTasks(info.tasks || []);
    setPortalAuthFailed(false);
  } catch (err: any) {
    if (seq !== portalInfoSeq) return;
    if (err?.status === 401 || String(err?.message || "").includes("HTTP 401")) {
      setPortalAuthFailed(true);
    } else {
      setPortalError(errorMessageOf(err, "Failed to load portal tasks"));
    }
  }
}

export async function refreshActivePortalTimeline(targetTaskID?: string | null): Promise<void> {
  const tok = portalToken();
  const tid = targetTaskID !== undefined ? targetTaskID : activePortalTaskID();
  if (!tok || !tid) {
    portalTimelineSeq++;
    setPortalTimeline(null);
    return;
  }
  const seq = ++portalTimelineSeq;
  try {
    const tl = await intakeAPI.getPortalTimeline(tok, tid);
    if (seq === portalTimelineSeq && activePortalTaskID() === tid) {
      setPortalTimeline(tl);
    }
  } catch {
    // Keep existing snapshot on transient error
  }
}

export function selectPortalTask(taskID: string | null): void {
  setActivePortalTaskID(taskID);
  setPortalError(null);
  if (!taskID) {
    portalTimelineSeq++;
    setPortalTimeline(null);
    return;
  }
  void refreshActivePortalTimeline(taskID);
}

export function mountTesterPortal(explicitToken?: string): () => void {
  const tok = (explicitToken ?? portalToken() ?? "").trim();
  if (!tok) return () => {};
  setPortalToken(tok);
  setPortalLoading(true);
  void refreshPortalInfo()
    .then(() => {
      const list = portalTasks();
      if (list.length > 0 && !activePortalTaskID()) {
        selectPortalTask(list[0].id);
      }
    })
    .finally(() => setPortalLoading(false));

  let hasConnectedOnce = false;
  const disconnect = intakeAPI.connectPortalStream(tok, {
    onConnectionChange: (ok) => setPortalConnected(ok),
    onEvent: (evt) => {
      if (evt.type === "connected") {
        if (hasConnectedOnce) {
          void refreshPortalInfo();
          if (activePortalTaskID()) {
            void refreshActivePortalTimeline(activePortalTaskID());
          }
        }
        hasConnectedOnce = true;
      } else if (evt.type === "task.updated") {
        void refreshPortalInfo();
        if (evt.taskId && evt.taskId === activePortalTaskID()) {
          void refreshActivePortalTimeline(evt.taskId);
        }
      } else if (evt.type === "timeline.updated") {
        if (!evt.taskId || evt.taskId === activePortalTaskID()) {
          void refreshActivePortalTimeline(activePortalTaskID());
        }
      }
    },
  });

  // Fallback poll while active task or linked session is busy in case reverse proxies buffer SSE frames.
  const pollTimer = setInterval(() => {
    const cur = activePortalTask();
    if (!cur) return;
    const tl = portalTimeline();
    const tlBusy = Boolean(tl && (!tl.taskId || tl.taskId === cur.id) && tl.busy);
    if (cur.status === "running" || tlBusy) {
      void refreshPortalInfo();
      void refreshActivePortalTimeline(cur.id);
    }
  }, 3000);

  return () => {
    clearInterval(pollTimer);
    disconnect();
  };
}

export async function uploadTesterAttachment(file: File): Promise<IntakeAttachmentRef> {
  const tok = portalToken();
  if (!tok) throw new Error("Missing tester token");
  return intakeAPI.uploadPortalFile(tok, file);
}

export async function submitTesterNewTask(input: {
  title: string;
  description: string;
  steps?: string;
  attachmentIds?: string[];
}): Promise<IntakeTaskItem> {
  const tok = portalToken();
  if (!tok) throw new Error("Missing tester token");
  const created = await intakeAPI.submitPortalTask(tok, input);
  setPortalTasks((prev) => [created, ...prev.filter((item) => item.id !== created.id)]);
  selectPortalTask(created.id);
  return created;
}

export async function submitTesterTaskFeedback(input: {
  taskId: string;
  action: "pass" | "reopen" | "comment";
  text?: string;
  attachmentIds?: string[];
}): Promise<IntakeTaskItem> {
  const tok = portalToken();
  if (!tok) throw new Error("Missing tester token");
  const updated = await intakeAPI.submitPortalFeedback(tok, input);
  setPortalTasks((prev) =>
    prev.map((item) => (item.id === updated.id ? updated : item))
  );
  return updated;
}
