import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import { appSettings, setAppSettings } from "../store/settings";
import {
  newIsolatedSession, startNewThreadDraft, switchActiveSessionSandboxMode,
} from "../store/session-sandbox";
import { setWorkspace, workspace } from "../store/workspace";
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
});
