import { reconcile, unwrap } from "solid-js/store";
import type { SessionInfo } from "../services/sse-types";
import {
  collectCrossProjectSessions,
  groupRunningByProject,
  partitionAttentionSessions,
} from "../components/layout/cross-project-nav";
import {
  countRunningSubagents,
  groupSubagentsByRoot,
  shouldAutoExpandSubagents,
} from "../components/layout/subagent-nav";
import { stripSubagentTitleSuffix } from "../store/session-title";
import type { ProjectNavEntry } from "../components/layout/left-nav-helpers";
import { setWorkspace, workspace, type WorkspaceState } from "../store/workspace";
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
          session("ses_quiet", 400),
        ],
        statusBySession: {},
        unreadCompletedBySession: { ses_done: true, ses_approval: true },
        interruptedBySession: { ses_paused: "paused", ses_done: "stopped" },
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

        // All idle, no approvals, not active -> collapsed by default
        assertEqual(shouldAutoExpandSubagents(subs, new Set(), ""), false);
        assertEqual(countRunningSubagents(subs), 0);

        // Selecting a child session auto-expands the parent drawer
        setWorkspace("activeSessionID", "ses_child_2");
        assertEqual(shouldAutoExpandSubagents(subs, new Set(), ""), true);
        setWorkspace("activeSessionID", null);

        // Pending approval on a child session auto-expands
        assertEqual(shouldAutoExpandSubagents(subs, new Set(["ses_grandchild"]), ""), true);

        // Search filter matching a child session auto-expands
        assertEqual(shouldAutoExpandSubagents(subs, new Set(), "schema"), true);

        // Busy child session auto-expands and counts as running
        setWorkspace("statusBySession", "ses_child_1", { type: "busy" });
        assertEqual(shouldAutoExpandSubagents(subs, new Set(), ""), true);
        assertEqual(countRunningSubagents(subs), 1);
      },
    );
  });
});
