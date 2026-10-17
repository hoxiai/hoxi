// web/src/components/layout/LeftNav.tsx
import { createEffect, createMemo, createSignal, For, onCleanup, Show } from "solid-js";
import { ArrowRightLeft } from "lucide-solid";
import {
  archiveSession,
  discardSession,
  newIsolatedSession,
  openCreateWorkspaceModal,
  openHandoffModal,
  reorderProjects,
  selectSession,
  setProjectOrder,
  setSidebarProjectMode,
  switchWorkspace,
  unarchiveSession,
  workspace,
} from "../../store/workspace";
import { hoxiAPI } from "../../services/api";
import type { SessionInfo } from "../../services/sse-types";
import { Icon, Spinner } from "../icons";
import { buildAllProjects, findProjectDropTarget } from "./left-nav-helpers";
import MultiProjectTreeItem from "./MultiProjectItem";
import SingleProjectNavView from "./SingleProjectNavView";
import UserAccountMenu from "./UserAccountMenu";
import { t } from "../../i18n";

export default function LeftNav() {
  const [filterQuery, setFilterQuery] = createSignal("");
  const [showSearch, setShowSearch] = createSignal(false);
  const [creatingForPath, setCreatingForPath] = createSignal<string | null>(null);
  const [creatingGlobal, setCreatingGlobal] = createSignal(false);

  // Global shortcut: Cmd+N (or Ctrl+N) creates a new thread
  createEffect(() => {
    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "n" && !e.shiftKey && !e.altKey) {
        const activeTag = (document.activeElement as HTMLElement)?.tagName?.toLowerCase();
        if (activeTag !== "input" && activeTag !== "textarea") {
          e.preventDefault();
          void onCreateGlobalNewThread();
        }
      }
    };
    window.addEventListener("keydown", handleGlobalKeyDown);
    onCleanup(() => window.removeEventListener("keydown", handleGlobalKeyDown));
  });

  const onCreateGlobalNewThread = async () => {
    if (creatingGlobal()) return;
    setCreatingGlobal(true);
    try {
      await newIsolatedSession();
    } catch (err) {
      console.error("Failed to create new thread:", err);
    } finally {
      setCreatingGlobal(false);
    }
  };

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
      setDropTarget(findProjectDropTarget(projectListContainerRef, moveEv.clientY, fromPath));
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

  return (
    <aside class="relative h-full w-full flex flex-col bg-transparent pl-2 pr-1 select-none">
      {/* Minimal Header: Mode Toggle + Search + New Workspace */}
      <div class="px-2 pt-1 pb-2 flex items-center justify-between gap-1">
        <div
          class="inline-flex items-center rounded-lg bg-[var(--bg-inset)] p-0.5"
          title={t("leftNav.switchSidebarMode")}
        >
          <button
            type="button"
            onClick={() => setSidebarProjectMode("multi")}
            class={`inline-flex items-center gap-1 rounded-md px-2.5 py-1 text-[12px] font-medium transition-colors cursor-pointer ${
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
            class={`inline-flex items-center gap-1 rounded-md px-2.5 py-1 text-[12px] font-medium transition-colors cursor-pointer ${
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
            onClick={() => openHandoffModal()}
            class="p-1 rounded-md text-[var(--accent-primary)] hover:bg-[var(--accent-surface)] transition-colors cursor-pointer"
            title={t("leftNav.handoffTooltip")}
          >
            <ArrowRightLeft size={14} />
          </button>
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
        <div class="px-2 pb-2">
          <div class="flex items-center gap-1.5 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] px-2.5 py-1.5 text-[12px]">
            <Icon name="magnifying-glass" size="small" class="text-[var(--text-muted)]" />
            <input
              type="text"
              placeholder={t("leftNav.filterPlaceholder")}
              value={filterQuery()}
              onInput={(e) => setFilterQuery(e.currentTarget.value)}
              class="w-full bg-transparent text-[12px] text-[var(--text-primary)] placeholder:text-[var(--text-faint)] outline-none"
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

      {/* Prominent New Thread Button above all projects */}
      <div class="px-2 pb-2 shrink-0">
        <button
          type="button"
          onClick={onCreateGlobalNewThread}
          disabled={creatingGlobal()}
          class="w-full flex items-center justify-between gap-2 px-3 py-2 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-inset)] hover:bg-[var(--bg-hover)] text-[var(--text-primary)] hover:border-[var(--accent-border)] transition-all cursor-pointer shadow-2xs group"
          title={t("leftNav.newThread")}
        >
          <div class="flex items-center gap-2 min-w-0">
            <Show
              when={!creatingGlobal()}
              fallback={<Spinner class="w-3.5 h-3.5 text-[var(--accent-primary)]" />}
            >
              <Icon name="new-session" size="small" class="text-[var(--accent-primary)]" />
            </Show>
            <span class="text-[13px] font-medium truncate">
              {t("leftNav.newThread")}
            </span>
          </div>
          <span class="text-[11.5px] font-mono text-[var(--text-faint)] group-hover:text-[var(--text-muted)]">
            ⌘N
          </span>
        </button>
      </div>

      {/* MODE A: MULTI-PROJECT TREE */}
      <Show when={workspace.sidebarProjectMode === "multi"}>
        <div
          ref={projectListContainerRef}
          class="flex-1 min-h-0 overflow-y-auto px-1 py-1 space-y-1"
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

      {/* Kilo-style Bottom Bar: Account Popover + Settings Gear */}
      <UserAccountMenu />
    </aside>
  );
}
