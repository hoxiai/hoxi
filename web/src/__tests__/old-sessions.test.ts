import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI, type OldSessionsDeleteResult } from "../services/api";
import type { SessionInfo } from "../services/sse-types";
import {
  clearSessionGoal,
  deleteOldSessions,
  hasRecentSessionInterrupt,
  markSessionInterrupted,
  nextActiveSessionCandidate,
  partitionCodeSessions,
  removeSessionFromStore,
  resolveParentSessionID,
  resolveRootSessionID,
  sessionGoal,
  setWorkspace,
  workspace,
  writeSessionGoal,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

function session(id: string): SessionInfo {
  return { id, title: id, directory: "/tmp/project", time: { created: 1, updated: 1 } } as SessionInfo;
}

const storage = { totalBytes: 0, databaseBytes: 0, sessionsBytes: 0, engineLogsBytes: 0, cacheIndexBytes: 0, dataDir: "" };

async function withCleanupFixture(
  result: OldSessionsDeleteResult,
  run: (sent: { days?: number; ids?: string[] }) => Promise<void>
) {
  const saved = structuredClone(unwrap(workspace));
  const originalDelete = hoxiAPI.deleteOldSessions;
  const originalFetch = globalThis.fetch;
  const sent: { days?: number; ids?: string[] } = {};
  globalThis.fetch = async () => {
    throw new Error("Unexpected network request in old session cleanup test");
  };
  hoxiAPI.deleteOldSessions = async (days, ids) => {
    sent.days = days;
    sent.ids = ids;
    return result;
  };
  try {
    await run(sent);
  } finally {
    hoxiAPI.deleteOldSessions = originalDelete;
    globalThis.fetch = originalFetch;
    setWorkspace(reconcile(saved));
  }
}

describe("old session cleanup", () => {
  it("removes only the sessions the backend confirmed as deleted", async () => {
    await withCleanupFixture(
      { deleted: ["ses_old_a"], skipped: [{ id: "ses_old_b", reason: "worktreeDirty" }], freedBytes: 10, storage },
      async (sent) => {
        setWorkspace("sessions", reconcile([session("ses_old_a"), session("ses_old_b"), session("ses_keep")]));
        setWorkspace("activeSessionID", "ses_keep");
        const res = await deleteOldSessions(30, ["ses_old_a", "ses_old_b"]);
        assertDeepEqual(sent, { days: 30, ids: ["ses_old_a", "ses_old_b"] });
        assertDeepEqual(res.deleted, ["ses_old_a"]);
        assertDeepEqual(
          workspace.sessions.map((s) => s.id),
          ["ses_old_b", "ses_keep"],
          "a skipped session must stay in the sidebar"
        );
        assertEqual(workspace.activeSessionID, "ses_keep", "an untouched active session stays selected");
      }
    );
  });

  it("clears the selection when the active session was deleted and nothing remains", async () => {
    await withCleanupFixture({ deleted: ["ses_only"], skipped: [], freedBytes: 5, storage }, async () => {
      setWorkspace("sessions", reconcile([session("ses_only")]));
      setWorkspace("activeSessionID", "ses_only");
      await deleteOldSessions(60, ["ses_only"]);
      assertDeepEqual(workspace.sessions.map((s) => s.id), []);
      assertEqual(workspace.activeSessionID, null);
    });
  });

  it("removeSessionFromStore picks the same next session as nextActiveSessionCandidate", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      const s = (id: string, updated: number) =>
        ({ id, title: id, directory: "/tmp/p", time: { created: 1, updated } }) as SessionInfo;
      setWorkspace("sessions", reconcile([s("ses_del", 50), s("ses_a", 200), s("ses_b", 100)]));
      setWorkspace("sessionFinishedAt", { ses_b: 999 });
      const expected = nextActiveSessionCandidate("ses_del")?.id;
      assertEqual(removeSessionFromStore("ses_del")?.id, expected);
      assertEqual(expected, "ses_b");
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("keeps orphaned child conversations visible as roots once their parent is gone", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      const orphan = {
        ...session("ses_cld_fork"),
        parentID: "ses_deleted_root",
      };
      const orphanSub = {
        ...session("ses_cld_fork_sub"),
        parentID: "ses_cld_fork",
      };
      setWorkspace("sessions", reconcile([orphan, orphanSub]));
      setWorkspace("sessionIndex", reconcile({}));
      assertEqual(resolveParentSessionID("ses_cld_fork"), null);
      assertEqual(resolveRootSessionID("ses_cld_fork"), "ses_cld_fork");
      assertEqual(resolveRootSessionID("ses_cld_fork_sub"), "ses_cld_fork");
      const { roots, subagentsByRoot } = partitionCodeSessions(workspace.sessions);
      assertDeepEqual(roots.map((s) => s.id), ["ses_cld_fork"]);
      assertDeepEqual(
        (subagentsByRoot.get("ses_cld_fork") ?? []).map((s) => s.id),
        ["ses_cld_fork_sub"],
      );
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("purges session goal, interrupt state, and dismissed prompt keys on removeSessionFromStore", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace("sessions", reconcile([session("ses_drop"), session("ses_stay")]));
      writeSessionGoal("ses_drop", { objective: "Finish migration", status: "active", continuations: 0, updatedAt: 1 });
      writeSessionGoal("ses_stay", { objective: "Keep running", status: "active", continuations: 0, updatedAt: 1 });
      markSessionInterrupted("ses_drop", "stopped");
      markSessionInterrupted("ses_stay", "stopped");
      setWorkspace("dismissedPlanFollowups", { "ses_drop:msg_1": true, "ses_stay:msg_2": true });
      setWorkspace("dismissedSandboxMergePrompts", { "ses_drop:head_1": true, "ses_stay:head_2": true });

      removeSessionFromStore("ses_drop");

      assertEqual(sessionGoal("ses_drop"), undefined, "deleted session goal must be cleared");
      assertEqual(sessionGoal("ses_stay")?.objective, "Keep running", "other session goal stays intact");
      assertEqual(hasRecentSessionInterrupt("ses_drop"), false, "deleted session interrupt record must be cleared");
      assertEqual(hasRecentSessionInterrupt("ses_stay"), true, "other session interrupt record stays intact");
      assertDeepEqual(workspace.dismissedPlanFollowups, { "ses_stay:msg_2": true });
      assertDeepEqual(workspace.dismissedSandboxMergePrompts, { "ses_stay:head_2": true });
    } finally {
      clearSessionGoal("ses_drop");
      clearSessionGoal("ses_stay");
      setWorkspace(reconcile(saved));
    }
  });
});
