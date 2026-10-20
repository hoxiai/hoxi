// web/src/components/timeline/tool-card-state.ts
import type { MessagePart, SnapshotFileDiff } from "../../services/sse-types";
import {
  ensureChildSubagentSessionLoaded,
  extractChildSubagentSessionId,
  type MessageTurn,
  workspace,
} from "../../store/workspace";
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


export function enrichDiffWithWorktree(
  d: SnapshotFileDiff,
  wtDiffs: SnapshotFileDiff[] | undefined,
  isolated?: boolean
): SnapshotFileDiff {
  if (!wtDiffs || wtDiffs.length === 0) return d;
  const norm = normalizeWorkspaceRelPath(d.file);
  if (!norm) return d;
  const wt =
    wtDiffs.find((w) => w.file === norm) ??
    wtDiffs.find((w) => {
      const wn = normalizeWorkspaceRelPath(w.file);
      return wn === norm || wn.endsWith(`/${norm}`) || norm.endsWith(`/${wn}`);
    });
  if (!wt) return d;
  const sameCounts =
    (d.additions ?? 0) === (wt.additions ?? 0) &&
    (d.deletions ?? 0) === (wt.deletions ?? 0);
  const canUseWtPatch =
    Boolean(wt.patch) &&
    !d.patch &&
    (Boolean(isolated) || sameCounts || (d.before === undefined && d.after === undefined));
  const nextPatch = d.patch || (canUseWtPatch ? wt.patch : undefined);
  const nextStamp = d.stamp || (canUseWtPatch || sameCounts ? wt.stamp : undefined);
  const nextStatus = d.status || wt.status;
  const nextRepo = d.repo || wt.repo;
  const nextRepoDir = d.repoDir || wt.repoDir;
  const nextBranch = d.branch || wt.branch;
  const nextGenerated = d.generatedLike ?? wt.generatedLike;
  const nextLarge = d.large ?? (sameCounts ? wt.large : undefined);
  if (
    nextPatch === d.patch &&
    nextStamp === d.stamp &&
    nextStatus === d.status &&
    nextRepo === d.repo &&
    nextRepoDir === d.repoDir &&
    nextBranch === d.branch &&
    nextGenerated === d.generatedLike &&
    nextLarge === d.large
  ) {
    return d;
  }
  return {
    ...d,
    patch: nextPatch,
    stamp: nextStamp,
    status: nextStatus,
    repo: nextRepo,
    repoDir: nextRepoDir,
    branch: nextBranch,
    generatedLike: nextGenerated,
    large: nextLarge,
  };
}

function resolveDiffOwnerSessionID(sessionID: string): string {
  if (!sessionID) return sessionID;
  const seen = new Set<string>();
  let cur = sessionID;
  while (cur && !seen.has(cur)) {
    seen.add(cur);
    const s = workspace.sessions.find((item) => item.id === cur);
    if (s?.parentID) {
      cur = s.parentID;
      continue;
    }
    break;
  }
  if (
    cur === sessionID &&
    !workspace.diffsBySession[cur] &&
    !workspace.worktreeDiffsBySession[cur] &&
    workspace.activeSessionID &&
    workspace.activeSessionID !== cur
  ) {
    return workspace.activeSessionID;
  }
  return cur;
}

function matchSessionDiff(
  sessionID: string,
  file: string
): SnapshotFileDiff | undefined {
  const norm = normalizeWorkspaceRelPath(file);
  if (!norm) return undefined;
  const ownerID = resolveDiffOwnerSessionID(sessionID);
  const wtDiffs = workspace.worktreeDiffsBySession[ownerID] ?? [];
  const isolated = workspace.worktreeBySession[ownerID]?.isolated;
  const diffs = workspace.diffsBySession[ownerID] ?? [];
  const exact = diffs.find((d) => d.file === norm);
  if (exact) return enrichDiffWithWorktree(exact, wtDiffs, isolated);
  const suffix = diffs.find(
    (d) => d.file.endsWith(`/${norm}`) || norm.endsWith(`/${d.file}`)
  );
  if (suffix) return enrichDiffWithWorktree(suffix, wtDiffs, isolated);
  return (
    wtDiffs.find((d) => d.file === norm) ??
    wtDiffs.find((d) => d.file.endsWith(`/${norm}`) || norm.endsWith(`/${d.file}`))
  );
}

function enrichToolDiffMeta(
  entry: SnapshotFileDiff,
  sessionID?: string
): SnapshotFileDiff {
  if (!sessionID) return entry;
  const existing = matchSessionDiff(sessionID, entry.file);
  if (!existing) return entry;
  const sameCounts =
    (entry.additions ?? 0) === (existing.additions ?? 0) &&
    (entry.deletions ?? 0) === (existing.deletions ?? 0);
  const canUseExistingPatch =
    Boolean(existing.patch) &&
    !entry.patch &&
    sameCounts &&
    entry.before === undefined &&
    entry.after === undefined;
  return {
    ...entry,
    patch: entry.patch || (canUseExistingPatch ? existing.patch : undefined),
    stamp: entry.stamp || (sameCounts ? existing.stamp : undefined),
    status: entry.status || existing.status,
    repo: entry.repo || existing.repo,
    branch: entry.branch || existing.branch,
    generatedLike: entry.generatedLike ?? existing.generatedLike,
  };
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
    const entry: SnapshotFileDiff = {
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
    };
    if (formattedHunks.length === 0 && additions === 0 && deletions === 0 && sessionID) {
      const existing = matchSessionDiff(sessionID, normFile);
      if (existing && (existing.patch || existing.before !== undefined || existing.after !== undefined)) {
        results.push(existing);
        return;
      }
    }
    results.push(enrichToolDiffMeta(entry, sessionID));
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
  primaryFile: string,
  fallbackSessionID?: string
): SnapshotFileDiff[] {
  if (!isEdit || part.state.status === "error") return [];
  const state = part.state;
  const meta = state.status !== "pending" ? state.metadata : undefined;
  const diffSessionID = fallbackSessionID || part.sessionID;

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
      const patch = typeof item.diff === "string" ? item.diff : undefined;
      const before = typeof item.before === "string" ? item.before : undefined;
      const after = typeof item.after === "string" ? item.after : undefined;
      const hasItemPayload =
        patch !== undefined ||
        before !== undefined ||
        after !== undefined ||
        typeof item.additions === "number" ||
        typeof item.deletions === "number";
      if (hasItemPayload) {
        const counts = countDiffLines(patch, before, after);
        const entry: SnapshotFileDiff = {
          file,
          patch,
          before,
          after,
          additions: typeof item.additions === "number" ? item.additions : counts.additions,
          deletions: typeof item.deletions === "number" ? item.deletions : counts.deletions,
        };
        if (isRealFileDiff(entry)) {
          list.push(enrichToolDiffMeta(entry, diffSessionID));
        }
        continue;
      }
      const sessionDiff = matchSessionDiff(diffSessionID, file);
      if (sessionDiff && isRealFileDiff(sessionDiff)) {
        list.push(sessionDiff);
      }
    }
    if (list.length > 0) return list;
  }

  // 2. `apply_patch` input.patchText
  if (typeof state.input?.patchText === "string") {
    const parsed = parseApplyPatchText(state.input.patchText, diffSessionID).filter(
      isRealFileDiff
    );
    if (parsed.length > 0) return parsed;
  }

  // 3. Single file `edit` / `write` / `patch` / `replace_file_content` / `write_to_file`
  const file = normalizeWorkspaceRelPath(primaryFile);
  if (!file) return [];
  const input = resolveToolInput(part);
  const patch = meta?.diff || meta?.patch || input.patch || input.diff;
  let chunksBefore: string | undefined;
  let chunksAfter: string | undefined;
  if (Array.isArray(input.ReplacementChunks) && input.ReplacementChunks.length > 0) {
    const bParts: string[] = [];
    const aParts: string[] = [];
    for (const ch of input.ReplacementChunks) {
      if (ch && typeof ch === "object") {
        const tb = ch.TargetContent ?? ch.targetContent;
        const ta = ch.ReplacementContent ?? ch.replacementContent;
        if (typeof tb === "string") bParts.push(tb);
        if (typeof ta === "string") aParts.push(ta);
      }
    }
    if (bParts.length > 0 || aParts.length > 0) {
      chunksBefore = bParts.join("\n");
      chunksAfter = aParts.join("\n");
    }
  }
  const before =
    meta?.filediff?.before ??
    input.oldString ??
    input.old_string ??
    input.TargetContent ??
    input.targetContent ??
    chunksBefore;
  const after =
    meta?.filediff?.after ??
    input.newString ??
    input.new_string ??
    input.ReplacementContent ??
    input.replacementContent ??
    input.content ??
    input.file_text ??
    input.CodeContent ??
    input.codeContent ??
    chunksAfter;
  const hasOwnPayload =
    typeof patch === "string" ||
    typeof before === "string" ||
    typeof after === "string" ||
    typeof meta?.filediff?.additions === "number" ||
    typeof meta?.filediff?.deletions === "number";
  if (hasOwnPayload) {
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
    return isRealFileDiff(entry) ? [enrichToolDiffMeta(entry, diffSessionID)] : [];
  }

  const sessionDiff = matchSessionDiff(diffSessionID, file);
  if (sessionDiff) {
    return isRealFileDiff(sessionDiff) ? [sessionDiff] : [];
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
  const patchFiles: string[] = [];

  const recordDiff = (d: SnapshotFileDiff) => {
    const norm = normalizeWorkspaceRelPath(d.file);
    if (!norm) return;
    const candidate = enrichToolDiffMeta({ ...d, file: norm }, sessionID);
    if (!isRealFileDiff(candidate)) return;
    const prev = byFile.get(norm);
    if (!prev) {
      byFile.set(norm, candidate);
      return;
    }
    byFile.set(norm, {
      ...prev,
      ...candidate,
      file: norm,
      additions: (prev.additions ?? 0) + (candidate.additions ?? 0),
      deletions: (prev.deletions ?? 0) + (candidate.deletions ?? 0),
      patch:
        prev.patch && candidate.patch && prev.patch !== candidate.patch
          ? `${prev.patch.replace(/\n*$/, "\n")}${candidate.patch.replace(/^---\s+a\/.*\n\+\+\+\s+b\/.*\n/, "")}`
          : candidate.patch || prev.patch,
      before: prev.before ?? candidate.before,
      after: candidate.after ?? prev.after,
    });
  };

  const visitedSessions = new Set<string>([sessionID]);
  const scanParts = (parts: MessagePart[], ownerSessionID: string) => {
    for (const p of parts) {
      if (p.type === "tool") {
        if (isTaskTool(p.tool)) {
          const childID = extractChildSubagentSessionId(p);
          if (childID && !visitedSessions.has(childID)) {
            visitedSessions.add(childID);
            ensureChildSubagentSessionLoaded(
              childID,
              ownerSessionID,
              p.state.status === "completed" || p.state.status === "error"
            );
            const childTurns = workspace.messagesBySession[childID] ?? [];
            for (const ct of childTurns) scanParts(ct.parts, childID);
          }
          continue;
        }
        if (p.state.status !== "completed") continue;
        if (isEditingTool(p.tool)) {
          const primary = extractTargetFile(p) || "";
          for (const d of resolveToolFileDiffs(p, true, primary, sessionID)) {
            recordDiff(d);
          }
        }
      } else if (p.type === "patch" && Array.isArray(p.files)) {
        for (const rawFile of p.files) {
          const norm = normalizeWorkspaceRelPath(rawFile);
          if (norm && !patchFiles.includes(norm)) patchFiles.push(norm);
        }
      }
    }
  };

  for (const step of cycleTurns) {
    scanParts(step.parts, sessionID);
  }

  // OpenCode `patch` parts snapshot the entire working tree, which in shared `local`
  // directories includes unrelated workspace changes. Only fall back to `patch` parts
  // when running in an isolated worktree and no editing tool parts were recorded.
  if (byFile.size === 0 && patchFiles.length > 0 && workspace.worktreeBySession[sessionID]?.isolated) {
    for (const norm of patchFiles) {
      const fromSession = matchSessionDiff(sessionID, norm);
      if (fromSession && isRealFileDiff(fromSession)) {
        byFile.set(norm, { ...fromSession, file: norm });
      }
    }
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

export function deriveMarkdownDownloadFilename(
  text: string,
  sessionID?: string,
  dateStr = new Date().toISOString().slice(0, 10)
): string {
  const headingMatch = text.match(/^#{1,3}\s+(.+)$/m);
  const sessionTitle = sessionID
    ? workspace.sessions.find((s) => s.id === sessionID)?.title
    : "";
  const rawStem = (headingMatch?.[1] || sessionTitle || "")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/[*_`~#]/g, "")
    .replace(/[/\\?%*:|"<>]/g, "-")
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^[-.]+|[-.]+$/g, "")
    .slice(0, 48);
  const isGeneric =
    !rawStem ||
    /^(new[- ]session|untitled|\u65b0\u4f1a\u8bdd|\u672a\u547d\u540d\u4f1a\u8bdd)$/i.test(rawStem);
  const stem = isGeneric ? "hoxi-note" : rawStem;
  return `${stem}-${dateStr}.md`;
}

