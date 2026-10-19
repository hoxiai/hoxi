// web/src/__tests__/task-progress.test.ts
import {
  groupAssistantParts,
  hasLiveTailRenderedOutput,
  hasLiveTailWorkGroup,
  isAttentionToolPart,
  resolveMergedTurnParts,
  resolveTurnNavigationTarget,
  stabilizeTurnRenderItems,
} from "../components/timeline/ContextActivityGroupView";
import {
  collectCycleStepItems,
  findLatestTodoPart,
  isTodoListStale,
  normalizeTodoItems,
  summarizeTodoActivity,
} from "../components/timeline/task-progress-helpers";
import {
  collectCycleChangedFiles,
  collectSessionChangedFiles,
} from "../components/timeline/tool-card-state";
import type { MessagePart } from "../services/sse-types";

import type { MessageTurn } from "../store/workspace";
import { firstMatchingPartID } from "../components/timeline/TranscriptSearchBar";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

const sid = "ses_tp";
const done = (
  id: string,
  tool: string,
  input: Record<string, unknown> = {},
  metadata: Record<string, unknown> = {}
): MessagePart =>
  ({
    id, sessionID: sid, messageID: "msg_a", type: "tool", callID: id, tool,
    state: { status: "completed", input, output: "", title: "", metadata, time: { start: 1, end: 2 } },
  }) as MessagePart;
const textPart = (id: string, text: string): MessagePart => ({
  id, sessionID: sid, messageID: "msg_a", type: "text", text,
});
const reasoningPart = (id: string, text: string): MessagePart => ({
  id, sessionID: sid, messageID: "msg_a", type: "reasoning", text, time: { start: 1, end: 2 },
});
const todoInput = (statuses: string[]) => ({
  todos: statuses.map((status, i) => ({ id: String(i), content: `item ${i}`, status })),
});

describe("task progress todo staleness", () => {
  it("navigates a work-only turn to the earlier turn that anchors its run", () => {
    const turns: MessageTurn[] = [
      { info: { id: "anchor", sessionID: sid, role: "assistant", time: { created: 1 } },
        parts: [textPart("intro", "Checking files"), done("own", "read")] },
      { info: { id: "folded", sessionID: sid, role: "assistant", time: { created: 2 } },
        parts: [done("carried", "bash", { command: "unique-command" })] },
      { info: { id: "answer", sessionID: sid, role: "assistant", time: { created: 3 } },
        parts: [textPart("final", "Finished")] },
    ];
    const matched = firstMatchingPartID(turns[1], /unique-command/g);
    assertEqual(matched, "carried");
    assertEqual(resolveTurnNavigationTarget(turns, "folded", matched), "anchor");
    assertEqual(resolveTurnNavigationTarget(turns, "anchor", "intro"), "anchor");
    assertEqual(resolveTurnNavigationTarget(turns, "answer"), "answer");
    assertTrue(resolveMergedTurnParts(turns, 1).isVisuallyEmpty, "folded turn renders nothing itself");
  });

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

  it("folds read, edit, bash, and reasoning parts into unified activity groups across turns", () => {
    const turns: MessageTurn[] = [
      { info: { id: "msg_u", sessionID: sid, role: "user", time: { created: 1 } }, parts: [textPart("u1", "fix bug")] },
      {
        info: { id: "msg_a1", sessionID: sid, role: "assistant", time: { created: 2 } },
        parts: [reasoningPart("r1", "Thinking..."), done("t1", "read", { filePath: "a.ts" }), textPart("empty", "   ")],
      },
      {
        info: { id: "msg_a2", sessionID: sid, role: "assistant", time: { created: 3 } },
        parts: [done("t2", "edit", { filePath: "a.ts" }), done("t3", "bash", { command: "npm test" })],
      },
      {
        info: { id: "msg_a3", sessionID: sid, role: "assistant", time: { created: 4, completed: 5 } },
        parts: [reasoningPart("r2", "Wrapping up"), textPart("txt_final", "All fixed and tested.")],
      },
    ];

    const m1 = resolveMergedTurnParts(turns, 1);
    assertTrue(!m1.isVisuallyEmpty, "the turn that starts the run anchors the unified group");
    assertDeepEqual(m1.carriedTurnIDs, ["msg_a2", "msg_a3"]);
    const m2 = resolveMergedTurnParts(turns, 2);
    assertTrue(m2.isVisuallyEmpty, "pure work intermediate turn folds back into the anchor");

    const m3 = resolveMergedTurnParts(turns, 3);
    assertTrue(!m3.isVisuallyEmpty, "final turn renders its response");
    assertDeepEqual(m3.effectiveParts.map((p) => p.id), ["txt_final"]);

    const grouped = groupAssistantParts(m1.effectiveParts);
    assertEqual(resolveTurnNavigationTarget(turns, "msg_a1"), "msg_a1");
    assertEqual(resolveTurnNavigationTarget(turns, "msg_a2"), "msg_a1");
    assertEqual(resolveTurnNavigationTarget(turns, "msg_a3"), "msg_a3");
    assertEqual(resolveTurnNavigationTarget(turns, "msg_a3", "r2"), "msg_a1");
    assertEqual(resolveTurnNavigationTarget(turns, "msg_u"), "msg_u");
    assertEqual(resolveTurnNavigationTarget(turns, "missing"), "missing");
    assertEqual(grouped.length, 1, "one unified activity group in the anchor turn");
    assertEqual(grouped[0].kind, "activity-group");
    if (grouped[0].kind === "activity-group") {
      assertDeepEqual(
        grouped[0].parts.map((p) => p.id),
        ["r1", "t1", "t2", "t3", "r2"]
      );
    }
  });

  it("keeps the anchor group stable while new steps stream in", () => {
    const user: MessageTurn = { info: { id: "msg_u", sessionID: sid, role: "user", time: { created: 1 } }, parts: [] };
    const step = (id: string, parts: MessagePart[]): MessageTurn => ({
      info: { id, sessionID: sid, role: "assistant", time: { created: 2 } },
      parts,
    });
    const first = step("msg_s1", [done("s1", "read")]);
    const before = [user, first];
    const after = [user, first, step("msg_s2", []), step("msg_s3", [done("s3", "bash")])];
    const anchorBefore = resolveMergedTurnParts(before, 1);
    const anchorAfter = resolveMergedTurnParts(after, 1);
    assertTrue(anchorBefore.ownsLiveTail && anchorAfter.ownsLiveTail, "anchor keeps the live tail");
    assertDeepEqual(anchorAfter.effectiveParts.map((p) => p.id), ["s1", "s3"]);
    assertTrue(resolveMergedTurnParts(after, 3).isVisuallyEmpty, "newest step does not remount the group");
    assertTrue(!resolveMergedTurnParts(after, 3).ownsLiveTail, "only the anchor owns the live tail");
  });

  it("detects live tail work groups and attention-worthy failed commands", () => {
    const activeCycle: MessageTurn[] = [
      {
        info: { id: "msg_a1", sessionID: sid, role: "assistant", time: { created: 2 } },
        parts: [done("t1", "read"), done("t2", "bash", { command: "go test ./..." }, { exitCode: 1 })],
      },
    ];
    assertTrue(hasLiveTailWorkGroup(activeCycle), "cycle ending with tool call has live tail group");
    assertTrue(!isAttentionToolPart(activeCycle[0].parts[0]), "normal read is not attention tool");
    assertTrue(isAttentionToolPart(activeCycle[0].parts[1]), "non-zero exit bash command is surfaced");

    const completedWithText: MessageTurn[] = [
      {
        info: { id: "msg_a1", sessionID: sid, role: "assistant", time: { created: 2, completed: 3 } },
        parts: [done("t1", "read"), textPart("txt1", "Done")],
      },
    ];
    assertTrue(!hasLiveTailWorkGroup(completedWithText), "cycle ending with text has no live tail work group");
    assertTrue(hasLiveTailRenderedOutput(completedWithText), "cycle with streaming/completed text already renders output");

    const emptyWaitingCycle: MessageTurn[] = [
      {
        info: { id: "msg_a1", sessionID: sid, role: "assistant", time: { created: 2 } },
        parts: [textPart("blank", "   ")],
      },
    ];
    assertTrue(!hasLiveTailRenderedOutput(emptyWaitingCycle), "blank placeholder turn still needs waiting indicator");

    const rPart = reasoningPart("r_stream", "Thinking...");
    const tPart = textPart("t_stream", "Hello");
    const firstItems = groupAssistantParts([rPart]);
    const secondItems = stabilizeTurnRenderItems(firstItems, groupAssistantParts([rPart]));
    assertTrue(firstItems === secondItems, "stabilizeTurnRenderItems reuses outer array when parts match");
    const thirdItems = stabilizeTurnRenderItems(secondItems, groupAssistantParts([rPart, tPart]));
    assertTrue(thirdItems[0] === firstItems[0], "stabilizeTurnRenderItems preserves existing activity-group item reference when new single text part appends");
  });

  it("matches merged tail rendering across assistant error boundaries", () => {
    const work = done("before_error", "read");
    const reasoning = reasoningPart("before_error_reasoning", "Checked the files");
    const freshWork = done("after_error", "bash");
    const turn = (id: string, parts: MessagePart[], error = false): MessageTurn => ({
      info: {
        id, sessionID: sid, role: "assistant", time: { created: 1 },
        ...(error ? { error: { name: "APIError", data: { message: "Request failed" } } } : {}),
      },
      parts,
    });
    const cases: Array<{ label: string; turns: MessageTurn[]; expected: boolean }> = [
      { label: "empty cycle", turns: [], expected: false },
      { label: "errored work then empty assistant", turns: [turn("error", [work], true), turn("tail", [])], expected: false },
      { label: "errored reasoning then whitespace assistant", turns: [turn("error", [reasoning], true), turn("tail", [textPart("blank", "  ")])], expected: false },
      { label: "carried work flushed by empty error", turns: [turn("work", [work]), turn("error", [], true), turn("tail", [])], expected: false },
      { label: "work carries through empty assistants without error", turns: [turn("work", [work]), turn("empty", []), turn("tail", [])], expected: true },
      { label: "fresh work after error carries to tail", turns: [turn("error", [work], true), turn("fresh", [freshWork]), turn("tail", [])], expected: true },
      { label: "visible text after fresh work", turns: [turn("error", [work], true), turn("tail", [freshWork, textPart("answer", "Done")])], expected: false },
      { label: "errored final turn", turns: [turn("error", [work], true)], expected: false },
    ];

    for (const { label, turns, expected } of cases) {
      const hasRenderedLiveTail = turns.some((_, index) => {
        const merged = resolveMergedTurnParts(turns, index);
        const rendered = groupAssistantParts(merged.effectiveParts);
        return merged.ownsLiveTail && rendered[rendered.length - 1]?.kind === "activity-group";
      });
      assertEqual(hasRenderedLiveTail, expected, `${label}: merged rendering`);
      assertEqual(hasLiveTailWorkGroup(turns), expected, label);
    }
  });

  it("excludes unmodified files, failed edits, and todowrite from changed files summary", () => {
    const erroredEdit: MessagePart = {
      id: "err_edit",
      sessionID: sid,
      messageID: "msg_a1",
      type: "tool",
      callID: "err_edit",
      tool: "edit",
      state: {
        status: "error",
        input: { filePath: "failed.ts", oldString: "a", newString: "b" },
        error: "oldString not found",
        time: { start: 1, end: 2 },
      },
    };
    const noopEdit = done("noop_edit", "edit", {
      filePath: "same.ts",
      oldString: "const x = 1;",
      newString: "const x = 1;",
    });
    const realEdit = done("real_edit", "edit", {
      filePath: "changed.ts",
      oldString: "const x = 1;",
      newString: "const x = 2;\nconst y = 3;",
    });
    const turns: MessageTurn[] = [
      {
        info: { id: "msg_a1", sessionID: sid, role: "assistant", time: { created: 2, completed: 3 } },
        parts: [
          done("todo_1", "todowrite", todoInput(["completed"])),
          erroredEdit,
          noopEdit,
          realEdit,
        ],
      },
    ];
    const changed = collectCycleChangedFiles(turns, sid);
    assertDeepEqual(
      changed.map((d) => ({ file: d.file, additions: d.additions, deletions: d.deletions })),
      [{ file: "changed.ts", additions: 2, deletions: 1 }]
    );

    const multiTurnSession: MessageTurn[] = [
      ...turns,
      { info: { id: "msg_u2", sessionID: sid, role: "user", time: { created: 4 } }, parts: [textPart("u2", "more")] },
      {
        info: { id: "msg_a2", sessionID: sid, role: "assistant", time: { created: 5, completed: 6 } },
        parts: [
          done("real_edit_2", "edit", {
            filePath: "second.go",
            oldString: "package a",
            newString: "package a\n\nfunc main() {}",
          }),
        ],
      },
    ];
    const sessionChanged = collectSessionChangedFiles(multiTurnSession, sid);
    assertDeepEqual(
      sessionChanged.map((d) => ({ file: d.file, additions: d.additions, deletions: d.deletions })),
      [{ file: "second.go", additions: 2, deletions: 0 }]
    );
  });
});

