import { reconcile, unwrap } from "solid-js/store";
import type { SessionInfo } from "../services/sse-types";
import { setWorkspace, workspace } from "../store/workspace";
import { replaceSessionFromEvent } from "../store/workspace-sessions";
import { assertEqual, describe, it } from "./test-harness";

function session(id: string, extra?: Partial<SessionInfo>): SessionInfo {
  return { id, title: "test", time: { created: 1, updated: 1 }, ...extra };
}

describe("project attribution", () => {
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
