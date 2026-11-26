// web/src/components/tasks/portal-timeline-helpers.ts
// Pure grouping, diff-line aggregation, and chronological conversation interleaving
// shared by external QA / collaboration portals so process steps fold cleanly without card clutter.
import type {
  IntakeTaskComment,
  SanitizedExecutionStep,
  SanitizedExecutionTurn,
  SanitizedTodoItem,
} from "../../services/intake-api";

export interface PortalFileChangeSummary {
  file: string;
  additions: number;
  deletions: number;
}

export interface PortalDiffSummary {
  additions: number;
  deletions: number;
  files: PortalFileChangeSummary[];
  editStepCount: number;
}

export interface PortalActivitySegment {
  kind: "activity";
  key: string;
  tools: SanitizedExecutionStep[];
  latestTodos: SanitizedTodoItem[];
  toolCount: number;
  editCount: number;
  hasRunning: boolean;
  hasError: boolean;
  activeLabel: string;
  diff: PortalDiffSummary;
  startTime?: number;
  endTime?: number;
}

export interface PortalTextSegment {
  kind: "text";
  key: string;
  text: string;
}

export type PortalTimelineSegment = PortalActivitySegment | PortalTextSegment;

export type PortalConversationEntry =
  | {
      kind: "execution";
      key: string;
      turns: SanitizedExecutionTurn[];
      createdAt: number;
    }
  | {
      kind: "comment";
      key: string;
      comment: IntakeTaskComment;
      createdAt: number;
    };

// Matches the sanitized single-file edit label prefix ("修改 ") without raw CJK literals.
const EDIT_LABEL_PREFIX = "\u4fee\u6539 ";
const GENERIC_EDIT_LABEL = "\u4fee\u6539\u9879\u76ee\u6587\u4ef6";
const MULTI_FILE_LABEL_RE = /\s\u7b49\s+\d+\s+\u4e2a\u6587\u4ef6$/;

function clampNonNeg(value?: number): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) return 0;
  return Math.floor(value);
}

function inferSingleFilenameFromLabel(label?: string): string {
  const trimmed = (label || "").trim();
  if (!trimmed || trimmed === GENERIC_EDIT_LABEL || MULTI_FILE_LABEL_RE.test(trimmed)) {
    return "";
  }
  if (trimmed.startsWith(EDIT_LABEL_PREFIX)) {
    return trimmed.slice(EDIT_LABEL_PREFIX.length).trim();
  }
  return "";
}

export function extractStepFileChanges(step: SanitizedExecutionStep): PortalFileChangeSummary[] {
  if (step.kind !== "tool" || step.category !== "edit" || step.status === "error") {
    return [];
  }
  if (Array.isArray(step.files) && step.files.length > 0) {
    const out: PortalFileChangeSummary[] = [];
    for (const item of step.files) {
      const file = (item?.file || "").trim();
      const additions = clampNonNeg(item?.additions);
      const deletions = clampNonNeg(item?.deletions);
      if (file) {
        out.push({ file, additions, deletions });
      }
    }
    if (out.length > 0) return out;
  }
  const additions = clampNonNeg(step.additions);
  const deletions = clampNonNeg(step.deletions);
  const inferred = inferSingleFilenameFromLabel(step.label);
  if (inferred && (additions > 0 || deletions > 0)) {
    return [{ file: inferred, additions, deletions }];
  }
  return [];
}

export function summarizeStepsDiff(steps: readonly SanitizedExecutionStep[]): PortalDiffSummary {
  let additions = 0;
  let deletions = 0;
  let editStepCount = 0;
  const order: string[] = [];
  const byFile = new Map<string, PortalFileChangeSummary>();

  for (const step of steps) {
    if (step.kind !== "tool" || step.category !== "edit" || step.status === "error") {
      continue;
    }
    editStepCount++;
    const stepAdds = clampNonNeg(step.additions);
    const stepDels = clampNonNeg(step.deletions);
    const fileList = extractStepFileChanges(step);

    if (fileList.length > 0) {
      let fileAdds = 0;
      let fileDels = 0;
      for (const f of fileList) {
        fileAdds += f.additions;
        fileDels += f.deletions;
        const prev = byFile.get(f.file);
        if (prev) {
          prev.additions += f.additions;
          prev.deletions += f.deletions;
        } else {
          order.push(f.file);
          byFile.set(f.file, { ...f });
        }
      }
      additions += Math.max(stepAdds, fileAdds);
      deletions += Math.max(stepDels, fileDels);
    } else {
      additions += stepAdds;
      deletions += stepDels;
    }
  }

  return {
    additions,
    deletions,
    files: order.map((name) => byFile.get(name)!),
    editStepCount,
  };
}

export function summarizeTimelineDiff(turns: readonly SanitizedExecutionTurn[]): PortalDiffSummary {
  const allSteps: SanitizedExecutionStep[] = [];
  for (const turn of turns) {
    for (const step of turn.steps ?? []) {
      allSteps.push(step);
    }
  }
  return summarizeStepsDiff(allSteps);
}

export function groupSanitizedTurns(
  turns: readonly SanitizedExecutionTurn[]
): PortalTimelineSegment[] {
  const segments: PortalTimelineSegment[] = [];
  let bufferedTools: SanitizedExecutionStep[] = [];
  let bufferedTodos: SanitizedTodoItem[] = [];
  let bufferedFirstKey = "";
  let bufferedTodoLabel = "";

  const flushActivity = () => {
    if (bufferedTools.length === 0 && bufferedTodos.length === 0) return;

    let editCount = 0;
    let hasRunning = false;
    let hasError = false;
    let startTime: number | undefined;
    let endTime: number | undefined;
    let runningLabel = "";
    let lastToolLabel = "";

    for (const tool of bufferedTools) {
      if (tool.category === "edit") editCount++;
      if (tool.status === "running" || tool.status === "pending") {
        hasRunning = true;
        if (tool.label) runningLabel = tool.label;
      }
      if (tool.status === "error") hasError = true;
      if (tool.label) lastToolLabel = tool.label;
      if (tool.startTime && tool.startTime > 0) {
        startTime = startTime === undefined ? tool.startTime : Math.min(startTime, tool.startTime);
      }
      if (tool.endTime && tool.endTime > 0) {
        endTime = endTime === undefined ? tool.endTime : Math.max(endTime, tool.endTime);
      }
    }

    segments.push({
      kind: "activity",
      key: bufferedFirstKey || `act_${segments.length}`,
      tools: bufferedTools,
      latestTodos: bufferedTodos,
      toolCount: bufferedTools.length,
      editCount,
      hasRunning,
      hasError,
      activeLabel: runningLabel || lastToolLabel || bufferedTodoLabel,
      diff: summarizeStepsDiff(bufferedTools),
      startTime,
      endTime,
    });

    bufferedTools = [];
    bufferedTodos = [];
    bufferedFirstKey = "";
    bufferedTodoLabel = "";
  };

  for (const turn of turns) {
    for (const step of turn.steps ?? []) {
      if (step.kind === "text") {
        const text = step.text?.trim();
        if (!text) continue;
        flushActivity();
        segments.push({
          kind: "text",
          key: step.id || `${turn.id}_text_${segments.length}`,
          text: step.text!,
        });
        continue;
      }

      if (step.kind === "todo") {
        const items = step.todos ?? [];
        if (items.length === 0) continue;
        if (!bufferedFirstKey) bufferedFirstKey = step.id || `${turn.id}_todo_${segments.length}`;
        bufferedTodos = items;
        if (step.label) bufferedTodoLabel = step.label;
        continue;
      }

      if (step.kind === "tool") {
        if (!bufferedFirstKey) bufferedFirstKey = step.id || `${turn.id}_tool_${segments.length}`;
        bufferedTools.push(step);
      }
    }
  }

  flushActivity();
  return segments;
}

export function buildPortalConversationEntries(
  turns: readonly SanitizedExecutionTurn[],
  comments: readonly IntakeTaskComment[]
): PortalConversationEntry[] {
  const followups = comments.filter((c) => c.action !== "submit");
  if (turns.length === 0 && followups.length === 0) return [];

  if (followups.length === 0) {
    return [
      {
        kind: "execution",
        key: `exec_${turns[0]?.id || "0"}`,
        turns: [...turns],
        createdAt: turns[0]?.createdAt || 0,
      },
    ];
  }

  if (turns.length === 0) {
    return followups.map((comment) => ({
      kind: "comment",
      key: `cmt_${comment.id}`,
      comment,
      createdAt: comment.createdAt || 0,
    }));
  }

  type TaggedItem =
    | { kind: "turn"; order: number; ts: number; turn: SanitizedExecutionTurn }
    | { kind: "comment"; order: number; ts: number; comment: IntakeTaskComment };

  const tagged: TaggedItem[] = [];
  let lastTurnTs = 0;
  for (let i = 0; i < turns.length; i++) {
    const turn = turns[i];
    const ts = turn.createdAt > 0 ? turn.createdAt : lastTurnTs;
    if (ts > 0) lastTurnTs = ts;
    tagged.push({ kind: "turn", order: i, ts, turn });
  }
  for (let i = 0; i < followups.length; i++) {
    const comment = followups[i];
    tagged.push({ kind: "comment", order: turns.length + i, ts: comment.createdAt || 0, comment });
  }

  tagged.sort((a, b) => {
    if (a.ts !== b.ts) return a.ts - b.ts;
    if (a.kind !== b.kind) return a.kind === "turn" ? -1 : 1;
    return a.order - b.order;
  });

  const entries: PortalConversationEntry[] = [];
  let turnRun: SanitizedExecutionTurn[] = [];
  let runStartTs = 0;

  const flushTurnRun = () => {
    if (turnRun.length === 0) return;
    entries.push({
      kind: "execution",
      key: `exec_${turnRun[0].id || entries.length}`,
      turns: turnRun,
      createdAt: runStartTs,
    });
    turnRun = [];
    runStartTs = 0;
  };

  for (const item of tagged) {
    if (item.kind === "turn") {
      if (turnRun.length === 0) runStartTs = item.ts;
      turnRun.push(item.turn);
    } else {
      flushTurnRun();
      entries.push({
        kind: "comment",
        key: `cmt_${item.comment.id}`,
        comment: item.comment,
        createdAt: item.ts,
      });
    }
  }
  flushTurnRun();
  return entries;
}

export function selectMatchingPortalTimeline<T extends { taskId?: string }>(
  taskId: string | null | undefined,
  timeline: T | null | undefined
): T | null {
  if (!taskId || !timeline) return null;
  if (timeline.taskId && timeline.taskId !== taskId) return null;
  return timeline;
}

export function formatPortalDuration(start?: number, end?: number): string {
  if (!start || !end || end <= start) return "";
  const sec = (end - start) / 1000;
  if (sec < 0.1) return "";
  if (sec < 10) return `${sec.toFixed(1)}s`;
  if (sec < 60) return `${Math.round(sec)}s`;
  const mins = Math.floor(sec / 60);
  const rem = Math.round(sec % 60);
  return rem > 0 ? `${mins}m ${rem}s` : `${mins}m`;
}
