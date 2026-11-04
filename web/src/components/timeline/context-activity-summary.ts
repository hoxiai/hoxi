// web/src/components/timeline/context-activity-summary.ts
import { $RAW } from "solid-js/store";
import type { MessagePart, ToolPart } from "../../services/sse-types";
import {
  extractChildSubagentSessionId,
  isSessionBusy,
  isSubagentToolAlive,
  resolveSubagentTitle,
} from "../../store/workspace";
import type { IconName } from "../icons";
import { resolveSubagentCurrentStepLabel } from "./SubagentInlineTimeline";
import { liveReasoningTicker, reasoningHeading, reasoningSummary } from "./ThoughtBox";
import {
  extractCommand,
  extractDescription,
  extractTargetFile,
  formatToolDisplayName,
  getFilename,
  isEditingTool,
  isReadTool,
  isSearchOrListTool,
  isShellTool,
  isTaskTool,
} from "./tool-card-helpers";
import {
  isSessionScratchFile,
  normalizeWorkspaceRelPath,
  resolveToolFileDiffs,
} from "./tool-card-state";
import { t as tI18n } from "../../i18n";
import { isAttentionToolPart } from "./context-activity-parts";

export type StepDisplay = { action: string; detail: string };

function basename(p: string): string {
  const parts = p.split("/").filter(Boolean);
  return parts.length ? parts[parts.length - 1] : p;
}

export function summarizeStepTarget(part: ToolPart): string {
  if (isTaskTool(part.tool)) {
    const childID = extractChildSubagentSessionId(part);
    const rawDesc = extractDescription(part);
    const desc = rawDesc && resolveSubagentTitle(part.sessionID, childID, rawDesc).label;
    const childStep = resolveSubagentCurrentStepLabel(childID);
    if (desc && childStep) return `${desc} · ${childStep}`;
    return desc || childStep || "";
  }
  const file = extractTargetFile(part);
  if (file) return getFilename(file) || basename(file);
  if (isShellTool(part.tool)) {
    const desc = extractDescription(part);
    if (desc) return desc;
    const cmd = extractCommand(part);
    return cmd ? `$ ${cmd}` : "";
  }
  const desc = extractDescription(part);
  if (desc) return desc;
  const inp = part.state.input || {};
  if (typeof inp.pattern === "string" && inp.pattern.trim()) {
    return `"${inp.pattern.trim()}"`;
  }
  return "";
}

interface TargetBucket {
  preview: string[];
  seen: Set<string>;
}

const makeBucket = (): TargetBucket => ({ preview: [], seen: new Set() });

const addBucketTarget = (bucket: TargetBucket, val: string) => {
  const clean = val.trim();
  if (!clean || bucket.seen.has(clean)) return;
  bucket.seen.add(clean);
  if (bucket.preview.length < 6) bucket.preview.push(clean);
};

const buildBucketTooltip = (
  header: string,
  bucket: TargetBucket,
  tailItems: string[]
): string => {
  let total = bucket.seen.size;
  let preview = bucket.preview;
  let tailSeen: Set<string> | undefined;
  for (const raw of tailItems) {
    const clean = raw.trim();
    if (!clean || bucket.seen.has(clean) || tailSeen?.has(clean)) continue;
    (tailSeen ||= new Set()).add(clean);
    total++;
    if (preview.length < 6) {
      if (preview === bucket.preview) preview = preview.slice();
      preview.push(clean);
    }
  }
  if (total === 0) return header;
  const lines = preview.map((item) => `• ${item}`).join("\n");
  const extra = total > 6 ? `\n… +${total - 6}` : "";
  return `${header}\n${lines}${extra}`;
};

interface PrefixSnap {
  id: string;
  tool?: string;
  status?: string;
  timeStart?: number;
  timeEnd?: number;
  error?: string;
  inputRef?: unknown;
  metaRef?: unknown;
}

const rawOf = <T,>(v: T): T =>
  v && typeof v === "object" ? (v as { [$RAW]?: T })[$RAW] ?? v : v;

const snapPart = (raw: MessagePart): PrefixSnap => {
  if (raw.type === "tool") {
    const st = raw.state as {
      status: string;
      time?: { start?: number; end?: number };
      error?: string;
      input?: unknown;
      metadata?: unknown;
    };
    return {
      id: raw.id,
      tool: raw.tool,
      status: st.status,
      timeStart: st.time?.start,
      timeEnd: st.time?.end,
      error: st.error,
      inputRef: rawOf(st.input),
      metaRef: rawOf(st.metadata),
    };
  }
  const rt = (raw as { time?: { start?: number; end?: number } }).time;
  return { id: raw.id, timeStart: rt?.start, timeEnd: rt?.end };
};

const isSnapValid = (raw: MessagePart | undefined, s: PrefixSnap): boolean => {
  if (!raw || raw.id !== s.id) return false;
  if (raw.type !== "tool") {
    const rt = (raw as { time?: { start?: number; end?: number } }).time;
    return rt?.start === s.timeStart && rt?.end === s.timeEnd;
  }
  const st = raw.state as {
    status: string;
    time?: { start?: number; end?: number };
    error?: string;
    input?: unknown;
    metadata?: unknown;
  };
  return (
    raw.tool === s.tool &&
    st.status === s.status &&
    st.time?.start === s.timeStart &&
    st.time?.end === s.timeEnd &&
    st.error === s.error &&
    rawOf(st.input) === s.inputRef &&
    rawOf(st.metadata) === s.metaRef
  );
};

export interface ActivityPrefixCache {
  cursor: number;
  snaps: PrefixSnap[];
  editIndices: number[];
  reads: number;
  searches: number;
  edits: number;
  commands: number;
  subagents: number;
  otherTools: number;
  thoughts: number;
  readBucket: TargetBucket;
  editBucket: TargetBucket;
  cmdBucket: TargetBucket;
  searchBucket: TargetBucket;
  subagentBucket: TargetBucket;
  attentionTools: ToolPart[];
  lastTool: ToolPart | undefined;
  start: number | undefined;
  end: number | undefined;
}

export function createPrefixCache(): ActivityPrefixCache {
  return {
    cursor: 0,
    snaps: [],
    editIndices: [],
    reads: 0,
    searches: 0,
    edits: 0,
    commands: 0,
    subagents: 0,
    otherTools: 0,
    thoughts: 0,
    readBucket: makeBucket(),
    editBucket: makeBucket(),
    cmdBucket: makeBucket(),
    searchBucket: makeBucket(),
    subagentBucket: makeBucket(),
    attentionTools: [],
    lastTool: undefined,
    start: undefined,
    end: undefined,
  };
}

function isSettledPrefixStep(p: MessagePart): boolean {
  if (p.type === "tool") {
    return !isTaskTool(p.tool) && (p.state.status === "completed" || p.state.status === "error");
  }
  if (p.type === "reasoning") {
    return p.time?.end != null;
  }
  return true;
}

function accumulatePrefixStep(prefixCache: ActivityPrefixCache, p: MessagePart, index: number) {
  if (p.type === "reasoning") {
    prefixCache.thoughts++;
    if (p.time?.start && (!prefixCache.start || p.time.start < prefixCache.start)) {
      prefixCache.start = p.time.start;
    }
    if (p.time?.end && (!prefixCache.end || p.time.end > prefixCache.end)) {
      prefixCache.end = p.time.end;
    }
    return;
  }
  if (p.type !== "tool") return;
  prefixCache.lastTool = p;
  const target = summarizeStepTarget(p);
  if (isReadTool(p.tool)) {
    prefixCache.reads++;
    addBucketTarget(prefixCache.readBucket, target);
  } else if (isEditingTool(p.tool)) {
    prefixCache.edits++;
    prefixCache.editIndices.push(index);
    addBucketTarget(prefixCache.editBucket, target);
  } else if (isShellTool(p.tool)) {
    prefixCache.commands++;
    addBucketTarget(prefixCache.cmdBucket, target);
  } else if (isSearchOrListTool(p.tool)) {
    prefixCache.searches++;
    addBucketTarget(prefixCache.searchBucket, target);
  } else if (isTaskTool(p.tool)) {
    prefixCache.subagents++;
    addBucketTarget(prefixCache.subagentBucket, target);
  } else {
    prefixCache.otherTools++;
  }
  if (isAttentionToolPart(p)) {
    prefixCache.attentionTools.push(p);
  }
  if (p.state.status !== "pending") {
    const s = p.state.time?.start;
    if (s && (!prefixCache.start || s < prefixCache.start)) prefixCache.start = s;
    if (p.state.status === "completed" || p.state.status === "error") {
      const e = p.state.time?.end;
      if (e && (!prefixCache.end || e > prefixCache.end)) prefixCache.end = e;
    }
  }
}

export interface ActivityStepSummary {
  stats: {
    toolCount: number;
    thoughts: number;
    badges: Array<{ icon: IconName; count: number; tooltip: string }>;
  };
  runningTools: ToolPart[];
  activeReasoning: Extract<MessagePart, { type: "reasoning" }> | undefined;
  lastTool: ToolPart | undefined;
  hasRunningPart: boolean;
  diffStats: { additions: number; deletions: number } | undefined;
  attentionTools: ToolPart[];
  timeRange: { start: number | undefined; end: number | undefined };
}

export function computeActivityStepSummary(
  parts: MessagePart[],
  startedAt: number | undefined,
  cacheRef: { current: ActivityPrefixCache }
): ActivityStepSummary {
  let prefixCache = cacheRef.current;
  const len = parts.length;
  const rawParts = rawOf(parts);
  let cacheValid = len >= prefixCache.cursor;
  for (let i = 0; cacheValid && i < prefixCache.cursor; i++) {
    cacheValid = isSnapValid(rawOf(rawParts[i]), prefixCache.snaps[i]);
  }
  if (!cacheValid) {
    prefixCache = createPrefixCache();
    cacheRef.current = prefixCache;
  }
  while (prefixCache.cursor < len - 1) {
    const idx = prefixCache.cursor;
    const raw = rawOf(rawParts[idx]);
    if (!raw || !isSettledPrefixStep(raw)) break;
    accumulatePrefixStep(prefixCache, raw, idx);
    prefixCache.snaps.push(snapPart(raw));
    prefixCache.cursor++;
  }
  const tailWatchStart = Math.max(0, prefixCache.cursor - 32);
  for (let i = tailWatchStart; i < prefixCache.cursor; i++) {
    const rp = parts[i];
    if (rp?.type === "tool") {
      void rp.state.status;
      void rp.state.metadata;
      if (rp.state.status === "completed" || rp.state.status === "error") void rp.state.time?.end;
    }
  }

  let reads = prefixCache.reads;
  let searches = prefixCache.searches;
  let edits = prefixCache.edits;
  let commands = prefixCache.commands;
  let subagents = prefixCache.subagents;
  let otherTools = prefixCache.otherTools;
  let thoughts = prefixCache.thoughts;
  const tailReads: string[] = [];
  const tailEdits: string[] = [];
  const tailCmds: string[] = [];
  const tailSearches: string[] = [];
  const tailSubagents: string[] = [];
  const runningTools: ToolPart[] = [];
  let diffByFile: Map<string, { additions: number; deletions: number }> | undefined;
  let tailAttention: ToolPart[] | undefined;
  let start = startedAt && startedAt > 0 ? startedAt : prefixCache.start;
  if (prefixCache.start && (!start || prefixCache.start < start)) start = prefixCache.start;
  let end = prefixCache.end;
  let hasRunningPart = false;
  let activeReasoning: Extract<MessagePart, { type: "reasoning" }> | undefined;
  let lastTool = prefixCache.lastTool;

  const recordToolDiffs = (p: ToolPart) => {
    if (p.state.status !== "completed") return;
    const fileTarget = extractTargetFile(p) || "";
    for (const d of resolveToolFileDiffs(p, true, fileTarget)) {
      if (!d.file || isSessionScratchFile(normalizeWorkspaceRelPath(d.file), p.sessionID)) {
        continue;
      }
      (diffByFile ||= new Map()).set(d.file, {
        additions: d.additions || 0,
        deletions: d.deletions || 0,
      });
    }
  };
  for (const idx of prefixCache.editIndices) {
    const ep = parts[idx];
    if (ep?.type === "tool") recordToolDiffs(ep);
  }

  for (let i = prefixCache.cursor; i < len; i++) {
    const p = parts[i];
    if (p.type === "reasoning") {
      thoughts++;
      if (p.time?.start && (!start || p.time.start < start)) start = p.time.start;
      if (p.time?.end && (!end || p.time.end > end)) end = p.time.end;
      if (p.time?.end == null) {
        activeReasoning = p;
        if (!hasRunningPart && isSessionBusy(p.sessionID)) hasRunningPart = true;
      }
    } else if (p.type === "tool") {
      lastTool = p;
      const status = p.state.status;
      if (status === "pending" || status === "running") {
        runningTools.push(p);
        if (!hasRunningPart) {
          hasRunningPart = isTaskTool(p.tool)
            ? isSubagentToolAlive(p, p.sessionID)
            : isSessionBusy(p.sessionID);
        }
      }
      const target = summarizeStepTarget(p);
      if (isReadTool(p.tool)) {
        reads++;
        if (target) tailReads.push(target);
      } else if (isEditingTool(p.tool)) {
        edits++;
        if (target) tailEdits.push(target);
        recordToolDiffs(p);
      } else if (isShellTool(p.tool)) {
        commands++;
        if (target) tailCmds.push(target);
      } else if (isSearchOrListTool(p.tool)) {
        searches++;
        if (target) tailSearches.push(target);
      } else if (isTaskTool(p.tool)) {
        subagents++;
        if (target) tailSubagents.push(target);
      } else {
        otherTools++;
      }
      if (isAttentionToolPart(p)) {
        (tailAttention ||= prefixCache.attentionTools.slice()).push(p);
      }
      if (status !== "pending") {
        const s = p.state.time?.start;
        if (s && (!start || s < start)) start = s;
        if (status === "completed" || status === "error") {
          const e = p.state.time?.end;
          if (e && (!end || e > end)) end = e;
        }
      }
    }
  }

  const toolCount = reads + searches + edits + commands + subagents + otherTools;
  const badges: Array<{ icon: IconName; count: number; tooltip: string }> = [];
  const pushBadge = (
    icon: IconName,
    count: number,
    key: Parameters<typeof tI18n>[0],
    bucket: TargetBucket,
    tail: string[]
  ) => {
    if (count > 0) {
      badges.push({
        icon,
        count,
        tooltip: buildBucketTooltip(tI18n(key, { count }), bucket, tail),
      });
    }
  };
  pushBadge("glasses", reads, "timeline.reads", prefixCache.readBucket, tailReads);
  pushBadge("edit-small-2", edits, "timeline.editsCount", prefixCache.editBucket, tailEdits);
  pushBadge("console", commands, "timeline.commandsCount", prefixCache.cmdBucket, tailCmds);
  pushBadge("magnifying-glass", searches, "timeline.searches", prefixCache.searchBucket, tailSearches);
  pushBadge("task", subagents, "timeline.subagentsCount", prefixCache.subagentBucket, tailSubagents);

  let additions = 0;
  let deletions = 0;
  if (diffByFile) {
    for (const item of diffByFile.values()) {
      additions += item.additions;
      deletions += item.deletions;
    }
  }
  const diffStats = additions > 0 || deletions > 0 ? { additions, deletions } : undefined;
  const attentionTools = tailAttention ?? prefixCache.attentionTools;

  return {
    stats: { toolCount, thoughts, badges },
    runningTools,
    activeReasoning,
    lastTool,
    hasRunningPart,
    diffStats,
    attentionTools,
    timeRange: { start, end },
  };
}

export function resolveActiveStepDisplay(
  summary: ActivityStepSummary,
  previous?: StepDisplay
): StepDisplay {
  const { runningTools, activeReasoning, lastTool } = summary;
  if (runningTools.length > 0) {
    // Show the newest running step whose target is known; steps whose arguments haven't arrived keep the bar as it was.
    const targeted = runningTools.findLast((tool) => summarizeStepTarget(tool) !== "");
    const awaitingArgs = runningTools.every((tool) => Object.keys(tool.state.input ?? {}).length === 0);
    if (!targeted && awaitingArgs && previous?.detail) return previous;
    const active = targeted ?? runningTools[runningTools.length - 1];
    const detail = targeted ? summarizeStepTarget(targeted) : "";
    const extra = detail && runningTools.length > 1 ? ` (+${runningTools.length - 1})` : "";
    return { action: formatToolDisplayName(active.tool, active), detail: `${detail}${extra}`.trim() };
  }

  if (activeReasoning) {
    const heading = liveReasoningTicker(activeReasoning.text ?? "", true);
    return {
      action: tI18n("timeline.thinking"),
      detail: heading || "",
    };
  }

  // Fallback during brief transitions between steps while `isLiveTail` is true
  if (lastTool) {
    return {
      action: formatToolDisplayName(lastTool.tool, lastTool),
      detail: summarizeStepTarget(lastTool),
    };
  }

  return {
    action: tI18n("timeline.executing"),
    detail: "",
  };
}

export function extractThoughtOnlyPreview(parts: MessagePart[], toolCount: number): string {
  if (toolCount > 0) return "";
  for (let i = parts.length - 1; i >= 0; i--) {
    const p = parts[i];
    if (p.type === "reasoning" && p.text?.trim()) {
      const cleaned = p.text.replace(/\[REDACTED\]/g, "").trim();
      const parsed = reasoningHeading(cleaned, false);
      return parsed.title || reasoningSummary(parsed.body);
    }
  }
  return "";
}
