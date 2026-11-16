import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI, type WorktreeMeta } from "../services/api";
import { appSettings, setAppSettings } from "../store/settings";
import {
  activateSurfaceMode,
  getInitialCodeProjectDir,
  getRememberedCodeSurface,
  indexSession,
  isWorkSurfacePath,
  loadProjects,
  openDiffInInspector,
  resolveDefaultProjectDir,
  resolveSessionSurface,
  saveWorkspaceProfile,
  sessionBelongsToProject,
  sessionSurfaceRevision,
  setDefaultWorkspaceDir,
  setRememberedCodeSurface,
  setSurfaceMode,
  setWorkspace,
   surfaceMode,
  switchWorkspace,
  unrecordWorkWorkspaceDir,
  visibleSavedWorkspaces,
  workspace,
} from "../store/workspace";
import { newIsolatedSession, switchActiveSessionSandboxMode } from "../store/session-sandbox";
import { loadSessionMessages, selectSession } from "../store/workspace-sessions";
import { assertEqual, assertTrue, describe, it } from "./test-harness";
import { sessionDirectory } from "../store/session-query";
import {
  applySessionDiffEvent,
  cancelScheduledWorktreeStatus,
  refreshSessionDiff,
  refreshWorktreeStatus,
} from "../store/session-diffs";
import { submitSessionPrompt } from "../store/session-goal";

let nextID = 0;

async function withSessionFixture(run: (id: string, meta: WorktreeMeta) => Promise<void>) {
  const saved = structuredClone(unwrap(workspace));
  const sandboxMode = appSettings.defaultSandboxMode;
  const originalAPI = { ...hoxiAPI };
  const id = `ses_isolation_test_${++nextID}`;
  const meta: WorktreeMeta = {
    sessionID: id, isolated: true, directory: `/repo/.hoxi/worktrees/${id}`, createdAt: "test",
  };
  setWorkspace({ rootDir: "/repo", activeSessionID: "existing", sessions: [],
    worktreeBySession: {}, messagesBySession: {}, statusBySession: {}, diffsBySession: {},
    worktreeDiffsBySession: {} });
  setAppSettings("defaultSandboxMode", "worktree");
  hoxiAPI.createSession = async () => ({
    id, title: "Isolation test", directory: "/repo", time: { created: 1, updated: 1 },
  });
  hoxiAPI.createWorktree = async () => meta;
  hoxiAPI.deleteSession = async () => {};
  try {
    await run(id, meta);
  } finally {
    cancelScheduledWorktreeStatus(id);
    Object.assign(hoxiAPI, originalAPI);
    setAppSettings("defaultSandboxMode", sandboxMode);
    setWorkspace(reconcile(saved));
  }
}

async function rejection(task: Promise<unknown>): Promise<Error> {
  let error: unknown;
  try { await task; } catch (caught) { error = caught; }
  assertTrue(error instanceof Error, "operation must reject instead of falling back to the root workspace");
  return error as Error;
}

describe("session worktree isolation", () => {
  it("shows shared directory Git changes separately from empty session changes and clears them when clean", async () => {
    await withSessionFixture(async (id) => {
      setWorkspace({ activeSessionID: id, sessions: [{
        id, title: "test", directory: "/repo", time: { created: 1, updated: 1 },
      }] });
      let dirty = true;
      hoxiAPI.getWorktreeStatus = async (sessionID, directory) => {
        assertEqual(sessionID, id);
        assertEqual(directory, "/repo");
        return { sessionID: id, directory: "/repo", isolated: false, dirty,
          changedFiles: dirty ? 1 : 0, uncommittedLines: 2, additions: 2, deletions: 0 };
      };
      hoxiAPI.getSessionDiff = async () => [];
      hoxiAPI.getWorktreeDiff = async (sessionID, directory) => {
        assertEqual(sessionID, id);
        assertEqual(directory, "/repo");
        return [{ file: "manual.ts", additions: 2, deletions: 0 }];
      };
      await refreshWorktreeStatus(id);
      assertEqual(workspace.worktreeDiffsBySession[id][0].file, "manual.ts");
      assertEqual(workspace.diffsBySession[id].length, 0);
      applySessionDiffEvent(id, []);
      assertEqual(workspace.worktreeDiffsBySession[id].length, 1);
      dirty = false;
      await refreshWorktreeStatus(id);
      assertEqual(workspace.worktreeDiffsBySession[id].length, 0);
    });
  });

  it("recovers empty session diffs from a dirty isolated worktree, but not a shared directory", async () => {
    await withSessionFixture(async (id, meta) => {
      setWorkspace({ activeSessionID: id, worktreeBySession: { [id]: { ...meta, dirty: true } } });
      hoxiAPI.getSessionDiff = async () => [];
      let gitDiffCalls = 0;
      hoxiAPI.getWorktreeDiff = async (sessionID, directory) => {
        assertEqual(sessionID, id);
        assertEqual(directory, meta.directory);
        gitDiffCalls++;
        return [{ file: "owned.ts", additions: 2, deletions: 1 }];
      };
      await refreshSessionDiff(id);
      assertEqual(workspace.diffsBySession[id][0].file, "owned.ts");
      setWorkspace("worktreeBySession", id, "isolated", false);
      await refreshSessionDiff(id);
      assertEqual(gitDiffCalls, 1);
      assertEqual(workspace.diffsBySession[id].length, 0);
    });
  });

  it("ignores stale Git diff responses after a newer refresh reports a clean directory", async () => {
    await withSessionFixture(async (id, meta) => {
      setWorkspace({ activeSessionID: id, worktreeBySession: { [id]: meta } });
      let dirty = true;
      hoxiAPI.getWorktreeStatus = async () => ({
        sessionID: id, directory: meta.directory, isolated: true, dirty,
        changedFiles: 1, uncommittedLines: 1, additions: 1, deletions: 0,
      });
      hoxiAPI.getSessionDiff = async () => [];
      let resolveDiff!: (diffs: Awaited<ReturnType<typeof hoxiAPI.getWorktreeDiff>>) => void;
      hoxiAPI.getWorktreeDiff = () => new Promise((resolve) => { resolveDiff = resolve; });
      const pending = refreshWorktreeStatus(id);
      await Promise.resolve();
      dirty = false;
      await refreshWorktreeStatus(id);
      resolveDiff([{ file: "stale.ts", additions: 1, deletions: 0 }]);
      await pending;
      assertEqual(workspace.worktreeDiffsBySession[id].length, 0);
      assertEqual(workspace.worktreeBySession[id].dirty, false);
    });
  });

  it("does not let an old REST response overwrite a newer SSE diff, including an empty diff", async () => {
    await withSessionFixture(async (id) => {
      let resolveDiff!: (diffs: Awaited<ReturnType<typeof hoxiAPI.getSessionDiff>>) => void;
      hoxiAPI.getSessionDiff = () => new Promise((resolve) => { resolveDiff = resolve; });
      const pending = refreshSessionDiff(id);
      applySessionDiffEvent(id, [{ file: "latest.ts", additions: 1, deletions: 0 }]);
      resolveDiff([]);
      await pending;
      assertEqual(workspace.diffsBySession[id][0].file, "latest.ts");
      const nextPending = refreshSessionDiff(id);
      applySessionDiffEvent(id, []);
      resolveDiff([{ file: "stale.ts", additions: 1, deletions: 0 }]);
      await nextPending;
      assertEqual(workspace.diffsBySession[id].length, 0);
    });
  });

  it("keeps the newest REST diff when overlapping requests finish out of order", async () => {
    await withSessionFixture(async (id) => {
      const resolvers: Array<(diffs: Awaited<ReturnType<typeof hoxiAPI.getSessionDiff>>) => void> = [];
      hoxiAPI.getSessionDiff = () => new Promise((resolve) => { resolvers.push(resolve); });
      const first = refreshSessionDiff(id);
      const second = refreshSessionDiff(id);
      resolvers[1]([{ file: "latest.ts", additions: 1, deletions: 0 }]);
      await second;
      resolvers[0]([]);
      await first;
      assertEqual(workspace.diffsBySession[id][0].file, "latest.ts");
    });
  });

  it("opens changed-file statistics as a diff and preserves the clicked patch without changing live diffs", async () => {
    await withSessionFixture(async (id) => {
      setWorkspace({ activeSessionID: id, diffsBySession: { [id]: [] }, inspectorOpen: false });
      const diff = { file: "src/example.ts", additions: 1, deletions: 1,
        patch: "--- a/src/example.ts\n+++ b/src/example.ts\n@@ -1 +1 @@\n-old\n+new\n" };
      openDiffInInspector(diff);
      assertEqual(workspace.inspectorTab, "diffs");
      assertEqual(workspace.inspectorOpen, true);
      assertEqual(workspace.inspectedDiffFile, diff.file);
      assertEqual(workspace.inspectedFileDiff?.patch, diff.patch);
      assertEqual(workspace.diffsBySession[id].length, 0);
      openDiffInInspector("another.ts");
      assertEqual(workspace.inspectedFileDiff, null);
      assertEqual(workspace.inspectedDiffFile, "another.ts");
    });
  });

  it("keeps each session's files and counts separate from shared Git changes", async () => {
    await withSessionFixture(async (id) => {
      const otherID = `${id}_other`;
      setWorkspace("sessions", [id, otherID].map((sessionID) => ({
        id: sessionID, parentID: "parent", title: "test", directory: "/other-repo",
        time: { created: 1, updated: 1 },
      })));
      hoxiAPI.getMessages = async () => [];
      hoxiAPI.getSessionDiff = async (sessionID, directory) => {
        assertEqual(directory, "/other-repo");
        return [{ file: sessionID === id ? "a.ts" : "b.ts", additions: sessionID === id ? 3 : 7, deletions: 0 }];
      };
      let gitDiffCalls = 0;
      hoxiAPI.getWorktreeDiff = async () => { gitDiffCalls++; return [
        { file: "manual.ts", additions: 100, deletions: 50 },
      ]; };
      await selectSession(id);
      await selectSession(otherID);
      assertEqual(workspace.diffsBySession[id].length, 1);
      assertEqual(workspace.diffsBySession[id][0].file, "a.ts");
      assertEqual(workspace.diffsBySession[id][0].additions, 3);
      assertEqual(workspace.diffsBySession[otherID][0].file, "b.ts");
      assertEqual(workspace.diffsBySession[otherID][0].additions, 7);
      assertEqual(gitDiffCalls, 0);
    });
  });

  it("scopes status requests and preserves session changes when Git is clean", async () => {
    await withSessionFixture(async (id) => {
      setWorkspace({ activeSessionID: id, sessions: [{
        id, title: "test", directory: "/other-repo", time: { created: 1, updated: 1 },
      }] });
      hoxiAPI.getWorktreeStatus = async (sessionID, directory) => {
        assertEqual(sessionID, id);
        assertEqual(directory, "/other-repo");
        return { sessionID: id, directory: "/other-repo", isolated: false, dirty: false,
          changedFiles: 0, uncommittedLines: 0, additions: 0, deletions: 0 };
      };
      hoxiAPI.getSessionDiff = async () => [{ file: "owned.ts", additions: 0, deletions: 4 }];
      await refreshWorktreeStatus(id);
      assertEqual(sessionDirectory(id), "/other-repo");
      assertEqual(workspace.diffsBySession[id][0].deletions, 4);
      hoxiAPI.getMessages = async () => [];
      // Child sessions do not schedule a background Git status refresh.
      setWorkspace("sessions", 0, "parentID", "parent");
      hoxiAPI.getSessionDiff = async () => [];
      await loadSessionMessages(id);
      assertEqual(workspace.diffsBySession[id].length, 0);
    });
  });

  it("clears the previous session's file preview when selecting another session", async () => {
    await withSessionFixture(async (id) => {
      setWorkspace({
        inspectedFilePath: "previous.ts", inspectedFileLine: 5,
        inspectedFileDiff: { file: "previous.ts", additions: 9, deletions: 2 },
        inspectedDiffFile: "previous.ts", activeInspectorFolder: "/previous",
        sessions: [{ id, parentID: "parent", title: "test", directory: "/repo", time: { created: 1, updated: 1 } }],
      });
      hoxiAPI.getMessages = async () => [];
      hoxiAPI.getSessionDiff = async () => [];
      await selectSession(id);
      assertEqual(workspace.inspectedFileDiff, null);
      assertEqual(workspace.inspectedFilePath, null);
      assertEqual(workspace.inspectedDiffFile, null);
      assertEqual(workspace.activeInspectorFolder, null);
    });
  });

  it("rejects failed provisioning, removes the incomplete session and never dispatches a prompt", async () => {
    await withSessionFixture(async (id) => {
      let callbackCount = 0;
      let deleteDirectory: string | undefined;
      let promptCount = 0;
      hoxiAPI.createWorktree = async () => { throw new Error("worktree creation failed"); };
      hoxiAPI.deleteSession = async (deletedID, directory) => {
        assertEqual(deletedID, id);
        deleteDirectory = directory;
      };
      hoxiAPI.sendPromptAsync = async () => { promptCount++; };
      await rejection(newIsolatedSession(undefined, { onSessionCreated: () => { callbackCount++; } }));
      assertEqual(deleteDirectory, "/repo");
      assertEqual(callbackCount, 0);
      assertEqual(workspace.activeSessionID, "existing");
      assertEqual(workspace.sessions.some((session) => session.id === id), false);
      assertEqual(workspace.messagesBySession[id], undefined);
      assertTrue(Boolean(workspace.sessionSetupError), "show the setup failure to the user");
      await rejection(submitSessionPrompt("must not reach the root", id));
      assertEqual(promptCount, 0);
    });
  });

  it("rejects non-isolated worktree responses such as a non-Git workspace", async () => {
    await withSessionFixture(async (id, meta) => {
      hoxiAPI.createWorktree = async () => ({ ...meta, isolated: false, directory: "/repo" });
      await rejection(newIsolatedSession(undefined, { isolated: true }));
      assertEqual(workspace.worktreeBySession[id], undefined);
      assertEqual(workspace.activeSessionID, "existing");
    });
  });

  it("keeps a failed first prompt out of the timeline and never sends it to the main workspace", async () => {
    await withSessionFixture(async (id) => {
      setWorkspace("activeSessionID", null);
      let promptCount = 0;
      hoxiAPI.createWorktree = async () => { throw new Error("checkout failed"); };
      hoxiAPI.sendPromptAsync = async () => { promptCount++; };
      await rejection(submitSessionPrompt("edit the code"));
      assertEqual(promptCount, 0);
      assertEqual(workspace.messagesBySession[id], undefined);
      assertEqual(workspace.statusBySession[id], undefined);
      assertEqual(workspace.activeSessionID, null);
    });
  });

  it("keeps the originating workspace and isolation choice if the user switches projects while creating", async () => {
    await withSessionFixture(async (id, meta) => {
      const session = await hoxiAPI.createSession();
      let resolveSession!: (value: typeof session) => void;
      hoxiAPI.createSession = () => new Promise((resolve) => { resolveSession = resolve; });
      let setupDirectory: string | undefined;
      hoxiAPI.createWorktree = async (_id, directory) => { setupDirectory = directory; return meta; };
      const creation = newIsolatedSession();
      setWorkspace({ rootDir: "/other-repo", activeSessionID: "other-session" });
      setAppSettings("defaultSandboxMode", "local");
      resolveSession(session);
      assertEqual(await creation, id);
      assertEqual(setupDirectory, "/repo");
      assertEqual(sessionDirectory(id), meta.directory);
      assertEqual(workspace.activeSessionID, "other-session");
      assertEqual(workspace.sessions.some((session) => session.id === id), false);
    });
  });

  it("publishes the session and invokes its callback only after the isolated directory is ready", async () => {
    await withSessionFixture(async (id, meta) => {
      let resolveSetup!: (value: WorktreeMeta) => void;
      let started!: () => void;
      const setupStarted = new Promise<void>((resolve) => { started = resolve; });
      const setup = new Promise<WorktreeMeta>((resolve) => { resolveSetup = resolve; });
      const calls: Array<[string, string | undefined]> = [];
      hoxiAPI.createWorktree = (_id, directory) => {
        calls.push([_id, directory]);
        started();
        return setup;
      };
      let callbackDirectory: string | undefined;
      const creation = newIsolatedSession(undefined, {
        onSessionCreated: (sessionID) => { callbackDirectory = sessionDirectory(sessionID); },
      });
      await setupStarted;
      const activeWhilePending = workspace.activeSessionID;
      const publishedWhilePending = workspace.sessions.some((session) => session.id === id);
      const callbackWhilePending = callbackDirectory;
      resolveSetup(meta);
      assertEqual(await creation, id);
      assertEqual(activeWhilePending, "existing");
      assertEqual(publishedWhilePending, false);
      assertEqual(callbackWhilePending, undefined);
      assertEqual(callbackDirectory, meta.directory);
      assertEqual(calls[0][1], "/repo");
      assertEqual(sessionDirectory(id), meta.directory);
    });
  });

  it("blocks concurrent prompts while provisioning and propagates a provisioning failure", async () => {
    await withSessionFixture(async (id) => {
      let failSetup!: (error: Error) => void;
      let started!: () => void;
      const setupStarted = new Promise<void>((resolve) => { started = resolve; });
      const setup = new Promise<WorktreeMeta>((_resolve, reject) => { failSetup = reject; });
      hoxiAPI.createWorktree = () => { started(); return setup; };
      let promptCount = 0;
      hoxiAPI.sendPromptAsync = async () => { promptCount++; };
      const creation = rejection(newIsolatedSession());
      await setupStarted;
      // The global SSE stream can expose the backend session before provisioning finishes.
      setWorkspace("sessions", [await hoxiAPI.createSession()]);
      const submission = rejection(submitSessionPrompt("must wait for isolation", id));
      await Promise.resolve();
      const sentWhilePending = promptCount;
      const optimisticWhilePending = workspace.messagesBySession[id];
      failSetup(new Error("checkout failed"));
      await Promise.all([creation, submission]);
      assertEqual(sentWhilePending, 0);
      assertEqual(optimisticWhilePending, undefined);
      assertEqual(promptCount, 0);
    });
  });

  it("preserves the previous sandbox and reports errors when enabling isolation fails", async () => {
    await withSessionFixture(async (id) => {
      setWorkspace("activeSessionID", id);
      setWorkspace("worktreeBySession", id, {
        sessionID: id, isolated: false, directory: "/repo", createdAt: "test",
      });
      hoxiAPI.createWorktree = async () => { throw new Error("checkout failed"); };
      await rejection(switchActiveSessionSandboxMode("worktree"));
      assertEqual(workspace.worktreeBySession[id].isolated, false);
      assertEqual(sessionDirectory(id), "/repo");
      assertTrue(Boolean(workspace.sessionSetupError));
    });
  });

  it("does not remove the worktree of a running task", async () => {
    await withSessionFixture(async (id, meta) => {
      setWorkspace("activeSessionID", id);
      setWorkspace("worktreeBySession", id, meta);
      setWorkspace("statusBySession", id, { type: "busy" });
      let removeCount = 0;
      hoxiAPI.removeWorktree = async () => { removeCount++; return { ok: true }; };
      await rejection(switchActiveSessionSandboxMode("local"));
      assertEqual(removeCount, 0);
      assertEqual(sessionDirectory(id), meta.directory);
    });
  });

  it("isolates Hoxi Work workspaces and directories from Code projects and remembered surfaces", async () => {
    await withSessionFixture(async () => {
      const prevSurface = surfaceMode();
      setDefaultWorkspaceDir("/Users/test/.hoxi/workspaces/default");
      try {
        setSurfaceMode("code");
        setWorkspace({
          rootDir: "/Users/test/code-app",
          savedWorkspaces: [
            {
              id: "ws_code_1",
              name: "Code App",
              color: "#5EA0F7",
              surface: "code",
              folders: [{ id: "f_code_1", path: "/Users/test/code-app", isPrimary: true, position: 0 }],
              createdAt: "2026-01-01T00:00:00Z",
              updatedAt: "2026-01-01T00:00:00Z",
            },
          ],
          recentProjects: ["/Users/test/code-app"],
          projectOrder: ["/Users/test/code-app"],
          projects: [],
        });
        setRememberedCodeSurface("/Users/test/code-app", "ses_code_1");

        hoxiAPI.listSessions = async () => [];
        hoxiAPI.resolveProject = async (dir) => ({
          projectID: dir.includes("work-ops") ? "ws_work_1" : "ws_code_1",
          directory: dir,
          roots: [dir],
        });
        hoxiAPI.createWorkspace = async (input) => ({
          id: "ws_work_1",
          name: input.name || "Work Ops",
          color: input.color || "#5EA0F7",
          surface: input.surface,
          folders: input.folders.map((f, idx) => ({
            id: `f_work_${idx}`,
            path: f.path,
            isPrimary: Boolean(f.isPrimary ?? idx === 0),
            position: idx,
          })),
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
        });

        // Switch to Work mode and create a custom Work workspace (/Users/test/work-ops)
        await activateSurfaceMode("work");
        assertEqual(surfaceMode(), "work");

        const savedWork = await saveWorkspaceProfile({
          name: "Work Ops",
          folders: [{ path: "/Users/test/work-ops", isPrimary: true }],
        });
        await switchWorkspace("/Users/test/work-ops");

        assertEqual(savedWork.surface, "work");
        assertTrue(isWorkSurfacePath("/Users/test/work-ops"), "Work workspace folder must be recognized as work surface");
        assertEqual(workspace.recentProjects.includes("/Users/test/work-ops"), false);
        assertEqual(workspace.projectOrder.includes("/Users/test/work-ops"), false);
        assertEqual(getRememberedCodeSurface().rootDir, "/Users/test/code-app");
        assertEqual(getInitialCodeProjectDir(), "/Users/test/code-app");
        assertEqual(resolveDefaultProjectDir("/Users/test/work-ops"), "/Users/test/code-app");
        assertEqual(visibleSavedWorkspaces("code").some((w) => w.id === "ws_work_1"), false);
        assertEqual(visibleSavedWorkspaces("work").some((w) => w.id === "ws_work_1"), true);

        // OpenCode project list returning the Work folder must be filtered out of Code projects
        hoxiAPI.listProjects = async () => [
          { id: "p1", worktree: "/Users/test/code-app", time: { created: 1, updated: 1 } },
          { id: "p2", worktree: "/Users/test/work-ops", time: { created: 1, updated: 1 } },
        ];
        await loadProjects();
        assertEqual(workspace.projects.some((p) => p.worktree === "/Users/test/work-ops"), false);
        assertEqual(workspace.projects.some((p) => p.worktree === "/Users/test/code-app"), true);

        // Session surface & project ownership checks respect custom Work workspace directories
        assertEqual(resolveSessionSurface({ id: "s_work", directory: "/Users/test/work-ops" }), "work");
        assertEqual(
          sessionBelongsToProject(
            { id: "s_work", title: "Work task", directory: "/Users/test/work-ops", time: { created: 1, updated: 1 } },
            "/Users/test/code-app"
          ),
          false
        );

        // Switching back to Code mode restores /Users/test/code-app, and switching to Work restores /Users/test/work-ops
        await activateSurfaceMode("code");
        assertEqual(surfaceMode(), "code");
        assertEqual(workspace.rootDir, "/Users/test/code-app");

        await activateSurfaceMode("work");
        assertEqual(surfaceMode(), "work");
        assertEqual(workspace.rootDir, "/Users/test/work-ops");
      } finally {
        unrecordWorkWorkspaceDir("/Users/test/work-ops");
        setSurfaceMode(prevSurface);
      }
    });
  });

  it("uses warm surface switch even when a surface has 0 sessions and skips Git status on Work surface", async () => {
    await withSessionFixture(async () => {
      const prevSurface = surfaceMode();
      setDefaultWorkspaceDir("/Users/test/.hoxi/workspaces/default");
      try {
        setSurfaceMode("code");
        setWorkspace({
          rootDir: "/Users/test/code-empty",
          savedWorkspaces: [],
          recentProjects: ["/Users/test/code-empty"],
          projectOrder: ["/Users/test/code-empty"],
          projects: [],
        });
        setRememberedCodeSurface("/Users/test/code-empty", "");

        let listSessionsCalls = 0;
        let resolveProjectCalls = 0;
        const worktreeStatusDirs: string[] = [];
        hoxiAPI.listWorkspaces = async () => ({ workspaces: [], dismissed: [] });
        hoxiAPI.listProjects = async () => [];
        hoxiAPI.listAgents = async () => [];
        hoxiAPI.listSessions = async () => {
          listSessionsCalls++;
          return [];
        };
        hoxiAPI.resolveProject = async (dir) => {
          resolveProjectCalls++;
          return {
            projectID: dir.includes(".hoxi/workspaces") ? "ws_default_work" : "ws_code_empty",
            directory: dir,
            roots: [dir],
          };
        };
        hoxiAPI.getWorktreeStatus = async (sessionID, directory) => {
          if (directory) worktreeStatusDirs.push(directory);
          return {
            sessionID,
            directory: directory || "",
            isolated: false,
            dirty: false,
            changedFiles: 0,
            uncommittedLines: 0,
            additions: 0,
            deletions: 0,
          };
        };

        // Cold switch to Code and Work so both have an empty cached session list ([])
        await switchWorkspace("/Users/test/code-empty");
        await activateSurfaceMode("work");
        assertEqual(surfaceMode(), "work");
        assertEqual(workspace.sessions.length, 0);
        assertEqual(workspace.bootstrappingSessions, false);
        assertEqual(
          worktreeStatusDirs.some((d) => d.includes(".hoxi/workspaces/default")),
          false
        );

        const callsAfterInitialBootstrap = {
          listSessions: listSessionsCalls,
          resolveProject: resolveProjectCalls,
        };

        // Warm switch back to empty Code surface and back to empty Work surface
        await activateSurfaceMode("code");
        assertEqual(surfaceMode(), "code");
        assertEqual(workspace.bootstrappingSessions, false);
        assertEqual(listSessionsCalls, callsAfterInitialBootstrap.listSessions);
        assertEqual(resolveProjectCalls, callsAfterInitialBootstrap.resolveProject);

        await activateSurfaceMode("work");
        assertEqual(surfaceMode(), "work");
        assertEqual(workspace.bootstrappingSessions, false);
        assertEqual(listSessionsCalls, callsAfterInitialBootstrap.listSessions);
        assertEqual(resolveProjectCalls, callsAfterInitialBootstrap.resolveProject);
      } finally {
        setSurfaceMode(prevSurface);
      }
    });
  });

  it("does not invalidate isWorkSurfacePath reactive memos on non-surface sessionIndex updates", async () => {
    await withSessionFixture(async () => {
      setDefaultWorkspaceDir("/Users/test/.hoxi/workspaces/default");
      try {
        indexSession({
          id: "ses_perf_1",
          title: "Initial",
          directory: "/Users/test/perf-code",
          surface: "code",
          time: { created: 1, updated: 1 },
        });
        assertEqual(isWorkSurfacePath("/Users/test/perf-code"), false);
        const revBefore = sessionSurfaceRevision();

        // Updating title/timestamps on existing session with same surface & directory must not bump surface revision
        indexSession({
          id: "ses_perf_1",
          title: "Updated title",
          directory: "/Users/test/perf-code",
          surface: "code",
          time: { created: 1, updated: 999 },
        });
        assertEqual(sessionSurfaceRevision(), revBefore);
        assertEqual(isWorkSurfacePath("/Users/test/perf-code"), false);

        // Indexing a brand-new work session in another directory bumps the surface revision and updates lookup
        indexSession({
          id: "ses_perf_work",
          title: "Work session",
          directory: "/Users/test/perf-work",
          surface: "work",
          time: { created: 2, updated: 2 },
        });
        assertEqual(sessionSurfaceRevision(), revBefore + 1);
        assertEqual(isWorkSurfacePath("/Users/test/perf-code"), false);
        assertEqual(isWorkSurfacePath("/Users/test/perf-work"), true);
      } finally {
        unrecordWorkWorkspaceDir("/Users/test/perf-work");
      }
    });
  });
});
