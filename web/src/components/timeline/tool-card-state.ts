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
  const trimmed = (raw || "").trim();
  if (!trimmed) return "";
  const root = workspace.rootDir;
  if (root && trimmed.startsWith(root)) {
    return trimmed.slice(root.length).replace(/^\/+/, "");
  }
  return trimmed.replace(/^\.\/+/, "");
}

function matchSessionDiff(
  sessionID: string,
  file: string
): SnapshotFileDiff | undefined {
  const norm = normalizeWorkspaceRelPath(file);
  if (!norm) return undefined;
  const diffs = workspace.diffsBySession[sessionID] ?? [];
  return diffs.find(
    (d) =>
      d.file === norm ||
      d.file.endsWith(`/${norm}`) ||
      norm.endsWith(`/${d.file}`)
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
        add(f.relativePath || f.filePath || f.file || f.path);
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

export function resolveToolFileDiffs(
  part: ToolPart,
  isEdit: boolean,
  primaryFile: string
): SnapshotFileDiff[] {
  if (!isEdit) return [];
  const state = part.state;
  const meta = state.status !== "pending" ? state.metadata : undefined;

  // 1. Multi-file metadata from OpenCode `apply_patch` (`metadata.files`)
  if (Array.isArray(meta?.files) && meta.files.length > 0) {
    const list: SnapshotFileDiff[] = [];
    for (const item of meta.files) {
      if (!item || typeof item !== "object") continue;
      const rawPath = item.relativePath || item.filePath || item.file || item.path;
      const file = normalizeWorkspaceRelPath(String(rawPath || ""));
      if (!file) continue;
      const sessionDiff = matchSessionDiff(part.sessionID, file);
      if (sessionDiff) {
        list.push(sessionDiff);
        continue;
      }
      const patch = typeof item.diff === "string" ? item.diff : undefined;
      const before = typeof item.before === "string" ? item.before : undefined;
      const after = typeof item.after === "string" ? item.after : undefined;
      const counts = countDiffLines(patch, before, after);
      list.push({
        file,
        patch,
        before,
        after,
        additions: typeof item.additions === "number" ? item.additions : counts.additions,
        deletions: typeof item.deletions === "number" ? item.deletions : counts.deletions,
      });
    }
    if (list.length > 0) return list;
  }

  // 2. `apply_patch` input.patchText
  if (typeof state.input?.patchText === "string") {
    const parsed = parseApplyPatchText(state.input.patchText, part.sessionID);
    if (parsed.length > 0) return parsed;
  }

  // 3. Single file `edit` / `write` / `patch`
  const file = normalizeWorkspaceRelPath(primaryFile);
  if (!file) return [];
  const sessionDiff = matchSessionDiff(part.sessionID, file);
  if (sessionDiff) return [sessionDiff];

  const patch = meta?.diff || meta?.patch;
  const before = meta?.filediff?.before ?? state.input?.oldString;
  const after = meta?.filediff?.after ?? state.input?.newString ?? state.input?.content;
  if (patch || typeof after === "string") {
    const counts = countDiffLines(
      typeof patch === "string" ? patch : undefined,
      typeof before === "string" ? before : undefined,
      typeof after === "string" ? after : undefined
    );
    return [
      {
        file,
        patch: typeof patch === "string" ? patch : undefined,
        before: typeof before === "string" ? before : undefined,
        after: typeof after === "string" ? after : undefined,
        additions: meta?.filediff?.additions ?? counts.additions,
        deletions: meta?.filediff?.deletions ?? counts.deletions,
      },
    ];
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
 * including direct edit/write/apply_patch calls, patch parts, and child subagent sessions.
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
    byFile.set(norm, fromSession || { ...d, file: norm });
  };

  const scanParts = (parts: MessagePart[]) => {
    for (const p of parts) {
      if (p.type === "tool") {
        if (isEditingTool(p.tool)) {
          for (const file of extractTargetFiles(p)) {
            if (!byFile.has(file)) {
              recordDiff({ file, additions: 0, deletions: 0 });
            }
          }
          const primary = extractTargetFile(p) || "";
          for (const d of resolveToolFileDiffs(p, true, primary)) {
            recordDiff(d);
          }
        } else if (isTaskTool(p.tool)) {
          const meta = p.state.status !== "pending" ? p.state.metadata : undefined;
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
          if (fromSession) {
            byFile.set(norm, fromSession);
          } else {
            byFile.set(norm, { file: norm, additions: 0, deletions: 0 });
          }
        }
      }
    }
  };

  for (const step of cycleTurns) {
    scanParts(step.parts);
  }

  return Array.from(byFile.values());
}

export { computeChangedFileLines, resolveSessionDiffForFile } from "./file-diff-lines";
