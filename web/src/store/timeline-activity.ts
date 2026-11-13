import { createMemo, mapArray, type Accessor } from "solid-js";
import type { MessagePart } from "../services/sse-types";
import type { MessageTurn } from "./workspace-state";

export type TimelineActivityKind = "reasoning" | "explore" | "edit" | "shell" | "task" | "text" | "error";
export interface TimelineActivitySegment {
  id: string;
  messageID: string;
  messageIDs: string[];
  kind: TimelineActivityKind;
  heightPct: number;
  stepCount: number;
  running: boolean;
  time: { start: number; end?: number };
  duration?: number;
  preview: string;
  primaryPart?: MessagePart;
  completed: boolean;
}

type ProjectPart = (part: MessagePart) => MessagePart[];
const identityPart: ProjectPart = (part) => [part];
const KIND_PRIORITY: TimelineActivityKind[] = ["error", "task", "edit", "shell", "explore", "reasoning", "text"];

interface PartMetric {
  part: MessagePart;
  live: boolean;
  runningKind: TimelineActivityKind | null;
  settledKind: TimelineActivityKind | null;
  start?: number;
  end?: number;
}
interface TurnSource {
  turn: MessageTurn;
  role: Accessor<"user" | "assistant">;
  created: Accessor<number>;
  completed: Accessor<number | undefined>;
  preview: Accessor<string>;
  parts: Accessor<PartMetric[]>;
}

function classifyKind(part: MessagePart, completed: boolean): TimelineActivityKind | null {
  if (part.type === "reasoning") {
    if (!completed && !part.time?.end) return "reasoning";
    return part.text ? "reasoning" : null;
  }
  if (part.type === "text") return !completed || part.text ? "text" : null;
  if (part.type !== "tool") return null;
  const name = (part.tool || "").toLowerCase();
  if (name === "todowrite" || name === "todoread") return null;
  if (part.state.status === "error") return "error";
  if (["edit", "write", "patch", "multiedit", "apply_patch"].includes(name)) return "edit";
  if (name === "bash" || name === "shell") return "shell";
  return name === "task" ? "task" : "explore";
}

function projectMetrics(part: MessagePart, project: ProjectPart): PartMetric[] {
  return project(part).map((item) => {
    const time: { start: number; end?: number } | undefined = item.type === "reasoning" ? item.time
      : item.type === "tool" && item.state.status !== "pending" ? item.state.time : undefined;
    const live = item.type === "reasoning" ? Boolean(item.time && !item.time.end)
      : item.type === "tool" && (item.state.status === "pending" || item.state.status === "running");
    return { part: item, live, runningKind: classifyKind(item, false), settledKind: classifyKind(item, true), start: time?.start, end: time?.end };
  });
}

function sameMetrics(a: PartMetric[], b: PartMetric[]): boolean {
  return a.length === b.length && a.every((item, i) => item.part === b[i].part && item.live === b[i].live &&
    item.runningKind === b[i].runningKind && item.settledKind === b[i].settledKind && item.start === b[i].start && item.end === b[i].end);
}

function userPreview(turn: MessageTurn): string {
  const text = turn.parts.filter((part) => part.type === "text").map((part) => part.text).join(" ").replace(/\s+/g, " ").trim();
  return text.length > 72 ? `${text.slice(0, 72)}…` : text;
}

function groupSources(sources: TurnSource[]): TurnSource[][] {
  const groups: TurnSource[][] = [];
  for (const source of sources) {
    if (source.role() === "user" || groups.length === 0) groups.push([]);
    groups[groups.length-1].push(source);
  }
  return groups;
}

function summarizeGroup(sources: TurnSource[]): TimelineActivitySegment {
  const user = sources.find((source) => source.role() === "user");
  const anchor = user ?? sources[0];
  const parts = sources.flatMap((source) => source.role() === "assistant" ? source.parts() : []);
  const running = parts.some((part) => part.live);
  const first = new Map<TimelineActivityKind, MessagePart>();
  let stepCount = 0;
  let start = anchor.created();
  let end: number | undefined;
  for (const part of parts) {
    const kind = running ? part.runningKind : part.settledKind;
    if (kind) { stepCount++; if (!first.has(kind)) first.set(kind, part.part); }
    if (part.start !== undefined) start = Math.min(start, part.start);
    if (part.end !== undefined) end = Math.max(end ?? part.end, part.end);
  }
  if (!running) {
    for (const source of sources) {
      const completed = source.completed();
      if (completed !== undefined) end = Math.max(end ?? completed, completed);
    }
  } else end = undefined;
  const kind = KIND_PRIORITY.find((candidate) => first.has(candidate)) ?? "text";
  return {
    id: anchor.turn.info.id, messageID: anchor.turn.info.id,
    messageIDs: sources.map((source) => source.turn.info.id), kind,
    heightPct: Math.min(100, Math.max(20, stepCount*10)), stepCount, running,
    time: { start, ...(end !== undefined ? { end } : {}) },
    duration: end !== undefined ? Math.max(0, end-start) : undefined,
    preview: user?.preview() ?? "", primaryPart: first.get(kind),
    completed: !running && sources.every((source) => source.role() === "user" || Boolean(source.completed())),
  };
}

function sameSegment(a: TimelineActivitySegment | undefined, b: TimelineActivitySegment): boolean {
  if (!a) return false;
  return a.id === b.id && a.kind === b.kind && a.heightPct === b.heightPct && a.stepCount === b.stepCount &&
    a.running === b.running && a.time.start === b.time.start && a.time.end === b.time.end &&
    a.preview === b.preview && a.primaryPart === b.primaryPart && a.completed === b.completed &&
    a.messageIDs.length === b.messageIDs.length && a.messageIDs.every((id, i) => id === b.messageIDs[i]);
}

export function buildTimelineActivity(turns: MessageTurn[], project: ProjectPart = identityPart): TimelineActivitySegment[] {
  const sources = turns.map((turn): TurnSource => ({
    turn, role: () => turn.info.role, created: () => turn.info.time.created,
    completed: () => turn.info.time.completed, preview: () => userPreview(turn),
    parts: () => turn.parts.flatMap((part) => projectMetrics(part, project)),
  }));
  return groupSources(sources).map(summarizeGroup);
}

/** Stable per-part/per-group computations: text growth alone does not rebuild historical activity. */
export function createTimelineActivity(turns: Accessor<MessageTurn[]>, project: ProjectPart = identityPart): Accessor<TimelineActivitySegment[]> {
  const sources = mapArray(turns, (turn): TurnSource => {
    const role = createMemo(() => turn.info.role);
    const parts = mapArray(() => role() === "assistant" ? turn.parts : [], (part) =>
      createMemo(() => projectMetrics(part, project), [], { equals: sameMetrics }));
    return {
      turn, role, created: createMemo(() => turn.info.time.created),
      completed: createMemo(() => turn.info.time.completed),
      preview: createMemo(() => role() === "user" ? userPreview(turn) : ""),
      parts: createMemo(() => parts().flatMap((part) => part())),
    };
  });
  const groups = createMemo<TurnSource[][]>((previous) => {
    const old = new Map(previous.map((group) => [group[0], group]));
    return groupSources(sources()).map((group) => {
      const cached = old.get(group[0]);
      return cached?.length === group.length && cached.every((source, i) => source === group[i]) ? cached : group;
    });
  }, []);
  const segments = mapArray(groups, (group) => createMemo(() => summarizeGroup(group), undefined, { equals: sameSegment }));
  return createMemo(() => segments().map((segment) => segment()), [], {
    equals: (a, b) => a.length === b.length && a.every((segment, i) => segment === b[i]),
  });
}
