// web/src/store/session-goal-state.ts
// Session goals keep one objective in focus across turns. This module owns the
// goal records, `/goal` parsing and the turn-evidence decision; the loop that
// sends prompts lives in session-goal.ts.
import { createStore, produce, unwrap } from "solid-js/store";
import type { MessagePart } from "../services/sse-types";
import type { MessageTurn } from "./workspace-state";

export const GOAL_MAX_OBJECTIVE_LENGTH = 10000;
/** Automatic continuations allowed before the goal pauses for review (cost guard). */
export const GOAL_MAX_CONTINUATIONS = 20;
export const GOAL_STORAGE_KEY = "hoxi.session-goals:v1";
export const GOAL_REPORT_TOOL = "goal_report";

export type SessionGoalStatus = "active" | "paused" | "blocked" | "complete";
export type GoalPauseReason = "stopped" | "error" | "no-progress" | "budget" | "manual" | "restart";

export interface SessionGoal {
  objective: string;
  status: SessionGoalStatus;
  reason?: string;
  continuations: number;
  updatedAt: number;
}

export type GoalCommand =
  | { kind: "status" }
  | { kind: "pause" }
  | { kind: "resume" }
  | { kind: "clear" }
  | { kind: "set"; objective: string }
  | { kind: "invalid"; error: "objective-too-long" };

/** Parses `/goal ...`; returns null for any text that is not a goal command. */
export function parseGoalCommand(text: string): GoalCommand | null {
  const trimmed = text.trim();
  if (!/^\/goal(\s|$)/i.test(trimmed)) return null;
  const rest = trimmed.slice("/goal".length).trim();
  if (!rest) return { kind: "status" };
  const word = rest.toLowerCase();
  if (word === "pause") return { kind: "pause" };
  if (word === "resume") return { kind: "resume" };
  if (word === "clear") return { kind: "clear" };
  if (rest.length > GOAL_MAX_OBJECTIVE_LENGTH) return { kind: "invalid", error: "objective-too-long" };
  return { kind: "set", objective: rest };
}

export interface GoalTurnEvidence {
  interrupted: boolean;
  errored: boolean;
  /** Completed tool calls in the last assistant turn, excluding goal_report. */
  completedTools: number;
  report?: { status: "complete" | "blocked"; reason: string };
}

export type GoalDecision =
  | { kind: "continue" }
  | { kind: "complete"; reason: string }
  | { kind: "blocked"; reason: string }
  | { kind: "pause"; reason: GoalPauseReason };

/** Pure transition rule applied once per idle turn. */
export function decideGoalTurn(evidence: GoalTurnEvidence, continuations: number): GoalDecision {
  if (evidence.interrupted) return { kind: "pause", reason: "stopped" };
  if (evidence.errored) return { kind: "pause", reason: "error" };
  if (evidence.report?.status === "complete") return { kind: "complete", reason: evidence.report.reason };
  if (evidence.report?.status === "blocked") return { kind: "blocked", reason: evidence.report.reason };
  if (evidence.completedTools === 0) return { kind: "pause", reason: "no-progress" };
  if (continuations >= GOAL_MAX_CONTINUATIONS) return { kind: "pause", reason: "budget" };
  return { kind: "continue" };
}

/** Reads the most recent assistant turn; goal_report is only trusted once it completed. */
export function collectTurnEvidence(turns: readonly MessageTurn[], interrupted: boolean): GoalTurnEvidence {
  let last: MessageTurn | undefined;
  for (let i = turns.length - 1; i >= 0; i--) {
    if (turns[i].info.role === "assistant") {
      last = turns[i];
      break;
    }
  }
  if (!last) return { interrupted, errored: false, completedTools: 0 };

  let completedTools = 0;
  let report: GoalTurnEvidence["report"];
  for (const part of last.parts as MessagePart[]) {
    if (part.type !== "tool" || part.state.status !== "completed") continue;
    if (part.tool !== GOAL_REPORT_TOOL) {
      completedTools++;
      continue;
    }
    const status = part.state.input?.status;
    const reason = part.state.input?.reason;
    if (status === "complete" || status === "blocked") {
      report = { status, reason: typeof reason === "string" ? reason : "" };
    }
  }
  return { interrupted, errored: Boolean(last.info.error), completedTools, report };
}

/** Restores persisted goals. An active goal cannot survive a restart, so it comes back paused. */
export function restoreStoredGoals(raw: unknown): Record<string, SessionGoal> {
  const out: Record<string, SessionGoal> = {};
  if (!raw || typeof raw !== "object") return out;
  for (const [sessionID, value] of Object.entries(raw as Record<string, unknown>)) {
    if (!value || typeof value !== "object") continue;
    const goal = value as Partial<SessionGoal>;
    if (typeof goal.objective !== "string" || !goal.objective.trim()) continue;
    const wasActive = goal.status === "active";
    const known = goal.status === "paused" || goal.status === "blocked" || goal.status === "complete";
    out[sessionID] = {
      objective: goal.objective.slice(0, GOAL_MAX_OBJECTIVE_LENGTH),
      status: known ? goal.status! : "paused",
      reason: wasActive ? "restart" : typeof goal.reason === "string" ? goal.reason : undefined,
      continuations: typeof goal.continuations === "number" && goal.continuations >= 0 ? goal.continuations : 0,
      updatedAt: typeof goal.updatedAt === "number" ? goal.updatedAt : Date.now(),
    };
  }
  return out;
}

function loadGoals(): Record<string, SessionGoal> {
  try {
    const raw = typeof localStorage === "undefined" ? null : localStorage.getItem(GOAL_STORAGE_KEY);
    return restoreStoredGoals(raw ? JSON.parse(raw) : null);
  } catch {
    return {};
  }
}

function persistGoals(): void {
  try {
    if (typeof localStorage !== "undefined") {
      localStorage.setItem(GOAL_STORAGE_KEY, JSON.stringify(unwrap(goals)));
    }
  } catch {
    /* storage can be full or disabled; the goal still works for this run */
  }
}

const [goals, setGoals] = createStore<Record<string, SessionGoal>>(loadGoals());

export function sessionGoal(sessionID: string | null | undefined): SessionGoal | undefined {
  return sessionID ? goals[sessionID] : undefined;
}

export function writeSessionGoal(sessionID: string, goal: SessionGoal | null): void {
  setGoals(
    produce((state) => {
      if (goal) state[sessionID] = goal;
      else delete state[sessionID];
    })
  );
  persistGoals();
}

export function pauseSessionGoal(sessionID: string, reason: GoalPauseReason = "manual"): void {
  const goal = goals[sessionID];
  if (!goal || goal.status !== "active") return;
  writeSessionGoal(sessionID, { ...goal, status: "paused", reason, updatedAt: Date.now() });
}

export function clearSessionGoal(sessionID: string): void {
  if (goals[sessionID]) writeSessionGoal(sessionID, null);
}

