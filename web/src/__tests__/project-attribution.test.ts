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

  it("learns the project ID from the first event when boot has not resolved it yet", () => {
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
      assertEqual(workspace.activeProjectID, "prj_boot");
      assertEqual(workspace.sessions.some((s) => s.id === "created"), true);
    } finally {
      setWorkspace(reconcile(saved));
    }
  });
});
