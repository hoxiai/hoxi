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

export function matchesSessionSearch(session: SessionInfo, query: string): boolean {
  const q = query.trim().toLowerCase();
  return !session.parentID && (!q ||
    (session.title || "").toLowerCase().includes(q) ||
    (session.slug || "").toLowerCase().includes(q));
}

export function matchesProjectSessionSearch(
  project: SearchProject,
  sessions: readonly SessionInfo[],
  query: string,
): boolean {
  return matchesProjectSearch(project, query) ||
    sessions.some((session) => matchesSessionSearch(session, query));
}
