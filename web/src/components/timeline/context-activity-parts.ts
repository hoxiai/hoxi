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

export function isTodoToolPart(part: MessagePart): boolean {
  if (part.type !== "tool") return false;
  const lower = part.tool.toLowerCase();
  return lower === "todowrite" || lower === "todoread";
}

export function isVisibleTextPart(
  part: MessagePart
): part is Extract<MessagePart, { type: "text" }> {
  return part.type === "text" && part.text.trim().length > 0;
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
