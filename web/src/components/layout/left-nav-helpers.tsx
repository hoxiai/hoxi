// web/src/components/layout/left-nav-helpers.tsx
import { Show } from "solid-js";
import { workspace } from "../../store/workspace";
import type { SessionInfo } from "../../services/sse-types";
import { DiffChanges, Icon, Spinner } from "../icons";

import { t } from "../../i18n";

export interface ProjectNavEntry {
  name: string;
  path: string;
  sandboxes: number;
  vcs: boolean;
  isPinned: boolean;
}

export type SessionBadge = "running" | "approval" | "idle";

export function dayLabel(ts: number): string {
  const d = new Date(ts);
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const dayMs = 86400000;
  if (ts >= startOfToday) return t("common.today");
  if (ts >= startOfToday - dayMs) return t("common.yesterday");
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function badgeFor(
  sessionID: string,
  pendingSessions: Set<string>,
  status: string | undefined
): SessionBadge {
  if (pendingSessions.has(sessionID)) return "approval";
  if (status === "busy") return "running";
  return "idle";
}

export function basename(p: string): string {
  const parts = p.split("/").filter(Boolean);
  return parts.length ? parts[parts.length - 1] : p || "workspace";
}

export function shortenPath(p: string): string {
  const home = "/Users/" + (p.split("/")[2] || "");
  if (p.startsWith(home)) {
    return "~" + p.slice(home.length);
  }
  return p;
}

export function diffStats(sessionID: string): { additions: number; deletions: number } | null {
  const diffs = workspace.diffsBySession[sessionID];
  if (!diffs || diffs.length === 0) return null;
  let additions = 0;
  let deletions = 0;
  for (const d of diffs) {
    additions += d.additions || 0;
    deletions += d.deletions || 0;
  }
  if (additions === 0 && deletions === 0) return null;
  return { additions, deletions };
}

export function buildAllProjects(): ProjectNavEntry[] {
  const map = new Map<string, ProjectNavEntry>();

  if (workspace.rootDir) {
    map.set(workspace.rootDir, {
      name: basename(workspace.rootDir),
      path: workspace.rootDir,
      sandboxes: 0,
      vcs: true,
      isPinned: true,
    });
  }

  for (const p of workspace.projects) {
    if (p.worktree && p.worktree !== "/") {
      const existing = map.get(p.worktree);
      if (!existing) {
        map.set(p.worktree, {
          name: basename(p.worktree),
          path: p.worktree,
          sandboxes: (p.sandboxes || []).length,
          vcs: p.vcs === "git",
          isPinned: false,
        });
      } else {
        existing.sandboxes = Math.max(existing.sandboxes, (p.sandboxes || []).length);
      }
    }
  }

  for (const p of workspace.recentProjects) {
    if (p && !map.has(p)) {
      map.set(p, {
        name: basename(p),
        path: p,
        sandboxes: 0,
        vcs: true,
        isPinned: false,
      });
    }
  }

  return Array.from(map.values());
}

export function SessionNavItem(props: {
  session: SessionInfo;
  active: boolean;
  canDelete: boolean;
  showBranchIcon?: boolean;
  pendingSessions: Set<string>;
  onSelect: () => void;
  onDiscard: (ev: MouseEvent, sessionID: string) => void;
}) {
  const status = () => workspace.statusBySession[props.session.id]?.type;
  const badge = () => badgeFor(props.session.id, props.pendingSessions, status());
  const wt = () => workspace.worktreeBySession[props.session.id];
  const stats = () => diffStats(props.session.id);

  return (
    <div
      class={`group/row flex items-center justify-between gap-1.5 rounded-lg px-2 py-1 cursor-pointer transition-colors ${
        props.active
          ? "bg-[var(--bg-hover)] text-[var(--text-primary)] font-medium"
          : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]/50 hover:text-[var(--text-primary)]"
      }`}
      onClick={() => props.onSelect()}
    >
      <div class="flex items-center gap-1.5 min-w-0 flex-1">
        <Show when={badge() === "approval"}>
          <span class="shrink-0 w-2 h-2 rounded-full bg-[var(--warn)] animate-pulse" />
        </Show>
        <Show when={badge() === "running"}>
          <span class="shrink-0 text-[var(--status-running)] inline-flex items-center">
            <Spinner class="w-3.5 h-3.5" />
          </span>
        </Show>
        <span class="truncate text-[12px] flex-1">
          {props.session.title || "New Session"}
        </span>
      </div>

      <div class="flex items-center gap-1.5 shrink-0 text-[10px] font-mono">
        <Show when={wt()?.branch}>
          <span class="inline-flex items-center gap-0.5 text-[var(--text-faint)] truncate max-w-[72px]">
            <Show when={props.showBranchIcon}>
              <Icon name="branch" size="small" class="w-3 h-3" />
            </Show>
            <span class="truncate">{wt()?.branch}</span>
          </span>
        </Show>

        <Show when={stats()}>
          <DiffChanges changes={stats()!} />
        </Show>

        <Show when={props.canDelete}>
          <button
            type="button"
            class="opacity-0 group-hover/row:opacity-100 text-[var(--text-faint)] hover:text-[var(--danger-text)] transition-opacity p-0.5 cursor-pointer"
            onClick={(ev) => props.onDiscard(ev, props.session.id)}
            title={t("leftNav.deleteSessionTooltip")}
          >
            <Icon name="trash" size="small" class="w-3 h-3" />
          </button>
        </Show>
      </div>
    </div>
  );
}
