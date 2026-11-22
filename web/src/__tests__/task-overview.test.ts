// web/src/__tests__/task-overview.test.ts
import type { MessagePart, SessionInfo } from "../services/sse-types";
import {
  buildTaskOverview,
  dismissTaskOverviewNode,
  forgetSessionTaskOverview,
  isTaskNodeExpanded,
  restoreTaskOverviewNode,
  toggleTaskNodeExpanded,
} from "../store/task-overview";
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
  workspace,
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
  it("removes only the selected finished convoy attempt, keeping dependencies and retry results", () => {
    const root = "ses_dismiss_convoy";
    setWorkspace({ activeSessionID: root, sessions: [session(root)], statusBySession: {} });
    const plan = convoyPlan({
      id: "plan_dismiss",
      parentSessionID: root,
      status: "running",
      tasks: [
        { id: "a", prompt: "First task", status: "failed", endedAt: 2 },
        { id: "b", prompt: "Second task", status: "running", dependsOn: ["a"] },
      ],
    });
    const plans = () => buildTaskOverview([plan])[0].nodes;
    const [parent] = plans();
    assertEqual(dismissTaskOverviewNode(parent), undefined);
    assertEqual(dismissTaskOverviewNode(parent.children[1]), undefined);
    const key = dismissTaskOverviewNode(parent.children[0])!;
    assertTrue(Boolean(key));
    try {
      assertDeepEqual(plans()[0].children.map((child) => child.detail), ["b"]);
      assertDeepEqual(plans()[0].children[0].dependsOn, ["a"]);
      assertEqual(plan.tasks.length, 2);

      const withOnlyFirstTask = () => buildTaskOverview([{ ...plan, status: "failed", tasks: [plan.tasks[0]] }])[0].nodes;
      assertEqual(withOnlyFirstTask().length, 0, "an empty finished plan is hidden as well");
      plan.tasks[0].status = "queued";
      assertEqual(plans()[0].children.length, 2, "queued retries must be visible");
      plan.tasks[0].status = "failed";
      plan.tasks[0].endedAt = 3;
      assertEqual(plans()[0].children.length, 2, "a new attempt gets a new result row");
    } finally {
      restoreTaskOverviewNode(key);
    }
  });

  it("restores a dismissed child session and never hides its descendants or running work", () => {
    const root = "ses_dismiss_root";
    const child = "ses_dismiss_child";
    const grandchild = "ses_dismiss_grandchild";
    setWorkspace({
      activeSessionID: root,
      sessions: [session(root), session(child, root), session(grandchild, child)],
      messagesBySession: {},
      statusBySession: { [grandchild]: { type: "busy" } },
      interruptedBySession: {},
    });
    const nodes = () => buildTaskOverview([])[1].nodes;
    const [childNode] = nodes();
    const key = dismissTaskOverviewNode(childNode)!;
    assertTrue(Boolean(key));
    try {
      assertDeepEqual(nodes().map((node) => node.sessionID), [grandchild]);
      assertEqual(dismissTaskOverviewNode(nodes()[0]), undefined);
      assertEqual(workspace.sessions.find((item) => item.id === child)?.parentID, root);
      setWorkspace("statusBySession", child, { type: "busy" });
      assertEqual(nodes()[0].sessionID, child);
      assertEqual(dismissTaskOverviewNode(childNode), undefined, "a stale row cannot dismiss a now busy session");
      setWorkspace("statusBySession", child, { type: "idle" });
      assertEqual(nodes()[0].sessionID, grandchild);
    } finally {
      restoreTaskOverviewNode(key);
    }
    assertEqual(nodes()[0].sessionID, child);
  });

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

    const proposePrompt = buildProposeSubtasksPrompt("Build X");
    assertTrue(proposePrompt.includes("```hoxi-subtasks"));
    assertTrue(proposePrompt.includes("fewest cohesive, self-contained subtasks"));
    assertEqual(proposePrompt.includes("Prefer 2–6"), false);
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

    toggleTaskNodeExpanded("todo:ses_cleanup:prt_1:0", 1);
    toggleTaskNodeExpanded("subagent:ses_cleanup", 1);
    assertEqual(isTaskNodeExpanded("todo:ses_cleanup:prt_1:0", 1), true);
    assertEqual(isTaskNodeExpanded("subagent:ses_cleanup", 1), true);
    setWorkspace({
      activeSessionID: "ses_cleanup_parent",
      sessions: [session("ses_cleanup_parent"), session("ses_cleanup", "ses_cleanup_parent")],
      messagesBySession: {},
      statusBySession: {},
      interruptedBySession: {},
    });
    const cleanupNode = buildTaskOverview([])[1].nodes[0];
    assertTrue(Boolean(dismissTaskOverviewNode(cleanupNode)));
    assertEqual(buildTaskOverview([])[1].nodes.length, 0);
    forgetSessionTaskOverview(["ses_cleanup"]);
    assertEqual(isTaskNodeExpanded("todo:ses_cleanup:prt_1:0", 1), false);
    assertEqual(isTaskNodeExpanded("subagent:ses_cleanup", 1), false);
    assertEqual(buildTaskOverview([])[1].nodes.length, 1, "forgetSessionTaskOverview clears dismissed entry");
  });
});
