// web/src/components/layout/left-nav-helpers.tsx
import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import { Pencil } from "lucide-solid";
import {
  DEFAULT_WORKSPACE_IDENTITY_COLOR,
  isDismissedProjectPath,
  isPlaceholderSessionTitle,
  normalizeFolderPath,
  normalizeWorkspaceIdentityColor,
  renameSessionTitle,
  workspace,
  workspacePrimaryFolder,
} from "../../store/workspace";
import type { SessionInfo } from "../../services/sse-types";
import { DiffChanges, Icon, Spinner } from "../icons";

import { t } from "../../i18n";

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

export type SessionBadge = "running" | "approval" | "idle";

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
  const secondaryPaths = new Set<string>();

  for (const saved of workspace.savedWorkspaces) {
    const primary = normalizeFolderPath(workspacePrimaryFolder(saved));
    if (!primary || isDismissedProjectPath(saved.id) || isDismissedProjectPath(primary)) continue;
    for (const f of saved.folders) {
      const fp = normalizeFolderPath(f.path);
      if (fp && fp !== primary) secondaryPaths.add(fp);
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
    if (!wt || wt === "/" || isDismissedProjectPath(wt) || secondaryPaths.has(wt)) continue;
    const existing = map.get(wt);
    if (!existing) {
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
    if (!clean || isDismissedProjectPath(clean) || map.has(clean) || secondaryPaths.has(clean)) continue;
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
  if (rootClean && !isDismissedProjectPath(rootClean) && !map.has(rootClean) && !secondaryPaths.has(rootClean)) {
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

export function SessionNavItem(props: {
  session: SessionInfo;
  active: boolean;
  canDelete: boolean;
  isArchived?: boolean;
  showBranchIcon?: boolean;
  pendingSessions: Set<string>;
  onSelect: () => void;
  onArchive?: (ev: MouseEvent, sessionID: string) => void;
  onUnarchive?: (ev: MouseEvent, sessionID: string) => void;
  onDiscard: (ev: MouseEvent, sessionID: string) => Promise<void> | void;
}) {
  const [editing, setEditing] = createSignal(false);
  const [draftTitle, setDraftTitle] = createSignal("");
  const [confirmingDelete, setConfirmingDelete] = createSignal(false);
  const [deleting, setDeleting] = createSignal(false);
  let rowRef: HTMLDivElement | undefined;
  let inputRef: HTMLInputElement | undefined;

  const status = () => workspace.statusBySession[props.session.id]?.type;
  const badge = () => badgeFor(props.session.id, props.pendingSessions, status());
  const wt = () => workspace.worktreeBySession[props.session.id];
  const stats = () => diffStats(props.session.id);

  createEffect(() => {
    if (!confirmingDelete()) return;
    const handlePointerDown = (ev: MouseEvent) => {
      if (rowRef && !rowRef.contains(ev.target as Node)) {
        setConfirmingDelete(false);
      }
    };
    const handleKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") {
        setConfirmingDelete(false);
      }
    };
    window.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    onCleanup(() => {
      window.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    });
  });

  const startEditing = (ev?: MouseEvent) => {
    ev?.stopPropagation();
    setConfirmingDelete(false);
    setDraftTitle(props.session.title || "");
    setEditing(true);
    requestAnimationFrame(() => {
      inputRef?.focus();
      inputRef?.select();
    });
  };

  const commitEdit = () => {
    if (!editing()) return;
    setEditing(false);
    const next = draftTitle().trim();
    if (next && next !== props.session.title) {
      void renameSessionTitle(props.session.id, next);
    }
  };

  const handleConfirmDelete = async (ev: MouseEvent) => {
    ev.stopPropagation();
    if (deleting()) return;
    setDeleting(true);
    try {
      await props.onDiscard(ev, props.session.id);
    } finally {
      setDeleting(false);
      setConfirmingDelete(false);
    }
  };

  return (
    <div
      ref={rowRef}
      class={`group/row relative flex items-center justify-between gap-1 rounded-lg px-2 py-1 cursor-pointer transition-colors overflow-hidden ${
        confirmingDelete()
          ? "bg-[var(--danger-surface)] text-[var(--text-primary)] ring-1 ring-[var(--danger-border)]"
          : props.active
            ? "bg-[var(--bg-hover)] text-[var(--text-primary)] font-medium"
            : "text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]/60 hover:text-[var(--text-primary)]"
      }`}
      onClick={() => {
        if (!editing() && !confirmingDelete()) props.onSelect();
      }}
      onDblClick={(ev) => startEditing(ev)}
    >
      <div class="flex items-center gap-1.5 min-w-0 flex-1 mr-1">
        <Show when={badge() === "approval"}>
          <span class="shrink-0 w-2 h-2 rounded-full bg-[var(--warn)] animate-pulse" />
        </Show>
        <Show when={badge() === "running"}>
          <span class="shrink-0 text-[var(--status-running)] inline-flex items-center">
            <Spinner class="w-3.5 h-3.5" />
          </span>
        </Show>
        <Show
          when={!editing()}
          fallback={
            <input
              ref={inputRef}
              type="text"
              value={draftTitle()}
              placeholder={t("leftNav.renamePlaceholder")}
              onInput={(ev) => setDraftTitle(ev.currentTarget.value)}
              onClick={(ev) => ev.stopPropagation()}
              onBlur={commitEdit}
              onKeyDown={(ev) => {
                if (ev.key === "Enter" && !ev.isComposing) {
                  ev.preventDefault();
                  commitEdit();
                } else if (ev.key === "Escape") {
                  ev.preventDefault();
                  setEditing(false);
                }
              }}
              class="flex-1 min-w-0 rounded border border-[var(--accent-border)] bg-[var(--bg-panel)] px-1.5 py-0.5 text-[12.5px] text-[var(--text-primary)] outline-none"
            />
          }
        >
          <span
            class={`truncate text-[13.5px] flex-1 leading-snug ${
              props.isArchived ? "text-[var(--text-muted)]" : ""
            }`}
            title={props.session.title || ""}
          >
            {isPlaceholderSessionTitle(props.session.title)
              ? t("leftNav.newThread")
              : props.session.title}
          </span>
        </Show>
      </div>

      {/* Static Badges (Branch & Diff) shown when not hovered and not confirming delete */}
      <Show when={!confirmingDelete() && !editing()}>
        <div class="flex items-center gap-1 shrink-0 text-[11px] font-mono group-hover/row:opacity-0 transition-opacity">
          <Show when={wt()?.isolated && wt()?.branch}>
            <span class="inline-flex items-center gap-0.5 text-[var(--text-faint)] truncate max-w-[64px]">
              <Show when={props.showBranchIcon}>
                <Icon name="branch" size="small" class="w-3 h-3" />
              </Show>
              <span class="truncate">{wt()?.branch}</span>
            </span>
          </Show>

          <Show when={stats()}>
            <DiffChanges changes={stats()!} />
          </Show>
        </div>
      </Show>

      {/* Hover Actions / Delete Gate Overlay: solid floating action pill covering text cleanly */}
      <Show when={!editing()}>
        <div
          class={`absolute right-1 top-1/2 -translate-y-1/2 flex items-center z-10 transition-all ${
            confirmingDelete()
              ? "opacity-100 pointer-events-auto"
              : "opacity-0 pointer-events-none group-hover/row:opacity-100 group-hover/row:pointer-events-auto"
          }`}
          onClick={(ev) => ev.stopPropagation()}
        >
          <Show
            when={!confirmingDelete()}
            fallback={
              <div class="flex items-center gap-1 rounded-md border border-[var(--danger-border)] bg-[var(--danger-surface)] px-1.5 py-0.5 shadow-md">
                <button
                  type="button"
                  disabled={deleting()}
                  onClick={(ev) => void handleConfirmDelete(ev)}
                  class="inline-flex items-center gap-0.5 rounded bg-[var(--danger-text)] px-1.5 py-0.5 text-[11px] font-medium text-white hover:opacity-90 transition-opacity cursor-pointer shadow-xs"
                  title={t("leftNav.discardConfirm")}
                >
                  <Show
                    when={!deleting()}
                    fallback={<Spinner class="w-2.5 h-2.5 text-white" />}
                  >
                    <span>{t("leftNav.confirmDelete")}</span>
                  </Show>
                </button>
                <button
                  type="button"
                  disabled={deleting()}
                  onClick={(ev) => {
                    ev.stopPropagation();
                    setConfirmingDelete(false);
                  }}
                  class="rounded px-1.5 py-0.5 text-[11px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                >
                  {t("leftNav.cancelDelete")}
                </button>
              </div>
            }
          >
            <div class="flex items-center gap-0.5 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-panel)] px-1 py-0.5 shadow-md">
              <button
                type="button"
                class="inline-flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                onClick={(ev) => startEditing(ev)}
                title={t("leftNav.renameSessionTooltip")}
              >
                <Pencil size={11} class="shrink-0 block" />
              </button>

              <Show when={!props.isArchived && props.onArchive}>
                <button
                  type="button"
                  class="inline-flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  onClick={(ev) => {
                    ev.stopPropagation();
                    props.onArchive?.(ev, props.session.id);
                  }}
                  title={t("leftNav.archiveSessionTooltip")}
                >
                  <Icon name="archive" size="small" class="w-3 h-3" />
                </button>
              </Show>

              <Show when={props.isArchived && props.onUnarchive}>
                <button
                  type="button"
                  class="inline-flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
                  onClick={(ev) => {
                    ev.stopPropagation();
                    props.onUnarchive?.(ev, props.session.id);
                  }}
                  title={t("leftNav.unarchiveSessionTooltip")}
                >
                  <Icon name="reset" size="small" class="w-3 h-3" />
                </button>
              </Show>

              <Show when={props.canDelete}>
                <button
                  type="button"
                  class="inline-flex h-5 w-5 items-center justify-center rounded text-[var(--text-muted)] hover:text-[var(--danger-text)] hover:bg-[var(--danger-surface)] transition-colors cursor-pointer"
                  onClick={(ev) => {
                    ev.stopPropagation();
                    setConfirmingDelete(true);
                  }}
                  title={t("leftNav.deleteSessionTooltip")}
                >
                  <Icon name="trash" size="small" class="w-3 h-3" />
                </button>
              </Show>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}
