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
  isTodoListStale,
  summarizeTodoActivity,
} from "../components/timeline/task-progress-helpers";
import {
  findLatestTodoPart,
  normalizeTodoItems,
} from "../store/task-progress";
import {
  collectCycleChangedFiles,
  collectSessionChangedFiles,
  deriveMarkdownDownloadFilename,
  resolveToolFileDiff,
} from "../components/timeline/tool-card-state";
import type { MessagePart, ToolPart } from "../services/sse-types";
import { filterLocalSessionDiffsByTurns } from "../store/message-lookup";
import { type MessageTurn, setWorkspace } from "../store/workspace";
import { firstMatchingPartID } from "../components/timeline/TranscriptSearchBar";
import { formatElapsedSeconds } from "../components/timeline/duration-label";
import {
  parseInline,
  parseMarkdownBlocks,
  stabilizeBlockToken,
} from "../components/timeline/markdown-parser";
import {
  liveReasoningTicker,
  normalizeReasoningText,
  reasoningHeading,
} from "../components/timeline/ThoughtBox";
import {
  expandInlineThinkingParts,
  extractVisibleAssistantText,
  splitInlineThinking,
} from "../components/timeline/context-activity-parts";
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

describe("elapsed duration labels", () => {
  it("keeps tenths under a minute and folds longer spans", () => {
    assertEqual(formatElapsedSeconds(0.4), "0.4s");
    assertEqual(formatElapsedSeconds(12.4), "12.4s");
    assertEqual(formatElapsedSeconds(59.9), "59.9s");
    assertEqual(formatElapsedSeconds(60), "1m 0s");
    assertEqual(formatElapsedSeconds(123.4), "2m 3s");
    assertEqual(formatElapsedSeconds(3723), "1h 2m");
  });
});

describe("task progress todo staleness", () => {
  it("keeps an omitted initial text part invisible until its first token arrives", () => {
    const initial = { id: "prt_empty", sessionID: sid, messageID: "msg_a", type: "text" } as MessagePart;
    const turns: MessageTurn[] = [{
      info: { id: "msg_a", sessionID: sid, role: "assistant", time: { created: 1 } },
      parts: [initial],
    }];
    assertEqual(groupAssistantParts(turns[0].parts).length, 0);
    assertTrue(resolveMergedTurnParts(turns, 0).isVisuallyEmpty);
    assertEqual(hasLiveTailRenderedOutput(turns), false);
    turns[0].parts[0] = { ...initial, text: "First token" } as MessagePart;
    assertEqual(groupAssistantParts(turns[0].parts).length, 1);
    assertEqual(resolveMergedTurnParts(turns, 0).isVisuallyEmpty, false);
    assertEqual(hasLiveTailRenderedOutput(turns), true);
  });

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

    let tailTextReads = 0;
    const streamingTailPart = {
      id: "txt_live",
      sessionID: sid,
      messageID: "msg_a2",
      type: "text" as const,
      get text() {
        tailTextReads++;
        return "Streaming markdown body...";
      },
    };
    assertTrue(
      hasLiveTailRenderedOutput([
        {
          info: { id: "msg_a1", sessionID: sid, role: "assistant", time: { created: 1 } },
          parts: [done("t_early", "read")],
        },
        {
          info: { id: "msg_a2", sessionID: sid, role: "assistant", time: { created: 2 } },
          parts: [streamingTailPart],
        },
      ]),
      "short-circuits on leading work part"
    );
    assertEqual(tailTextReads, 0, "does not subscribe to trailing streaming text part when leading work exists");

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

    // In local (non-isolated) mode, OpenCode `patch` parts snapshot the entire working tree
    // and `worktreeDiffsBySession` holds all uncommitted repo files. Neither may leak into
    // the session's changed-files summary or override the tool's own +/- counts.
    setWorkspace("worktreeBySession", sid, {
      sessionID: sid,
      isolated: false,
      directory: "/repo",
      createdAt: "test",
    });
    setWorkspace("worktreeDiffsBySession", sid, [
      { file: "second.go", additions: 99, deletions: 42 },
      { file: "unrelated-dirty.ts", additions: 50, deletions: 10 },
    ]);
    const turnWithSnapshotPatch: MessageTurn[] = [
      {
        info: { id: "msg_a3", sessionID: sid, role: "assistant", time: { created: 7, completed: 8 } },
        parts: [
          done("real_edit_3", "edit", {
            filePath: "second.go",
            oldString: "package a",
            newString: "package a\n\nfunc main() {}",
          }),
          {
            id: "patch_1",
            sessionID: sid,
            messageID: "msg_a3",
            type: "patch",
            hash: "abc",
            files: ["second.go", "unrelated-dirty.ts"],
          },
        ],
      },
    ];
    const localCycleChanged = collectCycleChangedFiles(turnWithSnapshotPatch, sid);
    assertDeepEqual(
      localCycleChanged.map((d) => ({ file: d.file, additions: d.additions, deletions: d.deletions })),
      [{ file: "second.go", additions: 2, deletions: 0 }]
    );
    const filteredLocalDiffs = filterLocalSessionDiffsByTurns(
      sid,
      [
        { file: "second.go", additions: 2, deletions: 0 },
        { file: "unrelated-dirty.ts", additions: 50, deletions: 10 },
      ],
      { [sid]: turnWithSnapshotPatch },
      "/repo"
    );
    assertDeepEqual(
      filteredLocalDiffs.map((d) => d.file),
      ["second.go"]
    );

    // Subagent file modifications (via metadata.sessionId, output `session_id:`, or running/interrupted
    // task tools, including Antigravity `replace_file_content` / `TargetFile`) must surface in parent summary.
    const childSid = "ses_subagent_child_1";
    const childTurns: MessageTurn[] = [
      {
        info: { id: "msg_c1", sessionID: childSid, role: "assistant", time: { created: 10, completed: 11 } },
        parts: [
          {
            id: "sub_edit_1",
            sessionID: childSid,
            messageID: "msg_c1",
            type: "tool",
            callID: "sub_edit_1",
            tool: "replace_file_content",
            state: {
              status: "completed",
              input: {
                TargetFile: "src/subagent-edited.ts",
                TargetContent: "const a = 1;",
                ReplacementContent: "const a = 2;\nconst b = 3;",
              },
              output: "updated",
              title: "Edit subagent-edited.ts",
              metadata: {},
              time: { start: 10, end: 11 },
            },
          },
        ],
      },
    ];
    setWorkspace("messagesBySession", childSid, childTurns);
    const parentWithSubagent: MessageTurn[] = [
      {
        info: { id: "msg_p1", sessionID: sid, role: "assistant", time: { created: 9, completed: 12 } },
        parts: [
          {
            id: "task_call_1",
            sessionID: sid,
            messageID: "msg_p1",
            type: "tool",
            callID: "task_call_1",
            tool: "task",
            state: {
              status: "running",
              input: { description: "Update subagent file" },
              output: `session_id: ${childSid}\nDone`,
              title: "Update subagent file",
              time: { start: 9 },
            },
          } as unknown as MessagePart,
        ],
      },
    ];
    const subagentChanged = collectCycleChangedFiles(parentWithSubagent, sid);
    assertDeepEqual(
      subagentChanged.map((d) => ({ file: d.file, additions: d.additions, deletions: d.deletions })),
      [{ file: "src/subagent-edited.ts", additions: 2, deletions: 1 }]
    );
    const subagentFilteredDiffs = filterLocalSessionDiffsByTurns(
      sid,
      [
        { file: "src/subagent-edited.ts", additions: 2, deletions: 1 },
        { file: "unrelated-dirty.ts", additions: 50, deletions: 10 },
      ],
      { [sid]: parentWithSubagent, [childSid]: childTurns },
      "/repo"
    );
    assertDeepEqual(
      subagentFilteredDiffs.map((d) => d.file),
      ["src/subagent-edited.ts"]
    );
    setWorkspace("messagesBySession", childSid, []);
    setWorkspace("worktreeDiffsBySession", sid, []);
  });

  it("reads Antigravity edit_file and write_file payloads as diffs", () => {
    const editPart: ToolPart = {
      id: "agy_edit",
      sessionID: sid,
      messageID: "msg_agy",
      type: "tool",
      callID: "agy_edit",
      tool: "edit",
      state: {
        status: "completed",
        input: {
          filePath: "client/lib/chat_hub_controller.dart",
          Path: "client/lib/chat_hub_controller.dart",
          Target: "const a = 1;",
          Replacement: "const a = 2;\nconst b = 3;",
        },
        output: "",
        title: "Edit chat_hub_controller.dart",
        metadata: {},
        time: { start: 1, end: 2 },
      },
    };
    assertDeepEqual(resolveToolFileDiff(editPart, true, "client/lib/chat_hub_controller.dart"), {
      file: "client/lib/chat_hub_controller.dart",
      patch: undefined,
      before: "const a = 1;",
      after: "const a = 2;\nconst b = 3;",
      additions: 2,
      deletions: 1,
    });

    const writePart: ToolPart = {
      ...editPart,
      id: "agy_write",
      tool: "write",
      state: {
        ...editPart.state,
        input: { filePath: "client/lib/new_file.dart", Content: "line 1\nline 2" },
      },
    };
    const written = resolveToolFileDiff(writePart, true, "client/lib/new_file.dart");
    assertEqual(written?.additions, 2);
    assertEqual(written?.deletions, 0);
  });

  it("keeps streaming markdown blocks and inline tokens stable without intermediate layout thrashing", () => {
    // 1. Ordered list streaming: `\n2` / `\n2.` / `\n2. ` must stay inside the same list block
    // and preserve item[0] object identity via stabilizeBlockToken.
    const step1 = parseMarkdownBlocks("1. First item\n2");
    assertEqual(step1.length, 1, "partial ordered marker stays in single list block");
    assertEqual(step1[0].type, "list");
    const list1 = step1[0] as Extract<(typeof step1)[number], { type: "list" }>;
    assertEqual(list1.items.length, 2);
    assertEqual(list1.items[0].text, "First item");
    assertEqual(list1.items[1].orderNumber, 2);
    assertEqual(list1.items[1].text, "");

    const step2 = parseMarkdownBlocks("1. First item\n2.");
    const stabilized2 = stabilizeBlockToken(step1[0], step2[0]) as typeof list1;
    assertTrue(stabilized2.items[0] === list1.items[0], "unchanged list item reference is preserved");
    assertEqual(stabilized2.items[1].orderNumber, 2);
    assertEqual(stabilized2.items[1].text, "");

    const step3 = parseMarkdownBlocks("1. First item\n2. Second item");
    const stabilized3 = stabilizeBlockToken(stabilized2, step3[0]) as typeof list1;
    assertTrue(stabilized3.items[0] === list1.items[0], "first item reference stays stable as second streams");
    assertEqual(stabilized3.items[1].text, "Second item");

    // 2. Heading streaming: `###` and `### ` mount as heading immediately rather than paragraph
    assertDeepEqual(parseMarkdownBlocks("###"), [{ type: "heading", level: 3, text: "" }]);
    assertDeepEqual(parseMarkdownBlocks("### "), [{ type: "heading", level: 3, text: "" }]);
    assertDeepEqual(parseMarkdownBlocks("### Title"), [{ type: "heading", level: 3, text: "Title" }]);

    // 3. Code fence streaming: partial opening/closing backticks don't flash paragraphs or trailing lines
    assertDeepEqual(parseMarkdownBlocks("`"), []);
    assertDeepEqual(parseMarkdownBlocks("``"), []);
    assertDeepEqual(parseMarkdownBlocks("```ts\nconst x = 1;\n`"), [
      { type: "code", lang: "ts", code: "const x = 1;" },
    ]);
    assertDeepEqual(parseMarkdownBlocks("```ts\nconst x = 1;\n``"), [
      { type: "code", lang: "ts", code: "const x = 1;" },
    ]);
    assertDeepEqual(parseMarkdownBlocks("```ts\nconst x = 1;\n```"), [
      { type: "code", lang: "ts", code: "const x = 1;" },
    ]);
    assertDeepEqual(parseMarkdownBlocks("```json {\"score\":68}```"), [
      { type: "code", lang: "json", code: '{"score":68}' },
    ]);
    assertDeepEqual(parseMarkdownBlocks("``` json {\"score\":68}```"), [
      { type: "code", lang: "json", code: '{"score":68}' },
    ]);
    assertDeepEqual(parseMarkdownBlocks("~~~json {\"score\":68}~~~"), [
      { type: "code", lang: "json", code: '{"score":68}' },
    ]);
    // Untagged one-line fences keep their whole content instead of losing a "language".
    assertDeepEqual(parseMarkdownBlocks("```{\"score\":68}```"), [
      { type: "code", lang: "text", code: '{"score":68}' },
    ]);
    assertDeepEqual(parseMarkdownBlocks("```npm run build```"), [
      { type: "code", lang: "text", code: "npm run build" },
    ]);
    assertDeepEqual(parseMarkdownBlocks("```bash```"), [
      { type: "code", lang: "text", code: "bash" },
    ]);
    assertDeepEqual(parseMarkdownBlocks("```bash npm run build```"), [
      { type: "code", lang: "bash", code: "npm run build" },
    ]);

    // 3b. Horizontal rule & GFM table streaming: no intermediate list/paragraph flash
    assertDeepEqual(parseMarkdownBlocks("Intro\n\n-"), [{ type: "paragraph", text: "Intro" }]);
    assertDeepEqual(parseMarkdownBlocks("Intro\n\n--"), [{ type: "paragraph", text: "Intro" }]);
    assertDeepEqual(parseMarkdownBlocks("Intro\n\n---"), [
      { type: "paragraph", text: "Intro" },
      { type: "hr" },
    ]);
    // 3c. In a list context, a bare `-`/`*` tail must not spawn a phantom empty item that
    // collapses into an hr on the next keystroke; a real new bullet still shows immediately.
    const bullet = { text: "a", indent: 0, kind: "bullet" };
    const listA = { type: "list", ordered: false, start: 1, items: [bullet] };
    const one = [listA] as unknown[];
    assertDeepEqual(parseMarkdownBlocks("- a\n-"), one);
    assertDeepEqual(parseMarkdownBlocks("- a\n--"), one);
    assertDeepEqual(parseMarkdownBlocks("- a\n\n-"), one);
    assertDeepEqual(parseMarkdownBlocks("* a\n*"), one);
    assertDeepEqual(parseMarkdownBlocks("- a\n---"), [listA, { type: "hr" }] as unknown[]);
    assertDeepEqual(
      parseMarkdownBlocks("- a\n- "),
      [
        {
          type: "list",
          ordered: false,
          start: 1,
          items: [bullet, { text: "", indent: 0, kind: "bullet" }],
        },
      ] as unknown[]
    );
    assertDeepEqual(parseMarkdownBlocks("| Col A | Col B |\n|---"), [
      { type: "table", headers: ["Col A", "Col B"], aligns: ["left", "left"], rows: [] },
    ]);

    // 4. Inline bold streaming: no raw `*` / `**` flash and stable bold span identity
    assertDeepEqual(parseInline("Prefix *"), [{ type: "text", text: "Prefix " }]);
    assertDeepEqual(parseInline("Prefix **"), [{ type: "text", text: "Prefix " }]);
    assertDeepEqual(parseInline("Prefix **bo"), [
      { type: "text", text: "Prefix " },
      { type: "bold", text: "bo" },
    ]);
    assertDeepEqual(parseInline("Prefix **bold*"), [
      { type: "text", text: "Prefix " },
      { type: "bold", text: "bold" },
    ]);
    assertDeepEqual(parseInline("Prefix **bold**"), [
      { type: "text", text: "Prefix " },
      { type: "bold", text: "bold" },
    ]);

    // 5. Inline code streaming: unclosed backtick marks `unclosed: true` and prevents bare file path hijack
    assertDeepEqual(parseInline("Check `"), [{ type: "text", text: "Check " }]);
    assertDeepEqual(parseInline("Check `see web/src/App.tsx"), [
      { type: "text", text: "Check " },
      { type: "code", text: "see web/src/App.tsx", unclosed: true },
    ]);
    assertDeepEqual(parseInline("Check `see web/src/App.tsx`"), [
      { type: "text", text: "Check " },
      { type: "code", text: "see web/src/App.tsx" },
    ]);

    // 6. Inline link streaming: `[label](partial` renders as link immediately without raw URL spill
    assertDeepEqual(parseInline("Visit [Docs](https://hoxi"), [
      { type: "text", text: "Visit " },
      { type: "link", text: "Docs", href: "https://hoxi" },
    ]);
    assertDeepEqual(parseInline("Visit [Docs](https://hoxi.ai)"), [
      { type: "text", text: "Visit " },
      { type: "link", text: "Docs", href: "https://hoxi.ai" },
    ]);

    // 7. Live reasoning single-line ticker updates with latest phase heading or latest thought line
    assertEqual(
      liveReasoningTicker("**Phase 1: Inspecting**\nReading files...\n**Phase 2: Updating parser**", true),
      "Phase 2: Updating parser"
    );
    assertEqual(
      liveReasoningTicker("First we check the store.\nNow verifying timeline scroll anchoring.", true),
      "Now verifying timeline scroll anchoring."
    );
    assertEqual(
      liveReasoningTicker("**Completed Analysis**\nEverything looks good.", false),
      "Completed Analysis"
    );
    assertEqual(
      deriveMarkdownDownloadFilename("## Architecture Plan: Session Diff Isolation\n\nBody text", undefined, "2026-10-19"),
      "Architecture-Plan-Session-Diff-Isolation-2026-10-19.md"
    );
    assertEqual(
      deriveMarkdownDownloadFilename("Plain short reply without heading", undefined, "2026-10-19"),
      "hoxi-note-2026-10-19.md"
    );
  });

  it("normalizes Codex concatenated reasoning headings and inline <thinking> blocks without breaking part identity", () => {
    const concatenated =
      "**Running build and mobile checks****Checking Go build and Flutter status****Reviewing potential frontend issue**";
    assertEqual(
      normalizeReasoningText(concatenated),
      "**Running build and mobile checks**\n\n**Checking Go build and Flutter status**\n\n**Reviewing potential frontend issue**"
    );
    assertEqual(
      liveReasoningTicker(concatenated, true),
      "Reviewing potential frontend issue"
    );
    assertEqual(
      reasoningHeading(concatenated).title,
      "Running build and mobile checks"
    );
    assertEqual(
      liveReasoningTicker(
        "<thinking>**Running build and mobile checks****Checking Go build and Flutter status****Reviewing potential frontend issue**",
        true
      ),
      "Reviewing potential frontend issue"
    );
    assertEqual(
      reasoningHeading(
        "<thinking>**Running build and mobile checks****Checking Go build and Flutter status****Reviewing potential frontend issue**</thinking>"
      ).title,
      "Running build and mobile checks"
    );

    // Unclosed <thinking> emitted right before tool call stays pure reasoning work
    const unclosedPart = textPart(
      "prt_unclosed_think",
      "<thinking>**Inspecting workspace layout****Checking git status**"
    );
    const expandedUnclosed = expandInlineThinkingParts([
      unclosedPart,
      done("call_1", "bash", { command: "git status" }),
    ]);
    assertEqual(expandedUnclosed.length, 2);
    assertEqual(expandedUnclosed[0].type, "reasoning");
    assertEqual(expandedUnclosed[0].id, "prt_unclosed_think");
    assertEqual(
      expandedUnclosed[0].type === "reasoning" ? expandedUnclosed[0].text : "",
      "**Inspecting workspace layout****Checking git status**"
    );

    // Closed <thinking>...</thinking> followed by visible answer splits into reasoning + visible text
    const mixedPart = textPart(
      "prt_mixed",
      "<thinking>**Checking tests****Preparing final summary**</thinking>\n\nAll tests passed."
    );
    assertEqual(extractVisibleAssistantText(mixedPart.type === "text" ? mixedPart.text : ""), "All tests passed.");
    const split = splitInlineThinking(mixedPart.type === "text" ? mixedPart.text : "");
    assertTrue(split.hasLeadingThinking);
    assertEqual(split.thinkingText, "**Checking tests****Preparing final summary**");
    assertEqual(split.visibleText, "All tests passed.");

    const turns = [
      {
        info: { id: "msg_1", sessionID: sid, role: "assistant" as const, time: { created: 1 } },
        parts: [unclosedPart, done("call_1", "bash", { command: "git status" })],
      },
      {
        info: { id: "msg_2", sessionID: sid, role: "assistant" as const, time: { created: 2, completed: 3 } },
        parts: [mixedPart],
      },
    ];
    const merged0 = resolveMergedTurnParts(turns, 0);
    const grouped0 = groupAssistantParts(merged0.effectiveParts);
    assertEqual(grouped0.length, 1);
    assertEqual(grouped0[0].kind, "activity-group");
    assertEqual(grouped0[0].kind === "activity-group" ? grouped0[0].parts.length : 0, 3);

    const merged1 = resolveMergedTurnParts(turns, 1);
    const grouped1 = groupAssistantParts(merged1.effectiveParts);
    assertEqual(grouped1.length, 1);
    assertEqual(grouped1[0].kind, "single");
    assertEqual(
      grouped1[0].kind === "single" && grouped1[0].part.type === "text" ? grouped1[0].part.text : "",
      "All tests passed."
    );

    // Verify stabilizeTurnRenderItems keeps identical object references across streaming deltas
    if (mixedPart.type === "text") {
      mixedPart.text =
        "<thinking>**Checking tests****Preparing final summary**</thinking>\n\nAll tests passed with 0 errors.";
    }
    const grouped0Next = stabilizeTurnRenderItems(
      grouped0,
      groupAssistantParts(resolveMergedTurnParts(turns, 0).effectiveParts)
    );
    const grouped1Next = stabilizeTurnRenderItems(
      grouped1,
      groupAssistantParts(resolveMergedTurnParts(turns, 1).effectiveParts)
    );
    assertTrue(grouped0Next === grouped0);
    assertTrue(grouped1Next === grouped1);
    assertEqual(
      grouped1Next[0].kind === "single" && grouped1Next[0].part.type === "text" ? grouped1Next[0].part.text : "",
      "All tests passed with 0 errors."
    );
  });
});
