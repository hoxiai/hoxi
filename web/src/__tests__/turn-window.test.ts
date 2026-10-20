// web/src/__tests__/turn-window.test.ts
import {
  computeTurnWindowSlice,
  INITIAL_VISIBLE_TURNS,
  MAX_AUTO_EXPANDED_TURNS,
  resolveNextWindowLimit,
  TURN_BATCH_SIZE,
} from "../components/timeline/turn-window";
import {
  type ActivityBarSegment,
  computeVisibleSegments,
  MAX_RAIL_SEGMENTS,
  resolveSegmentDuration,
} from "../components/timeline/TaskTimelineBar";
import type { MessageTurn } from "../store/workspace";
import { assertEqual, describe, it } from "./test-harness";

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
