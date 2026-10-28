// web/src/__tests__/task-overview.test.ts
import type { MessagePart, SessionInfo } from "../services/sse-types";
import { buildTaskOverview } from "../store/task-overview";
import {
  bindInlineConvoyPlan,
  buildConvoyCompletionSummaryPrompt,
  buildProposeSubtasksPrompt,
  clearBoundInlineConvoyPlan,
  getBoundInlineConvoyPlanID,
  isSubtaskPlanCodeBlockLang,
  parseSubtaskPlanBlock,
  parseSubtasksCommand,
  setWorkspace,
  type ConvoyPlan,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

function session(id: string, parentID?: string, created = 1): SessionInfo {
  return {
    id,
    title: id,
    ...(parentID ? { parentID } : {}),
    time: { created, updated: created },
  };
}

function convoyPlan(overrides: Partial<ConvoyPlan> & Pick<ConvoyPlan, "id" | "status" | "tasks">): ConvoyPlan {
  return {
    title: "Convoy",
    directory: "/repo",
    concurrency: 2,
    createdAt: 1,
    updatedAt: 2,
    ...overrides,
  };
}

describe("unified task overview", () => {
  it("maps a convoy containing the active session and nests its tasks", () => {
    setWorkspace({
      activeSessionID: "ses_task_root",
      sessions: [
        session("ses_task_root"),
        session("ses_task_child", "ses_task_root", 2),
        session("ses_task_grandchild", "ses_task_child", 3),
      ],
      messagesBySession: {},
      statusBySession: {},
      interruptedBySession: {},
    });
    const plans: ConvoyPlan[] = [
      convoyPlan({
        id: "plan_live",
        status: "running",
        tasks: [
          { id: "a", prompt: "First task", status: "succeeded", sessionID: "ses_task_root" },
          {
            id: "b",
            prompt: "Second task\nwith details",
            status: "running",
            sessionID: "ses_other",
            dependsOn: ["a"],
          },
        ],
      }),
      convoyPlan({
        id: "plan_old",
        status: "succeeded",
        tasks: [{ id: "x", prompt: "Old task", status: "succeeded", sessionID: "ses_unrelated" }],
      }),
    ];

    const groups = buildTaskOverview(plans);
    assertEqual(groups.length, 3);
    assertEqual(groups[0].nodes.length, 1);
    const planNode = groups[0].nodes[0];
    assertEqual(planNode.id, "convoy:plan_live");
    assertEqual(planNode.children.length, 2);
    assertEqual(planNode.children[1].title, "Second task");
    assertDeepEqual(planNode.children[1].dependsOn, ["a"]);

    assertEqual(groups[1].nodes.length, 1);
    assertEqual(groups[1].nodes[0].id, "subagent:ses_task_child");
    assertEqual(groups[1].nodes[0].children[0].id, "subagent:ses_task_grandchild");
    assertEqual(groups[2].nodes.length, 0);
  });

  it("maps the latest todo plan to unified task statuses", () => {
    const sessionID = "ses_task_todo";
    const todoPart = {
      id: "prt_todo",
      sessionID,
      messageID: "msg_a",
      type: "tool",
      callID: "call_todo",
      tool: "todowrite",
      state: {
        status: "completed",
        input: {
          todos: [
            { content: "Ship it", status: "in_progress" },
            { content: "Test it", status: "pending" },
            { content: "Done already", status: "completed" },
          ],
        },
        output: "",
        title: "",
        metadata: {},
        time: { start: 1, end: 2 },
      },
    } as MessagePart;
    setWorkspace({
      activeSessionID: sessionID,
      sessions: [session(sessionID)],
      messagesBySession: {
        [sessionID]: [
          { info: { id: "msg_u", sessionID, role: "user", time: { created: 1 } }, parts: [] },
          {
            info: { id: "msg_a", sessionID, role: "assistant", time: { created: 2 } },
            parts: [todoPart],
          },
        ],
      },
      statusBySession: { [sessionID]: { type: "busy" } },
      interruptedBySession: {},
    });

    const groups = buildTaskOverview([]);
    assertEqual(groups[1].nodes.length, 0);
    assertDeepEqual(
      groups[2].nodes.map((node) => [node.title, node.status]),
      [
        ["Ship it", "running"],
        ["Test it", "pending"],
        ["Done already", "succeeded"],
      ]
    );
  });

  it("reports errored subagents as failed and interrupted work consistently as cancelled", () => {
    setWorkspace({
      activeSessionID: "ses_root",
      sessions: [
        session("ses_root"),
        session("ses_err_child", "ses_root", 2),
        session("ses_int_child", "ses_root", 3),
      ],
      messagesBySession: {
        ses_err_child: [
          {
            info: {
              id: "msg_err",
              sessionID: "ses_err_child",
              role: "assistant",
              time: { created: 2 },
              error: { name: "Error", message: "boom" },
            } as never,
            parts: [],
          },
        ],
      },
      statusBySession: {},
      interruptedBySession: { ses_int_child: "stopped" },
    });

    const groups = buildTaskOverview([
      convoyPlan({ id: "plan_older", status: "running", updatedAt: 10, tasks: [] }),
      convoyPlan({ id: "plan_newer", status: "running", updatedAt: 20, tasks: [] }),
    ]);
    assertDeepEqual(
      groups[0].nodes.map((n) => n.id),
      ["convoy:plan_newer", "convoy:plan_older"]
    );
    assertDeepEqual(
      groups[1].nodes.map((n) => [n.id, n.status]),
      [
        ["subagent:ses_err_child", "failed"],
        ["subagent:ses_int_child", "cancelled"],
      ]
    );
  });

  it("keeps finished convoy plans visible for their parentSessionID and parses inline hoxi-subtasks blocks", () => {
    setWorkspace({
      activeSessionID: "ses_parent_main",
      sessions: [session("ses_parent_main")],
      messagesBySession: {},
      statusBySession: {},
      interruptedBySession: {},
    });
    const groups = buildTaskOverview([
      convoyPlan({
        id: "plan_from_parent",
        parentSessionID: "ses_parent_main",
        status: "succeeded",
        tasks: [{ id: "t1", prompt: "Subtask 1", status: "succeeded", sessionID: "ses_sub_1" }],
      }),
    ]);
    assertEqual(groups[0].nodes.length, 1);
    assertEqual(groups[0].nodes[0].id, "convoy:plan_from_parent");

    assertTrue(isSubtaskPlanCodeBlockLang("hoxi-subtasks"));
    assertTrue(isSubtaskPlanCodeBlockLang("HOXI-CONVOY"));
    assertEqual(isSubtaskPlanCodeBlockLang("json"), false);

    const parsed = parseSubtaskPlanBlock(
      JSON.stringify({
        title: "Auth Refactor",
        concurrency: 4,
        tasks: [
          { id: "Backend API", prompt: "Build endpoints", dependsOn: ["unknown", "Backend API"] },
          { id: "Frontend UI", prompt: "Build components", dependsOn: ["Backend API"] },
        ],
      })
    );
    assertDeepEqual(parsed, {
      title: "Auth Refactor",
      concurrency: 4,
      tasks: [
        { id: "backend-api", prompt: "Build endpoints" },
        { id: "frontend-ui", prompt: "Build components", dependsOn: ["backend-api"] },
      ],
    });

    const autoStartParsed = parseSubtaskPlanBlock(
      JSON.stringify({
        title: "Heartbeat Cron",
        autoStart: true,
        concurrency: 1,
        tasks: [{ id: "cron", prompt: "Add heartbeat cron" }],
      })
    );
    assertDeepEqual(autoStartParsed, {
      title: "Heartbeat Cron",
      concurrency: 1,
      tasks: [{ id: "cron", prompt: "Add heartbeat cron" }],
      autoStart: true,
    });

    // Rejects cyclic dependency graphs
    const cyclic = parseSubtaskPlanBlock(
      JSON.stringify({
        tasks: [
          { id: "a", prompt: "step a", dependsOn: ["b"] },
          { id: "b", prompt: "step b", dependsOn: ["a"] },
        ],
      })
    );
    assertEqual(cyclic, null);

    // Parses /subtasks, /convoy, and /subtask slash commands
    assertDeepEqual(parseSubtasksCommand("/subtasks split auth and ui"), {
      kind: "propose",
      goal: "split auth and ui",
    });
    assertDeepEqual(parseSubtasksCommand("/convoy"), { kind: "propose", goal: "" });
    assertDeepEqual(parseSubtasksCommand("/subtask fix flaky test"), {
      kind: "spawn",
      prompt: "fix flaky test",
    });
    assertDeepEqual(parseSubtasksCommand("/subtask"), { kind: "usage" });
    assertEqual(parseSubtasksCommand("normal prompt"), null);

    assertTrue(buildProposeSubtasksPrompt("Build X").includes("```hoxi-subtasks"));
    const summaryPrompt = buildConvoyCompletionSummaryPrompt(
      convoyPlan({
        id: "plan_done",
        title: "Parallel Run",
        status: "succeeded",
        mergedAt: 100,
        tasks: [{ id: "api", prompt: "p", status: "succeeded", summary: "Added routes" }],
      })
    );
    assertTrue(summaryPrompt.includes("Added routes"));

    bindInlineConvoyPlan("card_key_1", "plan_123");
    assertEqual(getBoundInlineConvoyPlanID("card_key_1"), "plan_123");
    clearBoundInlineConvoyPlan("card_key_1");
    assertEqual(getBoundInlineConvoyPlanID("card_key_1"), undefined);
  });
});
