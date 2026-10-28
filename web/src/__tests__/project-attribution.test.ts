import { reconcile, unwrap } from "solid-js/store";
import type { SessionInfo } from "../services/sse-types";
import { hoxiAPI } from "../services/api";
import {
  activeDirectory,
  bootstrapProjectSessions,
  deleteCachedProjectSessions,
  filterProjectSessions,
  isDismissedProjectPath,
  saveWorkspaceProfile,
  setWorkspace,
  workspace,
} from "../store/workspace";
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

  it("falls back to directory ownership for a removed project ID", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace({
        rootDir: "/repo",
        activeProjectID: "prj_new",
        savedWorkspaces: [{
          id: "prj_new",
          name: "Repo",
          color: "#5EA0F7",
          folders: [{ id: "folder", path: "/repo", isPrimary: true, position: 0 }],
          createdAt: "",
          updatedAt: "",
        }],
        sessions: [],
        worktreeBySession: {},
      });
      const owned = filterProjectSessions(
        [session("legacy-project", { projectID: "prj_removed", directory: "/repo" })],
        "/repo",
        "prj_new"
      );
      assertEqual(owned.some((item) => item.id === "legacy-project"), true);
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("does not directory-fallback a live foreign project from a scoped list", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace({
        rootDir: "/repo",
        activeProjectID: "prj_new",
        savedWorkspaces: [
          {
            id: "prj_new", name: "Repo", color: "#5EA0F7",
            folders: [{ id: "a", path: "/repo", isPrimary: true, position: 0 }],
            createdAt: "", updatedAt: "",
          },
          {
            id: "prj_other", name: "Other", color: "#5EA0F7",
            folders: [{ id: "b", path: "/other", isPrimary: true, position: 0 }],
            createdAt: "", updatedAt: "",
          },
        ],
        sessions: [],
        worktreeBySession: {},
      });
      const owned = filterProjectSessions(
        [session("live-foreign", { projectID: "prj_other", directory: "/repo/.hoxi/worktrees/ses_x" })],
        "/repo",
        "prj_new"
      );
      assertEqual(owned.length, 0);
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("rejects directory-less foreign sessions before project resolution", () => {
    const saved = structuredClone(unwrap(workspace));
    try {
      setWorkspace({ rootDir: "/repo", activeProjectID: null, savedWorkspaces: [], sessions: [], worktreeBySession: {} });
      replaceSessionFromEvent(session("directory-less-foreign", { projectID: "prj_other" }));
      assertEqual(workspace.sessions.some((item) => item.id === "directory-less-foreign"), false);
    } finally {
      setWorkspace(reconcile(saved));
    }
  });

  it("updates worktreeBySession directory when a session is relinked to a new project directory", async () => {
    const saved = structuredClone(unwrap(workspace));
    const origListSessions = hoxiAPI.listSessions;
    const origGetMessages = hoxiAPI.getMessages;
    try {
      setWorkspace({
        rootDir: "/repo-new",
        activeProjectID: "prj_1",
        activeSessionID: null,
        sessions: [],
        worktreeBySession: {
          ses_relinked: {
            sessionID: "ses_relinked",
            isolated: false,
            directory: "/repo-old",
            createdAt: "2026-01-01T00:00:00.000Z",
          },
        },
      });
      hoxiAPI.listSessions = async () => [
        session("ses_relinked", { projectID: "prj_1", directory: "/repo-new", title: "Relinked task" }),
      ];
      hoxiAPI.getMessages = async () => [];
      await bootstrapProjectSessions("/repo-new", true, "prj_1");
      assertEqual(workspace.worktreeBySession.ses_relinked?.directory, "/repo-new");
      assertEqual(workspace.activeSessionID, "ses_relinked");
      assertEqual(activeDirectory(), "/repo-new");
    } finally {
      deleteCachedProjectSessions("/repo-new");
      hoxiAPI.listSessions = origListSessions;
      hoxiAPI.getMessages = origGetMessages;
      setWorkspace(reconcile(saved));
    }
  });

  it("does not auto-select un-relinked sessions from a replaced directory and cleans up old folder state on save", async () => {
    const saved = structuredClone(unwrap(workspace));
    const origUpdateWorkspace = hoxiAPI.updateWorkspace;
    const origListSessions = hoxiAPI.listSessions;
    const origResolveProject = hoxiAPI.resolveProject;
    const origListAgents = hoxiAPI.listAgents;
    try {
      setWorkspace({
        rootDir: "/repo-old",
        activeProjectID: "prj_1",
        activeSessionID: "ses_old",
        editingWorkspaceDir: "/repo-old",
        providers: [{ id: "p1", name: "P1", models: {} }],
        savedWorkspaces: [
          {
            id: "prj_1",
            name: "MyRepo",
            color: "#5EA0F7",
            folders: [{ id: "fld_1", path: "/repo-old", isPrimary: true, position: 0 }],
            createdAt: "",
            updatedAt: "",
          },
        ],
        recentProjects: ["/repo-old", "/other"],
        projects: [{ id: "p_old", worktree: "/repo-old", time: { created: 1, updated: 1 } }],
        projectOrder: ["/other", "/repo-old"],
        sessions: [session("ses_old", { projectID: "prj_1", directory: "/repo-old", title: "Old dir task" })],
        worktreeBySession: {
          ses_old: {
            sessionID: "ses_old",
            isolated: false,
            directory: "/repo-old",
            createdAt: "2026-01-01T00:00:00.000Z",
          },
        },
      });

      let capturedInput: unknown = null;
      hoxiAPI.updateWorkspace = async (_id, input) => {
        capturedInput = input;
        return {
          id: "prj_1",
          name: "MyRepo",
          color: "#5EA0F7",
          folders: [{ id: "fld_1", path: "/repo-new", isPrimary: true, position: 0 }],
          createdAt: "",
          updatedAt: "",
        };
      };
      hoxiAPI.resolveProject = async () => ({
        projectID: "prj_1",
        directory: "/repo-new",
        roots: ["/repo-new"],
      });
      hoxiAPI.listAgents = async () => [];
      // Old folder still existed on disk so ses_old kept directory: "/repo-old"
      hoxiAPI.listSessions = async () => [
        session("ses_old", { projectID: "prj_1", directory: "/repo-old", title: "Old dir task" }),
      ];

      await saveWorkspaceProfile(
        { name: "MyRepo", folders: [{ path: "/repo-new", isPrimary: true }] },
        "prj_1"
      );

      assertEqual((capturedInput as { folders: Array<{ id?: string }> })?.folders?.[0]?.id, "fld_1");
      assertEqual(workspace.rootDir, "/repo-new");
      assertEqual(workspace.activeSessionID, null);
      assertEqual(activeDirectory(), "/repo-new");
      assertEqual(isDismissedProjectPath("/repo-old"), true);
      assertEqual(workspace.recentProjects.includes("/repo-old"), false);
      assertEqual(workspace.projects.some((p) => p.worktree === "/repo-old"), false);
      assertEqual(workspace.projectOrder[1], "/repo-new");
    } finally {
      deleteCachedProjectSessions("/repo-new");
      hoxiAPI.updateWorkspace = origUpdateWorkspace;
      hoxiAPI.listSessions = origListSessions;
      hoxiAPI.resolveProject = origResolveProject;
      hoxiAPI.listAgents = origListAgents;
      setWorkspace(reconcile(saved));
    }
  });
});
