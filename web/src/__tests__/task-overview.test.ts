// web/src/__tests__/task-overview.test.ts
import type { MessagePart, SessionInfo } from "../services/sse-types";
import { buildTaskOverview } from "../store/task-overview";
import { setWorkspace, type ConvoyPlan } from "../store/workspace";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

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
});
