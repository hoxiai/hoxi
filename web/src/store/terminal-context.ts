// web/src/store/terminal-context.ts
import { createSignal } from "solid-js";
import { hoxiAPI } from "../services/api";
import type { MessagePart, SnapshotFileDiff } from "../services/sse-types";
import { t } from "../i18n";
import { loadSessionMessages } from "./workspace-sessions";
import { setWorkspace, workspace } from "./workspace-state";
import { normalizeFolderPath } from "./workspace-folders";
import { isSessionDeleted, sessionDirectory } from "./session-query";

const MAX_TERMINAL_BUFFER_CHARS = 24_000;
export const MAX_TERMINAL_CONTEXT_CHARS = 6_000;
export const MAX_CHANGES_CONTEXT_CHARS = 8_000;
export const MAX_SESSION_CONTEXT_CHARS = 4_000;
export const MAX_WORKTREE_CONTEXT_CHARS = 6_000;

const terminalBuffersByDir = new Map<string, string>();
const [terminalBufferVersion, setTerminalBufferVersion] = createSignal(0);
interface MentionRead { directory: string | undefined; token: object; promise: Promise<void> }
interface MentionAttempt { directory: string | undefined; at: number }
const inflightSessionLoads = new Map<string, MentionRead>();
const inflightDiffLoads = new Map<string, MentionRead>();
// Mention chips re-run on every keystroke; remember recent attempts so a clean
// worktree, an empty session or a half-typed ID is fetched at most once per window.
const MENTION_REFETCH_MS = 15_000;
const sessionLoadAttemptAt = new Map<string, MentionAttempt>();
const diffLoadAttemptAt = new Map<string, MentionAttempt>();

function mentionDirectory(key: string): string | undefined {
  return sessionDirectory(key) || workspace.rootDir || undefined;
}

function loadMentionData(
  key: string, forSend: boolean, reads: Map<string, MentionRead>, attempts: Map<string, MentionAttempt>,
  hasData: () => boolean, load: (isCurrent: () => boolean) => Promise<void>
): Promise<void> {
  const directory = mentionDirectory(key);
  const pending = reads.get(key);
  if (pending && pending.directory === directory) return pending.promise;
  if (hasData()) return Promise.resolve();
  const attempt = attempts.get(key);
  if (!forSend && attempt && attempt.directory === directory && Date.now() - attempt.at < MENTION_REFETCH_MS) return Promise.resolve();
  attempts.delete(key);
  if (attempts.size >= 512) attempts.delete(attempts.keys().next().value!);
  attempts.set(key, { directory, at: Date.now() });
  const token = {};
  const isCurrent = () => reads.get(key)?.token === token && mentionDirectory(key) === directory && !isSessionDeleted(key);
  const promise = (async () => {
    await load(isCurrent);
    if (!isCurrent()) throw new Error(t("timeline.sessionUnavailable"));
  })().finally(() => { if (reads.get(key)?.token === token) reads.delete(key); });
  reads.set(key, { directory, token, promise });
  return promise;
}

export interface ContextMentionChipInfo {
  token: string;
  kind: "terminal" | "changes" | "session" | "worktree";
  label: string;
  summary: string;
  status: "ready" | "truncated" | "empty" | "error";
}

function normalizeDirKey(directory?: string | null): string {
  const norm = normalizeFolderPath(directory || workspace.rootDir || "");
  return norm || "__root__";
}

/**
 * Strips ANSI CSI/OSC escape codes and normalizes carriage returns into readable text.
 */
export function stripAnsiEscapeCodes(raw: string): string {
  if (!raw) return "";
  return raw
    // Strip OSC sequences: ESC ] ... BEL or ESC \
    .replace(/\x1b\][\s\S]*?(?:\x07|\x1b\\)/g, "")
    // Strip CSI and standard ANSI sequences
    .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, "")
    .replace(/\x1b[@-_]/g, "")
    .replace(/\r\n/g, "\n")
    .replace(/\r/g, "\n");
}

export function appendTerminalOutput(directory: string | undefined, rawChunk: string): void {
  const cleaned = stripAnsiEscapeCodes(rawChunk);
  if (!cleaned) return;
  const key = normalizeDirKey(directory);
  const prev = terminalBuffersByDir.get(key) ?? "";
  let next = prev + cleaned;
  if (next.length > MAX_TERMINAL_BUFFER_CHARS) {
    next = next.slice(next.length - MAX_TERMINAL_BUFFER_CHARS);
  }
  terminalBuffersByDir.set(key, next);
  setTerminalBufferVersion((v) => v + 1);
}

export function clearTerminalOutput(directory?: string): void {
  if (directory === undefined) {
    terminalBuffersByDir.clear();
  } else {
    terminalBuffersByDir.delete(normalizeDirKey(directory));
  }
  setTerminalBufferVersion((v) => v + 1);
}

export const terminalPTYAPI = {
  createPTY: (directory: string) => hoxiAPI.createPTY(directory),
  deletePTY: (id: string, directory: string) => hoxiAPI.deletePTY(id, directory),
  resizePTY: (id: string, cols: number, rows: number, directory: string) =>
    hoxiAPI.resizePTY(id, cols, rows, directory),
};

function collectSessionShellFallback(sessionID: string | null): string {
  if (!sessionID) return "";
  const turns = workspace.messagesBySession[sessionID] ?? [];
  const snippets: string[] = [];
  for (let i = turns.length - 1; i >= 0 && snippets.length < 4; i--) {
    const turn = turns[i];
    for (let j = turn.parts.length - 1; j >= 0 && snippets.length < 4; j--) {
      const p = turn.parts[j] as MessagePart & {
        tool?: string;
        state?: {
          status?: string;
          input?: Record<string, any>;
          output?: string;
          error?: string;
        };
      };
      if (p.type !== "tool") continue;
      const toolName = (p.tool || "").toLowerCase();
      if (!toolName.includes("bash") && !toolName.includes("shell") && !toolName.includes("exec")) {
        continue;
      }
      const cmd = p.state?.input?.command || p.state?.input?.cmd || toolName;
      const out = stripAnsiEscapeCodes(p.state?.output || p.state?.error || "").trim();
      if (!out) continue;
      const statusTag = p.state?.status === "error" ? " [ERROR]" : "";
      snippets.unshift(`$ ${cmd}${statusTag}\n${out.slice(-2000)}`);
    }
  }
  return snippets.join("\n\n");
}

export function getTerminalOutputSnapshot(
  directory?: string | null,
  sessionID?: string | null,
  maxChars = MAX_TERMINAL_CONTEXT_CHARS
): {
  text: string;
  totalChars: number;
  lineCount: number;
  truncated: boolean;
  source: "pty" | "shell-tools" | "none";
} {
  // Subscribe to reactive buffer updates
  terminalBufferVersion();
  const primaryKey = normalizeDirKey(directory);
  let raw = (terminalBuffersByDir.get(primaryKey) ?? "").trim();
  let source: "pty" | "shell-tools" | "none" = raw ? "pty" : "none";

  if (!raw && primaryKey !== "__root__") {
    const rootFallback = (terminalBuffersByDir.get(normalizeDirKey(workspace.rootDir)) ?? "").trim();
    if (rootFallback) {
      raw = rootFallback;
      source = "pty";
    }
  }

  if (!raw && sessionID) {
    const shellFallback = collectSessionShellFallback(sessionID).trim();
    if (shellFallback) {
      raw = shellFallback;
      source = "shell-tools";
    }
  }

  if (!raw) {
    return { text: "", totalChars: 0, lineCount: 0, truncated: false, source: "none" };
  }

  const totalChars = raw.length;
  const truncated = totalChars > maxChars;
  const text = truncated ? raw.slice(totalChars - maxChars) : raw;
  const lineCount = text.split("\n").length;
  return { text, totalChars, lineCount, truncated, source };
}

function resolveDiffsForSession(sessionID: string | null): SnapshotFileDiff[] {
  if (!sessionID) {
    return workspace.worktreeDiffsBySession["__root__"] ?? workspace.diffsBySession["__root__"] ?? [];
  }
  const sessionDiffs = workspace.diffsBySession[sessionID] ?? [];
  if (sessionDiffs.length > 0) return sessionDiffs;
  const wtDiffs = workspace.worktreeDiffsBySession[sessionID] ?? [];
  if (wtDiffs.length > 0) return wtDiffs;
  const isIsolated =
    Boolean(workspace.worktreeBySession[sessionID]?.isolated) ||
    workspace.sessions.find((s) => s.id === sessionID)?.sandboxMode === "worktree";
  if (isIsolated) return [];
  const dir = sessionDirectory(sessionID);
  if (dir && workspace.rootDir && normalizeFolderPath(dir) !== normalizeFolderPath(workspace.rootDir)) {
    return [];
  }
  return workspace.worktreeDiffsBySession["__root__"] ?? [];
}

function synthesizeFilePatch(d: SnapshotFileDiff): string {
  if (d.patch && d.patch.trim()) {
    return d.patch.trim();
  }
  if (typeof d.before === "string" || typeof d.after === "string") {
    const beforeLines = (d.before ?? "").split("\n").slice(0, 40).map((l) => `-${l}`).join("\n");
    const afterLines = (d.after ?? "").split("\n").slice(0, 40).map((l) => `+${l}`).join("\n");
    return `--- a/${d.file}\n+++ b/${d.file}\n${beforeLines}\n${afterLines}`.trim();
  }
  return "";
}

export function formatUnifiedDiffsContext(
  diffs: SnapshotFileDiff[],
  maxChars = MAX_CHANGES_CONTEXT_CHARS
): {
  summaryLines: string;
  patchesBlock: string;
  fileCount: number;
  additions: number;
  deletions: number;
  truncated: boolean;
} {
  let additions = 0;
  let deletions = 0;
  for (const d of diffs) {
    additions += d.additions || 0;
    deletions += d.deletions || 0;
  }

  const maxFiles = 15;
  const summaryLines = diffs
    .slice(0, maxFiles)
    .map((d) => `- \`${d.file}\` (+${d.additions} -${d.deletions})`)
    .join("\n");

  let truncated = diffs.length > maxFiles;
  const patchChunks: string[] = [];
  let usedChars = 0;

  for (const d of diffs.slice(0, maxFiles)) {
    const patch = synthesizeFilePatch(d);
    if (!patch) continue;
    const header = `### ${d.file} (+${d.additions} -${d.deletions})\n\`\`\`diff\n`;
    const footer = `\n\`\`\``;
    const remaining = maxChars - usedChars - header.length - footer.length;
    if (remaining <= 120) {
      truncated = true;
      break;
    }
    if (patch.length > remaining) {
      patchChunks.push(`${header}${patch.slice(0, remaining)}\n... [diff truncated]${footer}`);
      usedChars = maxChars;
      truncated = true;
      break;
    }
    patchChunks.push(`${header}${patch}${footer}`);
    usedChars += header.length + patch.length + footer.length;
  }

  return {
    summaryLines,
    patchesBlock: patchChunks.join("\n\n"),
    fileCount: diffs.length,
    additions,
    deletions,
    truncated,
  };
}

export function formatSessionContextBlock(
  refID: string,
  maxChars = MAX_SESSION_CONTEXT_CHARS
): {
  title: string;
  body: string;
  turnCount: number;
  errorCount: number;
  truncated: boolean;
  found: boolean;
} {
  const targetSession = workspace.sessions.find((s) => s.id === refID);
  const turns = workspace.messagesBySession[refID] ?? [];
  const title = targetSession?.title || refID;
  if (!targetSession && turns.length === 0) {
    return { title, body: "", turnCount: 0, errorCount: 0, truncated: false, found: false };
  }

  const recentTurns = turns.slice(-6);
  const textSections: string[] = [];
  const toolErrors: string[] = [];
  let truncated = turns.length > 6;

  for (const turn of recentTurns) {
    const roleLabel = turn.info.role.toUpperCase();
    for (const part of turn.parts) {
      if (part.type === "text") {
        const raw = ((part as any).text || "").trim();
        if (!raw) continue;
        if (raw.length > 900) {
          truncated = true;
          textSections.push(`${roleLabel}: ${raw.slice(0, 900)}...`);
        } else {
          textSections.push(`${roleLabel}: ${raw}`);
        }
      } else if (part.type === "tool") {
        const tp = part as any;
        if (tp.state?.status === "error") {
          const errText = stripAnsiEscapeCodes(tp.state?.error || tp.state?.output || "tool failed")
            .trim()
            .slice(0, 400);
          toolErrors.push(`- Tool \`${tp.tool || "tool"}\` failed: ${errText}`);
        }
      }
    }
  }

  let body = textSections.join("\n\n");
  if (toolErrors.length > 0) {
    body = `${body}\n\nKey Tool Errors:\n${toolErrors.slice(-4).join("\n")}`.trim();
  }
  if (body.length > maxChars) {
    body = `${body.slice(0, maxChars)}\n... [session context truncated]`;
    truncated = true;
  }

  return {
    title,
    body,
    turnCount: turns.length,
    errorCount: toolErrors.length,
    truncated,
    found: true,
  };
}

function ensureSessionMessagesLoaded(refID: string, forSend = false): Promise<void> {
  if (!refID) return Promise.resolve();
  return loadMentionData(refID, forSend, inflightSessionLoads, sessionLoadAttemptAt,
    () => (workspace.messagesBySession[refID]?.length ?? 0) > 0,
    () => loadSessionMessages(refID));
}

function ensureWorktreeDiffsLoaded(refID: string, forSend = false): Promise<void> {
  const key = refID || "__root__";
  const hasOwnDiffs = () =>
    (refID ? (workspace.diffsBySession[refID]?.length ?? 0) > 0 : false) ||
    (workspace.worktreeDiffsBySession[key]?.length ?? 0) > 0;
  return loadMentionData(key, forSend, inflightDiffLoads, diffLoadAttemptAt, hasOwnDiffs, async (isCurrent) => {
    const dir = mentionDirectory(key);
    const diffs = await hoxiAPI.getWorktreeDiff(key, dir);
    if (!isCurrent()) return;
    if (Array.isArray(diffs)) {
      setWorkspace("worktreeDiffsBySession", key, diffs);
      if (refID && (workspace.diffsBySession[refID]?.length ?? 0) === 0) {
        setWorkspace("diffsBySession", refID, diffs);
      }
    }
  });
}

/**
 * Inspects active `@terminal`, `@changes`, `@session:<id>`, and `@worktree:<id>` mentions
 * in the prompt so the Composer can display status chips (ready, truncated, empty/error).
 */
export function inspectPromptContextMentions(
  rawPrompt: string,
  sessionID: string | null
): ContextMentionChipInfo[] {
  if (!rawPrompt || !rawPrompt.includes("@")) return [];
  const chips: ContextMentionChipInfo[] = [];
  const seen = new Set<string>();
  const dir = sessionDirectory(sessionID) || workspace.rootDir;

  if (/(?:^|[^a-zA-Z0-9_])@terminal\b/.test(rawPrompt)) {
    seen.add("@terminal");
    const snap = getTerminalOutputSnapshot(dir, sessionID, MAX_TERMINAL_CONTEXT_CHARS);
    chips.push({
      token: "@terminal",
      kind: "terminal",
      label: "@terminal",
      summary:
        snap.source === "none"
          ? t("timeline.mentionStatusEmpty")
          : t("timeline.mentionTerminalLines", { count: snap.lineCount }),
      status: snap.source === "none" ? "empty" : snap.truncated ? "truncated" : "ready",
    });
  }

  if (/(?:^|[^a-zA-Z0-9_])@changes\b/.test(rawPrompt)) {
    seen.add("@changes");
    void ensureWorktreeDiffsLoaded(sessionID || "__root__").catch(() => {});
    const diffs = resolveDiffsForSession(sessionID);
    const formatted = formatUnifiedDiffsContext(diffs, MAX_CHANGES_CONTEXT_CHARS);
    chips.push({
      token: "@changes",
      kind: "changes",
      label: "@changes",
      summary:
        formatted.fileCount === 0
          ? t("timeline.mentionStatusEmpty")
          : `${formatted.fileCount} files (+${formatted.additions} -${formatted.deletions})`,
      status:
        formatted.fileCount === 0 ? "empty" : formatted.truncated ? "truncated" : "ready",
    });
  }

  for (const match of rawPrompt.matchAll(/@session:([a-zA-Z0-9_-]+)/g)) {
    const refID = match[1];
    const token = `@session:${refID}`;
    if (seen.has(token)) continue;
    seen.add(token);
    void ensureSessionMessagesLoaded(refID).catch(() => {});
    const info = formatSessionContextBlock(refID, MAX_SESSION_CONTEXT_CHARS);
    chips.push({
      token,
      kind: "session",
      label: `@session:${info.title.slice(0, 18)}`,
      summary: !info.found
        ? t("timeline.mentionStatusUnavailable")
        : info.turnCount === 0
          ? t("timeline.mentionStatusEmpty")
          : t("timeline.mentionSessionTurns", { count: info.turnCount }),
      status: !info.found
        ? "error"
        : info.turnCount === 0
          ? "empty"
          : info.truncated
            ? "truncated"
            : "ready",
    });
  }

  for (const match of rawPrompt.matchAll(/@worktree:([a-zA-Z0-9_-]+)/g)) {
    const refID = match[1];
    const token = `@worktree:${refID}`;
    if (seen.has(token)) continue;
    seen.add(token);
    void ensureWorktreeDiffsLoaded(refID).catch(() => {});
    const wt = workspace.worktreeBySession[refID];
    const diffs = resolveDiffsForSession(refID);
    const formatted = formatUnifiedDiffsContext(diffs, MAX_WORKTREE_CONTEXT_CHARS);
    chips.push({
      token,
      kind: "worktree",
      label: `@worktree:${(wt?.branch || refID).slice(0, 18)}`,
      summary: !wt
        ? t("timeline.mentionStatusUnavailable")
        : formatted.fileCount === 0
          ? wt.branch || refID
          : `${formatted.fileCount} files (+${formatted.additions} -${formatted.deletions})`,
      status: !wt ? "error" : formatted.truncated ? "truncated" : "ready",
    });
  }

  return chips;
}

export function hasAsyncContextMentions(rawPrompt: string): boolean {
  if (!rawPrompt || !rawPrompt.includes("@")) return false;
  return (
    /(?:^|[^a-zA-Z0-9_])@changes\b/.test(rawPrompt) ||
    /@session:([a-zA-Z0-9_-]+)/.test(rawPrompt) ||
    /@worktree:([a-zA-Z0-9_-]+)/.test(rawPrompt)
  );
}

/**
 * Ensures any referenced `@changes`, `@session:<id>`, or `@worktree:<id>` data is fetched
 * before expanding context mentions on send.
 */
export async function resolveContextMentionsForSend(
  rawPrompt: string,
  sessionID: string | null
): Promise<string> {
  const directory = sessionDirectory(sessionID) || workspace.rootDir;
  const tasks: Promise<void>[] = [];
  const requireMention = (mention: string, read: Promise<void>) => read.catch((error) => {
    throw new Error(t("timeline.mentionLoadFailed", { mention, error: error instanceof Error ? error.message : String(error) }));
  });
  if (/(?:^|[^a-zA-Z0-9_])@changes\b/.test(rawPrompt)) {
    tasks.push(requireMention("@changes", ensureWorktreeDiffsLoaded(sessionID || "__root__", true)));
  }
  for (const match of rawPrompt.matchAll(/@session:([a-zA-Z0-9_-]+)/g)) {
    tasks.push(requireMention(match[0], ensureSessionMessagesLoaded(match[1], true)));
  }
  for (const match of rawPrompt.matchAll(/@worktree:([a-zA-Z0-9_-]+)/g)) {
    tasks.push(requireMention(match[0], ensureWorktreeDiffsLoaded(match[1], true)));
  }
  if (tasks.length > 0) {
    await Promise.all(tasks);
  }
  if ((sessionDirectory(sessionID) || workspace.rootDir) !== directory || (sessionID && isSessionDeleted(sessionID))) {
    throw new Error(t("timeline.sessionUnavailable"));
  }
  return expandContextMentions(rawPrompt, sessionID);
}

/**
 * Expands `@terminal`, `@changes`, `@session:<id>`, and `@worktree:<id>` tokens into
 * bounded, explicit context blocks with unified diffs, terminal logs, and key conclusions.
 */
export function expandContextMentions(rawPrompt: string, sessionID: string | null): string {
  const blocks: string[] = [];
  const dir = sessionDirectory(sessionID) || workspace.rootDir;

  if (/(?:^|[^a-zA-Z0-9_])@terminal\b/.test(rawPrompt)) {
    const snap = getTerminalOutputSnapshot(dir, sessionID, MAX_TERMINAL_CONTEXT_CHARS);
    if (snap.text) {
      const truncAttr = snap.truncated ? ` truncated="true" max_chars="${MAX_TERMINAL_CONTEXT_CHARS}"` : "";
      blocks.push(
        `<terminal_context source="${snap.source}" lines="${snap.lineCount}"${truncAttr}>\n${snap.text}\n</terminal_context>`
      );
    } else {
      blocks.push(
        `<terminal_context status="empty">\nNo recent terminal or shell output captured for this workspace.\n</terminal_context>`
      );
    }
  }

  if (/(?:^|[^a-zA-Z0-9_])@changes\b/.test(rawPrompt)) {
    const diffs = resolveDiffsForSession(sessionID);
    if (diffs.length > 0) {
      const formatted = formatUnifiedDiffsContext(diffs, MAX_CHANGES_CONTEXT_CHARS);
      const truncNote = formatted.truncated
        ? `\n[Note: Diff output truncated to ${MAX_CHANGES_CONTEXT_CHARS} chars]`
        : "";
      const patchSection = formatted.patchesBlock
        ? `\n\nUnified Diffs:\n${formatted.patchesBlock}${truncNote}`
        : "";
      blocks.push(
        `<git_changes_context files="${formatted.fileCount}" additions="${formatted.additions}" deletions="${formatted.deletions}" truncated="${formatted.truncated}">\nCurrent session changed files:\n${formatted.summaryLines}${patchSection}\n</git_changes_context>`
      );
    }
  }

  for (const match of rawPrompt.matchAll(/@session:([a-zA-Z0-9_-]+)/g)) {
    const refID = match[1];
    const info = formatSessionContextBlock(refID, MAX_SESSION_CONTEXT_CHARS);
    if (info.found || info.body) {
      blocks.push(
        `<session_context id="${refID}" title="${info.title}" turns="${info.turnCount}" errors="${info.errorCount}" truncated="${info.truncated}">\n${info.body || "Referenced session"}\n</session_context>`
      );
    }
  }

  for (const match of rawPrompt.matchAll(/@worktree:([a-zA-Z0-9_-]+)/g)) {
    const refID = match[1];
    const wt = workspace.worktreeBySession[refID];
    const diffs = resolveDiffsForSession(refID);
    if (wt) {
      const formatted = formatUnifiedDiffsContext(diffs, MAX_WORKTREE_CONTEXT_CHARS);
      const patchSection = formatted.patchesBlock ? `\n\nUnified Diffs:\n${formatted.patchesBlock}` : "";
      blocks.push(
        `<worktree_context session="${refID}" branch="${wt.branch || refID}" path="${wt.directory}" truncated="${formatted.truncated}">\n${formatted.summaryLines || "Isolated worktree branch"}${patchSection}\n</worktree_context>`
      );
    }
  }

  return blocks.length > 0 ? `${rawPrompt}\n\n${blocks.join("\n\n")}` : rawPrompt;
}
