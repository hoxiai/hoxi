import { createMemo, createRoot } from "solid-js";
import { reconcile, unwrap } from "solid-js/store";
import type { SessionInfo } from "../services/sse-types";
import {
  collectCrossProjectSessions,
  filterCrossProjectSessions,
  groupRunningByProject,
  partitionAttentionSessions,
  RECENT_TASKS_MAX,
  RECENT_TASKS_MIN_FALLBACK,
  RECENT_TASKS_WINDOW_MS,
  selectRecentCrossProjectSessions,
} from "../components/layout/cross-project-nav";
import { stripSubagentTitleSuffix } from "../store/session-title";
import { sameProjectNavEntries, type ProjectNavEntry } from "../components/layout/left-nav-helpers";
import {
  childSubagentIDsFromTurns,
  clearSessionPendingApprovals,
  clearSubagentDrawerOverride,
  countRunningSubagents,
  DEFAULT_VISIBLE_SUBAGENTS,
  getSubagentVisibleLimit,
  groupSubagentsByRoot,
  healTransientSidebarProjectMode,
  indexSessions,
  isSubagentDrawerExpanded,
  isWorkSurfacePath,
  loadMoreSubagents,
  partitionVisibleSubagents,
  removeStorageItem,
  sessionsWithPendingPermission,
  setSidebarProjectMode,
  setSubagentDrawerExpanded,
  setWorkspace,
  shouldAutoExpandSubagents,
  SIDEBAR_NAV_TAB_KEY,
  SIDEBAR_PROJECT_MODE_KEY,
  switchSidebarNavTab,
  upsertMessagePart,
  workspace,
  writeStorageItem,
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

        // Middle-key sessionIndex mutations invalidate the folder lookup cache immediately.
        setWorkspace(
          "sessionIndex",
          reconcile({
            ses_1: { directory: "/code-first", surface: "code" as const, updated: 1 },
            ses_2: { directory: "/dynamic-middle", surface: "code" as const, updated: 1 },
            ses_3: { directory: "/code-last", surface: "code" as const, updated: 1 },
          }),
        );
        assertEqual(isWorkSurfacePath("/dynamic-middle"), false);
        setWorkspace("sessionIndex", "ses_2", {
          directory: "/dynamic-middle",
          surface: "work",
          updated: 2,
        });
        assertEqual(isWorkSurfacePath("/dynamic-middle"), true);
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

  it("caps recent cross-project tasks at 30, enforces 7-day window with 5-item fallback, and exempts urgent sessions", () => {
    const baseNow = 1_800_000_000_000;
    const dayMs = 86_400_000;
    withWorkspace(
      {
        rootDir: "/repo-a",
        bootstrappingSessions: false,
        sessions: [],
        statusBySession: {},
        unreadCompletedBySession: {},
        sessionFinishedAt: {},
      },
      () => {
        // 1) 40 sessions active today -> capped at 30, hiddenCount = 10
        const burstToday = Array.from({ length: 40 }, (_, i) =>
          session(`ses_today_${i}`, baseNow - i * 60_000),
        );
        const burstResult = selectRecentCrossProjectSessions(
          collectCrossProjectSessions({
            allProjects: PROJECTS,
            projectSessions: { "/repo-b": burstToday },
            filterQuery: "",
          }),
          { now: baseNow, pendingSessions: new Set() },
        );
        assertEqual(burstResult.items.length, RECENT_TASKS_MAX);
        assertEqual(burstResult.hiddenCount, 10);
        assertEqual(burstResult.isSearchTruncated, false);

        // 2) 8 sessions within 7 days + 15 sessions 20 days old -> keeps only the 8 recent ones
        const eightRecent = Array.from({ length: 8 }, (_, i) =>
          session(`ses_week_${i}`, baseNow - (i + 1) * 12 * 3_600_000),
        );
        const fifteenStale = Array.from({ length: 15 }, (_, i) =>
          session(`ses_stale_${i}`, baseNow - RECENT_TASKS_WINDOW_MS - (i + 1) * dayMs),
        );
        const windowResult = selectRecentCrossProjectSessions(
          collectCrossProjectSessions({
            allProjects: PROJECTS,
            projectSessions: { "/repo-b": [...eightRecent, ...fifteenStale] },
            filterQuery: "",
          }),
          { now: baseNow, pendingSessions: new Set() },
        );
        assertEqual(windowResult.items.length, 8);
        assertEqual(windowResult.hiddenCount, 15);

        // 3) Only 2 sessions within 7 days + 10 stale sessions -> 5-item fallback prevents empty/tiny list
        const fallbackResult = selectRecentCrossProjectSessions(
          collectCrossProjectSessions({
            allProjects: PROJECTS,
            projectSessions: { "/repo-b": [...eightRecent.slice(0, 2), ...fifteenStale.slice(0, 10)] },
            filterQuery: "",
          }),
          { now: baseNow, pendingSessions: new Set() },
        );
        assertEqual(fallbackResult.items.length, RECENT_TASKS_MIN_FALLBACK);
        assertEqual(fallbackResult.hiddenCount, 7);

        // 4) Urgent/active sessions older than 7 days and beyond rank 30 are unconditionally retained
        setWorkspace("statusBySession", { ses_old_busy: { type: "busy" } });
        setWorkspace("unreadCompletedBySession", { ses_old_unread: true });
        setWorkspace("sessionFinishedAt", { ses_stale_promoted: baseNow - 1_000 });
        const mixed = [
          ...burstToday,
          session("ses_old_busy", baseNow - 30 * dayMs),
          session("ses_old_approval", baseNow - 25 * dayMs),
          session("ses_old_unread", baseNow - 20 * dayMs),
          session("ses_stale_promoted", baseNow - 40 * dayMs),
        ];
        const urgentResult = selectRecentCrossProjectSessions(
          collectCrossProjectSessions({
            allProjects: PROJECTS,
            projectSessions: { "/repo-b": mixed },
            filterQuery: "",
          }),
          {
            now: baseNow,
            pendingSessions: new Set(["ses_old_approval"]),
          },
        );
        assertEqual(urgentResult.items.length, RECENT_TASKS_MAX);
        const keptIDs = urgentResult.items.map((item) => item.session.id);
        assertEqual(keptIDs[0], "ses_old_busy");
        assertEqual(keptIDs.includes("ses_old_approval"), true);
        assertEqual(keptIDs.includes("ses_old_unread"), true);
        assertEqual(keptIDs.includes("ses_stale_promoted"), true);

        // 5) Search bypasses 7-day cutoff and caps rendered matches at 30
        const fortyStaleMatches = Array.from({ length: 40 }, (_, i) =>
          session(`ses_match_${i}`, baseNow - (10 + i) * dayMs),
        );
        const searchResult = selectRecentCrossProjectSessions(
          collectCrossProjectSessions({
            allProjects: PROJECTS,
            projectSessions: { "/repo-b": fortyStaleMatches },
            filterQuery: "ses_match",
          }),
          { now: baseNow, query: "ses_match", pendingSessions: new Set() },
        );
        assertEqual(searchResult.items.length, 30);
        assertEqual(searchResult.totalMatches, 40);
        assertEqual(searchResult.isSearchTruncated, true);
      },
    );
  });

  it("caps subagent drawer items by default and resets paging on collapse", () => {
    withWorkspace(
      {
        statusBySession: { sub_14: { type: "busy" } },
      },
      () => {
        const subs = Array.from({ length: 16 }, (_, i) => session(`sub_${i}`, i + 1));
        clearSubagentDrawerOverride("ses_parent");
        assertEqual(getSubagentVisibleLimit("ses_parent"), DEFAULT_VISIBLE_SUBAGENTS);

        const initial = partitionVisibleSubagents(
          subs,
          getSubagentVisibleLimit("ses_parent"),
          new Set(["sub_15"]),
          "sub_13",
        );
        assertEqual(initial.visible.length, DEFAULT_VISIBLE_SUBAGENTS);
        assertEqual(initial.hiddenCount, 6);
        assertEqual(initial.visible.some((s) => s.id === "sub_13"), true);
        assertEqual(initial.visible.some((s) => s.id === "sub_14"), true);
        assertEqual(initial.visible.some((s) => s.id === "sub_15"), true);

        loadMoreSubagents("ses_parent");
        const expanded = partitionVisibleSubagents(
          subs,
          getSubagentVisibleLimit("ses_parent"),
          new Set(),
          null,
        );
        assertEqual(expanded.visible.length, 16);
        assertEqual(expanded.hiddenCount, 0);

        setSubagentDrawerExpanded("ses_parent", false, false);
        assertEqual(getSubagentVisibleLimit("ses_parent"), DEFAULT_VISIBLE_SUBAGENTS);
        clearSubagentDrawerOverride("ses_parent");
      },
    );
  });

  it("separates Recent Tasks tab state from Project View mode and migrates legacy stored mode", () => {
    withWorkspace(
      {
        sidebarNavTab: "projects",
        sidebarProjectMode: "single",
      },
      () => {
        switchSidebarNavTab("recent");
        assertEqual(workspace.sidebarNavTab, "recent");
        assertEqual(workspace.sidebarProjectMode, "single");

        setSidebarProjectMode("multi");
        assertEqual(workspace.sidebarNavTab, "projects");
        assertEqual(workspace.sidebarProjectMode, "multi");

        removeStorageItem(SIDEBAR_NAV_TAB_KEY);
        writeStorageItem(SIDEBAR_PROJECT_MODE_KEY, "recent");
        healTransientSidebarProjectMode();
        assertEqual(workspace.sidebarNavTab, "recent");
      },
    );
  });

  it("batches indexSessions updates into a single folder-surface flush and avoids stream-part thrash", () => {
    withWorkspace(
      {
        rootDir: "/repo-a",
        bootstrappingSessions: false,
        sessions: [session("ses_active", 100, { directory: "/repo-a", surface: "code" })],
        sessionIndex: {},
        messagesBySession: {
          ses_active: [
            {
              info: { id: "msg_1", sessionID: "ses_active", role: "assistant", time: { created: 1 } },
              parts: [],
            },
          ],
        },
      },
      () => {
        let surfaceEvaluations = 0;
        createRoot((dispose) => {
          const check = createMemo(() => {
            surfaceEvaluations += 1;
            return isWorkSurfacePath("/repo-batch");
          });
          assertEqual(check(), false);
          assertEqual(surfaceEvaluations, 1);

          const batchList = Array.from({ length: 120 }, (_, i) =>
            session(`ses_batch_${i}`, i + 1, { directory: "/repo-batch", surface: "code" }),
          );
          indexSessions(batchList);
          assertEqual(check(), false);
          // Without batching + coalesced surface revision, 120 first-seen sessions would trigger 120 flushes.
          assertEqual(surfaceEvaluations <= 2, true);
          dispose();
        });

        const cachedRepoB = [
          session("ses_b_root", 200, { directory: "/repo-b", surface: "code" }),
          session("ses_b_sub", 210, { parentID: "ses_b_root", directory: "/repo-b", surface: "code" }),
        ];
        let collectRuns = 0;
        createRoot((dispose) => {
          const collected = createMemo(() => {
            collectRuns += 1;
            return collectCrossProjectSessions({
              allProjects: PROJECTS,
              projectSessions: { "/repo-b": cachedRepoB },
              filterQuery: "",
            });
          });
          assertEqual(collected().length, 2);
          assertEqual(collectRuns, 1);

          // Streaming text or bash tool parts in the active session must not re-run cross-project partitioning.
          upsertMessagePart({
            id: "prt_text_1",
            sessionID: "ses_active",
            messageID: "msg_1",
            type: "text",
            text: "hello world",
          });
          upsertMessagePart({
            id: "prt_bash_1",
            sessionID: "ses_active",
            messageID: "msg_1",
            type: "tool",
            callID: "call_bash_1",
            tool: "bash",
            state: { status: "running", input: { command: "ls" }, title: "ls", time: { start: 1 } },
          });
          assertEqual(collected().length, 2);
          assertEqual(collectRuns, 1);

          assertDeepEqual(
            filterCrossProjectSessions(collected(), "repo b").map((i) => i.session.id),
            ["ses_b_root"],
          );
          dispose();
        });

        // Compact Recent Tasks mode collapses idle subagent drawers by default while honoring autoExpand & manual toggle.
        clearSubagentDrawerOverride("ses_compact");
        assertEqual(isSubagentDrawerExpanded("ses_compact", false, false), false);
        assertEqual(isSubagentDrawerExpanded("ses_compact", true, false), true);
        setSubagentDrawerExpanded("ses_compact", true, false);
        assertEqual(isSubagentDrawerExpanded("ses_compact", false, false), true);
        clearSubagentDrawerOverride("ses_compact");

        // Structural equality check on ProjectNavEntry lists prevents redundant downstream memo invalidation.
        assertEqual(
          sameProjectNavEntries([project("/repo-a", "Repo A")], [project("/repo-a", "Repo A")]),
          true,
        );
        assertEqual(
          sameProjectNavEntries([project("/repo-a", "Repo A")], [project("/repo-a", "Renamed")]),
          false,
        );
      },
    );
  });
});
