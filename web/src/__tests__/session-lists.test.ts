import { createRoot } from "solid-js";
import { reconcile, unwrap } from "solid-js/store";
import { hoxiAPI } from "../services/api";
import type { SessionInfo } from "../services/sse-types";
import { bootstrapProjectSessions, createProjectSessionLists, setWorkspace, workspace } from "../store/workspace";
import { assertDeepEqual, assertEqual, assertTrue, describe, it } from "./test-harness";

function session(id: string, directory: string, extra?: Partial<SessionInfo>): SessionInfo {
  return { id, title: id, directory, time: { created: 1, updated: 1 }, ...extra };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => { resolve = r; });
  return { promise, resolve };
}

const ids = (list: SessionInfo[] | undefined) => (list || []).map((s) => s.id);

async function withListsFixture(
  run: (lists: ReturnType<typeof createProjectSessionLists>) => Promise<void>
): Promise<void> {
  const saved = structuredClone(unwrap(workspace));
  const originalAPI = { ...hoxiAPI };
  hoxiAPI.getWorktreeStatus = async () => ({ sessionID: "__root__", isRepo: false, files: [] } as any);
  hoxiAPI.getSessionStatuses = async () => ({});
  hoxiAPI.listPermissions = async () => [];
  hoxiAPI.getMessages = async () => [];
  hoxiAPI.getSessionDiff = async () => [];
  setWorkspace({ savedWorkspaces: [], sessions: [], activeSessionID: null, activeProjectID: null,
    bootstrappingSessions: false, sessionIndex: {}, worktreeBySession: {}, messagesBySession: {} });
  let dispose!: () => void;
  const lists = createRoot((d) => { dispose = d; return createProjectSessionLists(); });
  try {
    await run(lists);
  } finally {
    lists.dispose();
    dispose();
    Object.assign(hoxiAPI, originalAPI);
    setWorkspace(reconcile(saved));
  }
}

describe("sidebar project session lists", () => {
  it("never caches the newly active project's tasks under a project whose load was still pending", async () => {
    await withListsFixture(async (lists) => {
      setWorkspace({ rootDir: "/repo-a", sessions: [session("ses_a1", "/repo-a")] });
      const listA = deferred<SessionInfo[]>();
      hoxiAPI.listSessions = () => listA.promise;
      const loading = lists.load("/repo-a");
      // The user switches to B before A's list arrives.
      setWorkspace({ rootDir: "/repo-b", sessions: [session("ses_b1", "/repo-b")] });
      listA.resolve([session("ses_a1", "/repo-a")]);
      await loading;
      assertEqual(ids(lists.sessions()["/repo-a"]).includes("ses_b1"), false);
      lists.syncActive();
      assertDeepEqual(ids(lists.sessions()["/repo-b"]), ["ses_b1"]);
    });
  });

  it("drops tasks owned by another project from a scoped list response", async () => {
    await withListsFixture(async (lists) => {
      setWorkspace({ rootDir: "/repo-a", sessions: [] });
      hoxiAPI.listSessions = async () => [
        session("ses_b1", "/repo-b"),
        session("ses_a_leak", "/repo-a"),
        session("ses_b_tree", "/repo-b/.hoxi/worktrees/ses_b_tree"),
      ];
      await lists.load("/repo-b");
      assertDeepEqual(ids(lists.sessions()["/repo-b"]), ["ses_b1", "ses_b_tree"]);
    });
  });

  it("does not let a late list from before an A→B→A round trip overwrite the current list", async () => {
    await withListsFixture(async () => {
      setWorkspace({ rootDir: "/repo-a", sessions: [] });
      const reads: Array<ReturnType<typeof deferred<SessionInfo[]>>> = [];
      hoxiAPI.listSessions = () => {
        const read = deferred<SessionInfo[]>();
        reads.push(read);
        return read.promise;
      };
      const first = bootstrapProjectSessions("/repo-a", false);
      setWorkspace({ rootDir: "/repo-b", sessions: [] });
      const second = bootstrapProjectSessions("/repo-b", false);
      setWorkspace({ rootDir: "/repo-a", sessions: [] });
      const third = bootstrapProjectSessions("/repo-a", false);
      reads[1].resolve([session("ses_b1", "/repo-b")]);
      await second;
      assertEqual(workspace.sessions.length, 0, "B's late list must not land in A");
      reads[2].resolve([session("ses_a1", "/repo-a"), session("ses_a2", "/repo-a")]);
      await third;
      reads[0].resolve([session("ses_a1", "/repo-a")]);
      await first;
      assertDeepEqual(ids(workspace.sessions), ["ses_a1", "ses_a2"]);
    });
  });

  it("does not discard the first load when an optimistic update has no cached list", async () => {
    await withListsFixture(async (lists) => {
      setWorkspace({ rootDir: "/repo", sessions: [] });
      const response = deferred<SessionInfo[]>();
      hoxiAPI.listSessions = () => response.promise;
      const loading = lists.load("/repo");
      lists.updateSession("/repo", "ses_a", (info) => ({ ...info, title: "updated" }));
      response.resolve([session("ses_a", "/repo")]);
      await loading;
      assertDeepEqual(ids(lists.sessions()["/repo"]), ["ses_a"]);
    });
  });

  it("still opens the initial task when a reconnect refresh lands before the startup load", async () => {
    await withListsFixture(async () => {
      setWorkspace({ rootDir: "/repo", sessions: [], bootstrappingSessions: true });
      const reads: Array<ReturnType<typeof deferred<SessionInfo[]>>> = [];
      hoxiAPI.listSessions = () => {
        const read = deferred<SessionInfo[]>();
        reads.push(read);
        return read.promise;
      };
      const startup = bootstrapProjectSessions("/repo", true);
      const reconnect = bootstrapProjectSessions("/repo", false);
      const list = [session("ses_last", "/repo", { time: { created: 1, updated: 9 } })];
      reads[1].resolve(list);
      await reconnect;
      reads[0].resolve(list);
      await startup;
      assertEqual(workspace.activeSessionID, "ses_last");
      assertTrue(!workspace.bootstrappingSessions);
      // Let the selection's debounced diff/status refreshes hit the stubs.
      await new Promise((r) => setTimeout(r, 250));
    });
  });
});
