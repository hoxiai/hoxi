import { reconcile, unwrap } from "solid-js/store";
import { t } from "../i18n";
import { hoxiAPI, type CommitAndPushResponse } from "../services/api";
import type { SnapshotFileDiff } from "../services/sse-types";
import { commitModelSpec, executeTaskCommitAndPush, setCommitModelSpec } from "../store/git-commit";
import { sessionDirectory, setWorkspace, workspace } from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

let nextID = 0;

async function withCommitFixture(run: (fixture: {
  id: string;
  directory: string;
  diffs: SnapshotFileDiff[];
  calls: {
    status: Parameters<typeof hoxiAPI.getWorktreeStatus>[];
    diff: Parameters<typeof hoxiAPI.getSessionDiff>[];
    worktreeDiff: Parameters<typeof hoxiAPI.getWorktreeDiff>[];
    commit: Parameters<typeof hoxiAPI.commitWorktree>[];
    push: Parameters<typeof hoxiAPI.pushWorktree>[];
  };
  result: CommitAndPushResponse;
}) => Promise<void>) {
  const saved = structuredClone(unwrap(workspace));
  const originalAPI = { ...hoxiAPI };
  const originalSpec = commitModelSpec();
  const storageDescriptor = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  const originalFetch = globalThis.fetch;
  const values = new Map<string, string>();
  const storage: Storage = {
    get length() { return values.size; },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => { values.delete(key); },
    setItem: (key, value) => { values.set(key, String(value)); },
  };
  const id = `ses_git_commit_${++nextID}`;
  const directory = `/commit-test/.hoxi/worktrees/${id}`;
  const diffs: SnapshotFileDiff[] = [{
    file: "src/task.ts", before: "old\n", after: "new\n", status: "modified",
    additions: 1, deletions: 1,
  }];
  const calls = {
    status: [] as Parameters<typeof hoxiAPI.getWorktreeStatus>[],
    diff: [] as Parameters<typeof hoxiAPI.getSessionDiff>[],
    worktreeDiff: [] as Parameters<typeof hoxiAPI.getWorktreeDiff>[],
    commit: [] as Parameters<typeof hoxiAPI.commitWorktree>[],
    push: [] as Parameters<typeof hoxiAPI.pushWorktree>[],
  };
  const result: CommitAndPushResponse = { ok: true, commit: "fixture-commit", pushed: true };
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
  globalThis.fetch = async () => { throw new Error("Unexpected network request in git commit test"); };
  try {
    setCommitModelSpec("none");
    setWorkspace("worktreeBySession", reconcile({}));
    setWorkspace("messagesBySession", reconcile({}));
    setWorkspace("loadingMessagesBySession", reconcile({}));
    setWorkspace("aiBySession", reconcile({}));
    setWorkspace("diffsBySession", reconcile({ [id]: [{ ...diffs[0], file: "stale-cache.ts" }] }));
    // A background child avoids unrelated scheduled and display-diff refreshes.
    setWorkspace({ rootDir: "/unrelated-root", activeSessionID: "ses_other", sessions: [{
      id, parentID: "fixture-parent", title: "Commit fixture", directory,
      time: { created: 1, updated: 1 },
    }] });
    hoxiAPI.getWorktreeStatus = async (...args) => {
      calls.status.push(args);
      return {
        sessionID: args[0], directory: args[1] || "", isolated: true,
        dirty: calls.status.length === 1, changedFiles: 1,
        uncommittedLines: 2, additions: 1, deletions: 1,
      };
    };
    hoxiAPI.getSessionDiff = async (...args) => { calls.diff.push(args); return diffs; };
    hoxiAPI.getWorktreeDiff = async (...args) => { calls.worktreeDiff.push(args); return []; };
    hoxiAPI.commitWorktree = async (...args) => { calls.commit.push(args); return result; };
    hoxiAPI.pushWorktree = async (...args) => { calls.push.push(args); return result; };
    hoxiAPI.getMessages = async () => [];
    hoxiAPI.generateCommitMessage = async () => { throw new Error("Commit LLM must be disabled"); };
    await run({ id, directory, diffs, calls, result });
  } finally {
    Object.assign(hoxiAPI, originalAPI);
    setWorkspace(reconcile(saved));
    setCommitModelSpec(originalSpec);
    globalThis.fetch = originalFetch;
    if (storageDescriptor) Object.defineProperty(globalThis, "localStorage", storageDescriptor);
    else Reflect.deleteProperty(globalThis, "localStorage");
  }
}

async function assertRejects(run: () => Promise<unknown>, expected: string) {
  let error: unknown;
  try { await run(); } catch (caught) { error = caught; }
  assertTrue(error instanceof Error, "operation must reject");
  assertEqual((error as Error).message, expected);
}

describe("task commit and push", () => {
  it("uses the background session directory and authoritative snapshots, not root or display diffs", async () => {
    await withCommitFixture(async ({ id, directory, diffs, calls, result }) => {
      assertEqual(sessionDirectory(id), directory);
      assertEqual(await executeTaskCommitAndPush(id), result);
      assertDeepEqual(calls.status, [[id, directory], [id, directory], ["__root__", "/unrelated-root"]]);
      assertDeepEqual(calls.diff, [[id, directory]]);
      assertDeepEqual(calls.worktreeDiff, []);
      assertEqual(calls.commit.length, 1);
      assertEqual(calls.commit[0][0], id);
      assertEqual(calls.commit[0][1], "feat(src): update task");
      assertDeepEqual(calls.commit[0][2], { push: true, directory, sessionDiffs: diffs });
      assertEqual(calls.commit[0][2]?.sessionDiffs, diffs, "must forward the original snapshot array");
      assertDeepEqual(calls.push, []);
    });
  });

  it("forwards an empty snapshot array without falling back to worktree diffs", async () => {
    await withCommitFixture(async ({ id, diffs, calls }) => {
      diffs.length = 0;
      await executeTaskCommitAndPush(id);
      assertEqual(calls.commit.length, 1);
      assertEqual(calls.commit[0][2]?.sessionDiffs, diffs);
      assertDeepEqual(calls.commit[0][2]?.sessionDiffs, []);
      assertDeepEqual(calls.diff, [[id, sessionDirectory(id)]]);
      assertDeepEqual(calls.worktreeDiff, []);
    });
  });

  it("supports commit-only with a trimmed custom message and original snapshots", async () => {
    await withCommitFixture(async ({ id, directory, diffs, calls }) => {
      await executeTaskCommitAndPush(id, { push: false, customMessage: "  fix: task  " });
      assertDeepEqual(calls.commit, [[id, "fix: task", { push: false, directory, sessionDiffs: diffs }]]);
      assertEqual(calls.commit[0][2]?.sessionDiffs, diffs);
      assertDeepEqual(calls.push, []);
      assertDeepEqual(calls.worktreeDiff, []);
    });
  });

  for (const field of ["before", "after", "status"] as const) {
    it(`rejects snapshots missing ${field} without committing or pushing`, async () => {
      await withCommitFixture(async ({ id, diffs, calls }) => {
        delete diffs[0][field];
        await assertRejects(() => executeTaskCommitAndPush(id), t("timeline.gitSessionDiffUnavailable"));
        assertEqual(calls.diff.length, 1);
        assertDeepEqual(calls.commit, []);
        assertDeepEqual(calls.push, []);
        assertDeepEqual(calls.worktreeDiff, []);
      });
    });
  }

  it("propagates snapshot fetch failures without committing, pushing or falling back", async () => {
    await withCommitFixture(async ({ id, calls }) => {
      hoxiAPI.getSessionDiff = async () => { throw new Error("snapshot fetch failed"); };
      await assertRejects(() => executeTaskCommitAndPush(id), "snapshot fetch failed");
      assertDeepEqual(calls.commit, []);
      assertDeepEqual(calls.push, []);
      assertDeepEqual(calls.worktreeDiff, []);
    });
  });

  it("rejects empty snapshots for commit-only without any fallback", async () => {
    await withCommitFixture(async ({ id, diffs, calls }) => {
      diffs.length = 0;
      await assertRejects(() => executeTaskCommitAndPush(id, { push: false }), t("timeline.gitSessionNothingToCommit"));
      assertDeepEqual(calls.commit, []);
      assertDeepEqual(calls.push, []);
      assertDeepEqual(calls.worktreeDiff, []);
    });
  });

  it("rejects absent sessions, root scope and runner sessions before calling APIs", async () => {
    await withCommitFixture(async ({ id, calls }) => {
      setWorkspace("activeSessionID", null);
      for (const sessionID of [undefined, null, "", "__root__"]) {
        await assertRejects(() => executeTaskCommitAndPush(sessionID), t("timeline.gitSessionRequired"));
      }
      for (const sessionID of ["ses_agy_fixture", "ses_cld_fixture"]) {
        await assertRejects(() => executeTaskCommitAndPush(sessionID), t("timeline.gitSessionDiffUnavailable"));
      }
      setWorkspace("sessions", (session) => session.id === id, "runtime", "claude-code");
      await assertRejects(() => executeTaskCommitAndPush(id), t("timeline.gitSessionDiffUnavailable"));
      assertDeepEqual(calls, { status: [], diff: [], worktreeDiff: [], commit: [], push: [] });
    });
  });

  it("only pushes a clean worktree using the session directory", async () => {
    await withCommitFixture(async ({ id, directory, calls, result }) => {
      hoxiAPI.getWorktreeStatus = async (sessionID, dir) => ({
        sessionID, directory: dir || "", isolated: true, dirty: false,
        changedFiles: 0, uncommittedLines: 0, additions: 0, deletions: 0,
      });
      assertEqual(await executeTaskCommitAndPush(id), result);
      assertDeepEqual(calls.push, [[id, directory]]);
      assertDeepEqual(calls.commit, []);
      assertDeepEqual(calls.diff, []);
      assertDeepEqual(calls.worktreeDiff, []);
      await assertRejects(() => executeTaskCommitAndPush(id, { push: false }), t("timeline.gitSessionNothingToCommit"));
      assertDeepEqual(calls.push, [[id, directory]]);
      assertDeepEqual(calls.commit, []);
    });
  });
});
