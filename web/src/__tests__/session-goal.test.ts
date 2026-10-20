import type { MessageTurn } from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";
import {
  collectTurnEvidence,
  decideGoalTurn,
  GOAL_MAX_CONTINUATIONS,
  GOAL_MAX_OBJECTIVE_LENGTH,
  parseGoalCommand,
  restoreStoredGoals,
} from "../store/session-goal-state";

function assistantTurn(parts: MessageTurn["parts"], error?: unknown): MessageTurn {
  return {
    info: { id: "msg", sessionID: "ses", role: "assistant", time: { created: 0, completed: 1 }, error } as never,
    parts,
  };
}

function userTurn(): MessageTurn {
  return { info: { id: "usr", sessionID: "ses", role: "user", time: { created: 0 } } as never, parts: [] };
}

function toolPart(tool: string, status: "completed" | "pending", input: Record<string, unknown>): MessageTurn["parts"][number] {
  const state = status === "completed"
    ? { status, input, output: "ok", title: tool, metadata: {}, time: { start: 0, end: 1 } }
    : { status, input, raw: "", title: tool };
  return { id: `prt_${tool}_${status}`, sessionID: "ses", messageID: "msg", type: "tool", callID: "c", tool, state } as never;
}

describe("session goal command parsing", () => {
  it("recognises the goal commands and leaves ordinary text alone", () => {
    assertDeepEqual(parseGoalCommand("/goal"), { kind: "status" });
    assertDeepEqual(parseGoalCommand("  /goal  Fix the login flow "), { kind: "set", objective: "Fix the login flow" });
    assertDeepEqual(parseGoalCommand("/GOAL pause"), { kind: "pause" });
    assertDeepEqual(parseGoalCommand("/goal Resume"), { kind: "resume" });
    assertDeepEqual(parseGoalCommand("/goal clear"), { kind: "clear" });
    assertEqual(parseGoalCommand("/goalie plan"), null, "prefix of another word is not a goal");
    assertEqual(parseGoalCommand("/goal:command x"), null, "custom /goal:command must not be captured");
    assertEqual(parseGoalCommand("please /goal fix"), null, "only a leading command counts");
  });

  it("rejects objectives over the length limit", () => {
    const long = "x".repeat(GOAL_MAX_OBJECTIVE_LENGTH + 1);
    assertDeepEqual(parseGoalCommand(`/goal ${long}`), { kind: "invalid", error: "objective-too-long" });
  });
});

describe("session goal turn decisions", () => {
  const working = { interrupted: false, errored: false, completedTools: 2 };

  it("continues while progress is being made", () => {
    assertDeepEqual(decideGoalTurn(working, 0), { kind: "continue" });
  });

  it("pauses when the user stopped the turn or it errored", () => {
    assertDeepEqual(decideGoalTurn({ ...working, interrupted: true }, 0), { kind: "pause", reason: "stopped" });
    assertDeepEqual(decideGoalTurn({ ...working, errored: true }, 0), { kind: "pause", reason: "error" });
  });

  it("records the agent's verdict from goal_report", () => {
    assertDeepEqual(decideGoalTurn({ ...working, report: { status: "complete", reason: "tests pass" } }, 3),
      { kind: "complete", reason: "tests pass" });
    assertDeepEqual(decideGoalTurn({ ...working, report: { status: "blocked", reason: "needs API key" } }, 3),
      { kind: "blocked", reason: "needs API key" });
  });

  it("pauses a turn with no successful action and no report", () => {
    assertDeepEqual(decideGoalTurn({ ...working, completedTools: 0 }, 0), { kind: "pause", reason: "no-progress" });
  });

  it("pauses at the continuation budget so cost cannot run away", () => {
    assertDeepEqual(decideGoalTurn(working, GOAL_MAX_CONTINUATIONS - 1), { kind: "continue" });
    assertDeepEqual(decideGoalTurn(working, GOAL_MAX_CONTINUATIONS), { kind: "pause", reason: "budget" });
  });
});

describe("session goal turn evidence", () => {
  it("counts completed tools and trusts only a completed goal_report from the latest assistant turn", () => {
    const turns = [
      userTurn(),
      assistantTurn([toolPart("goal_report", "completed", { status: "blocked", reason: "old" })]),
      userTurn(),
      assistantTurn([
        toolPart("bash", "completed", { command: "go test ./..." }),
        toolPart("edit", "completed", {}),
        toolPart("goal_report", "pending", { status: "complete", reason: "not yet" }),
      ]),
    ];
    const evidence = collectTurnEvidence(turns, false);
    assertEqual(evidence.completedTools, 2);
    assertEqual(evidence.report, undefined, "a pending goal_report must not count as a verdict");
    assertEqual(evidence.errored, false);
  });

  it("reads a completed goal_report and flags assistant errors", () => {
    const turns = [userTurn(), assistantTurn([toolPart("goal_report", "completed", { status: "complete", reason: "verified" })], { name: "APIError" })];
    const evidence = collectTurnEvidence(turns, true);
    assertDeepEqual(evidence.report, { status: "complete", reason: "verified" });
    assertTrue(evidence.errored, "an assistant error must be visible to the decision");
    assertTrue(evidence.interrupted);
  });

  it("returns an empty turn when there is no assistant message yet", () => {
    assertDeepEqual(collectTurnEvidence([userTurn()], false), { interrupted: false, errored: false, completedTools: 0 });
  });
});

describe("session goal persistence", () => {
  it("restores an active goal as paused after a restart", () => {
    const restored = restoreStoredGoals({
      ses_a: { objective: "Ship it", status: "active", continuations: 4, updatedAt: 10 },
      ses_b: { objective: "Done", status: "complete", continuations: 1, updatedAt: 11 },
    });
    assertEqual(restored.ses_a.status, "paused");
    assertEqual(restored.ses_a.reason, "restart");
    assertEqual(restored.ses_a.continuations, 4);
    assertEqual(restored.ses_b.status, "complete", "complete goals stay complete");
  });

  it("drops malformed entries and clamps overlong objectives", () => {
    const restored = restoreStoredGoals({
      ses_bad: "not-an-object",
      ses_empty: { objective: "   ", status: "paused" },
      ses_long: { objective: "y".repeat(GOAL_MAX_OBJECTIVE_LENGTH + 50), status: "weird", continuations: -3 },
    });
    assertEqual(Object.keys(restored).join(","), "ses_long");
    assertEqual(restored.ses_long.objective.length, GOAL_MAX_OBJECTIVE_LENGTH);
    assertEqual(restored.ses_long.status, "paused");
    assertEqual(restored.ses_long.continuations, 0);
  });

  it("returns nothing for unusable storage", () => {
    assertDeepEqual(restoreStoredGoals(null), {});
    assertDeepEqual(restoreStoredGoals("text"), {});
  });
});
