// web/src/components/timeline/context-activity-parts.ts
import type { MessagePart, ToolPart } from "../../services/sse-types";
import { isSessionBusy, isSubagentToolInterrupted } from "../../store/workspace";
import {
  extractExitCode,
  extractTargetFile,
  isEditingTool,
  isShellTool,
  isTaskTool,
} from "./tool-card-helpers";
import { resolveToolFileDiagnostics } from "./tool-card-state";

export interface InlineThinkingSplit {
  hasLeadingThinking: boolean;
  thinkingText: string;
  visibleText: string;
  isUnclosedThinking: boolean;
}

const PARTIAL_OPEN_THINK_RE = /^<(?:t(?:h(?:i(?:n(?:k(?:i(?:n(?:g)?)?)?)?)?)?)?)?$/i;
const OPEN_THINK_TAG_RE = /^<think(?:ing)?>/i;
const CLOSE_THINK_TAG_RE = /<\/think(?:ing)?>/i;
const PARTIAL_CLOSE_THINK_SUFFIX_RE = /<\/(?:t(?:h(?:i(?:n(?:k(?:i(?:n(?:g)?)?)?)?)?)?)?)?$/i;

/**
 * Splits leading `<think>` / `<thinking>` blocks out of an assistant `text`
 * part. Handles closed blocks followed by visible reply text, pure thinking
 * blocks, and unclosed blocks emitted before a tool call or while streaming.
 */
export function splitInlineThinking(rawText?: string | null): InlineThinkingSplit {
  const source = rawText ?? "";
  const trimmedStart = source.trimStart();
  if (!trimmedStart.startsWith("<")) {
    return {
      hasLeadingThinking: false,
      thinkingText: "",
      visibleText: source,
      isUnclosedThinking: false,
    };
  }
  if (PARTIAL_OPEN_THINK_RE.test(trimmedStart)) {
    return {
      hasLeadingThinking: true,
      thinkingText: "",
      visibleText: "",
      isUnclosedThinking: true,
    };
  }
  if (!OPEN_THINK_TAG_RE.test(trimmedStart)) {
    return {
      hasLeadingThinking: false,
      thinkingText: "",
      visibleText: source,
      isUnclosedThinking: false,
    };
  }

  let cursor = trimmedStart;
  const thinkingChunks: string[] = [];
  let isUnclosedThinking = false;

  while (true) {
    const leading = cursor.trimStart();
    if (leading && PARTIAL_OPEN_THINK_RE.test(leading)) {
      cursor = "";
      isUnclosedThinking = true;
      break;
    }
    const openMatch = leading.match(OPEN_THINK_TAG_RE);
    if (!openMatch) {
      cursor = leading;
      break;
    }
    const afterOpen = leading.slice(openMatch[0].length);
    const closeMatch = CLOSE_THINK_TAG_RE.exec(afterOpen);
    if (!closeMatch || closeMatch.index === undefined) {
      const cleanedTail = afterOpen.replace(PARTIAL_CLOSE_THINK_SUFFIX_RE, "").trim();
      if (cleanedTail) thinkingChunks.push(cleanedTail);
      cursor = "";
      isUnclosedThinking = true;
      break;
    }
    const inner = afterOpen.slice(0, closeMatch.index).trim();
    if (inner) thinkingChunks.push(inner);
    cursor = afterOpen.slice(closeMatch.index + closeMatch[0].length);
  }

  return {
    hasLeadingThinking: true,
    thinkingText: thinkingChunks.join("\n\n"),
    visibleText: cursor.trimStart(),
    isUnclosedThinking,
  };
}

export function extractVisibleAssistantText(rawText?: string | null): string {
  const split = splitInlineThinking(rawText);
  return split.hasLeadingThinking ? split.visibleText : (rawText ?? "");
}

type TextPartWithTime = Extract<MessagePart, { type: "text" }> & {
  time?: { start: number; end?: number };
};
type ReasoningPart = Extract<MessagePart, { type: "reasoning" }>;

interface InlinePartWrappers {
  pureReasoning: ReasoningPart;
  leadReasoning: ReasoningPart;
  visibleText: TextPartWithTime;
}

const inlinePartWrapperCache = new WeakMap<object, InlinePartWrappers>();

function getInlinePartWrappers(part: TextPartWithTime): InlinePartWrappers {
  let cached = inlinePartWrapperCache.get(part);
  if (cached) return cached;

  const resolveTime = (forceCompleted: boolean): { start: number; end?: number } => {
    const rawTime = part.time;
    const start = rawTime?.start ?? 0;
    if (rawTime?.end != null) {
      return { start, end: rawTime.end };
    }
    const split = splitInlineThinking(part.text);
    if (forceCompleted || !split.isUnclosedThinking) {
      return { start, end: start || 1 };
    }
    return { start };
  };

  const pureReasoning: ReasoningPart = {
    get id() {
      return part.id;
    },
    get sessionID() {
      return part.sessionID;
    },
    get messageID() {
      return part.messageID;
    },
    type: "reasoning",
    get text() {
      return splitInlineThinking(part.text).thinkingText;
    },
    get time() {
      return resolveTime(false);
    },
  };

  const leadReasoning: ReasoningPart = {
    get id() {
      return `${part.id}#thinking`;
    },
    get sessionID() {
      return part.sessionID;
    },
    get messageID() {
      return part.messageID;
    },
    type: "reasoning",
    get text() {
      return splitInlineThinking(part.text).thinkingText;
    },
    get time() {
      return resolveTime(true);
    },
  };

  const visibleText: TextPartWithTime = {
    get id() {
      return part.id;
    },
    get sessionID() {
      return part.sessionID;
    },
    get messageID() {
      return part.messageID;
    },
    type: "text",
    get text() {
      return splitInlineThinking(part.text).visibleText;
    },
    get metadata() {
      return part.metadata;
    },
    get time() {
      return part.time;
    },
  };

  cached = { pureReasoning, leadReasoning, visibleText };
  inlinePartWrapperCache.set(part, cached);
  return cached;
}

/**
 * Expands assistant `text` parts that start with `<think>` / `<thinking>` into
 * normalized `reasoning` + visible `text` parts while preserving stable wrapper
 * references per underlying part for `stabilizeTurnRenderItems`.
 */
export function expandInlineThinkingParts(parts: MessagePart[]): MessagePart[] {
  let changed = false;
  const out: MessagePart[] = [];
  for (const part of parts) {
    if (part.type !== "text") {
      out.push(part);
      continue;
    }
    const split = splitInlineThinking(part.text);
    if (!split.hasLeadingThinking) {
      out.push(part);
      continue;
    }
    changed = true;
    const wrappers = getInlinePartWrappers(part as TextPartWithTime);
    const hasVisible = split.visibleText.trim().length > 0;
    const hasThinking = split.thinkingText.trim().length > 0 || split.isUnclosedThinking;
    if (!hasVisible) {
      if (hasThinking) out.push(wrappers.pureReasoning);
      continue;
    }
    if (split.thinkingText.trim().length > 0) {
      out.push(wrappers.leadReasoning);
    }
    out.push(wrappers.visibleText);
  }
  return changed ? out : parts;
}

export function isTodoToolPart(part: MessagePart): boolean {
  if (part.type !== "tool") return false;
  const lower = part.tool.toLowerCase();
  return lower === "todowrite" || lower === "todoread";
}

export function isVisibleTextPart(
  part: MessagePart
): part is Extract<MessagePart, { type: "text" }> {
  return part.type === "text" && extractVisibleAssistantText(part.text).trim().length > 0;
}

export function isContextToolPart(
  part: MessagePart
): part is Extract<MessagePart, { type: "tool" }> {
  if (part.type !== "tool") return false;
  if (part.state.status === "error") return false;
  const t = part.tool.toLowerCase();
  return (
    t === "read" ||
    t.endsWith("_read") ||
    t === "grep" ||
    t === "glob" ||
    t === "list" ||
    t === "ls"
  );
}

export function isWorkPart(part: MessagePart): boolean {
  if (part.type === "reasoning") {
    return part.time?.end == null || Boolean(part.text && part.text.trim().length > 0);
  }
  if (part.type === "text") {
    const split = splitInlineThinking(part.text);
    return (
      split.hasLeadingThinking &&
      !split.visibleText.trim() &&
      (split.isUnclosedThinking || split.thinkingText.trim().length > 0)
    );
  }
  return part.type === "tool";
}

export const isExplorationPart = isWorkPart;

export function isAttentionToolPart(part: MessagePart): part is ToolPart {
  if (part.type !== "tool" || isTodoToolPart(part)) return false;
  if (part.state.status === "error") return true;
  if (
    !isTaskTool(part.tool) &&
    (part.state.status === "pending" || part.state.status === "running") &&
    !isSessionBusy(part.sessionID)
  ) {
    return true;
  }
  if (isShellTool(part.tool) && part.state.status === "completed") {
    const code = extractExitCode(part.state);
    if (code !== undefined && code !== 0) return true;
  }
  if (isEditingTool(part.tool) && part.state.status === "completed") {
    const file = extractTargetFile(part) || "";
    const diags = resolveToolFileDiagnostics(part, true, file);
    if (diags.some((d) => !d.severity || d.severity === 1)) return true;
  }
  if (isTaskTool(part.tool) && isSubagentToolInterrupted(part, part.sessionID)) {
    return true;
  }
  return false;
}
