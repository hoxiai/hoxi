// web/src/store/session-goal.ts
// Goal loop driver and the public prompt entry point. `/goal ...` composer
// commands are routed here; every other prompt goes straight to
// dispatchSessionPrompt. Goal state itself lives in session-goal-state.
import { t } from "../i18n";
import { hoxiAPI } from "../services/api";
import {
  buildProposeSubtasksPrompt,
  parseSubtasksCommand,
  spawnBackgroundSubtask,
} from "./orchestration";
import { isSessionBusy, sessionDirectory } from "./session-query";
import { hasEditingQueuedPrompt } from "./prompt-queue";
import { setActiveAgent, setFastMode, toggleFastMode } from "./session-ai";
import { canRevertSession } from "./session-capabilities";
import { revertToHere } from "./session-revert";
import { startNewThreadDraft } from "./session-sandbox";
import { dispatchSessionPrompt } from "./workspace-session-messages";
import { loadSessionMessagesOnly } from "./workspace-sessions";
import { setWorkspace, workspace } from "./workspace-state";
import {
  clearSessionGoal,
  collectTurnEvidence,
  decideGoalTurn,
  GOAL_MAX_OBJECTIVE_LENGTH,
  type GoalCommand,
  parseGoalCommand,
  pauseSessionGoal,
  sessionGoal,
  writeSessionGoal,
} from "./session-goal-state";

const advancing = new Set<string>();

// Every runtime understands a plain-text verdict; goal_report only exists where
// Hoxi's built-in goal_report tool is registered, so it is offered but never required.
const VERDICT_INSTRUCTIONS = [
  "When the objective is fully met and verified, end your final reply with this exact last line:",
  "[[goal:complete]] <one sentence on what you verified>",
  "If you are genuinely blocked and cannot proceed safely, end your final reply with this exact last line instead:",
  "[[goal:blocked]] <the concrete blocker>",
  "Otherwise do not write either line. If a goal_report tool is available you may call it instead.",
];

function startPrompt(objective: string): string {
  return [
    `Session goal: ${objective}`,
    "",
    "Work toward this objective across turns. Make concrete progress each turn and verify it.",
    ...VERDICT_INSTRUCTIONS,
  ].join("\n");
}

function continuePrompt(objective: string): string {
  return [
    `Continue working toward the session goal: ${objective}`,
    "Take the next concrete step.",
    "End your final reply with `[[goal:complete]] <what you verified>` once the objective is met and verified, or `[[goal:blocked]] <blocker>` if you cannot proceed safely; otherwise write neither (or call goal_report if available).",
  ].join("\n");
}

/** Handles `/goal` from the composer. Called before any message is sent. */
export async function runGoalCommand(command: GoalCommand, sessionID: string | null): Promise<void> {
  if (command.kind === "invalid") {
    throw new Error(t("timeline.goalObjectiveTooLong", { max: GOAL_MAX_OBJECTIVE_LENGTH }));
  }
  if (command.kind === "status") {
    if (!sessionID || !sessionGoal(sessionID)) throw new Error(t("timeline.goalCommandUsage"));
    return;
  }
  if (!sessionID) throw new Error(t("timeline.goalRequiresSession"));
  if (command.kind === "pause") return pauseSessionGoal(sessionID, "manual");
  if (command.kind === "clear") return clearSessionGoal(sessionID);
  if (command.kind === "resume") return resumeSessionGoal(sessionID);
  await startSessionGoal(sessionID, command.objective);
}

export async function startSessionGoal(sessionID: string, objective: string): Promise<void> {
  writeSessionGoal(sessionID, { objective, status: "active", continuations: 0, updatedAt: Date.now() });
  await dispatchSessionPrompt(startPrompt(objective), sessionID);
}

export async function resumeSessionGoal(sessionID: string): Promise<void> {
  const goal = sessionGoal(sessionID);
  if (!goal || goal.status === "active") return;
  writeSessionGoal(sessionID, {
    ...goal,
    status: "active",
    reason: undefined,
    continuations: 0,
    updatedAt: Date.now(),
  });
  await dispatchSessionPrompt(continuePrompt(goal.objective), sessionID);
}

/**
 * Called when a session turns idle. Evaluates the finished turn and either sends
 * the next continuation, pauses, or records complete/blocked. It never runs while
 * queued prompts are waiting, and it reloads the transcript so it sees the new turn.
 */
export async function advanceSessionGoal(sessionID: string): Promise<void> {
  const goal = sessionGoal(sessionID);
  if (!goal || goal.status !== "active" || advancing.has(sessionID)) return;
  if ((workspace.queuedPromptsBySession[sessionID] ?? []).length > 0 || hasEditingQueuedPrompt(sessionID)) return;

  advancing.add(sessionID);
  try {
    await loadSessionMessagesOnly(sessionID);
    const current = sessionGoal(sessionID);
    if (!current || current.status !== "active" || isSessionBusy(sessionID)) return;

    const evidence = collectTurnEvidence(
      workspace.messagesBySession[sessionID] ?? [],
      Boolean(workspace.interruptedBySession[sessionID])
    );
    const decision = decideGoalTurn(evidence, current.continuations);
    const now = Date.now();
    if (decision.kind === "continue") {
      writeSessionGoal(sessionID, { ...current, continuations: current.continuations + 1, updatedAt: now });
      await dispatchSessionPrompt(continuePrompt(current.objective), sessionID);
    } else if (decision.kind === "pause") {
      writeSessionGoal(sessionID, { ...current, status: "paused", reason: decision.reason, updatedAt: now });
    } else {
      writeSessionGoal(sessionID, {
        ...current,
        status: decision.kind === "complete" ? "complete" : "blocked",
        reason: decision.reason || undefined,
        updatedAt: now,
      });
    }
  } catch (err) {
    console.warn("advanceSessionGoal failed:", err);
  } finally {
    advancing.delete(sessionID);
  }
}

export type ComposerControlCommand =
  | { kind: "new" }
  | { kind: "diff" }
  | { kind: "undo" }
  | { kind: "mode"; mode: "plan" | "build"; prompt: string }
  | { kind: "fast"; action: "toggle" | "on" | "off"; prompt: string };

const MAX_CUSTOM_CMD_DIRS = 24;
const customCommandsByDir = new Map<string, Set<string>>();

export function recordWorkspaceCommandNames(dir: string | undefined, names: string[]): void {
  const key = dir || "__root__";
  if (customCommandsByDir.has(key)) customCommandsByDir.delete(key);
  while (customCommandsByDir.size >= MAX_CUSTOM_CMD_DIRS) {
    const oldest = customCommandsByDir.keys().next().value;
    if (oldest === undefined) break;
    customCommandsByDir.delete(oldest);
  }
  customCommandsByDir.set(key, new Set(names.map((n) => n.toLowerCase())));
}

function hasCustomWorkspaceCommand(dir: string | undefined, name: string): boolean {
  const set = customCommandsByDir.get(dir || "__root__");
  return Boolean(set?.has(name.toLowerCase()));
}

const REVIEW_HEADER_PREFIX = "Please address the following inline code review comments:\n";

export function parseComposerControlCommand(
  text: string,
  directory?: string
): ComposerControlCommand | null {
  const trimmed = text.trim();
  if (trimmed.startsWith(REVIEW_HEADER_PREFIX)) {
    const splitIdx = trimmed.indexOf("\n\n/");
    if (splitIdx > 0) {
      const prefix = trimmed.slice(0, splitIdx);
      const tail = trimmed.slice(splitIdx + 2).trim();
      const modeMatch = tail.match(/^\/(plan|build)(?:\s+([\s\S]*))?$/i);
      if (modeMatch) {
        const mode = modeMatch[1].toLowerCase() as "plan" | "build";
        if (!hasCustomWorkspaceCommand(directory, mode)) {
          const rest = (modeMatch[2] ?? "").trim();
          return {
            kind: "mode",
            mode,
            prompt: rest ? `${prefix}\n\n${rest}` : prefix,
          };
        }
      }
    }
    return null;
  }
  if (!trimmed.startsWith("/")) return null;
  const cmdName = trimmed.slice(1).split(/\s+/)[0]?.toLowerCase() || "";
  if (hasCustomWorkspaceCommand(directory, cmdName)) return null;

  if (/^\/(?:new|clear)$/i.test(trimmed)) return { kind: "new" };
  if (/^\/diff$/i.test(trimmed)) return { kind: "diff" };
  if (/^\/undo$/i.test(trimmed)) return { kind: "undo" };
  const fastMatch = trimmed.match(/^\/fast(?:\s+([\s\S]*))?$/i);
  if (fastMatch) {
    const rest = (fastMatch[1] ?? "").trim();
    if (!rest) return { kind: "fast", action: "toggle", prompt: "" };
    if (/^on$/i.test(rest)) return { kind: "fast", action: "on", prompt: "" };
    if (/^off$/i.test(rest)) return { kind: "fast", action: "off", prompt: "" };
    return { kind: "fast", action: "on", prompt: rest };
  }
  const modeMatch = trimmed.match(/^\/(plan|build)(?:\s+([\s\S]*))?$/i);
  if (modeMatch) {
    return {
      kind: "mode",
      mode: modeMatch[1].toLowerCase() as "plan" | "build",
      prompt: (modeMatch[2] ?? "").trim(),
    };
  }
  return null;
}

export function isPureLocalSlashCommand(text: string, directory?: string): boolean {
  const cmd = parseComposerControlCommand(text, directory);
  if (!cmd) return false;
  if (cmd.kind === "mode" || cmd.kind === "fast") return !cmd.prompt;
  return true;
}

/**
 * Sends a prompt from the composer or any other caller: goal and local control
 * commands are handled locally, everything else is dispatched (or queued while busy).
 */
export async function submitSessionPrompt(
  promptText: string,
  targetSessionID?: string | null,
  options?: Parameters<typeof dispatchSessionPrompt>[2]
): Promise<string> {
  const sid = targetSessionID ?? workspace.activeSessionID;
  const effectiveDir = options?.directory || sessionDirectory(sid) || workspace.rootDir || undefined;
  const controlCommand = parseComposerControlCommand(promptText, effectiveDir);
  if (controlCommand) {
    switch (controlCommand.kind) {
      case "new":
        startNewThreadDraft();
        return "";
      case "diff":
        setWorkspace({ inspectorOpen: true, inspectorTab: "diffs" });
        return "";
      case "undo": {
        if (sid && canRevertSession(sid)) {
          const turns = workspace.messagesBySession[sid] ?? [];
          for (let i = turns.length - 1; i >= 0; i--) {
            if (turns[i].info.role === "user") {
              await revertToHere(sid, turns[i].info.id);
              break;
            }
          }
        }
        return "";
      }
      case "mode":
        setActiveAgent(controlCommand.mode, sid);
        if (!controlCommand.prompt) return "";
        return dispatchSessionPrompt(controlCommand.prompt, targetSessionID, options);
      case "fast":
        if (controlCommand.action === "toggle") toggleFastMode(sid);
        else setFastMode(controlCommand.action === "on", sid);
        if (!controlCommand.prompt) return "";
        return dispatchSessionPrompt(controlCommand.prompt, targetSessionID, options);
    }
  }
  if (promptText.trim().toLowerCase() === "/compact") {
    const sid = targetSessionID ?? workspace.activeSessionID;
    const sel = workspace.selectedModel;
    if (sid && sel?.providerID && sel?.modelID && !isSessionBusy(sid)) {
      const compacted = await hoxiAPI.summarizeSession(
        sid,
        sel.providerID,
        sel.modelID,
        options?.directory || sessionDirectory(sid)
      );
      if (compacted) {
        await loadSessionMessagesOnly(sid);
      }
      return "";
    }
  }
  const goalCommand = parseGoalCommand(promptText);
  if (goalCommand) {
    await runGoalCommand(goalCommand, targetSessionID ?? workspace.activeSessionID);
    return "";
  }
  const subtasksCommand = parseSubtasksCommand(promptText);
  if (subtasksCommand) {
    if (subtasksCommand.kind === "usage") {
      throw new Error(t("timeline.subtasksCommandUsage"));
    }
    if (subtasksCommand.kind === "spawn") {
      await spawnBackgroundSubtask(
        subtasksCommand.prompt,
        targetSessionID ?? workspace.activeSessionID,
        options?.directory
      );
      return "";
    }
    return dispatchSessionPrompt(
      buildProposeSubtasksPrompt(subtasksCommand.goal),
      targetSessionID,
      options
    );
  }
  return dispatchSessionPrompt(promptText, targetSessionID, options);
}
