// web/src/store/inspector.ts
// Inspector panel navigation (files / diffs / browser) and per-session review comments.
import { hoxiAPI } from "../services/api";
import type { SnapshotFileDiff } from "../services/sse-types";
import { extractFileSuffix, getResolvedFilePath, normalizeCandidatePath } from "./file-links";
import { setWorkspace, workspace, type ReviewCommentItem } from "./workspace-state";

function firstChangedLine(patch?: string): number | null {
  if (!patch) return null;
  const hunk = patch.match(/^@@\s+-\d+(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@/m);
  if (!hunk) return null;
  let line = Number(hunk[1]);
  for (const entry of patch.slice(patch.indexOf(hunk[0]) + hunk[0].length).split("\n")) {
    if (entry.startsWith("@@")) break;
    if (entry.startsWith("+")) return line;
    if (!entry.startsWith("-")) line++;
  }
  return line;
}

function parseFileReference(raw: string): { path: string; line: number | null } {
  const { line: suffixLine } = extractFileSuffix(raw);
  let str = normalizeCandidatePath(raw);
  const parsedLine = suffixLine && suffixLine > 0 ? suffixLine : null;

  const activeSession = workspace.activeSessionID
    ? workspace.sessions.find((s) => s.id === workspace.activeSessionID)
    : undefined;
  const activeWorktreeDir = workspace.activeSessionID
    ? workspace.worktreeBySession[workspace.activeSessionID]?.directory
    : undefined;
  const primaryDir =
    workspace.activeInspectorFolder ||
    activeWorktreeDir ||
    activeSession?.directory ||
    workspace.rootDir ||
    "";

  // If checkFile resolved a bare filename or suffix to an exact path, prefer it
  const cachedResolved = getResolvedFilePath(str, primaryDir) || getResolvedFilePath(str, workspace.rootDir || "");
  if (cachedResolved) {
    str = cachedResolved;
  }

  const candidateRoots = (
    workspace.activeInspectorFolder
      ? [workspace.activeInspectorFolder]
      : [activeWorktreeDir, activeSession?.directory, workspace.rootDir]
  ).filter((d): d is string => Boolean(d));
  for (const root of candidateRoots) {
    const normRoot = root.replace(/\/+$/, "");
    if (normRoot && str.startsWith(`${normRoot}/`)) {
      str = str.slice(normRoot.length + 1);
      break;
    }
  }
  return { path: str, line: parsedLine };
}

function resolveKnownPoolDiff(refPath: string): SnapshotFileDiff | null {
  if (!refPath) return null;
  const key = workspace.activeSessionID || "__root__";
  const pool = [
    ...(workspace.activeSessionID ? workspace.diffsBySession[workspace.activeSessionID] ?? [] : []),
    ...(workspace.worktreeDiffsBySession[key] ?? []),
    ...(workspace.worktreeDiffsBySession["__root__"] ?? []),
  ];
  return (
    pool.find((item) => item.file === refPath) ??
    pool.find((item) => item.file.endsWith(`/${refPath}`) || refPath.endsWith(`/${item.file}`)) ??
    null
  );
}

export function openFileInInspector(filePath: string, line?: number | null, diff?: SnapshotFileDiff | null) {
  const ref = parseFileReference(filePath);
  if (!ref.path) return;
  const targetLine = line && line > 0 ? line : ref.line;
  const resolvedDiff = diff !== undefined ? diff : resolveKnownPoolDiff(ref.path);
  setWorkspace({
    inspectedFileDiff: resolvedDiff ?? null,
    inspectedFilePath: ref.path,
    inspectedFileLine: targetLine && targetLine > 0 ? targetLine : null,
    inspectorTab: "files",
    inspectorOpen: true,
  });
}

export function openChangedFileInInspector(diff: SnapshotFileDiff | string) {
  const file = typeof diff === "string" ? diff : diff.file;
  const ref = parseFileReference(file);
  const resolvedDiff = typeof diff === "string" ? resolveKnownPoolDiff(ref.path) : diff;
  openFileInInspector(
    file,
    ref.line ?? firstChangedLine(resolvedDiff?.patch),
    resolvedDiff
  );
}

export function openDiffInInspector(
  file?: string | SnapshotFileDiff,
  scope?: "worktree" | "session"
) {
  const filePath = typeof file === "string" ? file : file?.file;
  const cleaned = filePath ? parseFileReference(filePath).path : "";
  setWorkspace({
    ...(scope ? { diffScope: scope } : {}),
    inspectedFileDiff: typeof file === "object" ? file : null,
    inspectedDiffFile: cleaned || null,
    inspectorTab: "diffs",
    inspectorOpen: true,
  });
}

export function openURLInInspector(url: string) {
  const trimmed = url.trim();
  if (!trimmed || !/^https?:\/\//i.test(trimmed)) return;
  setWorkspace({
    inspectedBrowserURL: trimmed,
    inspectorTab: "browser",
    inspectorOpen: true,
  });
}

export function openExternalURL(url: string) {
  const trimmed = url.trim();
  if (!trimmed || !/^https?:\/\//i.test(trimmed)) return;
  const wailsRuntime = (window as any).runtime;
  if (typeof wailsRuntime?.BrowserOpenURL === "function") {
    wailsRuntime.BrowserOpenURL(trimmed);
    return;
  }
  window.open(trimmed, "_blank", "noopener,noreferrer");
}

export async function openExternalFile(
  filePath: string,
  options?: {
    directory?: string;
    sessionID?: string;
    action?: "open" | "reveal";
  }
): Promise<void> {
  const target = filePath.trim();
  if (!target) return;
  await hoxiAPI.openExternalFile(target, {
    directory: options?.directory,
    sessionID: options?.sessionID ?? (workspace.activeSessionID || undefined),
    action: options?.action,
  });
}

export function addReviewComment(sessionID: string, comment: Omit<ReviewCommentItem, "id">) {
  const item: ReviewCommentItem = {
    ...comment,
    id: `rc_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`,
  };
  setWorkspace("reviewCommentsBySession", sessionID, [
    ...(workspace.reviewCommentsBySession[sessionID] ?? []),
    item,
  ]);
}

export function removeReviewComment(sessionID: string, commentID: string) {
  setWorkspace(
    "reviewCommentsBySession",
    sessionID,
    (workspace.reviewCommentsBySession[sessionID] ?? []).filter((c) => c.id !== commentID)
  );
}

export function clearReviewComments(sessionID: string) {
  setWorkspace("reviewCommentsBySession", sessionID, []);
}
