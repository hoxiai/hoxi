import { reconcile, unwrap } from "solid-js/store";
import { t } from "../i18n";
import { hoxiAPI, type CommitAndPushResponse } from "../services/api";
import type { SnapshotFileDiff } from "../services/sse-types";
import { canQuickCommitSession, commitModelSpec, executeTaskCommitAndPush, generateSmartCommitMessage, setCommitModelSpec, uncommittedSessionFiles } from "../store/git-commit";
import { sessionDirectory, setGitAttribution, setWorkspace, workspace } from "../store/workspace";
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
    send: Parameters<typeof hoxiAPI.sendPromptAsync>[];
  };
}) => Promise<void>) {
  const saved = structuredClone(unwrap(workspace));
  const originalAPI = { ...hoxiAPI };
  const originalSpec = commitModelSpec();
  const storageDescriptor = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  const originalFetch = globalThis.fetch;
  const originalTimeout = globalThis.setTimeout;
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
    send: [] as Parameters<typeof hoxiAPI.sendPromptAsync>[],
  };
  const result: CommitAndPushResponse = { ok: true, commit: "fixture-commit", pushed: true };
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
  // Follow-up refreshes must not outlive the fixture's API mocks.
  globalThis.setTimeout = (() => 0) as unknown as typeof setTimeout;
  globalThis.fetch = async () => { throw new Error("Unexpected network request in git commit test"); };
  try {
    setCommitModelSpec("none");
    setWorkspace("worktreeBySession", reconcile({}));
    setWorkspace("messagesBySession", reconcile({}));
    setWorkspace("loadingMessagesBySession", reconcile({}));
    setWorkspace("aiBySession", reconcile({
      [id]: { model: { providerID: "background-provider", modelID: "background-model" }, agent: "plan", effort: "high" },
      ses_other: { model: { providerID: "active-provider", modelID: "active-model" }, agent: "review", effort: "low" },
    }));
    setWorkspace("defaultAI", { model: { providerID: "default-provider", modelID: "default-model" }, agent: "plan", effort: "medium" });
    setWorkspace("providers", []);
    setWorkspace("hoxiProviders", reconcile({}));
    setWorkspace("statusBySession", reconcile({}));
    setWorkspace("queuedPromptsBySession", reconcile({}));
    setWorkspace("diffsBySession", reconcile({ [id]: [{ ...diffs[0], file: "stale-cache.ts" }] }));
    // A prior user turn and a non-placeholder title avoid title-generation requests.
    setWorkspace({ rootDir: "/unrelated-root", activeSessionID: "ses_other", sessions: [{
      id, parentID: "fixture-parent", title: "Commit fixture", directory,
      time: { created: 1, updated: 1 },
    }] });
    setWorkspace("messagesBySession", id, [{
      info: { id: `msg_${id}`, sessionID: id, role: "user", time: { created: 1 } }, parts: [],
    }]);
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
    hoxiAPI.sendPromptAsync = async (...args) => { calls.send.push(args); };
    hoxiAPI.getMessages = async () => [];
    hoxiAPI.generateCommitMessage = async () => { throw new Error("Commit LLM must be disabled"); };
    await run({ id, directory, diffs, calls });
    assertDeepEqual(calls.status, [], "dispatch must not inspect worktree status directly");
    assertDeepEqual(calls.diff, [], "dispatch must not fetch session snapshots");
    assertDeepEqual(calls.commit, [], "dispatch must not commit directly");
    assertDeepEqual(calls.push, [], "dispatch must not push directly");
  } finally {
    Object.assign(hoxiAPI, originalAPI);
    setWorkspace(reconcile(saved));
    setCommitModelSpec(originalSpec);
    globalThis.fetch = originalFetch;
    globalThis.setTimeout = originalTimeout;
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
  it("dispatches a localized build task using the background session directory and effort", async () => {
    await withCommitFixture(async ({ id, directory, calls }) => {
      const activeAI = structuredClone(unwrap(workspace.aiBySession.ses_other));
      const backgroundAI = structuredClone(unwrap(workspace.aiBySession[id]));
      assertEqual(sessionDirectory(id), directory);
      assertEqual(await executeTaskCommitAndPush(id), id, "returns dispatch ID, not a Git result");
      assertEqual(calls.send.length, 1);
      const [target, prompt, options] = calls.send[0];
      assertEqual(target, id);
      assertEqual(prompt, [
        t("timeline.gitAgentCommitPrompt", { directory: JSON.stringify(directory) }),
        t("timeline.gitAgentCommitPushPrompt"),
        t("timeline.gitAgentCommitAttributionPrompt"),
      ].join("\n\n"));
      assertEqual(options?.directory, directory);
      assertDeepEqual(options?.model, backgroundAI.model);
      assertEqual(options?.variant, "high");
      assertEqual(options?.agent, "build");
      assertEqual(workspace.activeSessionID, "ses_other");
      assertDeepEqual(workspace.aiBySession.ses_other, activeAI);
      assertDeepEqual(workspace.aiBySession[id], backgroundAI, "build override must not replace the saved agent");
      assertEqual(workspace.statusBySession[id]?.type, "busy");
      const optimistic = workspace.messagesBySession[id].at(-1)!;
      assertEqual(optimistic.info.agent, "build");
      assertDeepEqual(optimistic.parts.map((part) => part.type === "text" ? part.text : ""), [prompt]);
      assertDeepEqual(calls.worktreeDiff, []);
    });
  });

  it("temporarily overrides an OpenCode commit task with the configured cheap model", async () => {
    await withCommitFixture(async ({ id, calls }) => {
      setWorkspace("providers", [{
        id: "cheap-provider", name: "Cheap", npm: "cheap", models: {
          "cheap-model": { id: "cheap-model", name: "Cheap Model", reasoning: false },
        },
      }]);
      setCommitModelSpec("");
      setWorkspace("globalConfig", { small_model: "cheap-provider/cheap-model" });
      setWorkspace("sessions", (session) => session.id === id, "runtime", "opencode");
      await executeTaskCommitAndPush(id);
      assertDeepEqual(calls.send[0][2]?.model, {
        providerID: "cheap-provider", modelID: "cheap-model",
      });
      assertEqual(workspace.aiBySession[id].model?.providerID, "background-provider");
    });
  });

  for (const snapshot of ["empty", "missing", "before", "after", "status"] as const) {
    it(`dispatches despite ${snapshot} snapshots without inspecting Git APIs`, async () => {
      await withCommitFixture(async ({ id, diffs, calls }) => {
        if (snapshot === "empty") diffs.length = 0;
        else if (snapshot !== "missing") delete diffs[0][snapshot];
        setWorkspace("diffsBySession", reconcile(snapshot === "missing" ? {} : { [id]: diffs }));
        hoxiAPI.getSessionDiff = async (...args) => {
          calls.diff.push(args);
          throw new Error("snapshot fetch unavailable");
        };
        assertEqual(await executeTaskCommitAndPush(id, { push: false }), id);
        assertEqual(calls.send.length, 1);
        assertDeepEqual(calls.worktreeDiff, []);
      });
    });
  }

  it("rejects absent sessions, root scope and missing directories before dispatch", async () => {
    await withCommitFixture(async ({ id, calls }) => {
      setWorkspace("activeSessionID", null);
      for (const sessionID of [undefined, null, "", "__root__"]) {
        await assertRejects(() => executeTaskCommitAndPush(sessionID), t("timeline.gitSessionRequired"));
      }
      setWorkspace("rootDir", "");
      setWorkspace("sessions", (session) => session.id === id, "directory", "");
      await assertRejects(() => executeTaskCommitAndPush(id), t("timeline.gitSessionRequired"));
      assertDeepEqual(calls.send, []);
      assertDeepEqual(calls.worktreeDiff, []);
    });
  });

  for (const runtime of ["claude-code", "antigravity"] as const) {
    it(`allows ${runtime} runner sessions to dispatch a build task`, async () => {
      await withCommitFixture(async ({ id, directory, calls }) => {
        setWorkspace("sessions", (session) => session.id === id, "runtime", runtime);
        setWorkspace("aiBySession", id, "model", { providerID: runtime, modelID: "runner-model" });
        assertEqual(await executeTaskCommitAndPush(id), id);
        assertEqual(calls.send.length, 1);
        assertEqual(calls.send[0][2]?.runtime, runtime);
        assertEqual(calls.send[0][2]?.agent, "build");
        assertEqual(calls.send[0][2]?.directory, directory);
      });
    });
  }

  it("uses commit-only instructions and JSON-encodes a trimmed custom message", async () => {
    await withCommitFixture(async ({ id, directory, calls }) => {
      const message = 'fix: handle "quoted" paths\n\nKeep \\ escapes';
      assertEqual(await executeTaskCommitAndPush(id, { push: false, customMessage: ` \n${message}\t ` }), id);
      assertEqual(calls.send.length, 1);
      assertEqual(calls.send[0][1], [
        t("timeline.gitAgentCommitPrompt", { directory: JSON.stringify(directory) }),
        t("timeline.gitAgentCommitOnlyPrompt"),
        t("timeline.gitAgentCommitMessagePrompt", { message: JSON.stringify(message) }),
        t("timeline.gitAgentCommitAttributionPrompt"),
      ].join("\n\n"));
    });
  });

  it("drops the attribution instruction when commit attribution is off", async () => {
    await withCommitFixture(async ({ id, directory, calls }) => {
      setGitAttribution("enabled", false);
      try {
        assertEqual(await executeTaskCommitAndPush(id, { push: false }), id);
      } finally {
        setGitAttribution("enabled", true);
      }
      assertEqual(calls.send[0][1], [
        t("timeline.gitAgentCommitPrompt", { directory: JSON.stringify(directory) }),
        t("timeline.gitAgentCommitOnlyPrompt"),
      ].join("\n\n"));
      assertEqual(String(calls.send[0][1]).includes("code@hoxi.ai"), false);
    });
  });

  it("omits a blank custom message and supports explicit push", async () => {
    await withCommitFixture(async ({ id, directory, calls }) => {
      assertEqual(await executeTaskCommitAndPush(id, { push: true, customMessage: " \n\t " }), id);
      assertEqual(calls.send.length, 1);
      assertEqual(calls.send[0][1], [
        t("timeline.gitAgentCommitPrompt", { directory: JSON.stringify(directory) }),
        t("timeline.gitAgentCommitPushPrompt"),
        t("timeline.gitAgentCommitAttributionPrompt"),
      ].join("\n\n"));
    });
  });

  it("propagates dispatch failures and rolls back the optimistic turn", async () => {
    await withCommitFixture(async ({ id, calls }) => {
      const messages = structuredClone(unwrap(workspace.messagesBySession[id]));
      hoxiAPI.sendPromptAsync = async (...args) => {
        calls.send.push(args);
        throw new Error("dispatch failed");
      };
      await assertRejects(() => executeTaskCommitAndPush(id), "dispatch failed");
      assertEqual(calls.send.length, 1);
      assertDeepEqual(workspace.messagesBySession[id], messages);
      assertEqual(workspace.statusBySession[id]?.type, "idle");
      assertDeepEqual(calls.worktreeDiff, []);
    });
  });

  it("passes sessionDirectory(id) when generateSmartCommitMessage fetches uncached worktree diffs", async () => {
    await withCommitFixture(async ({ id, directory, diffs, calls }) => {
      setWorkspace("diffsBySession", reconcile({}));
      hoxiAPI.getWorktreeDiff = async (...args) => {
        calls.worktreeDiff.push(args);
        return diffs;
      };
      const msg = await generateSmartCommitMessage(id);
      assertEqual(msg, "feat(src): update task");
      assertDeepEqual(calls.worktreeDiff, [[id, directory]]);
    });
  });
});

describe("uncommitted session commit gate", () => {
  it("tracks real worktree diffs and only allows committing while idle with pending files", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      const id = `ses_gate_${++nextID}`;
      const diffs: SnapshotFileDiff[] = [{
        file: "src/a.ts", before: "", after: "x\n", status: "added",
        additions: 1, deletions: 0,
      }];
      setWorkspace("diffsBySession", reconcile({ [id]: diffs }));
      setWorkspace("statusBySession", reconcile({ [id]: { type: "idle" } }));

      assertDeepEqual(uncommittedSessionFiles(id), diffs, "the chip reads real worktree diffs");
      assertEqual(canQuickCommitSession(id), true, "idle session with pending files can commit");
      assertEqual(canQuickCommitSession(""), false, "no session cannot commit");
      assertEqual(canQuickCommitSession("ses_absent"), false, "session without diffs cannot commit");

      setWorkspace("statusBySession", id, { type: "busy" });
      assertEqual(canQuickCommitSession(id), false, "busy session cannot commit");

      setWorkspace("statusBySession", id, { type: "idle" });
      setWorkspace("diffsBySession", id, []);
      assertEqual(canQuickCommitSession(id), false, "no pending files cannot commit");
    } finally {
      setWorkspace(reconcile(saved));
    }
  });
});
