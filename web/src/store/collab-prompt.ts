// web/src/store/collab-prompt.ts
// Parser and display projection for external collaborator prompts
// (<untrusted_pm_request> and <untrusted_tester_report>) across both the
// compact attributed format and legacy boilerplate-wrapped session turns.
import type { MessageTurn } from "./workspace-state";
import type { SessionInfo } from "../services/sse-types";

export type CollabPromptRole = "pm" | "qa" | "op";
export type CollabPromptMode = "plan" | "build";
export type CollabPromptWorkspace = "worktree" | "local";
export type CollabPromptStage = "initial" | "start_build" | "followup";

export interface CollabPromptSection {
  heading: string;
  content: string;
}

export interface ParsedCollabPrompt {
  role: CollabPromptRole;
  author?: string;
  title?: string;
  mode?: CollabPromptMode;
  workspace?: CollabPromptWorkspace;
  stage?: CollabPromptStage;
  bodyText: string;
  sections: CollabPromptSection[];
  deniedPaths: string[];
  developerFeedback?: string;
  guardrailNotice?: string;
  legacy: boolean;
}

export interface CollabSessionTitleBadge {
  role: CollabPromptRole;
  cleanTitle: string;
}

export interface CollabSessionMeta {
  role: CollabPromptRole;
  author?: string;
  mode?: CollabPromptMode;
  workspace?: CollabPromptWorkspace;
  branch?: string;
  deniedPaths: string[];
  readOnlyPaths: string[];
  writablePaths: string[];
  title: string;
}

const PM_TAG = "untrusted_pm_request";
const QA_TAG = "untrusted_tester_report";
const OP_TAG = "untrusted_op_request";

function unescapeXmlAttr(value: string): string {
  return value.replace(/&(quot|lt|gt|amp|#10|#13|#9);/g, (_, ent: string) => {
    switch (ent) {
      case "quot":
        return '"';
      case "lt":
        return "<";
      case "gt":
        return ">";
      case "amp":
        return "&";
      case "#10":
        return "\n";
      case "#13":
        return "\r";
      case "#9":
        return "\t";
      default:
        return `&${ent};`;
    }
  });
}

function findTagEnd(src: string, startIndex: number): number {
  let quote: string | null = null;
  for (let i = startIndex; i < src.length; i++) {
    const ch = src[i];
    if (quote) {
      if (ch === quote) quote = null;
    } else if (ch === '"' || ch === "'") {
      quote = ch;
    } else if (ch === ">") {
      return i;
    }
  }
  return -1;
}

function parseXmlAttrs(rawAttrs: string): Record<string, string> {
  const attrs: Record<string, string> = {};
  let i = 0;
  const n = rawAttrs.length;
  while (i < n) {
    while (i < n && /\s/.test(rawAttrs[i])) i++;
    if (i >= n) break;
    const keyStart = i;
    while (i < n && /[A-Za-z0-9_:-]/.test(rawAttrs[i])) i++;
    const key = rawAttrs.slice(keyStart, i);
    while (i < n && /\s/.test(rawAttrs[i])) i++;
    if (!key || i >= n || rawAttrs[i] !== "=") {
      while (i < n && !/\s/.test(rawAttrs[i])) i++;
      continue;
    }
    i++;
    while (i < n && /\s/.test(rawAttrs[i])) i++;
    if (i >= n) break;
    const quote = rawAttrs[i];
    if (quote !== '"' && quote !== "'") {
      const valStart = i;
      while (i < n && !/\s/.test(rawAttrs[i])) i++;
      attrs[key] = unescapeXmlAttr(rawAttrs.slice(valStart, i));
      continue;
    }
    i++;
    const valStart = i;
    while (i < n && rawAttrs[i] !== quote) i++;
    attrs[key] = unescapeXmlAttr(rawAttrs.slice(valStart, i));
    if (i < n && rawAttrs[i] === quote) i++;
  }
  return attrs;
}

function parseModeAttr(raw?: string): CollabPromptMode | undefined {
  const clean = (raw || "").trim().toLowerCase();
  if (clean === "plan" || clean === "build") return clean;
  return undefined;
}

function parseWorkspaceAttr(raw?: string): CollabPromptWorkspace | undefined {
  const clean = (raw || "").trim().toLowerCase();
  if (clean === "local" || clean === "worktree") return clean;
  return undefined;
}

function parseStageAttr(raw?: string): CollabPromptStage | undefined {
  const clean = (raw || "").trim().toLowerCase();
  if (clean === "initial" || clean === "start_build" || clean === "followup") return clean;
  return undefined;
}

function splitDeniedPaths(raw?: string): string[] {
  if (!raw) return [];
  return raw
    .split(/[,，、]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

// Known Hoxi-generated section headings inside <untrusted_pm_request> / <untrusted_tester_report>.
const KNOWN_TITLE_HEADINGS = new Set([
  "\u9700\u6c42\u6807\u9898",
  "\u95ee\u9898\u6807\u9898",
]);

const KNOWN_UNWRAP_HEADINGS = new Set([
  "\u4ea7\u54c1\u7ecf\u7406\u8f93\u5165",
  "\u4ea7\u54c1\u7ecf\u7406\u5f00\u5de5\u8865\u5145",
]);

const KNOWN_SECTION_HEADINGS = new Set([
  ...KNOWN_TITLE_HEADINGS,
  ...KNOWN_UNWRAP_HEADINGS,
  "\u95ee\u9898\u63cf\u8ff0",
  "\u590d\u73b0\u6b65\u9aa4",
  "\u6d4b\u8bd5\u8865\u5145\u8bf4\u660e",
]);

function parseTaggedSections(inner: string, existingTitle?: string): {
  title?: string;
  bodyText: string;
  sections: CollabPromptSection[];
} {
  const trimmed = inner.trim();
  if (!trimmed) return { title: existingTitle, bodyText: "", sections: [] };
  if (!trimmed.startsWith("### ")) {
    return { title: existingTitle, bodyText: trimmed, sections: [] };
  }

  const lines = trimmed.split(/\r?\n/);
  const firstHeading = lines[0].slice(4).trim();
  if (!KNOWN_SECTION_HEADINGS.has(firstHeading)) {
    // Preserve user-authored Markdown headings verbatim so arbitrary "###" prose is never altered or dropped.
    return { title: existingTitle, bodyText: trimmed, sections: [] };
  }

  const rawSections: CollabPromptSection[] = [];
  let currentHeading = "";
  let currentLines: string[] = [];

  const flush = () => {
    if (!currentHeading) return;
    const content = currentLines.join("\n").trim();
    if (content) {
      rawSections.push({ heading: currentHeading, content });
    }
  };

  for (const line of lines) {
    if (line.startsWith("### ") && KNOWN_SECTION_HEADINGS.has(line.slice(4).trim())) {
      flush();
      currentHeading = line.slice(4).trim();
      currentLines = [];
    } else {
      currentLines.push(line);
    }
  }
  flush();

  if (rawSections.length === 0) {
    return { title: existingTitle, bodyText: trimmed, sections: [] };
  }

  let title = existingTitle;
  const remaining: CollabPromptSection[] = [];
  for (const sec of rawSections) {
    if (KNOWN_TITLE_HEADINGS.has(sec.heading)) {
      if (!title) title = sec.content;
      continue;
    }
    remaining.push(sec);
  }

  if (remaining.length === 0) {
    return { title, bodyText: title || trimmed, sections: [] };
  }
  if (remaining.length === 1 && KNOWN_UNWRAP_HEADINGS.has(remaining[0].heading)) {
    return { title, bodyText: remaining[0].content, sections: [] };
  }

  const bodyText = remaining
    .map((sec) => `${sec.heading}：${sec.content}`)
    .join("\n\n");
  return { title, bodyText, sections: remaining };
}

function extractLegacyMetadata(
  role: CollabPromptRole,
  prefix: string,
  suffix: string
): {
  mode?: CollabPromptMode;
  workspace?: CollabPromptWorkspace;
  stage?: CollabPromptStage;
  deniedPaths: string[];
  developerFeedback?: string;
  guardrailNotice?: string;
} {
  const combinedOuter = [prefix.trim(), suffix.trim()].filter(Boolean).join("\n\n");
  let mode: CollabPromptMode | undefined;
  let workspace: CollabPromptWorkspace | undefined;
  let stage: CollabPromptStage | undefined;

  if (role === "pm") {
    if (prefix.includes("\u53ea\u8bfb\u65b9\u6848\u5bf9\u9f50") || prefix.includes("Plan")) {
      mode = "plan";
      stage = prefix.includes("\u53d1\u8d77\u4e86\u65b0\u7684\u9700\u6c42\u5171\u521b\u4f1a\u8bdd") ? "initial" : "followup";
    } else if (
      prefix.includes("Build") ||
      prefix.includes("\u4e3b\u5de5\u4f5c\u533a\u76f4\u6539") ||
      prefix.includes("\u9694\u79bb Worktree") ||
      prefix.includes("\u5b9e\u73b0")
    ) {
      mode = "build";
      stage = prefix.includes("\u5fae\u8c03\u6216\u8865\u5145\u8981\u6c42") ? "followup" : "start_build";
    }
    if (prefix.includes("\u4e3b\u5de5\u4f5c\u533a\u76f4\u6539") || suffix.includes("\u6539\u52a8\u76f4\u63a5\u751f\u6548\u4e8e\u4e3b\u5de5\u4f5c\u533a")) {
      workspace = "local";
    } else if (
      prefix.includes("\u9694\u79bb Worktree") ||
      suffix.includes("\u9694\u79bb\u5de5\u4f5c\u6811")
    ) {
      workspace = "worktree";
    }
  } else {
    mode = "build";
    workspace = "worktree";
    stage = prefix.includes("\u590d\u6d4b\u540e\u53cd\u9988") ? "followup" : "initial";
  }

  let deniedPaths: string[] = [];
  const deniedMatch = prefix.match(/\u7981\u6b62\u4fee\u6539\u76ee\u5f55\u8303\u56f4[^：:]*[：:]\s*([^。\n]+)/);
  if (deniedMatch?.[1]) {
    deniedPaths = splitDeniedPaths(deniedMatch[1]);
  }

  let developerFeedback: string | undefined;
  const feedbackMatch = prefix.match(/\u9879\u76ee\u8d1f\u8d23\u4eba（\u7814\u53d1）\u6b64\u524d\u7684\u8bc4\u5ba1\u610f\u89c1[：:]\s*([\s\S]*?)(?:\n\n|$)/);
  if (feedbackMatch?.[1]?.trim()) {
    developerFeedback = feedbackMatch[1].trim();
  }

  return {
    mode,
    workspace,
    stage,
    deniedPaths,
    developerFeedback,
    guardrailNotice: combinedOuter || undefined,
  };
}

function isKnownLegacyPrefix(role: CollabPromptRole, prefix: string): boolean {
  const clean = prefix.trim();
  if (!clean) return false;
  if (role === "pm") {
    return (
      clean.startsWith("\u4ea7\u54c1\u7ecf\u7406") &&
      clean.includes("\u5b89\u5168\u8fb9\u754c\u8bf4\u660e") &&
      clean.includes(`<${PM_TAG}>`)
    );
  }
  return (
    (clean.startsWith("\u8bf7\u6392\u67e5\u5e76\u4fee\u590d\u4ee5\u4e0b\u7531\u6d4b\u8bd5\u4eba\u5458\u63d0\u4ea4") ||
      clean.startsWith("\u6d4b\u8bd5\u4eba\u5458\u590d\u6d4b\u540e\u53cd\u9988")) &&
    clean.includes("\u5b89\u5168\u8fb9\u754c\u8bf4\u660e") &&
    clean.includes(`<${QA_TAG}>`)
  );
}

function findBlockTag(
  src: string,
  tagName: string
): { openStart: number; openEnd: number; closeStart: number; closeEnd: number; attrsRaw: string } | null {
  const openToken = `<${tagName}`;
  const closeToken = `</${tagName}>`;
  let searchFrom = 0;

  while (searchFrom < src.length) {
    const openStart = src.indexOf(openToken, searchFrom);
    if (openStart < 0) return null;
    const afterName = openStart + openToken.length;
    const nextCh = src[afterName];
    if (nextCh && !/[\s>]/.test(nextCh)) {
      searchFrom = afterName;
      continue;
    }
    // Skip inline mentions inside the legacy security notice sentence ("下方 <untrusted_pm_request> 标签内的").
    const atLineStart = openStart === 0 || src[openStart - 1] === "\n" || src[openStart - 1] === "\r";
    if (!atLineStart) {
      searchFrom = afterName;
      continue;
    }
    const openEnd = findTagEnd(src, afterName);
    if (openEnd < 0) return null;
    if (openStart > 0) {
      const afterOpen = src[openEnd + 1];
      if (afterOpen && afterOpen !== "\n" && afterOpen !== "\r") {
        searchFrom = openEnd + 1;
        continue;
      }
    }
    const closeStart = src.lastIndexOf(closeToken);
    if (closeStart <= openEnd) return null;
    return {
      openStart,
      openEnd,
      closeStart,
      closeEnd: closeStart + closeToken.length,
      attrsRaw: src.slice(afterName, openEnd),
    };
  }
  return null;
}

function parseEnvelopeForTag(raw: string, tagName: string, role: CollabPromptRole): ParsedCollabPrompt | null {
  const trimmed = raw.trim();
  if (!trimmed.includes(`<${tagName}`)) return null;

  const block = findBlockTag(trimmed, tagName);
  if (!block) {
    // Handle legacy no-note BuildStartBuildPrompt ("目标需求：...") which carried the security notice without an XML body block.
    if (role === "pm" && isKnownLegacyPrefix("pm", trimmed)) {
      const targetMatch = trimmed.match(/\u76ee\u6807\u9700\u6c42[：:]\s*([^\n]+)/);
      if (targetMatch?.[1]?.trim()) {
        const title = targetMatch[1].trim();
        const legacyMeta = extractLegacyMetadata("pm", trimmed, "");
        return {
          role: "pm",
          title,
          mode: legacyMeta.mode ?? "build",
          workspace: legacyMeta.workspace,
          stage: "start_build",
          bodyText: title,
          sections: [],
          deniedPaths: legacyMeta.deniedPaths,
          developerFeedback: legacyMeta.developerFeedback,
          guardrailNotice: trimmed,
          legacy: true,
        };
      }
    }
    return null;
  }

  const prefix = trimmed.slice(0, block.openStart).trim();
  const suffix = trimmed.slice(block.closeEnd).trim();
  const isCompact = prefix === "" && suffix === "";

  if (!isCompact && !isKnownLegacyPrefix(role, prefix)) {
    // Avoid false positives when a developer quotes <untrusted_*> tags inside normal chat messages.
    return null;
  }

  const attrs = parseXmlAttrs(block.attrsRaw);
  const inner = trimmed.slice(block.openEnd + 1, block.closeStart);
  const structured = parseTaggedSections(inner, attrs.title || undefined);

  if (isCompact) {
    return {
      role,
      author: attrs.author || undefined,
      title: structured.title,
      mode: parseModeAttr(attrs.mode) ?? (role === "qa" ? "build" : undefined),
      workspace: parseWorkspaceAttr(attrs.workspace) ?? (role === "qa" ? "worktree" : undefined),
      stage: parseStageAttr(attrs.stage),
      bodyText: structured.bodyText,
      sections: structured.sections,
      deniedPaths: splitDeniedPaths(attrs.denied),
      legacy: false,
    };
  }

  const legacyMeta = extractLegacyMetadata(role, prefix, suffix);
  return {
    role,
    author: attrs.author || undefined,
    title: structured.title,
    mode: parseModeAttr(attrs.mode) ?? legacyMeta.mode,
    workspace: parseWorkspaceAttr(attrs.workspace) ?? legacyMeta.workspace,
    stage: parseStageAttr(attrs.stage) ?? legacyMeta.stage,
    bodyText: structured.bodyText,
    sections: structured.sections,
    deniedPaths:
      splitDeniedPaths(attrs.denied).length > 0
        ? splitDeniedPaths(attrs.denied)
        : legacyMeta.deniedPaths,
    developerFeedback: legacyMeta.developerFeedback,
    guardrailNotice: legacyMeta.guardrailNotice,
    legacy: true,
  };
}

/**
 * Parses an external collaborator prompt (<untrusted_pm_request> or <untrusted_tester_report>),
 * supporting both the compact attributed envelope and legacy boilerplate-wrapped prompts.
 * Returns null for ordinary user prompts (including developer messages that merely discuss the tags).
 */
export function parseCollabPrompt(raw: string): ParsedCollabPrompt | null {
  if (!raw || !raw.includes("<untrusted_")) return null;
  return (
    parseEnvelopeForTag(raw, PM_TAG, "pm") ??
    parseEnvelopeForTag(raw, QA_TAG, "qa") ??
    parseEnvelopeForTag(raw, OP_TAG, "op")
  );
}

export function formatCollabPromptForClipboard(raw: string): string | null {
  const parsed = parseCollabPrompt(raw);
  if (!parsed) return null;
  return parsed.bodyText || parsed.title || "";
}

export function formatCollabPromptForExport(raw: string): string | null {
  const parsed = parseCollabPrompt(raw);
  if (!parsed) return null;
  const roleTag = parsed.role === "pm" ? "PM" : parsed.role === "op" ? "OP" : "QA";
  const meta = [
    parsed.author ? `${roleTag} · ${parsed.author}` : roleTag,
    parsed.mode ? parsed.mode.toUpperCase() : "",
    parsed.workspace === "local" ? "Direct Local" : parsed.workspace === "worktree" ? "Worktree" : "",
  ]
    .filter(Boolean)
    .join(" · ");
  const header = `> **[${meta}]**${parsed.title && parsed.title !== parsed.bodyText ? ` ${parsed.title}` : ""}`;
  if (parsed.sections.length > 0) {
    const body = parsed.sections.map((s) => `**${s.heading}**\n\n${s.content}`).join("\n\n");
    return `${header}\n\n${body}`;
  }
  return parsed.bodyText ? `${header}\n\n${parsed.bodyText}` : header;
}

export function extractCollabPromptTitleFallback(raw: string): string | null {
  const parsed = parseCollabPrompt(raw);
  if (!parsed) return null;
  return parsed.title || parsed.bodyText || null;
}

const PM_TITLE_PREFIX = /^PM\s*·\s*(.+)$/;
const QA_TITLE_PREFIX = /^QA\s*·\s*(.+)$/;
const OP_TITLE_PREFIX = /^OP\s*·\s*(.+)$/;

/** Extracts a leading "PM · ", "QA · ", or "OP · " collaboration role badge from a session title. */
export function parseCollabSessionTitle(title?: string | null): CollabSessionTitleBadge | null {
  const trimmed = (title || "").trim();
  if (!trimmed) return null;
  const pm = trimmed.match(PM_TITLE_PREFIX);
  if (pm?.[1]?.trim()) return { role: "pm", cleanTitle: pm[1].trim() };
  const qa = trimmed.match(QA_TITLE_PREFIX);
  if (qa?.[1]?.trim()) return { role: "qa", cleanTitle: qa[1].trim() };
  const op = trimmed.match(OP_TITLE_PREFIX);
  if (op?.[1]?.trim()) return { role: "op", cleanTitle: op[1].trim() };
  return null;
}

/**
 * Derives collaboration session metadata (role, author, mode, workspace isolation, branch,
 * restricted paths) from persisted turns, session title, and worktree state.
 */
export function resolveCollabSessionMeta(
  session: SessionInfo | undefined,
  turns: readonly MessageTurn[],
  worktree?: { isolated?: boolean; branch?: string }
): CollabSessionMeta | null {
  let latest: ParsedCollabPrompt | null = null;
  let author: string | undefined;
  let deniedPaths: string[] = [];
  let inferredTitle = "";

  for (const turn of turns) {
    if (turn.info.role !== "user") continue;
    for (const part of turn.parts) {
      if (part.type !== "text" || !part.text) continue;
      const parsed = parseCollabPrompt(part.text);
      if (!parsed) continue;
      latest = parsed;
      if (parsed.author) author = parsed.author;
      if (parsed.deniedPaths.length > 0) deniedPaths = parsed.deniedPaths;
      if (parsed.title && !inferredTitle) inferredTitle = parsed.title;
    }
  }

  const titleBadge = parseCollabSessionTitle(session?.title);
  if (!latest && !titleBadge) return null;

  const role: CollabPromptRole = latest?.role ?? titleBadge!.role;
  const workspace: CollabPromptWorkspace | undefined = worktree?.isolated
    ? "worktree"
    : latest?.workspace ??
      (session?.sandboxMode === "worktree" || session?.sandboxMode === "local"
        ? session.sandboxMode
        : role === "qa"
          ? "worktree"
          : undefined);
  const mode: CollabPromptMode | undefined =
    session?.mode === "plan" || session?.mode === "build"
      ? session.mode
      : latest?.mode;
  const branch = (worktree?.branch || session?.branch || "").trim() || undefined;
  const title = titleBadge?.cleanTitle || inferredTitle || (session?.title || "").trim();

  const mergedReadOnly: string[] = [];
  const seenRO = new Set<string>();
  for (const p of [...(session?.readOnlyPaths ?? []), ...deniedPaths]) {
    const clean = (p || "").trim();
    if (!clean || seenRO.has(clean)) continue;
    seenRO.add(clean);
    mergedReadOnly.push(clean);
  }
  const writablePaths: string[] = [];
  const seenRW = new Set<string>();
  for (const p of session?.writablePaths ?? []) {
    const clean = (p || "").trim();
    if (!clean || seenRW.has(clean)) continue;
    seenRW.add(clean);
    writablePaths.push(clean);
  }

  return {
    role,
    author,
    mode,
    workspace,
    branch,
    deniedPaths: mergedReadOnly,
    readOnlyPaths: mergedReadOnly,
    writablePaths,
    title,
  };
}
