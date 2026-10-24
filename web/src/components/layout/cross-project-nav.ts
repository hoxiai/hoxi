// web/src/components/layout/cross-project-nav.ts
// Pure projections for the cross-project sidebar views (Attention / Running).
// They read only already-cached session lists plus global status flags, so the
// sidebar can answer "what needs me now?" without loading any transcript.
import type { SessionInfo } from "../../services/sse-types";
import {
  isSessionArchived,
  normalizeFolderPath,
  resolveSessionSurface,
  sessionActivityTime,
  workspace,
} from "../../store/workspace";
import type { ProjectNavEntry } from "./left-nav-helpers";
import { compareSessionRunning } from "./session-history";
import { matchesProjectSearch, matchesSessionSearch } from "./session-search";

export interface CrossProjectSession {
  session: SessionInfo;
  proj: ProjectNavEntry;
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

function isRunning(sessionID: string): boolean {
  const status = workspace.statusBySession[sessionID]?.type;
  return status === "busy" || status === "retry";
}

function matchesQuery(item: CrossProjectSession, query: string): boolean {
  if (!query) return true;
  return matchesSessionSearch(item.session, query) || matchesProjectSearch(item.proj, query);
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
    for (const session of sessionsForProject(source, proj, activeRoot)) {
      if (!session?.id || seen.has(session.id) || session.parentID || isSessionArchived(session) || resolveSessionSurface(session) !== "code") continue;
      seen.add(session.id);
      const item = { session, proj };
      if (!matchesQuery(item, query)) continue;
      out.push(item);
    }
  }
  return out;
}

export interface RunningGroup {
  proj: ProjectNavEntry;
  sessions: SessionInfo[];
}

/** Executing tasks grouped by project, newest activity first inside each group. */
export function groupRunningByProject(items: CrossProjectSession[]): RunningGroup[] {
  const groups = new Map<string, RunningGroup>();
  const running = items.filter((item) => isRunning(item.session.id));
  running.sort((a, b) => sessionActivityTime(b.session) - sessionActivityTime(a.session));
  for (const item of running) {
    const key = normalizeFolderPath(item.proj.path);
    const group = groups.get(key);
    if (group) group.sessions.push(item.session);
    else groups.set(key, { proj: item.proj, sessions: [item.session] });
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
    if (pendingSessions.has(id)) buckets.approvals.push(item);
    else if (workspace.unreadCompletedBySession[id]) buckets.completed.push(item);
    else if ((!isRunning(id) && workspace.interruptedBySession[id]) || workspace.stalledSinceBySession[id]) {
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
