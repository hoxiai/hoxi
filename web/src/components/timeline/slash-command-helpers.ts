// web/src/components/timeline/slash-command-helpers.ts
import { t } from "../../i18n";
import type { CommandInfo } from "../../services/api";

export interface SlashTokenRange {
  start: number;
  end: number;
}

export interface ActiveSlashCommandInfo {
  name: string;
  rawToken: string;
  description?: string;
  hints?: string[];
  hintSlots?: string[];
  activeArgIndex: number;
  agent?: string;
  model?: string;
  subtask?: boolean;
  hasArgs: boolean;
}

export function splitCommandHintSlots(hints?: string[]): string[] {
  if (!hints || hints.length === 0) return [];
  const out: string[] = [];
  for (const h of hints) {
    const matches = h.match(/<[^>]+>|\[[^\]]+\]|\{[^}]+\}|\S+/g);
    if (matches) {
      for (const m of matches) {
        if (m.trim()) out.push(m.trim());
      }
    }
  }
  return out;
}

/**
 * Counts how many positional arguments after `/cmd` have been completed
 * (terminated by unquoted whitespace). Matches Go's `splitCommandArgs` rules
 * so the returned count equals the 0-based index of the slot currently being typed.
 */
export function countCompletedCommandArgs(afterCmd: string): number {
  let completed = 0;
  let quote = "";
  let escaped = false;
  let inToken = false;
  for (let i = 0; i < afterCmd.length; i++) {
    const ch = afterCmd[i];
    if (escaped) {
      escaped = false;
      inToken = true;
      continue;
    }
    if (ch === "\\") {
      escaped = true;
      inToken = true;
      continue;
    }
    if (quote) {
      if (ch === quote) quote = "";
      inToken = true;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      inToken = true;
      continue;
    }
    if (ch === " " || ch === "\t" || ch === "\n" || ch === "\r") {
      if (inToken) {
        completed++;
        inToken = false;
      }
      continue;
    }
    inToken = true;
  }
  return completed;
}

/**
 * Applies a slash command suggestion (`item.type === "command"`, non-skill).
 * Backend `ExpandSlashCommand` only expands prompts that start with `/`, so if
 * the user triggered `/cmd` mid-sentence, the command is hoisted to index 0
 * while preserving surrounding text as command arguments.
 */
export function applySlashCommandToken(
  val: string,
  token: SlashTokenRange,
  insertText: string
): { text: string; cursor: number } {
  const before = val.slice(0, token.start);
  const after = val.slice(token.end);
  if (before.trim().length === 0) {
    const normalizedInsert =
      after.startsWith(" ") && insertText.endsWith(" ")
        ? insertText.slice(0, -1)
        : insertText;
    const nextText = `${normalizedInsert}${after}`;
    return {
      text: nextText,
      cursor: insertText.length,
    };
  }
  const lead = before.trim();
  const tail = after.trim();
  const rest = lead && tail ? `${lead} ${tail}` : lead || tail;
  const cmdPrefix = insertText.endsWith(" ") ? insertText : `${insertText} `;
  const nextText = rest ? `${cmdPrefix}${rest}` : cmdPrefix;
  return {
    text: nextText,
    cursor: cmdPrefix.length + lead.length,
  };
}

export function inspectActiveSlashCommand(
  val: string,
  commands: CommandInfo[]
): ActiveSlashCommandInfo | null {
  const trimmed = val.trimStart();
  if (!trimmed.startsWith("/") || trimmed.length < 2) return null;
  const rest = trimmed.slice(1);
  const wsIdx = rest.search(/\s/);
  const cmdToken = wsIdx >= 0 ? rest.slice(0, wsIdx) : rest;
  const afterCmd = wsIdx >= 0 ? rest.slice(wsIdx) : "";
  const rawArgs = afterCmd.trim();
  if (!cmdToken) return null;

  const cmdLower = cmdToken.toLowerCase();
  const altColon = cmdLower.replace(/\//g, ":");
  for (const c of commands) {
    if (c.source === "skill") continue;
    const candidate = c.name.toLowerCase();
    if (candidate === cmdLower || candidate === altColon) {
      const hints = c.hints?.length ? c.hints : undefined;
      const hintSlots = splitCommandHintSlots(hints);
      return {
        name: c.name,
        rawToken: `/${cmdToken}`,
        description: c.description || undefined,
        hints,
        hintSlots: hintSlots.length > 0 ? hintSlots : undefined,
        activeArgIndex: countCompletedCommandArgs(afterCmd),
        agent: c.agent || undefined,
        model: c.model || undefined,
        subtask: c.subtask || undefined,
        hasArgs: rawArgs.length > 0,
      };
    }
  }
  return null;
}

export function removeLeadingSlashCommand(val: string): string {
  return val.replace(/^\s*\/[^\s]+\s*/, "");
}

export function getBuiltinFrontendCommands(): CommandInfo[] {
  return [
    { name: "new", description: t("commandPalette.slashNewDesc"), source: "command" },
    { name: "plan", description: t("commandPalette.slashPlanDesc"), source: "command", hints: ["[prompt]"], agent: "plan" },
    { name: "build", description: t("commandPalette.slashBuildDesc"), source: "command", hints: ["[prompt]"], agent: "build" },
    { name: "fast", description: t("commandPalette.slashFastDesc"), source: "command", hints: ["[on|off]"] },
    { name: "diff", description: t("commandPalette.slashDiffDesc"), source: "command" },
    { name: "undo", description: t("commandPalette.slashUndoDesc"), source: "command" },
    { name: "goal", description: t("timeline.goalSlashDescription"), source: "command", hints: ["<objective>"] },
    { name: "subtasks", description: t("timeline.subtasksSlashDescription"), source: "command", hints: ["[goal]"] },
    { name: "subtask", description: t("timeline.subtaskSpawnSlashDescription"), source: "command", hints: ["<prompt>"] },
  ];
}

export function mergeSlashCommands(serverCmds?: CommandInfo[] | null): CommandInfo[] {
  // Custom / server-loaded commands take precedence if a workspace explicitly defines one with the same name.
  const merged: CommandInfo[] = [];
  const seen = new Set<string>();
  for (const c of serverCmds ?? []) {
    const key = c.name.toLowerCase();
    if (!seen.has(key)) {
      seen.add(key);
      merged.push(c);
    }
  }
  for (const c of getBuiltinFrontendCommands()) {
    const key = c.name.toLowerCase();
    if (!seen.has(key)) {
      seen.add(key);
      merged.push(c);
    }
  }
  return merged;
}
