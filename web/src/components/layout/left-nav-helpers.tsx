// web/src/components/layout/left-nav-helpers.tsx
import { Show } from "solid-js";
import { Folder, FolderOpen } from "lucide-solid";
import {
  DEFAULT_WORKSPACE_IDENTITY_COLOR,
  isActionableApprovalSession,
  isDefaultWorkspacePath,
  isDismissedProjectPath,
  isInvalidProjectPath,
  isWorkSurfacePath,
  normalizeFolderPath,
  normalizeWorkspaceIdentityColor,
  resolveApprovalRequestSurface,
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
  folders: Array<{ path: string; isPrimary: boolean; missing?: boolean }>;
  sandboxes: number;
  vcs: boolean;
  isPinned: boolean;
}

export type SessionBadge = "running" | "approval" | "completed" | "idle";

export function FolderIcon(props: { open?: boolean; color?: string; class?: string }) {
  const strokeColor = () => {
    const c = props.color?.trim();
    if (!c || c.toUpperCase() === DEFAULT_WORKSPACE_IDENTITY_COLOR) {
      return "var(--accent-primary)";
    }
    return c;
  };
  return (
    <Show
      when={props.open}
      fallback={
        <Folder
          class={props.class || "w-4 h-4 shrink-0"}
          style={{ color: strokeColor() }}
          strokeWidth={1.85}
        />
      }
    >
      <FolderOpen
        class={props.class || "w-4 h-4 shrink-0"}
        style={{ color: strokeColor() }}
        strokeWidth={1.85}
      />
    </Show>
  );
}

let cachedShortDayFormatter: Intl.DateTimeFormat | null = null;
let cachedTodayStartMs = 0;
let cachedTodayEndMs = 0;

function startOfTodayMs(nowMs: number): number {
  if (nowMs >= cachedTodayStartMs && nowMs < cachedTodayEndMs) {
    return cachedTodayStartMs;
  }
  const now = new Date(nowMs);
  cachedTodayStartMs = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  cachedTodayEndMs = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).getTime();
  return cachedTodayStartMs;
}

export function dayLabel(ts: number): string {
  const startOfToday = startOfTodayMs(Date.now());
  const dayMs = 86400000;
  if (ts >= startOfToday) return t("common.today");
  if (ts >= startOfToday - dayMs) return t("common.yesterday");
  try {
    cachedShortDayFormatter ??= new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" });
    return cachedShortDayFormatter.format(ts);
  } catch {
    return new Date(ts).toLocaleDateString(undefined, { month: "short", day: "numeric" });
  }
}

export function sameProjectNavEntries(
  a: readonly ProjectNavEntry[],
  b: readonly ProjectNavEntry[],
): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const x = a[i];
    const y = b[i];
    if (
      x.workspaceId !== y.workspaceId ||
      x.name !== y.name ||
      x.path !== y.path ||
      x.color !== y.color ||
      x.sandboxes !== y.sandboxes ||
      x.vcs !== y.vcs ||
      x.isPinned !== y.isPinned ||
      x.folders.length !== y.folders.length
    ) {
      return false;
    }
    for (let j = 0; j < x.folders.length; j++) {
      const fx = x.folders[j];
      const fy = y.folders[j];
      if (fx.path !== fy.path || fx.isPrimary !== fy.isPrimary || fx.missing !== fy.missing) {
        return false;
      }
    }
  }
  return true;
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

export function collectPendingSessionIDs(surface?: "code" | "work"): Set<string> {
  if (workspace.pendingPermissions.length === 0 && workspace.pendingQuestions.length === 0) {
    return EMPTY_PENDING_SET;
  }
  const set = new Set<string>();
  for (const p of workspace.pendingPermissions) {
    if (!isActionableApprovalSession(p?.sessionID)) continue;
    if (surface && resolveApprovalRequestSurface(p) !== surface) continue;
    set.add(p.sessionID);
    const subID =
      (typeof p.subagentSessionID === "string" && p.subagentSessionID.trim()) ||
      (typeof p.metadata?.subagentSessionID === "string" && p.metadata.subagentSessionID.trim()) ||
      "";
    if (subID) set.add(subID);
    const root = resolveRootSessionID(p.sessionID);
    if (root) set.add(root);
  }
  for (const q of workspace.pendingQuestions) {
    if (!isActionableApprovalSession(q?.sessionID)) continue;
    if (surface && resolveApprovalRequestSurface(q) !== surface) continue;
    set.add(q.sessionID);
    if (typeof q.subagentSessionID === "string" && q.subagentSessionID.trim()) {
      set.add(q.subagentSessionID.trim());
    }
    const root = resolveRootSessionID(q.sessionID);
    if (root) set.add(root);
  }
  return set.size > 0 ? set : EMPTY_PENDING_SET;
}

export function collectPendingApprovalRoots(surface?: "code" | "work"): Set<string> {
  if (workspace.pendingPermissions.length === 0 && workspace.pendingQuestions.length === 0) {
    return EMPTY_PENDING_SET;
  }
  const roots = new Set<string>();
  for (const p of workspace.pendingPermissions) {
    if (!isActionableApprovalSession(p?.sessionID)) continue;
    if (surface && resolveApprovalRequestSurface(p) !== surface) continue;
    roots.add(resolveRootSessionID(p.sessionID) || p.sessionID);
  }
  for (const q of workspace.pendingQuestions) {
    if (!isActionableApprovalSession(q?.sessionID)) continue;
    if (surface && resolveApprovalRequestSurface(q) !== surface) continue;
    roots.add(resolveRootSessionID(q.sessionID) || q.sessionID);
  }
  return roots.size > 0 ? roots : EMPTY_PENDING_SET;
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
  for (const [id, statusEntry] of Object.entries(workspace.statusBySession)) {
    if (statusEntry?.type !== "busy" && statusEntry?.type !== "retry") continue;
    if (seen.has(id)) continue;
    const entry = workspace.sessionIndex[id];
    if (!entry || entry.parentID) continue;
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

export function buildAllProjects(surface: "code" | "work" = "code"): ProjectNavEntry[] {
  const map = new Map<string, ProjectNavEntry>();
  const secondaryPaths = new Set<string>();
  const savedList = visibleSavedWorkspaces(surface);
  const rejectForSurface = (p: string) =>
    surface === "work" ? isDefaultWorkspacePath(p) : isWorkSurfacePath(p);

  for (const saved of savedList) {
    const primary = normalizeFolderPath(workspacePrimaryFolder(saved));
    if (
      !primary ||
      isInvalidProjectPath(primary) ||
      rejectForSurface(primary) ||
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
      rejectForSurface(primary) ||
      isDismissedProjectPath(saved.id) ||
      isDismissedProjectPath(primary)
    ) {
      continue;
    }
    // A saved workspace keeps its own row even when another workspace lists
    // its primary folder as a secondary folder.
    map.set(primary, {
      workspaceId: saved.id,
      name: saved.name || basename(primary),
      path: primary,
      color: normalizeWorkspaceIdentityColor(saved.color),
      folders: saved.folders.map((f) => ({
        path: f.path,
        isPrimary: f.isPrimary,
        missing: f.missing,
      })),
      sandboxes: 0,
      vcs: true,
      isPinned: primary === normalizeFolderPath(workspace.rootDir),
    });
  }

  if (surface === "work") {
    const rootClean = normalizeFolderPath(workspace.rootDir);
    if (
      rootClean &&
      rootClean === stripWorktreePath(rootClean) &&
      !isInvalidProjectPath(rootClean) &&
      !isDefaultWorkspacePath(rootClean) &&
      isWorkSurfacePath(rootClean) &&
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
    return Array.from(map.values());
  }

  for (const p of workspace.projects) {
    const wt = normalizeFolderPath(p.worktree);
    const strippedWt = stripWorktreePath(wt);
    if (
      !wt ||
      wt !== strippedWt ||
      isInvalidProjectPath(wt) ||
      isWorkSurfacePath(wt) ||
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
      isWorkSurfacePath(clean) ||
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
    !isWorkSurfacePath(rootClean) &&
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

