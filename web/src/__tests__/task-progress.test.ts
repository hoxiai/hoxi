// web/src/__tests__/task-progress.test.ts
import {
  collectCycleStepItems,
  findLatestTodoPart,
  isTodoListStale,
  normalizeTodoItems,
  summarizeTodoActivity,
} from "../components/timeline/task-progress-helpers";
import type { MessagePart } from "../services/sse-types";
import type { MessageTurn } from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

const sid = "ses_tp";
const done = (id: string, tool: string, input: Record<string, unknown> = {}): MessagePart =>
  ({
    id, sessionID: sid, messageID: "msg_a", type: "tool", callID: id, tool,
    state: { status: "completed", input, output: "", title: "", metadata: {}, time: { start: 1, end: 2 } },
  }) as MessagePart;
const todoInput = (statuses: string[]) => ({
  todos: statuses.map((status, i) => ({ id: String(i), content: `item ${i}`, status })),
});

describe("task progress todo staleness", () => {
  it("keeps recorded todo statuses after the session ends instead of forcing completed", () => {
    const raw = todoInput(["completed", "in_progress", "pending"]).todos;
    const statuses = normalizeTodoItems(raw, false, false).map((t) => t.status);
    assertDeepEqual(statuses, ["completed", "in_progress", "pending"]);
    const paused = normalizeTodoItems(raw, false, true).map((t) => t.status);
    assertDeepEqual(paused, ["completed", "interrupted", "pending"]);
  });

  it("counts steps after the latest todo update and flags a stale list", () => {
    const turns: MessageTurn[] = [
      { info: { id: "msg_u", sessionID: sid, role: "user", time: { created: 1 } }, parts: [] },
      {
        info: { id: "msg_a", sessionID: sid, role: "assistant", time: { created: 2 } },
        parts: [
          done("prt_1", "read"),
          done("prt_todo", "todowrite", todoInput(["in_progress", "pending", "pending", "pending"])),
          done("prt_2", "bash", { command: "git status" }),
          done("prt_3", "read"),
          done("prt_4", "glob"),
        ],
      },
    ];
    const latest = findLatestTodoPart(turns);
    assertEqual(latest?.partID, "prt_todo");
    const steps = collectCycleStepItems(turns, sid, false, latest?.partID);
    const summary = summarizeTodoActivity(steps);
    assertEqual(summary.stepsDone, 4);
    assertEqual(summary.stepsSinceTodo, 3);
    const todos = normalizeTodoItems(latest!.todos, true, false);
    assertTrue(isTodoListStale(todos, summary), "3 steps after the todo update is stale");
    const fresh = { ...summary, stepsSinceTodo: 2 };
    assertTrue(!isTodoListStale(todos, fresh), "below threshold is not stale");
    const allDone = normalizeTodoItems(todoInput(["completed", "cancelled"]).todos, true, false);
    assertTrue(!isTodoListStale(allDone, summary), "finished lists are never stale");
  });
});
