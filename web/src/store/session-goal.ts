// web/src/store/session-goal.ts
// Goal loop driver and the public prompt entry point. `/goal ...` composer
// commands are routed here; every other prompt goes straight to
// dispatchSessionPrompt. Goal state itself lives in session-goal-state.
import { t } from "../i18n";
import {
  buildProposeSubtasksPrompt,
  parseSubtasksCommand,
  spawnBackgroundSubtask,
} from "./orchestration";
import { isSessionBusy } from "./session-query";
import { dispatchSessionPrompt } from "./workspace-session-messages";
import { loadSessionMessagesOnly } from "./workspace-sessions";
import { workspace } from "./workspace-state";
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
// the hoxi-goal OpenCode plugin is loaded, so it is offered but never required.
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
  if ((workspace.queuedPromptsBySession[sessionID] ?? []).length > 0) return;

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

/**
 * Sends a prompt from the composer or any other caller: goal commands are handled
 * locally, everything else is dispatched (or queued while the session is busy).
 */
export async function submitSessionPrompt(
  promptText: string,
  targetSessionID?: string | null,
  options?: Parameters<typeof dispatchSessionPrompt>[2]
): Promise<string> {
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
