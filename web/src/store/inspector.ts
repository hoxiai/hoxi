// web/src/store/inspector.ts
// Inspector panel navigation (files / diffs / browser) and per-session review comments.
import { hoxiAPI } from "../services/api";
import { openExternalBrowserURL } from "../services/desktop-events";
import { locale } from "../i18n";
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

function isRenderPreviewDefaultPath(cleanPath: string): boolean {
  return /\.(?:md|mdx|markdown|svg)$/i.test(cleanPath.trim());
}

export function openChangedFileInInspector(diff: SnapshotFileDiff | string) {
  const file = typeof diff === "string" ? diff : diff.file;
  const ref = parseFileReference(file);
  const resolvedDiff = typeof diff === "string" ? resolveKnownPoolDiff(ref.path) : diff;
  const targetLine =
    ref.line ?? (isRenderPreviewDefaultPath(ref.path) ? null : firstChangedLine(resolvedDiff?.patch));
  openFileInInspector(file, targetLine, resolvedDiff);
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

function cleanInspectorHttpURL(raw: string): string {
  const trimmed = raw.trim();
  const match = trimmed.match(
    /^https?:\/\/[^\s<>[\]"`\u3000-\u303f\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff\uff00-\uffef\u2014\u2018\u2019\u201c\u201d\u2026\u00ab\u00bb]+/i
  );
  if (!match) return "";
  let candidate = match[0].replace(/[.,;:!?'"]+$/, "");
  while (candidate.endsWith(")")) {
    const opens = (candidate.match(/\(/g) || []).length;
    const closes = (candidate.match(/\)/g) || []).length;
    if (closes <= opens) break;
    candidate = candidate.slice(0, -1).replace(/[.,;:!?'"]+$/, "");
  }
  return candidate;
}

export function openURLInInspector(url: string) {
  const cleaned = cleanInspectorHttpURL(url);
  if (!cleaned) return;
  setWorkspace({
    inspectedBrowserURL: cleaned,
    inspectorTab: "browser",
    inspectorOpen: true,
  });
}

export function openExternalURL(url: string) {
  const cleaned = cleanInspectorHttpURL(url);
  if (!cleaned) return;
  openExternalBrowserURL(cleaned);
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
    locale: locale(),
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

const PROJECT_MEMORY_FILE = ".hoxi/memory/project.md";
const LEGACY_PROJECT_MEMORY_FILE = ".opencode/memory/project.md";
const MAX_MEMORY_DRAFTS = 32;
const memoryDraftByTarget = new Map<string, string>();

export interface MemoryBankTarget {
  scope: "session" | "project";
  directory: string;
  sessionID: string;
  key: string;
}

/** Memory moved from .opencode/memory to .hoxi/memory; keep reading both. */
export function sessionMemoryFiles(id: string): string[] {
  return [`.hoxi/memory/sessions/${id}.md`, `.opencode/memory/sessions/${id}.md`];
}

/**
 * Resolves the project root that owns `.hoxi/memory` even when `dir` is an
 * isolated session worktree (`<root>/.hoxi/worktrees/<sessionID>`).
 */
export function resolveProjectMemoryDirectory(dir?: string | null): string | undefined {
  const raw = (dir || workspace.rootDir || "").trim();
  if (!raw) return workspace.rootDir || undefined;
  const normalized = raw.replace(/\\/g, "/").replace(/\/+$/, "");
  const marker = "/.hoxi/worktrees/";
  const idx = normalized.lastIndexOf(marker);
  if (idx > 0) {
    const rest = normalized.slice(idx + marker.length);
    if (rest.split("/")[0]) {
      return normalized.slice(0, idx);
    }
  }
  return raw;
}

export function resolveMemoryBankTarget(
  scope: "session" | "project",
  rawDirectory?: string | null,
  sessionID?: string | null
): MemoryBankTarget {
  const directory = (resolveProjectMemoryDirectory(rawDirectory) || "").trim();
  const sid = scope === "session" ? (sessionID || "").trim() : "";
  return {
    scope,
    directory,
    sessionID: sid,
    key: `${scope}:${directory}:${sid}`,
  };
}

export function getMemoryBankDraft(targetKey: string): string | undefined {
  if (!targetKey) return undefined;
  return memoryDraftByTarget.get(targetKey);
}

export function setMemoryBankDraft(targetKey: string, content: string): void {
  if (!targetKey) return;
  if (memoryDraftByTarget.has(targetKey)) {
    memoryDraftByTarget.delete(targetKey);
  } else if (memoryDraftByTarget.size >= MAX_MEMORY_DRAFTS) {
    const oldest = memoryDraftByTarget.keys().next().value;
    if (oldest !== undefined) {
      memoryDraftByTarget.delete(oldest);
    }
  }
  memoryDraftByTarget.set(targetKey, content);
}

export function clearMemoryBankDraft(targetKey: string): void {
  if (!targetKey) return;
  memoryDraftByTarget.delete(targetKey);
}

export function resetMemoryBankDrafts(): void {
  memoryDraftByTarget.clear();
}

export async function loadMemoryBankContent(input: {
  scope: "session" | "project";
  rawDirectory?: string | null;
  sessionID?: string | null;
}): Promise<{ target: MemoryBankTarget; content: string; found: boolean }> {
  const rawDir = (input.rawDirectory || workspace.rootDir || "").trim();
  const target = resolveMemoryBankTarget(input.scope, rawDir, input.sessionID);
  const dirCandidates =
    target.directory && target.directory !== rawDir
      ? [target.directory, rawDir]
      : [target.directory || rawDir];
  const candidates: string[] =
    target.scope === "session"
      ? target.sessionID
        ? sessionMemoryFiles(target.sessionID)
        : []
      : [PROJECT_MEMORY_FILE, LEGACY_PROJECT_MEMORY_FILE];

  for (const dir of dirCandidates) {
    for (const file of candidates) {
      try {
        const res = await hoxiAPI.readFileContent(file, dir, undefined, locale());
        if (res?.content?.trim()) {
          return { target, content: res.content, found: true };
        }
      } catch (err) {
        if (
          dir === dirCandidates[dirCandidates.length - 1] &&
          file === candidates[candidates.length - 1]
        ) {
          console.error("read memory failed:", err);
        }
      }
    }
  }
  return { target, content: "", found: false };
}

export function saveMemoryBankContent(input: {
  scope: "project" | "session";
  sessionID?: string | null;
  directory?: string;
  content: string;
}): Promise<{ ok: boolean; scope: string; path: string; content: string }> {
  const target = resolveMemoryBankTarget(input.scope, input.directory, input.sessionID);
  if (target.scope === "session" && !target.sessionID) {
    return Promise.reject(new Error("valid sessionID is required for session memory"));
  }
  return hoxiAPI.saveMemory({
    scope: target.scope,
    ...(target.sessionID ? { sessionID: target.sessionID } : {}),
    ...(target.directory ? { directory: target.directory } : {}),
    content: input.content,
  });
}

export function promoteSessionMemoryToProject(input: {
  sessionID?: string | null;
  directory?: string;
  content?: string;
}): Promise<{ ok: boolean; path: string; content: string; addedCount: number }> {
  const dir = resolveProjectMemoryDirectory(input.directory);
  return hoxiAPI.promoteSessionMemory({
    ...(input.sessionID ? { sessionID: input.sessionID } : {}),
    ...(dir ? { directory: dir } : {}),
    ...(input.content ? { content: input.content } : {}),
  });
}
