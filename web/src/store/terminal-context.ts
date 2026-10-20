// web/src/store/terminal-context.ts
import { createSignal } from "solid-js";
import { hoxiAPI } from "../services/api";
import type { MessagePart, SnapshotFileDiff } from "../services/sse-types";
import { t } from "../i18n";
import { loadSessionMessages } from "./workspace-sessions";
import { setWorkspace, workspace } from "./workspace-state";
import { normalizeFolderPath } from "./workspace-folders";
import { sessionDirectory } from "./session-query";

const MAX_TERMINAL_BUFFER_CHARS = 24_000;
export const MAX_TERMINAL_CONTEXT_CHARS = 6_000;
export const MAX_CHANGES_CONTEXT_CHARS = 8_000;
export const MAX_SESSION_CONTEXT_CHARS = 4_000;
export const MAX_WORKTREE_CONTEXT_CHARS = 6_000;

const terminalBuffersByDir = new Map<string, string>();
const [terminalBufferVersion, setTerminalBufferVersion] = createSignal(0);
const inflightSessionLoads = new Set<string>();
const inflightDiffLoads = new Set<string>();
// Mention chips re-run on every keystroke; remember recent attempts so a clean
// worktree, an empty session or a half-typed ID is fetched at most once per window.
const MENTION_REFETCH_MS = 15_000;
const sessionLoadAttemptAt = new Map<string, number>();
const diffLoadAttemptAt = new Map<string, number>();

function attemptedRecently(attempts: Map<string, number>, key: string): boolean {
  const at = attempts.get(key);
  return at !== undefined && Date.now() - at < MENTION_REFETCH_MS;
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

async function ensureSessionMessagesLoaded(refID: string): Promise<void> {
  if (!refID || (workspace.messagesBySession[refID]?.length ?? 0) > 0) return;
  if (inflightSessionLoads.has(refID) || attemptedRecently(sessionLoadAttemptAt, refID)) return;
  inflightSessionLoads.add(refID);
  sessionLoadAttemptAt.set(refID, Date.now());
  try {
    await loadSessionMessages(refID);
  } catch {
    /* ignore background fetch errors */
  } finally {
    inflightSessionLoads.delete(refID);
  }
}

async function ensureWorktreeDiffsLoaded(refID: string): Promise<void> {
  const key = refID || "__root__";
  const existing = resolveDiffsForSession(refID);
  if (existing.length > 0 || inflightDiffLoads.has(key) || attemptedRecently(diffLoadAttemptAt, key)) return;
  inflightDiffLoads.add(key);
  diffLoadAttemptAt.set(key, Date.now());
  try {
    const dir = sessionDirectory(refID) || workspace.rootDir || undefined;
    const diffs = await hoxiAPI.getWorktreeDiff(key, dir);
    if (Array.isArray(diffs)) {
      setWorkspace("worktreeDiffsBySession", key, diffs);
      if (refID && (workspace.diffsBySession[refID]?.length ?? 0) === 0) {
        setWorkspace("diffsBySession", refID, diffs);
      }
    }
  } catch {
    /* ignore background fetch errors */
  } finally {
    inflightDiffLoads.delete(key);
  }
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

  if (/(?:^|\s)@terminal\b/.test(rawPrompt)) {
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

  if (/(?:^|\s)@changes\b/.test(rawPrompt)) {
    seen.add("@changes");
    void ensureWorktreeDiffsLoaded(sessionID || "__root__");
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
    void ensureSessionMessagesLoaded(refID);
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
    void ensureWorktreeDiffsLoaded(refID);
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

/**
 * Ensures any referenced `@changes`, `@session:<id>`, or `@worktree:<id>` data is fetched
 * before expanding context mentions on send.
 */
export async function resolveContextMentionsForSend(
  rawPrompt: string,
  sessionID: string | null
): Promise<string> {
  const tasks: Promise<void>[] = [];
  if (/(?:^|\s)@changes\b/.test(rawPrompt)) {
    tasks.push(ensureWorktreeDiffsLoaded(sessionID || "__root__"));
  }
  for (const match of rawPrompt.matchAll(/@session:([a-zA-Z0-9_-]+)/g)) {
    tasks.push(ensureSessionMessagesLoaded(match[1]));
  }
  for (const match of rawPrompt.matchAll(/@worktree:([a-zA-Z0-9_-]+)/g)) {
    tasks.push(ensureWorktreeDiffsLoaded(match[1]));
  }
  if (tasks.length > 0) {
    await Promise.allSettled(tasks);
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

  if (/(?:^|\s)@terminal\b/.test(rawPrompt)) {
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

  if (/(?:^|\s)@changes\b/.test(rawPrompt)) {
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
