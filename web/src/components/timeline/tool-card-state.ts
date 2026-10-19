// web/src/components/timeline/tool-card-state.ts
import type { MessagePart, SnapshotFileDiff } from "../../services/sse-types";
import { type MessageTurn, workspace } from "../../store/workspace";
import {
  countDiffLines,
  extractTargetFile,
  isEditingTool,
  isTaskTool,
  resolveToolInput,
  type DiagnosticItem,
  type ToolPart,
} from "./tool-card-helpers";

export function normalizeWorkspaceRelPath(raw: string): string {
  let trimmed = (raw || "").trim();
  if (!trimmed) return "";
  const root = workspace.rootDir.replace(/\/+$/, "");
  if (root && (trimmed === root || trimmed.startsWith(`${root}/`))) {
    trimmed = trimmed.slice(root.length).replace(/^\/+/, "");
  } else {
    trimmed = trimmed.replace(/^\.\/+/, "");
  }
  return trimmed.replace(/^\.hoxi\/worktrees\/[^/]+\/+/, "");
}


function matchSessionDiff(
  sessionID: string,
  file: string
): SnapshotFileDiff | undefined {
  const norm = normalizeWorkspaceRelPath(file);
  if (!norm) return undefined;
  const diffs = workspace.diffsBySession[sessionID] ?? [];
  const exact = diffs.find((d) => d.file === norm);
  if (exact) return exact;
  const suffix = diffs.find(
    (d) => d.file.endsWith(`/${norm}`) || norm.endsWith(`/${d.file}`)
  );
  if (suffix) return suffix;
  const wtDiffs = workspace.worktreeDiffsBySession[sessionID] ?? [];
  return (
    wtDiffs.find((d) => d.file === norm) ??
    wtDiffs.find((d) => d.file.endsWith(`/${norm}`) || norm.endsWith(`/${d.file}`))
  );
}

/**
 * Parses OpenCode `apply_patch` input (`*** Update/Add/Delete File: <path>`)
 * into per-file SnapshotFileDiff entries so filenames, +/- line counts, and inline diffs
 * work both while streaming and after completion.
 */
export function parseApplyPatchText(
  patchText: string,
  sessionID?: string
): SnapshotFileDiff[] {
  if (!patchText || typeof patchText !== "string") return [];
  const lines = patchText.replace(/\r\n/g, "\n").split("\n");
  const results: SnapshotFileDiff[] = [];

  let currentFile = "";
  let currentOp: "add" | "update" | "delete" = "update";
  let hunkLines: string[] = [];

  const flushCurrent = () => {
    if (!currentFile) return;
    const normFile = normalizeWorkspaceRelPath(currentFile);
    if (!normFile) return;

    const existing = sessionID ? matchSessionDiff(sessionID, normFile) : undefined;
    if (existing && (existing.patch || existing.before !== undefined || existing.after !== undefined)) {
      results.push(existing);
      return;
    }

    let additions = 0;
    let deletions = 0;
    const hunks: string[][] = [];
    let currentHunk: string[] = [];

    for (const hl of hunkLines) {
      if (hl.startsWith("*** ")) continue;
      if (hl.startsWith("@@")) {
        if (currentHunk.length > 0) hunks.push(currentHunk);
        currentHunk = [];
        continue;
      }
      if (hl.startsWith("+") && !hl.startsWith("+++")) {
        additions++;
        currentHunk.push(hl);
      } else if (hl.startsWith("-") && !hl.startsWith("---")) {
        deletions++;
        currentHunk.push(hl);
      } else if (hl.startsWith(" ") || hl === "") {
        currentHunk.push(hl.startsWith(" ") ? hl : ` ${hl}`);
      } else {
        currentHunk.push(` ${hl}`);
      }
    }
    if (currentHunk.length > 0) hunks.push(currentHunk);

    let oldStart = 1;
    let newStart = 1;
    const formattedHunks = hunks.map((lines) => {
      let oldCount = 0;
      let newCount = 0;
      for (const l of lines) {
        if (l.startsWith("-")) oldCount++;
        else if (l.startsWith("+")) newCount++;
        else {
          oldCount++;
          newCount++;
        }
      }
      const hdr = `@@ -${oldStart},${oldCount} +${newStart},${newCount} @@`;
      oldStart += Math.max(1, oldCount) + 3;
      newStart += Math.max(1, newCount) + 3;
      return `${hdr}\n${lines.join("\n")}`;
    });

    const header = `--- a/${normFile}\n+++ b/${normFile}\n`;
    results.push({
      file: normFile,
      patch: formattedHunks.length > 0 ? `${header}${formattedHunks.join("\n")}\n` : undefined,
      additions,
      deletions,
      status:
        currentOp === "add"
          ? "added"
          : currentOp === "delete"
            ? "deleted"
            : "modified",
    });
  };

  for (const line of lines) {
    const m = line.match(/^\*\*\*\s+(Update|Add|Delete)\s+File:\s*(.+)$/);
    if (m) {
      flushCurrent();
      const op = m[1].toLowerCase();
      currentOp = op === "add" ? "add" : op === "delete" ? "delete" : "update";
      currentFile = m[2].trim();
      hunkLines = [];
      continue;
    }
    if (line.startsWith("*** End Patch")) {
      flushCurrent();
      currentFile = "";
      hunkLines = [];
      continue;
    }
    if (currentFile) {
      hunkLines.push(line);
    }
  }
  flushCurrent();
  return results;
}

export function extractTargetFiles(part: ToolPart): string[] {
  const single = extractTargetFile(part);
  const state = part.state;
  const meta = state.status !== "pending" ? state.metadata : undefined;
  const out: string[] = [];
  const add = (p?: string | null) => {
    const norm = normalizeWorkspaceRelPath(p || "");
    if (norm && !out.includes(norm)) out.push(norm);
  };

  if (single) add(single);
  if (Array.isArray(meta?.files)) {
    for (const f of meta.files) {
      if (typeof f === "string") add(f);
      else if (f && typeof f === "object") {
        const rel = typeof f.relativePath === "string" ? f.relativePath : "";
        const abs = typeof f.filePath === "string" ? f.filePath : "";
        add((rel && !rel.startsWith("..") ? rel : abs) || rel || f.file || f.path);
      }
    }
  }
  const patchText = state.input?.patchText;
  if (typeof patchText === "string") {
    for (const d of parseApplyPatchText(patchText, part.sessionID)) {
      add(d.file);
    }
  }
  return out;
}

export function resolveLoadedContextFiles(part: ToolPart): string[] {
  const state = part.state;
  if (state.status !== "completed") return [];
  const loaded = state.metadata?.loaded;
  if (!Array.isArray(loaded)) return [];
  return loaded
    .filter((entry): entry is string => typeof entry === "string")
    .map((entry) => normalizeWorkspaceRelPath(entry));
}

export function resolveToolArgPills(part: ToolPart): string[] {
  const input = resolveToolInput(part);
  const pills: string[] = [];
  if (typeof input.subagent_type === "string" && input.subagent_type) {
    pills.push(input.subagent_type);
  }
  if (typeof input.offset === "number") pills.push(`offset=${input.offset}`);
  if (typeof input.limit === "number") pills.push(`limit=${input.limit}`);
  if (typeof input.pattern === "string" && input.pattern && extractTargetFile(part)) {
    pills.push(`pattern=${input.pattern}`);
  }
  if (typeof input.include === "string" && input.include) pills.push(`include=${input.include}`);
  if (typeof input.DurationSeconds === "number") pills.push(`${input.DurationSeconds}s`);
  if (part.state.status === "completed") {
    const matches = part.state.metadata?.matches ?? part.state.metadata?.count;
    if (typeof matches === "number") {
      pills.push(`${matches} ${matches === 1 ? "match" : "matches"}`);
    }
  }
  return pills;
}

export function resolveToolTodoItems(part: ToolPart): Array<{ content: string; status: string }> {
  const state = part.state;
  const raw =
    (state.status !== "pending" ? state.metadata?.todos : undefined) ?? state.input?.todos;
  if (!Array.isArray(raw)) return [];
  return raw.filter((item) => item && typeof item.content === "string");
}

export function isRealFileDiff(d: SnapshotFileDiff | undefined): boolean {
  if (!d || !d.file) return false;
  if ((d.additions ?? 0) > 0 || (d.deletions ?? 0) > 0) return true;
  if (d.status === "added" || d.status === "deleted") return true;
  if (typeof d.patch === "string" && d.patch.trim().length > 0) {
    const counts = countDiffLines(d.patch);
    return counts.additions > 0 || counts.deletions > 0;
  }
  if (typeof d.before === "string" && typeof d.after === "string") {
    return d.before !== d.after;
  }
  return false;
}

export function resolveToolFileDiffs(
  part: ToolPart,
  isEdit: boolean,
  primaryFile: string
): SnapshotFileDiff[] {
  if (!isEdit || part.state.status === "error") return [];
  const state = part.state;
  const meta = state.status !== "pending" ? state.metadata : undefined;

  // 1. Multi-file metadata from OpenCode `apply_patch` (`metadata.files`)
  if (Array.isArray(meta?.files) && meta.files.length > 0) {
    const list: SnapshotFileDiff[] = [];
    for (const item of meta.files) {
      if (!item || typeof item !== "object") continue;
      const rel = typeof item.relativePath === "string" ? item.relativePath : "";
      const abs = typeof item.filePath === "string" ? item.filePath : "";
      const rawPath = (rel && !rel.startsWith("..") ? rel : abs) || rel || item.file || item.path;
      const file = normalizeWorkspaceRelPath(String(rawPath || ""));
      if (!file) continue;
      const sessionDiff = matchSessionDiff(part.sessionID, file);
      if (sessionDiff && isRealFileDiff(sessionDiff)) {
        list.push(sessionDiff);
        continue;
      }
      const patch = typeof item.diff === "string" ? item.diff : undefined;
      const before = typeof item.before === "string" ? item.before : undefined;
      const after = typeof item.after === "string" ? item.after : undefined;
      const counts = countDiffLines(patch, before, after);
      const entry: SnapshotFileDiff = {
        file,
        patch,
        before,
        after,
        additions: typeof item.additions === "number" ? item.additions : counts.additions,
        deletions: typeof item.deletions === "number" ? item.deletions : counts.deletions,
      };
      if (isRealFileDiff(entry)) list.push(entry);
    }
    if (list.length > 0) return list;
  }

  // 2. `apply_patch` input.patchText
  if (typeof state.input?.patchText === "string") {
    const parsed = parseApplyPatchText(state.input.patchText, part.sessionID).filter(
      isRealFileDiff
    );
    if (parsed.length > 0) return parsed;
  }

  // 3. Single file `edit` / `write` / `patch`
  const file = normalizeWorkspaceRelPath(primaryFile);
  if (!file) return [];
  const sessionDiff = matchSessionDiff(part.sessionID, file);
  if (sessionDiff) {
    return isRealFileDiff(sessionDiff) ? [sessionDiff] : [];
  }

  const patch = meta?.diff || meta?.patch;
  const before = meta?.filediff?.before ?? state.input?.oldString;
  const after = meta?.filediff?.after ?? state.input?.newString ?? state.input?.content;
  if (patch || typeof after === "string") {
    const counts = countDiffLines(
      typeof patch === "string" ? patch : undefined,
      typeof before === "string" ? before : undefined,
      typeof after === "string" ? after : undefined
    );
    const entry: SnapshotFileDiff = {
      file,
      patch: typeof patch === "string" ? patch : undefined,
      before: typeof before === "string" ? before : undefined,
      after: typeof after === "string" ? after : undefined,
      additions: meta?.filediff?.additions ?? counts.additions,
      deletions: meta?.filediff?.deletions ?? counts.deletions,
    };
    return isRealFileDiff(entry) ? [entry] : [];
  }
  return [];
}

export function resolveToolFileDiff(
  part: ToolPart,
  isEdit: boolean,
  file: string
): SnapshotFileDiff | undefined {
  return resolveToolFileDiffs(part, isEdit, file)[0];
}

export function resolveToolFileDiagnostics(
  part: ToolPart,
  isEdit: boolean,
  file: string
): DiagnosticItem[] {
  if (!isEdit || !file) return [];
  const state = part.state;
  const metaDiag = state.status !== "pending" ? state.metadata?.diagnostics : undefined;
  if (metaDiag && typeof metaDiag === "object") {
    for (const [k, list] of Object.entries(metaDiag)) {
      if (
        (k === file || k.endsWith(`/${file}`) || file.endsWith(`/${k}`)) &&
        Array.isArray(list)
      ) {
        return list as DiagnosticItem[];
      }
    }
  }
  return [];
}

/**
 * Collects all unique modified files (with +/- counts and diff payloads) across an assistant turn cycle,
 * including completed edit/write/apply_patch calls, patch parts, and child subagent sessions.
 * Files without actual content modifications (`!isRealFileDiff`) or from failed/unfinished tool calls are excluded.
 */
export function collectCycleChangedFiles(
  cycleTurns: MessageTurn[],
  sessionID: string
): SnapshotFileDiff[] {
  const byFile = new Map<string, SnapshotFileDiff>();

  const recordDiff = (d: SnapshotFileDiff) => {
    const norm = normalizeWorkspaceRelPath(d.file);
    if (!norm) return;
    const fromSession = matchSessionDiff(sessionID, norm);
    if (fromSession) {
      if (isRealFileDiff(fromSession)) {
        byFile.set(norm, { ...fromSession, file: norm });
      } else {
        byFile.delete(norm);
      }
      return;
    }
    const candidate = { ...d, file: norm };
    if (isRealFileDiff(candidate)) {
      byFile.set(norm, candidate);
    }
  };

  const scanParts = (parts: MessagePart[]) => {
    for (const p of parts) {
      if (p.type === "tool") {
        if (p.state.status !== "completed") continue;
        if (isEditingTool(p.tool)) {
          const primary = extractTargetFile(p) || "";
          for (const d of resolveToolFileDiffs(p, true, primary)) {
            recordDiff(d);
          }
        } else if (isTaskTool(p.tool)) {
          const meta = p.state.metadata;
          const childID =
            meta?.sessionId || meta?.sessionID || p.state.input?.task_id;
          if (typeof childID === "string" && childID) {
            const childTurns = workspace.messagesBySession[childID] ?? [];
            for (const ct of childTurns) scanParts(ct.parts);
          }
        }
      } else if (p.type === "patch" && Array.isArray(p.files)) {
        for (const rawFile of p.files) {
          const norm = normalizeWorkspaceRelPath(rawFile);
          if (!norm || byFile.has(norm)) continue;
          const fromSession = matchSessionDiff(sessionID, norm);
          if (fromSession && isRealFileDiff(fromSession)) {
            byFile.set(norm, { ...fromSession, file: norm });
          }
        }
      }
    }
  };

  for (const step of cycleTurns) {
    scanParts(step.parts);
  }

  return Array.from(byFile.values()).filter(isRealFileDiff);
}

/**
 * Collects all unique modified files strictly from the latest user prompt cycle
 * of the active session (excluding unrelated workspace/historical git diffs).
 */
export function collectSessionChangedFiles(
  visibleTurns: MessageTurn[],
  sessionID: string
): SnapshotFileDiff[] {
  if (!sessionID || visibleTurns.length === 0) return [];
  let lastUserIdx = -1;
  for (let i = visibleTurns.length - 1; i >= 0; i--) {
    if (visibleTurns[i].info.role === "user") {
      lastUserIdx = i;
      break;
    }
  }
  const cycleTurns =
    lastUserIdx >= 0 ? visibleTurns.slice(lastUserIdx + 1) : visibleTurns;
  if (cycleTurns.length === 0) return [];
  return collectCycleChangedFiles(cycleTurns, sessionID);
}

export { computeChangedFileLines, resolveSessionDiffForFile } from "./file-diff-lines";


export interface RunnerSubagentItem {
  role: string;
  type: string;
  status: "running" | "reported";
  steps: number;
  currentTool: string;
  prompt: string;
  lastActivityAt: number;
}

export interface RunnerSubagentProgress {
  items: RunnerSubagentItem[];
  reported: number;
  total: number;
  steps: number;
  lastActivityAt: number;
}

/** Progress of Antigravity subagents, which never get child sessions of their own. */
export function resolveRunnerSubagentProgress(part: ToolPart): RunnerSubagentProgress | undefined {
  const meta = part.state.metadata as Record<string, any> | undefined;
  if (!meta || !Array.isArray(meta.subagents)) return undefined;
  const inputs = Array.isArray(part.state.input?.subagents) ? part.state.input.subagents : [];
  const items: RunnerSubagentItem[] = meta.subagents.map((raw: Record<string, any>, i: number) => ({
    role: String(raw?.role || raw?.type || ""),
    type: String(raw?.type || ""),
    status: raw?.status === "reported" ? "reported" : "running",
    steps: Number(raw?.steps) || 0,
    currentTool: String(raw?.currentTool || ""),
    prompt: String(inputs[i]?.prompt || ""),
    lastActivityAt: Number(raw?.lastActivityAt) || 0,
  }));
  return {
    items,
    reported: Number(meta.reported) || 0,
    total: Number(meta.total) || items.length,
    steps: items.reduce((sum, item) => sum + item.steps, 0),
    lastActivityAt: Number(meta.lastActivityAt) || 0,
  };
}

/** "Role · tool" of the subagent that moved most recently and is still working. */
export function resolveRunnerSubagentStepLabel(part: ToolPart): string {
  const progress = resolveRunnerSubagentProgress(part);
  let latest: RunnerSubagentItem | undefined;
  for (const item of progress?.items ?? []) {
    if (item.status !== "running" || !item.currentTool) continue;
    if (!latest || item.lastActivityAt > latest.lastActivityAt) latest = item;
  }
  return latest ? `${latest.role} · ${latest.currentTool}` : "";
}
