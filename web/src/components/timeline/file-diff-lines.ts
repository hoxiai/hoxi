// web/src/components/timeline/file-diff-lines.ts
import type { SnapshotFileDiff } from "../../services/sse-types";
import { workspace } from "../../store/workspace";
import { collectCycleChangedFiles, normalizeWorkspaceRelPath } from "./tool-card-state";

/**
 * Resolves the best available SnapshotFileDiff for `filePath` in `sessionID`
 * by checking the explicit fallback diff, session-scoped diffs, and tool
 * edit/write/patch parts across the session's message history.
 */
export function resolveSessionDiffForFile(
  filePath: string,
  sessionID?: string | null,
  fallbackDiff?: SnapshotFileDiff | null
): SnapshotFileDiff | undefined {
  const norm = normalizeWorkspaceRelPath(filePath.replace(/:\d+(?::\d+)?$/, ""));
  if (!norm) return undefined;

  const matchesPath = (candidate: string) =>
    candidate === norm ||
    candidate.endsWith(`/${norm}`) ||
    norm.endsWith(`/${candidate}`);

  if (
    fallbackDiff &&
    matchesPath(normalizeWorkspaceRelPath(fallbackDiff.file)) &&
    (fallbackDiff.patch || fallbackDiff.before !== undefined || fallbackDiff.after !== undefined)
  ) {
    return fallbackDiff;
  }

  const sid = sessionID || workspace.activeSessionID;
  if (sid) {
    const direct = (workspace.diffsBySession[sid] ?? []).find((d) => matchesPath(d.file));
    if (direct && (direct.patch || direct.before !== undefined || direct.after !== undefined)) {
      return direct;
    }
    const turns = workspace.messagesBySession[sid] ?? [];
    if (turns.length > 0) {
      const fromTurns = collectCycleChangedFiles(turns, sid).find((d) => matchesPath(d.file));
      if (fromTurns) return fromTurns;
    }
    if (direct) return direct;
  }

  return undefined;
}

/**
 * Computes the 1-indexed line numbers in `sourceLines` that were added or modified
 * by `diff` (supporting unified patches, V4A apply_patch, full-file before/after, and snippet edits).
 */
export function computeChangedFileLines(
  diff: SnapshotFileDiff | undefined,
  sourceLines: string[]
): Set<number> {
  const changed = new Set<number>();
  if (!diff || sourceLines.length === 0) return changed;

  const findBlockOffset = (blockLines: string[]) => {
    if (blockLines.length === 0 || blockLines.length > sourceLines.length) return -1;
    for (let i = 0; i <= sourceLines.length - blockLines.length; i++) {
      let ok = true;
      for (let j = 0; j < blockLines.length; j++) {
        if (sourceLines[i + j].trimEnd() !== blockLines[j].trimEnd()) {
          ok = false;
          break;
        }
      }
      if (ok) return i;
    }
    return -1;
  };

  if (typeof diff.patch === "string" && diff.patch.trim()) {
    const rawLines = diff.patch.replace(/\r\n/g, "\n").split("\n");
    const hunks: Array<{ startLine: number | null; lines: string[] }> = [];
    let curr: { startLine: number | null; lines: string[] } | null = null;
    for (const line of rawLines) {
      if (line.startsWith("*** ") || line.startsWith("--- ") || line.startsWith("+++ ")) continue;
      const m = line.match(/^@@(?:\s+-\d+(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@)?/);
      if (m) {
        if (curr && curr.lines.length > 0) hunks.push(curr);
        curr = { startLine: m[1] ? Number(m[1]) : null, lines: [] };
        continue;
      }
      if (!curr) curr = { startLine: null, lines: [] };
      curr.lines.push(line);
    }
    if (curr && curr.lines.length > 0) hunks.push(curr);

    for (const h of hunks) {
      const newSide: Array<{ text: string; added: boolean }> = [];
      for (const hl of h.lines) {
        if (hl.startsWith("-")) continue;
        if (hl.startsWith("+")) newSide.push({ text: hl.slice(1), added: true });
        else newSide.push({ text: hl.startsWith(" ") ? hl.slice(1) : hl, added: false });
      }
      const offset = findBlockOffset(newSide.map((x) => x.text));
      if (offset >= 0) {
        for (let j = 0; j < newSide.length; j++) {
          if (newSide[j].added) changed.add(offset + j + 1);
        }
      } else if (h.startLine && h.startLine > 0) {
        let ln = h.startLine;
        for (const ns of newSide) {
          if (ns.added) changed.add(ln);
          ln++;
        }
      }
    }
    if (changed.size > 0) return changed;
  }

  if (typeof diff.after === "string" && diff.after.length > 0) {
    const afterLines = diff.after.replace(/\r\n/g, "\n").split("\n");
    const offset = Math.max(0, findBlockOffset(afterLines));
    if (typeof diff.before !== "string" || diff.before.length === 0) {
      for (let i = 0; i < afterLines.length; i++) changed.add(offset + i + 1);
      return changed;
    }
    const beforeLines = diff.before.replace(/\r\n/g, "\n").split("\n");
    let bi = 0;
    let ai = 0;
    while (bi < beforeLines.length && ai < afterLines.length) {
      if (beforeLines[bi] === afterLines[ai]) {
        bi++;
        ai++;
        continue;
      }
      let syncB = -1;
      let syncA = -1;
      outer: for (let d = 1; d <= 60; d++) {
        for (let da = 0; da <= d; da++) {
          const db = d - da;
          if (
            bi + db < beforeLines.length &&
            ai + da < afterLines.length &&
            beforeLines[bi + db] === afterLines[ai + da]
          ) {
            syncB = bi + db;
            syncA = ai + da;
            break outer;
          }
        }
      }
      if (syncA >= 0) {
        for (let k = ai; k < syncA; k++) changed.add(offset + k + 1);
        bi = syncB;
        ai = syncA;
      } else {
        for (let k = ai; k < afterLines.length; k++) changed.add(offset + k + 1);
        break;
      }
    }
    while (ai < afterLines.length) {
      changed.add(offset + ai + 1);
      ai++;
    }
  }
  return changed;
}
