// web/src/components/layout/LeftNav.tsx
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { User } from "lucide-solid";
import {
  archiveSession,
  discardSession,
  newIsolatedSession,
  openCreateWorkspaceModal,
  reorderProjects,
  selectSession,
  setProjectOrder,
  setSidebarProjectMode,
  setWorkspace,
  switchWorkspace,
  unarchiveSession,
  workspace,
} from "../../store/workspace";
import { hoxiAPI } from "../../services/api";
import type { SessionInfo } from "../../services/sse-types";
import { Icon } from "../icons";
import { buildAllProjects } from "./left-nav-helpers";
import MultiProjectTreeItem from "./MultiProjectItem";
import SingleProjectNavView from "./SingleProjectNavView";
import { t } from "../../i18n";

export default function LeftNav() {
  const [filterQuery, setFilterQuery] = createSignal("");
  const [showSearch, setShowSearch] = createSignal(false);
  const [creatingForPath, setCreatingForPath] = createSignal<string | null>(null);

  // Drag-and-drop state for reordering projects
  const [draggingProjectPath, setDraggingProjectPath] = createSignal<string | null>(null);
  const [dropTarget, setDropTarget] = createSignal<{
    path: string;
    position: "before" | "after";
  } | null>(null);
  let projectListContainerRef: HTMLDivElement | undefined;
  let suppressNextProjectClick = false;

  // Expanded projects set (by path) for Multi-Project mode
  const [expandedProjects, setExpandedProjects] = createSignal<Set<string>>(
    new Set([workspace.rootDir])
  );

  // Expanded archived sections per project (by path) for Multi-Project mode
  const [expandedArchivedByProject, setExpandedArchivedByProject] = createSignal<
    Record<string, boolean>
  >({});

  // Cached sessions for inactive projects: Record<path, SessionInfo[]>
  const [projectSessions, setProjectSessions] = createSignal<Record<string, SessionInfo[]>>({});
  const [loadingProjects, setLoadingProjects] = createSignal<Record<string, boolean>>({});

  createEffect(() => {
    if (workspace.rootDir) {
      setExpandedProjects((prev) => new Set([...prev, workspace.rootDir]));
    }
  });

  const allProjects = createMemo(() => buildAllProjects());

  // Ensure newly discovered projects are recorded into projectOrder so switching projects never scrambles order
  createEffect(() => {
    const current = allProjects().map((p) => p.path);
    if (current.length === 0) return;
    const saved = workspace.projectOrder;
    const missing = current.filter((p) => !saved.includes(p));
    if (missing.length > 0) {
      if (saved.length === 0 && workspace.rootDir && current.includes(workspace.rootDir)) {
        const rest = current.filter((p) => p !== workspace.rootDir);
        setProjectOrder([workspace.rootDir, ...rest]);
      } else {
        setProjectOrder([...saved, ...missing]);
      }
    }
  });

  const projectList = createMemo(() => {
    const all = allProjects();
    const q = filterQuery().trim().toLowerCase();
    if (!q) return all;
    return all.filter(
      (item) =>
        item.name.toLowerCase().includes(q) ||
        item.path.toLowerCase().includes(q) ||
        item.folders.some((f) => f.path.toLowerCase().includes(q))
    );
  });

  const pendingSessions = createMemo(
    () => new Set(workspace.pendingPermissions.map((p) => p.sessionID))
  );

  const isExpanded = (path: string) => expandedProjects().has(path);

  const loadSessionsForProject = async (path: string) => {
    setLoadingProjects((prev) => ({ ...prev, [path]: true }));
    try {
      const list = await hoxiAPI.listSessions(path);
      setProjectSessions((prev) => ({ ...prev, [path]: list }));
    } catch (err) {
      console.error(`Failed to load sessions for ${path}:`, err);
    } finally {
      setLoadingProjects((prev) => ({ ...prev, [path]: false }));
    }
  };

  const toggleProject = async (path: string) => {
    if (suppressNextProjectClick) {
      suppressNextProjectClick = false;
      return;
    }
    const next = new Set(expandedProjects());
    if (next.has(path)) {
      next.delete(path);
      setExpandedProjects(next);
    } else {
      next.add(path);
      setExpandedProjects(next);
      if (path !== workspace.rootDir && !projectSessions()[path]) {
        await loadSessionsForProject(path);
      }
    }
  };

  const onProjectPointerDown = (ev: PointerEvent, fromPath: string) => {
    if (ev.button !== 0) return;
    const targetEl = ev.target as HTMLElement | null;
    if (targetEl?.closest("button")) return;

    const startY = ev.clientY;
    let isDragging = false;

    const handlePointerMove = (moveEv: PointerEvent) => {
      if (!isDragging) {
        if (Math.abs(moveEv.clientY - startY) < 5) return;
        isDragging = true;
        setDraggingProjectPath(fromPath);
      }

      if (!projectListContainerRef) return;
      const items = Array.from(
        projectListContainerRef.querySelectorAll<HTMLElement>("[data-project-item]")
      );
      if (items.length === 0) return;

      let matchedPath: string | null = null;
      let matchedPos: "before" | "after" = "after";

      for (const itemEl of items) {
        const itemPath = itemEl.getAttribute("data-project-item");
        if (!itemPath) continue;
        const headerEl =
          itemEl.querySelector<HTMLElement>("[data-project-header]") || itemEl;
        const rect = headerEl.getBoundingClientRect();
        const itemRect = itemEl.getBoundingClientRect();

        if (moveEv.clientY <= itemRect.bottom) {
          matchedPath = itemPath;
          const midY = rect.top + rect.height / 2;
          matchedPos = moveEv.clientY < midY ? "before" : "after";
          break;
        }
      }

      if (!matchedPath) {
        const lastEl = items[items.length - 1];
        matchedPath = lastEl.getAttribute("data-project-item");
        matchedPos = "after";
      }

      if (matchedPath && matchedPath !== fromPath) {
        setDropTarget({ path: matchedPath, position: matchedPos });
      } else {
        setDropTarget(null);
      }
    };

    const handlePointerUp = () => {
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", handlePointerUp);
      window.removeEventListener("pointercancel", handlePointerUp);

      if (isDragging) {
        suppressNextProjectClick = true;
        setTimeout(() => {
          suppressNextProjectClick = false;
        }, 80);

        const target = dropTarget();
        if (target && target.path !== fromPath) {
          reorderProjects(
            allProjects().map((p) => p.path),
            fromPath,
            target.path,
            target.position
          );
        }
        setDraggingProjectPath(null);
        setDropTarget(null);
      }
    };

    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", handlePointerUp);
    window.addEventListener("pointercancel", handlePointerUp);
  };

  const toggleArchivedForProject = (path: string) => {
    setExpandedArchivedByProject((prev) => ({
      ...prev,
      [path]: !prev[path],
    }));
  };

  const onCreateThreadInProject = async (e: MouseEvent, path: string) => {
    e.stopPropagation();
    if (creatingForPath()) return;
    setCreatingForPath(path);
    try {
      if (path !== workspace.rootDir) {
        await switchWorkspace(path);
      }
      setExpandedProjects((prev) => new Set([...prev, path]));
      await newIsolatedSession();
    } catch (err) {
      console.error("Failed to create thread in project:", err);
    } finally {
      setCreatingForPath(null);
    }
  };

  const onSelectSessionInProject = async (path: string, sessionID: string) => {
    if (path !== workspace.rootDir) {
      await switchWorkspace(path);
    }
    await selectSession(sessionID);
  };

  const onArchiveInProject = async (
    projPath: string,
    ev: MouseEvent,
    sessionID: string
  ) => {
    ev.stopPropagation();
    const now = Date.now();
    if (projPath !== workspace.rootDir) {
      setProjectSessions((prev) => ({
        ...prev,
        [projPath]: (prev[projPath] || []).map((s) =>
          s.id === sessionID ? { ...s, time: { ...s.time, archived: now } } : s
        ),
      }));
    }
    await archiveSession(sessionID, projPath);
  };

  const onUnarchiveInProject = async (
    projPath: string,
    ev: MouseEvent,
    sessionID: string
  ) => {
    ev.stopPropagation();
    if (projPath !== workspace.rootDir) {
      setProjectSessions((prev) => ({
        ...prev,
        [projPath]: (prev[projPath] || []).map((s) => {
          if (s.id !== sessionID) return s;
          const nextTime = { ...s.time };
          delete nextTime.archived;
          return { ...s, time: nextTime };
        }),
      }));
    }
    await unarchiveSession(sessionID, projPath);
  };

  const onDiscardInProject = async (
    projPath: string,
    ev: MouseEvent,
    sessionID: string
  ) => {
    ev.stopPropagation();
    if (projPath !== workspace.rootDir) {
      setProjectSessions((prev) => ({
        ...prev,
        [projPath]: (prev[projPath] || []).filter((s) => s.id !== sessionID),
      }));
    }
    await discardSession(sessionID, true, projPath);
  };

  const currentUsername = createMemo(() => {
    const parts = (workspace.rootDir || "").split("/").filter(Boolean);
    if (parts[0] === "Users" && parts[1]) return parts[1];
    return "User";
  });

  return (
    <aside class="relative h-full w-full flex flex-col bg-transparent pl-2 pr-1 select-none">
      {/* Minimal Header: Mode Toggle + Search + New Workspace */}
      <div class="px-2 pt-1 pb-1.5 flex items-center justify-between gap-1">
        <div
          class="inline-flex items-center rounded-lg bg-[var(--bg-inset)] p-0.5"
          title={t("leftNav.switchSidebarMode")}
        >
          <button
            type="button"
            onClick={() => setSidebarProjectMode("multi")}
            class={`inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-medium transition-colors cursor-pointer ${
              workspace.sidebarProjectMode === "multi"
                ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            }`}
          >
            <Icon name="layers" size="small" class="w-3 h-3" />
            <span>{t("leftNav.multiProject")}</span>
          </button>

          <button
            type="button"
            onClick={() => setSidebarProjectMode("single")}
            class={`inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-medium transition-colors cursor-pointer ${
              workspace.sidebarProjectMode === "single"
                ? "bg-[var(--bg-panel)] text-[var(--text-primary)] shadow-2xs"
                : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            }`}
          >
            <Icon name="folder" size="small" class="w-3 h-3" />
            <span>{t("leftNav.singleProject")}</span>
          </button>
        </div>

        <div class="flex items-center gap-0.5">
          <button
            type="button"
            onClick={() => setShowSearch((v) => !v)}
            class={`p-1 rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer ${
              showSearch() ? "bg-[var(--bg-hover)] text-[var(--text-primary)]" : ""
            }`}
            title={t("leftNav.filterPlaceholder")}
          >
            <Icon name="magnifying-glass" size="small" />
          </button>
          <button
            type="button"
            onClick={() => openCreateWorkspaceModal()}
            class="p-1 rounded-md text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors cursor-pointer"
            title={t("leftNav.newWorkspace")}
          >
            <Icon name="folder-add-left" size="small" />
          </button>
        </div>
      </div>

      {/* Collapsible Search Filter */}
      <Show when={showSearch()}>
        <div class="px-2 pb-1.5">
          <div class="flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2 py-1 text-[11.5px]">
            <Icon name="magnifying-glass" size="small" class="text-[var(--text-muted)]" />
            <input
              type="text"
              placeholder={t("leftNav.filterPlaceholder")}
              value={filterQuery()}
              onInput={(e) => setFilterQuery(e.currentTarget.value)}
              class="w-full bg-transparent text-[11.5px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
            />
            <Show when={filterQuery()}>
              <button
                type="button"
                onClick={() => setFilterQuery("")}
                class="text-[var(--text-muted)] hover:text-[var(--text-primary)] cursor-pointer"
              >
                <Icon name="close-small" size="small" />
              </button>
            </Show>
          </div>
        </div>
      </Show>

      {/* MODE A: MULTI-PROJECT TREE */}
      <Show when={workspace.sidebarProjectMode === "multi"}>
        <div
          ref={projectListContainerRef}
          class="flex-1 min-h-0 overflow-y-auto px-1 py-1 space-y-0.5"
        >
          <For each={projectList()}>
            {(proj) => (
              <MultiProjectTreeItem
                proj={proj}
                expanded={isExpanded(proj.path)}
                isCreating={creatingForPath() === proj.path}
                isLoadingSessions={Boolean(loadingProjects()[proj.path])}
                isDraggingThis={draggingProjectPath() === proj.path}
                isDropBefore={
                  dropTarget()?.path === proj.path && dropTarget()?.position === "before"
                }
                isDropAfter={
                  dropTarget()?.path === proj.path && dropTarget()?.position === "after"
                }
                isArchivedOpen={
                  Boolean(expandedArchivedByProject()[proj.path]) ||
                  Boolean(filterQuery().trim())
                }
                filterQuery={filterQuery()}
                cachedSessions={projectSessions()[proj.path] || []}
                pendingSessions={pendingSessions()}
                onPointerDown={onProjectPointerDown}
                onToggleProject={(p) => void toggleProject(p)}
                onToggleArchived={toggleArchivedForProject}
                onCreateThread={(e, p) => void onCreateThreadInProject(e, p)}
                onSelectSession={(p, id) => void onSelectSessionInProject(p, id)}
                onArchiveSession={(p, ev, id) => void onArchiveInProject(p, ev, id)}
                onUnarchiveSession={(p, ev, id) => void onUnarchiveInProject(p, ev, id)}
                onDiscardSession={(p, ev, id) => onDiscardInProject(p, ev, id)}
              />
            )}
          </For>
        </div>
      </Show>

      {/* MODE B: SINGLE-PROJECT FOCUSED VIEW */}
      <Show when={workspace.sidebarProjectMode === "single"}>
        <SingleProjectNavView
          allProjects={allProjects()}
          filterQuery={filterQuery()}
          pendingSessions={pendingSessions()}
          onArchive={(ev, id) => void onArchiveInProject(workspace.rootDir, ev, id)}
          onUnarchive={(ev, id) => void onUnarchiveInProject(workspace.rootDir, ev, id)}
          onDiscard={(ev, id) => onDiscardInProject(workspace.rootDir, ev, id)}
        />
      </Show>

      {/* Kilo-style Bottom Bar: Left User Profile, Right Settings Icon */}
      <div class="mt-auto border-t border-[var(--border-subtle)]/50 pt-2 pb-2.5 px-2 flex items-center justify-between">
        <button
          type="button"
          onClick={() => setWorkspace("providerModalOpen", true)}
          class="flex items-center gap-2 rounded-lg px-1.5 py-1 text-left text-[12px] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer min-w-0"
          title={t("leftNav.accountAndProviders")}
        >
          <div class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-[var(--bg-inset)] text-[var(--text-secondary)]">
            <User size={13} />
          </div>
          <span class="truncate font-medium">
            {currentUsername()}
          </span>
        </button>

        <button
          type="button"
          onClick={() => setWorkspace("providerModalOpen", true)}
          class="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)]/60 transition-colors cursor-pointer shrink-0"
          title={t("leftNav.settingsAndProviders")}
        >
          <Icon name="settings-gear" size="small" />
        </button>
      </div>
    </aside>
  );
}
