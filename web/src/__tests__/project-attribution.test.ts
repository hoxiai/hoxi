import { reconcile, unwrap } from "solid-js/store";
import type { SessionInfo } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import { setWorkspace, workspace } from "../store/workspace";
import { renameSessionTitle, replaceSession, replaceSessionFromEvent } from "../store/workspace-sessions";
import { assertEqual, describe, it } from "./test-harness";

function session(id: string, extra?: Partial<SessionInfo>): SessionInfo {
  return { id, title: "test", time: { created: 1, updated: 1 }, ...extra };
}

describe("project attribution", () => {
  it("does not insert a late background rename into the newly active workspace", async () => {
    const saved = structuredClone(unwrap(workspace));
    const original = hoxiAPI.renameSession;
    try {
      const task = session("ses_late_rename", { projectID: "prj_a", directory: "/repo-a" });
      setWorkspace({ rootDir: "/repo-a", activeProjectID: "prj_a", sessions: [task] });
      let finish!: (info: SessionInfo) => void;
      hoxiAPI.renameSession = () => new Promise((resolve) => { finish = resolve; });
      const pending = renameSessionTitle(task.id, "Renamed task");
      setWorkspace({ rootDir: "/repo-b", activeProjectID: "prj_b", sessions: [] });
      finish({ ...task, title: "Renamed task" });
      await pending;
      assertEqual(workspace.sessions.length, 0);
      assertEqual(workspace.sessionIndex[task.id]?.directory, "/repo-a");
    } finally {
      hoxiAPI.renameSession = original;
      setWorkspace(reconcile(saved));
    }
  });

  it("keeps foreign background updates out while retaining their global metadata", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace({ rootDir: "/repo", activeProjectID: "prj_root", sessions: [] });
      replaceSession(session("ses_foreign_background", { projectID: "prj_other", directory: "/other" }));
      replaceSession(session("ses_nested_background", { directory: "/repo/nested" }));
      assertEqual(workspace.sessions.length, 0);
      assertEqual(workspace.sessionIndex.ses_foreign_background?.projectID, "prj_other");
      replaceSession(session("ses_owned_background", { projectID: "prj_root", directory: "/external-worktree" }));
      assertEqual(workspace.sessions[0]?.id, "ses_owned_background");
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("admits events by stable project ID instead of directory prefixes", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace({
        rootDir: "/repo",
        activeProjectID: "prj_root",
        sessions: [],
        worktreeBySession: {},
      });

      replaceSessionFromEvent(session("foreign", {
        projectID: "prj_other",
        directory: "/repo/.hoxi/worktrees/foreign",
      }));
      assertEqual(workspace.sessions.some((s) => s.id === "foreign"), false);

      replaceSessionFromEvent(session("owned", {
        projectID: "prj_root",
        directory: "/outside/execution-dir",
      }));
      assertEqual(workspace.sessions.some((s) => s.id === "owned"), true);

      replaceSessionFromEvent(session("child", {
        projectID: "prj_other",
        parentID: "owned",
        directory: "/repo",
      }));
      assertEqual(workspace.sessions.some((s) => s.id === "child"), false);
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("admits by directory before boot resolves the project, without writing activeProjectID", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace({
        rootDir: "/repo",
        activeProjectID: null,
        sessions: [],
        worktreeBySession: {},
      });

      replaceSessionFromEvent(session("created", {
        projectID: "prj_boot",
        directory: "/repo",
      }));
      assertEqual(workspace.activeProjectID, null);
      assertEqual(workspace.sessions.some((s) => s.id === "created"), true);
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("rejects a foreign project ID for a loaded session without attribution", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace({
        rootDir: "/repo",
        activeProjectID: "prj_root",
        sessions: [session("legacy", { directory: "/repo", title: "legacy" })],
        worktreeBySession: {},
      });
      replaceSessionFromEvent(session("legacy", { projectID: "prj_other", directory: "/repo", title: "stolen" }));
      assertEqual(workspace.sessions.find((s) => s.id === "legacy")?.title, "legacy");
    } finally {
      setWorkspace(reconcile(saved));
    }
  });
});
