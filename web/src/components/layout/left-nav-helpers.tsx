// web/src/components/layout/left-nav-helpers.tsx
import { Show } from "solid-js";
import {
  DEFAULT_WORKSPACE_IDENTITY_COLOR,
  isDefaultWorkspacePath,
  isDismissedProjectPath,
  isInvalidProjectPath,
  normalizeFolderPath,
  normalizeWorkspaceIdentityColor,
  resolveRootSessionID,
  shortenPath,
  stripWorktreePath,
  workspace,
  workspacePrimaryFolder,
  visibleSavedWorkspaces,
} from "../../store/workspace";
import { t } from "../../i18n";
import type { SessionInfo } from "../../services/sse-types";

export { shortenPath };

export interface ProjectNavEntry {
  workspaceId?: string;
  name: string;
  path: string;
  color: string;
  folders: Array<{ path: string; isPrimary: boolean }>;
  sandboxes: number;
  vcs: boolean;
  isPinned: boolean;
}

export type SessionBadge = "running" | "approval" | "completed" | "idle";

export function FolderIcon(props: { open?: boolean; color?: string; class?: string }) {
  const fill = () => props.color || DEFAULT_WORKSPACE_IDENTITY_COLOR;
  return (
    <svg
      class={props.class || "w-4 h-4 shrink-0"}
      viewBox="0 0 20 20"
      fill="none"
    >
      <path
        d={
          props.open
            ? "M2 5.5A1.5 1.5 0 013.5 4h3.879a1.5 1.5 0 011.06.44l1.122 1.12A1.5 1.5 0 0010.62 6H16.5A1.5 1.5 0 0118 7.5v1H3.25A1.25 1.25 0 002 9.75V5.5z"
            : "M2 5.5A1.5 1.5 0 013.5 4h3.879a1.5 1.5 0 011.06.44l1.122 1.12A1.5 1.5 0 0010.62 6H16.5A1.5 1.5 0 0118 7.5v7a1.5 1.5 0 01-1.5 1.5h-13A1.5 1.5 0 012 14.5v-9z"
        }
        fill={fill()}
        fill-opacity={props.open ? "0.85" : "0.9"}
      />
      <Show when={props.open}>
        <path
          d="M2.288 9.81A1 1 0 013.25 9h13.5a1 1 0 01.962 1.275l-1.428 5A1 1 0 0115.322 16H3.716a1 1 0 01-.962-.725l-1.428-5a1 1 0 01.962-1.275z"
          fill={fill()}
        />
      </Show>
    </svg>
  );
}

export function dayLabel(ts: number): string {
  const d = new Date(ts);
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const dayMs = 86400000;
  if (ts >= startOfToday) return t("common.today");
  if (ts >= startOfToday - dayMs) return t("common.yesterday");
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

const EMPTY_PENDING_SET = new Set<string>();

export function sameStringSet(a: Set<string>, b: Set<string>): boolean {
  if (a === b) return true;
  if (a.size !== b.size) return false;
  for (const item of a) {
    if (!b.has(item)) return false;
  }
  return true;
}

export function resolveActiveRootSessionID(): string | null {
  const activeID = workspace.activeSessionID;
  if (!activeID) return null;
  return resolveRootSessionID(activeID) || activeID;
}

export function collectPendingSessionIDs(): Set<string> {
  if (workspace.pendingPermissions.length === 0 && workspace.pendingQuestions.length === 0) {
    return EMPTY_PENDING_SET;
  }
  const set = new Set<string>();
  for (const p of workspace.pendingPermissions) {
    set.add(p.sessionID);
    const root = resolveRootSessionID(p.sessionID);
    if (root) set.add(root);
  }
  for (const q of workspace.pendingQuestions) {
    set.add(q.sessionID);
    const root = resolveRootSessionID(q.sessionID);
    if (root) set.add(root);
  }
  return set;
}

export function badgeFor(
  sessionID: string,
  pendingSessions: Set<string>,
  status: string | undefined
): SessionBadge {
  if (pendingSessions.has(sessionID)) return "approval";
  if (status === "busy") return "running";
  if (workspace.unreadCompletedBySession[sessionID]) return "completed";
  return "idle";
}

/**
 * Counts executing sessions that belong to one project, even when the user is
 * viewing a different project. Status is global, so the sidebar can show that
 * work is still running elsewhere without loading those projects' transcripts.
 */
export function countRunningSessionsForProject(
  projectPath: string,
  sessions: SessionInfo[],
  extraFolders?: Array<{ path: string } | string>
): number {
  const roots = new Set<string>();
  const addRoot = (p?: string | null) => {
    const clean = normalizeFolderPath(p);
    if (clean) roots.add(clean);
  };
  addRoot(projectPath);
  if (extraFolders) {
    for (const f of extraFolders) {
      addRoot(typeof f === "string" ? f : f?.path);
    }
  }
  if (roots.size === 0) return 0;
  const seen = new Set<string>();
  let count = 0;
  const isRunning = (id: string) => {
    const status = workspace.statusBySession[id]?.type;
    return status === "busy" || status === "retry";
  };
  for (const session of sessions) {
    if (session.parentID || !isRunning(session.id) || seen.has(session.id)) continue;
    seen.add(session.id);
    count++;
  }
  for (const [id, entry] of Object.entries(workspace.sessionIndex)) {
    if (entry.parentID || seen.has(id) || !isRunning(id)) continue;
    const dir = normalizeFolderPath(entry.directory);
    if (!dir) continue;
    let matches = false;
    for (const root of roots) {
      if (dir === root || dir.startsWith(`${root}/.hoxi/worktrees/`)) {
        matches = true;
        break;
      }
    }
    if (!matches) continue;
    seen.add(id);
    count++;
  }
  return count;
}

export function basename(p: string): string {
  const parts = p.split(/[\\/]+/).filter(Boolean);
  return parts.length ? parts[parts.length - 1] : p || "workspace";
}

export function buildAllProjects(): ProjectNavEntry[] {
  const map = new Map<string, ProjectNavEntry>();
  const secondaryPaths = new Set<string>();
  const savedList = visibleSavedWorkspaces();

  for (const saved of savedList) {
    const primary = normalizeFolderPath(workspacePrimaryFolder(saved));
    if (
      !primary ||
      isInvalidProjectPath(primary) ||
      isDefaultWorkspacePath(primary) ||
      isDismissedProjectPath(saved.id) ||
      isDismissedProjectPath(primary)
    ) {
      continue;
    }
    for (const f of saved.folders) {
      const fp = normalizeFolderPath(f.path);
      const stripped = stripWorktreePath(f.path);
      if (fp && fp !== primary) secondaryPaths.add(fp);
      if (stripped && stripped !== primary) secondaryPaths.add(stripped);
    }
  }

  for (const saved of savedList) {
    const primary = normalizeFolderPath(workspacePrimaryFolder(saved));
    if (
      !primary ||
      isInvalidProjectPath(primary) ||
      isDefaultWorkspacePath(primary) ||
      isDismissedProjectPath(saved.id) ||
      isDismissedProjectPath(primary) ||
      secondaryPaths.has(primary)
    ) {
      continue;
    }
    map.set(primary, {
      workspaceId: saved.id,
      name: saved.name || basename(primary),
      path: primary,
      color: normalizeWorkspaceIdentityColor(saved.color),
      folders: saved.folders.map((f) => ({ path: f.path, isPrimary: f.isPrimary })),
      sandboxes: 0,
      vcs: true,
      isPinned: primary === normalizeFolderPath(workspace.rootDir),
    });
  }

  for (const p of workspace.projects) {
    const wt = normalizeFolderPath(p.worktree);
    const strippedWt = stripWorktreePath(wt);
    if (
      !wt ||
      wt !== strippedWt ||
      isInvalidProjectPath(wt) ||
      isDefaultWorkspacePath(wt) ||
      isDismissedProjectPath(wt) ||
      secondaryPaths.has(wt)
    ) {
      continue;
    }
    const sandboxPaths = (p.sandboxes || [])
      .map((s) => stripWorktreePath(s))
      .filter(Boolean);
    const existing = map.get(wt);
    if (!existing) {
      if (
        sandboxPaths.some(
          (s) => map.has(s) || secondaryPaths.has(s) || isDismissedProjectPath(s)
        )
      ) {
        for (const s of sandboxPaths) {
          const owner = map.get(s);
          if (owner) {
            owner.sandboxes = Math.max(owner.sandboxes, (p.sandboxes || []).length);
          }
        }
        continue;
      }
      map.set(wt, {
        name: basename(wt),
        path: wt,
        color: normalizeWorkspaceIdentityColor(p.icon?.color),
        folders: [{ path: wt, isPrimary: true }],
        sandboxes: (p.sandboxes || []).length,
        vcs: p.vcs === "git",
        isPinned: wt === normalizeFolderPath(workspace.rootDir),
      });
    } else {
      existing.sandboxes = Math.max(existing.sandboxes, (p.sandboxes || []).length);
    }
  }

  for (const p of workspace.recentProjects) {
    const clean = normalizeFolderPath(p);
    if (
      !clean ||
      clean !== stripWorktreePath(clean) ||
      isInvalidProjectPath(clean) ||
      isDefaultWorkspacePath(clean) ||
      isDismissedProjectPath(clean) ||
      map.has(clean) ||
      secondaryPaths.has(clean)
    ) {
      continue;
    }
    map.set(clean, {
      name: basename(clean),
      path: clean,
      color: DEFAULT_WORKSPACE_IDENTITY_COLOR,
      folders: [{ path: clean, isPrimary: true }],
      sandboxes: 0,
      vcs: true,
      isPinned: clean === normalizeFolderPath(workspace.rootDir),
    });
  }

  const rootClean = normalizeFolderPath(workspace.rootDir);
  if (
    rootClean &&
    rootClean === stripWorktreePath(rootClean) &&
    !isInvalidProjectPath(rootClean) &&
    !isDefaultWorkspacePath(rootClean) &&
    !isDismissedProjectPath(rootClean) &&
    !map.has(rootClean) &&
    !secondaryPaths.has(rootClean)
  ) {
    map.set(rootClean, {
      name: basename(rootClean),
      path: rootClean,
      color: DEFAULT_WORKSPACE_IDENTITY_COLOR,
      folders: [{ path: rootClean, isPrimary: true }],
      sandboxes: 0,
      vcs: true,
      isPinned: true,
    });
  }

  const orderIndex = new Map<string, number>();
  workspace.projectOrder.forEach((p, idx) => {
    const clean = normalizeFolderPath(p);
    if (!orderIndex.has(clean)) orderIndex.set(clean, idx);
  });

  const entries = Array.from(map.values());
  entries.sort((a, b) => {
    const idxA = orderIndex.get(a.path);
    const idxB = orderIndex.get(b.path);
    if (idxA !== undefined && idxB !== undefined) return idxA - idxB;
    if (idxA !== undefined) return -1;
    if (idxB !== undefined) return 1;
    return 0;
  });

  return entries;
}

export function findProjectDropTarget(
  container: HTMLElement,
  clientY: number,
  fromPath: string
): { path: string; position: "before" | "after" } | null {
  const items = Array.from(
    container.querySelectorAll<HTMLElement>("[data-project-item]")
  );
  if (items.length === 0) return null;

  for (const itemEl of items) {
    const itemPath = itemEl.getAttribute("data-project-item");
    if (!itemPath) continue;
    const headerEl =
      itemEl.querySelector<HTMLElement>("[data-project-header]") || itemEl;
    const rect = headerEl.getBoundingClientRect();
    const itemRect = itemEl.getBoundingClientRect();

    if (clientY <= itemRect.bottom) {
      const midY = rect.top + rect.height / 2;
      const position = clientY < midY ? "before" : "after";
      return itemPath !== fromPath ? { path: itemPath, position } : null;
    }
  }

  const lastEl = items[items.length - 1];
  const lastPath = lastEl.getAttribute("data-project-item");
  if (lastPath && lastPath !== fromPath) {
    return { path: lastPath, position: "after" };
  }
  return null;
}

