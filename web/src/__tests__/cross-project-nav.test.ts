import { reconcile, unwrap } from "solid-js/store";
import type { SessionInfo } from "../services/sse-types";
import {
  collectCrossProjectSessions,
  groupRunningByProject,
  partitionAttentionSessions,
} from "../components/layout/cross-project-nav";
import { stripSubagentTitleSuffix } from "../store/session-title";
import type { ProjectNavEntry } from "../components/layout/left-nav-helpers";
import {
  childSubagentIDsFromTurns,
  clearSessionPendingApprovals,
  countRunningSubagents,
  groupSubagentsByRoot,
  isSubagentDrawerExpanded,
  isWorkSurfacePath,
  sessionsWithPendingPermission,
  setSubagentDrawerExpanded,
  setWorkspace,
  shouldAutoExpandSubagents,
  workspace,
  type WorkspaceState,
} from "../store/workspace";
import { assertDeepEqual, assertEqual, describe, it } from "./test-harness";

function project(path: string, name: string): ProjectNavEntry {
  return {
    name,
    path,
    color: "#888888",
    folders: [{ path, isPrimary: true }],
    sandboxes: 0,
    vcs: true,
    isPinned: false,
  };
}

function session(id: string, updated: number, extra: Partial<SessionInfo> = {}): SessionInfo {
  return { id, title: id, time: { created: updated, updated }, ...extra };
}

function withWorkspace(
  state: Partial<WorkspaceState>,
  run: () => void,
): void {
  const saved = structuredClone(unwrap(workspace));
  try {
    setWorkspace(reconcile({ ...saved, ...state }));
    run();
  } finally {
    setWorkspace(reconcile(saved));
  }
}

const PROJECTS = [project("/repo-a", "Repo A"), project("/repo-b", "Repo B")];

describe("Cross-project sidebar views", () => {
  it("groups running sessions by project and drops idle, archived and child sessions", () => {
    withWorkspace(
      {
        rootDir: "/repo-a",
        bootstrappingSessions: false,
        sessions: [
          session("ses_a1", 300),
          session("ses_a2", 100),
          session("ses_a3", 200),
          session("ses_a4", 400, { parentID: "ses_a1" }),
          session("ses_a5", 500, { time: { created: 1, updated: 500, archived: 9 } }),
        ],
        statusBySession: {
          ses_a1: { type: "busy" },
          ses_a2: { type: "retry", attempt: 1, message: "retrying", next: 1 },
          ses_a4: { type: "busy" },
          ses_a5: { type: "busy" },
        },
      },
      () => {
        const items = collectCrossProjectSessions({
          allProjects: PROJECTS,
          projectSessions: { "/repo-b": [session("ses_b1", 900)] },
          filterQuery: "",
        });
        assertEqual(items.length, 4);
        const groups = groupRunningByProject(items);
        assertEqual(groups.length, 1);
        assertEqual(groups[0].proj.path, "/repo-a");
        assertDeepEqual(
          groups[0].sessions.map((s) => s.id),
          ["ses_a1", "ses_a2"],
        );
      },
    );
  });

  it("orders attention sections by urgency and assigns each session once", () => {
    withWorkspace(
      {
        rootDir: "/repo-a",
        bootstrappingSessions: false,
        sessions: [
          session("ses_approval", 100),
          session("ses_done", 200),
          session("ses_paused", 300),
          session("ses_stopped", 350),
          session("ses_quiet", 400),
        ],
        statusBySession: {},
        unreadCompletedBySession: { ses_done: true, ses_approval: true },
        interruptedBySession: { ses_paused: "paused", ses_done: "stopped", ses_stopped: "stopped" },
      },
      () => {
        const sections = partitionAttentionSessions(
          collectCrossProjectSessions({
            allProjects: PROJECTS,
            projectSessions: {},
            filterQuery: "",
          }),
          new Set(["ses_approval"]),
        );
        assertDeepEqual(
          sections.map((s) => s.id),
          ["approvals", "completed", "paused"],
        );
        assertDeepEqual(
          sections.map((s) => s.items.map((i) => i.session.id)),
          [["ses_approval"], ["ses_done"], ["ses_paused"]],
        );
      },
    );
  });

  it("honors the search query across titles and project names", () => {
    withWorkspace(
      { rootDir: "/repo-a", bootstrappingSessions: false, sessions: [session("ses_alpha", 1), session("ses_beta", 2)] },
      () => {
        const byTitle = collectCrossProjectSessions({
          allProjects: PROJECTS,
          projectSessions: { "/repo-b": [session("ses_gamma", 3)] },
          filterQuery: "gam",
        });
        assertDeepEqual(byTitle.map((i) => i.session.id), ["ses_gamma"]);

        const byProject = collectCrossProjectSessions({
          allProjects: PROJECTS,
          projectSessions: { "/repo-b": [session("ses_delta", 4)] },
          filterQuery: "repo b",
        });
        assertDeepEqual(byProject.map((i) => i.session.id), ["ses_delta"]);

        const empty = collectCrossProjectSessions({
          allProjects: PROJECTS,
          projectSessions: {},
          filterQuery: "",
        });
        assertEqual(empty.length, 2);
      },
    );
  });

  it("deduplicates cross-project sessions, excludes running sessions from paused, and includes stalled sessions", () => {
    withWorkspace(
      {
        rootDir: "/repo-a",
        bootstrappingSessions: false,
        sessions: [
          session("ses_dup", 500),
          session("ses_running_stale_interrupted", 400),
          session("ses_stalled", 300),
        ],
        statusBySession: {
          ses_running_stale_interrupted: { type: "busy" },
          ses_stalled: { type: "busy" },
        },
        interruptedBySession: {
          ses_running_stale_interrupted: "paused",
        },
        stalledSinceBySession: {
          ses_stalled: 12345,
        },
      },
      () => {
        const items = collectCrossProjectSessions({
          allProjects: PROJECTS,
          projectSessions: { "/repo-b": [session("ses_dup", 500)] },
          filterQuery: "",
        });
        assertDeepEqual(
          items.map((i) => i.session.id),
          ["ses_dup", "ses_running_stale_interrupted", "ses_stalled"],
        );
        const sections = partitionAttentionSessions(items, new Set());
        assertDeepEqual(
          sections.map((s) => s.id),
          ["paused"],
        );
        assertDeepEqual(
          sections[0].items.map((i) => i.session.id),
          ["ses_stalled"],
        );
      },
    );
  });

  it("groups nested subagents under their root session and auto-expands when active, busy, or pending approval", () => {
    withWorkspace(
      {
        rootDir: "/repo-a",
        bootstrappingSessions: false,
        activeSessionID: null,
        sessions: [
          session("ses_root", 100, { title: "Implement Convoy" }),
          session("ses_child_2", 250, {
            parentID: "ses_root",
            title: "Convoy · plan_abc123 · task-b",
            time: { created: 20, updated: 250 },
          }),
          session("ses_child_1", 200, {
            parentID: "ses_root",
            title: "Explore repo (@explore subagent)",
            time: { created: 10, updated: 200 },
          }),
          session("ses_grandchild", 300, {
            parentID: "ses_child_1",
            title: "Read schema (@explore subagent)",
            time: { created: 30, updated: 300 },
          }),
        ],
        statusBySession: {},
      },
      () => {
        const grouped = groupSubagentsByRoot(workspace.sessions);
        const subs = grouped.get("ses_root") ?? [];
        assertDeepEqual(
          subs.map((s) => s.id),
          ["ses_child_1", "ses_child_2", "ses_grandchild"],
        );
        assertEqual(stripSubagentTitleSuffix(subs[0].title), "Explore repo");
        assertEqual(stripSubagentTitleSuffix(subs[1].title), "task-b");

        // Subagent drawer is expanded by default when a session has subtasks
        assertEqual(shouldAutoExpandSubagents(subs, new Set(), ""), false);
        assertEqual(isSubagentDrawerExpanded("ses_root", false), true);
        assertEqual(countRunningSubagents(subs), 0);

        // Selecting a child session triggers autoExpand
        setWorkspace("activeSessionID", "ses_child_2");
        assertEqual(shouldAutoExpandSubagents(subs, new Set(), ""), true);
        setWorkspace("activeSessionID", null);

        // Pending approval on a child session triggers autoExpand
        assertEqual(shouldAutoExpandSubagents(subs, new Set(["ses_grandchild"]), ""), true);

        // Search filter matching a child session triggers autoExpand
        assertEqual(shouldAutoExpandSubagents(subs, new Set(), "schema"), true);

        // Busy child session triggers autoExpand and counts as running
        setWorkspace("statusBySession", "ses_child_1", { type: "busy" });
        assertEqual(shouldAutoExpandSubagents(subs, new Set(), ""), true);
        assertEqual(countRunningSubagents(subs), 1);

        // Manual collapse persists by session ID, and re-opens when autoExpand transitions false -> true
        setSubagentDrawerExpanded("ses_root", false, false);
        assertEqual(isSubagentDrawerExpanded("ses_root", false), false);
        // When autoExpand transitions false -> true, the manual collapse yields to autoExpand and stays open
        assertEqual(isSubagentDrawerExpanded("ses_root", true), true);
        assertEqual(isSubagentDrawerExpanded("ses_root", false), true);
        // Manual collapse while autoExpand is true stays collapsed even after autoExpand returns to false
        setSubagentDrawerExpanded("ses_root", false, true);
        assertEqual(isSubagentDrawerExpanded("ses_root", true), false);
        assertEqual(isSubagentDrawerExpanded("ses_root", false), false);

        // OpenCode child approvals rewritten to root sessionID with subagentSessionID
        // are attributed to the child and cleared individually when that child finishes.
        setWorkspace("pendingPermissions", [
          {
            id: "perm_child_1",
            sessionID: "ses_root",
            subagentSessionID: "ses_child_1",
            permission: "bash",
            patterns: ["ls"],
            metadata: { subagentSessionID: "ses_child_1" },
            always: [],
          },
          {
            id: "perm_child_2",
            sessionID: "ses_root",
            subagentSessionID: "ses_child_2",
            permission: "bash",
            patterns: ["pwd"],
            metadata: { subagentSessionID: "ses_child_2" },
            always: [],
          },
        ]);
        assertEqual(sessionsWithPendingPermission().has("ses_child_1"), true);
        assertEqual(sessionsWithPendingPermission().has("ses_child_2"), true);
        clearSessionPendingApprovals("ses_child_1");
        assertDeepEqual(
          workspace.pendingPermissions.map((p) => p.id),
          ["perm_child_2"],
        );
        clearSessionPendingApprovals("ses_root");
        assertEqual(workspace.pendingPermissions.length, 0);

        // Non-first sessionIndex mutations invalidate the folder lookup cache immediately.
        setWorkspace(
          "sessionIndex",
          reconcile({
            ses_1: { directory: "/code-first", surface: "code" as const, updated: 1 },
            ses_2: { directory: "/dynamic-second", surface: "code" as const, updated: 1 },
          }),
        );
        assertEqual(isWorkSurfacePath("/dynamic-second"), false);
        setWorkspace("sessionIndex", "ses_2", {
          directory: "/dynamic-second",
          surface: "work",
          updated: 2,
        });
        assertEqual(isWorkSurfacePath("/dynamic-second"), true);
      },
    );
  });

  it("extracts unique child subagent IDs from task, agent, and subagent tool parts in order", () => {
    const turns = [
      {
        info: { id: "msg_1", sessionID: "ses_p", role: "assistant" as const, time: { created: 1 } },
        parts: [
          {
            id: "prt_1",
            sessionID: "ses_p",
            messageID: "msg_1",
            type: "tool" as const,
            callID: "call_1",
            tool: "task",
            state: { status: "completed" as const, input: {}, output: "", title: "", metadata: { sessionId: "ses_sub_1" }, time: { start: 1, end: 2 } },
          },
          {
            id: "prt_2",
            sessionID: "ses_p",
            messageID: "msg_1",
            type: "tool" as const,
            callID: "call_2",
            tool: "Agent",
            state: { status: "completed" as const, input: {}, output: "", title: "", metadata: { sessionId: "ses_sub_2" }, time: { start: 1, end: 2 } },
          },
          {
            id: "prt_3",
            sessionID: "ses_p",
            messageID: "msg_1",
            type: "tool" as const,
            callID: "call_3",
            tool: "invoke_subagent",
            state: { status: "completed" as const, input: {}, output: "", title: "", metadata: { sessionId: "ses_sub_1" }, time: { start: 1, end: 2 } },
          },
          {
            id: "prt_4",
            sessionID: "ses_p",
            messageID: "msg_1",
            type: "tool" as const,
            callID: "call_4",
            tool: "bash",
            state: { status: "completed" as const, input: {}, output: "", title: "", metadata: { sessionId: "ses_ignored" }, time: { start: 1, end: 2 } },
          },
        ],
      },
    ];
    assertDeepEqual(childSubagentIDsFromTurns(turns), ["ses_sub_1", "ses_sub_2"]);
    assertDeepEqual(childSubagentIDsFromTurns(undefined), []);
  });
});
