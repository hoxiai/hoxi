import { createRoot, createSignal } from "solid-js";
import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { appSettings, setAppSettings } from "../store/settings";
import {
  newIsolatedSession, startNewThreadDraft, switchActiveSessionSandboxMode,
} from "../store/session-sandbox";
import {
  activeDirectory,
  bootstrapProjectSessions,
  buildWorkspaceFoldersSystemPrompt,
  findWorkspaceProfileByDir,
  getComposerSkill,
  reconcileSessionsInPlace,
  selectComposerSkill,
  setWorkspace,
  submitSessionPrompt,
  workspace,
  workspaceFoldersForDir,
} from "../store/workspace";
import {
  createImageAttachments,
  createPasteCollapse,
  createSessionDraftSync,
} from "../components/timeline/composer-hooks";
import type { SessionInfo } from "../services/sse-types";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

async function withDraftFixture(run: (calls: string[]) => void | Promise<void>) {
  const savedWorkspace = structuredClone(unwrap(workspace));
  const savedSettings = structuredClone(unwrap(appSettings));
  const originalAPI = { ...hoxiAPI };
  const storageDescriptor = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  const documentDescriptor = Object.getOwnPropertyDescriptor(globalThis, "document");
  const calls: string[] = [];
  const values = new Map<string, string>();
  const storage: Storage = {
    get length() { return values.size; },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => { values.delete(key); },
    setItem: (key, value) => { values.set(key, String(value)); },
  };
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
  Object.defineProperty(globalThis, "document", { configurable: true, value: {
    documentElement: { classList: { toggle() {} }, dataset: {}, style: { setProperty() {} } },
  } });
  for (const key of Object.keys(originalAPI)) {
    Object.assign(hoxiAPI, { [key]: async () => {
      calls.push(key);
      throw new Error(`Unexpected API call: ${key}`);
    } });
  }
  try {
    setWorkspace({ rootDir: "/new-thread-test", activeSessionID: "ses_existing",
      sessionSetupError: "Previous setup failed", newThreadRequest: 7,
      sessions: [{ id: "ses_existing", title: "Existing thread", directory: "/new-thread-test",
        time: { created: 1, updated: 1 } }] });
    setWorkspace("worktreeBySession", reconcile({}));
    setAppSettings("defaultSandboxMode", "local");
    await run(calls);
  } finally {
    Object.assign(hoxiAPI, originalAPI);
    setAppSettings(reconcile(savedSettings));
    setWorkspace(reconcile(savedWorkspace));
    if (storageDescriptor) Object.defineProperty(globalThis, "localStorage", storageDescriptor);
    else Reflect.deleteProperty(globalThis, "localStorage");
    if (documentDescriptor) Object.defineProperty(globalThis, "document", documentDescriptor);
    else Reflect.deleteProperty(globalThis, "document");
  }
}

describe("new thread drafts", () => {
  it("rejects session creation without a project before calling any API", async () => {
    await withDraftFixture(async (calls) => {
      setWorkspace("rootDir", "");
      const sessions = structuredClone(unwrap(workspace.sessions));
      for (const isolated of [false, true]) {
        let error: unknown;
        try { await newIsolatedSession(undefined, { isolated }); }
        catch (caught) { error = caught; }
        assertTrue(error instanceof Error, "creation without a project must reject");
        assertTrue(Boolean(workspace.sessionSetupError), "show the missing-project error");
        assertEqual(workspace.activeSessionID, "ses_existing");
        assertDeepEqual(workspace.sessions, sessions);
        assertDeepEqual(calls, []);
      }
    });
  });

  it("repeated drafts keep the project and history, clear errors and increment the request without creating sessions", async () => {
    await withDraftFixture((calls) => {
      const sessions = structuredClone(unwrap(workspace.sessions));
      for (const request of [8, 9]) {
        setWorkspace("sessionSetupError", "Previous setup failed");
        startNewThreadDraft();
        assertEqual(workspace.rootDir, "/new-thread-test");
        assertEqual(workspace.activeSessionID, null);
        assertEqual(workspace.sessionSetupError, null);
        assertEqual(workspace.newThreadRequest, request);
        assertDeepEqual(workspace.sessions, sessions);
        assertDeepEqual(workspace.worktreeBySession, {});
        assertDeepEqual(calls, []);
      }
    });
  });

  it("changes draft sandbox defaults without creating a session or provisioning a worktree", async () => {
    await withDraftFixture(async (calls) => {
      startNewThreadDraft();
      const sessions = structuredClone(unwrap(workspace.sessions));
      for (const mode of ["worktree", "local"] as const) {
        await switchActiveSessionSandboxMode(mode);
        assertEqual(appSettings.defaultSandboxMode, mode);
        assertEqual(workspace.activeSessionID, null);
        assertEqual(workspace.rootDir, "/new-thread-test");
        assertEqual(workspace.newThreadRequest, 8);
        assertDeepEqual(workspace.sessions, sessions);
        assertDeepEqual(workspace.worktreeBySession, {});
        assertDeepEqual(calls, []);
      }
    });
  });

  it("resolves activeDirectory to workspace.rootDir during a new thread draft in a non-default project", async () => {
    await withDraftFixture(() => {
      setWorkspace("rootDir", "/repo-second");
      startNewThreadDraft();
      assertEqual(workspace.activeSessionID, null);
      assertEqual(activeDirectory(), "/repo-second");
    });
  });

  it("includes primary workspace context for single-folder projects and maps worktree paths back to saved profiles", async () => {
    await withDraftFixture(() => {
      setWorkspace("savedWorkspaces", [
        {
          id: "ws_second",
          name: "Second Project",
          color: "#34D399",
          folders: [
            { id: "f1", path: "/repo-second", isPrimary: true, position: 0 },
            { id: "f2", path: "/repo-second-shared", isPrimary: false, position: 1 },
          ],
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
        },
      ]);

      const singlePrompt = buildWorkspaceFoldersSystemPrompt(workspaceFoldersForDir("/repo-single"));
      assertTrue(Boolean(singlePrompt?.includes('"/repo-single"')), "single-folder workspace must inject primary folder context");

      const wtDir = "/repo-second/.hoxi/worktrees/ses_wt_99";
      const matched = findWorkspaceProfileByDir(wtDir);
      assertEqual(matched?.id, "ws_second");
      const wtFolders = workspaceFoldersForDir(wtDir);
      assertEqual(wtFolders.length, 2);
      assertEqual(wtFolders[0].path, wtDir);
      assertEqual(wtFolders[1].path, "/repo-second-shared");
      const wtPrompt = buildWorkspaceFoldersSystemPrompt(wtFolders);
      assertTrue(Boolean(wtPrompt?.includes(JSON.stringify(wtDir))));
      assertTrue(Boolean(wtPrompt?.includes('"/repo-second-shared"')));
    });
  });

  it("does not overwrite a new thread draft when an in-flight bootstrapProjectSessions finishes", async () => {
    await withDraftFixture(async () => {
      setWorkspace({ rootDir: "/repo-second", activeSessionID: null, sessions: [] });
      let resolveList!: (list: SessionInfo[]) => void;
      hoxiAPI.listSessions = () => new Promise((r) => { resolveList = r; });
      hoxiAPI.getWorktreeStatus = async () => ({
        sessionID: "__root__", directory: "/repo-second", isolated: false, dirty: false,
        changedFiles: 0, uncommittedLines: 0, additions: 0, deletions: 0,
      });
      hoxiAPI.getSessionStatuses = async () => ({});
      hoxiAPI.listPermissions = async () => [];

      const boot = bootstrapProjectSessions("/repo-second", true);
      // User clicks + New Thread while listSessions is still in flight
      startNewThreadDraft();
      resolveList([
        { id: "ses_old", title: "Old session", directory: "/repo-second", time: { created: 1, updated: 2 } },
      ]);
      await boot;
      assertEqual(workspace.activeSessionID, null, "must stay in new thread draft instead of selecting ses_old");
    });
  });

  it("preserves a newly created active session when a stale listSessions reconciles", async () => {
    await withDraftFixture(() => {
      setWorkspace({
        rootDir: "/repo-second",
        activeSessionID: "ses_just_created",
        sessions: [
          { id: "ses_just_created", title: "New chat", directory: "/repo-second", time: { created: 10, updated: 10 } },
        ],
      });
      reconcileSessionsInPlace([
        { id: "ses_older", title: "Older chat", directory: "/repo-second", time: { created: 1, updated: 5 } },
      ]);
      assertDeepEqual(
        workspace.sessions.map((s) => s.id),
        ["ses_just_created", "ses_older"]
      );
    });
  });

  it("pins new session creation and prompt dispatch to the draft directory even if rootDir changes", async () => {
    await withDraftFixture(async () => {
      setWorkspace({
        rootDir: "/repo-second",
        activeSessionID: null,
        sessions: [],
        providers: [{ id: "openai", name: "OpenAI", models: { "gpt-5": { id: "gpt-5", name: "GPT-5" } } }],
        defaultAI: { agent: "build", model: { providerID: "openai", modelID: "gpt-5" }, effort: "" },
      });
      let createdInDir: string | undefined;
      let sentInDir: string | undefined;
      let sentSystem: string | undefined;
      hoxiAPI.createSession = async (_title, dir) => {
        createdInDir = dir;
        // Simulate rootDir drifting before createSession resolves
        setWorkspace("rootDir", "/repo-default");
        return {
          id: "ses_second_1",
          title: "Second project task",
          directory: dir,
          time: { created: 1, updated: 1 },
        };
      };
      hoxiAPI.sendPromptAsync = async (_id, _text, opts) => {
        sentInDir = opts?.directory;
        sentSystem = opts?.system;
      };
      hoxiAPI.summarizeSessionTitle = async () => ({ ok: false, title: "" });

      const id = await submitSessionPrompt("Check files in this project", null, {
        directory: "/repo-second",
      });
      assertEqual(id, "ses_second_1");
      assertEqual(createdInDir, "/repo-second");
      assertEqual(sentInDir, "/repo-second");
      assertTrue(Boolean(sentSystem?.includes('"/repo-second"')));
    });
  });

  it("preserves unsent new-thread drafts per workspace across session switches and startNewThreadDraft", async () => {
    await withDraftFixture(() => {
      createRoot((dispose) => {
        try {
          setWorkspace({ rootDir: "/repo-a", activeSessionID: null });
          const [text, setText] = createSignal("");
          const sessionID = () => workspace.activeSessionID;
          const directory = () => workspace.rootDir;
          const imgAttach = createImageAttachments(sessionID, directory);
          const pasteCollapse = createPasteCollapse();
          const draftSync = createSessionDraftSync({
            sessionID,
            directory,
            resetRequest: () => workspace.newThreadRequest,
            text,
            setText,
            imgAttach,
            pasteCollapse,
            onAfterRestore: () => {},
          });

          // 1. Type in Workspace A's new thread and pick a skill
          setText("Draft in repo A");
          selectComposerSkill(null, { key: "skl_a", name: "skill-a" });
          assertEqual(getComposerSkill(null)?.key, "skl_a");

          // 2. Switch to an existing session in Workspace A -> composer clears for that session
          setWorkspace("activeSessionID", "ses_existing");
          draftSync.sync();
          assertEqual(text(), "");
          setText("Reply to existing session");

          // 3. Click "New Thread" (`startNewThreadDraft`) -> restores Workspace A's new-thread draft & skill
          startNewThreadDraft();
          draftSync.sync();
          assertEqual(workspace.activeSessionID, null);
          assertEqual(text(), "Draft in repo A");
          assertEqual(getComposerSkill(null)?.key, "skl_a");

          // 4. Switch to Workspace B's new thread -> independent empty draft, then type in Workspace B
          setWorkspace({ rootDir: "/repo-b", activeSessionID: null });
          draftSync.sync();
          assertEqual(text(), "");
          assertEqual(getComposerSkill(null), null);
          setText("Draft in repo B");
          selectComposerSkill(null, { key: "skl_b", name: "skill-b" });

          // 5. Switch back to Workspace A and click "New Thread" -> Workspace A's draft is still intact
          setWorkspace({ rootDir: "/repo-a", activeSessionID: "ses_existing" });
          draftSync.sync();
          assertEqual(text(), "Reply to existing session");
          startNewThreadDraft();
          draftSync.sync();
          assertEqual(text(), "Draft in repo A");
          assertEqual(getComposerSkill(null)?.key, "skl_a");

          // 6. Clean up drafts
          draftSync.clearCurrentDraft(null, "/repo-a");
          draftSync.clearCurrentDraft(null, "/repo-b");
          draftSync.clearCurrentDraft("ses_existing", "/repo-a");
          selectComposerSkill(null, null, undefined, "/repo-a");
          selectComposerSkill(null, null, undefined, "/repo-b");
        } finally {
          dispose();
        }
      });
    });
  });
});
