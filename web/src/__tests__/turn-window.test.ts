// web/src/__tests__/turn-window.test.ts
import {
  computeTurnWindowSlice,
  INITIAL_VISIBLE_TURNS,
  MAX_AUTO_EXPANDED_TURNS,
  nextProgressiveTurnLimit,
  resolveNextWindowLimit,
  TURN_BATCH_SIZE,
  TURN_MOUNT_CHUNK,
} from "../components/timeline/turn-window";
import {
  type ActivityBarSegment,
  buildTurnSegments,
  computeVisibleSegments,
  MAX_RAIL_SEGMENTS,
  resolveSegmentDuration,
} from "../components/timeline/TaskTimelineBar";
import type { MessageTurn } from "../store/workspace";
import { resolveMergedTurnParts } from "../components/timeline/context-activity-turns";
import { assertEqual, assertTrue, describe, it } from "./test-harness";

function mockTurn(id: string, role: "user" | "assistant" = "assistant"): MessageTurn {
  return {
    info: {
      id,
      role,
      sessionID: "test-ses",
      time: { created: Date.now() },
    },
    parts: [],
  };
}

describe("TurnWindow windowing slice & prompt cycle protection", () => {
  it("keeps a long live activity anchor stable and renders each carried step once", () => {
    const turns = [mockTurn("prompt", "user"), ...Array.from({ length: 1000 }, (_, i) => {
      const turn = mockTurn(`step_${i}`);
      turn.parts = [{ type: "reasoning", id: `part_${i}`, messageID: turn.info.id, sessionID: "test-ses", text: "work", time: { start: 1, end: 2 } }];
      return turn;
    })];
    const slice = computeTurnWindowSlice(turns, INITIAL_VISIBLE_TURNS);
    assertEqual(slice.anchorIndex, 1);
    assertEqual(slice.indexes[0], 0);
    assertEqual(slice.indexes[1], 1);
    assertEqual(slice.visibleTurns.length, INITIAL_VISIBLE_TURNS + TURN_BATCH_SIZE + 2);
    const anchor = resolveMergedTurnParts(turns, slice.anchorIndex!, slice.anchorIndex!);
    assertEqual(anchor.effectiveParts.length, 1000);
    assertEqual(resolveMergedTurnParts(turns, slice.startIndex, slice.anchorIndex!).effectiveParts.length, 0);
    const after = computeTurnWindowSlice([...turns, mockTurn("next")], INITIAL_VISIBLE_TURNS);
    assertEqual(after.visibleTurns[1], slice.visibleTurns[1]);
  });
  it("bounds a long prompt cycle while preserving its prompt and latest steps", () => {
    const turns = [mockTurn("prompt", "user"), ...Array.from({ length: 1000 }, (_, i) => mockTurn(`step_${i}`))];
    const slice = computeTurnWindowSlice(turns, INITIAL_VISIBLE_TURNS);
    assertEqual(slice.promptIndex, 0);
    assertEqual(slice.visibleTurns[0].info.id, "prompt");
    assertEqual(slice.visibleTurns.at(-1)?.info.id, "step_999");
    assertEqual(slice.visibleTurns.length, INITIAL_VISIBLE_TURNS + TURN_BATCH_SIZE + 1);
    assertEqual(slice.hiddenCount + slice.visibleTurns.length, turns.length);
    // A single prompt owns every folded step, so no earlier *message* is hidden.
    assertEqual(slice.hiddenUserCount, 0);
    const expanded = computeTurnWindowSlice(turns, INITIAL_VISIBLE_TURNS + TURN_BATCH_SIZE);
    assertEqual(expanded.visibleTurns.length, slice.visibleTurns.length + TURN_BATCH_SIZE);
    const all = computeTurnWindowSlice(turns, turns.length);
    assertEqual(all.visibleTurns.length, turns.length);
    assertEqual(all.hiddenCount, 0);
  });
  it("slices recent turns and never cuts an assistant cycle from its user prompt", () => {
    // Turn 0: user, Turn 1: assistant, Turn 2: assistant, Turn 3: assistant
    // Turn 4: user, Turn 5: assistant, Turn 6: assistant
    const turns: MessageTurn[] = [
      mockTurn("t0", "user"),
      mockTurn("t1", "assistant"),
      mockTurn("t2", "assistant"),
      mockTurn("t3", "assistant"),
      mockTurn("t4", "user"),
      mockTurn("t5", "assistant"),
      mockTurn("t6", "assistant"),
    ];

    // If limit is 2 (t5, t6), t5 is assistant, so it must walk back to include t4 (the user prompt).
    const slice = computeTurnWindowSlice(turns, 2);
    assertEqual(slice.startIndex, 4);
    assertEqual(slice.visibleTurns.length, 3);
    assertEqual(slice.visibleTurns[0].info.id, "t4");
    assertEqual(slice.hiddenCount, 4);
    // t0 is a genuinely hidden earlier message; t4 is carried as the visible prompt.
    assertEqual(slice.hiddenUserCount, 1);

    // If limit is 7 (all turns), startIndex is 0.
    const full = computeTurnWindowSlice(turns, 7);
    assertEqual(full.startIndex, 0);
    assertEqual(full.visibleTurns.length, 7);
    assertEqual(full.hiddenCount, 0);
  });
});

describe("TurnWindow limit resolution & session-switching lifecycle", () => {
  it("resets limit to INITIAL_VISIBLE_TURNS when switching between sessions", () => {
    const listA: MessageTurn[] = Array.from({ length: 60 }, (_, i) => mockTurn(`a_${i}`));
    const listB: MessageTurn[] = Array.from({ length: 60 }, (_, i) => mockTurn(`b_${i}`));

    const stateA = { limit: 50, seen: 60, lastTailID: "a_59" };
    const stateB = { limit: 12, seen: 0, lastTailID: "" };

    // Switching from session A to session B resets limit for session B
    const limitB = resolveNextWindowLimit({
      currentSession: "session_B",
      lastActiveSessionID: "session_A",
      state: stateB,
      total: listB.length,
      tailID: "b_59",
      list: listB,
    });
    assertEqual(limitB, INITIAL_VISIBLE_TURNS);
    assertEqual(stateB.limit, INITIAL_VISIBLE_TURNS);

    // Switching back from session B to session A resets session A from 50 to INITIAL_VISIBLE_TURNS
    const limitA = resolveNextWindowLimit({
      currentSession: "session_A",
      lastActiveSessionID: "session_B",
      state: stateA,
      total: listA.length,
      tailID: "a_59",
      list: listA,
    });
    assertEqual(limitA, INITIAL_VISIBLE_TURNS);
    assertEqual(stateA.limit, INITIAL_VISIBLE_TURNS);
  });

  it("mounts older turns in bounded animation-frame chunks", () => {
    let limit = INITIAL_VISIBLE_TURNS;
    const target = INITIAL_VISIBLE_TURNS + TURN_BATCH_SIZE;
    const steps: number[] = [];
    while (limit < target) {
      limit = nextProgressiveTurnLimit(limit, target);
      steps.push(limit);
    }
    assertEqual(steps.length, Math.ceil(TURN_BATCH_SIZE / TURN_MOUNT_CHUNK));
    assertTrue(steps.every((value, index) => index === 0 || value - steps[index - 1] <= TURN_MOUNT_CHUNK));
    assertEqual(limit, target);
    assertEqual(nextProgressiveTurnLimit(target, target), target);
    assertEqual(nextProgressiveTurnLimit(5, 100, 0), 6);
  });

  it("caps automatic streaming tail expansion at MAX_AUTO_EXPANDED_TURNS", () => {
    let list: MessageTurn[] = Array.from({ length: 10 }, (_, i) => mockTurn(`t_${i}`));
    const state = { limit: INITIAL_VISIBLE_TURNS, seen: 10, lastTailID: "t_9" };

    // Simulate appending turns one-by-one up to 50 turns
    for (let i = 10; i < 50; i++) {
      list = [...list, mockTurn(`t_${i}`)];
      resolveNextWindowLimit({
        currentSession: "session_stream",
        lastActiveSessionID: "session_stream",
        state,
        total: list.length,
        tailID: `t_${i}`,
        list,
      });
    }

    // Must be capped at MAX_AUTO_EXPANDED_TURNS
    assertEqual(state.limit, MAX_AUTO_EXPANDED_TURNS);
  });

  it("does not shrink user-expanded limits during tail streaming", () => {
    // User manually loaded earlier turns, so limit is 44
    let list: MessageTurn[] = Array.from({ length: 50 }, (_, i) => mockTurn(`t_${i}`));
    const state = { limit: 44, seen: 50, lastTailID: "t_49" };

    // New streaming turn arrives
    list = [...list, mockTurn("t_50")];
    const nextLimit = resolveNextWindowLimit({
      currentSession: "session_stream",
      lastActiveSessionID: "session_stream",
      state,
      total: list.length,
      tailID: "t_50",
      list,
    });

    // Limit must remain 44 (not clamped down to 32)
    assertEqual(nextLimit, 44);
    assertEqual(state.limit, 44);
  });
});

describe("TaskTimelineBar segment windowing & duration resolution", () => {
  it("keeps all segments when count is within MAX_RAIL_SEGMENTS", () => {
    const segments: ActivityBarSegment[] = Array.from({ length: 30 }, (_, i) => ({
      id: `seg_${i}`,
      messageID: `m_${i}`,
      kind: "explore",
      color: "#34d399",
      label: `tool_${i}`,
      heightPct: 50,
      duration: 100,
    }));

    const visible = computeVisibleSegments(segments, 15, MAX_RAIL_SEGMENTS);
    assertEqual(visible.length, 30);
  });

  it("windows segments to MAX_RAIL_SEGMENTS centered around reading focus", () => {
    const total = 200;
    const segments: ActivityBarSegment[] = Array.from({ length: total }, (_, i) => ({
      id: `seg_${i}`,
      messageID: `m_${i}`,
      kind: "explore",
      color: "#34d399",
      label: `tool_${i}`,
      heightPct: 50,
      duration: 100,
    }));

    // Focus near middle (index 100)
    const midWindow = computeVisibleSegments(segments, 100, MAX_RAIL_SEGMENTS);
    assertEqual(midWindow.length, MAX_RAIL_SEGMENTS);
    const midHalf = Math.floor(MAX_RAIL_SEGMENTS / 2);
    assertEqual(midWindow[0].id, `seg_${100 - midHalf}`);

    // Focus near start (index 5) -> clamped to start = 0
    const startWindow = computeVisibleSegments(segments, 5, MAX_RAIL_SEGMENTS);
    assertEqual(startWindow.length, MAX_RAIL_SEGMENTS);
    assertEqual(startWindow[0].id, "seg_0");
    assertEqual(startWindow[MAX_RAIL_SEGMENTS - 1].id, `seg_${MAX_RAIL_SEGMENTS - 1}`);

    // Focus near end (index 195) -> clamped to end = 200
    const endWindow = computeVisibleSegments(segments, 195, MAX_RAIL_SEGMENTS);
    assertEqual(endWindow.length, MAX_RAIL_SEGMENTS);
    assertEqual(endWindow[MAX_RAIL_SEGMENTS - 1].id, "seg_199");
    assertEqual(endWindow[0].id, `seg_${total - MAX_RAIL_SEGMENTS}`);
  });

  it("resolves static duration for completed steps and dynamic duration for active steps", () => {
    const completedSeg: ActivityBarSegment = {
      id: "seg_done",
      messageID: "m_1",
      kind: "edit",
      color: "#34d399",
      label: "done",
      heightPct: 50,
      duration: 1500,
      time: { start: 1000, end: 2500 },
    };
    assertEqual(resolveSegmentDuration(completedSeg, 5000), 1500);

    const runningSeg: ActivityBarSegment = {
      id: "seg_running",
      messageID: "m_2",
      kind: "explore",
      color: "#a78bfa",
      label: "running",
      heightPct: 50,
      time: { start: 10000 },
    };
    assertEqual(resolveSegmentDuration(runningSeg, 13500), 3500);
  });
});

describe("TaskTimelineBar per-turn trajectory grouping", () => {
  const toolPart = (id: string, messageID: string, tool: string, status: "running" | "completed") => ({
    id,
    sessionID: "test-ses",
    messageID,
    type: "tool" as const,
    callID: `call_${id}`,
    tool,
    state:
      status === "running"
        ? { status: "running" as const, input: { command: "npm test" }, time: { start: 1300 } }
        : {
            status: "completed" as const,
            input: { command: "npm test" },
            output: "ok",
            title: tool,
            metadata: {},
            time: { start: 1300, end: 1800 },
          },
  });

  it("collapses every assistant part of a prompt into one rail node", () => {
    const turns: MessageTurn[] = [
      {
        info: { id: "u1", sessionID: "test-ses", role: "user", time: { created: 1000, completed: 1000 } },
        parts: [{ id: "p1", sessionID: "test-ses", messageID: "u1", type: "text", text: "Fix the auth bug" }],
      },
      {
        info: { id: "a1", sessionID: "test-ses", role: "assistant", time: { created: 1100, completed: 2000 } },
        parts: [
          { id: "p2", sessionID: "test-ses", messageID: "a1", type: "reasoning", text: "thinking", time: { start: 1100, end: 1300 } },
          toolPart("p3", "a1", "bash", "completed"),
        ],
      },
      {
        info: { id: "u2", sessionID: "test-ses", role: "user", time: { created: 2100, completed: 2100 } },
        parts: [{ id: "p4", sessionID: "test-ses", messageID: "u2", type: "text", text: "Add regression tests" }],
      },
      {
        info: { id: "a2", sessionID: "test-ses", role: "assistant", time: { created: 2200, completed: 3000 } },
        parts: [{ id: "p5", sessionID: "test-ses", messageID: "a2", type: "text", text: "Done" }],
      },
    ];

    const segments = buildTurnSegments(turns);
    assertEqual(segments.length, 2);
    assertEqual(segments[0].messageID, "u1");
    assertEqual(segments[0].messageIDs?.join(","), "u1,a1");
    assertEqual(segments[0].stepCount, 2);
    assertEqual(segments[0].duration, 1000);
    assertEqual(segments[0].label.includes("Fix the auth bug"), true);
    assertEqual(segments[1].messageID, "u2");
    assertEqual(segments[1].label.includes("Add regression tests"), true);
    assertEqual(segments[1].running, false);
  });

  it("keeps a prompt live while one of its tools is still running", () => {
    const turns: MessageTurn[] = [
      {
        info: { id: "u1", sessionID: "test-ses", role: "user", time: { created: 1000 } },
        parts: [{ id: "p1", sessionID: "test-ses", messageID: "u1", type: "text", text: "Run tests" }],
      },
      {
        info: { id: "a1", sessionID: "test-ses", role: "assistant", time: { created: 1100 } },
        parts: [toolPart("p2", "a1", "bash", "running")],
      },
    ];

    const segments = buildTurnSegments(turns);
    assertEqual(segments.length, 1);
    assertEqual(segments[0].running, true);
    assertEqual(segments[0].time?.end, undefined);
    assertEqual(segments[0].duration, undefined);
    assertEqual(resolveSegmentDuration(segments[0], 2000), 1000);
  });
});
