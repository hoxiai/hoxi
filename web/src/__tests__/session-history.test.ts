import type { SessionInfo } from "../services/sse-types";
import { compareSessionRunning, partitionSessionHistory } from "../components/layout/session-history";
import { assertEqual, describe, it } from "./test-harness";

describe("Sidebar session history", () => {
  const now = 1_800_000_000_000;
  const sessions = Array.from({ length: 12 }, (_, index) => ({
    id: String(index), title: String(index),
    time: { created: now - (index + 1) * 3_600_000, updated: now - (index + 1) * 3_600_000 },
  } as SessionInfo));
  const options = {
    enabled: true, minimum: 7, recentMinutes: 60, now,
    activityTime: (session: SessionInfo) => session.time.updated,
    protectedSession: (_session: SessionInfo) => false,
  };

  it("puts busy and retrying sessions first while preserving order within each group", () => {
    const statuses = { "8": { type: "busy" }, "10": { type: "retry" }, "0": { type: "idle" } };
    const sorted = [...sessions].sort((a, b) => compareSessionRunning(a, b, statuses));
    assertEqual(sorted.map((session) => session.id).join(","), "8,10,0,1,2,3,4,5,6,7,9,11");
    assertEqual(sessions[0].id, "0");
    const result = partitionSessionHistory(sorted, options);
    assertEqual(result.visible.slice(0, 2).map((session) => session.id).join(","), "8,10");
  });

  it("restores the original order when execution finishes", () => {
    const statuses = { "8": { type: "busy" } };
    assertEqual([...sessions].sort((a, b) => compareSessionRunning(a, b, statuses))[0].id, "8");
    statuses["8"].type = "idle";
    assertEqual([...sessions].sort((a, b) => compareSessionRunning(a, b, statuses))
      .map((session) => session.id).join(","), sessions.map((session) => session.id).join(","));
  });

  it("keeps the minimum and collapses only the remainder", () => {
    const result = partitionSessionHistory(sessions, options);
    assertEqual(result.visible.length, 7);
    assertEqual(result.older.length, 5);
    assertEqual(partitionSessionHistory(sessions.slice(0, 6), options).older.length, 0);
  });

  it("preserves row identities when the active session changes the visible partition", () => {
    const select = (id: string) => partitionSessionHistory(sessions, {
      ...options, protectedSession: (session) => session.id === id,
    });
    const before = select("8");
    const after = select("9");
    assertEqual(before.visible.length, 8);
    assertEqual(after.visible.length, 8);
    for (let index = 0; index < options.minimum; index++) {
      assertEqual(after.visible[index], before.visible[index]);
    }
    assertEqual(after.visible[7], sessions[9]);
    assertEqual(after.older.find((session) => session.id === "8"), sessions[8]);
  });

  it("unions recent activity and protected sessions with the minimum", () => {
    const result = partitionSessionHistory(sessions, {
      ...options,
      activityTime: (session) => Number(session.id) < 9 ? now - 60_000 : session.time.updated,
      protectedSession: (session) => session.id === "11",
    });
    assertEqual(result.visible.map((session) => session.id).join(","), "0,1,2,3,4,5,6,7,8,11");
    assertEqual(result.older.length, 2);
  });

  it("supports disabling collapse, custom counts, and zero retention", () => {
    assertEqual(partitionSessionHistory(sessions, { ...options, enabled: false }).older.length, 0);
    assertEqual(partitionSessionHistory(sessions, {
      ...options, minimum: 3, recentMinutes: 0, activityTime: () => now,
    }).visible.length, 3);
  });

  it("expires recent sessions at the configured boundary", () => {
    const recent = { ...options, minimum: 1, activityTime: () => now - 3_600_000 };
    assertEqual(partitionSessionHistory(sessions, recent).older.length, 0);
    assertEqual(partitionSessionHistory(sessions, { ...recent, now: now + 1 }).older.length, 11);
  });
});
