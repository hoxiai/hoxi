// web/src/components/timeline/tool-taxonomy.ts
import type { IconName } from "../icons";
import { t } from "../../i18n";
import type { MessagePart } from "../../services/sse-types";

type ToolPart = Extract<MessagePart, { type: "tool" }>;

// Canonical tool names emitted by the local CLI runtimes. The Go runner
// normalizes most of them already, but persisted history and child sessions can
// still carry the raw identifier, so display and grouping resolve both forms.
const SHELL_TOOLS = new Set([
  "bash", "shell", "terminal", "execute_command", "run_command",
  "run_shell_command", "cmd_command", "cmd", "command", "powershell", "pwsh",
]);
const READ_TOOLS = new Set([
  "read", "read_file", "view_file", "read_many_files", "read_file_content", "open_file",
]);
const SEARCH_TOOLS = new Set([
  "grep", "grep_search", "search_file_content", "ripgrep", "rg", "search_file", "search",
]);
const LIST_TOOLS = new Set([
  "glob", "find", "find_by_name", "list", "ls", "list_dir", "list_directory", "list_files",
]);
const TODO_TOOLS = new Set(["todowrite", "todoread", "taskcreate", "taskupdate", "tasklist"]);
const TASK_TOOLS = new Set([
  "task", "agent", "invoke_subagent", "manage_subagents", "define_subagent",
  "browser_subagent", "spawn_agent",
]);
const QUESTION_TOOLS = new Set(["question", "ask_question"]);
const WEB_SEARCH_TOOLS = new Set(["websearch", "search_web", "web_search", "google_web_search"]);
const WEB_FETCH_TOOLS = new Set(["webfetch", "web_fetch", "read_url_content", "fetch_url"]);
const WRITE_TOOLS = new Set(["write", "write_file", "write_to_file", "create_file"]);
const PATCH_TOOLS = new Set(["patch", "apply_patch"]);
const EDIT_TOOLS = new Set([
  "edit", "edit_file", "replace", "replace_file_content", "multi_replace_file_content",
  "sed_file", "str_replace", "str_replace_editor", "notebook_edit",
]);
const MESSAGE_TOOLS = new Set(["send_message", "send_command_input"]);
const RESOURCE_TOOLS = new Set(["read_resource", "list_resources"]);
const WAIT_TOOLS = new Set(["wait", "wait_5_seconds", "schedule"]);

function lower(tool: string): string {
  return (tool || "").toLowerCase();
}

export function isShellTool(tool: string): boolean {
  const name = lower(tool);
  return SHELL_TOOLS.has(name) || name.includes("bash") || name.includes("shell");
}

export function isReadTool(tool: string): boolean {
  const name = lower(tool);
  return READ_TOOLS.has(name) || name.endsWith("_read");
}

export function isSearchTool(tool: string): boolean {
  return SEARCH_TOOLS.has(lower(tool)) || lower(tool).includes("grep");
}

export function isListTool(tool: string): boolean {
  return LIST_TOOLS.has(lower(tool));
}

export function isSearchOrListTool(tool: string): boolean {
  return isSearchTool(tool) || isListTool(tool);
}

export function isTodoTool(tool: string): boolean {
  return TODO_TOOLS.has(lower(tool));
}

export function isTaskTool(tool: string): boolean {
  const name = lower(tool);
  return TASK_TOOLS.has(name) || name.includes("subagent");
}

export function isQuestionTool(tool: string): boolean {
  return QUESTION_TOOLS.has(lower(tool));
}

export function isComputerTool(tool: string): boolean {
  return lower(tool) === "hoxi_computer";
}

export function isBrowserTool(tool: string): boolean {
  const name = lower(tool);
  return name === "hoxi_browser" || name.startsWith("browser_") || name.startsWith("capture_browser_") ||
    name === "click_browser_pixel" || name === "execute_browser_javascript" || name === "open_browser_url";
}

function isWebSearchTool(tool: string): boolean {
  return WEB_SEARCH_TOOLS.has(lower(tool));
}

function isWebFetchTool(tool: string): boolean {
  const name = lower(tool);
  return WEB_FETCH_TOOLS.has(name) || name.includes("webfetch") || name.includes("fetch");
}

function isWriteTool(tool: string): boolean {
  const name = lower(tool);
  return WRITE_TOOLS.has(name) || name.includes("write");
}

function isPatchTool(tool: string): boolean {
  return PATCH_TOOLS.has(lower(tool)) || lower(tool).includes("patch");
}

function isEditTool(tool: string): boolean {
  const name = lower(tool);
  return EDIT_TOOLS.has(name) || name.includes("edit") || name.includes("replace");
}

export function isEditingTool(tool: string): boolean {
  const name = lower(tool);
  if (isTodoTool(name) || isReadTool(name) || isSearchOrListTool(name)) return false;
  return isWriteTool(name) || isPatchTool(name) || isEditTool(name);
}

export function kiloToolIconName(tool: string): IconName {
  const name = lower(tool);
  if (isShellTool(name)) return "console";
  if (isEditingTool(name)) return "code-lines";
  if (isReadTool(name)) return "glasses";
  if (isListTool(name)) return "bullet-list";
  if (isSearchTool(name) || isWebSearchTool(name)) return "magnifying-glass-menu";
  if (isComputerTool(name) || isBrowserTool(name) || isWebFetchTool(name) || isWebSearchTool(name))
    return "window-cursor";
  if (isTodoTool(name)) return "checklist";
  if (isTaskTool(name)) return "task";
  if (name === "skill") return "brain";
  if (isQuestionTool(name) || name === "agent_report") return "bubble-5";
  if (name === "agent_wait" || name === "agent_status") return "task";
  return "mcp";
}

export function formatToolDisplayName(tool: string, part?: ToolPart): string {
  if (!tool) return "";
  const name = lower(tool);
  if (name === "agent_report") return t("toolCard.actionSystemMessage");
  if (name === "agent_wait" || WAIT_TOOLS.has(name)) {
    return part?.state.metadata?.waitingFor === "subagents"
      ? t("toolCard.actionWaitSubagents")
      : t("toolCard.actionWait");
  }
  if (name === "agent_status" || name === "manage_subagents") return t("toolCard.actionCheckSubagents");
  if (name === "goal_report") return t("toolCard.actionGoalReport");
  if (isQuestionTool(name)) return t("toolCard.actionQuestion");
  if (isComputerTool(name)) return t("toolCard.actionComputer");
  if (isBrowserTool(name)) return t("toolCard.actionBrowser");
  if (name === "hoxi_code_index") return t("toolCard.actionCodeIndex");
  if (name === "hoxi_tools") return t("toolCard.actionTools");
  if (name === "call_mcp_tool" || name.startsWith("mcp__")) return t("toolCard.actionMcp");
  if (name === "skill") return t("toolCard.actionSkill");
  if (isShellTool(name)) return t("toolCard.actionShell");
  if (isTodoTool(name)) return t("toolCard.actionTodo");
  if (isTaskTool(name)) return t("toolCard.actionSubagent");
  if (isReadTool(name)) return t("toolCard.actionRead");
  if (isPatchTool(name)) return t("toolCard.actionPatch");
  if (isWriteTool(name)) return t("toolCard.actionWrite");
  if (isEditTool(name)) return t("toolCard.actionEdit");
  if (name === "notebook_execution") return t("toolCard.actionNotebook");
  if (isWebSearchTool(name)) return t("toolCard.actionWebSearch");
  if (isWebFetchTool(name)) return t("toolCard.actionWebFetch");
  if (isSearchTool(name) || name.includes("search")) return t("toolCard.actionSearch");
  if (name === "find" || name === "find_by_name") return t("toolCard.actionFind");
  if (isListTool(name)) return t("toolCard.actionList");
  if (MESSAGE_TOOLS.has(name)) return t("toolCard.actionMessage");
  if (name === "command_status") return t("toolCard.actionCommandStatus");
  if (name === "manage_task") return t("toolCard.actionBackgroundTask");
  if (name === "generate_image") return t("toolCard.actionImage");
  if (RESOURCE_TOOLS.has(name)) return t("toolCard.actionResource");
  if (name === "run_workflow") return t("toolCard.actionWorkflow");
  if (name === "finish") return t("toolCard.actionFinish");
  return tool;
}

// A persisted title that is just the tool identifier (e.g. "run_command" from
// an Antigravity step without toolSummary) must not replace the localized label.
const BARE_TOOL_WORDS = new Set([
  "read", "write", "edit", "bash", "shell", "glob", "grep", "list", "ls", "task",
  "agent", "question", "skill", "webfetch", "websearch", "browser", "monitor",
]);

export function isRawToolIdentifier(text: string, tool?: string): boolean {
  const value = (text || "").trim().toLowerCase();
  if (!value) return false;
  if (tool && value === lower(tool)) return true;
  if (/^[a-z][a-z0-9]*(?:_[a-z0-9]+)+$/.test(value)) return true;
  return BARE_TOOL_WORDS.has(value);
}

export function meaningfulToolTitle(title: string | undefined, tool?: string): string {
  const value = (title || "").trim();
  if (!value || isRawToolIdentifier(value, tool)) return "";
  return value;
}
