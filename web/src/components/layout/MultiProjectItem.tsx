import { createEffect, createMemo, createSelector, createSignal, For, onCleanup, Show } from "solid-js";
import { GripVertical, MoreHorizontal, FolderPlus, Pencil, Layers, Trash2, Plus } from "lucide-solid";
import {
  isSessionArchived,
  normalizeFolderPath,
  openCreateWorkspaceModal,
  openDeleteWorkspaceModal,
  openEditWorkspaceModal,
  openManageWorkspacesModal,
  quickAddFolderToWorkspace,
  setWorkspace,
  sortSessionsByActivity,
  switchWorkspace,
  workspace,
} from "../../store/workspace";
import type { SessionInfo } from "../../services/sse-types";
import { Icon, Spinner } from "../icons";
import {
  basename,
  FolderIcon,
  resolveActiveRootSessionID,
  SessionNavItem,
  shortenPath,
  type ProjectNavEntry,
} from "./left-nav-helpers";
import { t } from "../../i18n";
import { SessionHistoryList, useSessionHistory } from "./SessionHistory";

export function WorkspaceActionMenu(props: {
  dirOrWorkspaceID: string;
  alwaysVisible?: boolean;
}) {
  const [open, setOpen] = createSignal(false);
  let menuRef: HTMLDivElement | undefined;
  let btnRef: HTMLButtonElement | undefined;

  createEffect(() => {
    if (!open()) return;
    const handlePointerDown = (e: MouseEvent) => {
      const target = e.target as Node;
      if (
        menuRef &&
        !menuRef.contains(target) &&
        btnRef &&
        !btnRef.contains(target)
      ) {
        setOpen(false);
      }
    };
    window.addEventListener("pointerdown", handlePointerDown);
    onCleanup(() => window.removeEventListener("pointerdown", handlePointerDown));
  });

  return (
    <div class="relative inline-flex items-center shrink-0" onClick={(e) => e.stopPropagation()}>
      <button
        ref={btnRef}
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          setOpen((v) => !v);
        }}
        class={`inline-flex h-5 w-5 shrink-0 items-center justify-center rounded text-[var(--text-faint)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-opacity cursor-pointer ${
          props.alwaysVisible || open()
            ? "opacity-100"
            : "opacity-0 group-hover/proj:opacity-100"
        }`}
        title={t("leftNav.workspaceActions")}
      >
        <MoreHorizontal size={13} class="block shrink-0" />
      </button>

      <Show when={open()}>
        <div
          ref={menuRef}
          class="absolute right-0 top-6 w-48 rounded-xl border border-[var(--border-strong)] bg-[var(--bg-elevated)] p-1 shadow-xl z-50 animate-in fade-in zoom-in-95 duration-100"
        >
          <button
            type="button"
            onClick={() => {
              setOpen(false);
              openEditWorkspaceModal(props.dirOrWorkspaceID);
            }}
            class="w-full flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Pencil size={13} />
            <span>{t("leftNav.editWorkspace")}</span>
          </button>

          <button
            type="button"
            onClick={() => {
              setOpen(false);
              void quickAddFolderToWorkspace(props.dirOrWorkspaceID);
            }}
            class="w-full flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <FolderPlus size={13} />
            <span>{t("leftNav.addFolderToWorkspace")}</span>
          </button>

          <button
            type="button"
            onClick={() => {
              setOpen(false);
              openCreateWorkspaceModal();
            }}
            class="w-full flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Plus size={13} />
            <span>{t("leftNav.newWorkspace")}</span>
          </button>

          <button
            type="button"
            onClick={() => {
              setOpen(false);
              openManageWorkspacesModal();
            }}
            class="w-full flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[12px] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] hover:text-[var(--text-primary)] transition-colors cursor-pointer"
          >
            <Layers size={13} />
            <span>{t("leftNav.manageWorkspaces")}</span>
          </button>

          <div class="my-1 border-t border-[var(--border-subtle)]/60" />

          <button
            type="button"
            onClick={() => {
              setOpen(false);
              openDeleteWorkspaceModal(props.dirOrWorkspaceID);
            }}
            class="w-full flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[12px] text-[var(--danger-text)] hover:bg-[var(--danger-surface)] transition-colors cursor-pointer"
          >
            <Trash2 size={13} />
            <span>{t("leftNav.deleteWorkspace")}</span>
          </button>
        </div>
      </Show>
    </div>
  );
}

export default function MultiProjectTreeItem(props: {
  proj: ProjectNavEntry;
  expanded: boolean;
  isCreating: boolean;
  isLoadingSessions: boolean;
  isDraggingThis: boolean;
  isDropBefore: boolean;
  isDropAfter: boolean;
  isArchivedOpen: boolean;
  filterQuery: string;
  cachedSessions: SessionInfo[];
  pendingSessions: Set<string>;
  onPointerDown: (ev: PointerEvent, fromPath: string) => void;
  onToggleProject: (path: string) => void;
  onToggleArchived: (path: string) => void;
  onCreateThread: (e: MouseEvent, path: string) => void;
  onSelectSession: (path: string, sessionID: string) => void;
  onArchiveSession: (path: string, ev: MouseEvent, sessionID: string) => void;
  onUnarchiveSession: (path: string, ev: MouseEvent, sessionID: string) => void;
  onDiscardSession: (path: string, ev: MouseEvent, sessionID: string) => Promise<void> | void;
}) {
  const isActive = () =>
    normalizeFolderPath(props.proj.path) === normalizeFolderPath(workspace.rootDir);
  const isSelectedSession = createSelector<string | null, string>(() =>
    isActive() ? resolveActiveRootSessionID() : null
  );

  const allFilteredSessions = createMemo(() => {
    if (!props.expanded) return [];
    const q = props.filterQuery.trim().toLowerCase();
    const rawList =
      isActive() && !workspace.bootstrappingSessions
        ? workspace.sessions
        : props.cachedSessions;
    const list = rawList.filter((s) => !s.parentID);
    const sorted = sortSessionsByActivity(list);
    if (!q) return sorted;
    return sorted.filter(
      (s) =>
        (s.title || "").toLowerCase().includes(q) ||
        (s.slug || "").toLowerCase().includes(q)
    );
  });

  const activeSessions = createMemo(() =>
    allFilteredSessions().filter((s) => !isSessionArchived(s))
  );

  const archivedSessions = createMemo(() =>
    allFilteredSessions().filter((s) => isSessionArchived(s))
  );
  const history = useSessionHistory(activeSessions, () => props.proj.path,
    () => props.pendingSessions, () => props.filterQuery);

  const inspectWorkspaceFolder = async (folderPath: string) => {
    if (!isActive()) {
      await switchWorkspace(props.proj.path);
    }
    setWorkspace("activeInspectorFolder", folderPath);
    setWorkspace("inspectorTab", "files");
    setWorkspace("inspectorOpen", true);
  };

  return (
    <div data-project-item={props.proj.path} class="relative">
      <Show when={props.isDropBefore}>
        <div class="h-0.5 bg-[var(--accent-primary)] rounded-full mx-1.5 my-0.5" />
      </Show>

      {/* Project Header Row */}
      <div
        data-project-header={props.proj.path}
        onPointerDown={(ev) => props.onPointerDown(ev, props.proj.path)}
        onClick={() => props.onToggleProject(props.proj.path)}
        title={
          props.proj.folders.length > 1
            ? props.proj.folders.map((f) => shortenPath(f.path)).join("\n")
            : shortenPath(props.proj.path)
        }
        class={`group/proj flex items-center justify-between gap-1.5 rounded-lg px-2.5 py-1.5 cursor-pointer active:cursor-grabbing transition-colors ${
          props.isDraggingThis
            ? "opacity-55 bg-[var(--accent-surface)] ring-1 ring-[var(--accent-border)]"
            : "hover:bg-[var(--bg-hover)]/70"
        }`}
      >
        <div class="flex items-center gap-1.5 min-w-0 flex-1">
          <span class="shrink-0 text-[var(--text-faint)] group-hover/proj:text-[var(--text-muted)] transition-colors inline-flex items-center">
            <Show
              when={props.expanded}
              fallback={<Icon name="chevron-right" size="small" class="w-3.5 h-3.5" />}
            >
              <Icon name="chevron-down" size="small" class="w-3.5 h-3.5" />
            </Show>
          </span>

          <FolderIcon
            open={props.expanded || isActive()}
            color={props.proj.color}
            class="w-3.5 h-3.5 shrink-0"
          />

          <span
            class={`truncate text-[13px] ${
              isActive()
                ? "font-semibold text-[var(--text-primary)]"
                : "font-medium text-[var(--text-secondary)] group-hover/proj:text-[var(--text-primary)]"
            }`}
          >
            {props.proj.name}
          </span>

          <Show when={props.proj.folders.length > 1}>
            <span
              class="shrink-0 rounded px-1.5 py-0.2 text-[11px] font-mono bg-[var(--bg-inset)] text-[var(--text-muted)]"
              title={t("leftNav.folderCountBadge", {
                count: props.proj.folders.length,
              })}
            >
              {props.proj.folders.length}
            </span>
          </Show>
        </div>

        <div class="flex items-center gap-0.5 shrink-0">
          <span
            class="inline-flex h-5 w-5 shrink-0 items-center justify-center rounded text-[var(--text-faint)] hover:text-[var(--text-muted)] opacity-0 group-hover/proj:opacity-100 transition-opacity cursor-grab active:cursor-grabbing"
            title={t("leftNav.dragToReorder")}
          >
            <GripVertical size={13} class="block shrink-0" />
          </span>

          <WorkspaceActionMenu
            dirOrWorkspaceID={props.proj.workspaceId || props.proj.path}
          />

          <button
            type="button"
            onClick={(e) => props.onCreateThread(e, props.proj.path)}
            disabled={props.isCreating}
            class="inline-flex h-5 w-5 shrink-0 items-center justify-center rounded text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] opacity-0 group-hover/proj:opacity-100 transition-opacity cursor-pointer"
            title={t("leftNav.newThread")}
          >
            <Show
              when={!props.isCreating}
              fallback={<Spinner class="w-3.5 h-3.5 text-[var(--accent-primary)]" />}
            >
              <Plus size={13} class="block shrink-0" />
            </Show>
          </button>
        </div>
      </div>

      {/* Expanded Workspace Folders & Sessions */}
      <Show when={props.expanded}>
        <div class="ml-3.5 pl-2 border-l border-[var(--border-subtle)]/60 my-1 space-y-1">
          {/* Multi-Folder Workspace Directory List */}
          <Show when={props.proj.folders.length > 1}>
            <div class="pb-1 mb-0.5 border-b border-[var(--border-subtle)]/40 space-y-0.5">
              <For each={props.proj.folders}>
                {(folder) => (
                  <button
                    type="button"
                    onClick={() => void inspectWorkspaceFolder(folder.path)}
                    title={folder.path}
                    class="w-full flex items-center justify-between gap-1.5 rounded-md px-2 py-1 text-[12px] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/50 transition-colors cursor-pointer"
                  >
                    <div class="flex items-center gap-1.5 min-w-0">
                      <FolderIcon
                        open={folder.isPrimary}
                        color={props.proj.color}
                        class="w-3 h-3 shrink-0"
                      />
                      <span class="truncate font-mono text-[12px]">
                        {basename(folder.path)}
                      </span>
                    </div>
                    <Show when={folder.isPrimary}>
                      <span class="shrink-0 text-[11px] px-1.5 rounded bg-[var(--bg-inset)] text-[var(--text-faint)]">
                        {t("leftNav.primaryBadge")}
                      </span>
                    </Show>
                  </button>
                )}
              </For>
            </div>
          </Show>

          <Show when={props.isLoadingSessions}>
            <div class="flex items-center gap-1.5 px-2 py-1 text-[12px] text-[var(--text-faint)]">
              <Spinner class="w-3 h-3" />
              <span>{t("common.loading")}</span>
            </div>
          </Show>
          <Show when={!props.isLoadingSessions && activeSessions().length === 0}>
            <div class="px-2 py-1.5 text-[12px] text-[var(--text-faint)] italic">
              {t("leftNav.noThreads")}
            </div>
          </Show>

          <Show when={!props.isLoadingSessions && activeSessions().length > 0}>
            <SessionHistoryList history={history}>
              {(session) => (
                <SessionNavItem
                  session={session}
                  active={isSelectedSession(session.id)}
                  canDelete={true}
                  isArchived={false}
                  pendingSessions={props.pendingSessions}
                  onSelect={() => props.onSelectSession(props.proj.path, session.id)}
                  onArchive={(ev, id) =>
                    props.onArchiveSession(props.proj.path, ev, id)
                  }
                  onDiscard={(ev, id) =>
                    props.onDiscardSession(props.proj.path, ev, id)
                  }
                />
              )}
            </SessionHistoryList>
          </Show>

          {/* Collapsible Archived Section for this project */}
          <Show when={!props.isLoadingSessions && archivedSessions().length > 0}>
            <div class="pt-0.5">
              <button
                type="button"
                onClick={(ev) => {
                  ev.stopPropagation();
                  props.onToggleArchived(props.proj.path);
                }}
                class="w-full flex items-center justify-between gap-1.5 rounded-lg px-2 py-1 text-[12px] text-[var(--text-faint)] hover:text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]/40 transition-colors cursor-pointer"
              >
                <div class="flex items-center gap-1">
                  <Show
                    when={props.isArchivedOpen}
                    fallback={
                      <Icon name="chevron-right" size="small" class="w-3 h-3" />
                    }
                  >
                    <Icon name="chevron-down" size="small" class="w-3 h-3" />
                  </Show>
                  <Icon name="archive" size="small" class="w-3 h-3" />
                  <span>{t("leftNav.archivedSessions")}</span>
                </div>
                <span class="text-[11px] font-mono">
                  {archivedSessions().length}
                </span>
              </button>

              <Show when={props.isArchivedOpen}>
                <div class="mt-1 space-y-1">
                  <For each={archivedSessions()}>
                    {(session) => (
                      <SessionNavItem
                        session={session}
                        active={isSelectedSession(session.id)}
                        canDelete={true}
                        isArchived={true}
                        pendingSessions={props.pendingSessions}
                        onSelect={() =>
                          props.onSelectSession(props.proj.path, session.id)
                        }
                        onUnarchive={(ev, id) =>
                          props.onUnarchiveSession(props.proj.path, ev, id)
                        }
                        onDiscard={(ev, id) =>
                          props.onDiscardSession(props.proj.path, ev, id)
                        }
                      />
                    )}
                  </For>
                </div>
              </Show>
            </div>
          </Show>
        </div>
      </Show>

      <Show when={props.isDropAfter}>
        <div class="h-0.5 bg-[var(--accent-primary)] rounded-full mx-1.5 my-0.5" />
      </Show>
    </div>
  );
}
