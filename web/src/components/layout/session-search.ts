import type { SessionInfo } from "../../services/sse-types";

interface SearchProject {
  name: string;
  path: string;
  folders: ReadonlyArray<{ path: string }>;
}

export function matchesProjectSearch(project: SearchProject, query: string): boolean {
  const q = query.trim().toLowerCase();
  return !q || project.name.toLowerCase().includes(q) ||
    project.path.toLowerCase().includes(q) ||
    project.folders.some((folder) => folder.path.toLowerCase().includes(q));
}

export function matchesSessionQueryText(session: SessionInfo, query: string): boolean {
  const q = query.trim().toLowerCase();
  return !q ||
    (session.title || "").toLowerCase().includes(q) ||
    (session.slug || "").toLowerCase().includes(q);
}

export function matchesSessionSearch(
  session: SessionInfo,
  query: string,
  subagents?: readonly SessionInfo[],
): boolean {
  if (session.parentID) return false;
  if (matchesSessionQueryText(session, query)) return true;
  return Boolean(subagents?.some((sub) => !sub.time?.archived && matchesSessionQueryText(sub, query)));
}

export function matchesProjectSessionSearch(
  project: SearchProject,
  sessions: readonly SessionInfo[],
  query: string,
): boolean {
  if (matchesProjectSearch(project, query)) return true;
  const childrenByParent = new Map<string, SessionInfo[]>();
  for (const s of sessions) {
    if (s.parentID && !s.time?.archived) {
      const list = childrenByParent.get(s.parentID);
      if (list) list.push(s);
      else childrenByParent.set(s.parentID, [s]);
    }
  }
  return sessions.some((session) =>
    matchesSessionSearch(session, query, childrenByParent.get(session.id))
  );
}
