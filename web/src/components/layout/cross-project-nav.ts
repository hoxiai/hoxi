// web/src/components/layout/cross-project-nav.ts
// Pure projections for the cross-project sidebar views (Recent / Attention / Running).
// They read only already-cached session lists plus global status flags, so the
// sidebar can answer "what needs me now?" without loading any transcript.
import type { SessionInfo } from "../../services/sse-types";
import {
  isSessionArchived,
  normalizeFolderPath,
  partitionCodeSessions,
  sessionActivityTime,
  workspace,
} from "../../store/workspace";
import type { ProjectNavEntry } from "./left-nav-helpers";
import { compareSessionRunning } from "./session-history";
import { matchesProjectSearch, matchesSessionSearch } from "./session-search";

export const RECENT_TASKS_MAX = 30;
export const RECENT_TASKS_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;
export const RECENT_TASKS_MIN_FALLBACK = 5;
export const CROSS_PROJECT_SEARCH_MAX = 30;

export interface CrossProjectSession {
  session: SessionInfo;
  proj: ProjectNavEntry;
  subagents?: SessionInfo[];
}

export interface CrossProjectSource {
  allProjects: ProjectNavEntry[];
  projectSessions: Record<string, SessionInfo[]>;
  filterQuery: string;
}

export type AttentionSectionID = "approvals" | "completed" | "paused";

export interface AttentionSection {
  id: AttentionSectionID;
  items: CrossProjectSession[];
}

export interface RecentCrossProjectSelection {
  items: CrossProjectSession[];
  hiddenCount: number;
  totalMatches: number;
  isSearchTruncated: boolean;
}

function isRunning(sessionID: string): boolean {
  const status = workspace.statusBySession[sessionID]?.type;
  return status === "busy" || status === "retry";
}

function matchesQuery(item: CrossProjectSession, query: string): boolean {
  if (!query) return true;
  return (
    matchesSessionSearch(item.session, query, item.subagents) ||
    matchesProjectSearch(item.proj, query)
  );
}

/** Active root sessions live in `workspace.sessions`; other projects use the cache. */
function sessionsForProject(
  source: CrossProjectSource,
  proj: ProjectNavEntry,
  activeRoot: string,
): SessionInfo[] {
  const normPath = normalizeFolderPath(proj.path);
  if (normPath === activeRoot && !workspace.bootstrappingSessions) return workspace.sessions;
  return source.projectSessions[normPath] || source.projectSessions[proj.path] || [];
}

/** Root sessions across every project, excluding archived and subagent children. */
export function collectCrossProjectSessions(source: CrossProjectSource): CrossProjectSession[] {
  const query = source.filterQuery.trim().toLowerCase();
  const activeRoot = normalizeFolderPath(workspace.rootDir);
  const seen = new Set<string>();
  const out: CrossProjectSession[] = [];
  for (const proj of source.allProjects) {
    const { roots, subagentsByRoot } = partitionCodeSessions(
      sessionsForProject(source, proj, activeRoot),
    );
    for (const session of roots) {
      if (!session?.id || seen.has(session.id) || isSessionArchived(session)) continue;
      seen.add(session.id);
      const subagents = subagentsByRoot.get(session.id);
      const item: CrossProjectSession = subagents ? { session, proj, subagents } : { session, proj };
      if (!matchesQuery(item, query)) continue;
      out.push(item);
    }
  }
  return out;
}

/**
 * Selects bounded cross-project Recent Tasks:
 * - Always exempts running, pending-approval, unread-completed, and active sessions.
 * - Normal view: keeps sessions active within 7 days, with a 5-item fallback so
 *   returning after a break never renders blank, and caps total at 30.
 * - Search view: bypasses the 7-day window and caps rendered matches at 30.
 */
export function selectRecentCrossProjectSessions(
  items: readonly CrossProjectSession[],
  options: {
    now?: number;
    query?: string;
    pendingSessions: ReadonlySet<string>;
    activeRootSessionID?: string | null;
  },
): RecentCrossProjectSelection {
  if (items.length === 0) {
    return { items: [], hiddenCount: 0, totalMatches: 0, isSearchTruncated: false };
  }
  const now = options.now ?? Date.now();
  const searching = Boolean(options.query?.trim());
  const activeRoot = normalizeFolderPath(workspace.rootDir);
  const activeID = options.activeRootSessionID ?? null;

  const sorted = [...items].sort(
    (a, b) =>
      compareSessionRunning(a.session, b.session, workspace.statusBySession) ||
      sessionActivityTime(b.session) - sessionActivityTime(a.session),
  );

  const protectedIDs = new Set<string>();
  for (const item of sorted) {
    const id = item.session.id;
    const isActiveRow =
      Boolean(activeID && id === activeID) &&
      normalizeFolderPath(item.proj.path) === activeRoot;
    const isUrgent =
      isActiveRow ||
      isRunning(id) ||
      options.pendingSessions.has(id) ||
      Boolean(workspace.unreadCompletedBySession[id]) ||
      Boolean(
        item.subagents?.some(
          (sub) => isRunning(sub.id) || options.pendingSessions.has(sub.id),
        ),
      );
    if (isUrgent) protectedIDs.add(id);
  }

  const cap = searching ? CROSS_PROJECT_SEARCH_MAX : RECENT_TASKS_MAX;
  const maxUnprotected = Math.max(0, cap - protectedIDs.size);
  const minFallback = Math.max(0, RECENT_TASKS_MIN_FALLBACK - protectedIDs.size);
  const cutoff = now - RECENT_TASKS_WINDOW_MS;

  let unprotectedKept = 0;
  const selected: CrossProjectSession[] = [];
  for (const item of sorted) {
    if (protectedIDs.has(item.session.id)) {
      selected.push(item);
      continue;
    }
    if (unprotectedKept >= maxUnprotected) continue;
    if (
      searching ||
      sessionActivityTime(item.session) >= cutoff ||
      unprotectedKept < minFallback
    ) {
      selected.push(item);
      unprotectedKept++;
    }
  }

  const hiddenCount = Math.max(0, sorted.length - selected.length);
  return {
    items: selected,
    hiddenCount,
    totalMatches: sorted.length,
    isSearchTruncated: searching && hiddenCount > 0,
  };
}

export interface RunningGroup {
  proj: ProjectNavEntry;
  sessions: SessionInfo[];
  subagentsByRoot?: Map<string, SessionInfo[]>;
}

/** Executing tasks grouped by project, newest activity first inside each group. */
export function groupRunningByProject(items: CrossProjectSession[]): RunningGroup[] {
  const groups = new Map<string, RunningGroup>();
  const running = items.filter(
    (item) =>
      isRunning(item.session.id) ||
      Boolean(item.subagents?.some((sub) => isRunning(sub.id))),
  );
  running.sort((a, b) => sessionActivityTime(b.session) - sessionActivityTime(a.session));
  for (const item of running) {
    const key = normalizeFolderPath(item.proj.path);
    let group = groups.get(key);
    if (!group) {
      group = { proj: item.proj, sessions: [], subagentsByRoot: new Map() };
      groups.set(key, group);
    }
    group.sessions.push(item.session);
    if (item.subagents && item.subagents.length > 0) {
      group.subagentsByRoot?.set(item.session.id, item.subagents);
    }
  }
  return Array.from(groups.values());
}

const ATTENTION_ORDER: AttentionSectionID[] = ["approvals", "completed", "paused"];

/**
 * Splits sessions into the three reasons the sidebar should pull the user back:
 * a pending approval, an unread completion, or an interrupted (paused/stopped)
 * turn. Each session appears once, in the most urgent section.
 */
export function partitionAttentionSessions(
  items: CrossProjectSession[],
  pendingSessions: Set<string>,
): AttentionSection[] {
  const buckets: Record<AttentionSectionID, CrossProjectSession[]> = {
    approvals: [],
    completed: [],
    paused: [],
  };
  for (const item of items) {
    const id = item.session.id;
    const hasPendingApproval =
      pendingSessions.has(id) ||
      Boolean(item.subagents?.some((sub) => pendingSessions.has(sub.id)));
    if (hasPendingApproval) buckets.approvals.push(item);
    else if (workspace.unreadCompletedBySession[id]) buckets.completed.push(item);
    else if ((!isRunning(id) && workspace.interruptedBySession[id] === "paused") || workspace.stalledSinceBySession[id]) {
      buckets.paused.push(item);
    }
  }
  for (const id of ATTENTION_ORDER) {
    buckets[id].sort(
      (a, b) =>
        compareSessionRunning(a.session, b.session, workspace.statusBySession) ||
        sessionActivityTime(b.session) - sessionActivityTime(a.session),
    );
  }
  return ATTENTION_ORDER.filter((id) => buckets[id].length > 0).map((id) => ({
    id,
    items: buckets[id],
  }));
}
